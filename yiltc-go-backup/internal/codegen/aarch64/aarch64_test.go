package aarch64

import (
    "encoding/binary"
    "testing"
)

func TestAssemblerNOP(t *testing.T) {
    a := NewAssembler()
    a.NOP()
    code := a.Code()
    if len(code) != 4 {
        t.Fatalf("NOP: expected 4 bytes, got %d", len(code))
    }
    instr := binary.LittleEndian.Uint32(code)
    if instr != 0xD503201F {
        t.Errorf("NOP: expected 0xD503201F, got 0x%08X", instr)
    }
}

func TestAssemblerRET(t *testing.T) {
    a := NewAssembler()
    a.RET()
    code := a.Code()
    instr := binary.LittleEndian.Uint32(code)
    if instr != 0xD65F03C0 {
        t.Errorf("RET: expected 0xD65F03C0, got 0x%08X", instr)
    }
}

func TestAssemblerMOVZ(t *testing.T) {
    a := NewAssembler()
    a.MOVZ(R0, 42, 0)
    code := a.Code()
    instr := binary.LittleEndian.Uint32(code)
    expected := uint32(0x52800000) | uint32(42<<5) | uint32(R0<<5)
    if instr != expected {
        t.Errorf("MOVZ X0, #42: expected 0x%08X, got 0x%08X", expected, instr)
    }
}

func TestAssemblerADD(t *testing.T) {
    a := NewAssembler()
    a.ADD(R0, R1, R2)
    code := a.Code()
    if len(code) != 4 {
        t.Fatalf("ADD: expected 4 bytes, got %d", len(code))
    }
    // ADD X0, X1, X2 should produce non-zero output
    instr := binary.LittleEndian.Uint32(code)
    if instr == 0 {
        t.Error("ADD produced zero")
    }
}

func TestAssemblerSUB(t *testing.T) {
    a := NewAssembler()
    a.SUB(R0, R1, R2)
    code := a.Code()
    if len(code) != 4 {
        t.Fatalf("SUB: expected 4 bytes, got %d", len(code))
    }
}

func TestAssemblerMultipleInstructions(t *testing.T) {
    a := NewAssembler()
    a.NOP()
    a.NOP()
    a.RET()
    code := a.Code()
    if len(code) != 12 {
        t.Errorf("3 instructions: expected 12 bytes, got %d", len(code))
    }
    if a.Len() != 12 {
        t.Errorf("Len: expected 12, got %d", a.Len())
    }
}

func TestAssemblerLabelAndBranch(t *testing.T) {
    a := NewAssembler()
    a.NOP()
    a.B("target")
    a.NOP()
    a.BindLabel("target")
    a.RET()
    code := a.Code()
    if len(code) != 16 {
        t.Errorf("expected 16 bytes (4 instructions), got %d", len(code))
    }
}

func TestAssemblerBCond(t *testing.T) {
    a := NewAssembler()
    a.BCond(CondEQ, "target")
    a.BindLabel("target")
    a.RET()
    code := a.Code()
    if len(code) != 8 {
        t.Errorf("expected 8 bytes, got %d", len(code))
    }
}

func TestAssemblerBL(t *testing.T) {
    a := NewAssembler()
    a.BL("func")
    a.BindLabel("func")
    a.RET()
    code := a.Code()
    if len(code) != 8 {
        t.Errorf("expected 8 bytes, got %d", len(code))
    }
}

func TestAssemblerLDRSTR(t *testing.T) {
    a := NewAssembler()
    a.LDR(R0, R1, 0)
    a.STR(R2, R3, 8)
    code := a.Code()
    if len(code) != 8 {
        t.Errorf("LDR+STR: expected 8 bytes, got %d", len(code))
    }
}

func TestAssemblerFloat(t *testing.T) {
    a := NewAssembler()
    a.FADD(F0, F1, F2)
    a.FSUB(F0, F1, F2)
    a.FMUL(F0, F1, F2)
    code := a.Code()
    if len(code) != 12 {
        t.Errorf("3 float instructions: expected 12 bytes, got %d", len(code))
    }
}

func TestAssemblerCMP(t *testing.T) {
    a := NewAssembler()
    a.CMP(R0, R1)
    code := a.Code()
    instr := binary.LittleEndian.Uint32(code)
    if (instr>>24)&0xFF != 0xEB {
        t.Errorf("CMP: expected opcode 0xEB, got 0x%02X", (instr>>24)&0xFF)
    }
}

func TestAssemblerSpecial(t *testing.T) {
    a := NewAssembler()
    a.DMB()
    code := a.Code()
    if len(code) != 4 {
        t.Fatalf("DMB: expected 4 bytes, got %d", len(code))
    }
    instr := binary.LittleEndian.Uint32(code)
    if instr != 0xD5033BBF {
        t.Errorf("DMB: expected 0xD5033BBF, got 0x%08X", instr)
    }

    a2 := NewAssembler()
    a2.NOP()
    if a2.Len() != 4 {
        t.Errorf("NOP Len: expected 4, got %d", a2.Len())
    }
}

func TestRegAlloc(t *testing.T) {
    ra := NewRegAlloc()
    r1 := ra.Alloc()
    r2 := ra.Alloc()
    ra.Free(r1)
    r3 := ra.Alloc()
    if r2 == r3 && r2 != 0 {
        // After freeing r1, the next alloc should still get a register
        t.Logf("alloc: r1=%v r2=%v r3=%v", r1, r2, r3)
    }
}

func TestRegAllocSpillOffset(t *testing.T) {
    ra := NewRegAlloc()
    off1 := ra.SpillOffset()
    off2 := ra.SpillOffset()
    if off2 != off1+8 {
        t.Errorf("SpillOffset: expected %d, got %d", off1+8, off2)
    }
}

func TestRegisterProperties(t *testing.T) {
    if !R0.IsGPR() || !R10.IsGPR() {
        t.Error("integer registers should be GPR")
    }
    if !F0.IsFP() || !F31.IsFP() {
        t.Error("F0-F31 should be FP")
    }
    if R0.IsFP() || F0.IsGPR() {
        t.Error("register types should not overlap")
    }
    if !R19.IsCalleeSaved() {
        t.Error("R19 should be callee-saved")
    }
    if !F8.IsCalleeSaved() {
        t.Error("F8 should be callee-saved")
    }
}

func TestParseTarget(t *testing.T) {
    tests := []struct {
        triple string
        os     string
    }{
        {"aarch64-linux-gnu", "linux"},
        {"aarch64-linux-android", "android"},
        {"aarch64-windows-msvc", "windows"},
        {"aarch64-macos", "macos"},
        {"aarch64-unknown-none", "none"},
    }
    for _, tt := range tests {
        t.Run(tt.triple, func(t *testing.T) {
            tgt := ParseTarget(tt.triple)
            if tgt.OS != tt.os {
                t.Errorf("ParseTarget(%q).OS = %q, want %q", tt.triple, tgt.OS, tt.os)
            }
        })
    }
}

func TestABIAliases(t *testing.T) {
    if len(ArgRegs) != 8 {
        t.Errorf("ArgRegs: expected 8, got %d", len(ArgRegs))
    }
    if len(FPArgRegs) != 8 {
        t.Errorf("FPArgRegs: expected 8, got %d", len(FPArgRegs))
    }
    if len(RetRegs) != 2 {
        t.Errorf("RetRegs: expected 2, got %d", len(RetRegs))
    }
    if len(CalleeSavedRegs) != 10 {
        t.Errorf("CalleeSavedRegs: expected 10, got %d", len(CalleeSavedRegs))
    }
}
