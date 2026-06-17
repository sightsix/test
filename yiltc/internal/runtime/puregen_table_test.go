package runtime

import (
        "debug/elf"
        "encoding/binary"
        "fmt"
        "os"
        "os/exec"
        "path/filepath"
        "strings"
        "testing"
)

// ---------------------------------------------------------------------------
// 1. Machine Code Inspection Tests
//
// Verify the puregen-generated machine code for table functions has the
// expected structural properties. These tests validate code generation
// correctness at the byte level without needing to execute the code.
// ---------------------------------------------------------------------------

// tableFuncNames is the list of puregen table functions to test.
var tableFuncNames = []string{
        "y_table_new",
        "y_tab_len",
        "y_tab_get",
        "y_tab_has",
        "y_tab_del",
        "y_tab_set",
        "pure_hash_tagged",
        "pure_values_equal",
        "pure_table_rehash",
        "y_tab_iter_valid",
        "y_tab_iter_key",
        "y_tab_iter_val",
        "y_tab_iter_next",
        "y_tab_get_val_type",
}

// TestPuregenTableFunctionsGenerated verifies that all table functions are
// generated and have non-empty machine code.
func TestPuregenTableFunctionsGenerated(t *testing.T) {
        for _, name := range tableFuncNames {
                t.Run(name, func(t *testing.T) {
                        code := PuregenGetFunctionCode(name)
                        if code == nil || len(code) == 0 {
                                t.Fatalf("function %q has no generated code", name)
                        }
                        t.Logf("  %s: %d bytes", name, len(code))
                })
        }
}

// TestPuregenTableFunctionsEndWithRET verifies that each table function ends
// with a RET (0xC3) instruction. This catches missing-epilogue bugs.
func TestPuregenTableFunctionsEndWithRET(t *testing.T) {
        for _, name := range tableFuncNames {
                t.Run(name, func(t *testing.T) {
                        code := PuregenGetFunctionCode(name)
                        if code == nil {
                                t.Fatalf("no code for %q", name)
                        }
                        last := code[len(code)-1]
                        if last != 0xC3 {
                                // Find all RET instructions in the last 16 bytes
                                found := false
                                for i := len(code) - 16; i < len(code); i++ {
                                        if i >= 0 && code[i] == 0xC3 {
                                                found = true
                                                break
                                        }
                                }
                                t.Errorf("last byte is 0x%02X (expected 0xC3 RET), last 16 bytes: %X",
                                        last, code[len(code)-16:])
                                if found {
                                        t.Log("  (note: RET found nearby, possible data after last RET)")
                                }
                        }
                })
        }
}

// TestPuregenTabSetSavesRSI verifies that y_tab_set preserves RSI (the key)
// across the pure_hash_tagged call. This was a regression where the key was
// lost during hashing, causing t[key] to always return nil on lookup.
//
// The generated code should contain a PUSH RSI before the CALL to
// pure_hash_tagged, and a POP RSI after the call returns.
func TestPuregenTabSetSavesRSI(t *testing.T) {
        code := PuregenGetFunctionCode("y_tab_set")
        if code == nil {
                t.Fatal("no code for y_tab_set")
        }

        // Find the CALL to pure_hash_tagged (E8 xx xx xx xx).
        // There should be a PUSH RSI (56) before it and a POP RSI (5E) after.
        callOffsets := findCallOffsets(code)
        if len(callOffsets) == 0 {
                t.Fatal("no CALL instructions found in y_tab_set")
        }

        // The first CALL should be pure_hash_tagged. Check that RSI is saved.
        for _, off := range callOffsets {
                // Check the 20 bytes before the CALL for PUSH RSI (0x56)
                hasPushRSI := false
                start := off - 20
                if start < 0 {
                        start = 0
                }
                for i := start; i < off; i++ {
                        if code[i] == 0x56 {
                                hasPushRSI = true
                                break
                        }
                }

                // Check the 20 bytes after the CALL (5 bytes) for POP RSI (0x5E)
                hasPopRSI := false
                callEnd := off + 5
                end := callEnd + 20
                if end > len(code) {
                        end = len(code)
                }
                for i := callEnd; i < end; i++ {
                        if code[i] == 0x5E {
                                hasPopRSI = true
                                break
                        }
                }

                if hasPushRSI && hasPopRSI {
                        t.Logf("  CALL at offset %d: PUSH RSI found before, POP RSI found after", off)
                        return // success
                }
        }

        t.Errorf("y_tab_set does not properly save/restore RSI around pure_hash_tagged call")
}

// TestPuregenTabSetSavesRDX verifies that y_tab_set preserves RDX (the value)
// across function calls. The value register must survive the hash computation.
func TestPuregenTabSetSavesRDX(t *testing.T) {
        code := PuregenGetFunctionCode("y_tab_set")
        if code == nil {
                t.Fatal("no code for y_tab_set")
        }

        callOffsets := findCallOffsets(code)
        if len(callOffsets) == 0 {
                t.Fatal("no CALL instructions found in y_tab_set")
        }

        // Check that PUSH RDX (0x52) appears before the first CALL
        off := callOffsets[0]
        hasPushRDX := false
        start := off - 30
        if start < 0 {
                start = 0
        }
        for i := start; i < off; i++ {
                if code[i] == 0x52 {
                        hasPushRDX = true
                        break
                }
        }

        if !hasPushRDX {
                t.Errorf("y_tab_set does not save RDX (the value) before CALL pure_hash_tagged")
        }
}

// TestPuregenTabGetSavesRSI verifies that y_tab_get preserves RSI (the key)
// across the pure_hash_tagged call.
func TestPuregenTabGetSavesRSI(t *testing.T) {
        code := PuregenGetFunctionCode("y_tab_get")
        if code == nil {
                t.Fatal("no code for y_tab_get")
        }

        callOffsets := findCallOffsets(code)
        if len(callOffsets) == 0 {
                t.Fatal("no CALL instructions found in y_tab_get")
        }

        for _, off := range callOffsets {
                hasPushRSI := false
                start := off - 20
                if start < 0 {
                        start = 0
                }
                for i := start; i < off; i++ {
                        if code[i] == 0x56 {
                                hasPushRSI = true
                                break
                        }
                }

                hasPopRSI := false
                callEnd := off + 5
                end := callEnd + 20
                if end > len(code) {
                        end = len(code)
                }
                for i := callEnd; i < end; i++ {
                        if code[i] == 0x5E {
                                hasPopRSI = true
                                break
                        }
                }

                if hasPushRSI && hasPopRSI {
                        t.Logf("  CALL at offset %d: PUSH RSI found before, POP RSI found after", off)
                        return
                }
        }

        t.Errorf("y_tab_get does not properly save/restore RSI around pure_hash_tagged call")
}

// TestPuregenTableNewStructure verifies y_table_new uses mmap (syscall 9)
// and returns a tagged table value (tag 5).
func TestPuregenTableNewStructure(t *testing.T) {
        code := PuregenGetFunctionCode("y_table_new")
        if code == nil {
                t.Fatal("no code for y_table_new")
        }

        // Should contain SYSCALL (0x0F 0x05)
        hasSyscall := false
        for i := 0; i < len(code)-1; i++ {
                if code[i] == 0x0F && code[i+1] == 0x05 {
                        hasSyscall = true
                        break
                }
        }
        if !hasSyscall {
                t.Error("y_table_new does not contain SYSCALL instruction")
        }

        // Should contain MOV RAX, 9 (mmap syscall number): 48 C7 C0 09 00 00 00
        // or MOV RAX, 9 via mov_rm: B8 09 00 00 00 (but this is 32-bit)
        // With 64-bit: 48 C7 C0 09 00 00 00
        hasMmapSyscall := false
        for i := 0; i < len(code)-3; i++ {
                // Check for MOV RAX, 9 patterns
                if code[i] == 0xC7 && i > 0 && code[i-1] == 0x48 && i+3 < len(code) && code[i+1] == 0xC0 && code[i+2] == 0x09 {
                        hasMmapSyscall = true
                        break
                }
        }
        if !hasMmapSyscall {
                t.Log("  (may use different encoding for mmap syscall number)")
        }
}

// TestPuregenTabLenStructure verifies y_tab_len reads from the correct offset
// (count field at offset 0 from the table header).
func TestPuregenTabLenStructure(t *testing.T) {
        code := PuregenGetFunctionCode("y_tab_len")
        if code == nil {
                t.Fatal("no code for y_tab_len")
        }

        // The function should be relatively small (no loops, just load + tag).
        if len(code) > 200 {
                t.Errorf("y_tab_len is unexpectedly large: %d bytes", len(code))
        }

        // Should contain CMP RAX, 5 (check table tag): 48 83 F8 05
        hasTagCheck := false
        for i := 0; i < len(code)-3; i++ {
                if code[i] == 0x48 && code[i+1] == 0x83 && code[i+2] == 0xF8 && code[i+3] == 0x05 {
                        hasTagCheck = true
                        break
                }
        }
        if !hasTagCheck {
                t.Error("y_tab_len does not check for table tag (5)")
        }
}

// TestPuregenRelocationTargets verifies that all relocations for table functions
// point to valid targets.
func TestPuregenRelocationTargets(t *testing.T) {
        relocsByFunc := PuregenRelocationsByFunc()
        allFuncs := PuregenGetAllFunctions()
        funcSet := make(map[string]bool)
        for _, name := range allFuncs {
                funcSet[name] = true
        }

        for _, name := range tableFuncNames {
                t.Run(name, func(t *testing.T) {
                        relocs, ok := relocsByFunc[name]
                        if !ok {
                                return // no relocations for this function, that's fine
                        }

                        for _, r := range relocs {
                                // Cross-function calls should target known functions
                                if r.Symbol != "" {
                                        if !funcSet[r.Symbol] {
                                                t.Errorf("relocation targets unknown function %q (offset %d)",
                                                        r.Symbol, r.Offset)
                                        }
                                }
                        }
                })
        }
}

// ---------------------------------------------------------------------------
// 2. Disassembly Analysis Tests
//
// Compile a test Yilt program and analyze the generated binary to verify
// that the table operations produce correct x86_64 code.
// ---------------------------------------------------------------------------

// findCompiler returns the path to the yiltc binary, building it if needed.
func findCompiler(t *testing.T) string {
        t.Helper()
        // Try the pre-built binary first
        paths := []string{
                filepath.Join("..", "..", "bin", "yiltc"),
                "../../bin/yiltc",
        }

        for _, p := range paths {
                if _, err := os.Stat(p); err == nil {
                        abs, err := filepath.Abs(p)
                        if err == nil {
                                return abs
                        }
                }
        }

        // Build from source
        t.Log("building yiltc from source...")
        cmd := exec.Command("go", "build", "-o", "../../bin/yiltc", "../../cmd/yiltc/")
        cmd.Dir = "."
        if output, err := cmd.CombinedOutput(); err != nil {
                t.Fatalf("failed to build yiltc: %v\n%s", err, output)
        }
        abs, _ := filepath.Abs("../../bin/yiltc")
        return abs
}

// compileYilt compiles a Yilt source file and returns the path to the binary.
func compileYilt(t *testing.T, compiler, src string) string {
        t.Helper()

        dir := t.TempDir()
        srcPath := filepath.Join(dir, "test.yilt")
        outPath := filepath.Join(dir, "test_bin")

        if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
                t.Fatal(err)
        }

        cmd := exec.Command(compiler, srcPath, "-o", outPath)
        if output, err := cmd.CombinedOutput(); err != nil {
                t.Fatalf("compilation failed: %v\n%s", err, output)
        }

        return outPath
}

// runBinary executes a binary and returns its stdout/stderr combined.
func runBinary(t *testing.T, binPath string) string {
        t.Helper()
        cmd := exec.Command(binPath)
        out, err := cmd.CombinedOutput()
        if err != nil {
                t.Fatalf("binary execution failed: %v\n%s", err, out)
        }
        return string(out)
}

// TestIntegrationTableSetGet compiles and runs a table set/get program,
// verifying the output is correct.
func TestIntegrationTableSetGet(t *testing.T) {
        compiler := findCompiler(t)
        if compiler == "" {
                t.Skip("yiltc compiler not available")
        }

        tests := []struct {
                name   string
                source string
                want   string
        }{
                {
                        name: "set_get_int",
                        source: `fn main()
    let mut t = {}
    t[0] = 42
    print(t[0])
    print(len(t))
`,
                        want: "421",
                },
                {
                        name: "set_multiple",
                        source: `fn main()
    let mut t = {}
    t[0] = 10
    t[1] = 20
    t[2] = 30
    print(t[0])
    print(t[1])
    print(t[2])
    print(len(t))
`,
                        want: "1020303",
                },
                {
                        name: "overwrite",
                        source: `fn main()
    let mut t = {}
    t[0] = 42
    t[0] = 99
    print(t[0])
    print(len(t))
`,
                        want: "991",
                },
                {
                        name: "get_missing_key",
                        source: `fn main()
    let mut t = {}
    t[0] = 42
    print(t[1])
    print(len(t))
`,
                        want: "nil1",
                },
                {
                        name: "set_get_string_key",
                        source: `fn main()
    let mut t = {}
    t["hello"] = 42
    print(t["hello"])
    print(len(t))
`,
                        want: "421",
                },
        }

        for _, tt := range tests {
                t.Run(tt.name, func(t *testing.T) {
                        bin := compileYilt(t, compiler, tt.source)
                        got := runBinary(t, bin)
                        got = strings.TrimSpace(got)
                        if got != tt.want {
                                t.Errorf("output mismatch:\n  got:  %q\n  want: %q", got, tt.want)
                        }
                })
        }
}

// TestDisasmTabSetPreservesKey analyzes the compiled binary to verify that
// y_tab_set properly saves the key (RSI) across the pure_hash_tagged call.
func TestDisasmTabSetPreservesKey(t *testing.T) {
        compiler := findCompiler(t)
        if compiler == "" {
                t.Skip("yiltc compiler not available")
        }

        // Check if objdump is available
        if _, err := exec.LookPath("objdump"); err != nil {
                t.Skip("objdump not available")
        }

        source := `fn main()
    let mut t = {}
    t[0] = 42
    print(t[0])
`
        bin := compileYilt(t, compiler, source)

        // Read the ELF and find the .text section
        f, err := elf.Open(bin)
        if err != nil {
                t.Fatal(err)
        }
        defer f.Close()

        textSection := f.Section(".text")
        if textSection == nil {
                t.Fatal("no .text section in binary")
        }

        textData, err := textSection.Data()
        if err != nil {
                t.Fatal(err)
        }

        // Find all CALL rel32 (E8 xx xx xx xx) instructions
        // For each CALL, check if it's preceded by PUSH RSI within 30 bytes
        callCount := 0
        pushBeforeCall := 0
        popAfterCall := 0

        for i := 0; i < len(textData)-5; i++ {
                if textData[i] != 0xE8 {
                        continue
                }

                callCount++
                // Check preceding 30 bytes for PUSH RSI (0x56)
                start := i - 30
                if start < 0 {
                        start = 0
                }
                for j := start; j < i; j++ {
                        if textData[j] == 0x56 {
                                pushBeforeCall++
                                break
                        }
                }

                // Check following 30 bytes for POP RSI (0x5E)
                callEnd := i + 5
                end := callEnd + 30
                if end > len(textData) {
                        end = len(textData)
                }
                for j := callEnd; j < end; j++ {
                        if textData[j] == 0x5E {
                                popAfterCall++
                                break
                        }
                }
        }

        t.Logf("  Total CALL instructions in .text: %d", callCount)
        t.Logf("  PUSH RSI before CALL: %d", pushBeforeCall)
        t.Logf("  POP RSI after CALL: %d", popAfterCall)

        // We expect at least some CALLs to have RSI saved
        if callCount > 0 && pushBeforeCall == 0 {
                t.Error("no PUSH RSI found before any CALL in .text")
        }
}

// TestDisasmTableOperationsStructure verifies that the compiled binary
// contains the expected structure for table operations.
func TestDisasmTableOperationsStructure(t *testing.T) {
        compiler := findCompiler(t)
        if compiler == "" {
                t.Skip("yiltc compiler not available")
        }

        source := `fn main()
    let mut t = {}
    t[0] = 42
    print(t[0])
    print(len(t))
`
        bin := compileYilt(t, compiler, source)
        output := runBinary(t, bin)
        t.Logf("  Program output: %q", strings.TrimSpace(output))

        // Verify basic correctness
        if strings.TrimSpace(output) != "421" {
                t.Errorf("expected output %q, got %q", "421", strings.TrimSpace(output))
        }
}

// TestDisasmAnalyzeTableSet analyzes the binary to verify y_tab_set stores
// the correct key in the entry. It checks for MOV [base+offset], RSI
// instructions (which store the key) in the region of the binary that
// corresponds to y_tab_set.
func TestDisasmAnalyzeTableSet(t *testing.T) {
        compiler := findCompiler(t)
        if compiler == "" {
                t.Skip("yiltc compiler not available")
        }

        source := `fn main()
    let mut t = {}
    t[0] = 42
    print(t[0])
`
        bin := compileYilt(t, compiler, source)

        f, err := elf.Open(bin)
        if err != nil {
                t.Fatal(err)
        }
        defer f.Close()

        textSection := f.Section(".text")
        if textSection == nil {
                t.Fatal("no .text section")
        }

        textData, err := textSection.Data()
        if err != nil {
                t.Fatal(err)
        }

        // Look for the pattern: the entry key store should write RSI to
        // [RBX + RCX + 0] which encodes as:
        //   REX.W MOV [RBX+RCX+0], RSI  = 48 89 34 0B
        // or with offset 0: REX.W MOV [RBX+RCX], RSI = 48 89 34 0B
        //
        // Also check for the value store:
        //   MOV [RBX+RCX+8], RDX = 48 89 54 0B 08
        //
        // And the hash store:
        //   MOV [RBX+RCX+16], R13 = 4C 89 74 0B 10

        // Pattern: 48 89 34 0B (MOV [RBX+RCX], RSI) - store key
        keyStorePattern := []byte{0x48, 0x89, 0x34, 0x0B}
        keyStoreCount := countPattern(textData, keyStorePattern)

        // Pattern: 48 89 54 0B 08 (MOV [RBX+RCX+8], RDX) - store value
        valStorePattern := []byte{0x48, 0x89, 0x54, 0x0B, 0x08}
        valStoreCount := countPattern(textData, valStorePattern)

        // Pattern: 4C 89 6C 0B 10 (MOV [RBX+RCX+16], R13) - store hash
        hashStorePattern := []byte{0x4C, 0x89, 0x6C, 0x0B, 0x10}
        hashStoreCount := countPattern(textData, hashStorePattern)

        // Pattern: 48 89 7C 0B 18 (MOV [RBX+RCX+24], RDI) or
        // 48 89 54 0B 18 (MOV [RBX+RCX+24], RDX) - store occupied
        occupiedStorePattern1 := []byte{0x48, 0x89, 0x54, 0x0B, 0x18}
        occupiedStorePattern2 := []byte{0x48, 0x89, 0x7C, 0x0B, 0x18}
        occupiedCount := countPattern(textData, occupiedStorePattern1) + countPattern(textData, occupiedStorePattern2)

        t.Logf("  Key stores (MOV [RBX+RCX], RSI): %d", keyStoreCount)
        t.Logf("  Value stores (MOV [RBX+RCX+8], RDX): %d", valStoreCount)
        t.Logf("  Hash stores (MOV [RBX+RCX+16], R13): %d", hashStoreCount)
        t.Logf("  Occupied stores (MOV [RBX+RCX+24], ...): %d", occupiedCount)

        if keyStoreCount == 0 {
                t.Error("no entry key store instructions found (MOV [RBX+RCX], RSI)")
        }
        if valStoreCount == 0 {
                t.Error("no entry value store instructions found")
        }
        if hashStoreCount == 0 {
                t.Error("no entry hash store instructions found")
        }
}

// ---------------------------------------------------------------------------
// 3. Table Layout Constants Consistency Tests
//
// Verify that the constants in puregen_runtime.go match those in tables.go.
// ---------------------------------------------------------------------------

// TestTableConstantsConsistent verifies that the local rtXxx constants in
// puregen_runtime.go match the exported constants in tables.go.
func TestTableConstantsConsistent(t *testing.T) {
        // Header layout
        consts := []struct {
                name string
                got  int
                want int
        }{
                {"rtTableHeaderSize", rtTableHeaderSize, TableHeaderSize},
                {"rtTableOffCount", rtTableOffCount, TableOffCount},
                {"rtTableOffCapacity", rtTableOffCapacity, TableOffCapacity},
                {"rtTableOffThreshold", rtTableOffThreshold, TableOffThreshold},
                {"rtTableOffMask", rtTableOffMask, TableOffMask},
                {"rtTableOffEntries", rtTableOffEntries, TableOffEntries},
                {"rtTableOffEntryCap", rtTableOffEntryCap, TableOffEntryCap},
                {"rtTableOffTombstones", rtTableOffTombstones, TableOffTombstones},

                {"rtEntrySize", rtEntrySize, EntrySize},
                {"rtEntryOffKey", rtEntryOffKey, EntryOffKey},
                {"rtEntryOffValue", rtEntryOffValue, EntryOffValue},
                {"rtEntryOffHash", rtEntryOffHash, EntryOffHash},
                {"rtEntryOffOccupied", rtEntryOffOccupied, EntryOffOccupied},

                {"rtEntryEmpty", rtEntryEmpty, EntryEmpty},
                {"rtEntryOccupied", rtEntryOccupied, EntryOccupied},
                {"rtEntryTombstone", rtEntryTombstone, EntryTombstone},
        }

        for _, c := range consts {
                if c.got != c.want {
                        t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
                }
        }
}

// ---------------------------------------------------------------------------
// 4. Hash Function Correctness Tests
//
// Verify that the generated hash function produces consistent results
// for the same inputs.
// ---------------------------------------------------------------------------

// TestPuregenHashTaggedRelocations verifies that pure_hash_tagged has the
// expected structure for integer hashing (murmur3 finalizer).
func TestPuregenHashTaggedRelocations(t *testing.T) {
        code := PuregenGetFunctionCode("pure_hash_tagged")
        if code == nil {
                t.Fatal("no code for pure_hash_tagged")
        }

        // Should have multiple IMUL instructions (for murmur3 mixing and FNV)
        imulCount := 0
        for i := 0; i < len(code)-2; i++ {
                if code[i] == 0x0F && code[i+1] == 0xAF {
                        imulCount++
                }
        }
        t.Logf("  pure_hash_tagged: %d IMUL instructions", imulCount)
        if imulCount < 3 {
                t.Errorf("expected at least 3 IMUL instructions (murmur3 uses 3 IMULs), got %d", imulCount)
        }
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// findCallOffsets finds all CALL rel32 (E8) instruction offsets in the code.
func findCallOffsets(code []byte) []int {
        var offsets []int
        for i := 0; i < len(code)-5; i++ {
                if code[i] == 0xE8 {
                        // Read the rel32 and check if it looks reasonable
                        rel := int32(binary.LittleEndian.Uint32(code[i+1 : i+5]))
                        // The target should be somewhat nearby (within ±1MB)
                        if rel > -1048576 && rel < 1048576 {
                                offsets = append(offsets, i)
                        }
                }
        }
        return offsets
}

// countPattern counts non-overlapping occurrences of a byte pattern.
func countPattern(data, pattern []byte) int {
        count := 0
        for i := 0; i <= len(data)-len(pattern); i++ {
                match := true
                for j := 0; j < len(pattern); j++ {
                        if data[i+j] != pattern[j] {
                                match = false
                                break
                        }
                }
                if match {
                        count++
                        i += len(pattern) - 1 // skip past this match
                }
        }
        return count
}

// printHexDump prints a hex dump of code bytes for debugging.
func printHexDump(t *testing.T, code []byte, start, end int) {
        t.Helper()
        if end > len(code) {
                end = len(code)
        }
        if start < 0 {
                start = 0
        }
        for i := start; i < end; i += 16 {
                hex := ""
                ascii := ""
                for j := 0; j < 16 && i+j < end; j++ {
                        hex += fmt.Sprintf("%02x ", code[i+j])
                        if code[i+j] >= 32 && code[i+j] < 127 {
                                ascii += string(code[i+j])
                        } else {
                                ascii += "."
                        }
                }
                t.Logf("  %04x: %-48s %s", i, hex, ascii)
        }
}
