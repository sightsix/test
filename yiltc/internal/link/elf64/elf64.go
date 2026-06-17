// Package elf64 implements an ELF64 linker that produces minimal Linux / Unix
// executables for x86-64, AArch64, and RISC-V 64 targets. It supports both
// static ET_EXEC and PIE (ET_DYN) output formats.
package elf64

import (
        "encoding/binary"
        "fmt"
        "math"
        "sort"
        "strings"

        "github.com/yilt/yiltc/internal/link/types"
)

// =====================================================================
// ELF64 constants
// =====================================================================

// ELF identification indices.
const (
        ELFMAG0 = 0
        ELFMAG1 = 1
        ELFMAG2 = 2
        ELFMAG3 = 3
        ELFCLASS = 4 // ELFCLASS64 = 2
        ELFDATA = 5  // ELFDATA2LSB = 1, ELFDATA2MSB = 2
        ELFVERSION = 6
        ELFOSABI = 7
)

// ELF class values.
const (
        ELFCLASS64 = 2
)

// Data encoding.
const (
        ELFDATA2LSB = 1
        ELFDATA2MSB = 2
)

// Object file types.
const (
        ET_EXEC = 2
        ET_DYN  = 3
)

// Machine types.
const (
        EM_X86_64  = 62
        EM_AARCH64 = 183
        EM_RISCV   = 243
)

// Program header types.
const (
        PT_NULL    = 0
        PT_LOAD    = 1
        PT_INTERP  = 3
        PT_GNU_STACK = 0x6474e551
)

// Program header flags.
const (
        PF_X = 0x1
        PF_W = 0x2
        PF_R = 0x4
)

// Section header types.
const (
        SHT_NULL     = 0
        SHT_PROGBITS = 1
        SHT_SYMTAB   = 2
        SHT_STRTAB   = 3
        SHT_NOBITS   = 8
)

// Section header flags.
const (
        SHF_WRITE     = 0x1
        SHF_ALLOC     = 0x2
        SHF_EXECINSTR = 0x4
)

// Symbol binding.
const (
        STB_LOCAL  = 0
        STB_GLOBAL = 1
        STB_WEAK   = 2
)

// Symbol types.
const (
        STT_NOTYPE  = 0
        STT_OBJECT  = 1
        STT_FUNC    = 2
        STT_SECTION = 3
        STT_FILE    = 4
)

// Special section indices.
const (
        SHN_UNDEF  = 0
        SHN_ABS    = 0xfff1
)

// x86_64 relocation types.
const (
        R_X86_64_NONE   = 0
        R_X86_64_64     = 1
        R_X86_64_PC32   = 2
        R_X86_64_PLT32  = 4
        R_X86_64_32     = 10
        R_X86_64_32S    = 11
)

// AArch64 relocation types.
const (
        R_AARCH64_ABS64  = 257
        R_AARCH64_CALL26 = 283
        R_AARCH64_ADR_PREL_PG_HI21 = 275
        R_AARCH64_ADD_ABS_LO12_NC   = 277
        R_AARCH64_LDST8_ABS_LO12_NC = 279
        R_AARCH64_LDST16_ABS_LO12_NC = 280
        R_AARCH64_LDST32_ABS_LO12_NC = 281
        R_AARCH64_LDST64_ABS_LO12_NC = 282
)

// RISC-V relocation types.
const (
        R_RISCV_NONE    = 0
        R_RISCV_32      = 1
        R_RISCV_64      = 2
        R_RISCV_CALL    = 18
        R_RISCV_CALL_PLT = 19
        R_RISCV_PCREL_HI20 = 23
        R_RISCV_PCREL_LO12_I = 24
        R_RISCV_PCREL_LO12_S = 25
        R_RISCV_HI20    = 26
        R_RISCV_LO12_I  = 27
        R_RISCV_LO12_S  = 28
        R_RISCV_JAL     = 29
)

// Default base address.
const (
        defaultBaseExec = 0x400000
        defaultBasePIE  = 0x0
        bareMetalBase   = 0x8000
        pageSize        = 0x1000
)

// =====================================================================
// Internal types
// =====================================================================

// section holds a single output section.
type section struct {
        name  string
        data  []byte  // raw bytes; nil for SHT_NOBITS (BSS)
        flags uint64
        align uint64
        size  uint64
        // output fields (set during layout)
        offset uint64
        addr   uint64
        index  int // section header index
}

// symbol holds a linker-internal symbol.
type symbol struct {
        name    string
        addr    uint64
        size    uint64
        kind    types.SymbolKind
        binding int // STB_LOCAL / STB_GLOBAL / STB_WEAK
        section string // which section this symbol belongs to
}

// relocation holds a single relocation entry.
type relocation struct {
        section string // target section name
        offset  uint64
        sym     string // target symbol name
        relType types.RelocType
        addend  int64
}

// =====================================================================
// Linker
// =====================================================================

// L implements types.Linker for ELF64 targets.
type L struct {
        arch     string // "x86_64", "aarch64", "riscv64"
        osName   string // "linux", "android", "none"
        pie      bool
        baseAddr uint64
        strip    bool // omit .symtab and .strtab from output

        entry      string
        sections   []*section
        symbols    []*symbol
        relocs     []*relocation

        // sectionAddr maps every original section name (including those
        // merged away) to its final virtual address.  Populated after layout.
        sectionAddr map[string]uint64
        // mergeMap tracks where each merged-away section ended up.
        // Key: original section name.  Value: (canonical section name, offset).
        // Populated during mergeSections.
        mergeMap map[string][2]uint64
        // canonMap maps merged-away section names to their canonical names.
        canonMap map[string]string

        byteOrder binary.ByteOrder
}

// New creates a new ELF64 linker for the given architecture and OS.
// arch: "x86_64", "aarch64", "riscv64"
// osName: "linux", "android", "none"
// pie: produce position-independent executable
// baseAddr: override base address (0 = use default)
func New(arch, osName string, pie bool, baseAddr uint64) *L {
        bo := binary.LittleEndian
        if arch == "aarch64" {
                bo = binary.LittleEndian // AArch64 default is LE
        }
        base := uint64(defaultBaseExec)
        if pie {
                base = uint64(defaultBasePIE)
        }
        if osName == "none" {
                base = uint64(bareMetalBase)
        }
        if baseAddr != 0 {
                base = baseAddr
        }
        return &L{
                arch:      arch,
                osName:    osName,
                pie:       pie,
                baseAddr:  base,
                byteOrder: bo,
        }
}

// ---------- types.Linker interface ----------

// AddCode adds an executable code section.
func (l *L) AddCode(name string, data []byte) {
        l.sections = append(l.sections, &section{
                name:  name,
                data:  append([]byte(nil), data...),
                flags: SHF_ALLOC | SHF_EXECINSTR,
                align: 16,
                size:  uint64(len(data)),
        })
}

// AddData adds a read-only data section.
func (l *L) AddData(name string, data []byte) {
        l.sections = append(l.sections, &section{
                name:  name,
                data:  append([]byte(nil), data...),
                flags: SHF_ALLOC,
                align: 16,
                size:  uint64(len(data)),
        })
}

// AddRWData adds a read-write initialised data section.
func (l *L) AddRWData(name string, data []byte) {
        l.sections = append(l.sections, &section{
                name:  name,
                data:  append([]byte(nil), data...),
                flags: SHF_ALLOC | SHF_WRITE,
                align: 16,
                size:  uint64(len(data)),
        })
}

// AddBSS reserves zero-initialised bytes.
func (l *L) AddBSS(name string, size uint64, align uint32) {
        if align == 0 {
                align = 16
        }
        l.sections = append(l.sections, &section{
                name:  name,
                data:  nil,
                flags: SHF_ALLOC | SHF_WRITE,
                align: uint64(align),
                size:  size,
        })
}

// AddSymbol registers a symbol.
func (l *L) AddSymbol(name string, addr uint64, size uint64, kind types.SymbolKind) {
        binding := STB_GLOBAL
        if strings.HasPrefix(name, ".L") || strings.HasPrefix(name, "__") {
                binding = STB_LOCAL
        }
        l.symbols = append(l.symbols, &symbol{
                name:    name,
                addr:    addr,
                size:    size,
                kind:    kind,
                binding: binding,
        })
}

// AddRelocation records a relocation.
func (l *L) AddRelocation(section string, offset uint64, sym string, relType types.RelocType, addend int64) {
        l.relocs = append(l.relocs, &relocation{
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

// SetStrip controls whether symbol table sections (.symtab, .strtab) are
// omitted from the output.  Stripping significantly reduces binary size
// and is the default for release builds.
func (l *L) SetStrip(v bool) {
        l.strip = v
}

// SetCustomSection adds a raw section with arbitrary flags.
func (l *L) SetCustomSection(name string, data []byte, flags uint32) {
        l.sections = append(l.sections, &section{
                name:  name,
                data:  append([]byte(nil), data...),
                flags: uint64(flags),
                align: 16,
                size:  uint64(len(data)),
        })
}

// =====================================================================
// Link – main entry point
// =====================================================================

// Link produces the final ELF64 binary.
func (l *L) Link() ([]byte, error) {
        if len(l.sections) == 0 {
                return nil, fmt.Errorf("elf64: no sections to link")
        }

        // 1. Assign default section names if needed.
        l.ensureDefaults()

        // 2. Merge same-type sections into canonical ones (.text, .rodata, etc.).
        l.mergeSections()

        // 2b. Record current (non-merged) section addresses.
        l.recordSectionAddrs()

        // 3. Layout: compute file offsets and virtual addresses.
        if err := l.layout(); err != nil {
                return nil, fmt.Errorf("elf64: layout: %w", err)
        }

        // 3b. Re-record section addresses now that layout has assigned VAs.
        // Also resolve merged-away sections using the mergeMap.
        l.recordSectionAddrs()

        // 4. Resolve relocations against section data.
        if err := l.applyRelocations(); err != nil {
                return nil, fmt.Errorf("elf64: relocations: %w", err)
        }

        // 5. Build the symbol table and string tables (skip when stripping).
        var symTab, strTab []byte
        if !l.strip {
                symTab, strTab = l.buildSymbolTable()
        }

        // 6. Emit the binary.
        return l.emit(symTab, strTab)
}

// ---------- defaults ----------

// ensureDefaults adds .text, .rodata, .data, .bss if the user didn't
// already provide them.
func (l *L) ensureDefaults() {
        has := make(map[string]bool)
        for _, s := range l.sections {
                has[s.name] = true
        }
        if !has[".text"] {
                l.AddCode(".text", nil)
        }
        if !has[".rodata"] {
                l.AddData(".rodata", nil)
        }
        if !has[".data"] {
                l.AddRWData(".data", nil)
        }
        if !has[".bss"] {
                l.AddBSS(".bss", 0, 16)
        }
}

// ---------- section merging ----------

// mergeSections consolidates multiple sections of the same type into their
// canonical counterparts (.text, .rodata, .data, .bss).  This reduces the
// number of section headers and improves instruction cache locality.
//
// For each category, every non-canonical section's data is appended (with
// proper alignment) to the canonical section.  Relocations are rewritten so
// that their section and offset fields continue to reference the correct byte
// positions inside the merged section.
func (l *L) mergeSections() {
        type mergeRule struct {
                canonical string
                match     func(*section) bool
        }

        rules := []mergeRule{
                {".text", func(s *section) bool { return s.flags&SHF_EXECINSTR != 0 }},
                {".rodata", func(s *section) bool {
                        return s.flags&SHF_ALLOC != 0 && s.flags&SHF_EXECINSTR == 0 && s.flags&SHF_WRITE == 0 && s.data != nil
                }},
                {".data", func(s *section) bool {
                        return s.flags&SHF_WRITE != 0 && s.data != nil
                }},
                {".bss", func(s *section) bool {
                        return s.data == nil && s.flags&SHF_ALLOC != 0
                }},
        }

        for _, rule := range rules {
                // Locate the canonical section.
                var canon *section
                for _, s := range l.sections {
                        if s.name == rule.canonical {
                                canon = s
                                break
                        }
                }
                if canon == nil {
                        continue
                }

                // Collect non-canonical sections that match the rule.
                var toMerge []*section
                mergedNames := make(map[string]bool)
                for _, s := range l.sections {
                        if s.name != rule.canonical && rule.match(s) {
                                toMerge = append(toMerge, s)
                                mergedNames[s.name] = true
                        }
                }
                if len(toMerge) == 0 {
                        continue
                }

                // Compute the byte offset of each merged section within the
                // canonical section (relative to the canonical's start).
                offsetAdj := make(map[string]uint64, len(toMerge))
                off := canon.size
                for _, s := range toMerge {
                        aligned := alignUp(off, s.align)
                        offsetAdj[s.name] = aligned
                        if s.data != nil {
                                off = aligned + uint64(len(s.data))
                        } else {
                                off = aligned + s.size
                        }
                }

                // Append data from merged sections into the canonical section.
                var newData []byte
                if canon.data != nil {
                        newData = append(newData, canon.data...)
                }
                for _, s := range toMerge {
                        if s.data != nil {
                                pad := alignUp(uint64(len(newData)), s.align) - uint64(len(newData))
                                newData = append(newData, make([]byte, pad)...)
                                newData = append(newData, s.data...)
                        }
                }
                if len(newData) > 0 {
                        canon.data = newData
                }
                canon.size = off

                // Keep the maximum alignment requirement.
                for _, s := range toMerge {
                        if s.align > canon.align {
                                canon.align = s.align
                        }
                }

                // Update relocations whose patch-site section was merged.
                for _, r := range l.relocs {
                        if adj, ok := offsetAdj[r.section]; ok {
                                r.offset += adj
                                r.section = rule.canonical
                        }
                        // Also update relocations whose target symbol is a
                        // merged section.  After merging, the section no
                        // longer exists, so the relocation must point at the
                        // canonical section with an adjusted addend so that
                        // S + A still resolves to the same byte address.
                        if adj, ok := offsetAdj[r.sym]; ok {
                                r.sym = rule.canonical
                                r.addend += int64(adj)
                        }
                }

                // Record merge mapping for entry-point resolution and
                // future symbol lookups.
                for _, s := range toMerge {
                        if l.mergeMap == nil {
                                l.mergeMap = make(map[string][2]uint64)
                        }
                        // Store a string key in the merge map for later lookup.
                        l.mergeMap[s.name] = [2]uint64{uint64(len(rule.canonical)), offsetAdj[s.name]}
                        if l.canonMap == nil {
                                l.canonMap = make(map[string]string)
                        }
                        l.canonMap[s.name] = rule.canonical
                }

                // Remove merged sections from the list.
                var kept []*section
                for _, s := range l.sections {
                        if !mergedNames[s.name] {
                                kept = append(kept, s)
                        }
                }
                l.sections = kept
        }
}

// recordSectionAddrs populates the sectionAddr map with absolute virtual
// addresses for every section (including those that were merged away).
func (l *L) recordSectionAddrs() {
        if l.sectionAddr == nil {
                l.sectionAddr = make(map[string]uint64)
        }
        // Record non-merged sections.
        for _, s := range l.sections {
                l.sectionAddr[s.name] = s.addr
        }
        // Resolve merged-away sections using the mergeMap.
        for name, info := range l.mergeMap {
                _ = info[0] // canonical section length (reserved for future use)
                offset := info[1]
                // Find the canonical section by name (not length).
                // This is O(n) per merged section but the number of sections is small.
                canonName := l.canonMap[name]
                for _, s := range l.sections {
                        if s.name == canonName {
                                l.sectionAddr[name] = s.addr + offset
                                break
                        }
                }
        }
}

// =====================================================================
// Layout
// =====================================================================

// layout computes file offsets and virtual addresses for all sections.
//
// Memory layout (virtual):
//   base .. base+codeSize          .text   (R+X)
//   aligned ..                     .rodata (R)
//   aligned ..                     .data   (RW)
//   aligned ..                     .bss    (RW)
//
// File layout:
//   ELF header          64 bytes
//   program headers     n * 56 bytes
//   .text               (code)
//   .rodata             (read-only data)
//   .data               (read-write data)
//   .bss                (no file space)
//   .shstrtab           (section name strings)
//   .symtab             (symbol table)
//   .strtab             (symbol strings)
//   section headers     n * 64 bytes
func (l *L) layout() error {
        // Classify sections into segments.
        codeSections := l.filterSections(func(s *section) bool { return s.flags&SHF_EXECINSTR != 0 })
        roSections := l.filterSections(func(s *section) bool {
                return s.flags&SHF_ALLOC != 0 && s.flags&SHF_EXECINSTR == 0 && s.flags&SHF_WRITE == 0
        })
        rwSections := l.filterSections(func(s *section) bool {
                return s.flags&SHF_WRITE != 0 && s.data != nil
        })
        bssSections := l.filterSections(func(s *section) bool {
                return s.data == nil && s.flags&SHF_ALLOC != 0
        })

        // Compute total code size.
        codeSize := l.sectionsSize(codeSections)
        roSize := l.sectionsSize(roSections)
        rwSize := l.sectionsSize(rwSections)
        bssSize := l.sectionsSize(bssSections)

        // File header sizes.
        ehdrSize := uint64(64)
        phdrSize := uint64(56) // sizeof(Elf64_Phdr)

        // Program headers: 2-3 LOAD segments + optional GNU_STACK
        numPH := 2 // text+ro, data+bss
        if rwSize > 0 || bssSize > 0 {
                numPH = 3 // text+ro, data, bss (or data+bss combined)
        }
        // Actually let's use a simpler scheme:
        // PH[0] = LOAD (text + rodata, R+X)
        // PH[1] = LOAD (data + bss, RW)  (combined data and bss in one segment)
        // PH[2] = GNU_STACK
        numPH = 3
        if l.osName == "none" {
                numPH = 2 // no GNU_STACK for bare metal
        }

        headersSize := ehdrSize + uint64(numPH)*phdrSize

        // Align start of code to page boundary in both file and memory.
        // ELF requires: p_offset % p_align == p_vaddr % p_align.
        // By aligning both fileOff and vaddr to the page size, this is satisfied.
        fileOff := alignUp(headersSize, pageSize)

        // Virtual address for code.
        vaddr := alignUp(l.baseAddr+headersSize, pageSize)

        // Assign code sections.
        l.assignOffsets(codeSections, fileOff, vaddr)
        fileOff += codeSize
        vaddr += codeSize

        // Assign rodata sections (same segment as code).
        l.assignOffsets(roSections, fileOff, vaddr)
        fileOff += roSize
        vaddr += roSize

        // Data segment starts on a new page boundary.
        vaddrData := alignUp(vaddr, pageSize)
        fileOffData := alignUp(fileOff, pageSize) // align in file too

        l.assignOffsets(rwSections, fileOffData, vaddrData)
        fileOffData += rwSize
        vaddrData += rwSize

        // BSS comes right after data (no file space).
        l.assignOffsetsBSS(bssSections, vaddrData)

        return nil
}

// assignOffsets sets file offset and vaddr for each section.
func (l *L) assignOffsets(sections []*section, fileOff, vaddr uint64) {
        off := fileOff
        va := vaddr
        for _, s := range sections {
                va = alignUp(va, s.align)
                off = alignUp(off, s.align)
                s.offset = off
                s.addr = va
                if s.data != nil {
                        off += uint64(len(s.data))
                }
                va += s.size
        }
}

// assignOffsetsBSS is like assignOffsets but sections have no file space.
func (l *L) assignOffsetsBSS(sections []*section, vaddr uint64) {
        va := vaddr
        for _, s := range sections {
                va = alignUp(va, s.align)
                s.offset = 0 // no file offset
                s.addr = va
                va += s.size
        }
}

// =====================================================================
// Relocations
// =====================================================================

// applyRelocations modifies section data in-place to resolve relocations.
func (l *L) applyRelocations() error {
        // Build a section name → section map.
        secMap := make(map[string]*section, len(l.sections))
        for _, s := range l.sections {
                secMap[s.name] = s
        }

        // Build a symbol name → symbol map.
        symMap := make(map[string]*symbol, len(l.symbols))
        for _, sym := range l.symbols {
                symMap[sym.name] = sym
        }

        // Also build section-symbol map so we can resolve section names.
        // Include merged-away sections via sectionAddr.
        for _, s := range l.sections {
                symMap[s.name] = &symbol{
                        name: s.name,
                        addr: s.addr,
                }
        }
        for name, addr := range l.sectionAddr {
                if _, exists := symMap[name]; !exists {
                        symMap[name] = &symbol{
                                name: name,
                                addr: addr,
                        }
                }
        }

        for _, r := range l.relocs {
                sec, ok := secMap[r.section]
                if !ok {
                        return fmt.Errorf("relocation targets unknown section %q", r.section)
                }
                if sec.data == nil {
                        return fmt.Errorf("cannot relocate BSS section %q", r.section)
                }

                sym, ok := symMap[r.sym]

                if !ok {
                        // Try to find a symbol whose name matches a section.
                        return fmt.Errorf("relocation references unknown symbol %q", r.sym)
                }

                P := sec.addr + r.offset // address of the relocation site
                S := sym.addr            // symbol value

                switch l.arch {
                case "x86_64":
                        if err := l.relocX86_64(sec.data, int(r.offset), S, P, r.relType, r.addend); err != nil {
                                return err
                        }
                case "aarch64":
                        if err := l.relocAArch64(sec.data, int(r.offset), S, P, r.relType, r.addend); err != nil {
                                return err
                        }
                case "riscv64":
                        if err := l.relocRISCV64(sec.data, int(r.offset), S, P, r.relType, r.addend); err != nil {
                                return err
                        }
                default:
                        return fmt.Errorf("unsupported arch %q for relocations", l.arch)
                }
        }
        return nil
}

// ---- x86_64 relocations ----

func (l *L) relocX86_64(data []byte, off int, S, P uint64, rt types.RelocType, addend int64) error {
        bo := l.byteOrder
        A := uint64(addend)

        switch rt {
        case types.RelocAbs32, types.RelocAbs32S, types.RelocAbs64:
                val := int64(S + A)
                if rt == types.RelocAbs32 {
                        if int64(uint32(val)) != val {
                                return fmt.Errorf("R_X86_64_32 overflow: %d", val)
                        }
                        if off+4 > len(data) {
                                return fmt.Errorf("reloc out of bounds")
                        }
                        bo.PutUint32(data[off:], uint32(val))
                } else if rt == types.RelocAbs32S {
                        if val < math.MinInt32 || val > math.MaxInt32 {
                                return fmt.Errorf("R_X86_64_32S overflow: %d", val)
                        }
                        if off+4 > len(data) {
                                return fmt.Errorf("reloc out of bounds")
                        }
                        bo.PutUint32(data[off:], uint32(val))
                } else {
                        if off+8 > len(data) {
                                return fmt.Errorf("reloc out of bounds")
                        }
                        bo.PutUint64(data[off:], uint64(val))
                }

        case types.RelocPC32, types.RelocPLT32:
                val := int64(S + A - P)
                if off+4 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                bo.PutUint32(data[off:], uint32(val))

        default:
                return fmt.Errorf("unsupported x86_64 relocation type %d", rt)
        }
        return nil
}

// ---- AArch64 relocations ----

func (l *L) relocAArch64(data []byte, off int, S, P uint64, rt types.RelocType, addend int64) error {
        bo := l.byteOrder
        A := uint64(addend)

        switch rt {
        case types.RelocCall26:
                // AArch64 B instruction: 26-bit signed displacement, offset << 2.
                displacement := int64(S + A - P)
                if displacement&3 != 0 {
                        return fmt.Errorf("R_AARCH64_CALL26: target not 4-byte aligned")
                }
                imm26 := displacement >> 2
                if imm26 < -0x2000000 || imm26 > 0x1ffffff {
                        return fmt.Errorf("R_AARCH64_CALL26: out of range (%d)", displacement)
                }
                if off+4 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                inst := bo.Uint32(data[off:])
                inst = (inst &^ 0x03ffffff) | (uint32(imm26) & 0x03ffffff)
                bo.PutUint32(data[off:], inst)

        case types.RelocAbs64:
                if off+8 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                bo.PutUint64(data[off:], S+A)

        case types.RelocAbs32:
                if off+4 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                bo.PutUint32(data[off:], uint32(S+A))

        case types.RelocPC32:
                if off+4 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                bo.PutUint32(data[off:], uint32(int64(S+A-P)))

        default:
                return fmt.Errorf("unsupported aarch64 relocation type %d", rt)
        }
        return nil
}

// ---- RISC-V relocations ----

func (l *L) relocRISCV64(data []byte, off int, S, P uint64, rt types.RelocType, addend int64) error {
        bo := l.byteOrder
        A := uint64(addend)

        switch rt {
        case types.RelocCall26:
                // RISC-V JAL: 21-bit signed, shifted left 2.
                // Stored as U-type immediate in bits [31:12].
                displacement := int64(S + A - P)
                if displacement&3 != 0 {
                        return fmt.Errorf("R_RISCV_CALL: target not 4-byte aligned")
                }
                imm := displacement >> 2
                if imm < -0x100000 || imm > 0xfffff {
                        return fmt.Errorf("R_RISCV_CALL: out of range (%d)", displacement)
                }
                if off+4 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                inst := bo.Uint32(data[off:])
                // Clear immediate bits: [31], [30:21], [20], [19:12]
                inst &^= 0xfff00000
                // Encode U-type immediate.
                bit31 := uint32((imm >> 20) & 1) << 31
                bits3021 := uint32((imm >> 1) & 0x3ff) << 21
                bit20 := uint32((imm >> 11) & 1) << 20
                bits1912 := uint32((imm >> 12) & 0xff) << 12
                inst |= bit31 | bits3021 | bit20 | bits1912
                bo.PutUint32(data[off:], inst)

        case types.RelocAbs64:
                if off+8 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                bo.PutUint64(data[off:], S+A)

        case types.RelocAbs32:
                if off+4 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                bo.PutUint32(data[off:], uint32(S+A))

        case types.RelocPC32:
                if off+4 > len(data) {
                        return fmt.Errorf("reloc out of bounds")
                }
                bo.PutUint32(data[off:], uint32(int64(S+A-P)))

        default:
                return fmt.Errorf("unsupported riscv64 relocation type %d", rt)
        }
        return nil
}

// =====================================================================
// Symbol table
// =====================================================================

// buildSymbolTable constructs the .symtab and .strtab contents.
func (l *L) buildSymbolTable() ([]byte, []byte) {
        // Collect all symbols and sort: local first, then global.
        syms := make([]*symbol, len(l.symbols))
        copy(syms, l.symbols)
        sort.SliceStable(syms, func(i, j int) bool {
                if syms[i].binding != syms[j].binding {
                        return syms[i].binding < syms[j].binding
                }
                return syms[i].name < syms[j].name
        })

        // Build string table (start with null byte).
        var strTab []byte = []byte{0}
        strTabMap := make(map[string]uint32)

        putStr := func(s string) uint32 {
                if off, ok := strTabMap[s]; ok {
                        return off
                }
                off := uint32(len(strTab))
                strTab = append(strTab, []byte(s)...)
                strTab = append(strTab, 0)
                strTabMap[s] = off
                return off
        }

        // Elf64_Sym = 24 bytes: name(4) info(1) other(1) shndx(2) value(8) size(8)
        type elfSym struct {
                stName  uint32
                stInfo  byte
                stOther byte
                stShndx uint16
                stValue uint64
                stSize  uint64
        }

        var symTab []byte

        // Null symbol (index 0).
        symTab = append(symTab, make([]byte, 24)...)

        // Section index lookup.
        secIdxMap := make(map[string]uint16)
        for i, s := range l.sections {
                secIdxMap[s.name] = uint16(i + 1) // +1 for SHT_NULL at index 0
        }

        for _, sym := range syms {
                if sym.name == "" {
                        continue
                }
                nameOff := putStr(sym.name)

                var typ byte
                switch sym.kind {
                case types.SymFunc:
                        typ = STT_FUNC
                case types.SymData, types.SymBSS:
                        typ = STT_OBJECT
                case types.SymFile:
                        typ = STT_FILE
                case types.SymSection:
                        typ = STT_SECTION
                default:
                        typ = STT_NOTYPE
                }

                info := (byte(sym.binding) << 4) | (typ & 0xf)

                shndx := secIdxMap[sym.section]
                if sym.binding == STB_LOCAL || sym.section == "" {
                        shndx = SHN_ABS
                }
                // If symbol has a known address that matches a section, use that section.
                if shndx == 0 {
                        for _, s := range l.sections {
                                if s.addr <= sym.addr && sym.addr < s.addr+s.size {
                                        shndx = secIdxMap[s.name]
                                        break
                                }
                        }
                        if shndx == 0 {
                                shndx = SHN_ABS
                        }
                }

                es := elfSym{
                        stName:  nameOff,
                        stInfo:  info,
                        stOther: 0,
                        stShndx: shndx,
                        stValue: sym.addr,
                        stSize:  sym.size,
                }

                var buf [24]byte
                l.byteOrder.PutUint32(buf[0:4], es.stName)
                buf[4] = es.stInfo
                buf[5] = es.stOther
                l.byteOrder.PutUint16(buf[6:8], es.stShndx)
                l.byteOrder.PutUint64(buf[8:16], es.stValue)
                l.byteOrder.PutUint64(buf[16:24], es.stSize)
                symTab = append(symTab, buf[:]...)
        }

        return symTab, strTab
}

// =====================================================================
// Emit
// =====================================================================

// emit produces the final ELF64 binary byte stream.
func (l *L) emit(symTab, strTab []byte) ([]byte, error) {
        bo := l.byteOrder

        // ---- Determine segment boundaries ----
        codeStart, codeEnd := l.segmentBounds(func(s *section) bool {
                return s.flags&SHF_EXECINSTR != 0
        })
        roSections := l.filterSections(func(s *section) bool {
                return s.flags&SHF_ALLOC != 0 && s.flags&SHF_EXECINSTR == 0 && s.flags&SHF_WRITE == 0
        })
        rwStart, rwEnd := l.segmentBounds(func(s *section) bool {
                return s.flags&SHF_WRITE != 0 && s.data != nil
        })
        bssStart, bssEnd := l.segmentBounds(func(s *section) bool {
                return s.data == nil && s.flags&SHF_ALLOC != 0
        })

        // Text segment = code sections (+ trailing rodata if present)
        textVaddr := codeStart.addr
        textFileOff := codeStart.offset
        // Use code end as the baseline; extend through rodata if it has file data.
        textFileSz := codeEnd.offset
        if len(roSections) > 0 {
                lastRO := roSections[len(roSections)-1]
                if lastRO.data != nil {
                        textFileSz = lastRO.offset + uint64(len(lastRO.data))
                }
        }
        textFileSz -= codeStart.offset
        textMemSz := codeEnd.addr
        if len(roSections) > 0 {
                lastRO := roSections[len(roSections)-1]
                textMemSz = lastRO.addr + lastRO.size
        }
        textMemSz -= codeStart.addr
        // For static binaries, memsz must equal filesz to avoid kernel issues
        // with partial-page zeroing in constrained environments (containers).
        if textMemSz > textFileSz {
                textMemSz = textFileSz
        }

        // Data segment = data + bss (bss has no file size)
        dataVaddr := rwStart.addr
        dataFileOff := rwStart.offset
        var dataFileSz, dataMemSz uint64
        if dataVaddr > 0 || rwStart.size > 0 {
                dataFileSz = rwEnd.offset - rwStart.offset
                dataMemSz = bssEnd.addr - rwStart.addr
        }
        // If there's no data but there is BSS, BSS starts in memory after text.
        if dataFileSz == 0 && bssStart.size > 0 {
                dataVaddr = bssStart.addr
                dataMemSz = bssEnd.addr - bssStart.addr
        }

        // ---- Program headers ----
        numPH := 2
        if l.osName != "none" {
                numPH = 3 // + GNU_STACK
        }

        // ---- Section headers ----
        // Layout: SHT_NULL, then user sections, then .shstrtab [, .symtab, .strtab]
        numSH := 1 + len(l.sections) + 1 // null + user + shstrtab
        if !l.strip {
                numSH += 2 // + symtab + strtab
        }

        // Build .shstrtab
        shstrtab, shNameMap := l.buildShstrtab()

        // ---- Compute total file size ----
        ehdrSize := uint64(64)
        phdrOff := ehdrSize

        // Sections already have their file offsets. Find the end of the last file-backed section.
        lastFileOff := uint64(0)
        for _, s := range l.sections {
                if s.data != nil {
                        end := s.offset + uint64(len(s.data))
                        if end > lastFileOff {
                                lastFileOff = end
                        }
                }
        }

        // After all loadable sections, place: .shstrtab [, .symtab, .strtab]
        metaOff := alignUp(lastFileOff, 8)
        shstrtabOff := metaOff
        var symtabOff, strtabOff uint64
        var shdrOff uint64
        if l.strip {
                shdrOff = alignUp(shstrtabOff+uint64(len(shstrtab)), 8)
        } else {
                symtabOff = shstrtabOff + uint64(len(shstrtab))
                strtabOff = symtabOff + uint64(len(symTab))
                shdrOff = alignUp(strtabOff+uint64(len(strTab)), 8)
        }

        totalSize := shdrOff + uint64(numSH)*64

        buf := make([]byte, totalSize)

        // ---- ELF header ----
        l.writeEhdr(buf, bo, uint16(numPH), uint16(numSH), phdrOff, shdrOff)

        // ---- Program headers ----
        l.writePhdr(buf, bo, phdrOff, 0, textVaddr, textFileOff, textFileSz, textMemSz, PF_R|PF_X)
        if dataMemSz > 0 {
                l.writePhdr(buf, bo, phdrOff, 1, dataVaddr, dataFileOff, dataFileSz, dataMemSz, PF_R|PF_W)
        } else {
                // Empty data segment
                l.writePhdr(buf, bo, phdrOff, 1, 0, 0, 0, 0, PF_R|PF_W)
        }
        if l.osName != "none" {
                // PT_GNU_STACK: no executable stack
                l.writePhdr(buf, bo, phdrOff, 2, 0, 0, 0, 0, PF_R|PF_W)
        }

        // ---- Write section data ----
        for _, s := range l.sections {
                if s.data != nil && s.offset > 0 {
                        copy(buf[s.offset:], s.data)
                }
        }

        // ---- Write metadata sections ----
        copy(buf[shstrtabOff:], shstrtab)
        if !l.strip {
                copy(buf[symtabOff:], symTab)
                copy(buf[strtabOff:], strTab)
        }

        // ---- Section headers ----
        // SHT_NULL at index 0
        off := shdrOff
        off = l.writeShdr(buf, off, bo, 0, "", SHT_NULL, 0, 0, 0, 0, 0, 0, 0, 0)

        // User sections
        for _, s := range l.sections {
                shType := uint32(SHT_PROGBITS)
                shSize := uint64(len(s.data))
                if s.data == nil {
                        shType = uint32(SHT_NOBITS)
                        shSize = s.size // BSS: sh_size = memory size, not file size
                }
                nameIdx := shNameMap[s.name]
                // Link and info are 0 for normal sections.
                off = l.writeShdr(buf, off, bo,
                        nameIdx, s.name, shType, s.flags,
                        s.addr, s.offset, shSize,
                        0, 0, uint64(s.align), 0)
        }

        // .shstrtab
        shstrtabIdx := uint32(1 + len(l.sections))
        off = l.writeShdr(buf, off, bo,
                shNameMap[".shstrtab"], ".shstrtab", SHT_STRTAB, 0,
                0, shstrtabOff, uint64(len(shstrtab)), 0, 0, 1, uint64(len(shstrtab)))

        // .symtab and .strtab (omitted when stripping).
        if !l.strip {
                symtabIdx := shstrtabIdx + 1
                strtabIdx := symtabIdx + 1
                off = l.writeShdr(buf, off, bo,
                        shNameMap[".symtab"], ".symtab", SHT_SYMTAB, 0,
                        0, symtabOff, uint64(len(symTab)),
                        strtabIdx, // sh_link → strtab
                        1,         // sh_info = index of first non-local symbol
                        8, 24)     // sh_addralign, sh_entsize (Elf64_Sym = 24 bytes)

                off = l.writeShdr(buf, off, bo,
                        shNameMap[".strtab"], ".strtab", SHT_STRTAB, 0,
                        0, strtabOff, uint64(len(strTab)), 0, 0, 1, uint64(len(strTab)))
        }

        return buf, nil
}

// ---------- ELF header ----------

func (l *L) writeEhdr(buf []byte, bo binary.ByteOrder, numPH, numSH uint16, phdrOff, shdrOff uint64) {
        // e_ident
        buf[0] = 0x7f // ELFMAG0
        buf[1] = 'E'  // ELFMAG1
        buf[2] = 'L'  // ELFMAG2
        buf[3] = 'F'  // ELFMAG3
        buf[4] = ELFCLASS64
        buf[5] = ELFDATA2LSB
        buf[6] = 1    // EV_CURRENT
        buf[7] = 0    // ELFOSABI_NONE

        // e_type
        eType := uint16(ET_EXEC)
        if l.pie {
                eType = ET_DYN
        }
        bo.PutUint16(buf[16:18], eType)

        // e_machine
        bo.PutUint16(buf[18:20], l.elfMachine())

        // e_version
        bo.PutUint32(buf[20:24], 1) // EV_CURRENT

        // e_entry
        entryAddr := l.baseAddr // default if no entry symbol
        if l.entry != "" {
                // First check registered symbols.
                for _, sym := range l.symbols {
                        if sym.name == l.entry {
                                entryAddr = sym.addr
                                break
                        }
                }
                // Fall back to sectionAddr map which tracks both current and
                // merged-away sections.
                if entryAddr == l.baseAddr {
                        if addr, ok := l.sectionAddr[l.entry]; ok {
                                entryAddr = addr
                        }
                }
        }
        bo.PutUint64(buf[24:32], entryAddr)

        // e_phoff
        bo.PutUint64(buf[32:40], phdrOff)

        // e_shoff
        bo.PutUint64(buf[40:48], shdrOff)

        // e_flags
        bo.PutUint32(buf[48:52], l.elfFlags())

        // e_ehsize
        bo.PutUint16(buf[52:54], 64)

        // e_phentsize
        bo.PutUint16(buf[54:56], 56)

        // e_phnum
        bo.PutUint16(buf[56:58], numPH)

        // e_shentsize
        bo.PutUint16(buf[58:60], 64)

        // e_shnum
        bo.PutUint16(buf[60:62], numSH)

        // e_shstrndx
        bo.PutUint16(buf[62:64], uint16(1+len(l.sections))) // .shstrtab index
}

// ---------- Program header ----------

func (l *L) writePhdr(buf []byte, bo binary.ByteOrder, phdrOff uint64, index int, vaddr, fileOff, fileSz, memSz uint64, flags uint32) {
        off := phdrOff + uint64(index)*56
        pType := uint32(PT_LOAD)
        if index == 2 && l.osName != "none" {
                pType = PT_GNU_STACK
        }

        bo.PutUint32(buf[off:off+4], pType)          // p_type
        bo.PutUint32(buf[off+4:off+8], flags)        // p_flags
        bo.PutUint64(buf[off+8:off+16], fileOff)     // p_offset
        bo.PutUint64(buf[off+16:off+24], vaddr)      // p_vaddr
        bo.PutUint64(buf[off+24:off+32], vaddr)      // p_paddr
        bo.PutUint64(buf[off+32:off+40], fileSz)     // p_filesz
        bo.PutUint64(buf[off+40:off+48], memSz)      // p_memsz
        bo.PutUint64(buf[off+48:off+56], pageSize)   // p_align
}

// ---------- Section header ----------

func (l *L) writeShdr(buf []byte, off uint64, bo binary.ByteOrder,
        nameIdx uint32, name string, shType uint32, flags uint64,
        addr, offset, size uint64, link, info uint32, addralign, entsize uint64) uint64 {

        bo.PutUint32(buf[off:off+4], nameIdx)   // sh_name
        bo.PutUint32(buf[off+4:off+8], shType)  // sh_type
        bo.PutUint64(buf[off+8:off+16], flags)  // sh_flags
        bo.PutUint64(buf[off+16:off+24], addr)  // sh_addr
        bo.PutUint64(buf[off+24:off+32], offset) // sh_offset
        bo.PutUint64(buf[off+32:off+40], size)  // sh_size
        bo.PutUint32(buf[off+40:off+44], link)  // sh_link
        bo.PutUint32(buf[off+44:off+48], info)  // sh_info
        bo.PutUint64(buf[off+48:off+56], addralign) // sh_addralign
        bo.PutUint64(buf[off+56:off+64], entsize)  // sh_entsize

        return off + 64
}

// =====================================================================
// Helpers
// =====================================================================

// elfMachine returns the ELF e_machine value.
func (l *L) elfMachine() uint16 {
        switch l.arch {
        case "x86_64":
                return EM_X86_64
        case "aarch64":
                return EM_AARCH64
        case "riscv64":
                return EM_RISCV
        default:
                return EM_X86_64
        }
}

// elfFlags returns architecture-specific ELF flags.
func (l *L) elfFlags() uint32 {
        switch l.arch {
        case "riscv64":
                return 0x5 // RVC + double float ABI
        case "aarch64":
                return 0
        default:
                return 0
        }
}

// buildShstrtab constructs the section name string table and returns
// the table bytes plus a map from section name → string table offset.
func (l *L) buildShstrtab() ([]byte, map[string]uint32) {
        var buf []byte = []byte{0}
        nameMap := make(map[string]uint32)

        putName := func(name string) uint32 {
                off := uint32(len(buf))
                buf = append(buf, []byte(name)...)
                buf = append(buf, 0)
                nameMap[name] = off
                return off
        }

        for _, s := range l.sections {
                putName(s.name)
        }
        putName(".shstrtab")
        if !l.strip {
                putName(".symtab")
                putName(".strtab")
        }

        return buf, nameMap
}

// filterSections returns sections matching the predicate.
func (l *L) filterSections(pred func(*section) bool) []*section {
        var out []*section
        for _, s := range l.sections {
                if pred(s) {
                        out = append(out, s)
                }
        }
        return out
}

// sectionsSize returns the total aligned size of the given sections.
func (l *L) sectionsSize(sections []*section) uint64 {
        var total uint64
        for _, s := range sections {
                total = alignUp(total, s.align)
                if s.data != nil {
                        total += uint64(len(s.data))
                } else {
                        total += s.size
                }
        }
        return alignUp(total, 16)
}

// segmentBounds finds the first and last sections matching the predicate
// and returns start/end section descriptors for computing segment ranges.
func (l *L) segmentBounds(pred func(*section) bool) (start, end *section) {
        matching := l.filterSections(pred)
        if len(matching) == 0 {
                // Return a zero section
                return &section{}, &section{}
        }
        s := matching[0]
        _ = matching[len(matching)-1]
        // Compute end offset/addr
        var endOff, endAddr uint64
        for _, sec := range matching {
                if sec.data != nil {
                        secEnd := sec.offset + uint64(len(sec.data))
                        if secEnd > endOff {
                                endOff = secEnd
                        }
                }
                secAddrEnd := sec.addr + sec.size
                if secAddrEnd > endAddr {
                        endAddr = secAddrEnd
                }
        }
        return s, &section{offset: endOff, addr: endAddr, size: 0}
}

// alignUp rounds v up to the nearest multiple of a.
func alignUp(v, a uint64) uint64 {
        if a == 0 {
                return v
        }
        return (v + a - 1) &^ (a - 1)
}
