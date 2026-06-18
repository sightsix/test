package runtime

import (
        "testing"

        "github.com/yilt/yiltc/internal/codegen/x86_64"
)

func TestPuregenTabSetInsertPathEncoding(t *testing.T) {
        // Reproduce the exact sequence of instructions from genPure_TabSet's
        // isEmpty path to verify the encoding is correct.
        fb := newRtFuncBuilder("test_insert", newRodataBuilder())
        a := fb.a

        // These are the exact instructions from the isEmpty path:
        // Line 1231: a.MovRMMem(MemIndex(RBX, RCX, 1, EntryOffKey), RSI)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0), x86_64.RSI)
        
        // Line 1233: a.MovMemR(RDX, MemBase(RSP, 0))
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 0))
        
        // Line 1234: a.MovRMMem(MemIndex(RBX, RCX, 1, EntryOffValue), RDX)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 8), x86_64.RDX)
        
        // Line 1235: a.MovRMMem(MemIndex(RBX, RCX, 1, EntryOffHash), R13)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 16), x86_64.R13)
        
        // Line 1236: a.MovRM(RDX, EntryOccupied)
        a.MovRM(x86_64.RDX, 1)
        
        // Line 1237: a.MovRMMem(MemIndex(RBX, RCX, 1, EntryOffOccupied), RDX)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 24), x86_64.RDX)

        fn := fb.finalize()
        code := fn.Code
        t.Logf("Insert path code: %X", code)
        
        // Find the MOV RDX, [RSP+0] instruction in the code
        for i := 0; i < len(code)-3; i++ {
                if code[i] == 0x48 && code[i+1] == 0x8B && code[i+2] == 0x14 && code[i+3] == 0x24 {
                        t.Logf("  Found MOV RDX, [RSP] at offset %d", i)
                }
                if code[i] == 0x49 && code[i+1] == 0x8B && code[i+2] == 0x14 && code[i+3] == 0x24 {
                        t.Errorf("  Found MOV RDX, [R12] at offset %d — should be [RSP]!", i)
                }
        }
}
