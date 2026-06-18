package target

import (
        "fmt"
        "runtime"
        "strings"
)

// Target describes a compilation target.
type Target struct {
        Arch     string         // "x86_64", "aarch64", "rv64", "rv32", "wasm"
        OS       string         // "linux", "windows", "macos", "android", "none"
        ABI      string         // "gnu", "msvc", "musl", ""
        Format   string         // "elf64", "pe64", "macho64", "wasm"
        Triple   string         // full target triple
        Features map[string]bool // target feature flags
        Is64Bit  bool
        IsBare   bool
        Ext      string // output file extension override (e.g. "exe", "wasm")
}

// Known targets.
var targets = []Target{
        // x86_64
        {"x86_64", "linux", "gnu", "elf64", "x86_64-linux-gnu", map[string]bool{"sse2": true, "sse3": true, "avx": false}, true, false, ""},
        {"x86_64", "linux", "musl", "elf64", "x86_64-linux-musl", map[string]bool{"sse2": true, "static": true}, true, false, ""},
        {"x86_64", "windows", "msvc", "pe64", "x86_64-windows-msvc", map[string]bool{"sse2": true}, true, false, ""},
        {"x86_64", "macos", "", "macho64", "x86_64-macos", map[string]bool{"sse2": true}, true, false, ""},
        {"x86_64", "none", "", "elf64", "x86_64-unknown-none", map[string]bool{"sse2": true}, true, true, ""},

        // AArch64
        {"aarch64", "linux", "gnu", "elf64", "aarch64-linux-gnu", map[string]bool{"fp": true, "simd": true}, true, false, ""},
        {"aarch64", "android", "", "elf64", "aarch64-linux-android", map[string]bool{"fp": true, "simd": true}, true, false, ""},
        {"aarch64", "windows", "msvc", "pe64", "aarch64-windows-msvc", map[string]bool{"fp": true}, true, false, ""},
        {"aarch64", "macos", "", "macho64", "aarch64-macos", map[string]bool{"fp": true, "simd": true}, true, false, ""},
        {"aarch64", "none", "", "elf64", "aarch64-unknown-none", map[string]bool{"fp": true}, true, true, ""},

        // RISC-V 64
        {"rv64", "linux", "gnu", "elf64", "rv64-linux-gnu", map[string]bool{"d": true}, true, false, ""},
        {"rv64", "none", "", "elf64", "rv64-unknown-none", map[string]bool{"d": true}, true, true, ""},

        // RISC-V 32
        {"rv32", "none", "", "elf64", "rv32-unknown-none", map[string]bool{}, false, true, ""},

        // WebAssembly
        {"wasm", "unknown", "", "wasm", "wasm32-unknown-unknown", map[string]bool{"bulk-memory": true}, false, false, ""},
}

// compactArch maps short architecture names to full architecture names.
var compactArch = map[string]string{
        "x86":  "x86_64",
        "arm":  "aarch64",
        "rv":   "rv64",
        "rv32": "rv32",
        "wasm": "wasm",
}

// fullToCompactArch maps full architecture names back to compact names.
var fullToCompactArch = map[string]string{
        "x86_64":  "x86",
        "aarch64": "arm",
        "rv64":    "rv",
        "rv32":    "rv32",
        "wasm":    "wasm",
}

// ParseCompact parses a compact target name and returns the corresponding Target.
//
// Compact format: <arch>[+<modifier>][.<ext>]
//
// Architecture short names:
//
//      x86   → x86_64
//      arm   → aarch64
//      rv    → rv64
//      rv32  → rv32
//      wasm  → wasm
//
// Modifiers (default varies by arch, see below):
//
//      (no modifier)  → default OS/ABI for the architecture (linux-gnu)
//      +musl         → linux-musl
//      +windows      → windows-msvc
//      +macos        → macos (no ABI suffix)
//      +android      → linux-android (no ABI suffix)
//      +bare         → unknown-none (bare metal, no OS)
//
// Extension override:
//
//      .exe  → sets Ext field to "exe"
//      .wasm → sets Ext field to "wasm"
//
// Examples:
//
//      x86          → x86_64-linux-gnu
//      x86+musl     → x86_64-linux-musl
//      x86+windows  → x86_64-windows-msvc
//      x86+macos    → x86_64-macos
//      arm          → aarch64-linux-gnu
//      arm+macos    → aarch64-macos
//      arm+android  → aarch64-linux-android
//      rv           → rv64-linux-gnu
//      rv32+bare    → rv32-unknown-none
//      wasm         → wasm32-unknown-unknown
//      x86+bare     → x86_64-unknown-none
func ParseCompact(name string) (Target, error) {
        name = strings.TrimSpace(name)

        // Extract optional extension override (last '.' separated segment).
        ext := ""
        if dotIdx := strings.LastIndex(name, "."); dotIdx >= 0 {
                ext = name[dotIdx+1:]
                name = name[:dotIdx]
        }

        // Split into arch and optional modifier.
        parts := strings.SplitN(name, "+", 2)
        archShort := parts[0]
        modifier := ""
        if len(parts) == 2 {
                modifier = parts[1]
        }

        // Resolve architecture.
        arch, ok := compactArch[archShort]
        if !ok {
                return Target{}, fmt.Errorf("unknown compact architecture %q (valid: x86, arm, rv, rv32, wasm)", archShort)
        }

        // Resolve OS, ABI, and vendor from modifier.
        var os, abi, vendor string
        switch modifier {
        case "", "linux":
                os = "linux"
                abi = "gnu"
                vendor = ""
        case "musl":
                os = "linux"
                abi = "musl"
                vendor = ""
        case "windows":
                os = "windows"
                abi = "msvc"
                vendor = ""
        case "macos":
                os = "macos"
                abi = ""
                vendor = ""
        case "android":
                os = "android"
                abi = ""
                vendor = "linux"
        case "bare":
                os = "none"
                abi = ""
                vendor = "unknown"
        default:
                return Target{}, fmt.Errorf("unknown compact modifier %q (valid: musl, windows, macos, android, bare)", modifier)
        }

        // Special case: wasm defaults to unknown-unknown, not linux.
        if modifier == "" && arch == "wasm" {
                os = "unknown"
                abi = ""
                vendor = "unknown"
        }

        // Build the full triple for lookup.
        triple := arch
        if vendor != "" {
                triple += "-" + vendor + "-" + os
        } else {
                triple += "-" + os
        }
        if abi != "" {
                triple += "-" + abi
        }

        // Look up in the known targets table.
        for _, t := range targets {
                if t.Triple == triple {
                        t.Ext = ext
                        return t, nil
                }
        }

        // Construct a reasonable target even if not in the table.
        is64 := arch != "rv32" && arch != "wasm"
        format := "elf64"
        switch {
        case os == "windows" || strings.Contains(os, "windows"):
                format = "pe64"
        case os == "macos" || os == "darwin":
                format = "macho64"
        case arch == "wasm":
                format = "wasm"
        }

        return Target{
                Arch:     arch,
                OS:       os,
                ABI:      abi,
                Format:   format,
                Triple:   triple,
                Features: make(map[string]bool),
                Is64Bit:  is64,
                IsBare:   os == "none",
                Ext:      ext,
        }, nil
}

// ParseTarget parses a target string. It first tries the compact format
// (e.g. "x86", "arm+musl", "rv+bare.exe"), and if that fails, falls back
// to the full triple format (e.g. "x86_64-linux-gnu").
func ParseTarget(triple string) (Target, error) {
        triple = strings.TrimSpace(triple)

        // Try compact format first.
        if t, err := ParseCompact(triple); err == nil {
                return t, nil
        }

        // Direct match on full triple.
        for _, t := range targets {
                if t.Triple == triple {
                        return t, nil
                }
        }

        // Try partial match on full triple.
        for _, t := range targets {
                if strings.HasPrefix(t.Triple, triple) {
                        return t, nil
                }
        }

        // Try to construct from parts.
        parts := strings.Split(triple, "-")
        if len(parts) >= 2 {
                arch := parts[0]
                os := "none"
                abi := ""
                if len(parts) >= 3 {
                        os = parts[1]
                        abi = parts[2]
                }

                t := Target{
                        Arch:     arch,
                        OS:       os,
                        ABI:      abi,
                        Triple:   triple,
                        Is64Bit:  arch != "rv32" && arch != "wasm",
                        IsBare:   os == "none",
                        Features: make(map[string]bool),
                }

                switch {
                case strings.Contains(os, "windows"):
                        t.Format = "pe64"
                case strings.Contains(os, "macos") || strings.Contains(os, "darwin"):
                        t.Format = "macho64"
                case arch == "wasm":
                        t.Format = "wasm"
                default:
                        t.Format = "elf64"
                }

                return t, nil
        }

        return Target{}, fmt.Errorf("unknown target: %s", triple)
}

// CompactName returns the compact representation of this target.
// For example: "x86", "arm+musl", "rv+bare", "wasm".
// If the target has an Ext override, it is appended as ".<ext>".
func (t Target) CompactName() string {
        archShort, ok := fullToCompactArch[t.Arch]
        if !ok {
                archShort = t.Arch
        }

        modifier := ""
        switch {
        case t.OS == "unknown" && t.Arch == "wasm":
                // wasm: no modifier needed
        case t.OS == "linux" && t.ABI == "gnu":
                // default: no modifier needed
        case t.OS == "linux" && t.ABI == "musl":
                modifier = "+musl"
        case t.OS == "windows":
                modifier = "+windows"
        case t.OS == "macos":
                modifier = "+macos"
        case t.OS == "android":
                modifier = "+android"
        case t.OS == "none":
                modifier = "+bare"
        }

        name := archShort + modifier
        if t.Ext != "" {
                name += "." + t.Ext
        }
        return name
}

// DefaultTarget returns the target for the current host.
func DefaultTarget() Target {
        goos := runtime.GOOS
        goarch := runtime.GOARCH

        // Map Go arch to compact arch short name.
        archShort := ""
        switch goarch {
        case "amd64":
                archShort = "x86"
        case "arm64":
                archShort = "arm"
        case "riscv64":
                archShort = "rv"
        default:
                // Unrecognized arch — fall back to table default.
                return targets[0]
        }

        // Determine modifier from OS.
        modifier := ""
        switch goos {
        case "linux":
                modifier = "" // default → linux-gnu
        case "windows":
                modifier = "+windows"
        case "darwin":
                modifier = "+macos"
        case "android":
                modifier = "+android"
        default:
                // Unknown OS — fall back to table default.
                return targets[0]
        }

        compact := archShort + modifier
        t, err := ParseCompact(compact)
        if err != nil {
                return targets[0]
        }
        return t
}

// AllTargets returns all supported targets.
func AllTargets() []Target {
        result := make([]Target, len(targets))
        copy(result, targets)
        return result
}

// ValidateTarget checks if a target is valid.
func ValidateTarget(t Target) error {
        if t.Arch == "" {
                return fmt.Errorf("target must specify an architecture")
        }

        validArchs := map[string]bool{
                "x86_64": true, "aarch64": true, "rv64": true, "rv32": true, "wasm": true,
        }
        if !validArchs[t.Arch] {
                return fmt.Errorf("unsupported architecture: %s", t.Arch)
        }

        validOS := map[string]bool{
                "linux": true, "windows": true, "macos": true, "android": true, "none": true, "unknown": true,
        }
        if !validOS[t.OS] {
                return fmt.Errorf("unsupported OS: %s", t.OS)
        }

        validFormats := map[string]bool{
                "elf64": true, "pe64": true, "macho64": true, "wasm": true,
        }
        if !validFormats[t.Format] {
                return fmt.Errorf("unsupported output format: %s", t.Format)
        }

        // Check arch/os compatibility.
        if t.Arch == "wasm" && t.Format != "wasm" {
                return fmt.Errorf("wasm target requires wasm format")
        }

        return nil
}

// IsELF returns true for ELF-format targets.
func (t Target) IsELF() bool { return t.Format == "elf64" }

// IsPE returns true for PE-format targets.
func (t Target) IsPE() bool { return t.Format == "pe64" }

// IsMachO returns true for Mach-O-format targets.
func (t Target) IsMachO() bool { return t.Format == "macho64" }

// IsWASM returns true for WebAssembly targets.
func (t Target) IsWASM() bool { return t.Format == "wasm" }

// PtrSize returns the pointer size in bytes.
func (t Target) PtrSize() int {
        if t.Is64Bit {
                return 8
        }
        return 4
}

// StackAlign returns the stack alignment in bytes.
func (t Target) StackAlign() int {
        switch t.Arch {
        case "x86_64":
                return 16
        case "aarch64":
                return 16
        case "rv64":
                return 16
        case "rv32":
                return 16
        case "wasm":
                return 16
        default:
                return 16
        }
}

// PageAlign returns the page size.
func (t Target) PageAlign() int {
        switch t.OS {
        case "windows":
                return 4096
        default:
                return 4096
        }
}

// String returns a readable description using the compact name.
func (t Target) String() string {
        compact := t.CompactName()
        if compact != "" {
                return fmt.Sprintf("%s [%s]", compact, t.Triple)
        }
        return t.Triple
}

// HasFeature checks if a feature is enabled.
func (t Target) HasFeature(name string) bool {
        if t.Features == nil {
                return false
        }
        return t.Features[name]
}
