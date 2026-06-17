// Package pe64 implements a PE64 linker that produces minimal Windows
// executables for x86-64 and AArch64 targets.
package pe64

import (
        "encoding/binary"
        "fmt"
        "sort"
        "strings"

        "github.com/yilt/yiltc/internal/link/types"
)

// =====================================================================
// PE64 constants
// =====================================================================

// Machine types.
const (
        IMAGE_FILE_MACHINE_AMD64 = 0x8664
        IMAGE_FILE_MACHINE_ARM64 = 0xAA64
)

// PE signature.
var peSignature = [4]byte{'P', 'E', 0, 0}

// Optional header magic.
const (
        PE32PLUS_MAGIC = 0x20b
)

// Subsystem values.
const (
        IMAGE_SUBSYSTEM_WINDOWS_CUI = 3 // Console
        IMAGE_SUBSYSTEM_WINDOWS_GUI = 2 // GUI
)

// DLL characteristics.
const (
        IMAGE_DLLCHARACTERISTICS_HIGH_ENTROPY_VA = 0x0020
        IMAGE_DLLCHARACTERISTICS_DYNAMIC_BASE    = 0x0040
        IMAGE_DLLCHARACTERISTICS_NX_COMPAT       = 0x0100
        IMAGE_DLLCHARACTERISTICS_TERMINAL_SERVER = 0x8000
)

// Section characteristics.
const (
        IMAGE_SCN_CNT_CODE               = 0x00000020
        IMAGE_SCN_CNT_INITIALIZED_DATA   = 0x00000040
        IMAGE_SCN_CNT_UNINITIALIZED_DATA = 0x00000080
        IMAGE_SCN_MEM_EXECUTE            = 0x20000000
        IMAGE_SCN_MEM_READ               = 0x40000000
        IMAGE_SCN_MEM_WRITE              = 0x80000000
        IMAGE_SCN_ALIGN_16BYTES          = 0x00500000
)

// Directory entry indices.
const (
        IMAGE_DIRECTORY_ENTRY_EXPORT    = 0
        IMAGE_DIRECTORY_ENTRY_IMPORT    = 1
        IMAGE_DIRECTORY_ENTRY_BASERELOC = 5
)

// Relocation types (base relocation).
const (
        IMAGE_REL_BASED_DIR64 = 10
        IMAGE_REL_BASED_HIGHLOW = 3
)

// Sizes.
const (
        PEHeaderOffset = 64 // DOS header stub size
        PESignatureSize = 4
        COFFHeaderSize = 20
        OptionalHeaderSize64 = 240 // PE32+ optional header
        SectionHeaderSize = 40
        FileAlignment = 0x200
        SectionAlignment = 0x1000
        DefaultImageBase = 0x140000000
)

// =====================================================================
// Internal types
// =====================================================================

// peSection represents a PE section (.text, .rdata, .data, .bss).
type peSection struct {
        name       string // 8-byte padded name
        data       []byte
        virtualSize uint32
        characteristics uint32
        // computed during layout
        rva        uint32
        rawOffset  uint32
        rawSize    uint32
}

// peSymbol represents a symbol in the linker's view.
type peSymbol struct {
        name    string
        rva     uint32
        size    uint32
        kind    types.SymbolKind
}

// peReloc represents a relocation to apply.
type peReloc struct {
        section string
        offset  uint32
        sym     string
        relType types.RelocType
        addend  int64
}

// =====================================================================
// Linker
// =====================================================================

// L implements types.Linker for PE64 targets.
type L struct {
        arch     string // "x86_64" or "aarch64"
        bo       binary.ByteOrder

        imageBase uint64
        subsystem uint16

        entry      string
        sections   []*peSection
        symbols    []*peSymbol
        relocs     []*peReloc

        exports []string // symbol names to export
}

// New creates a new PE64 linker for the given architecture.
func New(arch string) *L {
        return &L{
                arch:       arch,
                bo:         binary.LittleEndian,
                imageBase:  DefaultImageBase,
                subsystem:  IMAGE_SUBSYSTEM_WINDOWS_CUI,
        }
}

// ---------- types.Linker interface ----------

// AddCode adds an executable code section.
func (l *L) AddCode(name string, data []byte) {
        if name == "" {
                name = ".text"
        }
        l.sections = append(l.sections, &peSection{
                name:           name,
                data:           append([]byte(nil), data...),
                virtualSize:    uint32(len(data)),
                characteristics: IMAGE_SCN_CNT_CODE | IMAGE_SCN_MEM_EXECUTE | IMAGE_SCN_MEM_READ | IMAGE_SCN_ALIGN_16BYTES,
        })
}

// AddData adds a read-only data section.
func (l *L) AddData(name string, data []byte) {
        if name == "" {
                name = ".rdata"
        }
        l.sections = append(l.sections, &peSection{
                name:           name,
                data:           append([]byte(nil), data...),
                virtualSize:    uint32(len(data)),
                characteristics: IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ | IMAGE_SCN_ALIGN_16BYTES,
        })
}

// AddRWData adds a read-write initialised data section.
func (l *L) AddRWData(name string, data []byte) {
        if name == "" {
                name = ".data"
        }
        l.sections = append(l.sections, &peSection{
                name:           name,
                data:           append([]byte(nil), data...),
                virtualSize:    uint32(len(data)),
                characteristics: IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ | IMAGE_SCN_MEM_WRITE | IMAGE_SCN_ALIGN_16BYTES,
        })
}

// AddBSS reserves zero-initialised bytes.
func (l *L) AddBSS(name string, size uint64, align uint32) {
        if name == "" {
                name = ".bss"
        }
        l.sections = append(l.sections, &peSection{
                name:           name,
                data:           nil,
                virtualSize:    uint32(size),
                characteristics: IMAGE_SCN_CNT_UNINITIALIZED_DATA | IMAGE_SCN_MEM_READ | IMAGE_SCN_MEM_WRITE | IMAGE_SCN_ALIGN_16BYTES,
        })
}

// AddSymbol registers a symbol.
func (l *L) AddSymbol(name string, addr uint64, size uint64, kind types.SymbolKind) {
        l.symbols = append(l.symbols, &peSymbol{
                name: name,
                rva:  uint32(addr),
                size: uint32(size),
                kind: kind,
        })
        if kind == types.SymFunc {
                l.exports = append(l.exports, name)
        }
}

// AddRelocation records a relocation.
func (l *L) AddRelocation(section string, offset uint64, sym string, relType types.RelocType, addend int64) {
        l.relocs = append(l.relocs, &peReloc{
                section: section,
                offset:  uint32(offset),
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
        l.sections = append(l.sections, &peSection{
                name:           name,
                data:           append([]byte(nil), data...),
                virtualSize:    uint32(len(data)),
                characteristics: uint32(flags) | IMAGE_SCN_ALIGN_16BYTES,
        })
}

// SetSubsystem sets the PE subsystem (e.g., IMAGE_SUBSYSTEM_WINDOWS_CUI).
func (l *L) SetSubsystem(sub uint16) {
        l.subsystem = sub
}

// SetImageBase sets the preferred load address.
func (l *L) SetImageBase(base uint64) {
        l.imageBase = base
}

// =====================================================================
// Link – main entry point
// =====================================================================

// Link produces the final PE64 binary.
func (l *L) Link() ([]byte, error) {
        if len(l.sections) == 0 {
                return nil, fmt.Errorf("pe64: no sections to link")
        }

        // Ensure default sections.
        l.ensureDefaults()

        // Layout.
        headersSize, err := l.layout()
        if err != nil {
                return nil, fmt.Errorf("pe64: layout: %w", err)
        }

        // Apply relocations against section data.
        if err := l.applyRelocations(); err != nil {
                return nil, fmt.Errorf("pe64: relocations: %w", err)
        }

        // Compute entry point RVA.
        entryRVA, err := l.findEntryPoint()
        if err != nil {
                return nil, fmt.Errorf("pe64: entry point: %w", err)
        }

        // Compute image size.
        lastSec := l.sections[len(l.sections)-1]
        imageSize := alignUp32(lastSec.rva+lastSec.virtualSize, SectionAlignment)

        // Emit.
        return l.emit(headersSize, entryRVA, imageSize)
}

// ---------- defaults ----------

func (l *L) ensureDefaults() {
        has := make(map[string]bool)
        for _, s := range l.sections {
                has[strings.ToLower(s.name)] = true
        }
        if !has[".text"] {
                l.AddCode(".text", nil)
        }
        if !has[".rdata"] {
                l.AddData(".rdata", nil)
        }
        if !has[".data"] {
                l.AddRWData(".data", nil)
        }
        if !has[".bss"] {
                l.AddBSS(".bss", 0, 16)
        }
}

// =====================================================================
// Layout
// =====================================================================

// layout assigns RVA and file offsets to all sections.
// Returns the size of all PE headers (aligned to FileAlignment).
func (l *L) layout() (uint32, error) {
        // Headers: DOS header(64) + PE sig(4) + COFF(20) + Optional(240) + section headers
        numSections := len(l.sections)
        headersRaw := uint32(PEHeaderOffset + PESignatureSize + COFFHeaderSize + OptionalHeaderSize64 + numSections*SectionHeaderSize)
        headersSize := alignUp32(headersRaw, FileAlignment)

        rva := headersSize // first section starts after headers
        rawOff := headersSize

        for _, sec := range l.sections {
                sec.rva = alignUp32(rva, SectionAlignment)
                sec.rawOffset = alignUp32(rawOff, FileAlignment)
                if sec.data != nil {
                        sec.rawSize = alignUp32(uint32(len(sec.data)), FileAlignment)
                } else {
                        sec.rawSize = 0
                }
                rva = sec.rva + sec.virtualSize
                rawOff = sec.rawOffset + sec.rawSize
        }

        return headersSize, nil
}

// =====================================================================
// Relocations
// =====================================================================

func (l *L) applyRelocations() error {
        secMap := make(map[string]*peSection)
        for _, s := range l.sections {
                secMap[strings.ToLower(s.name)] = s
        }

        symMap := make(map[string]*peSymbol)
        for _, sym := range l.symbols {
                symMap[sym.name] = sym
        }

        for _, r := range l.relocs {
                sec, ok := secMap[strings.ToLower(r.section)]
                if !ok {
                        return fmt.Errorf("pe64: relocation targets unknown section %q", r.section)
                }
                if sec.data == nil {
                        return fmt.Errorf("pe64: cannot relocate BSS section %q", r.section)
                }

                sym, ok := symMap[r.sym]
                if !ok {
                        return fmt.Errorf("pe64: relocation references unknown symbol %q", r.sym)
                }

                off := int(r.offset)
                S := uint64(l.imageBase) + uint64(sym.rva)
                P := uint64(l.imageBase) + uint64(sec.rva) + uint64(r.offset)

                switch r.relType {
                case types.RelocAbs32:
                        if off+4 > len(sec.data) {
                                return fmt.Errorf("pe64: reloc out of bounds")
                        }
                        l.bo.PutUint32(sec.data[off:], uint32(S+uint64(r.addend)))

                case types.RelocAbs64:
                        if off+8 > len(sec.data) {
                                return fmt.Errorf("pe64: reloc out of bounds")
                        }
                        l.bo.PutUint64(sec.data[off:], S+uint64(r.addend))

                case types.RelocPC32, types.RelocPLT32:
                        if off+4 > len(sec.data) {
                                return fmt.Errorf("pe64: reloc out of bounds")
                        }
                        l.bo.PutUint32(sec.data[off:], uint32(int64(S+uint64(r.addend))-int64(P)))

                case types.RelocCall26:
                        // For AArch64/PE: B instruction relocation
                        if off+4 > len(sec.data) {
                                return fmt.Errorf("pe64: reloc out of bounds")
                        }
                        displacement := int64(S+uint64(r.addend)) - int64(P)
                        if displacement&3 != 0 {
                                return fmt.Errorf("pe64: CALL26 target not 4-byte aligned")
                        }
                        imm26 := displacement >> 2
                        if imm26 < -0x2000000 || imm26 > 0x1ffffff {
                                return fmt.Errorf("pe64: CALL26 out of range (%d)", displacement)
                        }
                        inst := l.bo.Uint32(sec.data[off:])
                        inst = (inst &^ 0x03ffffff) | (uint32(imm26) & 0x03ffffff)
                        l.bo.PutUint32(sec.data[off:], inst)

                default:
                        return fmt.Errorf("pe64: unsupported relocation type %d", r.relType)
                }
        }
        return nil
}

// =====================================================================
// Export table
// =====================================================================

// buildExportTable builds the .edata content for exported symbols.
// Format: Export Directory Table + Export Address Table + Name Pointer Table +
//         Ordinal Table + DLL name string + name strings.
func (l *L) buildExportTable(imageBase uint64) []byte {
        if len(l.exports) == 0 {
                return nil
        }

        sort.Strings(l.exports)
        numExports := len(l.exports)

        // Export Directory Table is 40 bytes.
        edtSize := 40
        // Export Address Table: numExports * 4
        eatOff := edtSize
        eatSize := numExports * 4
        // Name Pointer Table: numExports * 4
        nptOff := eatOff + eatSize
        nptSize := numExports * 4
        // Ordinal Table: numExports * 2
        otOff := nptOff + nptSize
        otSize := numExports * 2
        // DLL name string
        dllName := "yilt.dll"
        dllNameOff := otOff + otSize
        dllNameBytes := append([]byte(dllName), 0)
        // Export name strings
        nameOff := uint32(dllNameOff + len(dllNameBytes))
        nameStrings := make([]uint32, numExports)
        nameData := make([]byte, 0)

        symMap := make(map[string]*peSymbol)
        for _, sym := range l.symbols {
                symMap[sym.name] = sym
        }

        for i, expName := range l.exports {
                nameStrings[i] = nameOff + uint32(len(nameData))
                nameData = append(nameData, []byte(expName)...)
                nameData = append(nameData, 0)
        }

        totalSize := nameOff + uint32(len(nameData))
        buf := make([]byte, totalSize)
        bo := l.bo

        // Export Directory Table
        bo.PutUint32(buf[0:4], 0)           // Characteristics
        bo.PutUint32(buf[4:8], uint32(0))   // TimeDateStamp
        bo.PutUint16(buf[8:10], 0)          // MajorVersion
        bo.PutUint16(buf[10:12], 0)         // MinorVersion
        bo.PutUint32(buf[12:16], uint32(nameOff)) // Name RVA (relative to image base)
        bo.PutUint32(buf[16:20], uint32(1)) // OrdinalBase
        bo.PutUint32(buf[20:24], uint32(numExports)) // NumberOfFunctions
        bo.PutUint32(buf[24:28], uint32(numExports)) // NumberOfNames
        bo.PutUint32(buf[28:32], uint32(eatOff))     // AddressOfFunctions
        bo.PutUint32(buf[32:36], uint32(nptOff))     // AddressOfNames
        bo.PutUint32(buf[36:40], uint32(otOff))      // AddressOfNameOrdinals

        // Export Address Table
        for i, expName := range l.exports {
                if sym, ok := symMap[expName]; ok {
                        bo.PutUint32(buf[eatOff+i*4:], sym.rva)
                }
        }

        // Name Pointer Table
        for i, noff := range nameStrings {
                bo.PutUint32(buf[nptOff+i*4:], noff)
        }

        // Ordinal Table
        for i := 0; i < numExports; i++ {
                bo.PutUint16(buf[otOff+i*2:], uint16(i))
        }

        // DLL name
        copy(buf[dllNameOff:], dllNameBytes)

        // Name strings
        copy(buf[nameOff:], nameData)

        return buf
}

// =====================================================================
// Emit
// =====================================================================

func (l *L) emit(headersSize, entryRVA, imageSize uint32) ([]byte, error) {
        bo := l.bo
        numSections := len(l.sections)

        // Build export table if we have exports.
        var exportData []byte
        if len(l.exports) > 0 {
                exportData = l.buildExportTable(l.imageBase)
                if exportData != nil {
                        // Add as a section
                        l.sections = append(l.sections, &peSection{
                                name:           ".edata",
                                data:           exportData,
                                virtualSize:    uint32(len(exportData)),
                                characteristics: IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ,
                        })
                        numSections++
                        // Re-layout is needed; for simplicity we just recalculate
                        // the last section's position.
                        last := l.sections[len(l.sections)-1]
                        prev := l.sections[len(l.sections)-2]
                        last.rva = alignUp32(prev.rva+prev.virtualSize, SectionAlignment)
                        last.rawOffset = alignUp32(prev.rawOffset+prev.rawSize, FileAlignment)
                        last.rawSize = alignUp32(uint32(len(exportData)), FileAlignment)
                        imageSize = alignUp32(last.rva+last.virtualSize, SectionAlignment)
                }
        }

        // Compute total file size.
        lastSec := l.sections[len(l.sections)-1]
        totalFileSize := lastSec.rawOffset + lastSec.rawSize

        buf := make([]byte, totalFileSize)

        // ---- DOS Header ----
        l.writeDOSHeader(buf)

        // ---- PE Signature ----
        copy(buf[PEHeaderOffset:], peSignature[:])

        peOff := PEHeaderOffset + PESignatureSize

        // ---- COFF Header ----
        bo.PutUint16(buf[peOff:], l.peMachine())       // Machine
        bo.PutUint16(buf[peOff+2:], uint16(numSections)) // NumberOfSections
        bo.PutUint32(buf[peOff+4:], 0)                  // TimeDateStamp
        bo.PutUint32(buf[peOff+8:], 0)                  // PointerToSymbolTable (0, no COFF symbols)
        bo.PutUint32(buf[peOff+12:], 0)                 // NumberOfSymbols
        bo.PutUint16(buf[peOff+16:], OptionalHeaderSize64) // SizeOfOptionalHeader
        bo.PutUint16(buf[peOff+18:], 0x0022)            // Characteristics: EXECUTABLE_IMAGE | LARGE_ADDRESS_AWARE

        // ---- Optional Header ----
        optOff := uint32(peOff + COFFHeaderSize)
        l.writeOptionalHeader(buf, optOff, entryRVA, imageSize, numSections)

        // ---- Section Headers ----
        shOff := optOff + OptionalHeaderSize64
        for i, sec := range l.sections {
                l.writeSectionHeader(buf, shOff+uint32(i)*SectionHeaderSize, sec)
        }

        // ---- Section Data ----
        for _, sec := range l.sections {
                if sec.data != nil && sec.rawOffset > 0 {
                        copy(buf[sec.rawOffset:], sec.data)
                }
        }

        return buf, nil
}

// ---------- DOS Header ----------

func (l *L) writeDOSHeader(buf []byte) {
        bo := l.bo

        // MZ signature
        bo.PutUint16(buf[0:], 0x5A4D)

        // e_lfanew: pointer to PE header
        bo.PutUint32(buf[60:], uint32(PEHeaderOffset))

        // Minimal DOS stub: "This program cannot be run in DOS mode.\r\n$"
        stub := []byte(
                "\x0e\x1f\xba\x0e\x00\xb4\x09\xcd\x21\xb8\x01\x4c\xcd\x21" +
                        "\x54\x68\x69\x73\x20\x70\x72\x6f\x67\x72\x61\x6d\x20" +
                        "\x63\x61\x6e\x6e\x6f\x74\x20\x62\x65\x20\x72\x75\x6e\x20" +
                        "\x69\x6e\x20\x44\x4f\x53\x20\x6d\x6f\x64\x65\x2e\x0d\x0d" +
                        "\x0a\x24\x00\x00",
        )
        copy(buf[2:], stub)
}

// ---------- Optional Header ----------

func (l *L) writeOptionalHeader(buf []byte, off uint32, entryRVA, imageSize uint32, numSections int) {
        bo := l.bo

        bo.PutUint16(buf[off:], PE32PLUS_MAGIC) // Magic (PE32+)
        buf[off+2] = 14            // MajorLinkerVersion
        buf[off+3] = 0             // MinorLinkerVersion
        bo.PutUint32(buf[off+4:], 0)            // SizeOfCode
        bo.PutUint32(buf[off+8:], 0)            // SizeOfInitializedData
        bo.PutUint32(buf[off+12:], 0)           // SizeOfUninitializedData
        bo.PutUint32(buf[off+16:], entryRVA)    // AddressOfEntryPoint
        bo.PutUint32(buf[off+20:], 0)           // BaseOfCode
        bo.PutUint32(buf[off+24:], 0)           // BaseOfData (not used in PE32+)

        bo.PutUint64(buf[off+28:], l.imageBase) // ImageBase
        bo.PutUint32(buf[off+36:], SectionAlignment) // SectionAlignment
        bo.PutUint32(buf[off+40:], FileAlignment)    // FileAlignment

        bo.PutUint16(buf[off+44:], 6)           // MajorOperatingSystemVersion
        bo.PutUint16(buf[off+46:], 0)           // MinorOperatingSystemVersion
        bo.PutUint16(buf[off+48:], 0)           // MajorImageVersion
        bo.PutUint16(buf[off+50:], 0)           // MinorImageVersion
        bo.PutUint16(buf[off+52:], 6)           // MajorSubsystemVersion
        bo.PutUint16(buf[off+54:], 0)           // MinorSubsystemVersion
        bo.PutUint32(buf[off+56:], 0)           // Win32VersionValue
        bo.PutUint32(buf[off+60:], imageSize)   // SizeOfImage
        bo.PutUint32(buf[off+64:], uint32(PEHeaderOffset+PESignatureSize+COFFHeaderSize+OptionalHeaderSize64+numSections*SectionHeaderSize)) // SizeOfHeaders
        bo.PutUint32(buf[off+68:], 0)           // CheckSum

        bo.PutUint16(buf[off+72:], l.subsystem) // Subsystem
        bo.PutUint16(buf[off+74:], IMAGE_DLLCHARACTERISTICS_HIGH_ENTROPY_VA|
                IMAGE_DLLCHARACTERISTICS_DYNAMIC_BASE|
                IMAGE_DLLCHARACTERISTICS_NX_COMPAT|
                IMAGE_DLLCHARACTERISTICS_TERMINAL_SERVER) // DllCharacteristics

        bo.PutUint64(buf[off+76:], 0x100000)  // SizeOfStackReserve
        bo.PutUint64(buf[off+84:], 0x1000)    // SizeOfStackCommit
        bo.PutUint64(buf[off+92:], 0x100000)  // SizeOfHeapReserve
        bo.PutUint64(buf[off+100:], 0x1000)   // SizeOfHeapCommit
        bo.PutUint32(buf[off+108:], 0)        // LoaderFlags
        bo.PutUint32(buf[off+112:], 16)       // NumberOfRvaAndSizes

        // Data directories (16 entries, each 8 bytes = 128 bytes total)
        // We leave them all zero for now (standalone executable).
        // If we added export data, set the export directory entry.
        ddOff := off + 116
        for i := 0; i < 16; i++ {
                bo.PutUint32(buf[ddOff+uint32(i)*8:], 0)
                bo.PutUint32(buf[ddOff+uint32(i)*8+4:], 0)
        }
}

// ---------- Section Header ----------

func (l *L) writeSectionHeader(buf []byte, off uint32, sec *peSection) {
        bo := l.bo

        // Name: 8 bytes, null-padded
        name := sec.name
        if len(name) > 8 {
                name = name[:8]
        }
        copy(buf[off:], []byte(name))
        for i := len(name); i < 8; i++ {
                buf[off+uint32(i)] = 0
        }

        bo.PutUint32(buf[off+8:], sec.virtualSize)
        bo.PutUint32(buf[off+12:], sec.rva)
        bo.PutUint32(buf[off+16:], sec.rawSize)
        bo.PutUint32(buf[off+20:], sec.rawOffset)
        bo.PutUint32(buf[off+24:], 0)               // PointerToRelocations
        bo.PutUint32(buf[off+28:], 0)               // PointerToLinenumbers
        bo.PutUint16(buf[off+32:], 0)               // NumberOfRelocations
        bo.PutUint16(buf[off+34:], 0)               // NumberOfLinenumbers
        bo.PutUint32(buf[off+36:], sec.characteristics)
}

// =====================================================================
// Helpers
// =====================================================================

// peMachine returns the COFF machine value.
func (l *L) peMachine() uint16 {
        switch l.arch {
        case "aarch64":
                return IMAGE_FILE_MACHINE_ARM64
        default:
                return IMAGE_FILE_MACHINE_AMD64
        }
}

// findEntryPoint returns the RVA of the entry point symbol.
func (l *L) findEntryPoint() (uint32, error) {
        if l.entry == "" {
                return 0, nil
        }
        // Build section RVA map.
        secMap := make(map[string]*peSection)
        for _, s := range l.sections {
                secMap[strings.ToLower(s.name)] = s
        }

        // Look for the entry symbol.
        for _, sym := range l.symbols {
                if sym.name == l.entry {
                        return sym.rva, nil
                }
        }

        // Try to find the entry as a section.
        if sec, ok := secMap[strings.ToLower(l.entry)]; ok {
                return sec.rva, nil
        }

        return 0, fmt.Errorf("entry point symbol %q not found", l.entry)
}

// alignUp32 rounds v up to the nearest multiple of a.
func alignUp32(v, a uint32) uint32 {
        if a == 0 {
                return v
        }
        return (v + a - 1) &^ (a - 1)
}
