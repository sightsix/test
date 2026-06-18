// Package rv32 implements a RISC-V 32-bit (RV32IMAFD) code generator for the Yilt compiler.
// It targets the ILP32D ABI and produces ELF relocatable object files or bare-metal binaries.
// The main differences from rv64 are 32-bit registers and the use of W-suffix instructions
// for all 32-bit arithmetic operations.
package rv32

import (
        "debug/elf"
        "encoding/binary"
        "fmt"
        "math"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/diag"
)

// =============================================================================
// Register definitions (RV32I)
// =============================================================================

const (
        X0  = 0  // zero - hardwired zero
        X1  = 1  // ra - return address
        X2  = 2  // sp - stack pointer
        X3  = 3  // gp - global pointer
        X4  = 4  // tp - thread pointer
        X5  = 5  // t0 - temporary/alternate link register
        X6  = 6  // t1 - temporary
        X7  = 7  // t2 - temporary
        X8  = 8  // s0 / fp - saved register / frame pointer
        X9  = 9  // s1 - saved register
        X10 = 10 // a0 - function argument / return value
        X11 = 11 // a1 - function argument / return value
        X12 = 12 // a2 - function argument
        X13 = 13 // a3 - function argument
        X14 = 14 // a4 - function argument
        X15 = 15 // a5 - function argument
        X16 = 16 // a6 - function argument
        X17 = 17 // a7 - function argument
        X18 = 18 // s2 - saved register
        X19 = 19 // s3 - saved register
        X20 = 20 // s4 - saved register
        X21 = 21 // s5 - saved register
        X22 = 22 // s6 - saved register
        X23 = 23 // s7 - saved register
        X24 = 24 // s8 - saved register
        X25 = 25 // s9 - saved register
        X26 = 26 // s10 - saved register
        X27 = 27 // s11 - saved register
        X28 = 28 // t3 - temporary
        X29 = 29 // t4 - temporary
        X30 = 30 // t5 - temporary
        X31 = 31 // t6 - temporary
)

// Named aliases for commonly used registers.
const (
        ZERO = X0
        RA   = X1
        SP   = X2
        GP   = X3
        TP   = X4
        FP   = X8
)

// Argument register aliases (a0-a7).
const (
        A0 = X10
        A1 = X11
        A2 = X12
        A3 = X13
        A4 = X14
        A5 = X15
        A6 = X16
        A7 = X17
)

// Temporary register aliases (t0-t6).
const (
        T0 = X5
        T1 = X6
        T2 = X7
        T3 = X28
        T4 = X29
        T5 = X30
        T6 = X31
)

// Saved register aliases (s1-s11).
const (
        S1  = X9
        S2  = X18
        S3  = X19
        S4  = X20
        S5  = X21
        S6  = X22
        S7  = X23
        S8  = X24
        S9  = X25
        S10 = X26
        S11 = X27
)

// Floating-point registers (f-registers, RV32F/D).
const (
        F0  = 0  // ft0 - temporary
        F1  = 1  // ft1 - temporary
        F2  = 2  // ft2 - temporary
        F3  = 3  // ft3 - temporary
        F4  = 4  // ft4 - temporary
        F5  = 5  // ft5 - temporary
        F6  = 6  // ft6 - temporary
        F7  = 7  // ft7 - temporary
        F8  = 8  // fs0 - saved
        F9  = 9  // fs1 - saved
        F10 = 10 // fa0 - argument / return value
        F11 = 11 // fa1 - argument / return value
        F12 = 12 // fa2 - argument
        F13 = 13 // fa3 - argument
        F14 = 14 // fa4 - argument
        F15 = 15 // fa5 - argument
        F16 = 16 // fa6 - argument
        F17 = 17 // fa7 - argument
        F18 = 18 // fs2 - saved
        F19 = 19 // fs3 - saved
        F20 = 20 // fs4 - saved
        F21 = 21 // fs5 - saved
        F22 = 22 // fs6 - saved
        F23 = 23 // fs7 - saved
        F24 = 24 // fs8 - saved
        F25 = 25 // fs9 - saved
        F26 = 26 // fs10 - saved
        F27 = 27 // fs11 - saved
        F28 = 28 // ft8 - temporary
        F29 = 29 // ft9 - temporary
        F30 = 30 // ft10 - temporary
        F31 = 31 // ft11 - temporary
)

// Float argument register aliases.
const (
        FA0 = F10
        FA1 = F11
        FA2 = F12
        FA3 = F13
        FA4 = F14
        FA5 = F15
        FA6 = F16
        FA7 = F17
)

// Callee-saved integer registers.
var calleeSavedInt = []int{FP, S1, S2, S3, S4, S5, S6, S7, S8, S9, S10, S11}

// Callee-saved float registers.
var calleeSavedFloat = []int{F8, F9, F18, F19, F20, F21, F22, F23, F24, F25, F26, F27}

// =============================================================================
// Instruction opcodes (RV32I - same as RV64I for 32-bit subset)
// =============================================================================

const (
        opLoad    = 0x03 // I-format: LB, LH, LW, LBU, LHU
        opLoadFP  = 0x07 // I-format: FLW
        opMiscMem = 0x0F // FENCE
        opOpImm   = 0x13 // I-format: ADDI, SLTI, SLTIU, XORI, ORI, ANDI, SLLI, SRLI, SRAI
        opAUIPC   = 0x17 // U-format: AUIPC
        opStore   = 0x23 // S-format: SB, SH, SW
        opStoreFP = 0x27 // S-format: FSW
        opOp      = 0x33 // R-format: ADD, SUB, SLL, SLT, SLTU, XOR, SRL, SRA, OR, AND
        opLUI     = 0x37 // U-format: LUI
        opBranch  = 0x63 // B-format: BEQ, BNE, BLT, BGE, BLTU, BGEU
        opJALR    = 0x67 // I-format: JALR
        opJAL     = 0x6F // J-format: JAL
        opSystem  = 0x73 // I-format: ECALL, EBREAK
        opOpFP    = 0x53 // R-format: FADD, FSUB, FMUL, FDIV, FSQRT, FEQ, FLT, FLE, FCVT...
        opFMADD   = 0x43 // R4-format: FMADD, FMSUB, FNMSUB, FNMADD
)

// =============================================================================
// funct3 fields
// =============================================================================

const (
        f3LB   = 0
        f3LH   = 1
        f3LW   = 2
        f3LBU  = 4
        f3LHU  = 5
        f3FLW  = 2
        f3SB   = 0
        f3SH   = 1
        f3SW   = 2
        f3FSW  = 2
        f3ADDI = 0
        f3SLTI = 2
        f3SLTIU = 3
        f3XORI = 4
        f3ORI  = 6
        f3ANDI = 7
        f3SLLI = 1
        f3SRLI = 5
        f3SRAI = 5
        f3ADD  = 0
        f3SUB  = 0
        f3SLL  = 1
        f3SLT  = 2
        f3SLTU = 3
        f3XOR  = 4
        f3SRL  = 5
        f3SRA  = 5
        f3OR   = 6
        f3AND  = 7
        f3MUL  = 0
        f3MULH = 1
        f3MULHSU = 2
        f3MULHU  = 3
        f3DIV  = 4
        f3DIVU = 5
        f3REM  = 6
        f3REMU = 7
        f3BEQ  = 0
        f3BNE  = 1
        f3BLT  = 4
        f3BGE  = 5
        f3BLTU = 6
        f3BGEU = 7
)

// =============================================================================
// funct7 fields
// =============================================================================

const (
        f7ADD  = 0x00
        f7SUB  = 0x20
        f7SRL  = 0x00
        f7SRA  = 0x20
        f7MUL  = 0x01 // M extension
)

// =============================================================================
// Tagged value representation (RV32)
// =============================================================================
// On 32-bit systems, Yilt uses pointer-tagging for tagged values.
// The 32-bit value has the following layout:
//
//      [31:30] = type tag (00 = integer, 01 = float32, 10 = table ptr, 11 = special)
//      [29:0]  = payload (30-bit integer, or 30-bit sub-tagged value)
//
// For 32-bit, integers are limited to 30 bits of precision.
// Floats use a different encoding scheme with type-specific boxing.

const (
        // TagMask32 masks the top 2 bits for the type tag.
        TagMask32 uint32 = 0xC0000000

        // TagInt32Val = 0b00 (integer values).
        TagInt32Val = 0x00
        // TagFloat32 = 0b01 (float values).
        TagFloat32 = 0x40 << 24 // 0x40000000
        // TagTable32 = 0b10 (table pointer, lower 30 bits are address/4).
        TagTable32 = 0x80 << 24 // 0x80000000
        // TagSpecial32 = 0b11 (bool, nil, etc).
        TagSpecial32 = 0xC0 << 24 // 0xC0000000

        // MaxIntTagged32 is the maximum integer value representable as a tagged 32-bit int.
        MaxIntTagged32 = (1 << 29) - 1
        MinIntTagged32 = -(1 << 29)
)

// TagInt32 creates a tagged 32-bit integer value.
func TagInt32(v int32) uint32 {
        return uint32(v) &^ TagMask32
}

// IsTaggedFloat32 checks if a tagged 32-bit value is a float.
func IsTaggedFloat32(v uint32) bool {
        return (v & TagMask32) == TagFloat32
}

// IsTaggedInt32 checks if a tagged 32-bit value is an integer.
func IsTaggedInt32(v uint32) bool {
        return (v & TagMask32) == TagInt32Val
}

// UntagInt32 extracts an integer from a tagged 32-bit value.
func UntagInt32(v uint32) int32 {
        return int32(v &^ TagMask32)
}

// =============================================================================
// Instruction encoding (same formats as RV64 but 32-bit operations only)
// =============================================================================

// encR encodes an R-format instruction.
func encR(opcode, rd, funct3, rs1, rs2, funct7 uint32) uint32 {
        return (funct7 << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | (rd << 7) | opcode
}

// encI encodes an I-format instruction.
func encI(opcode, rd, funct3, rs1, imm uint32) uint32 {
        return ((imm & 0xFFF) << 20) | (rs1 << 15) | (funct3 << 12) | (rd << 7) | opcode
}

// encS encodes an S-format instruction.
func encS(opcode, funct3, rs1, rs2, imm uint32) uint32 {
        return (((imm >> 5) & 0x7F) << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | ((imm & 0x1F) << 7) | opcode
}

// encB encodes a B-format instruction.
func encB(opcode, funct3, rs1, rs2, imm uint32) uint32 {
        bit12 := (imm >> 12) & 1
        bits10to5 := (imm >> 5) & 0x3F
        bit11 := (imm >> 11) & 1
        bits4to1 := (imm >> 1) & 0xF
        return (bit12 << 31) | (bits10to5 << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | (bits4to1 << 8) | (bit11 << 7) | opcode
}

// encU encodes a U-format instruction.
func encU(opcode, rd, imm uint32) uint32 {
        return ((imm & 0xFFFFF000) << 0) | (rd << 7) | opcode
}

// encJ encodes a J-format instruction.
func encJ(opcode, rd, imm uint32) uint32 {
        bit20 := (imm >> 20) & 1
        bits10to1 := (imm >> 1) & 0x3FF
        bit11 := (imm >> 11) & 1
        bits19to12 := (imm >> 12) & 0xFF
        return (bit20 << 31) | (bits10to1 << 21) | (bit11 << 20) | (bits19to12 << 12) | (rd << 7) | opcode
}

// =============================================================================
// Individual instruction constructors (RV32I)
// =============================================================================

// NOP is ADDI x0, x0, 0.
func NOP() uint32 { return ADDI(ZERO, ZERO, 0) }

// --- ALU immediate ---

// ADDI: rd = rs1 + imm
func ADDI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), f3ADDI, uint32(rs1), uint32(int16(imm)))
}

// SLTI: rd = (rs1 < imm) ? 1 : 0 (signed)
func SLTI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), f3SLTI, uint32(rs1), uint32(int16(imm)))
}

// SLTIU: rd = (rs1 < imm) ? 1 : 0 (unsigned)
func SLTIU(rd, rs1 int, imm uint16) uint32 {
        return encI(opOpImm, uint32(rd), f3SLTIU, uint32(rs1), uint32(imm))
}

// XORI: rd = rs1 ^ imm
func XORI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), f3XORI, uint32(rs1), uint32(int16(imm)))
}

// ORI: rd = rs1 | imm
func ORI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), f3ORI, uint32(rs1), uint32(int16(imm)))
}

// ANDI: rd = rs1 & imm
func ANDI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), f3ANDI, uint32(rs1), uint32(int16(imm)))
}

// SLLI: rd = rs1 << shamt (5-bit, max 31)
func SLLI(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 31 {
                panic("rv32: SLLI shamt out of range")
        }
        return encI(opOpImm, uint32(rd), f3SLLI, uint32(rs1), uint32(shamt))
}

// SRLI: rd = rs1 >> shamt (logical right shift, 5-bit)
func SRLI(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 31 {
                panic("rv32: SRLI shamt out of range")
        }
        return encI(opOpImm, uint32(rd), f3SRLI, uint32(rs1), uint32(shamt))
}

// SRAI: rd = rs1 >> shamt (arithmetic right shift, 5-bit)
func SRAI(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 31 {
                panic("rv32: SRAI shamt out of range")
        }
        imm := uint32(shamt) | (0x20 << 6) // set bit 30 for SRA
        return encI(opOpImm, uint32(rd), f3SRAI, uint32(rs1), imm)
}

// --- ALU register (RV32I: all operations are 32-bit) ---

// ADD: rd = rs1 + rs2
func ADD(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3ADD, uint32(rs1), uint32(rs2), f7ADD)
}

// SUB: rd = rs1 - rs2
func SUB(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3SUB, uint32(rs1), uint32(rs2), f7SUB)
}

// SLL: rd = rs1 << rs2[4:0]
func SLL(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3SLL, uint32(rs1), uint32(rs2), f7ADD)
}

// SLT: rd = (rs1 < rs2) ? 1 : 0 (signed)
func SLT(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3SLT, uint32(rs1), uint32(rs2), f7ADD)
}

// SLTU: rd = (rs1 < rs2) ? 1 : 0 (unsigned)
func SLTU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3SLTU, uint32(rs1), uint32(rs2), f7ADD)
}

// XOR: rd = rs1 ^ rs2
func XOR(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3XOR, uint32(rs1), uint32(rs2), f7ADD)
}

// SRL: rd = rs1 >> rs2[4:0] (logical)
func SRL(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3SRL, uint32(rs1), uint32(rs2), f7SRL)
}

// SRA: rd = rs1 >> rs2[4:0] (arithmetic)
func SRA(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3SRA, uint32(rs1), uint32(rs2), f7SRA)
}

// OR: rd = rs1 | rs2
func OR(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3OR, uint32(rs1), uint32(rs2), f7ADD)
}

// AND: rd = rs1 & rs2
func AND(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3AND, uint32(rs1), uint32(rs2), f7ADD)
}

// --- M extension (RV32M) ---

// MUL: rd = rs1 * rs2 (lower 32 bits)
func MUL(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3MUL, uint32(rs1), uint32(rs2), f7MUL)
}

// MULH: rd = upper 32 bits of rs1 * rs2 (signed * signed)
func MULH(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3MULH, uint32(rs1), uint32(rs2), f7MUL)
}

// MULHSU: rd = upper 32 bits of rs1 * rs2 (signed * unsigned)
func MULHSU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3MULHSU, uint32(rs1), uint32(rs2), f7MUL)
}

// MULHU: rd = upper 32 bits of rs1 * rs2 (unsigned * unsigned)
func MULHU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3MULHU, uint32(rs1), uint32(rs2), f7MUL)
}

// DIV: rd = rs1 / rs2 (signed)
func DIV(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3DIV, uint32(rs1), uint32(rs2), f7MUL)
}

// DIVU: rd = rs1 / rs2 (unsigned)
func DIVU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3DIVU, uint32(rs1), uint32(rs2), f7MUL)
}

// REM: rd = rs1 % rs2 (signed)
func REM(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3REM, uint32(rs1), uint32(rs2), f7MUL)
}

// REMU: rd = rs1 % rs2 (unsigned)
func REMU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), f3REMU, uint32(rs1), uint32(rs2), f7MUL)
}

// --- Load/Store (RV32I: LW is the widest integer load, no LD/LWU/LHU) ---

// LB: rd = sext8(M[rs1 + offset])
func LB(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), f3LB, uint32(rs1), uint32(int16(offset)))
}

// LH: rd = sext16(M[rs1 + offset])
func LH(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), f3LH, uint32(rs1), uint32(int16(offset)))
}

// LW: rd = sext32(M[rs1 + offset]) — on RV32 this loads the full register
func LW(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), f3LW, uint32(rs1), uint32(int16(offset)))
}

// LBU: rd = zext8(M[rs1 + offset])
func LBU(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), f3LBU, uint32(rs1), uint32(int16(offset)))
}

// LHU: rd = zext16(M[rs1 + offset])
func LHU(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), f3LHU, uint32(rs1), uint32(int16(offset)))
}

// SB: M[rs1 + offset] = rs2[7:0]
func SB(rs1, rs2 int, offset int16) uint32 {
        return encS(opStore, f3SB, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// SH: M[rs1 + offset] = rs2[15:0]
func SH(rs1, rs2 int, offset int16) uint32 {
        return encS(opStore, f3SH, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// SW: M[rs1 + offset] = rs2
func SW(rs1, rs2 int, offset int16) uint32 {
        return encS(opStore, f3SW, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// --- Float load/store (RV32F/D) ---

// FLW: rd_f = M[rs1 + offset] (32-bit float)
func FLW(rd, rs1 int, offset int16) uint32 {
        return encI(opLoadFP, uint32(rd), f3FLW, uint32(rs1), uint32(int16(offset)))
}

// FSW: M[rs1 + offset] = rs2_f (32-bit float)
func FSW(rs1, rs2 int, offset int16) uint32 {
        return encS(opStoreFP, f3FSW, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// --- Upper immediate ---

// LUI: rd = imm << 12
func LUI(rd int, imm uint32) uint32 {
        return encU(opLUI, uint32(rd), imm)
}

// AUIPC: rd = PC + (imm << 12)
func AUIPC(rd int, imm uint32) uint32 {
        return encU(opAUIPC, uint32(rd), imm)
}

// --- Branch ---

// BEQ: if rs1 == rs2, pc += offset
func BEQ(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, f3BEQ, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BNE: if rs1 != rs2, pc += offset
func BNE(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, f3BNE, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BLT: if rs1 < rs2 (signed), pc += offset
func BLT(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, f3BLT, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BGE: if rs1 >= rs2 (signed), pc += offset
func BGE(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, f3BGE, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BLTU: if rs1 < rs2 (unsigned), pc += offset
func BLTU(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, f3BLTU, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BGEU: if rs1 >= rs2 (unsigned), pc += offset
func BGEU(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, f3BGEU, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// --- Jump ---

// JAL: rd = pc + 4, pc += offset
func JAL(rd int, offset int32) uint32 {
        return encJ(opJAL, uint32(rd), uint32(offset))
}

// JALR: rd = pc + 4, pc = (rs1 + offset) & ~1
func JALR(rd, rs1 int, offset int16) uint32 {
        return encI(opJALR, uint32(rd), 0, uint32(rs1), uint32(int16(offset)))
}

// --- Special ---

// FENCE: memory fence
func FENCE(pred, succ uint32) uint32 {
        return (pred << 24) | (succ << 20) | (0 << 15) | (0 << 12) | (0 << 7) | opMiscMem
}

// FENCE_I: instruction fetch fence
func FENCE_I() uint32 {
        return (0xF << 20) | (1 << 7) | opMiscMem
}

// ECALL: environment call
func ECALL() uint32 {
        return encI(opSystem, 0, 0, 0, 0)
}

// EBREAK: environment break
func EBREAK() uint32 {
        return encI(opSystem, 0, 0, 0, 1)
}

// --- Float arithmetic (RV32F) ---

// FADDS: fd = fs1 + fs2 (32-bit float add, RNE)
func FADDS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x00, uint32(rs1), uint32(rs2), 0x00)
}

// FSUBS: fd = fs1 - fs2 (32-bit float sub, RNE)
func FSUBS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x04, uint32(rs1), uint32(rs2), 0x04)
}

// FMULS: fd = fs1 * fs2 (32-bit float mul, RNE)
func FMULS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x08, uint32(rs1), uint32(rs2), 0x08)
}

// FDIVS: fd = fs1 / fs2 (32-bit float div, RNE)
func FDIVS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x0C, uint32(rs1), uint32(rs2), 0x0C)
}

// FSQRTS: fd = sqrt(fs1) (32-bit float sqrt, RNE)
func FSQRTS(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x14, uint32(rs1), 0, 0x2C)
}

// --- Float comparison (RV32F) ---

// FEQS: rd = (fs1 == fs2) ? 1 : 0
func FEQS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x50, uint32(rs1), uint32(rs2), 0x51)
}

// FLTS: rd = (fs1 < fs2) ? 1 : 0
func FLTS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x54, uint32(rs1), uint32(rs2), 0x51)
}

// FLES: rd = (fs1 <= fs2) ? 1 : 0
func FLES(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x58, uint32(rs1), uint32(rs2), 0x51)
}

// --- Float sign injection (RV32F) ---

// FSGNJS: fd = (fs1 < 0) ? -|fs2| : |fs2|
func FSGNJS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x10, uint32(rs1), uint32(rs2), 0x20)
}

// FSGNJNS: fd = (fs1 < 0) ? |fs2| : -|fs2|
func FSGNJNS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x10, uint32(rs1), uint32(rs2), 0x20|0x08)
}

// FSGNJXS: fd = (fs1 < 0) ? -fs2 : fs2
func FSGNJXS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x10, uint32(rs1), uint32(rs2), 0x20|0x10)
}

// --- Float min/max ---

// FMSUBS: fd = (fs1 * fs2) - fs3 (fused multiply-sub)
func FMSUBS(rd, rs1, rs2, rs3 int) uint32 {
        return (uint32(rs3) << 27) | (uint32(rs2) << 20) | (uint32(rs1) << 15) | (0x00 << 12) | (uint32(rd) << 7) | 0x47
}

// FMADDS: fd = (fs1 * fs2) + fs3 (fused multiply-add)
func FMADDS(rd, rs1, rs2, rs3 int) uint32 {
        return (uint32(rs3) << 27) | (uint32(rs2) << 20) | (uint32(rs1) << 15) | (0x00 << 12) | (uint32(rd) << 7) | opFMADD
}

// --- Float conversion (RV32F) ---

// FCVT_W_S: rd = (int32)(float)fs1 (truncate toward zero)
func FCVT_W_S(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x60, uint32(rs1), 0, 0xC0) // rm=RTZ(1)
}

// FCVT_WU_S: rd = (uint32)(float)fs1 (truncate toward zero)
func FCVT_WU_S(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x60, uint32(rs1), 0, 0xC0|0x01)
}

// FCVT_S_W: fd = (float)(int32)rs1
func FCVT_S_W(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x68, uint32(rs1), 0, 0x68) // rm=RNE(0)
}

// FCVT_S_WU: fd = (float)(uint32)rs1
func FCVT_S_WU(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x68, uint32(rs1), 0, 0x68|0x01)
}

// --- Float move (RV32F) ---

// FMV_X_W: rd = bits(fs1) (move float bits to integer register)
func FMV_X_W(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x70, uint32(rs1), 0, 0x70)
}

// FMV_W_X: fd = bits(rs1) (move integer bits to float register)
func FMV_W_X(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x78, uint32(rs1), 0, 0x78)
}

// FCLASS_S: rd = classify(fs1)
func FCLASS_S(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x70, uint32(rs1), 0, 0x70|0x08)
}

// =============================================================================
// Label and fixup management
// =============================================================================

// Label represents a code label.
type Label struct {
        Name   string
        Addr   int
        Fixups []fixup
}

type fixup struct {
        kind  fixupKind
        off   int
        width int
}

type fixupKind int

const (
        fixupBranch fixupKind = iota
        fixupJump
        fixupPCRel32
)

// =============================================================================
// Assembler buffer
// =============================================================================

// AsmBuffer is a low-level instruction assembly buffer with label support.
type AsmBuffer struct {
        code   []uint32
        labels map[string]*Label
        ctr    int
        data   []byte
        relocs []Reloc
}

// Reloc represents a relocation entry.
type Reloc struct {
        Offset int
        Symbol string
        Kind   elf.R_RISCV
        Addend int64
}

// NewAsmBuffer creates a new assembler buffer.
func NewAsmBuffer() *AsmBuffer {
        return &AsmBuffer{
                code:   make([]uint32, 0, 256),
                labels: make(map[string]*Label),
                data:   make([]byte, 0, 256),
                relocs: make([]Reloc, 0),
        }
}

// Emit appends a 32-bit instruction.
func (a *AsmBuffer) Emit(instr uint32) {
        a.code = append(a.code, instr)
        a.ctr++
}

// EmitBytes appends raw bytes to the data section.
func (a *AsmBuffer) EmitBytes(b ...byte) int {
        off := len(a.data)
        a.data = append(a.data, b...)
        return off
}

// EmitString appends a null-terminated string to the data section.
func (a *AsmBuffer) EmitString(s string) int {
        off := len(a.data)
        a.data = append(a.data, s...)
        a.data = append(a.data, 0)
        return off
}

// EmitU32 appends a 32-bit little-endian value.
func (a *AsmBuffer) EmitU32(v uint32) int {
        off := len(a.data)
        var buf [4]byte
        binary.LittleEndian.PutUint32(buf[:], v)
        a.data = append(a.data, buf[:]...)
        return off
}

// EmitU64 appends a 64-bit little-endian value (as two 32-bit words on RV32).
func (a *AsmBuffer) EmitU64(v uint64) int {
        off := len(a.data)
        var buf [8]byte
        binary.LittleEndian.PutUint64(buf[:], v)
        a.data = append(a.data, buf[:]...)
        return off
}

// PC returns the current instruction offset in bytes.
func (a *AsmBuffer) PC() int {
        return a.ctr * 4
}

// Label creates or binds a label at the current PC.
func (a *AsmBuffer) Label(name string) *Label {
        l := &Label{Name: name, Addr: a.PC()}
        a.labels[name] = l
        return l
}

// Bind sets the label's address to the current PC.
func (a *AsmBuffer) Bind(name string) {
        if l, ok := a.labels[name]; ok {
                l.Addr = a.PC()
        } else {
                a.labels[name] = &Label{Name: name, Addr: a.PC()}
        }
}

// LabelAddr returns the address of a label.
func (a *AsmBuffer) LabelAddr(name string) int {
        if l, ok := a.labels[name]; ok {
                return l.Addr
        }
        return -1
}

// FixupBranch emits a branch with a placeholder and records a fixup.
func (a *AsmBuffer) FixupBranch(labelName string, instr uint32) {
        off := a.PC()
        a.Emit(instr)
        l := a.labels[labelName]
        if l == nil {
                l = &Label{Name: labelName}
                a.labels[labelName] = l
        }
        l.Fixups = append(l.Fixups, fixup{kind: fixupBranch, off: off})
}

// FixupJump emits a JAL with a placeholder and records a fixup.
func (a *AsmBuffer) FixupJump(labelName string, rd int) {
        off := a.PC()
        a.Emit(JAL(rd, 0))
        l := a.labels[labelName]
        if l == nil {
                l = &Label{Name: labelName}
                a.labels[labelName] = l
        }
        l.Fixups = append(l.Fixups, fixup{kind: fixupJump, off: off})
}

// AddReloc adds a relocation entry.
func (a *AsmBuffer) AddReloc(offset int, symbol string, kind elf.R_RISCV, addend int64) {
        a.relocs = append(a.relocs, Reloc{Offset: offset, Symbol: symbol, Kind: kind, Addend: addend})
}

// Resolve resolves all label fixups.
func (a *AsmBuffer) Resolve() {
        for _, l := range a.labels {
                for _, f := range l.Fixups {
                        delta := l.Addr - f.off
                        switch f.kind {
                        case fixupBranch:
                                if delta > 4096 || delta < -4096 {
                                        panic(fmt.Sprintf("rv32: branch offset %d out of range for label %q", delta, l.Name))
                                }
                                a.code[f.off/4] = patchBranchImm(a.code[f.off/4], int16(delta))
                        case fixupJump:
                                a.code[f.off/4] = patchJumpImm(a.code[f.off/4], int32(delta))
                        case fixupPCRel32:
                                hi20 := uint32(int32((delta + 0x800)) >> 12)
                                lo12 := uint32(int16(delta & 0xFFF))
                                a.code[f.off/4] = patchUpperImm(a.code[f.off/4], hi20)
                                a.code[f.off/4+1] = patchIImm(a.code[f.off/4+1], int16(lo12))
                        }
                }
        }
}

// Code returns the assembled code as a byte slice.
func (a *AsmBuffer) Code() []byte {
        a.Resolve()
        buf := make([]byte, len(a.code)*4)
        for i, instr := range a.code {
                binary.LittleEndian.PutUint32(buf[i*4:], instr)
        }
        return buf
}

// Data returns the data section bytes.
func (a *AsmBuffer) Data() []byte {
        return a.data
}

// Relocs returns the relocation entries.
func (a *AsmBuffer) Relocs() []Reloc {
        return a.relocs
}

// CodeLen returns the length of the code section in bytes.
func (a *AsmBuffer) CodeLen() int {
        return len(a.code) * 4
}

// patchBranchImm patches a B-format immediate.
func patchBranchImm(instr uint32, imm int16) uint32 {
        ui := uint32(int16(imm))
        bit12 := (ui >> 12) & 1
        bits10to5 := (ui >> 5) & 0x3F
        bit11 := (ui >> 11) & 1
        bits4to1 := (ui >> 1) & 0xF
        mask := uint32(0xFE000F80) // clear the B-format immediate bits: bit31, bits30:25, bits11:8, bit7
        return (instr &^ mask) | (bit12 << 31) | (bits10to5 << 25) | (bits4to1 << 8) | (bit11 << 7)
}

// patchJumpImm patches a J-format immediate.
func patchJumpImm(instr uint32, imm int32) uint32 {
        ui := uint32(imm)
        bit20 := (ui >> 20) & 1
        bits10to1 := (ui >> 1) & 0x3FF
        bit11 := (ui >> 11) & 1
        bits19to12 := (ui >> 12) & 0xFF
        mask := uint32(0xFFF00000)
        return (instr &^ mask) | (bit20 << 31) | (bits10to1 << 21) | (bit11 << 20) | (bits19to12 << 12)
}

// patchUpperImm patches the upper 20-bit immediate in a U-format instruction.
func patchUpperImm(instr uint32, imm uint32) uint32 {
        return (instr & 0xFFF) | (imm & 0xFFFFF000)
}

// patchIImm patches the lower 12-bit immediate in an I-format instruction.
func patchIImm(instr uint32, imm int16) uint32 {
        return (instr & 0xFFFFF) | (uint32(uint16(imm)) << 20)
}

// =============================================================================
// Code Generator
// =============================================================================

// Target describes the target platform.
type Target struct {
        OS   string // "none" (bare metal only for now)
        ABI  string // "ilp32d" or "ilp32"
        CPU  string
}

// Well-known targets.
var (
        TargetRV32Bare = &Target{OS: "none", ABI: "ilp32d", CPU: "generic-rv32"}
)

// VarKind describes where a variable is stored.
type VarKind int

const (
        VarReg  VarKind = iota
        VarStack
        VarGlobal
        VarImm
)

// VarLoc describes a variable's storage location.
type VarLoc struct {
        Kind  VarKind
        Reg   int
        Off   int
        Imm   int32
        FReg  int
        IsFP  bool
}

// Scope holds variable bindings.
type Scope struct {
        parent *Scope
        locals map[string]*VarLoc
}

func newScope(parent *Scope) *Scope {
        return &Scope{parent: parent, locals: make(map[string]*VarLoc)}
}

func (s *Scope) Lookup(name string) *VarLoc {
        if v, ok := s.locals[name]; ok {
                return v
        }
        if s.parent != nil {
                return s.parent.Lookup(name)
        }
        return nil
}

func (s *Scope) Define(name string, loc *VarLoc) {
        s.locals[name] = loc
}

// FuncInfo holds metadata about a compiled function.
type FuncInfo struct {
        Name       string
        Index      int
        CodeStart  int
        CodeLen    int
        NumParams  int
        FrameSize  int
        CalleeSave []int
}

// LoopCtx holds loop context for break/continue.
type LoopCtx struct {
        ContinueLabel string
        BreakLabel    string
        Depth         int
}

// Compiler is the top-level RV32 code generator.
type Compiler struct {
        target   *Target
        diag     *diag.DiagnosticHandler
        asm      *AsmBuffer
        scope    *Scope
        funcs    []*FuncInfo
        funcMap  map[string]*FuncInfo
        globals  map[string]*VarLoc

        usedIntRegs   [32]bool
        usedFloatRegs [32]bool

        curFunc  *FuncInfo
        frameOff int
        loopStack []*LoopCtx
        tmpCount int
        optLevel int
}

// NewCompiler creates a new RV32 code generator.
func NewCompiler(target *Target, dh *diag.DiagnosticHandler) *Compiler {
        return &Compiler{
                target:   target,
                diag:     dh,
                asm:      NewAsmBuffer(),
                scope:    newScope(nil),
                funcs:    make([]*FuncInfo, 0),
                funcMap:  make(map[string]*FuncInfo),
                globals:  make(map[string]*VarLoc),
                tmpCount: 0,
        }
}

// SetOptLevel sets the optimization level (0-2).
func (c *Compiler) SetOptLevel(level int) {
        c.optLevel = level
}

func (c *Compiler) nextTemp(prefix string) string {
        c.tmpCount++
        return fmt.Sprintf("%s_%d", prefix, c.tmpCount)
}

func (c *Compiler) allocFrame(size int) int {
        size = (size + 3) &^ 3 // align to 4 bytes
        c.frameOff -= size
        return c.frameOff
}

func (c *Compiler) allocReg() int {
        for _, r := range []int{T0, T1, T2, T3, T4, T5, T6} {
                if !c.usedIntRegs[r] {
                        c.usedIntRegs[r] = true
                        return r
                }
        }
        c.usedIntRegs[T0] = true
        return T0
}

func (c *Compiler) freeReg(r int) {
        c.usedIntRegs[r] = false
}

func (c *Compiler) allocFloatReg() int {
        for _, r := range []int{F0, F1, F2, F3, F4, F5, F6, F7} {
                if !c.usedFloatRegs[r] {
                        c.usedFloatRegs[r] = true
                        return r
                }
        }
        c.usedFloatRegs[F0] = true
        return F0
}

func (c *Compiler) freeFloatReg(r int) {
        c.usedFloatRegs[r] = false
}

// emitLoadImm loads a 32-bit immediate into a register.
// Uses LUI + ADDI (or just ADDI for small values).
func (c *Compiler) emitLoadImm(rd int, imm int32) {
        if imm >= -2048 && imm <= 2047 {
                c.asm.Emit(ADDI(rd, ZERO, int16(imm)))
                return
        }
        // LUI loads upper 20 bits shifted left by 12
        // ADDI adds lower 12 bits (sign-extended)
        hi := uint32(int32((uint32(imm) + 0x800) >> 12))
        lo := int16(imm & 0xFFF)
        c.asm.Emit(LUI(rd, hi))
        if lo != 0 {
                c.asm.Emit(ADDI(rd, rd, lo))
        }
}

// emitLoadFloatImm loads a float32 immediate into a float register.
func (c *Compiler) emitLoadFloatImm(frd int, val float32) {
        bits := math.Float32bits(val)
        t := c.allocReg()
        c.emitLoadImm(t, int32(bits))
        c.asm.Emit(FMV_W_X(frd, t))
        c.freeReg(t)
}

// emitLoadAddr loads an address into a register using AUIPC + ADDI.
func (c *Compiler) emitLoadAddr(rd int, label string) {
        off := c.asm.PC()
        c.asm.Emit(AUIPC(rd, 0))
        c.asm.Emit(ADDI(rd, rd, 0))
        l := c.asm.labels[label]
        if l == nil {
                l = &Label{Name: label}
                c.asm.labels[label] = l
        }
        l.Fixups = append(l.Fixups, fixup{kind: fixupPCRel32, off: off, width: 2})
}

// emitPrologue generates the function prologue.
func (c *Compiler) emitPrologue() {
        savedRegs := make([]int, 0)
        for _, r := range calleeSavedInt {
                if c.usedIntRegs[r] {
                        savedRegs = append(savedRegs, r)
                }
        }

        // Frame size: saved regs + FP/RA + local variables
        frameSize := (-c.frameOff + 3) &^ 3 // round up to 4-byte alignment
        frameSize += (len(savedRegs) + 2) * 4
        frameSize = (frameSize + 15) &^ 15    // 16-byte alignment

        if c.curFunc != nil {
                c.curFunc.FrameSize = frameSize
                c.curFunc.CalleeSave = savedRegs
        }

        // Adjust SP
        if frameSize <= 2048 {
                c.asm.Emit(ADDI(SP, SP, -int16(frameSize)))
        } else {
                c.emitLoadImm(T0, int32(frameSize))
                c.asm.Emit(SUB(SP, SP, T0))
        }

        // Save RA and FP (as SW on RV32)
        off := 0
        c.asm.Emit(SW(SP, RA, int16(off)))
        off += 4
        c.asm.Emit(SW(SP, FP, int16(off)))
        off += 4

        // Save callee-saved registers (as SW on RV32)
        for _, r := range savedRegs {
                c.asm.Emit(SW(SP, r, int16(off)))
                off += 4
        }

        // Set frame pointer
        c.asm.Emit(ADDI(FP, SP, int16(frameSize)))
}

// emitEpilogue generates the function epilogue.
func (c *Compiler) emitEpilogue() {
        frameSize := 0
        if c.curFunc != nil {
                frameSize = c.curFunc.FrameSize
        }

        // Restore callee-saved registers
        off := 8 // skip RA and FP
        if c.curFunc != nil {
                for range c.curFunc.CalleeSave {
                        // Would need register list to restore properly
                        off += 4
                }
        }

        // Restore FP and RA
        c.asm.Emit(LW(FP, SP, 4))
        c.asm.Emit(LW(RA, SP, 0))

        // Restore SP
        if frameSize <= 2048 {
                c.asm.Emit(ADDI(SP, SP, int16(frameSize)))
        } else {
                c.emitLoadImm(T0, int32(frameSize))
                c.asm.Emit(ADD(SP, SP, T0))
        }

        // Return
        c.asm.Emit(JALR(ZERO, RA, 0))
}

// =============================================================================
// Expression compilation
// =============================================================================

func (c *Compiler) compileExpr(expr ast.Expr) int {
        switch e := expr.(type) {
        case *ast.IntLit:
                return c.compileIntLit(e)
        case *ast.FloatLit:
                return c.compileFloatLit(e)
        case *ast.StringLit:
                return c.compileStringLit(e)
        case *ast.BoolLit:
                return c.compileBoolLit(e)
        case *ast.NilLit:
                return c.compileNilLit(e)
        case *ast.Ident:
                return c.compileIdent(e)
        case *ast.BinOp:
                return c.compileBinOp(e)
        case *ast.UnaryOp:
                return c.compileUnaryOp(e)
        case *ast.CallExpr:
                return c.compileCallExpr(e)
        case *ast.IndexExpr:
                return c.compileIndexExpr(e)
        case *ast.MemberExpr:
                return c.compileMemberExpr(e)
        case *ast.TableLit:
                return c.compileTableLit(e)
        case *ast.AssignExpr:
                return c.compileAssignExpr(e)
        case *ast.IndexAssignExpr:
                return c.compileIndexAssignExpr(e)
        case *ast.MemberAssignExpr:
                // TODO: implement member assignment for RISC-V 32
                c.compileExpr(e.Obj)
                c.compileExpr(e.Value)
                return 0
        case *ast.SpawnExpr:
                return c.compileSpawnExpr(e)
        case *ast.AwaitExpr:
                return c.compileAwaitExpr(e)
        default:
                c.diag.Error("", 0, 0, 0, fmt.Sprintf("rv32: unhandled expression type %T", expr))
                r := c.allocReg()
                c.asm.Emit(ADDI(r, ZERO, 0))
                return r
        }
}

func (c *Compiler) compileIntLit(e *ast.IntLit) int {
        r := c.allocReg()
        c.emitLoadImm(r, int32(e.Value))
        return r
}

func (c *Compiler) compileFloatLit(e *ast.FloatLit) int {
        r := c.allocReg()
        c.emitLoadFloatImm(F0, float32(e.Value))
        c.asm.Emit(FMV_X_W(r, F0))
        // Tag it as a float
        c.asm.Emit(ORI(r, r, 0x4000)) // set float tag bits
        return r
}

func (c *Compiler) compileStringLit(e *ast.StringLit) int {
        dataOff := c.asm.EmitString(e.Value)
        labelName := fmt.Sprintf("str_%d", dataOff)
        c.asm.Label(labelName)

        r := c.allocReg()
        c.emitLoadAddr(r, labelName)
        return r
}

func (c *Compiler) compileBoolLit(e *ast.BoolLit) int {
        r := c.allocReg()
        if e.Value {
                c.asm.Emit(ADDI(r, ZERO, 1))
        } else {
                c.asm.Emit(ADDI(r, ZERO, 0))
        }
        return r
}

func (c *Compiler) compileNilLit(e *ast.NilLit) int {
        r := c.allocReg()
        c.asm.Emit(ADDI(r, ZERO, 0))
        return r
}

func (c *Compiler) compileIdent(e *ast.Ident) int {
        loc := c.scope.Lookup(e.Name)
        if loc == nil {
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("undefined variable: %s", e.Name))
                r := c.allocReg()
                c.asm.Emit(ADDI(r, ZERO, 0))
                return r
        }

        switch loc.Kind {
        case VarReg:
                r := c.allocReg()
                c.asm.Emit(ADD(r, loc.Reg, ZERO))
                return r
        case VarStack:
                r := c.allocReg()
                c.asm.Emit(LW(r, FP, int16(loc.Off)))
                return r
        case VarGlobal:
                r := c.allocReg()
                c.emitLoadAddr(r, e.Name+"_data")
                c.asm.Emit(LW(r, r, 0))
                return r
        case VarImm:
                r := c.allocReg()
                c.emitLoadImm(r, loc.Imm)
                return r
        }
        return c.allocReg()
}

func (c *Compiler) compileBinOp(e *ast.BinOp) int {
        switch e.Op {
        case ast.TAnd:
                return c.compileLogicalAnd(e)
        case ast.TOr:
                return c.compileLogicalOr(e)
        }

        left := c.compileExpr(e.Left)
        right := c.compileExpr(e.Right)

        switch e.Op {
        case ast.TPlus:
                c.asm.Emit(ADD(left, left, right))
        case ast.TMinus:
                c.asm.Emit(SUB(left, left, right))
        case ast.TStar:
                c.asm.Emit(MUL(left, left, right))
        case ast.TSlash:
                c.asm.Emit(DIV(left, left, right))
        case ast.TPercent:
                c.asm.Emit(REM(left, left, right))
        case ast.TAmp:
                c.asm.Emit(AND(left, left, right))
        case ast.TPipe:
                c.asm.Emit(OR(left, left, right))
        case ast.TCaret:
                c.asm.Emit(XOR(left, left, right))
        case ast.TLShift:
                c.asm.Emit(SLL(left, left, right))
        case ast.TRShift:
                c.asm.Emit(SRA(left, left, right))
        case ast.TEq:
                c.asm.Emit(XOR(left, left, right))
                c.asm.Emit(SLTIU(left, left, 1))
        case ast.TNeq:
                c.asm.Emit(XOR(left, left, right))
                c.asm.Emit(SLTU(left, ZERO, left))
        case ast.TLt:
                c.asm.Emit(SLT(left, left, right))
        case ast.TLe:
                c.asm.Emit(SLT(left, left, right))
                c.asm.Emit(XORI(left, left, 1))
        case ast.TGt:
                c.asm.Emit(SLT(left, right, left))
        case ast.TGe:
                c.asm.Emit(SLT(left, right, left))
                c.asm.Emit(XORI(left, left, 1))
        default:
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("unhandled binary operator: %s", e.Op))
                c.asm.Emit(ADDI(left, ZERO, 0))
        }

        c.freeReg(right)
        return left
}

func (c *Compiler) compileLogicalAnd(e *ast.BinOp) int {
        r := c.allocReg()

        left := c.compileExpr(e.Left)
        falseOff := c.asm.PC()
        c.asm.Emit(BEQ(left, ZERO, 0))
        c.freeReg(left)

        right := c.compileExpr(e.Right)
        falseOff2 := c.asm.PC()
        c.asm.Emit(BEQ(right, ZERO, 0))
        c.asm.Emit(ADDI(r, ZERO, 1))
        endOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0))
        c.freeReg(right)

        falseAddr := c.asm.PC()
        c.asm.Emit(ADDI(r, ZERO, 0))

        c.asm.code[falseOff/4] = patchBranchImm(c.asm.code[falseOff/4], int16(falseAddr-falseOff))
        c.asm.code[falseOff2/4] = patchBranchImm(c.asm.code[falseOff2/4], int16(falseAddr-falseOff2))
        c.asm.code[endOff/4] = patchJumpImm(c.asm.code[endOff/4], int32(c.asm.PC()-endOff))

        return r
}

func (c *Compiler) compileLogicalOr(e *ast.BinOp) int {
        r := c.allocReg()

        left := c.compileExpr(e.Left)
        trueOff := c.asm.PC()
        c.asm.Emit(BNE(left, ZERO, 0))
        c.freeReg(left)

        right := c.compileExpr(e.Right)
        trueOff2 := c.asm.PC()
        c.asm.Emit(BNE(right, ZERO, 0))
        c.asm.Emit(ADDI(r, ZERO, 0))
        endOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0))
        c.freeReg(right)

        trueAddr := c.asm.PC()
        c.asm.Emit(ADDI(r, ZERO, 1))

        c.asm.code[trueOff/4] = patchBranchImm(c.asm.code[trueOff/4], int16(trueAddr-trueOff))
        c.asm.code[trueOff2/4] = patchBranchImm(c.asm.code[trueOff2/4], int16(trueAddr-trueOff2))
        c.asm.code[endOff/4] = patchJumpImm(c.asm.code[endOff/4], int32(c.asm.PC()-endOff))

        return r
}

func (c *Compiler) compileUnaryOp(e *ast.UnaryOp) int {
        operand := c.compileExpr(e.Operand)

        switch e.Op {
        case ast.TMinus:
                c.asm.Emit(SUB(operand, ZERO, operand))
        case ast.TNot:
                c.asm.Emit(SLTIU(operand, operand, 1))
        case ast.TTilde:
                c.asm.Emit(XORI(operand, operand, -1))
        default:
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("unhandled unary operator: %s", e.Op))
                c.asm.Emit(ADDI(operand, ZERO, 0))
        }

        return operand
}

func (c *Compiler) compileCallExpr(e *ast.CallExpr) int {
        argRegs := []int{A0, A1, A2, A3, A4, A5, A6, A7}
        var spilledArgs []int

        for i, arg := range e.Args {
                val := c.compileExpr(arg)
                if i < len(argRegs) {
                        if val != argRegs[i] {
                                c.asm.Emit(ADD(argRegs[i], val, ZERO))
                        }
                        c.freeReg(val)
                } else {
                        spillOff := c.allocFrame(4)
                        c.asm.Emit(SW(FP, val, int16(spillOff)))
                        spilledArgs = append(spilledArgs, spillOff)
                        c.freeReg(val)
                }
        }

        var funcName string
        if ident, ok := e.Func.(*ast.Ident); ok {
                funcName = ident.Name
        }

        if fi, ok := c.funcMap[funcName]; ok && fi != nil {
                callLabel := fmt.Sprintf("func_%s", funcName)
                c.asm.Emit(JAL(RA, 0))
                c.asm.FixupJump(callLabel, RA)
        } else if funcName != "" {
                callLabel := fmt.Sprintf("plt_%s", funcName)
                c.asm.Emit(JAL(RA, 0))
                c.asm.FixupJump(callLabel, RA)
        } else {
                funcVal := c.compileExpr(e.Func)
                c.asm.Emit(JALR(RA, funcVal, 0))
                c.freeReg(funcVal)
        }

        for range spilledArgs {
                c.frameOff += 4
        }

        r := c.allocReg()
        c.asm.Emit(ADD(r, A0, ZERO))
        return r
}

func (c *Compiler) compileIndexExpr(e *ast.IndexExpr) int {
        obj := c.compileExpr(e.Obj)
        key := c.compileExpr(e.Key)

        c.asm.Emit(ADD(A0, obj, ZERO))
        c.asm.Emit(ADD(A1, key, ZERO))
        c.freeReg(obj)
        c.freeReg(key)

        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_table_get", RA)

        r := c.allocReg()
        c.asm.Emit(ADD(r, A0, ZERO))
        return r
}

func (c *Compiler) compileMemberExpr(e *ast.MemberExpr) int {
        r := c.allocReg()
        loc := c.scope.Lookup(e.Field)
        if loc != nil {
                switch loc.Kind {
                case VarReg:
                        c.asm.Emit(ADD(r, loc.Reg, ZERO))
                case VarStack:
                        c.asm.Emit(LW(r, FP, int16(loc.Off)))
                default:
                        c.asm.Emit(ADDI(r, ZERO, 0))
                }
        } else {
                c.asm.Emit(ADDI(r, ZERO, 0))
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("undefined member: %s", e.Field))
        }
        return r
}

func (c *Compiler) compileTableLit(e *ast.TableLit) int {
        n := len(e.Entries)
        c.emitLoadImm(A0, int32(n))
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_table_new", RA)

        tablePtr := c.allocReg()
        c.asm.Emit(ADD(tablePtr, A0, ZERO))

        for _, entry := range e.Entries {
                key := c.compileExpr(entry.Key)
                val := c.compileExpr(entry.Value)

                c.asm.Emit(ADD(A0, tablePtr, ZERO))
                c.asm.Emit(ADD(A1, key, ZERO))
                c.asm.Emit(ADD(A2, val, ZERO))
                c.freeReg(key)
                c.freeReg(val)

                c.asm.Emit(JAL(RA, 0))
                c.asm.FixupJump("runtime_table_set", RA)
        }

        return tablePtr
}

func (c *Compiler) compileAssignExpr(e *ast.AssignExpr) int {
        val := c.compileExpr(e.Value)

        if ident, ok := e.Target.(*ast.Ident); ok {
                loc := c.scope.Lookup(ident.Name)
                if loc != nil {
                        switch loc.Kind {
                        case VarReg:
                                c.asm.Emit(ADD(loc.Reg, val, ZERO))
                        case VarStack:
                                c.asm.Emit(SW(FP, val, int16(loc.Off)))
                        }
                } else {
                        c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                                fmt.Sprintf("undefined variable: %s", ident.Name))
                }
        } else {
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        "invalid assignment target")
        }

        return val
}

func (c *Compiler) compileIndexAssignExpr(e *ast.IndexAssignExpr) int {
        obj := c.compileExpr(e.Obj)
        key := c.compileExpr(e.Key)
        val := c.compileExpr(e.Value)

        c.asm.Emit(ADD(A0, obj, ZERO))
        c.asm.Emit(ADD(A1, key, ZERO))
        c.asm.Emit(ADD(A2, val, ZERO))
        c.freeReg(obj)
        c.freeReg(key)

        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_table_set", RA)

        return val
}

func (c *Compiler) compileSpawnExpr(e *ast.SpawnExpr) int {
        if e.Call == nil {
                r := c.allocReg()
                c.asm.Emit(ADDI(r, ZERO, 0))
                return r
        }

        c.compileCallExpr(e.Call)
        c.asm.Emit(ADD(A0, A0, ZERO))
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_spawn", RA)

        r := c.allocReg()
        c.asm.Emit(ADD(r, A0, ZERO))
        return r
}

func (c *Compiler) compileAwaitExpr(e *ast.AwaitExpr) int {
        handle := c.compileExpr(e.Handle)
        c.asm.Emit(ADD(A0, handle, ZERO))
        c.freeReg(handle)

        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_await", RA)

        r := c.allocReg()
        c.asm.Emit(ADD(r, A0, ZERO))
        return r
}



// =============================================================================
// Statement compilation
// =============================================================================

func (c *Compiler) compileStmt(stmt ast.Stmt) {
        switch s := stmt.(type) {
        case *ast.LetStmt:
                c.compileLetStmt(s)
        case *ast.ExprStmt:
                r := c.compileExpr(s.Expr)
                c.freeReg(r)
        case *ast.ReturnStmt:
                c.compileReturnStmt(s)
        case *ast.IfStmt:
                c.compileIfStmt(s)
        case *ast.WhileStmt:
                c.compileWhileStmt(s)
        case *ast.ForStmt:
                c.compileForStmt(s)
        case *ast.MatchStmt:
                c.compileMatchStmt(s)
        case *ast.BreakStmt:
                c.compileBreakStmt(s)
        case *ast.ContinueStmt:
                c.compileContinueStmt(s)
        default:
                c.diag.Error("", 0, 0, 0, fmt.Sprintf("rv32: unhandled statement type %T", stmt))
        }
}

func (c *Compiler) compileLetStmt(s *ast.LetStmt) {
        val := c.compileExpr(s.Value)
        off := c.allocFrame(4)
        loc := &VarLoc{Kind: VarStack, Off: off}
        c.asm.Emit(SW(FP, val, int16(off)))
        c.freeReg(val)
        c.scope.Define(s.Name, loc)
}

func (c *Compiler) compileReturnStmt(s *ast.ReturnStmt) {
        if s.Value != nil {
                val := c.compileExpr(s.Value)
                c.asm.Emit(ADD(A0, val, ZERO))
                c.freeReg(val)
        }
        c.emitEpilogue()
}

func (c *Compiler) compileIfStmt(s *ast.IfStmt) {
        var jumpPatches []int

        for i, branch := range s.Branches {
                if i > 0 {
                        jumpPatches = append(jumpPatches, c.asm.PC())
                        c.asm.Emit(JAL(ZERO, 0))
                }

                cond := c.compileExpr(branch.Cond)
                nextCondOff := c.asm.PC()
                c.asm.Emit(JAL(ZERO, 0))
                c.freeReg(cond)

                c.scope = newScope(c.scope)
                for _, stmt := range branch.Body {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent

                // Jump to end
                jumpPatches = append(jumpPatches, c.asm.PC())
                c.asm.Emit(JAL(ZERO, 0))

                bodyStart := c.asm.PC()
                c.asm.code[nextCondOff/4] = patchJumpImm(c.asm.code[nextCondOff/4], int32(bodyStart-nextCondOff))
        }

        if len(s.Else) > 0 {
                c.scope = newScope(c.scope)
                for _, stmt := range s.Else {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent
        }

        endAddr := c.asm.PC()
        for _, p := range jumpPatches {
                c.asm.code[p/4] = patchJumpImm(c.asm.code[p/4], int32(endAddr-p))
        }
}

func (c *Compiler) compileWhileStmt(s *ast.WhileStmt) {
        loopLabel := c.nextTemp("while_loop")
        endLabel := c.nextTemp("while_end")

        ctx := &LoopCtx{
                ContinueLabel: loopLabel,
                BreakLabel:    endLabel,
                Depth:         len(c.loopStack),
        }
        c.loopStack = append(c.loopStack, ctx)

        c.asm.Label(loopLabel)
        cond := c.compileExpr(s.Cond)
        endOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0))
        c.freeReg(cond)

        c.scope = newScope(c.scope)
        for _, stmt := range s.Body {
                c.compileStmt(stmt)
        }
        c.scope = c.scope.parent

        // Jump back to loop
        backOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0))
        c.asm.code[backOff/4] = patchJumpImm(c.asm.code[backOff/4], int32(c.asm.LabelAddr(loopLabel)-backOff))

        endAddr := c.asm.PC()
        c.asm.Bind(endLabel)
        c.asm.code[endOff/4] = patchJumpImm(c.asm.code[endOff/4], int32(endAddr-endOff))

        c.loopStack = c.loopStack[:len(c.loopStack)-1]
}

func (c *Compiler) compileForStmt(s *ast.ForStmt) {
        iterLabel := c.nextTemp("for_iter")
        endLabel := c.nextTemp("for_end")

        ctx := &LoopCtx{
                ContinueLabel: iterLabel,
                BreakLabel:    endLabel,
                Depth:         len(c.loopStack),
        }
        c.loopStack = append(c.loopStack, ctx)

        collection := c.compileExpr(s.Over)
        c.asm.Emit(ADD(A0, collection, ZERO))
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_iter_new", RA)

        iterReg := c.allocReg()
        c.asm.Emit(ADD(iterReg, A0, ZERO))
        c.freeReg(collection)

        c.asm.Label(iterLabel)

        c.asm.Emit(ADD(A0, iterReg, ZERO))
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_iter_next", RA)

        doneOff := c.asm.PC()
        c.asm.Emit(BEQ(A0, ZERO, 0))
        c.asm.Emit(JAL(ZERO, 0))

        // Update iter_reg with the advanced index: runtime_iter_get_next() -> A0
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_iter_get_next", RA)
        c.asm.Emit(ADD(iterReg, A0, ZERO))

        // Get key: runtime_iter_get_key() -> A0
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_iter_get_key", RA)
        keyTmp := c.allocReg()
        c.asm.Emit(ADD(keyTmp, A0, ZERO)) // save key

        // Get value: runtime_iter_get_val() -> A0
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_iter_get_val", RA)
        // A0 = value

        c.scope = newScope(c.scope)
        if s.Key != "" {
                keyOff := c.allocFrame(4)
                c.asm.Emit(SW(FP, keyTmp, int16(keyOff)))
                c.scope.Define(s.Key, &VarLoc{Kind: VarStack, Off: keyOff})
        }
        if s.Value != "" {
                valOff := c.allocFrame(4)
                c.asm.Emit(SW(FP, A0, int16(valOff)))
                c.scope.Define(s.Value, &VarLoc{Kind: VarStack, Off: valOff})
        }
        c.freeReg(keyTmp)

        for _, stmt := range s.Body {
                c.compileStmt(stmt)
        }
        c.scope = c.scope.parent

        backOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0))
        c.asm.code[backOff/4] = patchJumpImm(c.asm.code[backOff/4], int32(c.asm.LabelAddr(iterLabel)-backOff))

        endAddr := c.asm.PC()
        c.asm.Bind(endLabel)
        c.asm.code[doneOff/4] = patchBranchImm(c.asm.code[doneOff/4], int16(endAddr-doneOff))

        c.freeReg(iterReg)
        c.loopStack = c.loopStack[:len(c.loopStack)-1]
}

func (c *Compiler) compileMatchStmt(s *ast.MatchStmt) {
        subject := c.compileExpr(s.Subject)
        var nextCasePatches []int

        for _, mc := range s.Cases {
                caseVal := c.compileExpr(mc.Value)
                c.asm.Emit(XOR(subject, subject, caseVal))
                c.freeReg(caseVal)

                nextOff := c.asm.PC()
                c.asm.Emit(BNE(subject, ZERO, 0))

                c.scope = newScope(c.scope)
                for _, stmt := range mc.Body {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent

                nextCasePatches = append(nextCasePatches, c.asm.PC())
                c.asm.Emit(JAL(ZERO, 0))

                nextAddr := c.asm.PC()
                c.asm.code[nextOff/4] = patchBranchImm(c.asm.code[nextOff/4], int16(nextAddr-nextOff))
        }

        if len(s.Default) > 0 {
                c.scope = newScope(c.scope)
                for _, stmt := range s.Default {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent
        }

        endAddr := c.asm.PC()
        for _, p := range nextCasePatches {
                c.asm.code[p/4] = patchJumpImm(c.asm.code[p/4], int32(endAddr-p))
        }

        c.freeReg(subject)
}

func (c *Compiler) compileBreakStmt(s *ast.BreakStmt) {
        if len(c.loopStack) == 0 {
                c.diag.Error("", s.Pos().Line, s.Pos().Col, s.Pos().Offset, "break outside of loop")
                return
        }
        ctx := c.loopStack[len(c.loopStack)-1]
        c.asm.Emit(JAL(ZERO, 0))
        c.asm.FixupJump(ctx.BreakLabel, ZERO)
}

func (c *Compiler) compileContinueStmt(s *ast.ContinueStmt) {
        if len(c.loopStack) == 0 {
                c.diag.Error("", s.Pos().Line, s.Pos().Col, s.Pos().Offset, "continue outside of loop")
                return
        }
        ctx := c.loopStack[len(c.loopStack)-1]
        c.asm.Emit(JAL(ZERO, 0))
        c.asm.FixupJump(ctx.ContinueLabel, ZERO)
}

// =============================================================================
// Top-level compilation
// =============================================================================

// Compile compiles a full Yilt program into RV32 machine code.
func (c *Compiler) Compile(program *ast.Program) (*Output, error) {
        if c.diag.HasErrors() {
                return nil, fmt.Errorf("cannot compile with %d errors", c.diag.ErrorCount())
        }

        // First pass: collect function declarations
        for _, file := range program.Files {
                for _, decl := range file.Decls {
                        if fn, ok := decl.(*ast.FnDecl); ok {
                                fi := &FuncInfo{
                                        Name:      fn.Name,
                                        Index:     len(c.funcs),
                                        NumParams: len(fn.Params),
                                }
                                c.funcs = append(c.funcs, fi)
                                c.funcMap[fn.Name] = fi
                        }
                }
        }

        // Second pass: compile functions
        for _, file := range program.Files {
                for _, decl := range file.Decls {
                        if fn, ok := decl.(*ast.FnDecl); ok {
                                if fn.Extern {
                                        continue
                                }
                                c.compileFunction(fn)
                        }
                }
        }

        c.compileEntry(program)
        return c.buildOutput()
}

func (c *Compiler) compileFunction(fn *ast.FnDecl) {
        fi := c.funcMap[fn.Name]
        if fi == nil {
                return
        }

        c.curFunc = fi
        c.frameOff = 0
        c.scope = newScope(nil)
        c.usedIntRegs = [32]bool{}
        c.usedFloatRegs = [32]bool{}
        c.usedIntRegs[FP] = true

        argRegs := []int{A0, A1, A2, A3, A4, A5, A6, A7}
        for i, param := range fn.Params {
                if i < len(argRegs) {
                        loc := &VarLoc{Kind: VarReg, Reg: argRegs[i]}
                        c.scope.Define(param.Name, loc)
                        c.usedIntRegs[argRegs[i]] = true
                } else {
                        off := (i - len(argRegs) + 1) * 4
                        loc := &VarLoc{Kind: VarStack, Off: off}
                        c.scope.Define(param.Name, loc)
                }
        }

        c.usedIntRegs[SP] = true
        c.usedIntRegs[ZERO] = true

        funcLabel := fmt.Sprintf("func_%s", fn.Name)
        fi.CodeStart = c.asm.PC()
        c.asm.Label(funcLabel)

        c.emitPrologue()

        for _, stmt := range fn.Body {
                c.compileStmt(stmt)
        }

        c.emitEpilogue()
        fi.CodeLen = c.asm.PC() - fi.CodeStart
        c.curFunc = nil
}

func (c *Compiler) compileEntry(program *ast.Program) {
        c.curFunc = &FuncInfo{Name: "_start", Index: len(c.funcs)}
        c.frameOff = 0
        c.scope = newScope(nil)

        c.asm.Label("_start")

        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("func_main", RA)

        // Exit: ecall with a0 = exit code
        c.asm.Emit(ECALL())

        c.curFunc = nil
}

// =============================================================================
// Output generation
// =============================================================================

// Output represents the compiled output.
type Output struct {
        Code      []byte
        Data      []byte
        Relocs    []Reloc
        Functions []*FuncInfo
        Globals   map[string]*VarLoc
        ELFBytes  []byte
}

func (c *Compiler) buildOutput() (*Output, error) {
        code := c.asm.Code()
        data := c.asm.Data()

        out := &Output{
                Code:      code,
                Data:      data,
                Relocs:    c.asm.Relocs(),
                Functions: c.funcs,
                Globals:   c.globals,
        }

        elfBytes, err := c.buildELF(code, data)
        if err != nil {
                return nil, err
        }
        out.ELFBytes = elfBytes

        return out, nil
}

// buildELF creates a minimal ELF32 relocatable object file for RV32.
func (c *Compiler) buildELF(code, data []byte) ([]byte, error) {
        var buf []byte

        // ELF32 header (52 bytes)
        ehdr := make([]byte, 52)

        ehdr[0] = 0x7F // ELFMAG0
        ehdr[1] = 0x45 // ELFMAG1 'E'
        ehdr[2] = 0x4C // ELFMAG2 'L'
        ehdr[3] = 0x46 // ELFMAG3 'F'
        ehdr[4] = 1    // ELFCLASS32
        ehdr[5] = 1    // ELFDATA2LSB
        ehdr[6] = 1    // EV_CURRENT
        ehdr[7] = 0    // ELFOSABI_NONE

        // e_type: ET_EXEC (2) for a bare-metal binary
        binary.LittleEndian.PutUint16(ehdr[16:18], 2)
        // e_machine: EM_RISCV = 243
        binary.LittleEndian.PutUint16(ehdr[18:20], 243)
        // e_version
        binary.LittleEndian.PutUint32(ehdr[20:24], 1)
        // e_entry: point to _start (will be determined by section layout)
        binary.LittleEndian.PutUint32(ehdr[24:28], 0)
        // e_phoff: program header table offset (after ELF header)
        binary.LittleEndian.PutUint32(ehdr[28:32], 52)
        // e_shoff: section header table offset (fill later)
        _ = 32
        // e_flags: EF_RISCV_FLOAT_ABI_DOUBLE = 0x0004
        binary.LittleEndian.PutUint32(ehdr[36:40], 0x0004)
        // e_ehsize
        binary.LittleEndian.PutUint16(ehdr[40:42], 52)
        // e_phentsize
        binary.LittleEndian.PutUint16(ehdr[42:44], 32)
        // e_phnum: 1 (text segment)
        binary.LittleEndian.PutUint16(ehdr[44:46], 1)
        // e_shentsize
        binary.LittleEndian.PutUint16(ehdr[46:48], 40)
        // e_shnum: 3 (null + .text + .data)
        numSections := 3
        binary.LittleEndian.PutUint16(ehdr[48:50], uint16(numSections))
        // e_shstrndx: 2
        binary.LittleEndian.PutUint16(ehdr[50:52], 2)

        // Program header for .text segment (LOAD)
        // Place .text and .data contiguously after headers

        phdr := make([]byte, 32)
        binary.LittleEndian.PutUint32(phdr[0:4], 1)     // PT_LOAD
        binary.LittleEndian.PutUint32(phdr[4:8], 0)     // p_offset
        binary.LittleEndian.PutUint32(phdr[8:12], 0)    // p_vaddr
        binary.LittleEndian.PutUint32(phdr[12:16], 0)   // p_paddr
        binary.LittleEndian.PutUint32(phdr[16:20], uint32(len(code)+len(data))) // p_filesz
        binary.LittleEndian.PutUint32(phdr[20:24], uint32(len(code)+len(data))) // p_memsz
        binary.LittleEndian.PutUint32(phdr[24:28], 5)   // p_flags: PF_R|PF_X
        binary.LittleEndian.PutUint32(phdr[28:32], 4)   // p_align

        buf = append(buf, ehdr...)
        buf = append(buf, phdr...)
        buf = append(buf, code...)
        buf = append(buf, data...)

        // Fix up e_entry to point to _start (beginning of .text for now)
        binary.LittleEndian.PutUint32(buf[24:28], 0)

        return buf, nil
}

// =============================================================================
// Utility functions
// =============================================================================

// RegName returns the ABI name of an integer register.
func RegName(r int) string {
        switch r {
        case X0: return "zero"
        case X1: return "ra"
        case X2: return "sp"
        case X3: return "gp"
        case X4: return "tp"
        case X5: return "t0"
        case X6: return "t1"
        case X7: return "t2"
        case X8: return "s0/fp"
        case X9: return "s1"
        case X10: return "a0"
        case X11: return "a1"
        case X12: return "a2"
        case X13: return "a3"
        case X14: return "a4"
        case X15: return "a5"
        case X16: return "a6"
        case X17: return "a7"
        case X18: return "s2"
        case X19: return "s3"
        case X20: return "s4"
        case X21: return "s5"
        case X22: return "s6"
        case X23: return "s7"
        case X24: return "s8"
        case X25: return "s9"
        case X26: return "s10"
        case X27: return "s11"
        case X28: return "t3"
        case X29: return "t4"
        case X30: return "t5"
        case X31: return "t6"
        default: return fmt.Sprintf("x%d", r)
        }
}

// Disasm disassembles a single RV32 instruction.
func Disasm(instr uint32, pc int) string {
        opcode := instr & 0x7F
        rd := (instr >> 7) & 0x1F
        funct3 := (instr >> 12) & 0x7
        rs1 := (instr >> 15) & 0x1F
        rs2 := (instr >> 20) & 0x1F
        funct7 := (instr >> 25) & 0x7F

        switch opcode {
        case opOpImm:
                imm := int16((instr >> 20) & 0xFFF)
                switch funct3 {
                case f3ADDI:
                        if rd == 0 && rs1 == 0 && imm == 0 {
                                return "nop"
                        }
                        return fmt.Sprintf("addi %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case f3SLTI:
                        return fmt.Sprintf("slti %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case f3SLTIU:
                        return fmt.Sprintf("sltiu %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), uint16(imm))
                case f3XORI:
                        return fmt.Sprintf("xori %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case f3ORI:
                        return fmt.Sprintf("ori %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case f3ANDI:
                        return fmt.Sprintf("andi %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case f3SLLI:
                        shamt := rs2 & 0x1F
                        return fmt.Sprintf("slli %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                case f3SRLI:
                        shamt := rs2 & 0x1F
                        if funct7&0x20 != 0 {
                                return fmt.Sprintf("srai %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                        }
                        return fmt.Sprintf("srli %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                }
        case opOp:
                switch funct3 {
                case f3ADD:
                        if funct7 == f7SUB {
                                return fmt.Sprintf("sub %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        }
                        return fmt.Sprintf("add %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case f3SLL:
                        return fmt.Sprintf("sll %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case f3SLT:
                        return fmt.Sprintf("slt %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case f3SLTU:
                        return fmt.Sprintf("sltu %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case f3XOR:
                        return fmt.Sprintf("xor %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case f3SRL:
                        if funct7 == f7SRA {
                                return fmt.Sprintf("sra %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        }
                        return fmt.Sprintf("srl %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case f3OR:
                        return fmt.Sprintf("or %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case f3AND:
                        return fmt.Sprintf("and %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                }
                if funct7 == f7MUL {
                        switch funct3 {
                        case f3MUL:
                                return fmt.Sprintf("mul %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case f3DIV:
                                return fmt.Sprintf("div %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case f3DIVU:
                                return fmt.Sprintf("divu %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case f3REM:
                                return fmt.Sprintf("rem %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case f3REMU:
                                return fmt.Sprintf("remu %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        }
                }
        case opLUI:
                imm := int32(instr & 0xFFFFF000)
                return fmt.Sprintf("lui %s, 0x%x", RegName(int(rd)), uint32(imm))
        case opAUIPC:
                imm := int32(instr & 0xFFFFF000)
                return fmt.Sprintf("auipc %s, 0x%x", RegName(int(rd)), uint32(imm))
        case opJAL:
                imm := decodeJImm(instr)
                target := pc + int(imm)
                if rd == 0 {
                        return fmt.Sprintf("j 0x%x", target)
                }
                return fmt.Sprintf("jal %s, 0x%x", RegName(int(rd)), target)
        case opJALR:
                imm := int16((instr >> 20) & 0xFFF)
                if rd == 0 && rs1 == RA && imm == 0 {
                        return "ret"
                }
                return fmt.Sprintf("jalr %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
        case opBranch:
                imm := decodeBImm(instr)
                target := pc + int(imm)
                switch funct3 {
                case f3BEQ:
                        return fmt.Sprintf("beq %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case f3BNE:
                        return fmt.Sprintf("bne %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case f3BLT:
                        return fmt.Sprintf("blt %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case f3BGE:
                        return fmt.Sprintf("bge %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case f3BLTU:
                        return fmt.Sprintf("bltu %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case f3BGEU:
                        return fmt.Sprintf("bgeu %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                }
        case opLoad:
                imm := int16((instr >> 20) & 0xFFF)
                switch funct3 {
                case f3LB:
                        return fmt.Sprintf("lb %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case f3LH:
                        return fmt.Sprintf("lh %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case f3LW:
                        return fmt.Sprintf("lw %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case f3LBU:
                        return fmt.Sprintf("lbu %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case f3LHU:
                        return fmt.Sprintf("lhu %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                }
        case opStore:
                imm := int16(((instr >> 25) << 5) | ((instr >> 7) & 0x1F))
                switch funct3 {
                case f3SB:
                        return fmt.Sprintf("sb %s, %d(%s)", RegName(int(rs2)), imm, RegName(int(rs1)))
                case f3SH:
                        return fmt.Sprintf("sh %s, %d(%s)", RegName(int(rs2)), imm, RegName(int(rs1)))
                case f3SW:
                        return fmt.Sprintf("sw %s, %d(%s)", RegName(int(rs2)), imm, RegName(int(rs1)))
                }
        case opMiscMem:
                pred := (instr >> 20) & 0xF
                succ := (instr >> 24) & 0xF
                return fmt.Sprintf("fence %d, %d", pred, succ)
        case opSystem:
                if (instr >> 20) == 0 {
                        return "ecall"
                }
                if (instr >> 20) == 1 {
                        return "ebreak"
                }
        }

        return fmt.Sprintf("unknown 0x%08x", instr)
}

func decodeJImm(instr uint32) int32 {
        bit20 := int32((instr >> 31) & 1)
        bits10to1 := int32((instr >> 21) & 0x3FF)
        bit11 := int32((instr >> 20) & 1)
        bits19to12 := int32((instr >> 12) & 0xFF)
        imm := (bit20 << 20) | (bits19to12 << 12) | (bit11 << 11) | (bits10to1 << 1)
        if bit20 != 0 {
                imm |= ^(int32(0) << 21)
        }
        return imm
}

func decodeBImm(instr uint32) int16 {
        bit12 := int16((instr >> 31) & 1)
        bits10to5 := int16((instr >> 25) & 0x3F)
        bit11 := int16((instr >> 7) & 1)
        bits4to1 := int16((instr >> 8) & 0xF)
        imm := (bit12 << 12) | (bits10to5 << 5) | (bit11 << 11) | (bits4to1 << 1)
        if bit12 != 0 {
                imm |= ^(int16(0) << 13)
        }
        return imm
}
