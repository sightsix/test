// Package link provides linker-related types and a factory function.
// The actual linker implementations live in sub-packages (elf64, pe64, macho64, wasmobj).
package link

import (
    "fmt"
    "strings"

    "github.com/yilt/yiltc/internal/link/elf64"
    "github.com/yilt/yiltc/internal/link/macho64"
    "github.com/yilt/yiltc/internal/link/pe64"
    "github.com/yilt/yiltc/internal/link/types"
    "github.com/yilt/yiltc/internal/link/wasmobj"
)

// Re-export types from linktypes for convenience.
type (
    SymbolKind = types.SymbolKind
    RelocType  = types.RelocType
    Linker     = types.Linker
)

// Re-export constants.
const (
    SymFunc    = types.SymFunc
    SymData    = types.SymData
    SymBSS     = types.SymBSS
    SymFile    = types.SymFile
    SymSection = types.SymSection

    RelocAbs32   = types.RelocAbs32
    RelocAbs64   = types.RelocAbs64
    RelocPC32    = types.RelocPC32
    RelocPLT32   = types.RelocPLT32
    RelocCall26  = types.RelocCall26
    RelocGotPCRel = types.RelocGotPCRel
    RelocGotEntry = types.RelocGotEntry
)

// Config holds global linker settings.
type Config struct {
    Target      string
    OutputName  string
    PIE         bool
    BaseAddress uint64
}

// NewLinker returns the appropriate Linker for the given Config.
func NewLinker(cfg Config) (Linker, error) {
    triple := strings.ToLower(cfg.Target)

    switch {
    case isELF(triple):
        arch := archFromTriple(triple)
        osName := osFromTriple(triple)
        return elf64.New(arch, osName, cfg.PIE, cfg.BaseAddress), nil

    case isPE(triple):
        arch := archFromTriple(triple)
        return pe64.New(arch), nil

    case isMachO(triple):
        arch := archFromTriple(triple)
        return macho64.New(arch), nil

    case isWASM(triple):
        return wasmobj.New(), nil

    default:
        return nil, fmt.Errorf("link: unsupported target %q", cfg.Target)
    }
}

func isELF(t string) bool {
    return strings.Contains(t, "linux") ||
        strings.Contains(t, "android") ||
        strings.Contains(t, "musl") ||
        strings.Contains(t, "unknown-none")
}

func isPE(t string) bool {
    return strings.Contains(t, "windows")
}

func isMachO(t string) bool {
    return strings.Contains(t, "macos") || strings.Contains(t, "darwin")
}

func isWASM(t string) bool {
    return strings.Contains(t, "wasm")
}

func archFromTriple(t string) string {
    switch {
    case strings.HasPrefix(t, "x86_64"):
        return "x86_64"
    case strings.HasPrefix(t, "aarch64"):
        return "aarch64"
    case strings.HasPrefix(t, "rv64"):
        return "riscv64"
    case strings.HasPrefix(t, "rv32"):
        return "riscv32"
    case strings.HasPrefix(t, "wasm"):
        return "wasm"
    default:
        return "x86_64"
    }
}

func osFromTriple(t string) string {
    if strings.Contains(t, "linux") {
        return "linux"
    }
    if strings.Contains(t, "android") {
        return "android"
    }
    if strings.Contains(t, "unknown-none") {
        return "none"
    }
    return "linux"
}
