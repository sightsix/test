package runtime

import (
    "fmt"
    "testing"
)

func TestDumpTabSetCode(t *testing.T) {
    code := PuregenGetFunctionCode("y_tab_set")
    if code == nil {
        t.Fatal("no code")
    }

    t.Logf("y_tab_set: %d bytes", len(code))

    // Find all REX.WR MOV r/m64, r64 (4C 89 xx) with SIB+disp8
    for i := 0; i < len(code)-5; i++ {
        if code[i] == 0x4C && code[i+1] == 0x89 {
            modrm := code[i+2]
            mod := (modrm >> 6) & 3
            rm := modrm & 7
            if mod == 1 && rm == 4 {
                sib := code[i+3]
                disp := code[i+4]
                scale := (sib >> 6) & 3
                index := (sib >> 3) & 7
                base := sib & 7
                reg := (modrm >> 3) & 7
                t.Logf("  STORE R%d -> [R%d+R%d*%d+%d] at offset %d: %02X %02X %02X %02X %02X",
                    reg+8, base, index, 1<<scale, signedDisp8(disp),
                    i, code[i], code[i+1], code[i+2], code[i+3], code[i+4])
            }
        }
    }

    // Find all REX.W MOV r/m64, r64 (48 89 xx) with SIB+disp8
    for i := 0; i < len(code)-5; i++ {
        if code[i] == 0x48 && code[i+1] == 0x89 {
            modrm := code[i+2]
            mod := (modrm >> 6) & 3
            rm := modrm & 7
            if mod == 1 && rm == 4 {
                sib := code[i+3]
                disp := code[i+4]
                scale := (sib >> 6) & 3
                index := (sib >> 3) & 7
                base := sib & 7
                reg := (modrm >> 3) & 7
                t.Logf("  STORE R%d -> [R%d+R%d*%d+%d] at offset %d: %02X %02X %02X %02X %02X",
                    reg, base, index, 1<<scale, signedDisp8(disp),
                    i, code[i], code[i+1], code[i+2], code[i+3], code[i+4])
            }
        }
    }

    // Full hex dump
    t.Log("--- Full hex dump ---")
    for i := 0; i < len(code); i += 16 {
        end := i + 16
        if end > len(code) {
            end = len(code)
        }
        hex := ""
        for j := i; j < end; j++ {
            hex += fmt.Sprintf("%02x ", code[j])
        }
        t.Logf("  %04x: %s", i, hex)
    }
}

func signedDisp8(b byte) int {
    if b >= 128 {
        return int(b) - 256
    }
    return int(b)
}
