package runtime

// ---------------------------------------------------------------------------
// Pure-Go Runtime Code Generator
//
// Generates x86_64 machine code for Yilt runtime functions using the project's
// own assembler (internal/codegen/x86_64). This eliminates the dependency on
// a C compiler (gcc) for building the Yilt compiler.
//
// Architecture:
//   - Each runtime function is generated independently as a code blob
//   - String literals are accumulated in a shared rodata section
//   - Relocations (R_X86_64_PC32, R_X86_64_32, R_X86_64_32S, R_X86_64_PLT32)
//     are emitted alongside the code and processed by the linker
//   - The generated code matches what GCC would produce for the same C source
//
// When the pure-Go runtime covers all functions, gen.go's embedded C-compiled
// binary can be removed entirely.
// ---------------------------------------------------------------------------

import (
        "github.com/yilt/yiltc/internal/codegen/x86_64"
)

// Linux x86_64 syscall numbers
const (
        sysRead  = 0
        sysWrite = 1
        sysMmap  = 9
        sysExit  = 60
)

// mmap flags
const (
        mmapRW       = 0x03 // PROT_READ | PROT_WRITE
        mmapPrivate  = 0x02
        mmapAnon     = 0x20
        mmapAnonPriv = mmapPrivate | mmapAnon
)

// Tag constants (must match C runtime and values.go)
const (
        rtTagShift    = 56
        rtTagInt      = 1
        rtTagBool     = 2
        rtTagFp       = 3
        rtTagStr      = 4
        rtTagTable    = 5
        rtTagNil      = 9
        rtTagErr      = 14

        rtValueMask    = 0x00FFFFFFFFFFFFFF
        rtStrHeaderSize = 24
        rtTrueVal      = uint64(2)<<56 | 1 // MK_TAG(TAG_BOOL, 1)
)

// ---------------------------------------------------------------------------
// puregenReloc describes a relocation for the pure-Go generated code.
// It follows the same format as RuntimeReloc in gen.go.
// ---------------------------------------------------------------------------
type puregenReloc struct {
        Offset uint64
        Type   PuregenRelocType
        Target string // section name or symbol name
        Addend uint64
        Symbol string // for cross-function calls
}

type PuregenRelocType int

const (
        PuregenRelocAbs32  PuregenRelocType = iota // absolute unsigned 32-bit
        PuregenRelocAbs32S                          // absolute signed 32-bit
        PuregenRelocPC32                            // PC-relative 32-bit
        PuregenRelocPLT32                           // PC-relative with -4 addend
)

// ---------------------------------------------------------------------------
// puregenFunc holds the generated code and relocations for one function.
// ---------------------------------------------------------------------------
type puregenFunc struct {
        Name        string
        Code        []byte
        Relocations []puregenReloc
}

// ---------------------------------------------------------------------------
// rodataBuilder accumulates string literal data and tracks offsets.
// ---------------------------------------------------------------------------
type rodataBuilder struct {
        data    []byte
        offsets map[string]uint64 // string → offset within data
}

func newRodataBuilder() *rodataBuilder {
        return &rodataBuilder{
                offsets: make(map[string]uint64),
        }
}

// add appends a string literal and returns its offset.
func (rb *rodataBuilder) add(s string) uint64 {
        if off, ok := rb.offsets[s]; ok {
                return off
        }
        off := uint64(len(rb.data))
        rb.data = append(rb.data, s...)
        rb.offsets[s] = off
        return off
}

// offset returns the offset of a previously added string, or -1.
func (rb *rodataBuilder) offset(s string) uint64 {
        if off, ok := rb.offsets[s]; ok {
                return off
        }
        return 0xFFFFFFFFFFFFFFFF
}

// ---------------------------------------------------------------------------
// rtFuncBuilder generates one runtime function.
// ---------------------------------------------------------------------------
type rtFuncBuilder struct {
        a      *x86_64.Asm
        name   string
        relocs []puregenReloc
        rodata *rodataBuilder // shared rodata for string references
}

func newRtFuncBuilder(name string, rd *rodataBuilder) *rtFuncBuilder {
        return &rtFuncBuilder{
                a:      x86_64.NewAsm(),
                name:   name,
                rodata: rd,
        }
}

// finalize resolves internal labels and returns the function descriptor.
func (fb *rtFuncBuilder) finalize() puregenFunc {
        for l := range fb.a.Labels() {
                fb.a.ResolveFixups(l)
        }
        return puregenFunc{
                Name:        fb.name,
                Code:        fb.a.Bytes(),
                Relocations: fb.relocs,
        }
}

// --- Relocation helpers ---

// addRelocPC32 adds a PC-relative 32-bit relocation for a string in rodata.
// The displacement will be: target_address - (patch_offset + 4) + addend.
func (fb *rtFuncBuilder) addRelocRodataStr(str string) {
        off := fb.rodata.offset(str)
        // The displacement is at the end of the instruction (last 4 bytes).
        // For LEA RXX, [RIP+disp]: REX(1) + opcode(1) + ModRM(1) [+SIB(1)] + disp(4).
        // We don't know the exact instruction size, but we know the displacement
        // was just emitted. Back up 4 bytes from the current position.
        dispOffset := uint64(fb.a.Offset() - 4)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: dispOffset,
                Type:   PuregenRelocPC32,
                Target: ".yilt.rt.rodata.str",
                Addend: off,
        })
}

// addRelocText adds a PLT32 relocation for a call to another runtime function.
// The offset should point to the 4-byte displacement in the CALL instruction.
func (fb *rtFuncBuilder) addRelocText(symbol string) {
        // CALL rel32: opcode E8 + 4-byte displacement
        // The displacement was just emitted, back up 4 bytes.
        dispOffset := uint64(fb.a.Offset() - 4)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: dispOffset,
                Type:   PuregenRelocPLT32,
                Target: symbol,
                Symbol: symbol,
        })
}

// --- Instruction helpers ---

// emitLEA_RodataStr emits LEA reg, [RIP + disp] where disp references a
// string in the rodata section. The displacement is resolved by the linker
// via a PC32 relocation.
func (fb *rtFuncBuilder) emitLEA_RodataStr(dst x86_64.Reg, str string) {
        // LEA dst, [RIP+0] — standard RIP-relative LEA.
        // We emit the full instruction then record a relocation at the displacement.
        fb.a.LEA(dst, x86_64.MemDispl(0))
        fb.addRelocRodataStr(str)
}

// --- Syscall emission ---

// syscall1: syscall(nr, a0) → RAX
func (fb *rtFuncBuilder) syscall1(nr int64, a0Reg x86_64.Reg) {
        fb.a.MovRM(x86_64.RAX, nr)
        // a0 is already in the correct register if it's RDI
        if a0Reg.Code != x86_64.RDI.Code {
                fb.a.MovRR(x86_64.RDI, a0Reg)
        }
        fb.a.SYSCALL()
}

// syscall3: syscall(nr, a0, a1, a2) → RAX
func (fb *rtFuncBuilder) syscall3(nr int64, a0, a1, a2 x86_64.Reg) {
        fb.a.MovRM(x86_64.RAX, nr)
        if a0.Code != x86_64.RDI.Code {
                fb.a.MovRR(x86_64.RDI, a0)
        }
        if a1.Code != x86_64.RSI.Code {
                fb.a.MovRR(x86_64.RSI, a1)
        }
        if a2.Code != x86_64.RDX.Code {
                fb.a.MovRR(x86_64.RDX, a2)
        }
        fb.a.SYSCALL()
}

// syscall6: syscall(nr, a0..a5) → RAX
func (fb *rtFuncBuilder) syscall6(nr int64, a0, a1, a2, a3, a4, a5 x86_64.Reg) {
        fb.a.MovRM(x86_64.RAX, nr)
        if a0.Code != x86_64.RDI.Code {
                fb.a.MovRR(x86_64.RDI, a0)
        }
        if a1.Code != x86_64.RSI.Code {
                fb.a.MovRR(x86_64.RSI, a1)
        }
        if a2.Code != x86_64.RDX.Code {
                fb.a.MovRR(x86_64.RDX, a2)
        }
        if a3.Code != x86_64.R10.Code {
                fb.a.MovRR(x86_64.R10, a3)
        }
        if a4.Code != x86_64.R8.Code {
                fb.a.MovRR(x86_64.R8, a4)
        }
        if a5.Code != x86_64.R9.Code {
                fb.a.MovRR(x86_64.R9, a5)
        }
        fb.a.SYSCALL()
}

// --- Tagged value helpers ---

// getInt sign-extends the 56-bit payload: dst = (src << 8) >> 8
func (fb *rtFuncBuilder) getInt(dst, src x86_64.Reg) {
        if dst.Code != src.Code {
                fb.a.MovRR(dst, src)
        }
        fb.a.ShlRI(dst, 8)
        fb.a.SarRI(dst, 8)
}

// getPtr extracts pointer: dst = src with tag bits cleared
// Uses SHL 8 + SHR 8 instead of AND mask because the mask 0x00FFFFFFFFFFFFFF
// cannot be represented as a sign-extended 32-bit immediate.
func (fb *rtFuncBuilder) getPtr(dst, src x86_64.Reg) {
        if dst.Code != src.Code {
                fb.a.MovRR(dst, src)
        }
        fb.a.ShlRI(dst, 8)
        fb.a.ShrRI(dst, 8)
}

// mkTag creates a tagged value: dst = (tag << 56) | (val & VALUE_MASK)
// Uses SHL 8 + SHR 8 to clear tag bits before OR-ing in the new tag.
//
// Bug fix: previously this function hardcoded R11 as the temp register
// for the tag value.  But callers like genPure_StrConcat pass R11 as
// both `val` and `dst`, which caused the MovRM64(R11, tag) to overwrite
// the just-cleared pointer before the OR — producing a tagged value
// with the right tag but a ZERO pointer, which downstream code saw as
// nil.  Now we pick a temp that doesn't alias dst.
func (fb *rtFuncBuilder) mkTag(tag uint64, val x86_64.Reg, dst x86_64.Reg) {
        if dst.Code != val.Code {
                fb.a.MovRR(dst, val)
        }
        fb.a.ShlRI(dst, 8)
        fb.a.ShrRI(dst, 8)
        // OR the tag into the top byte (bits 56-63). The tag value is small
        // (0-9), so tag<<56 exceeds 32-bit immediate range. Use a 64-bit
        // mov+shift through a temp register.  Pick R11 by default, but if
        // dst IS R11, use R10 instead (also caller-saved, also rarely used
        // in runtime).
        temp := x86_64.R11
        if dst.Code == x86_64.R11.Code {
                temp = x86_64.R10
        }
        fb.a.MovRM64(temp, int64(tag))
        fb.a.ShlRI(temp, 56)
        fb.a.OrRR(dst, temp)
}

// ---------------------------------------------------------------------------
// Runtime function generators
// ---------------------------------------------------------------------------

// genPure_Print: y_print(val: tagged) → void
//
// Prints a tagged value to stdout (no newline). Dispatches on tag:
//   TAG_INT (1): decimal representation via local int_to_str
//   TAG_BOOL (2): "true" or "false"
//   TAG_STR (4): raw string data from StrHeader
//   TAG_NIL/0/9: "nil"
//   TAG_TABLE (5): "[table]"
//   TAG_FP (3): stub (prints "[float]")
//   others: nothing
func genPure_Print(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_print", rd)

        // Stack layout (grows downward):
        //   [RSP+0..255]  int_to_str buffer (256 bytes, written backwards from end)

        a := fb.a

        // Prologue: save R12 (callee-saved), allocate stack
        a.PUSH(x86_64.R12)
        a.SubRI(x86_64.RSP, 256) // int_to_str buffer

        // R12 points to the end of the buffer (used by int_to_str)
        a.MovRR(x86_64.R12, x86_64.RSP)
        a.AddRI(x86_64.R12, 256)

        // Get tag from val (RDI)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)

        // Jump table using sequential comparisons
        // TAG_INT = 1
        notInt := a.GenLabel("print_not_int")
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JNE(notInt)

        // --- TAG_INT: print integer ---
        // Sign-extend 56-bit payload into RCX
        fb.getInt(x86_64.RCX, x86_64.RDI)

        // Labels for int printing
        zeroLabel := a.GenLabel("print_int_zero")
        writeIntLabel := a.GenLabel("print_int_write")
        negLabel := a.GenLabel("print_int_neg")
        negDoneLabel := a.GenLabel("print_int_neg_done")
        posLoop := a.GenLabel("print_int_pos_loop")

        // Check if zero
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(zeroLabel)

        // Check negative
        a.Jcc(x86_64.CondS, negLabel)

        // Positive path: set flag register R11 = 0, fall through to pos_loop
        a.MovZeroR64(x86_64.R11)
        a.JMP(posLoop)

        // Negative path: negate and set flag R11 = 1
        a.Label(negLabel)
        a.MovRM(x86_64.R11, 1) // negative flag
        a.NegR(x86_64.RCX)     // RCX = |value|

        // Shared positive digit loop
        a.Label(posLoop)

        // Divide RCX by 10: MOV RAX, RCX; CQO; MOV RBX, 10; IDIV RBX
        a.MovRR(x86_64.RAX, x86_64.RCX) // RAX = dividend
        a.CQO()                          // sign-extend RAX into RDX:RAX
        a.MovRM(x86_64.RBX, 10)         // RBX = 10
        a.IDivRR(x86_64.RBX)            // RAX = quotient, RDX = remainder (0-9)

        // Prepend digit: *--p = '0' + remainder
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRR(x86_64.R8, x86_64.RDX) // R8 = remainder
        a.OrRI(x86_64.R8, 0x30)        // R8 = '0' + remainder
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        // Continue if quotient != 0
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = quotient for next iteration
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(posLoop)

        // After digit loop: check negative flag to prepend '-'
        a.TestRR(x86_64.R11, x86_64.R11)
        a.JZ(negDoneLabel)

        // Prepend '-'
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64(0x2D)) // R8 = '-'
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        a.Label(negDoneLabel)
        a.JMP(writeIntLabel)

        // Zero case
        a.Label(zeroLabel)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64(0x30)) // '0'
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        // Calculate string length and write
        a.Label(writeIntLabel)
        // length = (buffer_end) - (current_pos) = (RSP + 256) - R12
        a.MovRR(x86_64.RDX, x86_64.RSP)
        a.AddRI(x86_64.RDX, 256)             // RDX = buffer end
        a.SubRR(x86_64.RDX, x86_64.R12)      // RDX = length

        // sys_write(1, R12, RDX)
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)               // stdout
        a.MovRR(x86_64.RSI, x86_64.R12)      // RSI = string start
        a.SYSCALL()

        doneLabel := a.GenLabel("print_done")
        a.JMP(doneLabel)

        // --- TAG_BOOL (2) ---
        notBool := a.GenLabel("print_not_bool")
        a.Label(notInt)
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JNE(notBool)

        // Check payload
        falseLabel := a.GenLabel("print_false")
        a.ShlRI(x86_64.RDI, 8); a.ShrRI(x86_64.RDI, 8) // clear tag
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(falseLabel)

        // true
        trueStrOff := rd.add("true")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "true")
        a.MovRM(x86_64.RDX, 4)
        a.SYSCALL()
        a.JMP(doneLabel)

        // false
        a.Label(falseLabel)
        falseStrOff := rd.add("false")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "false")
        a.MovRM(x86_64.RDX, 5)
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_STR (4) ---
        notStr := a.GenLabel("print_not_str")
        a.Label(notBool)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // Extract pointer from tagged value
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShlRI(x86_64.RAX, 8); a.ShrRI(x86_64.RAX, 8) // RAX = StrHeader* (clear tag)
        // Length at offset +8, data at offset +24
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RAX, 8)) // len
        a.MovRM(x86_64.RSI, rtStrHeaderSize)                   // add base offset for data ptr
        a.AddRR(x86_64.RSI, x86_64.RAX)                        // RSI = data ptr

        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_NIL / 0 ---
        notNil := a.GenLabel("print_not_nil")
        a.Label(notStr)
        nillLabel := a.GenLabel("print_nil")
        // TAG_NIL = 9, but 0 is also nil. Check both.
        a.CmpRI(x86_64.RAX, rtTagNil)
        a.JE(nillLabel)
        a.TestRR(x86_64.RAX, x86_64.RAX) // tag 0 = nil
        a.JNZ(notNil)

        a.Label(nillLabel)
        nilStrOff := rd.add("nil")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "nil")
        a.MovRM(x86_64.RDX, 3)
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_TABLE (5) ---
        notTable := a.GenLabel("print_not_table")
        a.Label(notNil)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        tableStrOff := rd.add("[table]")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "[table]")
        a.MovRM(x86_64.RDX, 7)
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_FP (3): print float value ---
        notFp := a.GenLabel("print_not_fp")
        a.Label(notTable)
        a.CmpRI(x86_64.RAX, rtTagFp)
        a.JNE(notFp)

        // Print float: uses fixed-point conversion with 6 decimal places.
        // Stack: [RSP+0..7] = temp double, [RSP+8..263] = int_to_str buf
        // R12 = end of int_to_str buffer

        // Save callee-saved regs we'll use
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // Extract float pointer
        fb.getPtr(x86_64.R13, x86_64.RDI) // R13 = float ptr
        a.TestRR(x86_64.R13, x86_64.R13)
        jzFpNil := a.GenLabel("fp_nil")
        a.JZ(jzFpNil)

        // Load float value into XMM0
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.R13, 0)))

        // Check for NaN: XMM0 != XMM0
        a.UCOMISD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM0))
        a.Jcc(x86_64.CondP, notFp) // PF=1 means unordered (NaN)

        // Check for +/-Inf: compare with 1e308 (0x7FEFFFFFFFFFFFFF)
        // Store XMM0 to stack, load as GPR, check exponent
        a.SubRI(x86_64.RSP, 8) // temp space
        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)), x86_64.XMM0)
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.RSP, 0)) // R14 = float bits
        a.ShrRI(x86_64.R14, 52)                              // R14 = exponent (11 bits) + sign
        a.AndRI(x86_64.R14, 0x7FF)                            // R14 = exponent only
        a.CmpRI(x86_64.R14, 0x7FF)
        fpSpecial := a.GenLabel("fp_special")
        a.JE(fpSpecial)

        // --- Normal float path ---
        // Check sign bit
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.RSP, 0))
        a.ShrRI(x86_64.R14, 63) // R14 = sign (0 or 1)
        isNeg := a.GenLabel("fp_neg")
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JNZ(isNeg)

        // Positive float
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)))
        a.JMP("fp_do_convert")

        a.Label(isNeg)
        // Negative float: print '-' first, then work with absolute value
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "-")
        a.MovRM(x86_64.RDX, 1)
        a.SYSCALL()
        // Get absolute value: clear sign bit
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.RSP, 0))
        a.MovRM64(x86_64.R15, int64(0x7FFFFFFFFFFFFFFF))
        a.AndRR(x86_64.R14, x86_64.R15)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.R14)
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)))

        a.Label("fp_do_convert")
        // XMM0 = |float value|
        // Convert integer part via truncation
        a.CVTTSD2SI64(x86_64.R14, x86_64.XMM0)  // R14 = trunc(x)
        a.CVTSI2SD64(x86_64.XMM1, x86_64.RegOp(x86_64.R14))   // XMM1 = (double)trunc(x)

        // Convert integer part to string in the int_to_str buffer.
        // R12 = buffer end (RSP + 256). We'll prepend digits backwards.
        // Use R15 as temporary, R13 already has float ptr.
        // Save R12 in RBX since we need it later for sys_write.
        a.MovRR(x86_64.RBX, x86_64.R12) // RBX = buffer end

        a.TestRR(x86_64.R14, x86_64.R14)
        fpIntZero := a.GenLabel("fp_int_zero")
        a.JZ(fpIntZero)

        // Check negative (shouldn't happen since we took abs value, but be safe)
        a.Jcc(x86_64.CondS, "fp_int_neg")

        // Positive digit loop
        fpPosLoop := a.GenLabel("fp_pos_loop")
        a.Label(fpPosLoop)
        a.MovRR(x86_64.RAX, x86_64.R14)
        a.MovZeroR64(x86_64.RDX)
        a.MovRM(x86_64.RCX, 10)
        a.DivRR(x86_64.RCX) // RAX=quotient, RDX=remainder
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64('0'))
        a.AddRR(x86_64.RDX, x86_64.R8)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.RDX)
        a.MovRR(x86_64.R14, x86_64.RAX)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(fpPosLoop)
        a.JMP("fp_int_write")

        a.Label(fpIntZero)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64('0'))
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        a.Label("fp_int_write")
        // Write integer part: sys_write(1, R12, RBX-R12)
        a.MovRR(x86_64.RDX, x86_64.RBX)
        a.SubRR(x86_64.RDX, x86_64.R12) // length
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        a.MovRR(x86_64.RSI, x86_64.R12)
        a.SYSCALL()

        // Check if there's a fractional part
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)))
        a.UCOMISD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1))
        // If XMM0 == XMM1, no fractional part → done
        noFrac := a.GenLabel("fp_no_frac")
        a.Jcc(x86_64.CondE, noFrac)

        // Has fractional part: compute 6 decimal digits
        // frac = (x - trunc(x)) * 1000000
        a.SubSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1))  // XMM0 = frac
        // Multiply by 1000000 (0x412E848000000000)
        a.MovRM64(x86_64.R14, int64(0x412E848000000000))
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.R14)
        a.MovSD_RM(x86_64.XMM2, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)))
        a.MulSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM2))
        // Round to nearest integer: add 0.5 and truncate
        a.MovRM64(x86_64.R14, int64(0x3FE0000000000000))
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.R14)
        a.MovSD_RM(x86_64.XMM2, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)))
        a.AddSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM2))
        a.CVTTSD2SI64(x86_64.R14, x86_64.XMM0) // R14 = frac * 1000000 (rounded)

        // Print decimal point
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, ".")
        a.MovRM(x86_64.RDX, 1)
        a.SYSCALL()

        // Convert fractional part to string (always 6 digits, zero-padded)
        // Build 6-digit string on stack: compute each digit
        // Digits: 100000, 10000, 1000, 100, 10, 1
        a.SubRI(x86_64.RSP, 8) // 8 bytes for digit buffer
        // R14 = frac_digits (0-999999)
        a.MovRR(x86_64.RAX, x86_64.R14)

        // Digit 1: /100000
        a.MovRM(x86_64.RCX, 100000)
        a.XorRR(x86_64.RDX, x86_64.RDX)
        a.DivRR(x86_64.RCX) // RAX=quotient, RDX=remainder
        a.MovRM(x86_64.R8, int64('0'))
        a.AddRR(x86_64.RAX, x86_64.R8)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.RAX)

        // Digit 2: remainder / 10000
        a.MovRR(x86_64.RAX, x86_64.RDX)
        a.MovRM(x86_64.RCX, 10000)
        a.XorRR(x86_64.RDX, x86_64.RDX)
        a.DivRR(x86_64.RCX)
        a.AddRR(x86_64.RAX, x86_64.R8)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 1), x86_64.RAX)

        // Digit 3: /1000
        a.MovRR(x86_64.RAX, x86_64.RDX)
        a.MovRM(x86_64.RCX, 1000)
        a.XorRR(x86_64.RDX, x86_64.RDX)
        a.DivRR(x86_64.RCX)
        a.AddRR(x86_64.RAX, x86_64.R8)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 2), x86_64.RAX)

        // Digit 4: /100
        a.MovRR(x86_64.RAX, x86_64.RDX)
        a.MovRM(x86_64.RCX, 100)
        a.XorRR(x86_64.RDX, x86_64.RDX)
        a.DivRR(x86_64.RCX)
        a.AddRR(x86_64.RAX, x86_64.R8)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 3), x86_64.RAX)

        // Digit 5: /10
        a.MovRR(x86_64.RAX, x86_64.RDX)
        a.MovRM(x86_64.RCX, 10)
        a.XorRR(x86_64.RDX, x86_64.RDX)
        a.DivRR(x86_64.RCX)
        a.AddRR(x86_64.RAX, x86_64.R8)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 4), x86_64.RAX)

        // Digit 6: remainder
        a.AddRR(x86_64.RDX, x86_64.R8)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 5), x86_64.RDX)

        // Strip trailing zeros: find last non-zero digit
        a.MovRM(x86_64.R14, 5) // start from digit 6 (offset 5)
        stripLoop := a.GenLabel("fp_strip")
        a.Label(stripLoop)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0)) // use RSP+0..5
        // Check byte at RSP + R14
        a.MovRR(x86_64.RCX, x86_64.RSP)
        a.AddRR(x86_64.RCX, x86_64.R14)
        a.MovZX8R(x86_64.RAX, x86_64.RCX)
        a.CmpRI(x86_64.RAX, int64('0'))
        stripDone := a.GenLabel("fp_strip_done")
        a.JNE(stripDone)
        a.SubRI(x86_64.R14, 1)
        a.CmpRI(x86_64.R14, 0)
        a.JGE(stripLoop)

        a.Label(stripDone)
        a.AddRI(x86_64.R14, 1) // length = last non-zero offset + 1

        // Write decimal digits
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        a.MovRR(x86_64.RSI, x86_64.RSP)
        a.MovRR(x86_64.RDX, x86_64.R14)
        a.SYSCALL()

        a.AddRI(x86_64.RSP, 8) // restore stack

        a.Label(noFrac)
        a.AddRI(x86_64.RSP, 8) // restore temp space
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.JMP(doneLabel)

        // --- NaN / Inf handling ---
        a.Label(fpSpecial)
        // Check sign: R14 still has exponent, reload bits
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.RSP, 0))
        a.ShrRI(x86_64.R14, 63)
        // Exponent is 0x7FF: check mantissa for NaN vs Inf
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.RSP, 0))
        a.MovRM64(x86_64.RCX, int64(0x000FFFFFFFFFFFFF))
        a.AndRR(x86_64.R15, x86_64.RCX) // R15 = mantissa
        a.TestRR(x86_64.R15, x86_64.R15)
        isInf := a.GenLabel("fp_is_inf")
        a.JZ(isInf) // mantissa = 0 → Inf

        // NaN
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "nan")
        a.MovRM(x86_64.RDX, 3)
        a.SYSCALL()
        a.AddRI(x86_64.RSP, 8)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.JMP(doneLabel)

        a.Label(isInf)
        // Check sign for -inf vs +inf
        a.TestRR(x86_64.R14, x86_64.R14)
        isPosInf := a.GenLabel("fp_pos_inf")
        a.JZ(isPosInf)
        // -inf
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "-inf")
        a.MovRM(x86_64.RDX, 4)
        a.SYSCALL()
        a.AddRI(x86_64.RSP, 8)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.JMP(doneLabel)
        a.Label(isPosInf)
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "inf")
        a.MovRM(x86_64.RDX, 3)
        a.SYSCALL()
        a.AddRI(x86_64.RSP, 8)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.JMP(doneLabel)

        // --- Null float pointer ---
        a.Label(jzFpNil)
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 1)
        fb.emitLEA_RodataStr(x86_64.RSI, "nil")
        a.MovRM(x86_64.RDX, 3)
        a.SYSCALL()
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.JMP(doneLabel)

        // --- default: print nothing ---
        a.Label(notFp)
        a.Label(doneLabel)

        // Epilogue: restore stack and callee-saved regs
        a.AddRI(x86_64.RSP, 256)
        a.POP(x86_64.R12)
        a.RET()

        _ = trueStrOff
        _ = falseStrOff
        _ = nilStrOff
        _ = tableStrOff

        return fb.finalize()
}

// genPure_Eprint: y_eprint(val: tagged) → void
//
// Prints a tagged value to stderr (fd=2), no newline.
// Dispatches on tag identically to y_print, but uses fd=2 for syscalls.
func genPure_Eprint(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_eprint", rd)

        // Stack layout (grows downward):
        //   [RSP+0..255]  int_to_str buffer (256 bytes, written backwards from end)

        a := fb.a

        // Prologue: save R12 (callee-saved), allocate stack
        a.PUSH(x86_64.R12)
        a.SubRI(x86_64.RSP, 256) // int_to_str buffer

        // R12 points to the end of the buffer (used by int_to_str)
        a.MovRR(x86_64.R12, x86_64.RSP)
        a.AddRI(x86_64.R12, 256)

        // Get tag from val (RDI)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)

        // Jump table using sequential comparisons
        // TAG_INT = 1
        notInt := a.GenLabel("eprint_not_int")
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JNE(notInt)

        // --- TAG_INT: print integer ---
        fb.getInt(x86_64.RCX, x86_64.RDI)

        // Labels for int printing
        zeroLabel := a.GenLabel("eprint_int_zero")
        writeIntLabel := a.GenLabel("eprint_int_write")
        negLabel := a.GenLabel("eprint_int_neg")
        posLoop := a.GenLabel("eprint_int_pos_loop")

        // Check if zero
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(zeroLabel)

        // Check negative
        a.Jcc(x86_64.CondS, negLabel)

        // Positive path: set flag register R11 = 0, fall through to pos_loop
        a.MovZeroR64(x86_64.R11)
        a.JMP(posLoop)

        // Negative path: negate and set flag R11 = 1
        a.Label(negLabel)
        a.MovRM(x86_64.R11, 1) // negative flag
        a.NegR(x86_64.RCX)     // RCX = |value|

        // Shared positive digit loop
        a.Label(posLoop)

        // Divide RCX by 10
        a.MovRR(x86_64.RAX, x86_64.RCX)
        a.CQO()
        a.MovRM(x86_64.RBX, 10)
        a.IDivRR(x86_64.RBX)

        // Prepend digit: *--p = '0' + remainder
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRR(x86_64.R8, x86_64.RDX) // R8 = remainder
        a.OrRI(x86_64.R8, 0x30)        // R8 = '0' + remainder
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        // Continue if quotient != 0
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = quotient for next iteration
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(posLoop)

        // Add '-' sign if negative
        a.TestRR(x86_64.R11, x86_64.R11)
        a.JZ(writeIntLabel)

        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64(0x2D)) // R8 = '-'
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        a.JMP(writeIntLabel)

        // Zero case: just "0"
        a.Label(zeroLabel)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64(0x30)) // R8 = '0'
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        a.Label(writeIntLabel)
        // length = (buffer_end) - (current_pos) = (RSP + 256) - R12
        a.MovRR(x86_64.RDX, x86_64.RSP)
        a.AddRI(x86_64.RDX, 256)        // RDX = buffer end
        a.SubRR(x86_64.RDX, x86_64.R12) // RDX = length

        // sys_write(2, R12, RDX) — stderr
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        a.MovRR(x86_64.RSI, x86_64.R12)
        a.SYSCALL()

        doneLabel := a.GenLabel("eprint_done")
        a.JMP(doneLabel)

        // --- TAG_BOOL (2) ---
        notBool := a.GenLabel("eprint_not_bool")
        a.Label(notInt)
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JNE(notBool)

        // Check payload
        falseLabel := a.GenLabel("eprint_false")
        a.ShlRI(x86_64.RDI, 8); a.ShrRI(x86_64.RDI, 8) // clear tag
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(falseLabel)

        // true
        rd.add("true")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "true")
        a.MovRM(x86_64.RDX, 4)
        a.SYSCALL()
        a.JMP(doneLabel)

        // false
        a.Label(falseLabel)
        rd.add("false")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "false")
        a.MovRM(x86_64.RDX, 5)
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_STR (4) ---
        notStr := a.GenLabel("eprint_not_str")
        a.Label(notBool)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // Extract pointer from tagged value
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShlRI(x86_64.RAX, 8); a.ShrRI(x86_64.RAX, 8) // RAX = StrHeader* (clear tag)
        // Length at offset +8, data at offset +24
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RAX, 8)) // len
        a.MovRM(x86_64.RSI, rtStrHeaderSize)                   // add base offset for data ptr
        a.AddRR(x86_64.RSI, x86_64.RAX)                        // RSI = data ptr

        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_NIL / 0 ---
        notNil := a.GenLabel("eprint_not_nil")
        a.Label(notStr)
        nillLabel := a.GenLabel("eprint_nil")
        a.CmpRI(x86_64.RAX, rtTagNil)
        a.JE(nillLabel)
        a.TestRR(x86_64.RAX, x86_64.RAX) // tag 0 = nil
        a.JNZ(notNil)

        a.Label(nillLabel)
        rd.add("nil")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "nil")
        a.MovRM(x86_64.RDX, 3)
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_TABLE (5) ---
        notTable := a.GenLabel("eprint_not_table")
        a.Label(notNil)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        rd.add("[table]")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "[table]")
        a.MovRM(x86_64.RDX, 7)
        a.SYSCALL()
        a.JMP(doneLabel)

        // --- TAG_FP (3): stub ---
        notFp := a.GenLabel("eprint_not_fp")
        a.Label(notTable)
        a.CmpRI(x86_64.RAX, rtTagFp)
        a.JNE(notFp)

        // Float stub: print nothing for now
        a.JMP(doneLabel)

        // --- default: print nothing ---
        a.Label(notFp)
        a.Label(doneLabel)

        // Epilogue: restore stack and R12
        a.AddRI(x86_64.RSP, 256)
        a.POP(x86_64.R12)
        a.RET()

        return fb.finalize()
}

// genPure_Eprintln: y_eprintln(val: tagged) → void
//
// C equivalent:
//   void y_eprintln(uint64_t val) { y_eprint(val); write(2, "\n", 1); }
func genPure_Eprintln(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_eprintln", rd)

        // Save RDI (val) across the y_eprint call.
        fb.a.PUSH(x86_64.RDI)

        // Call y_eprint(val)
        fb.a.CALL("y_eprint")
        fb.addRelocText("y_eprint")

        // Restore RDI
        fb.a.POP(x86_64.RDI)

        // write(2, "\n", 1)
        rd.add("\n")
        fb.a.MovRM(x86_64.RAX, sysWrite)
        fb.a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "\n")
        fb.a.MovRM(x86_64.RDX, 1)
        fb.a.SYSCALL()

        fb.a.RET()

        return fb.finalize()
}

// genPure_TypeOf: y_type_of(val: tagged) → tagged_str
//
// Returns a tagged string describing the type of the value.
// Dispatches on the top 8 tag bits:
//   TAG_INT (1)  → "int"
//   TAG_BOOL (2) → "bool"
//   TAG_FP (3)   → "float"
//   TAG_STR (4)  → "str"
//   TAG_TABLE (5)→ "table"
//   TAG_NIL/0/9  → "nil"
//   TAG_ERR (14) → "error"
//   default      → "unknown"
func genPure_TypeOf(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_type_of", rd)

        a := fb.a

        // Register all string literals in rodata
        rd.add("int")
        rd.add("bool")
        rd.add("float")
        rd.add("str")
        rd.add("table")
        rd.add("nil")
        rd.add("error")
        rd.add("unknown")

        // Pre-generate all labels so they can be shared across JMP and Label
        doneLabel := a.GenLabel("tof_done")
        isNilLabel := a.GenLabel("tof_is_nil")
        notInt := a.GenLabel("tof_not_int")
        notBool := a.GenLabel("tof_not_bool")
        notFp := a.GenLabel("tof_not_fp")
        notStr := a.GenLabel("tof_not_str")
        notTable := a.GenLabel("tof_not_table")
        notNil := a.GenLabel("tof_not_nil")
        notErr := a.GenLabel("tof_not_err")

        // Save RDI (the tagged value) — not strictly needed for y_str_new,
        // but keeps the stack balanced in case of future changes.
        a.PUSH(x86_64.RDI)

        // Extract tag from val (RDI)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)

        // --- TAG_INT (1) ---
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JNE(notInt)
        fb.emitLEA_RodataStr(x86_64.RDI, "int")
        a.MovRM(x86_64.RSI, 3)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_BOOL (2) ---
        a.Label(notInt)
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JNE(notBool)
        fb.emitLEA_RodataStr(x86_64.RDI, "bool")
        a.MovRM(x86_64.RSI, 4)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_FP (3) ---
        a.Label(notBool)
        a.CmpRI(x86_64.RAX, rtTagFp)
        a.JNE(notFp)
        fb.emitLEA_RodataStr(x86_64.RDI, "float")
        a.MovRM(x86_64.RSI, 5)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_STR (4) ---
        a.Label(notFp)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)
        fb.emitLEA_RodataStr(x86_64.RDI, "str")
        a.MovRM(x86_64.RSI, 3)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_TABLE (5) ---
        a.Label(notStr)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)
        fb.emitLEA_RodataStr(x86_64.RDI, "table")
        a.MovRM(x86_64.RSI, 5)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_NIL / 0 ---
        a.Label(notTable)
        a.CmpRI(x86_64.RAX, rtTagNil)
        a.JE(isNilLabel)
        a.TestRR(x86_64.RAX, x86_64.RAX) // tag 0 = nil
        a.JNZ(notNil)

        a.Label(isNilLabel)
        fb.emitLEA_RodataStr(x86_64.RDI, "nil")
        a.MovRM(x86_64.RSI, 3)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_ERR (14) ---
        a.Label(notNil)
        a.CmpRI(x86_64.RAX, rtTagErr)
        a.JNE(notErr)
        fb.emitLEA_RodataStr(x86_64.RDI, "error")
        a.MovRM(x86_64.RSI, 5)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- default: "unknown" ---
        a.Label(notErr)
        fb.emitLEA_RodataStr(x86_64.RDI, "unknown")
        a.MovRM(x86_64.RSI, 7)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        // done: restore and return
        a.Label(doneLabel)
        a.POP(x86_64.RDI) // clean up saved value
        a.RET()

        return fb.finalize()
}

// genPure_ToStr: y_to_str(val: RDI) -> RAX (tagged_str)
//
// Converts any tagged value to its string representation.
// Dispatches on the top 8 tag bits:
//   TAG_INT (1):   decimal string via local int-to-str, return via y_str_new
//   TAG_BOOL (2):  "true" or "false" via y_str_new
//   TAG_STR (4):   return val itself (identity)
//   TAG_NIL/0/9:   "nil" via y_str_new
//   TAG_TABLE (5): "[table]" via y_str_new
//   TAG_FP (3):    "[float]" via y_str_new (stub)
//   TAG_ERR (14):  "error" via y_str_new
//   default:       "unknown" via y_str_new
func genPure_ToStr(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_to_str", rd)

        a := fb.a

        // Register all rodata strings
        rd.add("true")
        rd.add("false")
        rd.add("nil")
        rd.add("[table]")
        rd.add("[float]")
        rd.add("error")
        rd.add("unknown")

        // Pre-generate labels
        strDoneLabel := a.GenLabel("tos_str_done")
        notInt := a.GenLabel("tos_not_int")
        notBool := a.GenLabel("tos_not_bool")
        notFp := a.GenLabel("tos_not_fp")
        notNil := a.GenLabel("tos_not_nil")
        isNilLabel := a.GenLabel("tos_is_nil")
        notTable := a.GenLabel("tos_not_table")
        notErr := a.GenLabel("tos_not_err")
        doneLabel := a.GenLabel("tos_done")

        zeroLabel := a.GenLabel("tos_int_zero")
        callStrNewLabel := a.GenLabel("tos_int_call_str_new")
        negLabel := a.GenLabel("tos_int_neg")
        negDoneLabel := a.GenLabel("tos_int_neg_done")
        posLoop := a.GenLabel("tos_int_pos_loop")
        falseLabel := a.GenLabel("tos_false")

        // Extract tag from val (RDI)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)

        // --- TAG_STR (4): identity — return val itself ---
        // Handle first to avoid prologue/epilogue overhead
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JE(strDoneLabel)

        // --- Prologue: save callee-saved regs, allocate stack ---
        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.SubRI(x86_64.RSP, 256) // int-to-str buffer

        // R12 = buffer end (for backward digit writing)
        a.MovRR(x86_64.R12, x86_64.RSP)
        a.AddRI(x86_64.R12, 256)

        // --- TAG_INT (1): convert to decimal string ---
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JNE(notInt)

        // Sign-extend 56-bit payload into RCX
        fb.getInt(x86_64.RCX, x86_64.RDI)

        // Check if zero
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(zeroLabel)

        // Check negative
        a.Jcc(x86_64.CondS, negLabel)

        // Positive path: R11 = 0 (no negative sign)
        a.MovZeroR64(x86_64.R11)
        a.JMP(posLoop)

        // Negative path: negate, set R11 = 1
        a.Label(negLabel)
        a.MovRM(x86_64.R11, 1)
        a.NegR(x86_64.RCX)

        // Shared digit loop (divides by 10, prepends digits backward)
        a.Label(posLoop)
        a.MovRR(x86_64.RAX, x86_64.RCX) // dividend
        a.CQO()
        a.MovRM(x86_64.RBX, 10)
        a.IDivRR(x86_64.RBX)

        // Prepend digit
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRR(x86_64.R8, x86_64.RDX)
        a.OrRI(x86_64.R8, 0x30) // '0' + remainder
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        // Continue if quotient != 0
        a.MovRR(x86_64.RCX, x86_64.RAX)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(posLoop)

        // Prepend '-' if negative
        a.TestRR(x86_64.R11, x86_64.R11)
        a.JZ(negDoneLabel)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64(0x2D)) // '-'
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        a.Label(negDoneLabel)
        a.JMP(callStrNewLabel)

        // Zero case
        a.Label(zeroLabel)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, -1))
        a.MovRM(x86_64.R8, int64(0x30)) // '0'
        a.MovRMMem(x86_64.MemBase(x86_64.R12, 0), x86_64.R8B)

        // Call y_str_new with the int string
        a.Label(callStrNewLabel)
        // length = (RSP + 256) - R12
        a.MovRR(x86_64.RSI, x86_64.RSP)
        a.AddRI(x86_64.RSI, 256)
        a.SubRR(x86_64.RSI, x86_64.R12) // RSI = length
        a.MovRR(x86_64.RDI, x86_64.R12) // RDI = string ptr
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_BOOL (2) ---
        a.Label(notInt)
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JNE(notBool)

        // Check payload
        a.ShlRI(x86_64.RDI, 8); a.ShrRI(x86_64.RDI, 8) // clear tag
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(falseLabel)

        // true
        fb.emitLEA_RodataStr(x86_64.RDI, "true")
        a.MovRM(x86_64.RSI, 4)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // false
        a.Label(falseLabel)
        fb.emitLEA_RodataStr(x86_64.RDI, "false")
        a.MovRM(x86_64.RSI, 5)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_FP (3): stub ---
        a.Label(notBool)
        a.CmpRI(x86_64.RAX, rtTagFp)
        a.JNE(notFp)
        fb.emitLEA_RodataStr(x86_64.RDI, "[float]")
        a.MovRM(x86_64.RSI, 7)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_NIL / 0 / 9 ---
        a.Label(notFp)
        a.CmpRI(x86_64.RAX, rtTagNil)
        a.JE(isNilLabel)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(notNil)

        a.Label(isNilLabel)
        fb.emitLEA_RodataStr(x86_64.RDI, "nil")
        a.MovRM(x86_64.RSI, 3)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_TABLE (5) ---
        a.Label(notNil)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)
        fb.emitLEA_RodataStr(x86_64.RDI, "[table]")
        a.MovRM(x86_64.RSI, 7)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- TAG_ERR (14) ---
        a.Label(notTable)
        a.CmpRI(x86_64.RAX, rtTagErr)
        a.JNE(notErr)
        fb.emitLEA_RodataStr(x86_64.RDI, "error")
        a.MovRM(x86_64.RSI, 5)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        // --- default: "unknown" ---
        a.Label(notErr)
        fb.emitLEA_RodataStr(x86_64.RDI, "unknown")
        a.MovRM(x86_64.RSI, 7)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        // --- done: epilogue ---
        a.Label(doneLabel)
        a.AddRI(x86_64.RSP, 256)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- TAG_STR identity ---
        a.Label(strDoneLabel)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.RET()

        return fb.finalize()
}

// genPure_SysExit: y_sys_exit(code: tagged_int) → void
//
// C equivalent:
//   void y_sys_exit(uint64_t code) { _exit(GET_INT(code)); }
func genPure_SysExit(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_exit", rd)

        // GET_INT(code) — sign-extend 56-bit from RDI
        fb.getInt(x86_64.RAX, x86_64.RDI)

        // syscall(SYS_exit, int_code)
        fb.a.MovRR(x86_64.RDI, x86_64.RAX) // exit code
        fb.a.MovRM(x86_64.RAX, sysExit)
        fb.a.SYSCALL()

        return fb.finalize()
}

// genPure_Println: y_println(val: tagged) → void
//
// C equivalent:
//   void y_println(uint64_t val) { y_print(val); write(1, "\n", 1); }
func genPure_Println(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_println", rd)

        // Save RDI (val) across the y_print call.
        fb.a.PUSH(x86_64.RDI)

        // Call y_print(val)
        fb.a.MovRR(x86_64.RDI, x86_64.RDI) // already in RDI, NOP but clear
        fb.a.CALL("y_print")
        fb.addRelocText("y_print")

        // Restore RDI
        fb.a.POP(x86_64.RDI)

        // write(1, "\n", 1)
        nlOff := rd.add("\n")
        fb.a.MovRM(x86_64.RAX, sysWrite)
        fb.a.MovRM(x86_64.RDI, 1) // stdout
        fb.emitLEA_RodataStr(x86_64.RSI, "\n")
        fb.a.MovRM(x86_64.RDX, 1)
        fb.a.SYSCALL()

        fb.a.RET()

        _ = nlOff // used implicitly via rodata
        return fb.finalize()
}

// genPure_Copy: y_copy(val: tagged) → tagged
//
// C equivalent:
//   uint64_t y_copy(uint64_t val) { return val; }
func genPure_Copy(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_copy", rd)

        // RDI already has val, just move to RAX
        fb.a.MovRR(x86_64.RAX, x86_64.RDI)
        fb.a.RET()

        return fb.finalize()
}

// genPure_Neg: y_neg(val: tagged_int) → tagged_int
//
// C equivalent:
//   uint64_t y_neg(uint64_t val) { return MK_TAG(TAG_INT, GET_INT(val) ^ VALUE_MASK) + 1; }
func genPure_Neg(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_neg", rd)

        // GET_INT(val)
        fb.getInt(x86_64.RAX, x86_64.RDI)

        // Negate: -val
        fb.a.NegR(x86_64.RAX)

        // MK_TAG(TAG_INT, result)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)

        fb.a.RET()
        return fb.finalize()
}

// genPure_Abs: y_abs(val: tagged_int) → tagged_int
//
// C equivalent:
//   uint64_t y_abs(uint64_t val) {
//       int64_t iv = GET_INT(val);
//       if (iv < 0) iv = -iv;
//       return MK_TAG(TAG_INT, iv);
//   }
func genPure_Abs(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_abs", rd)

        posLabel := fb.a.GenLabel("abs_pos")

        // GET_INT(val)
        fb.getInt(x86_64.RAX, x86_64.RDI)

        // if rax >= 0, skip negation
        fb.a.TestRR(x86_64.RAX, x86_64.RAX)
        fb.a.JGE(posLabel)

        // negate: NEG
        fb.a.NegR(x86_64.RAX)

        fb.a.Label(posLabel)

        // MK_TAG(TAG_INT, rax)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)

        fb.a.RET()
        return fb.finalize()
}

// genPure_Min: y_min(a: tagged_int, b: tagged_int) → tagged_int
func genPure_Min(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_min", rd)

        bLabel := fb.a.GenLabel("min_b")

        // GET_INT(a) into RAX
        fb.getInt(x86_64.RAX, x86_64.RDI)
        // GET_INT(b) into RCX
        fb.getInt(x86_64.RCX, x86_64.RSI)

        // if RAX <= RCX, use RAX (already in place)
        fb.a.CmpRR(x86_64.RAX, x86_64.RCX)
        fb.a.JLE(bLabel)

        // else use RCX
        fb.a.MovRR(x86_64.RAX, x86_64.RCX)

        fb.a.Label(bLabel)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// genPure_Max: y_max(a: tagged_int, b: tagged_int) → tagged_int
func genPure_Max(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_max", rd)

        bLabel := fb.a.GenLabel("max_b")

        fb.getInt(x86_64.RAX, x86_64.RDI)
        fb.getInt(x86_64.RCX, x86_64.RSI)

        fb.a.CmpRR(x86_64.RAX, x86_64.RCX)
        fb.a.JGE(bLabel)

        fb.a.MovRR(x86_64.RAX, x86_64.RCX)

        fb.a.Label(bLabel)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// genPure_SysClock: y_sys_clock() → tagged_int
//
// Returns elapsed time in milliseconds (approximate).
// Uses clock_gettime via syscall or a simple rdtsc-based approach.
// For now, returns 0 as a stub (matching current C runtime).
func genPure_SysClock(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_clock", rd)

        // Stub: return MK_TAG(TAG_INT, 0)
        fb.a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// genPure_SysPlatform: y_sys_platform() → tagged_str
// Returns "linux" as a string. Uses the rodata for the string data.
func genPure_SysPlatform(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_platform", rd)

        // For now, return a nil stub (matching C runtime).
        // A real implementation would create a StrHeader with "linux".
        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// genPure_SysSleep: y_sys_sleep(ms: tagged_int) → void
// Stub — matches C runtime (returns immediately).
func genPure_SysSleep(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_sleep", rd)

        fb.a.RET()
        return fb.finalize()
}

// genPure_SysArgs: y_sys_args() → tagged
// Stub — matches C runtime (returns nil).
func genPure_SysArgs(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_args", rd)

        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_SysArgc: y_sys_argc() → tagged_int
// Stub — returns 1 (matching C runtime).
func genPure_SysArgc(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_argc", rd)

        fb.a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_SysCwd: y_sys_cwd() → tagged_str
// Stub — returns nil (matching C runtime).
func genPure_SysCwd(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_cwd", rd)

        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_SysEnv: y_sys_env() → tagged_table
// Stub — returns nil (matching C runtime).
func genPure_SysEnv(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_env", rd)

        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_SysGetenv: y_sys_getenv(name: tagged_str) → tagged_str
// Stub — returns nil (matching C runtime).
func genPure_SysGetenv(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sys_getenv", rd)

        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_StubInt returns a function that returns MK_TAG(TAG_INT, 0).
func genPure_StubInt(name string, rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        fb.a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_StubFP returns a function that returns MK_TAG(TAG_FP, 0).
// The payload is 0 (null pointer) since we don't have a real float box.
func genPure_StubFP(name string, rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        fb.a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_StubVoid returns a function that returns nothing.
func genPure_StubVoid(name string, rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        fb.a.RET()
        return fb.finalize()
}

// genPure_StubNil returns a function that returns nil (0).
func genPure_StubNil(name string, rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_StubCopy returns a function that returns its first argument unchanged.
func genPure_StubCopy(name string, rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        fb.a.MovRR(x86_64.RAX, x86_64.RDI)
        fb.a.RET()
        return fb.finalize()
}

func genPure_StubNeg(name string, rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        // For negate: MK_TAG(TAG_INT, NEG(GET_INT(val)))
        fb.getInt(x86_64.RAX, x86_64.RDI)
        fb.a.NegR(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_Promote: y_promote(val: tagged) → tagged
// Stub — returns val unchanged (matching C runtime).
func genPure_Promote(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_promote", rd)
        fb.a.MovRR(x86_64.RAX, x86_64.RDI)
        fb.a.RET()
        return fb.finalize()
}

// genPure_Alloc: y_alloc(arena, size, align) → tagged_int (pointer)
// Uses mmap to allocate memory.
func genPure_Alloc(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_alloc", rd)

        // GET_INT(size) from RSI (second arg)
        fb.getInt(x86_64.RSI, x86_64.RSI)

        // Align size to 8 bytes: (size + 7) & ~7
        fb.a.AddRI(x86_64.RSI, 7)
        fb.a.AndRI(x86_64.RSI, ^int64(7))

        // mmap(NULL, size, PROT_READ|PROT_WRITE, MAP_PRIVATE|MAP_ANON, -1, 0)
        // syscall6(SYS_mmap, 0, size, 3, 0x22, -1, 0)
        fb.a.MovRM(x86_64.RAX, sysMmap)
        fb.a.MovZeroR64(x86_64.RDI)          // addr = NULL
        // RSI already has size
        fb.a.MovRM(x86_64.RDX, mmapRW)       // prot
        fb.a.MovRM(x86_64.R10, mmapAnonPriv) // flags
        fb.a.MovRM(x86_64.R8, int64(-1))     // fd = -1
        fb.a.MovZeroR64(x86_64.R9)           // offset = 0
        fb.a.SYSCALL()

        // mmap returns address in RAX, or (void*)(long)-ERRNO on error.
        // Check for error: if result < 0 (and > -4096), return 0.
        errorLabel := fb.a.GenLabel("alloc_ok")
        fb.a.CmpRI(x86_64.RAX, int64(-4096))
        fb.a.JAE(errorLabel)

        // Error case: return 0
        fb.a.MovZeroR64(x86_64.RAX)

        fb.a.Label(errorLabel)
        fb.a.RET()

        return fb.finalize()
}

// genPure_Free: y_free(arena, ptr) → void
// Stub — mmap-allocated memory is not freed (matches C runtime).
func genPure_Free(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_free", rd)
        fb.a.RET()
        return fb.finalize()
}

// genPure_ArenaPush: y_arena_push(parent) → tagged
// Stub — returns 0 (matching C runtime).
func genPure_ArenaPush(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_arena_push", rd)
        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()
        return fb.finalize()
}

// genPure_ArenaPop: y_arena_pop(arena) → void
// Stub (matching C runtime).
func genPure_ArenaPop(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_arena_pop", rd)
        fb.a.RET()
        return fb.finalize()
}

// genPure_Clamp: y_clamp(val, lo, hi) → tagged_int
func genPure_Clamp(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_clamp", rd)

        loLabel := fb.a.GenLabel("clamp_lo")
        hiLabel := fb.a.GenLabel("clamp_hi")

        // Extract all three ints
        fb.getInt(x86_64.RAX, x86_64.RDI) // val
        fb.getInt(x86_64.RCX, x86_64.RSI) // lo
        fb.getInt(x86_64.R8, x86_64.RDX)  // hi

        // if val < lo, use lo
        fb.a.CmpRR(x86_64.RAX, x86_64.RCX)
        fb.a.JGE(loLabel)
        fb.a.MovRR(x86_64.RAX, x86_64.RCX)
        fb.a.JMP(hiLabel)

        fb.a.Label(loLabel)
        // if val > hi, use hi
        fb.a.CmpRR(x86_64.RAX, x86_64.R8)
        fb.a.JLE(hiLabel)
        fb.a.MovRR(x86_64.RAX, x86_64.R8)

        fb.a.Label(hiLabel)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// genPure_Error: y_error(msg: tagged_str) → tagged_err
// Wraps a string value as an error (tag 14).
func genPure_Error(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_error", rd)

        // RDI has the string tagged value. Just change the tag to TAG_ERR.
        fb.a.MovRR(x86_64.RAX, x86_64.RDI)
        // Clear current tag
        fb.a.ShlRI(x86_64.RAX, 8); fb.a.ShrRI(x86_64.RAX, 8) // clear tag
        // Set TAG_ERR
        fb.a.OrRI(x86_64.RAX, int64(rtTagErr<<rtTagShift))
        fb.a.RET()

        return fb.finalize()
}

// genPure_IsError: y_is_error(val) → tagged_bool
func genPure_IsError(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_is_error", rd)

        // Extract tag
        fb.a.MovRR(x86_64.RAX, x86_64.RDI)
        fb.a.ShrRI(x86_64.RAX, 56)
        fb.a.AndRI(x86_64.RAX, 0xFF)

        // Compare with TAG_ERR (14)
        fb.a.CmpRI(x86_64.RAX, rtTagErr)
        fb.a.SetE(x86_64.RAX)

        // MK_TAG(TAG_BOOL, result)
        fb.mkTag(rtTagBool, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// genPure_ValEq: y_val_eq(a, b) → tagged_bool
// Compares two tagged values for equality.
func genPure_ValEq(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_val_eq", rd)

        notEqual := fb.a.GenLabel("veq_ne")

        fb.a.CmpRR(x86_64.RDI, x86_64.RSI)
        fb.a.JNE(notEqual)

        // Equal: return true
        fb.a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        fb.a.RET()

        fb.a.Label(notEqual)
        // Not equal: return false
        fb.a.MovZeroR64(x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// genPure_EnumEq: y_enum_eq(a, b) → tagged_bool
// Structural equality for enum values (table-backed).
// Implemented in puregen_enum_eq.go.
func genPure_EnumEq_(rd *rodataBuilder) puregenFunc {
        return genPure_EnumEq(rd)
}

// genPure_Sign: y_sign(val: tagged_int) → tagged_int
// Returns -1, 0, or 1 based on the sign of the integer.
func genPure_Sign(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sign", rd)

        zeroLabel := fb.a.GenLabel("sign_zero")
        negLabel := fb.a.GenLabel("sign_neg")

        fb.getInt(x86_64.RAX, x86_64.RDI)

        fb.a.TestRR(x86_64.RAX, x86_64.RAX)
        fb.a.JZ(zeroLabel)                   // if zero, return 0
        fb.a.Jcc(x86_64.CondS, negLabel)      // if negative, return -1

        // Positive: return 1
        fb.a.MovRM(x86_64.RAX, 1)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        // Zero: return 0
        fb.a.Label(zeroLabel)
        fb.a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        // Negative: return -1
        fb.a.Label(negLabel)
        fb.a.MovRM(x86_64.RAX, int64(-1))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        fb.a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// Pure-Go runtime generation entry point (init)
// ---------------------------------------------------------------------------

var (
        // puregenFunctions holds all generated runtime functions.
        puregenFunctions []puregenFunc

        // puregenRodataStr holds the string literal data section.
        puregenRodataStr []byte

        // puregenRelocations holds all relocations for the generated code.
        puregenRelocations []puregenReloc

        // puregenDone indicates generation is complete.
        puregenDone bool
)

func init() {
        rd := newRodataBuilder()

        // Generate each runtime function.
        // Simple stubs first, then progressively more complex ones.
        generators := []func(*rodataBuilder) puregenFunc{
                // Core
                genPure_Print,           // y_print (pure-Go print)
                genPure_Println,         // y_println (calls y_print via PLT32)
                genPure_Eprint,          // y_eprint (prints to stderr)
                genPure_Eprintln,        // y_eprintln (calls y_eprint via PLT32)
                genPure_SysExit,         // y_sys_exit
                genPure_Copy,            // y_copy
                genPure_Promote,         // y_promote
                genPure_Neg,             // y_neg
                genPure_Abs,             // y_abs
                genPure_Min,             // y_min
                genPure_Max,             // y_max
                genPure_Sign,            // y_sign
                genPure_Clamp,           // y_clamp
                genPure_ValEq,           // y_val_eq
                genPure_Error,           // y_error
                genPure_IsError,         // y_is_error

                // Core ops
                genPure_Input,            // y_input (reads a line from stdin)
                genPure_Len,              // y_len
                genPure_Panic,   // y_panic
                genPure_Assert,  // y_assert
                genPure_TypeOf,          // y_type_of (returns tagged type string)

                // Memory / Arena
                genPure_Alloc,
                genPure_Free,
                genPure_ArenaPush,
                genPure_ArenaPop,

                // System stubs
                genPure_SysClock,
                genPure_SysPlatform,
                genPure_SysSleep,
                genPure_SysArgs,
                genPure_SysArgc,
                genPure_SysCwd,
                genPure_SysEnv,
                genPure_SysGetenv,

                // Math ops
                genPure_Sqrt,
                genPure_Floor,
                genPure_Ceil,
                genPure_Round,

                // String ops
                genPure_StrNew,          // y_str_new
                genPure_StrConcat,       // y_str_concat

                // Conversion ops
                genPure_ToStr,           // y_to_str
                genPure_ToInt,           // y_to_int
                genPure_ToFp,            // y_to_fp

                // String ops
                genPure_StrLen,           // y_str_len
                genPure_Trim,             // y_trim
                genPure_Lower,            // y_lower
                genPure_Upper,            // y_upper
                genPure_Substr,           // y_substr
                genPure_Contains,         // y_contains
                genPure_StartsWith,       // y_starts_with
                genPure_EndsWith,         // y_ends_with
                genPure_Find,             // y_find
                genPure_StrSplit,          // y_str_split
                genPure_StrReplace,        // y_str_replace
                genPure_StrBytes,          // y_str_bytes
                genPure_StrToInt,          // y_str_to_int
                genPure_StrToFloat,        // y_str_to_float (stub)
                genPure_StrRepeat,         // y_str_repeat

                // Internal helpers (called by table ops via PLT32)
                genPure_HashTagged,       // pure_hash_tagged
                genPure_ValuesEqual,      // pure_values_equal

                // Table ops
                genPure_TableNew,         // y_table_new
                genPure_TabLen,           // y_tab_len
                genPure_TabGet,           // y_tab_get
                genPure_TabHas,           // y_tab_has
                genPure_TabDel,           // y_tab_del
                genPure_TableRehash,      // pure_table_rehash (called by y_tab_set)
                genPure_TabSet,           // y_tab_set

                // Table iterator ops
                genPure_TabGetValType,    // y_tab_get_val_type (stub: returns nil)
                genPure_TabIterValid,     // y_tab_iter_valid
                genPure_TabIterKey,       // y_tab_iter_key
                genPure_TabIterVal,       // y_tab_iter_val
                genPure_TabIterNext,      // y_tab_iter_next

                // Iterator ops
                genPure_IterNew,          // runtime_iter_new
                genPure_IterNext,         // runtime_iter_next
                genPure_IterGetKey,       // runtime_iter_get_key
                genPure_IterGetVal,       // runtime_iter_get_val
                genPure_IterGetNext,      // runtime_iter_get_next

                // Iterator ops (stubs)
                genPure_EnumEq_,       // y_enum_eq (enum structural equality)

                // Float ops
                genPure_FpNew,
                genPure_FpAdd,
                genPure_FpSub,
                genPure_FpMul,
                genPure_FpDiv,
                genPure_FpNeg,

                // FS ops
                func(rd *rodataBuilder) puregenFunc { return genPure_FsExists(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsReadText(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsWriteText(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsAppendText(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsReadLines(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsRemove(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsRename(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsCopy(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsMkdir(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsRmdir(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsReadDir(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsIsFile(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsIsDir(rd) },
                func(rd *rodataBuilder) puregenFunc { return genPure_FsFileSize(rd) },

                // Path ops
                genPure_PathNormalize,
                genPure_PathResolve,
                genPure_PathResolve2,
                genPure_PathRelative,
                genPure_PathJoin,
                genPure_PathDirname,
                genPure_PathParent,
                genPure_PathBasename,
                genPure_PathStem,
                genPure_PathExtname,
                genPure_PathIsAbs,
                genPure_PathSep,
                genPure_PathSepPosix,
                genPure_PathSepWin,

                // JSON
                genPure_JsonEncode,
                genPure_JsonDecode,

                // Closures
                genPure_ClosureNew,
                genPure_ClosureSet,
                genPure_ClosureGet,
                genPure_ClosureTrampoline,
        }

        for _, gen := range generators {
                fn := gen(rd)
                puregenFunctions = append(puregenFunctions, fn)
                puregenRelocations = append(puregenRelocations, fn.Relocations...)
        }

        // Store the rodata string section.
        puregenRodataStr = make([]byte, len(rd.data))
        copy(puregenRodataStr, rd.data)

        puregenDone = true
}

// ---------------------------------------------------------------------------
// Public API for the pure-Go runtime
// ---------------------------------------------------------------------------

// PuregenAvailable returns true if the pure-Go runtime was generated.
func PuregenAvailable() bool {
        return puregenDone
}

// PuregenGetFunctionCode returns the machine code for a generated function.
// Returns nil if the function is not in the pure-Go set.
func PuregenGetFunctionCode(name string) []byte {
        if !puregenDone {
                return nil
        }
        for _, fn := range puregenFunctions {
                if fn.Name == name {
                        return fn.Code
                }
        }
        return nil
}

// PuregenGetAllFunctions returns all generated function names.
func PuregenGetAllFunctions() []string {
        if !puregenDone {
                return nil
        }
        names := make([]string, len(puregenFunctions))
        for i, fn := range puregenFunctions {
                names[i] = fn.Name
        }
        return names
}

// PuregenGetRodataStr returns the generated rodata string section.
func PuregenGetRodataStr() []byte {
        return puregenRodataStr
}

// PuregenGetRelocations returns all generated relocations (flat list).
func PuregenGetRelocations() []puregenReloc {
        return puregenRelocations
}

// PuregenRelocationsByFunc returns relocations grouped by source function name.
// This is used by the linker since puregen functions are separate code sections,
// and their relocations use offsets relative to each function's own code blob.
func PuregenRelocationsByFunc() map[string][]puregenReloc {
        result := make(map[string][]puregenReloc)
        for _, fn := range puregenFunctions {
                if len(fn.Relocations) > 0 {
                        result[fn.Name] = fn.Relocations
                }
        }
        return result
}

// PuregenRodataBaseOffset returns the byte offset within the merged rodata
// section where puregen string data begins. Puregen strings are appended
// after C-compiled rodata, so this equals len(C rodata).
// The linker must add this to any puregen rodata relocation addends.
func PuregenRodataBaseOffset() uint64 {
        return uint64(len(GetRuntimeRodataStr()))
}
