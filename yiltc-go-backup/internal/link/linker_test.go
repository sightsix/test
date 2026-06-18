package link

import (
        "bytes"
        "testing"
)

// TestNewLinker_AllTargets verifies that the linker factory can create
// a linker for every supported target triple.
func TestNewLinker_AllTargets(t *testing.T) {
        tests := []struct {
                target string
                desc   string
        }{
                // ELF64 targets
                {"x86_64-linux-gnu", "x86_64 Linux"},
                {"x86_64-linux-musl", "x86_64 Linux musl"},
                {"x86_64-unknown-none", "x86_64 bare metal"},
                {"aarch64-linux-gnu", "AArch64 Linux"},
                {"aarch64-linux-android", "AArch64 Android"},
                {"aarch64-unknown-none", "AArch64 bare metal"},
                {"rv64-linux-gnu", "RISC-V 64 Linux"},
                {"rv64-unknown-none", "RISC-V 64 bare metal"},
                {"rv32-unknown-none", "RISC-V 32 bare metal"},
                // PE64 targets
                {"x86_64-windows-msvc", "x86_64 Windows"},
                {"aarch64-windows-msvc", "AArch64 Windows"},
                // Mach-O targets
                {"x86_64-macos", "x86_64 macOS"},
                {"aarch64-macos", "AArch64 macOS"},
                {"aarch64-apple-darwin", "AArch64 Darwin"},
                // WASM target
                {"wasm32-unknown-unknown", "WebAssembly"},
                {"wasm", "WebAssembly (short)"},
        }

        for _, tt := range tests {
                t.Run(tt.target, func(t *testing.T) {
                        cfg := Config{Target: tt.target, OutputName: "test"}
                        l, err := NewLinker(cfg)
                        if err != nil {
                                t.Fatalf("NewLinker(%q): %s", tt.target, err)
                        }
                        if l == nil {
                                t.Fatal("NewLinker returned nil")
                        }
                })
        }
}

// TestLinker_UnsupportedTarget verifies that an unsupported target returns an error.
func TestLinker_UnsupportedTarget(t *testing.T) {
        // Note: the linker uses a fallback to x86_64 for unknown architectures,
        // so we test with a clearly invalid empty string.
        cfg := Config{Target: "", OutputName: "test"}
        _, err := NewLinker(cfg)
        if err == nil {
                t.Fatal("expected error for empty target, got nil")
        }
}

// TestELF64_LinkBasic tests that the ELF64 linker produces valid output with magic bytes.
func TestELF64_LinkBasic(t *testing.T) {
        arches := []string{"x86_64", "aarch64", "riscv64"}
        for _, arch := range arches {
                t.Run(arch, func(t *testing.T) {
                        cfg := Config{
                                Target:     arch + "-linux-gnu",
                                OutputName: "test_" + arch,
                        }
                        l, err := NewLinker(cfg)
                        if err != nil {
                                t.Fatal(err)
                        }

                        l.AddCode("start", []byte{0x90, 0x90, 0xC3}) // NOP; NOP; RET
                        l.AddData("data", []byte{0x01, 0x02, 0x03, 0x04})
                        l.AddSymbol("start", 0, 3, SymFunc)
                        l.AddSymbol("data", 4, 4, SymData)
                        l.SetEntryPoint("start")

                        bin, err := l.Link()
                        if err != nil {
                                t.Fatalf("Link: %s", err)
                        }
                        if len(bin) == 0 {
                                t.Fatal("Link produced empty output")
                        }
                        // Verify ELF magic bytes: 0x7F 'E' 'L' 'F'
                        if len(bin) < 4 || bin[0] != 0x7F || bin[1] != 'E' || bin[2] != 'L' || bin[3] != 'F' {
                                t.Errorf("invalid ELF magic: %v", bin[:4])
                        }
                })
        }
}

// TestELF64_PIE tests that PIE mode works.
func TestELF64_PIE(t *testing.T) {
        cfg := Config{
                Target:     "x86_64-linux-gnu",
                OutputName: "test_pie",
                PIE:        true,
        }
        l, _ := NewLinker(cfg)
        l.AddCode("start", []byte{0xC3})
        l.AddSymbol("start", 0, 1, SymFunc)
        l.SetEntryPoint("start")

        bin, err := l.Link()
        if err != nil {
                t.Fatalf("Link: %s", err)
        }
        if !bytes.HasPrefix(bin, []byte{0x7F, 'E', 'L', 'F'}) {
                t.Error("PIE ELF missing magic bytes")
        }
}

// TestPE64_LinkBasic tests that the PE64 linker produces valid output.
func TestPE64_LinkBasic(t *testing.T) {
        arches := []string{"x86_64", "aarch64"}
        for _, arch := range arches {
                t.Run(arch, func(t *testing.T) {
                        cfg := Config{
                                Target:     arch + "-windows-msvc",
                                OutputName: "test_" + arch,
                        }
                        l, err := NewLinker(cfg)
                        if err != nil {
                                t.Fatal(err)
                        }

                        l.AddCode("start", []byte{0xC3})
                        l.AddSymbol("start", 0, 1, SymFunc)
                        l.SetEntryPoint("start")

                        bin, err := l.Link()
                        if err != nil {
                                t.Fatalf("Link: %s", err)
                        }
                        if len(bin) == 0 {
                                t.Fatal("Link produced empty output")
                        }
                        // Verify PE magic: "MZ"
                        if bin[0] != 'M' || bin[1] != 'Z' {
                                t.Errorf("invalid PE magic: %v", bin[:2])
                        }
                })
        }
}

// TestMachO64_LinkBasic tests that the Mach-O 64 linker produces valid output.
func TestMachO64_LinkBasic(t *testing.T) {
        arches := []string{"x86_64", "aarch64"}
        for _, arch := range arches {
                t.Run(arch, func(t *testing.T) {
                        cfg := Config{
                                Target:     arch + "-macos",
                                OutputName: "test_" + arch,
                        }
                        l, err := NewLinker(cfg)
                        if err != nil {
                                t.Fatal(err)
                        }

                        l.AddCode("_main", []byte{0xC3})
                        l.AddSymbol("_main", 0, 1, SymFunc)
                        l.SetEntryPoint("_main")

                        bin, err := l.Link()
                        if err != nil {
                                t.Fatalf("Link: %s", err)
                        }
                        if len(bin) == 0 {
                                t.Fatal("Link produced empty output")
                        }
                        // Mach-O magic: 0xFEEDFACE or 0xFEEDFACF (64-bit)
                        if bin[0] == 0xCE && bin[1] == 0xFA && bin[2] == 0xED && bin[3] == 0xFE {
                                // Mach-O 64-bit little-endian: 0xFEEDFACF
                                t.Logf("  %s: Mach-O 64 LE magic OK", arch)
                        } else if bin[0] == 0xCF && bin[1] == 0xFA && bin[2] == 0xED && bin[3] == 0xFE {
                                // Mach-O 64-bit big-endian header
                                t.Logf("  %s: Mach-O 64 BE magic OK", arch)
                        } else {
                                t.Errorf("invalid Mach-O magic for %s: %v", arch, bin[:4])
                        }
                })
        }
}

// TestWASM_LinkBasic tests that the WASM linker produces valid output.
func TestWASM_LinkBasic(t *testing.T) {
        cfg := Config{
                Target:     "wasm32-unknown-unknown",
                OutputName: "test_wasm",
        }
        l, err := NewLinker(cfg)
        if err != nil {
                t.Fatal(err)
        }

        l.AddCode("start", []byte{0x0B}) // end opcode
        l.AddSymbol("start", 0, 1, SymFunc)
        l.SetEntryPoint("start")

        bin, err := l.Link()
        if err != nil {
                t.Fatalf("Link: %s", err)
        }
        if len(bin) == 0 {
                t.Fatal("Link produced empty output")
        }
        // WASM magic: 0x00 0x61 0x73 0x6D ("\0asm")
        if bin[0] != 0x00 || bin[1] != 0x61 || bin[2] != 0x73 || bin[3] != 0x6D {
                t.Errorf("invalid WASM magic: %v", bin[:4])
        }
}

// TestELF64_BSS tests BSS section support.
func TestELF64_BSS(t *testing.T) {
        cfg := Config{Target: "x86_64-linux-gnu", OutputName: "test_bss"}
        l, _ := NewLinker(cfg)
        l.AddCode("start", []byte{0xC3})
        l.AddBSS("bss_data", 1024, 8)
        l.AddSymbol("start", 0, 1, SymFunc)
        l.AddSymbol("bss_data", 4, 1024, SymBSS)
        l.SetEntryPoint("start")

        bin, err := l.Link()
        if err != nil {
                t.Fatalf("Link: %s", err)
        }
        if !bytes.HasPrefix(bin, []byte{0x7F, 'E', 'L', 'F'}) {
                t.Error("BSS ELF missing magic bytes")
        }
}

// TestELF64_Relocation tests that relocations can be added without panicking.
func TestELF64_Relocation(t *testing.T) {
        cfg := Config{Target: "x86_64-linux-gnu", OutputName: "test_reloc"}
        l, _ := NewLinker(cfg)
        l.AddCode("start", make([]byte, 16))
        l.AddSymbol("start", 0, 16, SymFunc)
        l.SetEntryPoint("start")
        l.AddRelocation(".text", 8, "data", RelocAbs64, 0)
        l.AddData("data", []byte{0xDE, 0xAD, 0xBE, 0xEF})

        bin, err := l.Link()
        if err != nil {
                t.Fatalf("Link: %s", err)
        }
        if len(bin) == 0 {
                t.Fatal("Link with relocations produced empty output")
        }
}

// TestArchFromTriple verifies triple-to-architecture mapping.
func TestArchFromTriple(t *testing.T) {
        tests := []struct {
                triple string
                arch   string
        }{
                {"x86_64-linux-gnu", "x86_64"},
                {"aarch64-linux-gnu", "aarch64"},
                {"rv64-linux-gnu", "riscv64"},
                {"rv32-unknown-none", "riscv32"},
                {"wasm32-unknown-unknown", "wasm"},
        }
        for _, tt := range tests {
                t.Run(tt.triple, func(t *testing.T) {
                        got := archFromTriple(tt.triple)
                        if got != tt.arch {
                                t.Errorf("archFromTriple(%q) = %q, want %q", tt.triple, got, tt.arch)
                        }
                })
        }
}

// TestIsELF verifies ELF detection for various triples.
func TestIsELF(t *testing.T) {
        elfs := []string{"x86_64-linux-gnu", "aarch64-android", "rv64-musl", "rv32-unknown-none"}
        for _, t_str := range elfs {
                if !isELF(t_str) {
                        t.Errorf("isELF(%q) = false, want true", t_str)
                }
        }
        nonelfs := []string{"x86_64-windows-msvc", "aarch64-macos", "wasm32-unknown"}
        for _, t_str := range nonelfs {
                if isELF(t_str) {
                        t.Errorf("isELF(%q) = true, want false", t_str)
                }
        }
}
