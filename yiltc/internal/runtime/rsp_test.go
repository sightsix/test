package runtime

import (
    "testing"

    "github.com/yilt/yiltc/internal/codegen/x86_64"
)

func TestMovMemR_RSP(t *testing.T) {
    a := x86_64.NewAsm()
    a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 0))
    code := a.Bytes()
    t.Logf("MOV RDX, [RSP+0] = %X", code)
    // Expected: 48 8B 14 24 (REX.W MOV RDX, [RSP])
    if len(code) != 4 {
        t.Errorf("expected 4 bytes, got %d", len(code))
    }
    if code[0] != 0x48 {
        t.Errorf("expected REX.W (0x48), got 0x%02X", code[0])
    }
    if code[1] != 0x8B {
        t.Errorf("expected MOV opcode (0x8B), got 0x%02X", code[1])
    }
}

func TestMovMemR_RSP_disp8(t *testing.T) {
    a := x86_64.NewAsm()
    a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 8))
    code := a.Bytes()
    t.Logf("MOV RDX, [RSP+8] = %X", code)
    // Expected: 48 8B 54 24 08
}

func TestMovMemR_RSP_neg(t *testing.T) {
    a := x86_64.NewAsm()
    a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, -8))
    code := a.Bytes()
    t.Logf("MOV RDX, [RSP-8] = %X", code)
    // Expected: 48 8B 54 24 F8
}

func TestMovMemR_R12(t *testing.T) {
    a := x86_64.NewAsm()
    a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.R12, 0))
    code := a.Bytes()
    t.Logf("MOV RDX, [R12+0] = %X", code)
    // Expected: 49 8B 14 24 (REX.WB MOV RDX, [R12])
    if code[0] != 0x49 {
        t.Errorf("expected REX.WB (0x49), got 0x%02X", code[0])
    }
}
