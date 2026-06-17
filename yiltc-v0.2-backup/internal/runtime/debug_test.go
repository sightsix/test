package runtime

import (
    "encoding/binary"
    "fmt"
    "testing"
)

func TestDebugTabGetCode(t *testing.T) {
    code := PuregenGetFunctionCode("y_tab_get")
    if len(code) == 0 {
        t.Fatal("no code for y_tab_get")
    }
    
    // Find all CALL E8 instructions and check the displacement
    for i := 0; i < len(code)-4; i++ {
        if code[i] == 0xE8 {
            disp := int32(binary.LittleEndian.Uint32(code[i+1:i+5]))
            targetOffset := int64(i) + 5 + int64(disp)
            t.Logf("  CALL at offset %d: disp=%d (0x%x), target=%d (0x%x)", i, disp, uint32(disp), targetOffset, targetOffset)
        }
    }
    
    // Find all MOV with SIB + disp8 encoding that access entries
    // Look for the occupied read pattern
    for i := 0; i < len(code)-5; i++ {
        // Pattern: 4C 8B 7C 0B 18 = MOV R15, [RBX+RCX+24] (read occupied)
        if i+5 <= len(code) && code[i] == 0x4C && code[i+1] == 0x8B && code[i+2] == 0x7C && code[i+3] == 0x0B && code[i+4] == 0x18 {
            t.Logf("  Found MOV R15, [RBX+RCX+24] at offset %d (read occupied)", i)
        }
        // Pattern: 48 8B 54 0B 10 = MOV RDX, [RBX+RCX+16] (read hash)
        if i+5 <= len(code) && code[i] == 0x48 && code[i+1] == 0x8B && code[i+2] == 0x54 && code[i+3] == 0x0B && code[i+4] == 0x10 {
            t.Logf("  Found MOV RDX, [RBX+RCX+16] at offset %d (read hash)", i)
        }
        // Pattern: 48 8B 34 0B = MOV RSI, [RBX+RCX+0] (read key)
        if i+4 <= len(code) && code[i] == 0x48 && code[i+1] == 0x8B && code[i+2] == 0x34 && code[i+3] == 0x0B {
            t.Logf("  Found MOV RSI, [RBX+RCX+0] at offset %d (read key)", i)
        }
        // Pattern: 48 8B 44 0B 08 = MOV RAX, [RBX+RCX+8] (read value)
        if i+5 <= len(code) && code[i] == 0x48 && code[i+1] == 0x8B && code[i+2] == 0x44 && code[i+3] == 0x0B && code[i+4] == 0x08 {
            t.Logf("  Found MOV RAX, [RBX+RCX+8] at offset %d (read value)", i)
        }
    }
    
    fmt.Printf("  Total code size: %d bytes\n", len(code))
}

func TestDebugTabSetCode(t *testing.T) {
    code := PuregenGetFunctionCode("y_tab_set")
    if len(code) == 0 {
        t.Fatal("no code for y_tab_set")
    }
    
    // Find all CALL E8 instructions
    for i := 0; i < len(code)-4; i++ {
        if code[i] == 0xE8 {
            disp := int32(binary.LittleEndian.Uint32(code[i+1:i+5]))
            targetOffset := int64(i) + 5 + int64(disp)
            t.Logf("  CALL at offset %d: disp=%d (0x%x), target=%d (0x%x)", i, disp, uint32(disp), targetOffset, targetOffset)
        }
    }
    
    // Find store patterns
    for i := 0; i < len(code)-4; i++ {
        // Pattern: 48 89 34 0B = MOV [RBX+RCX+0], RSI (store key)
        if i+4 <= len(code) && code[i] == 0x48 && code[i+1] == 0x89 && code[i+2] == 0x34 && code[i+3] == 0x0B {
            t.Logf("  Found MOV [RBX+RCX+0], RSI at offset %d (store key)", i)
        }
        // Pattern: 48 89 54 0B 08 = MOV [RBX+RCX+8], RDX (store value)
        if i+5 <= len(code) && code[i] == 0x48 && code[i+1] == 0x89 && code[i+2] == 0x54 && code[i+3] == 0x0B && code[i+4] == 0x08 {
            t.Logf("  Found MOV [RBX+RCX+8], RDX at offset %d (store value)", i)
        }
        // Pattern: 4C 89 6C 0B 10 = MOV [RBX+RCX+16], R13 (store hash)
        if i+5 <= len(code) && code[i] == 0x4C && code[i+1] == 0x89 && code[i+2] == 0x6C && code[i+3] == 0x0B && code[i+4] == 0x10 {
            t.Logf("  Found MOV [RBX+RCX+16], R13 at offset %d (store hash)", i)
        }
        // Pattern: 48 89 54 0B 18 = MOV [RBX+RCX+24], RDX (store occupied)
        if i+5 <= len(code) && code[i] == 0x48 && code[i+1] == 0x89 && code[i+2] == 0x54 && code[i+3] == 0x0B && code[i+4] == 0x18 {
            t.Logf("  Found MOV [RBX+RCX+24], RDX at offset %d (store occupied)", i)
        }
    }
    
    fmt.Printf("  Total code size: %d bytes\n", len(code))
}
