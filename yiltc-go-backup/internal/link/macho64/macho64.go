// Package macho64 implements a Mach-O 64-bit linker that produces minimal
// macOS executables for x86-64 and AArch64 (Apple Silicon) targets.
package macho64

import (
        "encoding/binary"
        "fmt"
        "sort"
        "strings"

        "github.com/yilt/yiltc/internal/link/types"
)

// =====================================================================
// Mach-O 64 constants
// =====================================================================

// Magic numbers.
const (
        MH_MAGIC_64  = 0xFEEDFACF
        MH_CIGAM_64  = 0xCFFAEDFE
)

// Header constants.
const (
        MH_NOUNDEFS  = 0x1
        MH_DYLDLINK  = 0x4
        MH_PIE       = 0x200000
        MH_TWOLEVEL  = 0x80
)

// File types.
const (
        MH_EXECUTE = 2
        MH_DYLIB   = 6
        MH_BUNDLE  = 8
)

// CPU types.
const (
        CPU_TYPE_X86_64 = 0x01000007
        CPU_TYPE_ARM64  = 0x0100000C
)

// CPU subtypes.
const (
        CPU_SUBTYPE_X86_64_ALL = 3
        CPU_SUBTYPE_ARM64_ALL  = 0
)

// Load command types.
const (
        LC_SEGMENT_64    = 0x19
        LC_UNIXTHREAD    = 0x5
        LC_MAIN          = 0x80000028
        LC_SYMTAB        = 0x2
        LC_DYSYMTAB      = 0xB
        LC_CODE_SIGNATURE = 0x1D
        LC_FUNCTION_STARTS = 0x26
        LC_DATA_IN_CODE   = 0x29
        LC_DYLD_INFO      = 0x22
        LC_DYLD_INFO_ONLY = 0x80000022
        LC_LOAD_DYLINKER  = 0xE
        LC_UUID           = 0x1B
        LC_BUILD_VERSION  = 0x32
)

// VM protections.
const (
        VM_PROT_READ    = 0x1
        VM_PROT_WRITE   = 0x2
        VM_PROT_EXECUTE = 0x4
)

// Symbol kinds (n_type).
const (
        N_UNDF  = 0x0
        N_ABS   = 0x2
        N_SECT  = 0xe
        N_EXT   = 0x01
        N_PEXT  = 0x10
        N_TYPE  = 0x0e
)

// Section types.
const (
        S_REGULAR                  = 0
        S_ZEROFILL                 = 1
        S_NON_LAZY_SYMBOL_POINTERS = 6
        S_MOD_INIT_FUNC_POINTERS   = 9
)

// Segment/section attribute flags.
const (
        S_ATTR_PURE_INSTRUCTIONS = 0x80000000
        S_ATTR_SOME_INSTRUCTIONS = 0x00000400
)

// Header sizes.
const (
        MachOHeaderSize64 = 32
        SegmentCmdSize64  = 72
        SectionSize64     = 80
        SymtabCmdSize     = 24
        DysymtabCmdSize   = 80
        EntryPointCmdSize = 16
        CodeSignatureCmdSize = 16
        UUIDCmdSize       = 24
        BuildVersionSize  = 16
        UnixThreadSize64  = 0 // variable
        ThreadStateSize64 = 168 // x86_64 thread state (full)
        ThreadStateARM64  = 264 // ARM64 thread state
)

// Default settings.
const (
        machoPageSize   = 0x1000
        machoTextBase   = 0x100000000 // default for PIE (Mach-O slides from 0)
        machoTextBaseNonPIE = 0x100000 // non-PIE base address
        machoStackBase  = 0x7fff5fc00000 // stack top
        machoStackSize  = 0x100000       // stack size
)

// =====================================================================
// Internal types
// =====================================================================

// machoSection represents a section within a segment.
type machoSection struct {
        segName    string // segment name (__TEXT, __DATA, etc.)
        sectName   string // section name (__text, __rodata, etc.)
        data       []byte
        flags      uint32
        align      uint32
        // computed
        addr       uint64
        offset     uint64
        size       uint64
}

// machoSymbol represents a linker symbol.
type machoSymbol struct {
        name   string
        addr   uint64
        size   uint64
        kind   types.SymbolKind
        section string
}

// machoReloc represents a relocation entry.
type machoReloc struct {
        section string
        offset  uint64
        sym     string
        relType types.RelocType
        addend  int64
}

// =====================================================================
// Linker
// =====================================================================

// L implements types.Linker for Mach-O 64 targets.
type L struct {
        arch   string // "x86_64" or "aarch64"
        bo     binary.ByteOrder
        pie    bool

        entry    string
        sections []*machoSection
        symbols  []*machoSymbol
        relocs   []*machoReloc

        uuid [16]byte
}

// New creates a new Mach-O64 linker for the given architecture.
func New(arch string) *L {
        return &L{
                arch: arch,
                bo:   binary.LittleEndian,
                pie:  true, // macOS defaults to PIE
        }
}

// ---------- types.Linker interface ----------

// AddCode adds an executable code section.
func (l *L) AddCode(name string, data []byte) {
        if name == "" {
                name = "__text"
        }
        l.sections = append(l.sections, &machoSection{
                segName: "__TEXT",
                sectName: name,
                data:    append([]byte(nil), data...),
                flags:   S_ATTR_PURE_INSTRUCTIONS | S_REGULAR,
                align:   16,
        })
}

// AddData adds a read-only data section.
func (l *L) AddData(name string, data []byte) {
        if name == "" {
                name = "__rodata"
        }
        l.sections = append(l.sections, &machoSection{
                segName: "__TEXT",
                sectName: name,
                data:    append([]byte(nil), data...),
                flags:   S_REGULAR,
                align:   16,
        })
}

// AddRWData adds a read-write initialised data section.
func (l *L) AddRWData(name string, data []byte) {
        if name == "" {
                name = "__data"
        }
        l.sections = append(l.sections, &machoSection{
                segName: "__DATA",
                sectName: name,
                data:    append([]byte(nil), data...),
                flags:   S_REGULAR,
                align:   16,
        })
}

// AddBSS reserves zero-initialised bytes.
func (l *L) AddBSS(name string, size uint64, align uint32) {
        if name == "" {
                name = "__bss"
        }
        if align == 0 {
                align = 16
        }
        l.sections = append(l.sections, &machoSection{
                segName: "__DATA",
                sectName: name,
                data:    nil,
                flags:   S_ZEROFILL,
                align:   align,
                size:    size,
        })
}

// AddSymbol registers a symbol.
func (l *L) AddSymbol(name string, addr uint64, size uint64, kind types.SymbolKind) {
        l.symbols = append(l.symbols, &machoSymbol{
                name:    name,
                addr:    addr,
                size:    size,
                kind:    kind,
        })
}

// AddRelocation records a relocation.
func (l *L) AddRelocation(section string, offset uint64, sym string, relType types.RelocType, addend int64) {
        l.relocs = append(l.relocs, &machoReloc{
                section: section,
                offset:  offset,
                sym:     sym,
                relType: relType,
                addend:  addend,
        })
}

// SetEntryPoint declares the entry-point symbol name.
func (l *L) SetEntryPoint(name string) {
        l.entry = name
}

// SetCustomSection adds a raw section with arbitrary flags.
func (l *L) SetCustomSection(name string, data []byte, flags uint32) {
        segName := "__TEXT"
        parts := strings.SplitN(name, ",", 2)
        if len(parts) == 2 {
                segName = parts[0]
                name = parts[1]
        }
        l.sections = append(l.sections, &machoSection{
                segName: segName,
                sectName: name,
                data:    append([]byte(nil), data...),
                flags:   uint32(flags),
                align:   16,
        })
}

// SetPIE controls whether the output is position-independent.
func (l *L) SetPIE(pie bool) {
        l.pie = pie
}

// =====================================================================
// Link – main entry point
// =====================================================================

// Link produces the final Mach-O 64-bit binary.
func (l *L) Link() ([]byte, error) {
        if len(l.sections) == 0 {
                return nil, fmt.Errorf("macho64: no sections to link")
        }

        l.ensureDefaults()

        if err := l.layout(); err != nil {
                return nil, fmt.Errorf("macho64: layout: %w", err)
        }

        if err := l.applyRelocations(); err != nil {
                return nil, fmt.Errorf("macho64: relocations: %w", err)
        }

        return l.emit()
}

// ---------- defaults ----------

func (l *L) ensureDefaults() {
        has := make(map[string]bool)
        for _, s := range l.sections {
                has[s.segName+","+s.sectName] = true
        }
        if !has["__TEXT,__text"] {
                l.AddCode("__text", nil)
        }
        if !has["__TEXT,__rodata"] {
                l.AddData("__rodata", nil)
        }
        if !has["__DATA,__data"] {
                l.AddRWData("__data", nil)
        }
        if !has["__DATA,__bss"] {
                l.AddBSS("__bss", 0, 16)
        }
}

// =====================================================================
// Layout
// =====================================================================

func (l *L) layout() error {
        base := machoTextBase
        if !l.pie {
                base = machoTextBaseNonPIE
        }

        // Classify sections by segment.
        textSections := l.filterSections("__TEXT")
        dataSections := l.filterSections("__DATA")

        // __TEXT segment starts right after headers (page-aligned in vm).
        // We need to know the header size first.
        // Load commands: 1 LC_SEGMENT_64(__TEXT) + 1 LC_SEGMENT_64(__DATA) +
        //                1 LC_MAIN + 1 LC_SYMTAB + 1 LC_DYSYMTAB + 1 LC_CODE_SIGNATURE
        textSegmentCmdSize := SegmentCmdSize64 + uint32(len(textSections))*SectionSize64
        dataSegmentCmdSize := SegmentCmdSize64 + uint32(len(dataSections))*SectionSize64

        headerSize := uint64(MachOHeaderSize64)
        loadCmdSize := uint64(textSegmentCmdSize) + uint64(dataSegmentCmdSize) +
                EntryPointCmdSize + SymtabCmdSize + DysymtabCmdSize + CodeSignatureCmdSize

        // Headers are part of __TEXT segment.
        textFileStart := alignUp(headerSize+loadCmdSize, 8)
        textVaddr := alignUp(uint64(base)+headerSize+loadCmdSize, machoPageSize)

        // Assign offsets within __TEXT.
        fileOff := textFileStart
        vaddr := textVaddr
        for _, s := range textSections {
                va := alignUp(vaddr, uint64(s.align))
                fo := textFileStart + (va - textVaddr)
                s.addr = va
                s.offset = fo
                if s.data != nil {
                        s.size = uint64(len(s.data))
                        fo += s.size
                }
                vaddr = va + s.size
        }

        textFileEnd := fileOff + (vaddr - textVaddr) // not exactly right but close enough
        // Actually compute properly.
        if vaddr > textVaddr {
                textFileEnd = textFileStart + (vaddr - textVaddr)
        }

        // __DATA segment starts on the next page boundary.
        dataVaddrStart := alignUp(vaddr, machoPageSize)
        dataFileStart := alignUp(textFileEnd, 8)

        vaddr = dataVaddrStart
        fileOff = dataFileStart
        for _, s := range dataSections {
                va := alignUp(vaddr, uint64(s.align))
                fo := dataFileStart + (va - dataVaddrStart)
                s.addr = va
                if s.data != nil {
                        s.offset = fo
                        s.size = uint64(len(s.data))
                        fo += s.size
                } else {
                        // BSS: no file offset
                        s.offset = 0
                }
                vaddr = va + s.size
        }

        return nil
}

// =====================================================================
// Relocations
// =====================================================================

func (l *L) applyRelocations() error {
        secMap := make(map[string]*machoSection)
        for _, s := range l.sections {
                secMap[s.sectName] = s
        }

        symMap := make(map[string]*machoSymbol)
        for _, sym := range l.symbols {
                symMap[sym.name] = sym
        }

        for _, r := range l.relocs {
                sec, ok := secMap[r.section]
                if !ok {
                        return fmt.Errorf("macho64: relocation targets unknown section %q", r.section)
                }
                if sec.data == nil {
                        return fmt.Errorf("macho64: cannot relocate BSS section %q", r.section)
                }

                sym, ok := symMap[r.sym]
                if !ok {
                        return fmt.Errorf("macho64: relocation references unknown symbol %q", r.sym)
                }

                P := sec.addr + r.offset
                S := sym.addr
                off := int(r.offset)

                switch l.arch {
                case "x86_64":
                        if err := l.relocX86_64(sec.data, off, S, P, r.relType, r.addend); err != nil {
                                return err
                        }
                case "aarch64":
                        if err := l.relocAArch64(sec.data, off, S, P, r.relType, r.addend); err != nil {
                                return err
                        }
                default:
                        return fmt.Errorf("macho64: unsupported arch %q", l.arch)
                }
        }
        return nil
}

func (l *L) relocX86_64(data []byte, off int, S, P uint64, rt types.RelocType, addend int64) error {
        bo := l.bo
        A := uint64(addend)

        switch rt {
        case types.RelocAbs64:
                if off+8 > len(data) { return fmt.Errorf("reloc out of bounds") }
                bo.PutUint64(data[off:], S+A)
        case types.RelocAbs32:
                if off+4 > len(data) { return fmt.Errorf("reloc out of bounds") }
                bo.PutUint32(data[off:], uint32(S+A))
        case types.RelocPC32, types.RelocPLT32:
                if off+4 > len(data) { return fmt.Errorf("reloc out of bounds") }
                bo.PutUint32(data[off:], uint32(int64(S+A-P)))
        default:
                return fmt.Errorf("unsupported x86_64 macho reloc type %d", rt)
        }
        return nil
}

func (l *L) relocAArch64(data []byte, off int, S, P uint64, rt types.RelocType, addend int64) error {
        bo := l.bo
        A := uint64(addend)

        switch rt {
        case types.RelocCall26:
                displacement := int64(S + A - P)
                if displacement&3 != 0 {
                        return fmt.Errorf("ARM64 B relocation: target not 4-byte aligned")
                }
                imm26 := displacement >> 2
                if imm26 < -0x2000000 || imm26 > 0x1ffffff {
                        return fmt.Errorf("ARM64 B relocation: out of range")
                }
                if off+4 > len(data) { return fmt.Errorf("reloc out of bounds") }
                inst := bo.Uint32(data[off:])
                inst = (inst &^ 0x03ffffff) | (uint32(imm26) & 0x03ffffff)
                bo.PutUint32(data[off:], inst)
        case types.RelocAbs64:
                if off+8 > len(data) { return fmt.Errorf("reloc out of bounds") }
                bo.PutUint64(data[off:], S+A)
        case types.RelocAbs32:
                if off+4 > len(data) { return fmt.Errorf("reloc out of bounds") }
                bo.PutUint32(data[off:], uint32(S+A))
        case types.RelocPC32:
                if off+4 > len(data) { return fmt.Errorf("reloc out of bounds") }
                bo.PutUint32(data[off:], uint32(int64(S+A-P)))
        default:
                return fmt.Errorf("unsupported aarch64 macho reloc type %d", rt)
        }
        return nil
}

// =====================================================================
// Emit
// =====================================================================

func (l *L) emit() ([]byte, error) {
        bo := l.bo

        textSections := l.filterSections("__TEXT")
        dataSections := l.filterSections("__DATA")

        // Find entry point.
        entryAddr := l.findEntryAddr()

        // Build symbol table.
        nList, strTab := l.buildSymbolTable(textSections, dataSections)
        indirectSymTab := l.buildIndirectSymbolTable()

        // Compute __TEXT segment file/vm range.
        textFirst, textLast := l.segmentBounds(textSections)
        textFileStart := textFirst.offset
        textFileEnd := textLast.offset + textLast.size
        textVaddrStart := textFirst.addr
        textVaddrEnd := textLast.addr + textLast.size

        // Compute __DATA segment file/vm range.
        dataFirst, dataLast := l.segmentBounds(dataSections)
        dataFileStart := dataFirst.offset
        dataFileEnd := dataLast.offset + dataLast.size
        dataVaddrStart := dataFirst.addr
        dataVaddrEnd := dataLast.addr + dataLast.size

        // If data segment is empty, keep values minimal.
        if len(dataSections) == 0 {
                dataVaddrStart = textVaddrEnd
                dataVaddrEnd = textVaddrEnd
                dataFileStart = textFileEnd
                dataFileEnd = textFileEnd
        }

        // String table offset (starts after all section data).
        strTabOff := alignUp(max64(textFileEnd, dataFileEnd), 8)
        nListOff := strTabOff + uint64(len(strTab))
        indirectSymOff := nListOff + uint64(len(nList))
        // Align code signature to 16 bytes.
        codeSigOff := alignUp(indirectSymOff+uint64(len(indirectSymTab)), 16)
        totalSize := codeSigOff + 16 // placeholder code signature

        buf := make([]byte, totalSize)

        // ---- Mach-O Header ----
        // Offsets: magic(0) cputype(4) cpusubtype(8) filetype(12)
        //          ncmds(16) sizeofcmds(20) flags(24) reserved(28)
        machoFlags := uint32(MH_NOUNDEFS | MH_DYLDLINK | MH_TWOLEVEL)
        if l.pie {
                machoFlags |= MH_PIE
        }

        bo.PutUint32(buf[0:], MH_MAGIC_64)          // magic
        bo.PutUint32(buf[4:], l.cpuType())           // cputype
        bo.PutUint32(buf[8:], l.cpuSubtype())        // cpusubtype
        bo.PutUint32(buf[12:], MH_EXECUTE)           // filetype
        bo.PutUint32(buf[16:], uint32(6))            // ncmds
        bo.PutUint32(buf[20:], 0)                    // sizeofcmds (filled below)
        bo.PutUint32(buf[24:], machoFlags)           // flags
        bo.PutUint32(buf[28:], 0)                    // reserved

        // Load commands offset.
        cmdOff := uint64(MachOHeaderSize64)

        // ---- LC_SEGMENT_64 (__TEXT) ----
        cmdOff = l.writeSegmentCmd(buf, cmdOff, "__TEXT",
                textVaddrStart, textVaddrEnd-textVaddrStart,
                textFileStart, textFileEnd-textFileStart,
                VM_PROT_READ|VM_PROT_EXECUTE, VM_PROT_READ|VM_PROT_EXECUTE,
                textSections)

        // ---- LC_SEGMENT_64 (__DATA) ----
        cmdOff = l.writeSegmentCmd(buf, cmdOff, "__DATA",
                dataVaddrStart, dataVaddrEnd-dataVaddrStart,
                dataFileStart, dataFileEnd-dataFileStart,
                VM_PROT_READ|VM_PROT_WRITE, VM_PROT_READ|VM_PROT_WRITE,
                dataSections)

        // ---- LC_MAIN ----
        stackSize := uint64(machoStackSize)
        bo.PutUint32(buf[cmdOff:], LC_MAIN)
        bo.PutUint32(buf[cmdOff+4:], EntryPointCmdSize)
        bo.PutUint64(buf[cmdOff+8:], entryAddr-textVaddrStart) // entryoff (relative to __TEXT file start)
        bo.PutUint64(buf[cmdOff+16:], stackSize)
        cmdOff += EntryPointCmdSize

        // ---- LC_SYMTAB ----
        bo.PutUint32(buf[cmdOff:], LC_SYMTAB)
        bo.PutUint32(buf[cmdOff+4:], SymtabCmdSize)
        bo.PutUint32(buf[cmdOff+8:], uint32(nListOff))
        bo.PutUint32(buf[cmdOff+12:], uint32(len(nList))/16) // nlist64 is 16 bytes
        bo.PutUint32(buf[cmdOff+16:], uint32(strTabOff))
        bo.PutUint32(buf[cmdOff+20:], uint32(len(strTab)))
        cmdOff += SymtabCmdSize

        // ---- LC_DYSYMTAB ----
        bo.PutUint32(buf[cmdOff:], LC_DYSYMTAB)
        bo.PutUint32(buf[cmdOff+4:], DysymtabCmdSize)
        // All fields zero: we have no dynamic symbols in a static binary.
        // ilocalsym, nlocalsym, iextdefsym, nextdefsym, iundefsym, nundefsym
        // tocoff, ntoc, modtaboff, nmodtab, extrefsymoff, nextrefsyms,
        // indirectsymoff, nindirectsyms, extreloff, nextrel, locreloff, nlocrel
        cmdOff += DysymtabCmdSize

        // ---- LC_CODE_SIGNATURE ----
        bo.PutUint32(buf[cmdOff:], LC_CODE_SIGNATURE)
        bo.PutUint32(buf[cmdOff+4:], CodeSignatureCmdSize)
        bo.PutUint32(buf[cmdOff+8:], uint32(codeSigOff))
        bo.PutUint32(buf[cmdOff+12:], 16) // size (minimal placeholder)
        cmdOff += CodeSignatureCmdSize

        // Update sizeofcmds in header.
        bo.PutUint32(buf[24:], uint32(cmdOff-MachOHeaderSize64))

        // ---- Write section data ----
        for _, s := range l.sections {
                if s.data != nil && s.offset > 0 && uint64(len(s.data)) > 0 {
                        copy(buf[s.offset:], s.data)
                }
        }

        // ---- Write symbol/string tables ----
        copy(buf[strTabOff:], strTab)
        copy(buf[nListOff:], nList)
        copy(buf[indirectSymOff:], indirectSymTab)

        // ---- Write code signature placeholder ----
        // Just zero bytes – the linker would normally leave signing to codesign.

        return buf, nil
}

// ---------- Write LC_SEGMENT_64 ----------

func (l *L) writeSegmentCmd(buf []byte, off uint64, segName string,
        vaddr, vmsize, fileOff, fileSize uint64,
        initProt, maxProt uint32, sections []*machoSection) uint64 {

        bo := l.bo
        cmdSize := uint32(SegmentCmdSize64 + len(sections)*SectionSize64)

        bo.PutUint32(buf[off:], LC_SEGMENT_64)
        bo.PutUint32(buf[off+4:], cmdSize)

        // segname (16 bytes, null-padded)
        copy(buf[off+8:], []byte(segName))
        for i := len(segName); i < 16; i++ {
                buf[off+8+uint64(i)] = 0
        }

        bo.PutUint64(buf[off+24:], vaddr)
        bo.PutUint64(buf[off+32:], vmsize)
        bo.PutUint64(buf[off+40:], fileOff)
        bo.PutUint64(buf[off+48:], fileSize)
        bo.PutUint32(buf[off+56:], maxProt)  // maxprot
        bo.PutUint32(buf[off+60:], initProt) // initprot
        bo.PutUint32(buf[off+64:], uint32(len(sections))) // nsects
        bo.PutUint32(buf[off+68:], 0) // flags

        // Section headers.
        secOff := off + SegmentCmdSize64
        for _, s := range sections {
                // sectname (16 bytes)
                copy(buf[secOff:], []byte(s.sectName))
                for i := len(s.sectName); i < 16; i++ {
                        buf[secOff+uint64(i)] = 0
                }
                // segname (16 bytes)
                copy(buf[secOff+16:], []byte(segName))
                for i := len(segName); i < 16; i++ {
                        buf[secOff+16+uint64(i)] = 0
                }

                bo.PutUint64(buf[secOff+32:], s.addr)     // addr
                bo.PutUint64(buf[secOff+40:], s.size)     // size
                bo.PutUint32(buf[secOff+48:], uint32(s.offset)) // offset (file offset)

                var secFlags uint32
                if s.data == nil {
                        secFlags = S_ZEROFILL
                } else {
                        secFlags = S_REGULAR
                }
                if s.flags&S_ATTR_PURE_INSTRUCTIONS != 0 {
                        secFlags |= S_ATTR_PURE_INSTRUCTIONS
                }
                if s.flags&S_ATTR_SOME_INSTRUCTIONS != 0 {
                        secFlags |= S_ATTR_SOME_INSTRUCTIONS
                }
                bo.PutUint32(buf[secOff+52:], 0) // align (we handle alignment externally)
                bo.PutUint32(buf[secOff+56:], 0) // reloff
                bo.PutUint32(buf[secOff+60:], 0) // nreloc
                bo.PutUint32(buf[secOff+64:], secFlags) // flags (type + attributes)

                bo.PutUint32(buf[secOff+68:], 0) // reserved1
                bo.PutUint32(buf[secOff+72:], 0) // reserved2
                bo.PutUint32(buf[secOff+76:], 0) // reserved3

                secOff += SectionSize64
        }

        return off + uint64(cmdSize)
}

// =====================================================================
// Symbol table
// =====================================================================

// nlist64 is 16 bytes: n_strx(4) n_type(1) n_sect(1) n_desc(2) n_value(8)
func (l *L) buildSymbolTable(textSects, dataSects []*machoSection) (nlist, strTab []byte) {
        // Build section index map (1-based: 0 = no section).
        secIdxMap := make(map[string]uint8)
        for i, s := range textSects {
                secIdxMap[s.sectName] = uint8(i + 1)
        }
        for i, s := range dataSects {
                secIdxMap[s.sectName] = uint8(len(textSects) + i + 1)
        }

        // Sort symbols: local first, then global.
        syms := make([]*machoSymbol, len(l.symbols))
        copy(syms, l.symbols)
        sort.SliceStable(syms, func(i, j int) bool {
                return syms[i].name < syms[j].name
        })

        // Build string table.
        var st []byte = []byte{0}
        stMap := make(map[string]uint32)

        putStr := func(s string) uint32 {
                if off, ok := stMap[s]; ok {
                        return off
                }
                off := uint32(len(st))
                st = append(st, []byte(s)...)
                st = append(st, 0)
                stMap[s] = off
                return off
        }

        // Null symbol first.
        var nl []byte = make([]byte, 16)

        for _, sym := range syms {
                if sym.name == "" {
                        continue
                }
                nameOff := putStr(sym.name)

                var ntype byte = N_SECT | N_EXT
                var nsect uint8 = 0

                // Determine which section this symbol belongs to.
                if idx, ok := secIdxMap[sym.section]; ok {
                        nsect = idx
                } else {
                        // Try to find by address.
                        allSects := append(textSects, dataSects...)
                        for i, s := range allSects {
                                if s.addr <= sym.addr && sym.addr < s.addr+s.size {
                                        nsect = uint8(i + 1)
                                        break
                                }
                        }
                }
                if nsect == 0 {
                        ntype = N_ABS | N_EXT
                }

                var entry [16]byte
                l.bo.PutUint32(entry[0:4], nameOff)
                entry[4] = ntype
                entry[5] = nsect
                l.bo.PutUint16(entry[6:8], 0) // n_desc
                l.bo.PutUint64(entry[8:16], sym.addr)
                nl = append(nl, entry[:]...)
        }

        return nl, st
}

// buildIndirectSymbolTable builds an empty indirect symbol table.
func (l *L) buildIndirectSymbolTable() []byte {
        // Empty – no lazy/non-lazy symbol pointers in a static binary.
        return nil
}

// =====================================================================
// Helpers
// =====================================================================

func (l *L) cpuType() uint32 {
        switch l.arch {
        case "aarch64":
                return CPU_TYPE_ARM64
        default:
                return CPU_TYPE_X86_64
        }
}

func (l *L) cpuSubtype() uint32 {
        switch l.arch {
        case "aarch64":
                return CPU_SUBTYPE_ARM64_ALL
        default:
                return CPU_SUBTYPE_X86_64_ALL
        }
}

func (l *L) findEntryAddr() uint64 {
        if l.entry == "" {
                // Default to first code section start.
                for _, s := range l.sections {
                        if s.segName == "__TEXT" && s.data != nil && len(s.data) > 0 {
                                return s.addr
                        }
                }
                return 0
        }
        for _, sym := range l.symbols {
                if sym.name == l.entry {
                        return sym.addr
                }
        }
        return 0
}

func (l *L) filterSections(segName string) []*machoSection {
        var out []*machoSection
        for _, s := range l.sections {
                if s.segName == segName {
                        out = append(out, s)
                }
        }
        return out
}

func (l *L) segmentBounds(sections []*machoSection) (first, last *machoSection) {
        if len(sections) == 0 {
                return &machoSection{}, &machoSection{}
        }
        f := sections[0]
        le := sections[len(sections)-1]
        return f, le
}

func alignUp(v, a uint64) uint64 {
        if a == 0 {
                return v
        }
        return (v + a - 1) &^ (a - 1)
}

func max64(a, b uint64) uint64 {
        if a > b {
                return a
        }
        return b
}
