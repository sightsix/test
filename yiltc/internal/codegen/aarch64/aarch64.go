package aarch64

import (
        "encoding/binary"
        "fmt"
        "math"
        "strings"
)

// Register IDs for AArch64.
type Reg int

const (
        R0 Reg = iota
        R1
        R2
        R3
        R4
        R5
        R6
        R7
        R8
        R9
        R10
        R11
        R12
        R13
        R14
        R15
        R16
        R17
        R18
        R19
        R20
        R21
        R22
        R23
        R24
        R25
        R26
        R27
        R28
        R29 // FP
        R30 // LR
        R31 // SP/ZR
        RZR  = R31
        RSP  = R31

        // FP registers
        F0 Reg = iota + 64
        F1
        F2
        F3
        F4
        F5
        F6
        F7
        F8
        F9
        F10
        F11
        F12
        F13
        F14
        F15
        F16
        F17
        F18
        F19
        F20
        F21
        F22
        F23
        F24
        F25
        F26
        F27
        F28
        F29
        F30
        F31
)

func (r Reg) String() string {
        if r >= F0 {
                return fmt.Sprintf("D%d", int(r-F0))
        }
        switch r {
        case R29:
                return "FP"
        case R30:
                return "LR"
        case R31:
                return "ZR"
        default:
                return fmt.Sprintf("X%d", int(r))
        }
}

// IsCallerSaved returns true for caller-saved registers.
func (r Reg) IsCallerSaved() bool {
        if r >= F0 {
                return r <= F7 || (r >= F16 && r <= F31)
        }
        return (r >= R0 && r <= R7) || (r >= R16 && r <= R17)
}

// IsCalleeSaved returns true for callee-saved registers.
func (r Reg) IsCalleeSaved() bool {
        if r >= F0 {
                return r >= F8 && r <= F15
        }
        return r >= R19 && r <= R28
}

// IsGPR returns true if this is a general-purpose register.
func (r Reg) IsGPR() bool { return r < 64 }

// IsFP returns true if this is a floating-point register.
func (r Reg) IsFP() bool { return r >= 64 }

// ABI registers.
var (
        // Integer argument registers.
        ArgRegs = []Reg{R0, R1, R2, R3, R4, R5, R6, R7}
        // Float argument registers.
        FPArgRegs = []Reg{F0, F1, F2, F3, F4, F5, F6, F7}
        // Return value registers.
        RetRegs = []Reg{R0, R1}
        FPRetRegs = []Reg{F0, F1}
        // Callee-saved GPRs (must be preserved).
        CalleeSavedRegs = []Reg{R19, R20, R21, R22, R23, R24, R25, R26, R27, R28}
        // Callee-saved FP registers.
        CalleeSavedFPRegs = []Reg{F8, F9, F10, F11, F12, F13, F14, F15}
        // Temp registers (caller-saved).
        TempRegs = []Reg{R9, R10, R11, R12, R13, R14, R15}
)

// Target describes an AArch64 target.
type Target struct {
        Triple    string // e.g., "aarch64-linux-gnu"
        OS        string // "linux", "android", "windows", "macos", "none"
        Features  map[string]bool
}

// ParseTarget parses a target triple.
func ParseTarget(triple string) Target {
        parts := strings.SplitN(triple, "-", 2)
        t := Target{Triple: triple, Features: make(map[string]bool)}
        t.OS = "none"
        if len(parts) >= 2 {
                os := parts[1]
                if strings.Contains(os, "linux") {
                        t.OS = "linux"
                        if strings.Contains(os, "android") {
                                t.OS = "android"
                        }
                } else if strings.Contains(os, "windows") {
                        t.OS = "windows"
                } else if strings.Contains(os, "macos") || strings.Contains(os, "darwin") {
                        t.OS = "macos"
                }
        }
        return t
}

// ========== Instruction Encoding ==========

// Assembler produces AArch64 machine code.
type Assembler struct {
        code []uint32
        // Label -> offset
        labels map[string]int
        // Pending fixups: offset in code -> label name
        fixups map[int]string
        // Current offset
        offset int
}

func NewAssembler() *Assembler {
        return &Assembler{
                labels: make(map[string]int),
                fixups: make(map[int]string),
        }
}

// Code returns the assembled machine code as bytes.
func (a *Assembler) Code() []byte {
        // Apply fixups first
        for off, label := range a.fixups {
                target, ok := a.labels[label]
                if !ok {
                        continue
                }
                // Compute relative offset (in instructions, not bytes)
                pc := off / 4
                rel := target - pc
                a.code[off/4] |= encodeImm26(rel)
        }

        buf := make([]byte, len(a.code)*4)
        for i, instr := range a.code {
                binary.LittleEndian.PutUint32(buf[i*4:], instr)
        }
        return buf
}

// Len returns the length in bytes.
func (a *Assembler) Len() int { return a.offset }

// Offset returns the current offset in bytes.
func (a *Assembler) Offset() int { return a.offset }

// BindLabel defines a label at the current offset.
func (a *Assembler) BindLabel(name string) {
        a.labels[name] = a.offset / 4
}

// emit emits a single 32-bit instruction.
func (a *Assembler) emit(instr uint32) {
        idx := a.offset / 4
        for idx >= len(a.code) {
                a.code = append(a.code, 0)
        }
        a.code[idx] = instr
        a.offset += 4
}

// ========== Encoding Helpers ==========

func encodeImm26(imm int) uint32 {
        return uint32(imm & 0x03FFFFFF)
}

func encodeImm19(imm int) uint32 {
        return uint32(imm & 0x7FFFF)
}

func encodeImm14(imm int) uint32 {
        return uint32(imm & 0x3FFF)
}

func encodeBits(lo, hi, bits, value int) uint32 {
        mask := (1 << (hi - lo + 1)) - 1
        return uint32((value & mask) << lo)
}

func encodeRd(r Reg) uint32 {
        if r >= F0 {
                return uint32((r - F0) << 5) // Not used for Rd in normal GPR
        }
        return uint32(r << 5)
}

func encodeRn(r Reg) uint32 {
        if r >= F0 {
                return uint32((r - F0) << 5)
        }
        return uint32(r << 5)
}

func encodeRm(r Reg) uint32 {
        if r >= F0 {
                return uint32(r - F0)
        }
        return uint32(r)
}

func encodeRt(r Reg) uint32 {
        return encodeRd(r)
}

func encodeRt2(r Reg) uint32 {
        return uint32(r << 10)
}

func encodeCond(cond int) uint32 { return uint32(cond) }

// Conditions.
const (
        CondEQ = 0 // equal
        CondNE = 1 // not equal
        CondCS = 2 // carry set (HS)
        CondCC = 3 // carry clear (LO)
        CondMI = 4 // negative
        CondPL = 5 // positive or zero
        CondVS = 6 // overflow
        CondVC = 7 // no overflow
        CondHI = 8 // unsigned higher
        CondLS = 9 // unsigned lower or same
        CondGE = 10 // signed >=
        CondLT = 11 // signed <
        CondGT = 12 // signed >
        CondLE = 13 // signed <=
)

// ========== Data Processing (Immediate) ==========

// MOVZ - Move with zero.
func (a *Assembler) MOVZ(rd Reg, imm uint16, shift int) {
        var hw uint32
        switch shift {
        case 0:
                hw = 0
        case 16:
                hw = 1
        case 32:
                hw = 2
        case 48:
                hw = 3
        default:
                hw = 0
        }
        a.emit(0x52800000 | encodeBits(21, 21, 0, int(hw)) | uint32(imm<<5) | encodeRd(rd))
}

// MOVK - Move with keep.
func (a *Assembler) MOVK(rd Reg, imm uint16, shift int) {
        var hw uint32
        switch shift {
        case 0:
                hw = 0
        case 16:
                hw = 1
        case 32:
                hw = 2
        case 48:
                hw = 3
        default:
                hw = 0
        }
        a.emit(0x72800000 | encodeBits(21, 21, 0, int(hw)) | uint32(imm<<5) | encodeRd(rd))
}

// MOVN - Move with NOT.
func (a *Assembler) MOVN(rd Reg, imm uint16, shift int) {
        var hw uint32
        switch shift {
        case 0:
                hw = 0
        case 16:
                hw = 1
        default:
                hw = 0
        }
        a.emit(0x12800000 | encodeBits(21, 21, 0, int(hw)) | uint32(imm<<5) | encodeRd(rd))
}

// ========== Data Processing (Register) ==========

// ADD (shifted register).
func (a *Assembler) ADD(rd, rn, rm Reg) {
        a.emit(0x8B000000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// ADD (immediate).
func (a *Assembler) ADDI(rd, rn Reg, imm int) {
        a.emit(0x91000000 | encodeImm12(imm) | encodeRn(rn) | encodeRd(rd))
}

// SUB (shifted register).
func (a *Assembler) SUB(rd, rn, rm Reg) {
        a.emit(0xCB000000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// SUB (immediate).
func (a *Assembler) SUBI(rd, rn Reg, imm int) {
        a.emit(0xD1000000 | encodeImm12(imm) | encodeRn(rn) | encodeRd(rd))
}

// AND (shifted register).
func (a *Assembler) AND(rd, rn, rm Reg) {
        a.emit(0x8A000000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// ANDI (immediate).
func (a *Assembler) ANDI(rd, rn Reg, imm uint64) {
        a.emit(0x92000000 | encodeLogicImm(imm) | encodeRn(rn) | encodeRd(rd))
}

// ORR (shifted register) - also used for MOV (ORR Rd, XZR, Rm).
func (a *Assembler) ORR(rd, rn, rm Reg) {
        a.emit(0xAA000000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// ORRI (immediate).
func (a *Assembler) ORRI(rd, rn Reg, imm uint64) {
        a.emit(0xB2000000 | encodeLogicImm(imm) | encodeRn(rn) | encodeRd(rd))
}

// EOR (shifted register).
func (a *Assembler) EOR(rd, rn, rm Reg) {
        a.emit(0xCA000000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// EORI (immediate).
func (a *Assembler) EORI(rd, rn Reg, imm uint64) {
        a.emit(0xB2000000 | encodeLogicImm(imm) | encodeRn(rn) | encodeRd(rd))
}

// ORN - OR NOT.
func (a *Assembler) ORN(rd, rn, rm Reg) {
        a.emit(0xAA200000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// BIC - Bit clear.
func (a *Assembler) BIC(rd, rn, rm Reg) {
        a.emit(0x8A200000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// MADD - Multiply-add.
func (a *Assembler) MADD(rd, rn, rm, ra Reg) {
        a.emit(0x1B000000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd) | uint32(ra))
}

// MSUB - Multiply-subtract.
func (a *Assembler) MSUB(rd, rn, rm, ra Reg) {
        a.emit(0x1B008000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd) | uint32(ra))
}

// SDIV - Signed divide.
func (a *Assembler) SDIV(rd, rn, rm Reg) {
        a.emit(0x1AC00800 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// UDIV - Unsigned divide.
func (a *Assembler) UDIV(rd, rn, rm Reg) {
        a.emit(0x1AC00C00 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// LSL - Logical shift left (alias: UBFM).
func (a *Assembler) LSL(rd, rn Reg, shift int) {
        a.emit(0xD3400000 | uint32((shift&0x3F)<<16) | encodeRn(rn) | encodeRd(rd))
}

// LSR - Logical shift right (alias: UBFM).
func (a *Assembler) LSR(rd, rn Reg, shift int) {
        n := 64 - shift
        a.emit(0xD340FC00 | uint32((n&0x3F)<<16) | encodeRn(rn) | encodeRd(rd))
}

// ASR - Arithmetic shift right (alias: SBFM).
func (a *Assembler) ASR(rd, rn Reg, shift int) {
        a.emit(0x9340FC00 | uint32((shift&0x3F)<<16) | encodeRn(rn) | encodeRd(rd))
}

// NEG - Negate (alias: SUB Rd, XZR, Rn).
func (a *Assembler) NEG(rd, rn Reg) {
        a.SUB(rd, RZR, rn)
}

// NOT - Bitwise NOT (alias: ORN Rd, XZR, Rn).
func (a *Assembler) NOT(rd, rn Reg) {
        a.ORN(rd, RZR, rn)
}

// MOV - Move register (alias: ORR Rd, XZR, Rm).
func (a *Assembler) MOV(rd, rm Reg) {
        a.ORR(rd, RZR, rm)
}

// MOVImm - Move immediate (using MOVZ/MOVK sequence).
func (a *Assembler) MOVImm(rd Reg, val uint64) {
        if val <= 0xFFFF {
                a.MOVZ(rd, uint16(val), 0)
                return
        }
        a.MOVZ(rd, uint16(val), 0)
        a.MOVK(rd, uint16(val>>16), 16)
        if val > 0xFFFFFFFF {
                a.MOVK(rd, uint16(val>>32), 32)
        }
        if val > 0xFFFFFFFFFFFF {
                a.MOVK(rd, uint16(val>>48), 48)
        }
}

// CLZ - Count leading zeros.
func (a *Assembler) CLZ(rd, rn Reg) {
        a.emit(0xDAC01000 | encodeRn(rn) | encodeRd(rd))
}

// RBIT - Reverse bits.
func (a *Assembler) RBIT(rd, rn Reg) {
        a.emit(0xDAC00C00 | encodeRn(rn) | encodeRd(rd))
}

// REV64 - Reverse bytes in 64-bit register.
func (a *Assembler) REV64(rd, rn Reg) {
        a.emit(0xDAC00C00 | encodeRn(rn) | encodeRd(rd))
}

// ========== Compare and Conditional ==========

// CMP (alias: SUBS XZR, Rn, Rm).
func (a *Assembler) CMP(rn, rm Reg) {
        a.emit(0xEB000000 | encodeRm(rm) | encodeRn(rn))
}

// CMPI (alias: SUBS XZR, Rn, imm).
func (a *Assembler) CMPI(rn Reg, imm int) {
        a.emit(0xF1000000 | encodeImm12(imm) | encodeRn(rn))
}

// CMPFP (FCMP).
func (a *Assembler) CMPFP(rn, rm Reg) {
        a.emit(0x1E202000 | encodeRm(rm) | encodeRn(rn))
}

// CSET - Conditional set (alias: CINC Rd, XZR, cond).
func (a *Assembler) CSET(rd Reg, cond int) {
        a.emit(0x1A9F07E0 | encodeCond(cond) | encodeRd(rd))
}

// CINC - Conditional increment.
func (a *Assembler) CINC(rd, rn Reg, cond int) {
        a.emit(0x1A800400 | encodeCond(cond) | encodeRn(rn) | encodeRd(rd))
}

// CSEL - Conditional select.
func (a *Assembler) CSEL(rd, rn, rm Reg, cond int) {
        a.emit(0x1A800000 | encodeCond(cond) | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// FCSEL - Floating-point conditional select.
func (a *Assembler) FCSEL(rd, rn, rm Reg, cond int) {
        a.emit(0x1E200C00 | encodeCond(cond) | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// CCMP - Conditional compare.
func (a *Assembler) CCMP(rn, rm Reg, nzcv int, cond int) {
        a.emit(0x3A400000 | encodeCond(cond) | uint32(nzcv<<12) | encodeRm(rm) | encodeRn(rn))
}

// FCMPE - Floating-point compare with exception.
func (a *Assembler) FCMPE(rn, rm Reg) {
        a.emit(0x1E202010 | encodeRm(rm) | encodeRn(rn))
}

// FCMPEZero - Floating-point compare with zero.
func (a *Assembler) FCMPEZero(rn Reg) {
        a.emit(0x1E202008 | encodeRn(rn))
}

// ========== Branch ==========

// B - Unconditional branch.
func (a *Assembler) B(label string) {
        off := a.offset
        a.emit(0x14000000) // placeholder with imm26 = 0
        a.fixups[off] = label
}

// BL - Branch with link (function call).
func (a *Assembler) BL(label string) {
        off := a.offset
        a.emit(0x94000000) // placeholder with imm26 = 0
        a.fixups[off] = label
}

// BCond - Conditional branch.
func (a *Assembler) BCond(cond int, label string) {
        off := a.offset
        a.emit(0x54000000 | encodeCond(cond)) // placeholder with imm19 = 0
        a.fixups[off] = label
}

// BR - Branch to register.
func (a *Assembler) BR(rn Reg) {
        a.emit(0xD61F0000 | encodeRn(rn))
}

// BLR - Branch with link to register.
func (a *Assembler) BLR(rn Reg) {
        a.emit(0xD63F0000 | encodeRn(rn))
}

// CBZ - Compare and branch if zero.
func (a *Assembler) CBZ(rt Reg, label string) {
        off := a.offset
        a.emit(0xB4000000 | encodeRt(rt)) // placeholder
        a.fixups[off] = label
}

// CBNZ - Compare and branch if non-zero.
func (a *Assembler) CBNZ(rt Reg, label string) {
        off := a.offset
        a.emit(0xB5000000 | encodeRt(rt)) // placeholder
        a.fixups[off] = label
}

// TBZ - Test bit and branch if zero.
func (a *Assembler) TBZ(rt Reg, bit int, label string) {
        off := a.offset
        a.emit(0x36000000 | encodeBits(19, 23, 0, bit) | encodeRt(rt))
        a.fixups[off] = label
}

// TBNZ - Test bit and branch if non-zero.
func (a *Assembler) TBNZ(rt Reg, bit int, label string) {
        off := a.offset
        a.emit(0x37000000 | encodeBits(19, 23, 0, bit) | encodeRt(rt))
        a.fixups[off] = label
}

// RET - Return.
func (a *Assembler) RET() {
        a.RETR(R30) // LR
}

// RETR - Return to register.
func (a *Assembler) RETR(rn Reg) {
        a.emit(0xD65F0000 | encodeRn(rn))
}

// ========== Load/Store ==========

// LDR (64-bit) - Load register.
func (a *Assembler) LDR(rt, rn Reg, offset int) {
        a.emit(0xF9400000 | encodeOffset12(offset, 3) | encodeRn(rn) | encodeRt(rt))
}

// LDUR (64-bit) - Load register (unscaled).
func (a *Assembler) LDUR(rt, rn Reg, offset int) {
        a.emit(0xF8400000 | uint32(offset&0x1FF)<<12 | encodeRn(rn) | encodeRt(rt))
}

// STR (64-bit) - Store register.
func (a *Assembler) STR(rt, rn Reg, offset int) {
        a.emit(0xF9000000 | encodeOffset12(offset, 3) | encodeRn(rn) | encodeRt(rt))
}

// STUR (64-bit) - Store register (unscaled).
func (a *Assembler) STUR(rt, rn Reg, offset int) {
        a.emit(0xF8000000 | uint32(offset&0x1FF)<<12 | encodeRn(rn) | encodeRt(rt))
}

// LDP - Load pair.
func (a *Assembler) LDP(rt, rt2, rn Reg, offset int) {
        a.emit(0xA4400000 | encodeOffset7(offset, 3) | encodeRt2(rt2) | encodeRn(rn) | encodeRt(rt))
}

// STP - Store pair.
func (a *Assembler) STP(rt, rt2, rn Reg, offset int) {
        a.emit(0xA9000000 | encodeOffset7(offset, 3) | encodeRt2(rt2) | encodeRn(rn) | encodeRt(rt))
}

// LDRB - Load byte.
func (a *Assembler) LDRB(rt, rn Reg, offset int) {
        a.emit(0x39400000 | encodeOffset12(offset, 0) | encodeRn(rn) | encodeRt(rt))
}

// STRB - Store byte.
func (a *Assembler) STRB(rt, rn Reg, offset int) {
        a.emit(0x39000000 | encodeOffset12(offset, 0) | encodeRn(rn) | encodeRt(rt))
}

// LDRSW - Load register sign-extend word.
func (a *Assembler) LDRSW(rt, rn Reg, offset int) {
        a.emit(0xB9800000 | encodeOffset12(offset, 2) | encodeRn(rn) | encodeRt(rt))
}

// LDRW - Load 32-bit register.
func (a *Assembler) LDRW(rt, rn Reg, offset int) {
        a.emit(0xB9400000 | encodeOffset12(offset, 2) | encodeRn(rn) | encodeRt(rt))
}

// STRW - Store 32-bit register.
func (a *Assembler) STRW(rt, rn Reg, offset int) {
        a.emit(0xB9000000 | encodeOffset12(offset, 2) | encodeRn(rn) | encodeRt(rt))
}

// LDRSB - Load byte sign-extend.
func (a *Assembler) LDRSB(rt, rn Reg, offset int) {
        a.emit(0x39C00000 | encodeOffset12(offset, 0) | encodeRn(rn) | encodeRt(rt))
}

// ========== Floating-Point Instructions ==========

// FMOV (general) - Move float register.
func (a *Assembler) FMOV(rd, rn Reg) {
        a.emit(0x1E604000 | encodeRm(rn) | encodeRn(rd))
}

// FMOVGPR - Move from GPR to FP.
func (a *Assembler) FMOVGPR(fd Reg, rn Reg) {
        a.emit(0x9E670000 | encodeRn(rn) | encodeRd(fd))
}

// FMOVToGPR - Move from FP to GPR.
func (a *Assembler) FMOVToGPR(rd Reg, fn Reg) {
        a.emit(0x9E660000 | encodeRn(fn) | encodeRd(rd))
}

// FMOVImm - Move immediate to FP register.
func (a *Assembler) FMOVImm(fd Reg, imm float64) {
        bits := math.Float64bits(imm)
        a.emit(0x1E601000 | uint32(bits>>5) | encodeRd(fd))
}

// FADD - Floating-point add.
func (a *Assembler) FADD(rd, rn, rm Reg) {
        a.emit(0x1E608000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// FSUB - Floating-point subtract.
func (a *Assembler) FSUB(rd, rn, rm Reg) {
        a.emit(0x1E608400 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// FMUL - Floating-point multiply.
func (a *Assembler) FMUL(rd, rn, rm Reg) {
        a.emit(0x1E600800 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// FDIV - Floating-point divide.
func (a *Assembler) FDIV(rd, rn, rm Reg) {
        a.emit(0x1E601000 | encodeRm(rm) | encodeRn(rn) | encodeRd(rd))
}

// FSQRT - Floating-point square root.
func (a *Assembler) FSQRT(rd, rn Reg) {
        a.emit(0x1E60C000 | encodeRn(rn) | encodeRd(rd))
}

// FNEG - Floating-point negate.
func (a *Assembler) FNEG(rd, rn Reg) {
        a.emit(0x1E614000 | encodeRn(rn) | encodeRd(rd))
}

// FABS - Floating-point absolute value.
func (a *Assembler) FABS(rd, rn Reg) {
        a.emit(0x1E60C000 | encodeRn(rn) | encodeRd(rd))
}

// FCVT - Float convert (64<->64 in this case, identity, but needed for interface).
func (a *Assembler) FCVT(rd, rn Reg) {
        a.emit(0x1E224000 | encodeRn(rn) | encodeRd(rd))
}

// FCVTZS - Float convert to signed integer, round toward zero.
func (a *Assembler) FCVTZS(rd, rn Reg) {
        a.emit(0x9E780000 | encodeRn(rn) | encodeRd(rd))
}

// SCVTF - Signed integer convert to float.
func (a *Assembler) SCVTF(fd, rn Reg) {
        a.emit(0x9E620000 | encodeRn(rn) | encodeRd(fd))
}

// FLD (LDR D) - Load double.
func (a *Assembler) FLD(rt, rn Reg, offset int) {
        a.emit(0xFD400000 | encodeOffset12(offset, 3) | encodeRn(rn) | encodeRt(rt))
}

// FST (STR D) - Store double.
func (a *Assembler) FST(rt, rn Reg, offset int) {
        a.emit(0xFD000000 | encodeOffset12(offset, 3) | encodeRn(rn) | encodeRt(rt))
}

// FLDUR - Load double (unscaled).
func (a *Assembler) FLDUR(rt, rn Reg, offset int) {
        a.emit(0xFC400000 | uint32(offset&0x1FF)<<12 | encodeRn(rn) | encodeRt(rt))
}

// FSTUR - Store double (unscaled).
func (a *Assembler) FSTUR(rt, rn Reg, offset int) {
        a.emit(0xFC000000 | uint32(offset&0x1FF)<<12 | encodeRn(rn) | encodeRt(rt))
}

// ========== Special ==========

// NOP - No operation.
func (a *Assembler) NOP() {
        a.emit(0xD503201F)
}

// YIELD - Hint yield.
func (a *Assembler) YIELD() {
        a.emit(0xD503203F)
}

// DMB - Data memory barrier.
func (a *Assembler) DMB() {
        a.emit(0xD5033BBF)
}

// ISB - Instruction synchronization barrier.
func (a *Assembler) ISB() {
        a.emit(0xD5033FDF)
}

// SVC - Supervisor call.
func (a *Assembler) SVC(imm int) {
        a.emit(0xD4000001 | uint32(imm&0xFFFF)<<5)
}

// HLT - Halt.
func (a *Assembler) HLT(imm int) {
        a.emit(0xD4400000 | uint32(imm&0xFFFF)<<5)
}

// BRK - Breakpoint.
func (a *Assembler) BRK(imm int) {
        a.emit(0xD4200000 | uint32(imm&0xFFFF)<<5)
}

// ========== Encoding Helpers ==========

func encodeImm12(imm int) uint32 {
        u := uint32(imm) & 0xFFF
        shift := 0
        if imm > 0xFFF {
                if imm%0x1000 == 0 && imm <= 0xFFF000 {
                        shift = 1
                        u = uint32(imm>>12) & 0xFFF
                }
        }
        return (u << 10) | uint32(shift<<22)
}

func encodeOffset12(offset int, size int) uint32 {
        scale := 1 << size
        aligned := offset &^ (scale - 1)
        imm12 := uint32((aligned >> size) & 0xFFF)
        return imm12 << 10
}

func encodeOffset7(offset int, size int) uint32 {
        scale := 1 << size
        aligned := offset &^ (scale - 1)
        imm7 := uint32((aligned >> size) & 0x7F)
        return imm7 << 15
}

func encodeLogicImm(imm uint64) uint32 {
        // Simplified: encode small immediates
        // For a full implementation, use the ARM reference manual's logic
        if imm == 0 {
                return 0x1F << 10 // all ones in immr/immn
        }
        // Simple case: replicate pattern
        for n := 6; n >= 0; n-- {
                if imm > 0 && (imm>>(uint(n+3))) == 0 {
                        // Fits in this element size
                        elems := uint64(1<<uint(n+3)) - 1
                        if imm&^elems == imm {
                                return uint32(imm&0x3F)<<16 | uint32(n<<10)
                        }
                }
        }
        // Fallback: encode as NOR of immediate
        return encodeLogicImm(^imm)
}

// ========== Register Allocator ==========

// RegAlloc is a simple linear scan register allocator.
type RegAlloc struct {
        used     [64]bool // which registers are in use
        spillOff int      // next spill slot offset
        spilled  []Reg    // registers that have been spilled
}

func NewRegAlloc() *RegAlloc {
        return &RegAlloc{}
}

// Alloc allocates a GPR register, preferring caller-saved.
func (ra *RegAlloc) Alloc() Reg {
        // Try caller-saved first (X9-X15)
        for _, r := range TempRegs {
                if !ra.used[r] {
                        ra.used[r] = true
                        return r
                }
        }
        // Then callee-saved
        for _, r := range CalleeSavedRegs {
                if !ra.used[r] {
                        ra.used[r] = true
                        return r
                }
        }
        // All used - spill
        return RZR // caller must handle spilling
}

// AllocFP allocates a floating-point register.
func (ra *RegAlloc) AllocFP() Reg {
        for _, r := range FPArgRegs {
                if !ra.used[r] {
                        ra.used[r] = true
                        return r
                }
        }
        for i := F16; i <= F31; i++ {
                if !ra.used[i] {
                        ra.used[i] = true
                        return i
                }
        }
        return F31 // fallback
}

// Free releases a register.
func (ra *RegAlloc) Free(r Reg) {
        if r >= 0 && r < 64 {
                ra.used[r] = false
        }
}

// MarkUsed marks a register as in use (for function parameters, etc.).
func (ra *RegAlloc) MarkUsed(r Reg) {
        if r >= 0 && r < 64 {
                ra.used[r] = true
        }
}

// SpillOffset returns the next stack spill offset and advances.
func (ra *RegAlloc) SpillOffset() int {
        off := ra.spillOff
        ra.spillOff += 8
        return off
}

// SaveCalleeSaved returns the list of callee-saved registers in use.
func (ra *RegAlloc) SaveCalleeSaved() []Reg {
        var saved []Reg
        for _, r := range CalleeSavedRegs {
                if ra.used[r] {
                        saved = append(saved, r)
                }
        }
        return saved
}

func (ra *RegAlloc) SaveCalleeSavedFP() []Reg {
        var saved []Reg
        for _, r := range CalleeSavedFPRegs {
                if ra.used[r] {
                        saved = append(saved, r)
                }
        }
        return saved
}

// CalleeSavedSize returns total bytes needed to save callee-saved registers.
func (ra *RegAlloc) CalleeSavedSize() int {
        return len(ra.SaveCalleeSaved())*8 + len(ra.SaveCalleeSavedFP())*8
}
