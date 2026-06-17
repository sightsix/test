package runtime

import (
    
    "fmt"
    "testing"
)

func hexdump(code []byte, start, end int) string {
    result := ""
    for i := start; i < end && i < len(code); i++ {
        result += fmt.Sprintf("%02x ", code[i])
        if (i-start+1) % 16 == 0 {
            result += "\n"
        }
    }
    return result
}

func TestDebugTabGetCallContext(t *testing.T) {
    code := PuregenGetFunctionCode("y_tab_get")
    
    // Show bytes around CALL at offset 63 (pure_hash_tagged)
    start := 50
    end := 80
    t.Logf("TabGet bytes [%d:%d]:\n%s", start, end, hexdump(code, start, end))
    
    // Decode the push sequence before the call
    t.Logf("  Bytes 50-62 (pre-call setup):")
    for i := 50; i < 63; i++ {
        t.Logf("    [%d] = 0x%02x", i, code[i])
    }
    
    // Show bytes around CALL at offset 147 (pure_values_equal)
    start = 130
    end = 170
    t.Logf("TabGet bytes [%d:%d]:\n%s", start, end, hexdump(code, start, end))
    
    // Now check: what happens to R12 during the CALL sequence?
    // The pushes before CALL at offset 63 should be: RSI, R12, R14, R15, R8
    // Let's verify R12 is properly saved/restored
}

func TestDebugTabSetCallContext(t *testing.T) {
    code := PuregenGetFunctionCode("y_tab_set")
    
    // Show bytes around CALL at offset 148 (pure_hash_tagged)
    start := 135
    end := 165
    t.Logf("TabSet bytes [%d:%d]:\n%s", start, end, hexdump(code, start, end))
    
    // Show bytes around the first hash store at offset 285
    start = 270
    end = 310
    t.Logf("TabSet bytes (hash store) [%d:%d]:\n%s", start, end, hexdump(code, start, end))
}

func TestDebugTableNewHeaderLayout(t *testing.T) {
    code := PuregenGetFunctionCode("y_table_new")
    
    // y_table_new allocates a header of 56 bytes (rtTableHeaderSize) and 16*32=512 bytes for entries
    // Header layout: [0]=count, [8]=cap, [16]=threshold, [24]=mask, [32]=entries, [40]=entrycap, [48]=tombstones
    // Show the full code with store instructions
    t.Logf("y_table_new full code (%d bytes):", len(code))
    for i := 0; i < len(code); i++ {
        // Find MOV [mem], reg or MOV [mem], imm patterns (stores to header)
        if i+5 <= len(code) && code[i] == 0x49 && code[i+1] == 0x89 {
            // MOV [R12+disp], reg  (49 89 xx 24 xx)
            if code[i+3] == 0x24 {
                disp := int8(code[i+4])
                reg := ""
                switch code[i+2] {
                case 0x04: reg = "RAX"
                case 0x1C: reg = "RBX"
                case 0x0C: reg = "RCX"
                case 0x14: reg = "RDX"
                case 0x44: reg = "R8"
                case 0x4C: reg = "R13"
                }
                t.Logf("  [%d] MOV [R12+%d], %s  (header offset %d)", i, disp, reg, disp)
            }
        }
    }
}
