package target

import "testing"

func TestParseTarget(t *testing.T) {
    tests := []struct {
        triple string
        arch   string
        os     string
        format string
        is64   bool
        isBare bool
    }{
        {"x86_64-linux-gnu", "x86_64", "linux", "elf64", true, false},
        {"x86_64-windows-msvc", "x86_64", "windows", "pe64", true, false},
        {"x86_64-macos", "x86_64", "macos", "macho64", true, false},
        {"x86_64-unknown-none", "x86_64", "none", "elf64", true, true},
        {"aarch64-linux-gnu", "aarch64", "linux", "elf64", true, false},
        {"aarch64-linux-android", "aarch64", "android", "elf64", true, false},
        {"aarch64-windows-msvc", "aarch64", "windows", "pe64", true, false},
        {"aarch64-macos", "aarch64", "macos", "macho64", true, false},
        {"aarch64-unknown-none", "aarch64", "none", "elf64", true, true},
        {"rv64-linux-gnu", "rv64", "linux", "elf64", true, false},
        {"rv64-unknown-none", "rv64", "none", "elf64", true, true},
        {"rv32-unknown-none", "rv32", "none", "elf64", false, true},
        {"wasm32-unknown-unknown", "wasm", "unknown", "wasm", false, false},
    }

    for _, tc := range tests {
        t.Run(tc.triple, func(t *testing.T) {
            tgt, err := ParseTarget(tc.triple)
            if err != nil {
                t.Fatalf("ParseTarget(%q) error: %s", tc.triple, err)
            }
            if tgt.Arch != tc.arch {
                t.Errorf("arch: expected %q, got %q", tc.arch, tgt.Arch)
            }
            if tgt.OS != tc.os {
                t.Errorf("os: expected %q, got %q", tc.os, tgt.OS)
            }
            if tgt.Format != tc.format {
                t.Errorf("format: expected %q, got %q", tc.format, tgt.Format)
            }
            if tgt.Is64Bit != tc.is64 {
                t.Errorf("is64bit: expected %v, got %v", tc.is64, tgt.Is64Bit)
            }
            if tgt.IsBare != tc.isBare {
                t.Errorf("isBare: expected %v, got %v", tc.isBare, tgt.IsBare)
            }
        })
    }
}

func TestValidateTarget(t *testing.T) {
    tgt, _ := ParseTarget("x86_64-linux-gnu")
    if err := ValidateTarget(tgt); err != nil {
        t.Errorf("valid target should pass: %s", err)
    }

    invalid := Target{Arch: "mips"}
    if err := ValidateTarget(invalid); err == nil {
        t.Error("invalid arch should fail")
    }
}

func TestTargetHelpers(t *testing.T) {
    tgt, _ := ParseTarget("x86_64-linux-gnu")
    if !tgt.IsELF() {
        t.Error("x86_64-linux should be ELF")
    }
    if tgt.IsPE() || tgt.IsMachO() || tgt.IsWASM() {
        t.Error("x86_64-linux should not be PE/MachO/WASM")
    }

    tgt2, _ := ParseTarget("x86_64-windows-msvc")
    if !tgt2.IsPE() {
        t.Error("x86_64-windows should be PE")
    }

    tgt3, _ := ParseTarget("aarch64-macos")
    if !tgt3.IsMachO() {
        t.Error("aarch64-macos should be MachO")
    }

    tgt4, _ := ParseTarget("wasm32-unknown-unknown")
    if !tgt4.IsWASM() {
        t.Error("wasm target should be WASM")
    }
}

func TestTargetPtrSize(t *testing.T) {
    t64, _ := ParseTarget("x86_64-linux-gnu")
    if t64.PtrSize() != 8 {
        t.Errorf("64-bit target ptr size should be 8, got %d", t64.PtrSize())
    }

    t32, _ := ParseTarget("rv32-unknown-none")
    if t32.PtrSize() != 4 {
        t.Errorf("32-bit target ptr size should be 4, got %d", t32.PtrSize())
    }
}

func TestAllTargets(t *testing.T) {
    targets := AllTargets()
    if len(targets) == 0 {
        t.Error("should have at least one target")
    }
    for _, tgt := range targets {
        if tgt.Arch == "" {
            t.Errorf("target %q: missing arch", tgt.Triple)
        }
    }
}

func TestDefaultTarget(t *testing.T) {
    tgt := DefaultTarget()
    if tgt.Arch == "" {
        t.Error("default target should have an arch")
    }
}
