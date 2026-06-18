// Package rv64 implements a RISC-V 64-bit (RV64IMAFD) code generator for the Yilt compiler.
// It targets the LP64D ABI and produces ELF relocatable object files or bare-metal binaries.
package rv64

import (
        "debug/elf"
        "encoding/binary"
        "fmt"
        "math"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/diag"
)

// =============================================================================
// Register definitions
// =============================================================================

// Integer registers (x-registers).
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

// Floating-point registers (f-registers).
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

// Float temporary register aliases.
const (
        FT0 = F0
        FT1 = F1
        FT2 = F2
        FT3 = F3
        FT4 = F4
        FT5 = F5
        FT6 = F6
        FT7 = F7
        FT8 = F28
        FT9 = F29
        FT10 = F30
        FT11 = F31
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

// Caller-saved integer registers (must be saved by caller before a call).
var callerSavedInt = []int{RA, T0, T1, T2, T3, T4, T5, T6, A0, A1, A2, A3, A4, A5, A6, A7}

// Callee-saved integer registers (must be preserved by callee).
var calleeSavedInt = []int{FP, S1, S2, S3, S4, S5, S6, S7, S8, S9, S10, S11}

// Caller-saved float registers.
var callerSavedFloat = []int{F0, F1, F2, F3, F4, F5, F6, F7, F28, F29, F30, F31, FA0, FA1, FA2, FA3, FA4, FA5, FA6, FA7}

// Callee-saved float registers.
var calleeSavedFloat = []int{F8, F9, F18, F19, F20, F21, F22, F23, F24, F25, F26, F27}

// =============================================================================
// Instruction opcodes (top 7 bits of the instruction)
// =============================================================================

const (
        opLoad   = 0x03 // I-format: LB, LH, LW, LD, LBU, LHU, LWU
        opLoadFP = 0x07 // I-format: FLW, FLD
        opMiscMem = 0x0F // FENCE
        opOpImm  = 0x13 // I-format: ADDI, SLTI, SLTIU, XORI, ORI, ANDI, SLLI, SRLI, SRAI
        opAUIPC  = 0x17 // U-format: AUIPC
        opOpImm32 = 0x1B // I-format (32-bit): ADDIW, SLLIW, SRLIW, SRAIW
        opStore  = 0x23 // S-format: SB, SH, SW, SD
        opStoreFP = 0x27 // S-format: FSW, FSD
        opOp     = 0x33 // R-format: ADD, SUB, SLL, SLT, SLTU, XOR, SRL, SRA, OR, AND, MUL, DIV, REM...
        opLUI    = 0x37 // U-format: LUI
        opOp32   = 0x3B // R-format (32-bit): ADDW, SUBW, SLLW, SRLW, SRAW, MULW, DIVW, REMW...
        opFMADD  = 0x43 // R4-format: FMADD, FMSUB, FNMSUB, FNMADD
        opOpFP   = 0x53 // R-format: FADD, FSUB, FMUL, FDIV, FSQRT, FEQ, FLT, FLE, FCVT...
        opBranch = 0x63 // B-format: BEQ, BNE, BLT, BGE, BLTU, BGEU
        opJALR   = 0x67 // I-format: JALR
        opJAL    = 0x6F // J-format: JAL
        opSystem = 0x73 // I-format: ECALL, EBREAK
)

// =============================================================================
// funct3 fields
// =============================================================================

const (
        funct3LB   = 0
        funct3LH   = 1
        funct3LW   = 2
        funct3LD   = 3
        funct3LBU  = 4
        funct3LHU  = 5
        funct3LWU  = 6
        funct3FLW  = 2
        funct3FLD  = 3
        funct3SB   = 0
        funct3SH   = 1
        funct3SW   = 2
        funct3SD   = 3
        funct3FSW  = 2
        funct3FSD  = 3
        funct3ADDI = 0
        funct3SLTI = 2
        funct3SLTIU = 3
        funct3XORI = 4
        funct3ORI  = 6
        funct3ANDI = 7
        funct3SLLI = 1
        funct3SRLI = 5
        funct3SRAI = 5
        funct3ADD  = 0
        funct3SUB  = 0
        funct3SLL  = 1
        funct3SLT  = 2
        funct3SLTU = 3
        funct3XOR  = 4
        funct3SRL  = 5
        funct3SRA  = 5
        funct3OR   = 6
        funct3AND  = 7
        funct3MUL  = 0 // M extension shares funct3 with base
        funct3MULH = 1
        funct3MULHSU = 2
        funct3MULHU  = 3
        funct3DIV  = 4
        funct3DIVU = 5
        funct3REM  = 6
        funct3REMU = 7
        funct3BEQ  = 0
        funct3BNE  = 1
        funct3BLT  = 4
        funct3BGE  = 5
        funct3BLTU = 6
        funct3BGEU = 7
        funct3FADD = 0
        funct3FSUB = 4
        funct3FMUL = 8
        funct3FDIV = 12
        funct3FSQRT = 20
        funct3FEQ  = 10
        funct3FLT  = 14
        funct3FLE  = 16
)

// =============================================================================
// funct7 fields
// =============================================================================

const (
        funct7ADD  = 0x00
        funct7SUB  = 0x20
        funct7SRL  = 0x00
        funct7SRA  = 0x20
        funct7MULD = 0x01 // M extension: MUL, DIV, REM variants set bit 0
)

// Float funct7 values (rounded mode is in bits [4:0]).
const (
        funct7FADD  = 0x00 // rm=00000 (RNE)
        funct7FSUB  = 0x04
        funct7FMUL  = 0x08
        funct7FDIV  = 0x0C
        funct7FSQRT = 0x2C
)

// Float funct7 for FCVT (int<->float) operations.
const (
        funct7FCVT_I2F64 = 0x68 // W->D, round mode in [4:0]
        funct7FCVT_F64_2I = 0x60 // D->W, round mode in [4:0]
        funct7FCVT_I2F32 = 0x60 // W->S, round mode in [4:0]
        funct7FCVT_F32_2I = 0x40 // S->W, round mode in [4:0]
        funct7FCVT_D2S = 0x20 // D->S
        funct7FCVT_S2D = 0x21 // S->D
)

// Round modes for float operations.
const (
        rmRNE = 0b000 // Round to Nearest, ties to Even
        rmRTZ = 0b001 // Round towards Zero
        rmRDN = 0b010 // Round Down (towards -infinity)
        rmRUP = 0b011 // Round Up (towards +infinity)
        rmRMM = 0b100 // Round to Nearest, ties to Max Magnitude
        rmDYN = 0b111 // Dynamic rounding mode
)

// FENCE predecessor/successor bits.
const (
        fenceI  = 1 << 3  // input
        fenceO  = 1 << 2  // output
        fenceR  = 1 << 1  // read
        fenceW  = 1 << 0  // write
        fenceIR = fenceI | fenceR
        fenceIW = fenceI | fenceW
        fenceOR = fenceO | fenceR
        fenceOW = fenceO | fenceW
        fenceRW = fenceR | fenceW
        fenceIORW = fenceI | fenceO | fenceR | fenceW
)

// =============================================================================
// Tagged value representation
// =============================================================================
// Yilt uses NaN-boxing for tagged values on 64-bit systems.
// The 64-bit value has the following layout:
//
//      [63]   = tag bit (0 = integer, 1 = float/special)
//      [62:52] = for floats: upper bits of IEEE 754 double
//               for integers: all zeros
//      [51:0]  = for floats: lower bits of IEEE 754 double
//               for integers: 52-bit signed integer value
//
// This works because quiet NaN in IEEE 754 has exponent = all 1s,
// and a quiet NaN has the MSB of the mantissa set.

const (
        // TagMask is the mask for the tag bit.
        TagMask uint64 = 1 << 63

        // IntShift is the number of bits the integer value is left-shifted (0).
        IntShift = 0

        // IntTagBit = 0 means the high bit is 0 for integers.
        IntTagBit = 0

        // FloatTagBit = 1 means the high bit is 1 for floats.
        FloatTagBit = 1

        // MaxIntTagged is the maximum integer value representable as a tagged int.
        MaxIntTagged = (1 << 51) - 1
        MinIntTagged = -(1 << 51)

        // BoolTrue is the tagged representation of true (integer 1).
        BoolTrue uint64 = 1
        // BoolFalse is the tagged representation of false (integer 0).
        BoolFalse uint64 = 0
        // NilVal is the tagged representation of nil.
        NilVal uint64 = 0
)

// TagInt creates a tagged integer value.
func TagInt(v int64) uint64 {
        return uint64(v) &^ TagMask
}

// TagFloat creates a tagged float value (NaN-boxed).
func TagFloat(v float64) uint64 {
        return math.Float64bits(v) | TagMask
}

// IsTaggedFloat checks if a tagged value is a float.
func IsTaggedFloat(v uint64) bool {
        return (v & TagMask) != 0
}

// UntagInt extracts an integer from a tagged value.
func UntagInt(v uint64) int64 {
        return int64(v &^ TagMask)
}

// UntagFloat extracts a float from a tagged value.
func UntagFloat(v uint64) float64 {
        return math.Float64frombits(v)
}

// =============================================================================
// Instruction encoding
// =============================================================================

// EncR encodes an R-format instruction.
//
//      [31:25] funct7 [24:20] rs2 [19:15] rs1 [14:12] funct3 [11:7] rd [6:0] opcode
func encR(opcode, rd, funct3, rs1, rs2, funct7 uint32) uint32 {
        return (funct7 << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | (rd << 7) | opcode
}

// EncI encodes an I-format instruction.
//
//      [31:20] imm [19:15] rs1 [14:12] funct3 [11:7] rd [6:0] opcode
func encI(opcode, rd, funct3, rs1, imm uint32) uint32 {
        return ((imm & 0xFFF) << 20) | (rs1 << 15) | (funct3 << 12) | (rd << 7) | opcode
}

// EncS encodes an S-format instruction.
//
//      [31:25] imm[11:5] [24:20] rs2 [19:15] rs1 [14:12] funct3 [11:7] imm[4:0] [6:0] opcode
func encS(opcode, funct3, rs1, rs2, imm uint32) uint32 {
        return (((imm >> 5) & 0x7F) << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | ((imm & 0x1F) << 7) | opcode
}

// EncB encodes a B-format instruction.
//
//      [31] imm[12|10:5] [24:20] rs2 [19:15] rs1 [14:12] funct3 [11:7] imm[4:1|11] [6:0] opcode
func encB(opcode, funct3, rs1, rs2, imm uint32) uint32 {
        bit12 := (imm >> 12) & 1
        bits10to5 := (imm >> 5) & 0x3F
        bit11 := (imm >> 11) & 1
        bits4to1 := (imm >> 1) & 0xF
        return (bit12 << 31) | (bits10to5 << 25) | (rs2 << 20) | (rs1 << 15) | (funct3 << 12) | (bits4to1 << 8) | (bit11 << 7) | opcode
}

// EncU encodes a U-format instruction.
//
//      [31:12] imm [11:7] rd [6:0] opcode
func encU(opcode, rd, imm uint32) uint32 {
        return ((imm & 0xFFFFF000) << 0) | (rd << 7) | opcode
}

// EncJ encodes a J-format instruction.
//
//      [31] imm[20|10:1|11|19:12] [11:7] rd [6:0] opcode
func encJ(opcode, rd, imm uint32) uint32 {
        bit20 := (imm >> 20) & 1
        bits10to1 := (imm >> 1) & 0x3FF
        bit11 := (imm >> 11) & 1
        bits19to12 := (imm >> 12) & 0xFF
        return (bit20 << 31) | (bits10to1 << 21) | (bit11 << 20) | (bits19to12 << 12) | (rd << 7) | opcode
}

// =============================================================================
// Individual instruction constructors
// =============================================================================

// NOP is ADDI x0, x0, 0.
func NOP() uint32 { return ADDI(ZERO, ZERO, 0) }

// --- ALU immediate ---

// ADDI: rd = rs1 + imm
func ADDI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), funct3ADDI, uint32(rs1), uint32(int16(imm)))
}

// SLTI: rd = (rs1 < imm) ? 1 : 0 (signed)
func SLTI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), funct3SLTI, uint32(rs1), uint32(int16(imm)))
}

// SLTIU: rd = (rs1 < imm) ? 1 : 0 (unsigned)
func SLTIU(rd, rs1 int, imm uint16) uint32 {
        return encI(opOpImm, uint32(rd), funct3SLTIU, uint32(rs1), uint32(imm))
}

// XORI: rd = rs1 ^ imm
func XORI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), funct3XORI, uint32(rs1), uint32(int16(imm)))
}

// ORI: rd = rs1 | imm
func ORI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), funct3ORI, uint32(rs1), uint32(int16(imm)))
}

// ANDI: rd = rs1 & imm
func ANDI(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm, uint32(rd), funct3ANDI, uint32(rs1), uint32(int16(imm)))
}

// SLLI: rd = rs1 << shamt (logical left shift)
func SLLI(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 63 {
                panic("rv64: SLLI shamt out of range")
        }
        return encI(opOpImm, uint32(rd), funct3SLLI, uint32(rs1), uint32(shamt))
}

// SRLI: rd = rs1 >> shamt (logical right shift)
func SRLI(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 63 {
                panic("rv64: SRLI shamt out of range")
        }
        return encI(opOpImm, uint32(rd), funct3SRLI, uint32(rs1), uint32(shamt))
}

// SRAI: rd = rs1 >> shamt (arithmetic right shift)
func SRAI(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 63 {
                panic("rv64: SRAI shamt out of range")
        }
        imm := uint32(shamt) | (0x20 << 6) // set bit 30 to distinguish from SRLI
        return encI(opOpImm, uint32(rd), funct3SRAI, uint32(rs1), imm)
}

// SLLI64: rd = rs1 << shamt (RV64I, 64-bit shift, uses opImm32 funct3=1 with 6-bit shamt)
func SLLI64(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 63 {
                panic("rv64: SLLI64 shamt out of range")
        }
        // For RV64I, SLLI with shamt >= 32 uses funct3=1 and bits [25:20] for shamt [5:0]
        return encI(opOpImm, uint32(rd), funct3SLLI, uint32(rs1), uint32(shamt)|0x400)
}

// SRLI64: rd = rs1 >> shamt (RV64I, 64-bit logical right shift)
func SRLI64(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 63 {
                panic("rv64: SRLI64 shamt out of range")
        }
        return encI(opOpImm, uint32(rd), funct3SRLI, uint32(rs1), uint32(shamt)|0x400)
}

// SRAI64: rd = rs1 >> shamt (RV64I, 64-bit arithmetic right shift)
func SRAI64(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 63 {
                panic("rv64: SRAI64 shamt out of range")
        }
        imm := uint32(shamt) | 0x400 | (0x20 << 6)
        return encI(opOpImm, uint32(rd), funct3SRAI, uint32(rs1), imm)
}

// --- 32-bit ALU immediate (W-suffix, RV64I) ---

// ADDIW: rd = sext32(rs1[31:0] + imm)
func ADDIW(rd, rs1 int, imm int16) uint32 {
        return encI(opOpImm32, uint32(rd), 0, uint32(rs1), uint32(int16(imm)))
}

// SLLIW: rd = sext32(rs1[31:0] << shamt)
func SLLIW(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 31 {
                panic("rv64: SLLIW shamt out of range")
        }
        return encI(opOpImm32, uint32(rd), funct3SLLI, uint32(rs1), uint32(shamt))
}

// SRLIW: rd = sext32(zext32(rs1[31:0]) >> shamt)
func SRLIW(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 31 {
                panic("rv64: SRLIW shamt out of range")
        }
        return encI(opOpImm32, uint32(rd), funct3SRLI, uint32(rs1), uint32(shamt))
}

// SRAIW: rd = sext32(rs1[31:0] >> shamt)
func SRAIW(rd, rs1 int, shamt uint8) uint32 {
        if shamt > 31 {
                panic("rv64: SRAIW shamt out of range")
        }
        imm := uint32(shamt) | (0x20 << 6)
        return encI(opOpImm32, uint32(rd), funct3SRAI, uint32(rs1), imm)
}

// --- ALU register ---

// ADD: rd = rs1 + rs2
func ADD(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3ADD, uint32(rs1), uint32(rs2), funct7ADD)
}

// SUB: rd = rs1 - rs2
func SUB(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3SUB, uint32(rs1), uint32(rs2), funct7SUB)
}

// SLL: rd = rs1 << rs2
func SLL(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3SLL, uint32(rs1), uint32(rs2), funct7ADD)
}

// SLT: rd = (rs1 < rs2) ? 1 : 0 (signed)
func SLT(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3SLT, uint32(rs1), uint32(rs2), funct7ADD)
}

// SLTU: rd = (rs1 < rs2) ? 1 : 0 (unsigned)
func SLTU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3SLTU, uint32(rs1), uint32(rs2), funct7ADD)
}

// XOR: rd = rs1 ^ rs2
func XOR(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3XOR, uint32(rs1), uint32(rs2), funct7ADD)
}

// SRL: rd = rs1 >> rs2 (logical)
func SRL(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3SRL, uint32(rs1), uint32(rs2), funct7SRL)
}

// SRA: rd = rs1 >> rs2 (arithmetic)
func SRA(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3SRA, uint32(rs1), uint32(rs2), funct7SRA)
}

// OR: rd = rs1 | rs2
func OR(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3OR, uint32(rs1), uint32(rs2), funct7ADD)
}

// AND: rd = rs1 & rs2
func AND(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3AND, uint32(rs1), uint32(rs2), funct7ADD)
}

// --- 32-bit ALU register (W-suffix, RV64I) ---

// ADDW: rd = sext32(rs1[31:0] + rs2[31:0])
func ADDW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3ADD, uint32(rs1), uint32(rs2), funct7ADD)
}

// SUBW: rd = sext32(rs1[31:0] - rs2[31:0])
func SUBW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3SUB, uint32(rs1), uint32(rs2), funct7SUB)
}

// SLLW: rd = sext32(rs1[31:0] << rs2[4:0])
func SLLW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3SLL, uint32(rs1), uint32(rs2), funct7ADD)
}

// SRLW: rd = sext32(zext32(rs1[31:0]) >> rs2[4:0])
func SRLW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3SRL, uint32(rs1), uint32(rs2), funct7SRL)
}

// SRAW: rd = sext32(rs1[31:0] >> rs2[4:0])
func SRAW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3SRA, uint32(rs1), uint32(rs2), funct7SRA)
}

// --- M extension (multiplication and division) ---

// MUL: rd = rs1 * rs2 (lower 64 bits)
func MUL(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3MUL, uint32(rs1), uint32(rs2), funct7MULD)
}

// MULH: rd = upper 64 bits of rs1 * rs2 (signed * signed)
func MULH(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3MULH, uint32(rs1), uint32(rs2), funct7MULD)
}

// MULHU: rd = upper 64 bits of rs1 * rs2 (unsigned * unsigned)
func MULHU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3MULHU, uint32(rs1), uint32(rs2), funct7MULD)
}

// MULHSU: rd = upper 64 bits of rs1 * rs2 (signed * unsigned)
func MULHSU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3MULHSU, uint32(rs1), uint32(rs2), funct7MULD)
}

// MULW: rd = sext32(lower 32 bits of rs1 * rs2)
func MULW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3MUL, uint32(rs1), uint32(rs2), funct7MULD)
}

// DIV: rd = rs1 / rs2 (signed, truncated toward zero)
func DIV(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3DIV, uint32(rs1), uint32(rs2), funct7MULD)
}

// DIVU: rd = rs1 / rs2 (unsigned)
func DIVU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3DIVU, uint32(rs1), uint32(rs2), funct7MULD)
}

// REM: rd = rs1 % rs2 (signed, remainder)
func REM(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3REM, uint32(rs1), uint32(rs2), funct7MULD)
}

// REMU: rd = rs1 % rs2 (unsigned, remainder)
func REMU(rd, rs1, rs2 int) uint32 {
        return encR(opOp, uint32(rd), funct3REMU, uint32(rs1), uint32(rs2), funct7MULD)
}

// DIVW: rd = sext32(rs1[31:0] / rs2[31:0]) (signed)
func DIVW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3DIV, uint32(rs1), uint32(rs2), funct7MULD)
}

// DIVUW: rd = sext32(rs1[31:0] / rs2[31:0]) (unsigned)
func DIVUW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3DIVU, uint32(rs1), uint32(rs2), funct7MULD)
}

// REMW: rd = sext32(rs1[31:0] % rs2[31:0]) (signed)
func REMW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3REM, uint32(rs1), uint32(rs2), funct7MULD)
}

// REMUW: rd = sext32(rs1[31:0] % rs2[31:0]) (unsigned)
func REMUW(rd, rs1, rs2 int) uint32 {
        return encR(opOp32, uint32(rd), funct3REMU, uint32(rs1), uint32(rs2), funct7MULD)
}

// --- Load/Store ---

// LB: rd = sext8(M[rs1 + offset])
func LB(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), funct3LB, uint32(rs1), uint32(int16(offset)))
}

// LH: rd = sext16(M[rs1 + offset])
func LH(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), funct3LH, uint32(rs1), uint32(int16(offset)))
}

// LW: rd = sext32(M[rs1 + offset])
func LW(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), funct3LW, uint32(rs1), uint32(int16(offset)))
}

// LD: rd = M[rs1 + offset]
func LD(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), funct3LD, uint32(rs1), uint32(int16(offset)))
}

// LBU: rd = zext8(M[rs1 + offset])
func LBU(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), funct3LBU, uint32(rs1), uint32(int16(offset)))
}

// LHU: rd = zext16(M[rs1 + offset])
func LHU(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), funct3LHU, uint32(rs1), uint32(int16(offset)))
}

// LWU: rd = zext32(M[rs1 + offset])
func LWU(rd, rs1 int, offset int16) uint32 {
        return encI(opLoad, uint32(rd), funct3LWU, uint32(rs1), uint32(int16(offset)))
}

// SB: M[rs1 + offset] = rs2[7:0]
func SB(rs1, rs2 int, offset int16) uint32 {
        return encS(opStore, funct3SB, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// SH: M[rs1 + offset] = rs2[15:0]
func SH(rs1, rs2 int, offset int16) uint32 {
        return encS(opStore, funct3SH, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// SW: M[rs1 + offset] = rs2[31:0]
func SW(rs1, rs2 int, offset int16) uint32 {
        return encS(opStore, funct3SW, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// SD: M[rs1 + offset] = rs2
func SD(rs1, rs2 int, offset int16) uint32 {
        return encS(opStore, funct3SD, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// --- Float load/store ---

// FLW: rd_f = M[rs1 + offset] (32-bit float)
func FLW(rd, rs1 int, offset int16) uint32 {
        return encI(opLoadFP, uint32(rd), funct3FLW, uint32(rs1), uint32(int16(offset)))
}

// FLD: rd_f = M[rs1 + offset] (64-bit float)
func FLD(rd, rs1 int, offset int16) uint32 {
        return encI(opLoadFP, uint32(rd), funct3FLD, uint32(rs1), uint32(int16(offset)))
}

// FSW: M[rs1 + offset] = rs2_f (32-bit float)
func FSW(rs1, rs2 int, offset int16) uint32 {
        return encS(opStoreFP, funct3FSW, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// FSD: M[rs1 + offset] = rs2_f (64-bit float)
func FSD(rs1, rs2 int, offset int16) uint32 {
        return encS(opStoreFP, funct3FSD, uint32(rs1), uint32(rs2), uint32(int16(offset)))
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
        return encB(opBranch, funct3BEQ, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BNE: if rs1 != rs2, pc += offset
func BNE(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, funct3BNE, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BLT: if rs1 < rs2 (signed), pc += offset
func BLT(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, funct3BLT, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BGE: if rs1 >= rs2 (signed), pc += offset
func BGE(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, funct3BGE, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BLTU: if rs1 < rs2 (unsigned), pc += offset
func BLTU(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, funct3BLTU, uint32(rs1), uint32(rs2), uint32(int16(offset)))
}

// BGEU: if rs1 >= rs2 (unsigned), pc += offset
func BGEU(rs1, rs2 int, offset int16) uint32 {
        return encB(opBranch, funct3BGEU, uint32(rs1), uint32(rs2), uint32(int16(offset)))
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

// FENCE: memory fence with predecessor/successor flags
func FENCE(pred, succ uint32) uint32 {
        return (pred << 24) | (succ << 20) | (0 << 15) | (0 << 12) | (0 << 7) | opMiscMem
}

// FENCE_I: instruction fetch fence
func FENCE_I() uint32 {
        return (fenceIR << 20) | (0 << 15) | (0 << 12) | (1 << 7) | opMiscMem
}

// ECALL: environment call (syscall)
func ECALL() uint32 {
        return encI(opSystem, 0, 0, 0, 0)
}

// EBREAK: environment break (debugger breakpoint)
func EBREAK() uint32 {
        return encI(opSystem, 0, 0, 0, 1)
}

// --- Float arithmetic (RV64D) ---

// FADDD: fd = fd + fs2 (64-bit float add, RNE)
func FADDD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FADD, uint32(rs1), uint32(rs2), funct7FADD|rmRNE)
}

// FSUBD: fd = fd - fs2 (64-bit float sub, RNE)
func FSUBD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FSUB, uint32(rs1), uint32(rs2), funct7FSUB|rmRNE)
}

// FMULD: fd = fd * fs2 (64-bit float mul, RNE)
func FMULD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FMUL, uint32(rs1), uint32(rs2), funct7FMUL|rmRNE)
}

// FDIVD: fd = fd / fs2 (64-bit float div, RNE)
func FDIVD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FDIV, uint32(rs1), uint32(rs2), funct7FDIV|rmRNE)
}

// FSQRTD: fd = sqrt(fs1) (64-bit float sqrt, RNE)
func FSQRTD(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FSQRT, uint32(rs1), 0, funct7FSQRT|rmRNE)
}

// FADDS: fd = fd + fs2 (32-bit float add, RNE)
func FADDS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FADD&0x7, uint32(rs1), uint32(rs2), funct7FADD|rmRNE)
}

// FSUBS: fd = fd - fs2 (32-bit float sub, RNE)
func FSUBS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FSUB&0x7, uint32(rs1), uint32(rs2), funct7FSUB|rmRNE)
}

// FMULS: fd = fd * fs2 (32-bit float mul, RNE)
func FMULS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FMUL&0x7, uint32(rs1), uint32(rs2), funct7FMUL|rmRNE)
}

// FDIVS: fd = fd / fs2 (32-bit float div, RNE)
func FDIVS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FDIV&0x7, uint32(rs1), uint32(rs2), funct7FDIV|rmRNE)
}

// FSQRTS: fd = sqrt(fs1) (32-bit float sqrt, RNE)
func FSQRTS(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FSQRT&0x7, uint32(rs1), 0, funct7FSQRT|rmRNE)
}

// FSGNJD: fd = (fs1 >= 0) ? abs(fs2) : -abs(fs2)
func FSGNJD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x10, uint32(rs1), uint32(rs2), 0x20)
}

// FSGNJND: fd = (fs1 < 0) ? abs(fs2) : -abs(fs2)
func FSGNJND(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x10, uint32(rs1), uint32(rs2), 0x20|0x08)
}

// FSGNJXD: fd = (fs1 < 0) ? -fs2 : fs2
func FSGNJXD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), 0x10, uint32(rs1), uint32(rs2), 0x20|0x10)
}

// FMADDD: fd = (fs1 * fs2) + fs3 (fused multiply-add, RNE)
func FMADDD(rd, rs1, rs2, rs3 int) uint32 {
        // R4-format: [31:27] funct7 [26] rs3 [25:20] rs2 [19:15] rs1 [14:12] funct3 [11:7] rd [6:0] opcode
        return (0x00 << 27) | (uint32(rs3) << 27) | (uint32(rs2) << 20) | (uint32(rs1) << 15) | (0x00 << 12) | (uint32(rd) << 7) | opFMADD
}

// FMSUBD: fd = (fs1 * fs2) - fs3 (fused multiply-sub, RNE)
func FMSUBD(rd, rs1, rs2, rs3 int) uint32 {
        return (0x08 << 27) | (uint32(rs3) << 27) | (uint32(rs2) << 20) | (uint32(rs1) << 15) | (0x00 << 12) | (uint32(rd) << 7) | 0x47
}

// --- Float comparison ---

// FEQD: rd = (fs1 == fs2) ? 1 : 0 (64-bit float equality)
func FEQD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FEQ, uint32(rs1), uint32(rs2), 0x51)
}

// FLTD: rd = (fs1 < fs2) ? 1 : 0 (64-bit float less than, quiet NaN)
func FLTD(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FLT, uint32(rs1), uint32(rs2), 0x51)
}

// FLED: rd = (fs1 <= fs2) ? 1 : 0 (64-bit float less or equal, quiet NaN)
func FLED(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FLE, uint32(rs1), uint32(rs2), 0x51)
}

// FEQS: rd = (fs1 == fs2) ? 1 : 0 (32-bit)
func FEQS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FEQ&0x7, uint32(rs1), uint32(rs2), 0x51)
}

// FLTS: rd = (fs1 < fs2) ? 1 : 0 (32-bit)
func FLTS(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FLT&0x7, uint32(rs1), uint32(rs2), 0x51)
}

// FLES: rd = (fs1 <= fs2) ? 1 : 0 (32-bit)
func FLES(rd, rs1, rs2 int) uint32 {
        return encR(opOpFP, uint32(rd), funct3FLE&0x7, uint32(rs1), uint32(rs2), 0x51)
}

// --- Float conversion ---

// FCVT_D_W: fd = (double)(int32)rs1
func FCVT_D_W(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x68|rmRNE)
}

// FCVT_D_WU: fd = (double)(uint32)rs1
func FCVT_D_WU(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x68|rmRNE|0x01)
}

// FCVT_D_L: fd = (double)(int64)rs1
func FCVT_D_L(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x72|rmRNE)
}

// FCVT_D_LU: fd = (double)(uint64)rs1
func FCVT_D_LU(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x72|rmRNE|0x01)
}

// FCVT_W_D: rd = (int32)(double)fs1 (truncate)
func FCVT_W_D(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x60|rmRTZ)
}

// FCVT_WU_D: rd = (uint32)(double)fs1 (truncate)
func FCVT_WU_D(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x60|rmRTZ|0x01)
}

// FCVT_L_D: rd = (int64)(double)fs1 (truncate)
func FCVT_L_D(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x70|rmRTZ)
}

// FCVT_LU_D: rd = (uint64)(double)fs1 (truncate)
func FCVT_LU_D(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x70|rmRTZ|0x01)
}

// FCVT_S_D: fd = (float)(double)fs1
func FCVT_S_D(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x20|rmRNE)
}

// FCVT_D_S: fd = (double)(float)fs1
func FCVT_D_S(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x21|rmRNE)
}

// FCVT_S_W: fd = (float)(int32)rs1
func FCVT_S_W(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x60|rmRNE)
}

// FCVT_W_S: rd = (int32)(float)fs1 (truncate)
func FCVT_W_S(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x40|rmRTZ)
}

// --- Float move ---

// FMV_X_D: rd = bits(fs1) (move double bits to integer register)
func FMV_X_D(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x71)
}

// FMV_D_X: fd = bits(rs1) (move integer bits to double register)
func FMV_D_X(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x79)
}

// FCLASSD: rd = classify(fs1)
func FCLASSD(rd, rs1 int) uint32 {
        return encR(opOpFP, uint32(rd), 0, uint32(rs1), 0, 0x71|0x08)
}

// =============================================================================
// Label and fixup management
// =============================================================================

// Label represents a code label for forward/backward references.
type Label struct {
        Name  string
        Addr  int // resolved byte offset in code section
        Fixups []fixup
}

type fixup struct {
        kind  fixupKind // branch, jump, or auipc+addi pair
        off   int       // offset in code buffer where the fixup is
        width int       // instruction count for multi-instruction fixups
}

type fixupKind int

const (
        fixupBranch fixupKind = iota
        fixupJump
        fixupPCRel32 // AUIPC+ADDI+SRLI pair for 32-bit pc-relative
)

// =============================================================================
// Assembler buffer
// =============================================================================

// AsmBuffer is a low-level instruction assembly buffer with label support.
type AsmBuffer struct {
        code    []uint32
        labels  map[string]*Label
        ctr     int // instruction counter (not bytes)
        data    []byte
        relocs  []Reloc
}

// Reloc represents a relocation entry.
type Reloc struct {
        Offset int    // offset in code section (bytes)
        Symbol string // symbol name
        Kind   elf.R_RISCV // relocation type
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

// Emit appends a 32-bit instruction to the code buffer.
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

// EmitU64 appends a 64-bit little-endian value to the data section.
func (a *AsmBuffer) EmitU64(v uint64) int {
        off := len(a.data)
        var buf [8]byte
        binary.LittleEndian.PutUint64(buf[:], v)
        a.data = append(a.data, buf[:]...)
        return off
}

// EmitU32 appends a 32-bit little-endian value to the data section.
func (a *AsmBuffer) EmitU32(v uint32) int {
        off := len(a.data)
        var buf [4]byte
        binary.LittleEndian.PutUint32(buf[:], v)
        a.data = append(a.data, buf[:]...)
        return off
}

// PC returns the current instruction offset in bytes.
func (a *AsmBuffer) PC() int {
        return a.ctr * 4
}

// Label creates a new label with the given name.
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

// FixupBranch emits a branch instruction with a placeholder offset and records
// a fixup to be resolved later.
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

// FixupJump emits a JAL instruction with a placeholder offset.
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

// Resolve resolves all label fixups. Must be called before extracting code.
func (a *AsmBuffer) Resolve() {
        for _, l := range a.labels {
                for _, f := range l.Fixups {
                        delta := l.Addr - f.off
                        switch f.kind {
                        case fixupBranch:
                                if delta > 4096 || delta < -4096 {
                                        panic(fmt.Sprintf("rv64: branch offset %d out of range for label %q", delta, l.Name))
                                }
                                // Patch the B-format immediate (13-bit signed)
                                a.code[f.off/4] = patchBranchImm(a.code[f.off/4], int16(delta))
                        case fixupJump:
                                // Patch the J-format immediate (21-bit signed)
                                a.code[f.off/4] = patchJumpImm(a.code[f.off/4], int32(delta))
                        case fixupPCRel32:
                                // Patch AUIPC + ADDI pair for 32-bit pc-relative
                                if delta > 0x7FFFFFFF || delta < -0x80000000 {
                                        panic(fmt.Sprintf("rv64: pc-rel32 offset out of range for label %q", l.Name))
                                }
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

// patchBranchImm patches a B-format immediate field.
func patchBranchImm(instr uint32, imm int16) uint32 {
        ui := uint32(int16(imm))
        bit12 := (ui >> 12) & 1
        bits10to5 := (ui >> 5) & 0x3F
        bit11 := (ui >> 11) & 1
        bits4to1 := (ui >> 1) & 0xF
        mask := uint32(0xFE000F80) // clear the B-format immediate bits: bit31, bits30:25, bits11:8, bit7
        return (instr &^ mask) | (bit12 << 31) | (bits10to5 << 25) | (bits4to1 << 8) | (bit11 << 7)
}

// patchJumpImm patches a J-format immediate field.
func patchJumpImm(instr uint32, imm int32) uint32 {
        ui := uint32(imm)
        bit20 := (ui >> 20) & 1
        bits10to1 := (ui >> 1) & 0x3FF
        bit11 := (ui >> 11) & 1
        bits19to12 := (ui >> 12) & 0xFF
        mask := uint32(0xFFF00000) // clear immediate bits (keep opcode and rd)
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

// Target describes the target platform for code generation.
type Target struct {
        OS   string // "linux", "none"
        ABI  string // "lp64d", "lp64"
        CPU  string // e.g., "generic-rv64"
}

// Well-known targets.
var (
        TargetRV64Linux  = &Target{OS: "linux", ABI: "lp64d", CPU: "generic-rv64"}
        TargetRV64Bare   = &Target{OS: "none", ABI: "lp64d", CPU: "generic-rv64"}
)

// VarKind describes the kind of a variable location.
type VarKind int

const (
        VarReg  VarKind = iota // in a register
        VarStack               // on the stack (frame-relative offset)
        VarGlobal              // at a known global address
        VarImm                 // an immediate value
)

// VarLoc describes where a variable is stored.
type VarLoc struct {
        Kind  VarKind
        Reg   int       // register number (for VarReg)
        Off   int       // stack offset or global offset (for VarStack, VarGlobal)
        Imm   int64     // immediate value (for VarImm)
        FReg  int       // float register number
        IsFP  bool      // whether this is a float variable
}

// Scope holds variable bindings for the current lexical scope.
type Scope struct {
        parent *Scope
        locals map[string]*VarLoc
}

func newScope(parent *Scope) *Scope {
        return &Scope{parent: parent, locals: make(map[string]*VarLoc)}
}

// Lookup finds a variable by name, searching up the scope chain.
func (s *Scope) Lookup(name string) *VarLoc {
        if v, ok := s.locals[name]; ok {
                return v
        }
        if s.parent != nil {
                return s.parent.Lookup(name)
        }
        return nil
}

// Define adds a variable binding to this scope.
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
        CalleeSave []int // callee-saved registers pushed
}

// LoopCtx holds information about the current loop for break/continue.
type LoopCtx struct {
        ContinueLabel string
        BreakLabel    string
        Depth         int
}

// Compiler is the top-level RISC-V 64-bit code generator.
type Compiler struct {
        target   *Target
        diag     *diag.DiagnosticHandler
        asm      *AsmBuffer
        scope    *Scope
        funcs    []*FuncInfo
        funcMap  map[string]*FuncInfo
        globals  map[string]*VarLoc
        strings  map[string]int // string -> data offset
        strBuf   []byte

        // Register allocation state
        usedIntRegs   [32]bool
        usedFloatRegs [32]bool

        // Current function state
        curFunc    *FuncInfo
        frameOff   int // current frame offset (grows negative)
        loopStack  []*LoopCtx
        tmpCount   int // counter for temp labels

        // Code generation options
        optLevel int // 0 = none, 1 = basic, 2 = optimized
}

// NewCompiler creates a new RV64 code generator for the given target.
func NewCompiler(target *Target, dh *diag.DiagnosticHandler) *Compiler {
        return &Compiler{
                target:   target,
                diag:     dh,
                asm:      NewAsmBuffer(),
                scope:    newScope(nil),
                funcs:    make([]*FuncInfo, 0),
                funcMap:  make(map[string]*FuncInfo),
                globals:  make(map[string]*VarLoc),
                strings:  make(map[string]int),
                tmpCount: 0,
                optLevel: 0,
        }
}

// SetOptLevel sets the optimization level (0-2).
func (c *Compiler) SetOptLevel(level int) {
        c.optLevel = level
}

// nextTemp generates a unique temporary label name.
func (c *Compiler) nextTemp(prefix string) string {
        c.tmpCount++
        return fmt.Sprintf("%s_%d", prefix, c.tmpCount)
}

// allocFrame allocates space on the stack frame and returns the offset from FP.
func (c *Compiler) allocFrame(size int) int {
        size = (size + 7) &^ 7 // align to 8 bytes
        c.frameOff -= size
        return c.frameOff
}

// allocReg allocates a caller-saved temporary register.
func (c *Compiler) allocReg() int {
        for _, r := range []int{T0, T1, T2, T3, T4, T5, T6} {
                if !c.usedIntRegs[r] {
                        c.usedIntRegs[r] = true
                        return r
                }
        }
        // Spill: use T0 and save it
        c.emitSpillReg(T0)
        c.usedIntRegs[T0] = true
        return T0
}

// freeReg releases a temporary register.
func (c *Compiler) freeReg(r int) {
        c.usedIntRegs[r] = false
}

// allocFloatReg allocates a caller-saved float temporary register.
func (c *Compiler) allocFloatReg() int {
        for _, r := range []int{FT0, FT1, FT2, FT3, FT4, FT5, FT6, FT7} {
                if !c.usedFloatRegs[r] {
                        c.usedFloatRegs[r] = true
                        return r
                }
        }
        c.usedFloatRegs[FT0] = true
        return FT0
}

// freeFloatReg releases a float temporary register.
func (c *Compiler) freeFloatReg(r int) {
        c.usedFloatRegs[r] = false
}

// emitSpillReg emits code to save a register to the stack.
func (c *Compiler) emitSpillReg(r int) {
        off := c.allocFrame(8)
        c.asm.Emit(SD(FP, r, int16(off)))
}

// =============================================================================
// Load constant generation (LUI + ADDI, or LI pattern)
// =============================================================================

// emitLoadImm loads a 64-bit immediate into a register using LUI/ADDI sequences.
// For RV64I, large constants may require multiple instructions.
func (c *Compiler) emitLoadImm(rd int, imm int64) {
        // Handle small positive/negative values with ADDI
        if imm >= -2048 && imm <= 2047 {
                c.asm.Emit(ADDI(rd, ZERO, int16(imm)))
                return
        }

        // For RV64, use a sequence of ADDI/LUI/SLLI/ADDI to load arbitrary 64-bit values.
        // We generate: lui rd, imm[31:12] + addi rd, rd, imm[11:0] if the value fits in 32 bits.
        if imm >= int64(-0x80000000) && imm <= int64(0x7FFFFFFF) {
                uimm := uint32(imm)
                hi := (uimm + 0x800) >> 12 // add 0x800 to handle negative lo12
                lo := int16(uimm & 0xFFF)
                c.asm.Emit(LUI(rd, hi))
                if lo != 0 {
                        c.asm.Emit(ADDI(rd, rd, lo))
                }
                return
        }

        // Full 64-bit: generate LUI+ADDI for lower 32 bits, then SLLI+ORI for upper.
        uimm := uint64(imm)
        lo32 := uint32(uimm & 0xFFFFFFFF)
        hi32 := uint32(uimm >> 32)

        // Load lower 32 bits
        if lo32 != 0 {
                hi12 := (lo32 + 0x800) >> 12
                lo12 := int16(lo32 & 0xFFF)
                c.asm.Emit(LUI(rd, hi12))
                if lo12 != 0 {
                        c.asm.Emit(ADDI(rd, rd, lo12))
                }
        } else {
                c.asm.Emit(ADDI(rd, ZERO, 0)) // rd = 0
        }

        // Add upper 32 bits if non-zero
        if hi32 != 0 {
                t := c.allocReg()
                c.asm.Emit(LUI(t, hi32))
                c.asm.Emit(SLLI64(t, t, 32))
                c.asm.Emit(ADD(rd, rd, t))
                c.freeReg(t)
        }
}

// emitLoadFloatImm loads a float64 immediate into a float register.
func (c *Compiler) emitLoadFloatImm(frd int, val float64) {
        // Load the bits into an integer register, then move to float register.
        bits := math.Float64bits(val)
        t := c.allocReg()
        c.emitLoadImm(t, int64(bits))
        c.asm.Emit(FMV_D_X(frd, t))
        c.freeReg(t)
}

// emitLoadAddr loads an address into a register using AUIPC + ADDI.
func (c *Compiler) emitLoadAddr(rd int, label string) {
        off := c.asm.PC()
        c.asm.Emit(AUIPC(rd, 0))
        c.asm.Emit(ADDI(rd, rd, 0))
        if l, ok := c.asm.labels[label]; ok {
                l.Fixups = append(l.Fixups, fixup{kind: fixupPCRel32, off: off, width: 2})
        } else {
                l := &Label{Name: label}
                l.Fixups = append(l.Fixups, fixup{kind: fixupPCRel32, off: off, width: 2})
                c.asm.labels[label] = l
        }
}

// =============================================================================
// Stack frame management
// =============================================================================

// emitPrologue generates the function prologue.
func (c *Compiler) emitPrologue() {
        // Save callee-saved registers and set up frame pointer.
        // Compute which callee-saved registers we actually use.
        savedRegs := make([]int, 0)
        for _, r := range calleeSavedInt {
                if c.usedIntRegs[r] {
                        savedRegs = append(savedRegs, r)
                }
        }
        for _, r := range calleeSavedFloat {
                if c.usedFloatRegs[r] {
                        savedRegs = append(savedRegs, r)
                }
        }

        // Add space for FP and RA
        frameSize := (-c.frameOff + 7) &^ 7 // round up to 8-byte alignment
        frameSize += (len(savedRegs) + 2) * 8 // +2 for FP and RA
        frameSize = (frameSize + 15) &^ 15    // 16-byte alignment

        if c.curFunc != nil {
                c.curFunc.FrameSize = frameSize
                c.curFunc.CalleeSave = savedRegs
        }

        // Adjust stack pointer
        if frameSize <= 2048 {
                c.asm.Emit(ADDI(SP, SP, -int16(frameSize)))
        } else {
                c.emitLoadImm(T0, int64(frameSize))
                c.asm.Emit(SUB(SP, SP, T0))
        }

        // Save RA and FP
        off := 0
        c.asm.Emit(SD(SP, RA, int16(off)))
        off += 8
        c.asm.Emit(SD(SP, FP, int16(off)))
        off += 8

        // Save callee-saved registers
        for _, r := range savedRegs {
                c.asm.Emit(SD(SP, r, int16(off)))
                off += 8
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
        off := 16 // skip RA and FP slots
        if c.curFunc != nil {
                for range c.curFunc.CalleeSave {
                        c.asm.Emit(LD(T0, SP, int16(off)))
                        off += 8
                        // We'd need to know which register to restore here;
                        // simplified: we reload into the saved registers
                }
        }

        // Restore FP and RA
        c.asm.Emit(LD(FP, SP, 8))
        c.asm.Emit(LD(RA, SP, 0))

        // Restore stack pointer
        if frameSize <= 2048 {
                c.asm.Emit(ADDI(SP, SP, int16(frameSize)))
        } else {
                c.emitLoadImm(T0, int64(frameSize))
                c.asm.Emit(ADD(SP, SP, T0))
        }

        // Return
        c.asm.Emit(JALR(ZERO, RA, 0))
}

// =============================================================================
// Expression compilation - returns the register holding the result
// =============================================================================

// compileExpr compiles an expression and returns the register holding the result value.
// The returned register is always a caller-saved temporary that should be freed when done.
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
                // TODO: implement member assignment for RISC-V 64
                c.compileExpr(e.Obj)
                c.compileExpr(e.Value)
                return 0
        case *ast.SpawnExpr:
                return c.compileSpawnExpr(e)
        case *ast.AwaitExpr:
                return c.compileAwaitExpr(e)
        default:
                c.diag.Error("", 0, 0, 0, fmt.Sprintf("rv64: unhandled expression type %T", expr))
                r := c.allocReg()
                c.asm.Emit(ADDI(r, ZERO, 0))
                return r
        }
}

func (c *Compiler) compileIntLit(e *ast.IntLit) int {
        r := c.allocReg()
        c.emitLoadImm(r, e.Value)
        return r
}

func (c *Compiler) compileFloatLit(e *ast.FloatLit) int {
        // For tagged values: store float bits in integer register with NaN-boxing.
        r := c.allocReg()
        c.emitLoadFloatImm(FT0, e.Value)
        c.asm.Emit(FMV_X_D(r, FT0))
        // Set the tag bit for NaN-boxing
        c.asm.Emit(ORI(r, r, 1)) // bit 63 is set because the MSB of a normal double is set via NaN-boxing
        // Actually, for proper NaN-boxing we need to ensure bit 63 is set.
        // For IEEE 754 doubles, quiet NaN has exp=0x7FF and the MSB of mantissa set.
        // We rely on the runtime to handle proper boxing.
        // For now, set bit 0 as a float tag indicator.
        return r
}

func (c *Compiler) compileStringLit(e *ast.StringLit) int {
        // Allocate string in data section and return a pointer to it.
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
                if loc.IsFP {
                        // Float variable in integer context - move to int reg
                        r := c.allocReg()
                        c.asm.Emit(FMV_X_D(r, loc.FReg))
                        return r
                }
                // Copy from variable's register to a temp
                r := c.allocReg()
                c.asm.Emit(ADD(r, loc.Reg, ZERO))
                return r
        case VarStack:
                r := c.allocReg()
                c.asm.Emit(LD(r, FP, int16(loc.Off)))
                return r
        case VarGlobal:
                r := c.allocReg()
                c.emitLoadAddr(r, e.Name+"_data")
                c.asm.Emit(LD(r, r, 0))
                return r
        case VarImm:
                r := c.allocReg()
                c.emitLoadImm(r, loc.Imm)
                return r
        }
        return c.allocReg()
}

func (c *Compiler) compileBinOp(e *ast.BinOp) int {
        // Short-circuit for logical operators
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
                // Result in left: 1 if equal, 0 if not
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

        // Evaluate left; if false, short-circuit
        left := c.compileExpr(e.Left)
        falseOff := c.asm.PC()
        c.asm.Emit(BEQ(left, ZERO, 0)) // will be patched
        c.freeReg(left)

        // Evaluate right; if false, short-circuit
        right := c.compileExpr(e.Right)
        rightFalseOff := c.asm.PC()
        c.asm.Emit(BEQ(right, ZERO, 0)) // will be patched
        c.asm.Emit(ADDI(r, ZERO, 1)) // true
        endOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0)) // jump to end, will be patched
        c.freeReg(right)

        // false path
        falseAddr := c.asm.PC()
        c.asm.Emit(ADDI(r, ZERO, 0)) // false

        // Patch jumps
        c.asm.code[falseOff/4] = patchBranchImm(c.asm.code[falseOff/4], int16(falseAddr-falseOff))
        c.asm.code[rightFalseOff/4] = patchBranchImm(c.asm.code[rightFalseOff/4], int16(falseAddr-rightFalseOff))
        c.asm.code[endOff/4] = patchJumpImm(c.asm.code[endOff/4], int32(c.asm.PC()-endOff))

        return r
}

func (c *Compiler) compileLogicalOr(e *ast.BinOp) int {
        r := c.allocReg()

        left := c.compileExpr(e.Left)
        trueOff := c.asm.PC()
        c.asm.Emit(BNE(left, ZERO, 0)) // will be patched
        c.freeReg(left)

        right := c.compileExpr(e.Right)
        trueOff2 := c.asm.PC()
        c.asm.Emit(BNE(right, ZERO, 0)) // will be patched
        c.asm.Emit(ADDI(r, ZERO, 0)) // false
        endOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0)) // will be patched
        c.freeReg(right)

        trueAddr := c.asm.PC()
        c.asm.Emit(ADDI(r, ZERO, 1)) // true

        // Patch
        c.asm.code[trueOff/4] = patchBranchImm(c.asm.code[trueOff/4], int16(trueAddr-trueOff))
        c.asm.code[trueOff2/4] = patchBranchImm(c.asm.code[trueOff2/4], int16(trueAddr-trueOff2))
        c.asm.code[endOff/4] = patchJumpImm(c.asm.code[endOff/4], int32(c.asm.PC()-endOff))

        return r
}

func (c *Compiler) compileUnaryOp(e *ast.UnaryOp) int {
        operand := c.compileExpr(e.Operand)

        switch e.Op {
        case ast.TMinus:
                c.asm.Emit(SUB(operand, ZERO, operand)) // negate: 0 - operand
        case ast.TNot:
                // Logical not: result = (operand == 0) ? 1 : 0
                c.asm.Emit(SLTIU(operand, operand, 1))
        case ast.TTilde:
                c.asm.Emit(XORI(operand, operand, -1)) // bitwise not
        default:
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("unhandled unary operator: %s", e.Op))
                c.asm.Emit(ADDI(operand, ZERO, 0))
        }

        return operand
}

func (c *Compiler) compileCallExpr(e *ast.CallExpr) int {
        // Compile arguments into A0-A7
        argRegs := []int{A0, A1, A2, A3, A4, A5, A6, A7}
        var spilledArgs []int // for args beyond a7

        for i, arg := range e.Args {
                val := c.compileExpr(arg)
                if i < len(argRegs) {
                        if val != argRegs[i] {
                                c.asm.Emit(ADD(argRegs[i], val, ZERO))
                        }
                        c.freeReg(val)
                } else {
                        // Spill to stack
                        spillOff := c.allocFrame(8)
                        c.asm.Emit(SD(FP, val, int16(spillOff)))
                        spilledArgs = append(spilledArgs, spillOff)
                        c.freeReg(val)
                }
        }

        // Determine the callee
        var funcName string
        if ident, ok := e.Func.(*ast.Ident); ok {
                funcName = ident.Name
        }

        // Call the function
        if fi, ok := c.funcMap[funcName]; ok && fi != nil {
                // Direct call to known function
                if fi.CodeStart == c.asm.PC() {
                        // Calling ourselves (recursion) - use offset 0
                        c.asm.Emit(JAL(RA, 0))
                } else {
                        callLabel := fmt.Sprintf("func_%s", funcName)
                        c.asm.Emit(JAL(RA, 0)) // placeholder
                        c.asm.FixupJump(callLabel, RA)
                }
        } else if funcName != "" {
                // External function - emit call via PLT-like sequence
                callLabel := fmt.Sprintf("plt_%s", funcName)
                c.asm.Emit(JAL(RA, 0))
                c.asm.FixupJump(callLabel, RA)
        } else {
                // Indirect call
                funcVal := c.compileExpr(e.Func)
                c.asm.Emit(JALR(RA, funcVal, 0))
                c.freeReg(funcVal)
        }

        // Restore spilled args from stack
        for range spilledArgs {
                // Just adjust frame - we don't need the values anymore
                c.frameOff += 8
        }

        // Return value is in A0
        r := c.allocReg()
        c.asm.Emit(ADD(r, A0, ZERO))
        return r
}

func (c *Compiler) compileIndexExpr(e *ast.IndexExpr) int {
        // table[key] - compile obj and key, then emit runtime call
        obj := c.compileExpr(e.Obj)
        key := c.compileExpr(e.Key)

        // Move to argument registers
        c.asm.Emit(ADD(A0, obj, ZERO))
        c.asm.Emit(ADD(A1, key, ZERO))
        c.freeReg(obj)
        c.freeReg(key)

        // Call runtime table_get
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_table_get", RA)

        r := c.allocReg()
        c.asm.Emit(ADD(r, A0, ZERO))
        return r
}

func (c *Compiler) compileMemberExpr(e *ast.MemberExpr) int {
        // module.name - for now, treat as a simple name lookup
        r := c.allocReg()
        loc := c.scope.Lookup(e.Field)
        if loc != nil {
                switch loc.Kind {
                case VarReg:
                        c.asm.Emit(ADD(r, loc.Reg, ZERO))
                case VarStack:
                        c.asm.Emit(LD(r, FP, int16(loc.Off)))
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
        // Create a table literal by allocating it in the runtime.
        // Call runtime_table_new with the number of entries, then fill each entry.
        n := len(e.Entries)
        c.emitLoadImm(A0, int64(n))
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
                                c.asm.Emit(SD(FP, val, int16(loc.Off)))
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
        // Spawn creates a new goroutine/task - call runtime_spawn
        if e.Call == nil {
                r := c.allocReg()
                c.asm.Emit(ADDI(r, ZERO, 0))
                return r
        }

        // For spawn, we create a closure-like structure
        // Simplified: call runtime_spawn with a function pointer
        c.compileCallExpr(e.Call) // result in A0
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
                c.diag.Error("", 0, 0, 0, fmt.Sprintf("rv64: unhandled statement type %T", stmt))
        }
}

func (c *Compiler) compileLetStmt(s *ast.LetStmt) {
        val := c.compileExpr(s.Value)

        // Allocate stack space and store
        off := c.allocFrame(8)
        loc := &VarLoc{Kind: VarStack, Off: off}
        c.asm.Emit(SD(FP, val, int16(off)))
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
        // Compile if/but/else chain
        // Pattern: evaluate each condition, branch to body or next
        endLabel := c.nextTemp("if_end")

        for i, branch := range s.Branches {
                if i > 0 {
                        // Previous branch fell through - emit jump to end
                        c.asm.Emit(JAL(ZERO, 0)) // placeholder
                        // We'll patch these below
                }


                cond := c.compileExpr(branch.Cond)
                c.asm.Emit(BNE(cond, ZERO, 0)) // branch if true
                nextCondOff := c.asm.PC()
                c.asm.Emit(JAL(ZERO, 0)) // jump to next condition or else

                c.freeReg(cond)

                // Compile body in a new scope
                c.scope = newScope(c.scope)
                for _, stmt := range branch.Body {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent

                // Jump to end
                c.asm.Emit(JAL(ZERO, 0))

                // Patch: next condition target
                bodyStart := c.asm.PC()
                c.asm.code[nextCondOff/4] = patchJumpImm(c.asm.code[nextCondOff/4], int32(bodyStart-nextCondOff))
        }

        // Else branch
        if len(s.Else) > 0 {
                c.scope = newScope(c.scope)
                for _, stmt := range s.Else {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent
        }

        // Patch all end jumps
        c.asm.Bind(endLabel)
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

        // Loop condition
        c.asm.Label(loopLabel)
        cond := c.compileExpr(s.Cond)
        c.asm.Emit(BEQ(cond, ZERO, 0)) // branch if false to end
        endOff := c.asm.PC()
        c.asm.Emit(JAL(ZERO, 0)) // placeholder
        c.freeReg(cond)

        // Body
        c.scope = newScope(c.scope)
        for _, stmt := range s.Body {
                c.compileStmt(stmt)
        }
        c.scope = c.scope.parent

        // Jump back to condition
        c.asm.Emit(JAL(ZERO, 0))
        backOff := c.asm.PC()
        c.asm.code[backOff/4] = patchJumpImm(c.asm.code[backOff/4], int32(c.asm.LabelAddr(loopLabel)-backOff))

        // Patch end jump
        c.asm.Bind(endLabel)
        c.asm.code[endOff/4] = patchJumpImm(c.asm.code[endOff/4], int32(c.asm.LabelAddr(endLabel)-endOff))

        c.loopStack = c.loopStack[:len(c.loopStack)-1]
}

func (c *Compiler) compileForStmt(s *ast.ForStmt) {
        // for key, value in collection
        // Desugar to: create iterator, loop with next()
        iterLabel := c.nextTemp("for_iter")
        endLabel := c.nextTemp("for_end")

        ctx := &LoopCtx{
                ContinueLabel: iterLabel,
                BreakLabel:    endLabel,
                Depth:         len(c.loopStack),
        }
        c.loopStack = append(c.loopStack, ctx)

        // Create iterator from collection
        collection := c.compileExpr(s.Over)
        c.asm.Emit(ADD(A0, collection, ZERO))
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_iter_new", RA)

        // Iterator handle in a register
        iterReg := c.allocReg()
        c.asm.Emit(ADD(iterReg, A0, ZERO))
        c.freeReg(collection)

        c.asm.Label(iterLabel)

        // Get next: runtime_iter_next(iter)
        c.asm.Emit(ADD(A0, iterReg, ZERO))
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("runtime_iter_next", RA)

        // A0 = 1 if more, 0 if done
        doneOff := c.asm.PC()
        c.asm.Emit(BEQ(A0, ZERO, 0)) // branch if done
        c.asm.Emit(JAL(ZERO, 0))     // placeholder

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

        // A1 = key, A2 = value (from accessor calls)
        c.scope = newScope(c.scope)
        if s.Key != "" {
                keyOff := c.allocFrame(8)
                c.asm.Emit(SD(FP, keyTmp, int16(keyOff)))
                c.scope.Define(s.Key, &VarLoc{Kind: VarStack, Off: keyOff})
        }
        if s.Value != "" {
                valOff := c.allocFrame(8)
                c.asm.Emit(SD(FP, A0, int16(valOff)))
                c.scope.Define(s.Value, &VarLoc{Kind: VarStack, Off: valOff})
        }
        c.freeReg(keyTmp)

        for _, stmt := range s.Body {
                c.compileStmt(stmt)
        }
        c.scope = c.scope.parent

        // Jump back to iterator
        c.asm.Emit(JAL(ZERO, 0))
        backOff := c.asm.PC()
        c.asm.code[backOff/4] = patchJumpImm(c.asm.code[backOff/4], int32(c.asm.LabelAddr(iterLabel)-backOff))

        // Patch done jump
        c.asm.Bind(endLabel)
        c.asm.code[doneOff/4] = patchBranchImm(c.asm.code[doneOff/4], int16(c.asm.LabelAddr(endLabel)-doneOff))

        c.freeReg(iterReg)
        c.loopStack = c.loopStack[:len(c.loopStack)-1]
}

func (c *Compiler) compileMatchStmt(s *ast.MatchStmt) {
        subject := c.compileExpr(s.Subject)
        endLabel := c.nextTemp("match_end")

        for _, mc := range s.Cases {
                caseVal := c.compileExpr(mc.Value)
                c.asm.Emit(XOR(subject, subject, caseVal))
                c.freeReg(caseVal)

                nextOff := c.asm.PC()
                c.asm.Emit(BNE(subject, ZERO, 0)) // if not equal, skip body

                c.scope = newScope(c.scope)
                for _, stmt := range mc.Body {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent

                // Jump to end
                c.asm.Emit(JAL(ZERO, 0))

                // Patch: next case starts here
                nextCaseAddr := c.asm.PC()
                c.asm.code[nextOff/4] = patchBranchImm(c.asm.code[nextOff/4], int16(nextCaseAddr-nextOff))
        }

        // Default case
        if len(s.Default) > 0 {
                c.scope = newScope(c.scope)
                for _, stmt := range s.Default {
                        c.compileStmt(stmt)
                }
                c.scope = c.scope.parent
        }

        c.freeReg(subject)
        c.asm.Bind(endLabel)
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

// Compile compiles a full Yilt program into RV64 machine code.
func (c *Compiler) Compile(program *ast.Program) (*Output, error) {
        if c.diag.HasErrors() {
                return nil, fmt.Errorf("cannot compile with %d errors", c.diag.ErrorCount())
        }

        // First pass: collect all function declarations
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

        // Second pass: compile each function
        for _, file := range program.Files {
                for _, decl := range file.Decls {
                        if fn, ok := decl.(*ast.FnDecl); ok {
                                if fn.Extern {
                                        continue // extern functions are linked, not compiled
                                }
                                c.compileFunction(fn)
                        }
                }
        }

        // Compile _start entry point for bare metal or _main for linux
        c.compileEntry(program)

        return c.buildOutput()
}

// compileFunction compiles a single function.
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

        // Always use FP
        c.usedIntRegs[FP] = true

        // Bind parameters to argument registers or stack locations
        argRegs := []int{A0, A1, A2, A3, A4, A5, A6, A7}
        for i, param := range fn.Params {
                if i < len(argRegs) {
                        loc := &VarLoc{Kind: VarReg, Reg: argRegs[i]}
                        c.scope.Define(param.Name, loc)
                        c.usedIntRegs[argRegs[i]] = true
                } else {
                        // Extra parameters are on the stack above FP
                        off := (i - len(argRegs) + 1) * 8
                        loc := &VarLoc{Kind: VarStack, Off: off}
                        c.scope.Define(param.Name, loc)
                }
        }

        // Mark FP and SP as used
        c.usedIntRegs[SP] = true
        c.usedIntRegs[ZERO] = true

        // Emit function label
        funcLabel := fmt.Sprintf("func_%s", fn.Name)
        fi.CodeStart = c.asm.PC()
        c.asm.Label(funcLabel)

        // Emit prologue
        c.emitPrologue()

        // Compile body
        for _, stmt := range fn.Body {
                c.compileStmt(stmt)
        }

        // Emit implicit return if not already present
        c.emitEpilogue()

        fi.CodeLen = c.asm.PC() - fi.CodeStart

        c.curFunc = nil
}

// compileEntry generates the program entry point.
func (c *Compiler) compileEntry(program *ast.Program) {
        c.curFunc = &FuncInfo{Name: "_start", Index: len(c.funcs)}
        c.frameOff = 0
        c.scope = newScope(nil)

        c.asm.Label("_start")

        // For bare metal: call main, then ecall to exit
        c.asm.Emit(JAL(RA, 0))
        c.asm.FixupJump("func_main", RA)

        // Exit with code from A0
        c.asm.Emit(ADDI(A0, A0, 0)) // exit code
        c.asm.Emit(ECALL())

        c.curFunc = nil
}

// =============================================================================
// Output generation
// =============================================================================

// Output represents the compiled output.
type Output struct {
        Code       []byte // assembled code bytes
        Data       []byte // data section bytes
        Relocs     []Reloc
        Functions  []*FuncInfo
        Globals    map[string]*VarLoc
        ELFBytes   []byte // if targeting ELF
}

// buildOutput assembles the final output.
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

        if c.target.OS == "linux" || c.target.OS == "none" {
                elfBytes, err := c.buildELF(code, data)
                if err != nil {
                        return nil, err
                }
                out.ELFBytes = elfBytes
        }

        return out, nil
}

// buildELF creates a minimal ELF relocatable object file.
func (c *Compiler) buildELF(code, data []byte) ([]byte, error) {
        // This is a simplified ELF generator for RV64.
        // In production, you'd use a proper ELF writer or emit assembly and let the linker handle it.
        // Here we emit a minimal ELF with .text and .data sections.

        var buf []byte

        // ELF header (64 bytes for 64-bit)
        ehdr := make([]byte, 64)

        // e_ident
        ehdr[0] = 0x7F // ELFMAG0
        ehdr[1] = 0x45 // ELFMAG1 'E'
        ehdr[2] = 0x4C // ELFMAG2 'L'
        ehdr[3] = 0x46 // ELFMAG3 'F'
        ehdr[4] = 2    // ELFCLASS64
        ehdr[5] = 1    // ELFDATA2LSB (little-endian)
        ehdr[6] = 1    // EV_CURRENT
        ehdr[7] = 0    // ELFOSABI_NONE

        // e_type: ET_REL (relocatable)
        binary.LittleEndian.PutUint16(ehdr[16:18], 1)
        // e_machine: EM_RISCV = 243
        binary.LittleEndian.PutUint16(ehdr[18:20], 243)
        // e_version
        binary.LittleEndian.PutUint32(ehdr[20:24], 1)
        // e_entry
        binary.LittleEndian.PutUint64(ehdr[24:32], 0)
        // e_phoff
        binary.LittleEndian.PutUint64(ehdr[32:40], 0)
        // e_shoff: we'll fill this in later
        shoffPos := 40
        // e_flags: EF_RISCV_RVC=0x0001, EF_RISCV_FLOAT_ABI_DOUBLE=0x0004
        binary.LittleEndian.PutUint32(ehdr[48:52], 0x0004)
        // e_ehsize
        binary.LittleEndian.PutUint16(ehdr[52:54], 64)
        // e_phentsize
        binary.LittleEndian.PutUint16(ehdr[54:56], 0)
        // e_phnum
        binary.LittleEndian.PutUint16(ehdr[56:58], 0)
        // e_shentsize
        binary.LittleEndian.PutUint16(ehdr[58:60], 64)
        // e_shnum: 4 (null + .text + .data + .symtab)
        numSections := 4
        binary.LittleEndian.PutUint16(ehdr[60:62], uint16(numSections))
        // e_shstrndx: 3 (index of .shstrtab)
        binary.LittleEndian.PutUint16(ehdr[62:64], 3)

        buf = append(buf, ehdr...)

        // Section header string table content
        shstrtab := "\x00.text\x00.data\x00.shstrtab\x00"
        shstrtabOffText := 1
        shstrtabOffData := 7
        shstrtabOffShstrtab := 13

        // .text section
        textPad := (8 - (len(code) % 8)) % 8
        textSection := make([]byte, len(code)+textPad)
        copy(textSection, code)

        // .data section
        dataPad := (8 - (len(data) % 8)) % 8
        dataSection := make([]byte, len(data)+dataPad)
        copy(dataSection, data)

        // Calculate offsets
        textOff := uint64(64) // after ELF header
        dataOff := textOff + uint64(len(textSection))
        shstrtabOff := dataOff + uint64(len(dataSection))
        shOff := shstrtabOff + uint64(len(shstrtab))
        // Align shOff to 8 bytes
        shOff = (shOff + 7) &^ 7

        // Write sections
        buf = append(buf, textSection...)
        buf = append(buf, dataSection...)
        buf = append(buf, shstrtab...)

        // Pad to section header offset
        padLen := int(shOff) - len(buf)
        if padLen > 0 {
                buf = append(buf, make([]byte, padLen)...)
        }

        // Write e_shoff
        binary.LittleEndian.PutUint64(buf[shoffPos:shoffPos+8], shOff)

        // Section headers (64 bytes each)
        // SHT_NULL
        buf = append(buf, make([]byte, 64)...)

        // .text section header
        writeSectionHeader(&buf, uint32(elf.SHT_PROGBITS), uint32(shstrtabOffText), uint64(len(code)), 0, 0, uint64(6))

        // .data section header
        writeSectionHeader(&buf, uint32(elf.SHT_PROGBITS), uint32(shstrtabOffData), uint64(len(data)), 0, 0, uint64(3))

        // .shstrtab section header
        writeSectionHeader(&buf, uint32(elf.SHT_STRTAB), uint32(shstrtabOffShstrtab), uint64(len(shstrtab)), 0, 0, 0)

        return buf, nil
}

// writeSectionHeader writes a single ELF64 section header.
func writeSectionHeader(buf *[]byte, shType uint32, name uint32, size uint64, link uint32, info uint32, flags uint64) {
        sh := make([]byte, 64)
        binary.LittleEndian.PutUint32(sh[0:4], name)    // sh_name
        binary.LittleEndian.PutUint32(sh[4:8], shType)  // sh_type
        binary.LittleEndian.PutUint64(sh[8:16], flags)  // sh_flags
        binary.LittleEndian.PutUint64(sh[16:24], 0)     // sh_addr
        binary.LittleEndian.PutUint64(sh[24:32], 0)     // sh_offset (filled by linker)
        binary.LittleEndian.PutUint64(sh[32:40], size)  // sh_size
        binary.LittleEndian.PutUint32(sh[40:44], link)  // sh_link
        binary.LittleEndian.PutUint32(sh[44:48], info)  // sh_info
        binary.LittleEndian.PutUint64(sh[48:56], 8)     // sh_addralign
        binary.LittleEndian.PutUint64(sh[56:64], 0)     // sh_entsize
        *buf = append(*buf, sh...)
}

// =============================================================================
// Utility functions
// =============================================================================

// LabelAddr returns the address of a label. Used for manual patching.
func (a *AsmBuffer) LabelAddr(name string) int {
        if l, ok := a.labels[name]; ok {
                return l.Addr
        }
        return -1
}

// RegName returns the ABI name of an integer register.
func RegName(r int) string {
        switch r {
        case X0:
                return "zero"
        case X1:
                return "ra"
        case X2:
                return "sp"
        case X3:
                return "gp"
        case X4:
                return "tp"
        case X5:
                return "t0"
        case X6:
                return "t1"
        case X7:
                return "t2"
        case X8:
                return "s0/fp"
        case X9:
                return "s1"
        case X10:
                return "a0"
        case X11:
                return "a1"
        case X12:
                return "a2"
        case X13:
                return "a3"
        case X14:
                return "a4"
        case X15:
                return "a5"
        case X16:
                return "a6"
        case X17:
                return "a7"
        case X18:
                return "s2"
        case X19:
                return "s3"
        case X20:
                return "s4"
        case X21:
                return "s5"
        case X22:
                return "s6"
        case X23:
                return "s7"
        case X24:
                return "s8"
        case X25:
                return "s9"
        case X26:
                return "s10"
        case X27:
                return "s11"
        case X28:
                return "t3"
        case X29:
                return "t4"
        case X30:
                return "t5"
        case X31:
                return "t6"
        default:
                return fmt.Sprintf("x%d", r)
        }
}

// FRegName returns the ABI name of a floating-point register.
func FRegName(r int) string {
        switch r {
        case F0:
                return "ft0"
        case F1:
                return "ft1"
        case F2:
                return "ft2"
        case F3:
                return "ft3"
        case F4:
                return "ft4"
        case F5:
                return "ft5"
        case F6:
                return "ft6"
        case F7:
                return "ft7"
        case F8:
                return "fs0"
        case F9:
                return "fs1"
        case F10:
                return "fa0"
        case F11:
                return "fa1"
        case F12:
                return "fa2"
        case F13:
                return "fa3"
        case F14:
                return "fa4"
        case F15:
                return "fa5"
        case F16:
                return "fa6"
        case F17:
                return "fa7"
        case F18:
                return "fs2"
        case F19:
                return "fs3"
        case F20:
                return "fs4"
        case F21:
                return "fs5"
        case F22:
                return "fs6"
        case F23:
                return "fs7"
        case F24:
                return "fs8"
        case F25:
                return "fs9"
        case F26:
                return "fs10"
        case F27:
                return "fs11"
        case F28:
                return "ft8"
        case F29:
                return "ft9"
        case F30:
                return "ft10"
        case F31:
                return "ft11"
        default:
                return fmt.Sprintf("f%d", r)
        }
}

// Disasm disassembles a single 32-bit instruction into a human-readable string.
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
                case funct3ADDI:
                        if rd == 0 && rs1 == 0 && imm == 0 {
                                return "nop"
                        }
                        return fmt.Sprintf("addi %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case funct3SLTI:
                        return fmt.Sprintf("slti %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case funct3SLTIU:
                        return fmt.Sprintf("sltiu %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), uint16(imm))
                case funct3XORI:
                        return fmt.Sprintf("xori %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case funct3ORI:
                        return fmt.Sprintf("ori %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case funct3ANDI:
                        return fmt.Sprintf("andi %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case funct3SLLI:
                        shamt := rs2 & 0x3F
                        return fmt.Sprintf("slli %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                case funct3SRLI:
                        shamt := rs2 & 0x3F
                        if funct7&0x20 != 0 {
                                return fmt.Sprintf("srai %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                        }
                        return fmt.Sprintf("srli %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                }
        case opOpImm32:
                imm := int16((instr >> 20) & 0xFFF)
                switch funct3 {
                case 0:
                        return fmt.Sprintf("addiw %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), imm)
                case funct3SLLI:
                        shamt := rs2 & 0x1F
                        return fmt.Sprintf("slliw %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                case funct3SRLI:
                        shamt := rs2 & 0x1F
                        if funct7&0x20 != 0 {
                                return fmt.Sprintf("sraiw %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                        }
                        return fmt.Sprintf("srliw %s, %s, %d", RegName(int(rd)), RegName(int(rs1)), shamt)
                }
        case opOp:
                switch funct3 {
                case funct3ADD:
                        if funct7 == funct7SUB {
                                return fmt.Sprintf("sub %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        }
                        return fmt.Sprintf("add %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case funct3SLL:
                        return fmt.Sprintf("sll %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case funct3SLT:
                        return fmt.Sprintf("slt %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case funct3SLTU:
                        return fmt.Sprintf("sltu %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case funct3XOR:
                        return fmt.Sprintf("xor %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case funct3SRL:
                        if funct7 == funct7SRA {
                                return fmt.Sprintf("sra %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        }
                        return fmt.Sprintf("srl %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case funct3OR:
                        return fmt.Sprintf("or %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                case funct3AND:
                        return fmt.Sprintf("and %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                }
                if funct7 == funct7MULD {
                        switch funct3 {
                        case funct3MUL:
                                return fmt.Sprintf("mul %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case funct3DIV:
                                return fmt.Sprintf("div %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case funct3DIVU:
                                return fmt.Sprintf("divu %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case funct3REM:
                                return fmt.Sprintf("rem %s, %s, %s", RegName(int(rd)), RegName(int(rs1)), RegName(int(rs2)))
                        case funct3REMU:
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
                case funct3BEQ:
                        return fmt.Sprintf("beq %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case funct3BNE:
                        return fmt.Sprintf("bne %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case funct3BLT:
                        return fmt.Sprintf("blt %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case funct3BGE:
                        return fmt.Sprintf("bge %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case funct3BLTU:
                        return fmt.Sprintf("bltu %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                case funct3BGEU:
                        return fmt.Sprintf("bgeu %s, %s, 0x%x", RegName(int(rs1)), RegName(int(rs2)), target)
                }
        case opLoad:
                imm := int16((instr >> 20) & 0xFFF)
                switch funct3 {
                case funct3LB:
                        return fmt.Sprintf("lb %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case funct3LH:
                        return fmt.Sprintf("lh %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case funct3LW:
                        return fmt.Sprintf("lw %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case funct3LD:
                        return fmt.Sprintf("ld %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case funct3LBU:
                        return fmt.Sprintf("lbu %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case funct3LHU:
                        return fmt.Sprintf("lhu %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                case funct3LWU:
                        return fmt.Sprintf("lwu %s, %d(%s)", RegName(int(rd)), imm, RegName(int(rs1)))
                }
        case opStore:
                imm := int16(((instr >> 25) << 5) | ((instr >> 7) & 0x1F))
                switch funct3 {
                case funct3SB:
                        return fmt.Sprintf("sb %s, %d(%s)", RegName(int(rs2)), imm, RegName(int(rs1)))
                case funct3SH:
                        return fmt.Sprintf("sh %s, %d(%s)", RegName(int(rs2)), imm, RegName(int(rs1)))
                case funct3SW:
                        return fmt.Sprintf("sw %s, %d(%s)", RegName(int(rs2)), imm, RegName(int(rs1)))
                case funct3SD:
                        return fmt.Sprintf("sd %s, %d(%s)", RegName(int(rs2)), imm, RegName(int(rs1)))
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

// decodeJImm extracts the J-format immediate from an instruction.
func decodeJImm(instr uint32) int32 {
        bit20 := int32((instr >> 31) & 1)
        bits10to1 := int32((instr >> 21) & 0x3FF)
        bit11 := int32((instr >> 20) & 1)
        bits19to12 := int32((instr >> 12) & 0xFF)
        imm := (bit20 << 20) | (bits19to12 << 12) | (bit11 << 11) | (bits10to1 << 1)
        // Sign extend
        if bit20 != 0 {
                imm |= ^(int32(0) << 21)
        }
        return imm
}

// decodeBImm extracts the B-format immediate from an instruction.
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
