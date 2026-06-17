package rv32

import (
        "encoding/binary"
        "testing"
)

func TestNOP(t *testing.T) {
        instr := NOP()
        if instr != 0x00000013 {
                t.Errorf("NOP: expected 0x00000013, got 0x%08X", instr)
        }
}

func TestADDI(t *testing.T) {
        instr := ADDI(A0, ZERO, 42)
        if instr == 0 {
                t.Error("ADDI produced zero")
        }
        if instr&0x7F != 0x13 {
                t.Errorf("ADDI opcode: expected 0x13, got 0x%02X", instr&0x7F)
        }
        if (instr>>7)&0x1F != A0 {
                t.Errorf("ADDI rd: expected %d, got %d", A0, (instr>>7)&0x1F)
        }
        if (instr>>15)&0x1F != ZERO {
                t.Errorf("ADDI rs1: expected %d, got %d", ZERO, (instr>>15)&0x1F)
        }
}

func TestADD(t *testing.T) {
        instr := ADD(A0, A1, A2)
        if instr&0x7F != 0x33 {
                t.Errorf("ADD opcode: expected 0x33, got 0x%02X", instr&0x7F)
        }
        if (instr>>25)&0x7F != 0x00 {
                t.Errorf("ADD funct7: expected 0x00, got 0x%02X", (instr>>25)&0x7F)
        }
}

func TestSUB(t *testing.T) {
        instr := SUB(A0, A1, A2)
        if instr&0x7F != 0x33 {
                t.Errorf("SUB opcode: expected 0x33, got 0x%02X", instr&0x7F)
        }
        if (instr>>25)&0x7F != 0x20 {
                t.Errorf("SUB funct7: expected 0x20, got 0x%02X", (instr>>25)&0x7F)
        }
}

func TestMULDIV(t *testing.T) {
        mul := MUL(A0, A1, A2)
        if (mul>>25)&0x7F != 0x01 {
                t.Errorf("MUL funct7: expected 0x01, got 0x%02X", (mul>>25)&0x7F)
        }

        div := DIV(A0, A1, A2)
        if (div>>12)&0x7 != 0x04 {
                t.Errorf("DIV funct3: expected 4, got %d", (div>>12)&0x7)
        }
}

func TestLoadStore32(t *testing.T) {
        // LW is the widest load on RV32
        lw := LW(A0, ZERO, 0)
        if lw&0x7F != 0x03 {
                t.Errorf("LW opcode: expected 0x03, got 0x%02X", lw&0x7F)
        }
        if (lw>>12)&0x7 != 0x02 {
                t.Errorf("LW funct3: expected 2, got %d", (lw>>12)&0x7)
        }

        sw := SW(ZERO, A0, 0)
        if sw&0x7F != 0x23 {
                t.Errorf("SW opcode: expected 0x23, got 0x%02X", sw&0x7F)
        }

        // LB
        lb := LB(A0, ZERO, 0)
        if (lb>>12)&0x7 != 0x00 {
                t.Errorf("LB funct3: expected 0, got %d", (lb>>12)&0x7)
        }
}

func TestBranchInstructions(t *testing.T) {
        tests := []struct {
                name   string
                instr  uint32
                funct3 uint32
        }{
                {"BEQ", BEQ(A0, A1, 0), 0x00},
                {"BNE", BNE(A0, A1, 0), 0x01},
                {"BLT", BLT(A0, A1, 0), 0x04},
                {"BGE", BGE(A0, A1, 0), 0x05},
                {"BLTU", BLTU(A0, A1, 0), 0x06},
                {"BGEU", BGEU(A0, A1, 0), 0x07},
        }
        for _, tt := range tests {
                t.Run(tt.name, func(t *testing.T) {
                        if tt.instr == 0 {
                                t.Error("instruction produced zero")
                        }
                        if tt.instr&0x7F != 0x63 {
                                t.Errorf("%s opcode: expected 0x63, got 0x%02X", tt.name, tt.instr&0x7F)
                        }
                        if (tt.instr>>12)&0x7 != tt.funct3 {
                                t.Errorf("%s funct3: expected 0x%02X, got 0x%02X", tt.name, tt.funct3, (tt.instr>>12)&0x7)
                        }
                })
        }
}

func TestJALJALR(t *testing.T) {
        jal := JAL(RA, 8)
        if jal&0x7F != 0x6F {
                t.Errorf("JAL opcode: expected 0x6F, got 0x%02X", jal&0x7F)
        }

        jalr := JALR(ZERO, RA, 0)
        if jalr&0x7F != 0x67 {
                t.Errorf("JALR opcode: expected 0x67, got 0x%02X", jalr&0x7F)
        }
}

func TestFloatInstructions(t *testing.T) {
        fadd := FADDS(F0, F1, F2)
        if fadd == 0 {
                t.Error("FADDS produced zero")
        }
        if fadd&0x7F != 0x53 {
                t.Errorf("FADDS opcode: expected 0x53, got 0x%02X", fadd&0x7F)
        }

        fsub := FSUBS(F0, F1, F2)
        if fsub&0x7F != 0x53 {
                t.Errorf("FSUBS opcode: expected 0x53, got 0x%02X", fsub&0x7F)
        }
}

func TestECALL(t *testing.T) {
        ecall := ECALL()
        if ecall != 0x00000073 {
                t.Errorf("ECALL: expected 0x00000073, got 0x%08X", ecall)
        }
}

func TestAsmBuffer(t *testing.T) {
        buf := NewAsmBuffer()

        if buf.PC() != 0 {
                t.Errorf("initial PC: expected 0, got %d", buf.PC())
        }

        buf.Emit(NOP())
        if buf.PC() != 4 {
                t.Errorf("PC after 1 emit: expected 4, got %d", buf.PC())
        }

        buf.Emit(ADDI(A0, ZERO, 42))
        if buf.PC() != 8 {
                t.Errorf("PC after 2 emits: expected 8, got %d", buf.PC())
        }

        code := buf.Code()
        if len(code) != 8 {
                t.Errorf("Code length: expected 8, got %d", len(code))
        }

        nop := binary.LittleEndian.Uint32(code[0:4])
        if nop != 0x00000013 {
                t.Errorf("first instruction: expected NOP, got 0x%08X", nop)
        }
}

func TestAsmBufferLabels(t *testing.T) {
        buf := NewAsmBuffer()

        buf.Label("start")
        buf.Emit(NOP())
        buf.Bind("end")
        buf.Emit(NOP())

        code := buf.Code()
        if len(code) != 8 {
                t.Errorf("expected 8 bytes, got %d", len(code))
        }
}

func TestAsmBufferFixupBranch(t *testing.T) {
        buf := NewAsmBuffer()

        buf.Emit(NOP())
        buf.FixupBranch("target", BEQ(A0, A1, 0))
        buf.Emit(NOP())
        buf.Bind("target")
        buf.Emit(NOP())

        code := buf.Code()
        if len(code) != 16 {
                t.Errorf("expected 16 bytes, got %d", len(code))
        }
        branchInstr := binary.LittleEndian.Uint32(code[4:8])
        if branchInstr&0x7F != 0x63 {
                t.Errorf("branch opcode: expected 0x63, got 0x%02X", branchInstr&0x7F)
        }
}

func TestAsmBufferFixupJump(t *testing.T) {
        buf := NewAsmBuffer()

        buf.FixupJump("target", RA)
        buf.Emit(NOP())
        buf.Bind("target")

        code := buf.Code()
        if len(code) != 8 {
                t.Errorf("expected 8 bytes, got %d", len(code))
        }
        jalInstr := binary.LittleEndian.Uint32(code[0:4])
        if jalInstr&0x7F != 0x6F {
                t.Errorf("JAL opcode: expected 0x6F, got 0x%02X", jalInstr&0x7F)
        }
}

func TestAsmBufferDataSection(t *testing.T) {
        buf := NewAsmBuffer()

        off := buf.EmitString("rv32")
        if off != 0 {
                t.Errorf("EmitString offset: expected 0, got %d", off)
        }

        off2 := buf.EmitU32(0xDEADBEEF)
        if off2 != 5 { // "rv32\0" = 5 bytes
                t.Errorf("EmitU32 offset: expected 5, got %d", off2)
        }

        data := buf.Data()
        if len(data) != 9 {
                t.Errorf("Data length: expected 9, got %d", len(data))
        }
}

func TestAsmBufferCodeLen(t *testing.T) {
        buf := NewAsmBuffer()
        if buf.CodeLen() != 0 {
                t.Errorf("empty CodeLen: expected 0, got %d", buf.CodeLen())
        }
        buf.Emit(NOP())
        buf.Emit(NOP())
        buf.Emit(NOP())
        if buf.CodeLen() != 12 {
                t.Errorf("3 instructions CodeLen: expected 12, got %d", buf.CodeLen())
        }
}

func TestTaggedValues32(t *testing.T) {
        // TagInt32 uses top 2 bits as tag. Int = 0b00.
        v := TagInt32(42)
        if !IsTaggedInt32(v) {
                t.Error("TagInt32 should be tagged as int")
        }
        if IsTaggedFloat32(v) {
                t.Error("TagInt32 should not be tagged as float")
        }
        if UntagInt32(v) != 42 {
                t.Errorf("UntagInt32(TagInt32(42)) = %d, want 42", UntagInt32(v))
        }

        // Positive values work
        pos := TagInt32(1000)
        if UntagInt32(pos) != 1000 {
                t.Errorf("UntagInt32(TagInt32(1000)) = %d, want 1000", UntagInt32(pos))
        }

        // Zero
        zero := TagInt32(0)
        if UntagInt32(zero) != 0 {
                t.Errorf("UntagInt32(TagInt32(0)) = %d, want 0", UntagInt32(zero))
        }

        // Verify tag bits for special values
        if (TagFloat32 & TagMask32) != TagFloat32 {
                t.Error("TagFloat32 should have tag bits set")
        }
}

func TestRV32NoLD(t *testing.T) {
        // RV32 should not have LD (64-bit load) - verify that if someone tries
        // to construct one, the opcode would not be in the normal RV32 set.
        // We can't directly test absence, but we verify LW works and has funct3=2
        lw := LW(A0, ZERO, 0)
        if (lw>>12)&0x7 != 2 {
                t.Errorf("LW funct3 should be 2 on RV32, got %d", (lw>>12)&0x7)
        }
}

func TestCompilerCreation(t *testing.T) {
        c := NewCompiler(TargetRV32Bare, nil)
        if c == nil {
                t.Fatal("NewCompiler returned nil")
        }
        c.SetOptLevel(1)
        if c.optLevel != 1 {
                t.Errorf("SetOptLevel: expected 1, got %d", c.optLevel)
        }
}
