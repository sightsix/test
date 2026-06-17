package runtime

import (
    "testing"

    "github.com/yilt/yiltc/internal/codegen/x86_64"
)

func TestPuregenMovMemR_RSP(t *testing.T) {
    // Reproduce what genPure_TabSet does:
    // a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 0))
    fb := newRtFuncBuilder("test_rsp", newRodataBuilder())
    fb.a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 0))
    fn := fb.finalize()

    t.Logf("MovMemR(RDX, MemBase(RSP, 0)) = %X", fn.Code)
    if fn.Code[0] != 0x48 {
        t.Errorf("expected REX.W (0x48), got 0x%02X — assembler used R12 instead of RSP!", fn.Code[0])
    }
}

func TestPuregenMovMemR_R12(t *testing.T) {
    fb := newRtFuncBuilder("test_r12", newRodataBuilder())
    fb.a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.R12, 0))
    fn := fb.finalize()

    t.Logf("MovMemR(RDX, MemBase(R12, 0)) = %X", fn.Code)
    if fn.Code[0] != 0x49 {
        t.Errorf("expected REX.WB (0x49), got 0x%02X", fn.Code[0])
    }
}
