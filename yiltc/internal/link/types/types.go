// Package linktypes defines shared types used by all linker backends.
// This package exists to break the import cycle between the parent
// link package and its backend sub-packages.
package types

// SymbolKind describes the kind of a symbol.
type SymbolKind int

const (
        SymFunc   SymbolKind = iota // function symbol
        SymData                     // data symbol
        SymBSS                      // zero-initialised data
        SymFile                     // file-level symbol
        SymSection                  // section symbol
)

// RelocType identifies the kind of relocation.
type RelocType int

const (
        RelocAbs32   RelocType = iota // S + A (32-bit absolute, R_X86_64_32)
        RelocAbs32S                   // S + A (32-bit signed absolute, R_X86_64_32S)
        RelocAbs64                    // S + A (64-bit absolute)
        RelocPC32                     // S + A - P (32-bit PC-relative)
        RelocPLT32                    // L + A - P (32-bit PLT-relative)
        RelocCall26                   // 26-bit call (AArch64 B, RISC-V JAL)
        RelocGotPCRel                 // G + A - GOT (GOT entry PC-relative)
        RelocGotEntry                 // GOT slot (64-bit absolute)
)

// Linker is the interface implemented by every target-specific backend.
type Linker interface {
        AddCode(name string, data []byte)
        AddData(name string, data []byte)
        AddRWData(name string, data []byte)
        AddBSS(name string, size uint64, align uint32)
        AddSymbol(name string, addr uint64, size uint64, kind SymbolKind)
        AddRelocation(section string, offset uint64, sym string, relType RelocType, addend int64)
        SetEntryPoint(name string)
        SetCustomSection(name string, data []byte, flags uint32)
        Link() ([]byte, error)
}
