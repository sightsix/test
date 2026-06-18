package rv64

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
        // ADDI x10, x0, 42
        instr := ADDI(A0, ZERO, 42)
        if instr == 0 {
                t.Error("ADDI produced zero")
        }
        // Verify opcode (bits [6:0]) = 0x13
        if instr&0x7F != 0x13 {
                t.Errorf("ADDI opcode: expected 0x13, got 0x%02X", instr&0x7F)
        }
        // Verify rd (bits [11:7]) = 10
        if (instr>>7)&0x1F != A0 {
                t.Errorf("ADDI rd: expected %d, got %d", A0, (instr>>7)&0x1F)
        }
        // Verify rs1 (bits [19:15]) = 0
        if (instr>>15)&0x1F != ZERO {
                t.Errorf("ADDI rs1: expected %d, got %d", ZERO, (instr>>15)&0x1F)
        }
        // Verify funct3 (bits [14:12]) = 0
        if (instr>>12)&0x7 != 0 {
                t.Errorf("ADDI funct3: expected 0, got %d", (instr>>12)&0x7)
        }
}

func TestADD(t *testing.T) {
        instr := ADD(A0, A1, A2)
        if instr == 0 {
                t.Error("ADD produced zero")
        }
        if instr&0x7F != 0x33 {
                t.Errorf("ADD opcode: expected 0x33, got 0x%02X", instr&0x7F)
        }
        if (instr>>12)&0x7 != 0 {
                t.Errorf("ADD funct3: expected 0, got %d", (instr>>12)&0x7)
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

func TestMUL(t *testing.T) {
        instr := MUL(A0, A1, A2)
        if instr&0x7F != 0x33 {
                t.Errorf("MUL opcode: expected 0x33, got 0x%02X", instr&0x7F)
        }
        if (instr>>25)&0x7F != 0x01 {
                t.Errorf("MUL funct7: expected 0x01, got 0x%02X", (instr>>25)&0x7F)
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

func TestJumpInstructions(t *testing.T) {
        // JAL x1, +8 (forward jump of 2 instructions)
        jal := JAL(RA, 8)
        if jal&0x7F != 0x6F {
                t.Errorf("JAL opcode: expected 0x6F, got 0x%02X", jal&0x7F)
        }
        if (jal>>7)&0x1F != RA {
                t.Errorf("JAL rd: expected %d, got %d", RA, (jal>>7)&0x1F)
        }

        // JALR x0, x1, 0 (return)
        jalr := JALR(ZERO, RA, 0)
        if jalr&0x7F != 0x67 {
                t.Errorf("JALR opcode: expected 0x67, got 0x%02X", jalr&0x7F)
        }
}

func TestLoadStore(t *testing.T) {
        // LD x10, 0(x0)
        ld := LD(A0, ZERO, 0)
        if ld&0x7F != 0x03 {
                t.Errorf("LD opcode: expected 0x03, got 0x%02X", ld&0x7F)
        }
        if (ld>>12)&0x7 != 0x03 {
                t.Errorf("LD funct3: expected 3, got %d", (ld>>12)&0x7)
        }

        // SD x10, 0(x0)
        sd := SD(ZERO, A0, 0)
        if sd&0x7F != 0x23 {
                t.Errorf("SD opcode: expected 0x23, got 0x%02X", sd&0x7F)
        }
        if (sd>>12)&0x7 != 0x03 {
                t.Errorf("SD funct3: expected 3, got %d", (sd>>12)&0x7)
        }
}

func TestFloatInstructions(t *testing.T) {
        // FADDD f0, f1, f2
        fadd := FADDD(F0, F1, F2)
        if fadd == 0 {
                t.Error("FADDD produced zero")
        }
        if fadd&0x7F != 0x53 {
                t.Errorf("FADDD opcode: expected 0x53, got 0x%02X", fadd&0x7F)
        }

        // FSUBD
        fsub := FSUBD(F0, F1, F2)
        if fsub&0x7F != 0x53 {
                t.Errorf("FSUBD opcode: expected 0x53, got 0x%02X", fsub&0x7F)
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
        buf.Emit(ADDI(A0, ZERO, 10))
        if buf.PC() != 12 {
                t.Errorf("PC after 3 emits: expected 12, got %d", buf.PC())
        }

        code := buf.Code()
        if len(code) != 12 {
                t.Errorf("Code length: expected 12, got %d", len(code))
        }

        // Verify first instruction is NOP
        nop := binary.LittleEndian.Uint32(code[0:4])
        if nop != 0x00000013 {
                t.Errorf("first instruction: expected NOP (0x00000013), got 0x%08X", nop)
        }
}

func TestAsmBufferLabels(t *testing.T) {
        buf := NewAsmBuffer()

        buf.Label("start")
        buf.Emit(NOP())
        buf.Bind("end")
        buf.Emit(NOP())

        // Label creates at PC=0, Bind sets at PC=4, NOP at PC=4 = 2 instructions = 8 bytes
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
        // Verify the fixup produced a B-format instruction (opcode 0x63)
        // Note: the fixup modifies the B-format immediate field, so the exact
        // encoding depends on the delta between branch and target.
        branchInstr := binary.LittleEndian.Uint32(code[4:8])
        if branchInstr&0x7F != 0x63 {
                t.Errorf("branch opcode: expected 0x63 (B-format), got 0x%02X", branchInstr&0x7F)
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
        // JAL should be patched
        jalInstr := binary.LittleEndian.Uint32(code[0:4])
        if jalInstr&0x7F != 0x6F {
                t.Errorf("JAL opcode: expected 0x6F, got 0x%02X", jalInstr&0x7F)
        }
}

func TestAsmBufferData(t *testing.T) {
        buf := NewAsmBuffer()

        off := buf.EmitString("hello")
        if off != 0 {
                t.Errorf("EmitString offset: expected 0, got %d", off)
        }

        off2 := buf.EmitU64(0xDEADBEEFCAFE1234)
        if off2 != 6 { // "hello\0" = 6 bytes
                t.Errorf("EmitU64 offset: expected 6, got %d", off2)
        }

        data := buf.Data()
        if len(data) != 14 { // 6 + 8
                t.Errorf("Data length: expected 14, got %d", len(data))
        }

        // Verify string
        if string(data[0:5]) != "hello" {
                t.Errorf("string data: expected 'hello', got %q", string(data[0:5]))
        }
}

func TestAsmBufferCodeLen(t *testing.T) {
        buf := NewAsmBuffer()
        if buf.CodeLen() != 0 {
                t.Errorf("empty buffer CodeLen: expected 0, got %d", buf.CodeLen())
        }
        buf.Emit(NOP())
        buf.Emit(NOP())
        if buf.CodeLen() != 8 {
                t.Errorf("2 instructions CodeLen: expected 8, got %d", buf.CodeLen())
        }
}

func TestTaggedValues(t *testing.T) {
        // TagInt: the tag bit (MSB) is 0 for integers
        v := TagInt(42)
        if (v & TagMask) != 0 {
                t.Error("TagInt should have tag bit = 0")
        }
        if IsTaggedFloat(v) {
                t.Error("TagInt should not be tagged as float")
        }

        // TagFloat: the tag bit (MSB) is 1 for floats
        fv := TagFloat(3.14)
        if !IsTaggedFloat(fv) {
                t.Error("TagFloat should be tagged as float")
        }

        // UntagInt works for values where tag bit is 0
        pos := TagInt(100)
        if UntagInt(pos) != 100 {
                t.Errorf("UntagInt(TagInt(100)) = %d, want 100", UntagInt(pos))
        }

        // TagInt zero
        zero := TagInt(0)
        if UntagInt(zero) != 0 {
                t.Errorf("UntagInt(TagInt(0)) = %d, want 0", UntagInt(zero))
        }
}
