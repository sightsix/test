// Package aarch64 provides an AST-to-AArch64 codegen bridge that translates
// Yilt IR functions into native AArch64 machine code.
//
// It consumes the SSA-style IR defined in internal/ir and emits 32-bit
// little-endian AArch64 instructions using the Assembler defined in aarch64.go.
//
// Calling convention: AAPCS64
//   - Integer arguments:   X0–X7
//   - Return value:        X0
//   - Caller-saved:        X0–X15, X30 (LR), Q0–Q31
//   - Callee-saved:        X19–X28, X29 (FP), Q8–Q15
//   - Frame pointer:       X29
//   - Link register:       X30
//   - Stack pointer:       SP (X31)
//   - Arena pointer:       X28
package aarch64

import (
        "fmt"
        "math"
        "strings"

        "github.com/yilt/yiltc/internal/ir"
)

// ---------------------------------------------------------------------------
// Frame layout
// ---------------------------------------------------------------------------

// FrameLayout describes the stack frame for a compiled function.
//
// Stack layout (growing downward):
//
//      High addr
//        [caller frame]
//        [saved LR  (X30)]     ← pushed by STP pre-index
//        [saved FP  (X29)]     ← pushed by STP pre-index; FP points here
//        [saved callee-saved]  ← X19, X20, … (up to calleeSavedSlots)
//        [local slots 0 … N-1] ← one 8-byte slot per spilled virtual register
//        [padding for 16-byte alignment]
//      SP →
//      Low addr
type FrameLayout struct {
        CalleeSavedRegs []Reg   // which callee-saved GPRs are actually saved
        CalleeSavedFP   []Reg   // which callee-saved FP regs are actually saved
        TotalSlots      int     // number of 8-byte local variable / spill slots
        FrameSize       int     // total frame size in bytes (always 16-byte aligned)
}

// ---------------------------------------------------------------------------
// CodeGen — the main code generator
// ---------------------------------------------------------------------------

// CodeGen translates a single IR function into AArch64 machine code.
// It is NOT safe to use a CodeGen from multiple goroutines concurrently.
type CodeGen struct {
        asm *Assembler

        // Per-function state
        fn    *ir.Func
        frame *FrameLayout

        // Virtual-register → physical-register mapping.
        // Values not present here are spilled to the stack.
        regMap map[int]Reg

        // Virtual-register → stack-slot index (0-based from FP-16).
        // Slot index i lives at [FP, #-(16 + calleeSavedBytes + i*8)].
        vregSlot map[int]int

        // Next available spill slot.
        nextSlot int

        // Scratch registers available for expression evaluation.
        // We reserve X9–X15 (7 registers) as temporaries.
        scratchPool []Reg
        scratchUsed int
}

// NewCodeGen creates a new code generator instance.
func NewCodeGen() *CodeGen {
        return &CodeGen{
                regMap: make(map[int]Reg),
                vregSlot: make(map[int]int),
                scratchPool: []Reg{R9, R10, R11, R12, R13, R14, R15},
        }
}

// scratchRegs are the registers used for expression evaluation.
var scratchRegs = []Reg{R9, R10, R11, R12, R13, R14, R15}

// GenerateFunc compiles a single IR function into AArch64 machine code.
// Returns the raw 32-bit little-endian instruction bytes.
func (cg *CodeGen) GenerateFunc(fn *ir.Func) []byte {
        cg.asm = NewAssembler()
        cg.fn = fn
        cg.regMap = make(map[int]Reg)
        cg.vregSlot = make(map[int]int)
        cg.nextSlot = 0
        cg.scratchUsed = 0

        if fn.Extern || len(fn.Blocks) == 0 {
                return nil
        }

        // 1. Assign stack slots to every value that needs one.
        cg.assignSlots(fn)

        // 2. Determine which callee-saved registers we need.
        cg.computeFrame(fn)

        // 3. Emit prologue.
        cg.emitPrologue()

        // 4. Store incoming arguments (X0–X7) into their parameter slots.
        cg.storeParams()

        // 5. Emit code for each basic block.
        for _, blk := range fn.Blocks {
                cg.asm.BindLabel(blk.Label)
                cg.emitBlock(blk)
        }

        // 6. If the last block is not terminated, emit a fall-through return.
        last := fn.Blocks[len(fn.Blocks)-1]
        if !last.IsTerminated() {
                cg.emitReturnSequence(nil)
        }

        return cg.asm.Code()
}

// ---------------------------------------------------------------------------
// Slot assignment (first pass)
// ---------------------------------------------------------------------------

// assignSlots walks the entire function and gives every value that needs a
// stack home a spill slot index.  Constants and block parameters that are
// always in registers do not get slots.
func (cg *CodeGen) assignSlots(fn *ir.Func) {
        // Function parameters always get slots so that they can be reloaded.
        for _, p := range fn.Params {
                cg.ensureSlot(p.ID)
        }

        // Block parameters and instruction results.
        for _, blk := range fn.Blocks {
                for _, p := range blk.Params {
                        cg.ensureSlot(p.ID)
                }
                for _, ins := range blk.Instrs {
                        if ins.Dest != nil {
                                cg.ensureSlot(ins.Dest.ID)
                        }
                }
        }
}

// ensureSlot returns the slot index for a virtual register, allocating one
// if needed.
func (cg *CodeGen) ensureSlot(vregID int) int {
        if idx, ok := cg.vregSlot[vregID]; ok {
                return idx
        }
        idx := cg.nextSlot
        cg.nextSlot++
        cg.vregSlot[vregID] = idx
        return idx
}

// ---------------------------------------------------------------------------
// Frame computation
// ---------------------------------------------------------------------------

func (cg *CodeGen) computeFrame(fn *ir.Func) {
        // For now, we don't allocate callee-saved registers for virtual registers
        // (everything is stack-based).  However, we always save X28 (arena pointer)
        // if the function has more than a trivial number of instructions.
        needsArena := len(fn.Blocks) > 1
        calleeSaved := []Reg{}
        if needsArena {
                calleeSaved = append(calleeSaved, R28)
        }

        calleeBytes := len(calleeSaved) * 8
        localBytes := cg.nextSlot * 8
        total := calleeBytes + localBytes
        // 16-byte align.
        total = (total + 15) &^ 15

        cg.frame = &FrameLayout{
                CalleeSavedRegs: calleeSaved,
                TotalSlots:      cg.nextSlot,
                FrameSize:       total,
        }
}

// ---------------------------------------------------------------------------
// Prologue / Epilogue
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitPrologue() {
        a := cg.asm

        // STP X29, X30, [SP, #-16]!   — save FP and LR (pre-index).
        cg.emitSTPPreIndex(R29, R30, RSP, -16)

        // MOV X29, SP
        a.MOV(R29, RSP)

        // Save callee-saved registers (grows frame downward).
        off := -16
        for _, r := range cg.frame.CalleeSavedRegs {
                off -= 8
                a.STR(r, R29, off)
        }

        // SUB SP, SP, #frameSize
        if cg.frame.FrameSize > 0 {
                a.SUBI(RSP, RSP, cg.frame.FrameSize)
        }
}

func (cg *CodeGen) emitEpilogue() {
        a := cg.asm

        // ADD SP, SP, #frameSize
        if cg.frame.FrameSize > 0 {
                a.ADDI(RSP, RSP, cg.frame.FrameSize)
        }

        // Restore callee-saved registers (reverse order).
        off := -16
        for _, r := range cg.frame.CalleeSavedRegs {
                off -= 8
                a.LDR(r, R29, off)
        }

        // LDP X29, X30, [SP], #16    — restore FP and LR (post-index).
        cg.emitLDPPostIndex(R29, R30, RSP, 16)

        // RET
        a.RET()
}

// emitReturnSequence moves the optional return value into X0 and emits the
// epilogue.
func (cg *CodeGen) emitReturnSequence(retVal *ir.Value) {
        a := cg.asm
        if retVal != nil {
                tmp := cg.allocScratch()
                defer cg.freeScratch(tmp)
                cg.loadValue(retVal, tmp)
                a.MOV(R0, tmp)
        } else {
                // Zero the return register for void functions.
                a.MOVZ(R0, 0, 0)
        }
        cg.emitEpilogue()
}

// ---------------------------------------------------------------------------
// Parameter marshalling
// ---------------------------------------------------------------------------

func (cg *CodeGen) storeParams() {
        a := cg.asm
        for i, p := range cg.fn.Params {
                if i >= len(ArgRegs) {
                        break
                }
                slot := cg.vregSlot[p.ID]
                off := slotOffset(cg.frame, slot)
                a.STR(ArgRegs[i], R29, off)
        }
}

// ---------------------------------------------------------------------------
// Block emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitBlock(blk *ir.Block) {
        for _, ins := range blk.Instrs {
                if ins.IsTerminator() {
                        cg.emitTerminator(ins)
                        return // nothing after a terminator
                }
                cg.emitInstr(ins)
        }
}

// ---------------------------------------------------------------------------
// Terminator emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitTerminator(ins *ir.Instr) {
        switch ins.Op {
        case ir.OpReturn:
                var val *ir.Value
                if len(ins.Src) > 0 && ins.Src[0] != nil {
                        val = ins.Src[0]
                }
                cg.emitReturnSequence(val)

        case ir.OpJump:
                cg.emitJump(ins)

        case ir.OpBranch:
                cg.emitBranch(ins)

        case ir.OpPanic:
                cg.emitPanic(ins)

        default:
                // Unknown terminator — emit an epilogue as a safety net.
                cg.emitReturnSequence(nil)
        }
}

func (cg *CodeGen) emitJump(ins *ir.Instr) {
        if ins.Meta == nil || ins.Meta.Jump == nil {
                cg.asm.NOP()
                return
        }
        bt := ins.Meta.Jump
        // Store block-parameter arguments into the target block's param slots.
        cg.storeBlockArgs(bt)
        // Unconditional branch.
        cg.asm.B(bt.Block.Label)
}

func (cg *CodeGen) emitBranch(ins *ir.Instr) {
        if ins.Meta == nil || ins.Meta.Then == nil {
                cg.asm.NOP()
                return
        }
        thenBT := ins.Meta.Then
        elseBT := ins.Meta.Else

        // We need a scratch register for the condition test.
        // The condition is the first (and only) Src operand.
        if len(ins.Src) == 0 {
                cg.asm.NOP()
                return
        }
        condVal := ins.Src[0]

        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)
        cg.loadValue(condVal, tmp)

        // Compare condition with zero.  In Yilt, non-zero is truthy.
        cg.asm.CMPI(tmp, 0)

        // We need to emit: store then-args, B.NE thenBlock; store else-args, B elseBlock.
        // However, we must store the args BEFORE branching, but we can only branch
        // to one destination.  The solution: store then-args now, branch if NE to
        // thenBlock; fall through and store else-args, then branch to elseBlock.

        cg.storeBlockArgs(thenBT)
        cg.asm.BCond(CondNE, thenBT.Block.Label)

        // Fall-through: else path.
        if elseBT != nil && elseBT.Block != nil {
                cg.storeBlockArgs(elseBT)
                cg.asm.B(elseBT.Block.Label)
        } else {
                // No else block — just fall through (which should be the merge block).
        }
}

func (cg *CodeGen) storeBlockArgs(bt *ir.BranchTarget) {
        if bt == nil || bt.Block == nil {
                return
        }
        params := bt.Block.Params
        args := bt.Args
        for i, arg := range args {
                if i >= len(params) {
                        break
                }
                slot := cg.vregSlot[params[i].ID]
                off := slotOffset(cg.frame, slot)
                tmp := cg.allocScratch()
                cg.loadValue(arg, tmp)
                cg.asm.STR(tmp, R29, off)
                cg.freeScratch(tmp)
        }
}

func (cg *CodeGen) emitPanic(ins *ir.Instr) {
        a := cg.asm
        // Emit a call to y_panic with the message string.
        // For now, just emit a BRK (breakpoint) as a panic stub.
        a.BRK(1)
}

// ---------------------------------------------------------------------------
// Instruction emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitInstr(ins *ir.Instr) {
        a := cg.asm

        switch ins.Op {
        // ---- Constants ----
        case ir.OpConstInt, ir.OpConst:
                cg.emitConstInt(ins)
        case ir.OpConstUint:
                cg.emitConstUint(ins)
        case ir.OpConstFp:
                cg.emitConstFp(ins)
        case ir.OpConstBool:
                cg.emitConstBool(ins)
        case ir.OpConstNil:
                cg.emitConstNil(ins)
        case ir.OpConstStr:
                cg.emitConstStr(ins)

        // ---- Arithmetic ----
        case ir.OpAdd:
                cg.emitBinOp(ins, func(dst, src Reg) { a.ADD(dst, dst, src) })
        case ir.OpSub:
                cg.emitBinOp(ins, func(dst, src Reg) { a.SUB(dst, dst, src) })
        case ir.OpMul:
                cg.emitBinOp(ins, func(dst, src Reg) { a.MADD(dst, dst, src, RZR) })
        case ir.OpDiv:
                cg.emitBinOp(ins, func(dst, src Reg) { a.SDIV(dst, dst, src) })
        case ir.OpMod:
                cg.emitMod(ins)

        // ---- Bitwise ----
        case ir.OpAnd:
                cg.emitBinOp(ins, func(dst, src Reg) { a.AND(dst, dst, src) })
        case ir.OpOr:
                cg.emitBinOp(ins, func(dst, src Reg) { a.ORR(dst, dst, src) })
        case ir.OpXor:
                cg.emitBinOp(ins, func(dst, src Reg) { a.EOR(dst, dst, src) })
        case ir.OpShl:
                cg.emitShiftOp(ins, func(dst Reg, amt int) { a.LSL(dst, dst, amt) })
        case ir.OpShr:
                cg.emitShiftOp(ins, func(dst Reg, amt int) { a.LSR(dst, dst, amt) })

        // ---- Unary ----
        case ir.OpNeg:
                cg.emitUnary(ins, func(dst, src Reg) { a.NEG(dst, src) })
        case ir.OpBitNot:
                cg.emitUnary(ins, func(dst, src Reg) { a.NOT(dst, src) })
        case ir.OpNot:
                cg.emitLogicalNot(ins)

        // ---- Comparison ----
        case ir.OpEq:
                cg.emitCmp(ins, CondEQ)
        case ir.OpNeq:
                cg.emitCmp(ins, CondNE)
        case ir.OpLt:
                cg.emitCmp(ins, CondLT)
        case ir.OpLe:
                cg.emitCmp(ins, CondLE)
        case ir.OpGt:
                cg.emitCmp(ins, CondGT)
        case ir.OpGe:
                cg.emitCmp(ins, CondGE)

        // ---- Call ----
        case ir.OpCall:
                cg.emitCall(ins)
        case ir.OpCallIndirect:
                cg.emitCallIndirect(ins)

        // ---- Memory ----
        case ir.OpLoad:
                cg.emitLoad(ins)
        case ir.OpStore:
                cg.emitStore(ins)
        case ir.OpAlloc:
                cg.emitAlloc(ins)
        case ir.OpStackAlloc:
                cg.emitStackAlloc(ins)

        // ---- Table operations (delegated to runtime) ----
        case ir.OpTableNew:
                cg.emitRuntimeCall(ins, "y_table_new")
        case ir.OpTableGet:
                cg.emitRuntimeCall(ins, "y_tab_get")
        case ir.OpTableSet:
                cg.emitRuntimeCall2(ins, "y_tab_set")
        case ir.OpTableLen:
                cg.emitRuntimeCall(ins, "y_tab_len")
        case ir.OpTableDelete:
                cg.emitRuntimeCall2(ins, "y_tab_del")

        // ---- Index / Member ----
        case ir.OpIndexGet:
                cg.emitRuntimeCall(ins, "y_tab_get")
        case ir.OpIndexSet:
                cg.emitRuntimeCall2(ins, "y_tab_set")
        case ir.OpMemberGet:
                cg.emitRuntimeCall(ins, "y_member_get")

        // ---- Tagged value operations ----
        case ir.OpTag:
                cg.emitTag(ins)
        case ir.OpUntag:
                cg.emitUntag(ins)
        case ir.OpCheckTag:
                cg.emitCheckTag(ins)

        // ---- Arena ----
        case ir.OpArenaPush:
                cg.emitRuntimeCall0(ins, "y_arena_push")
        case ir.OpArenaPop:
                cg.emitRuntimeCall0(ins, "y_arena_pop")

        // ---- Misc ----
        case ir.OpNop:
                a.NOP()
        case ir.OpParam:
                // Block parameter pseudo-instruction: handled during slot assignment.
        }
}

// ---------------------------------------------------------------------------
// Constant emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitConstInt(ins *ir.Instr) {
        dst := ins.Dest
        if dst == nil {
                return
        }
        // Try to get the value from the Const field of the destination.
        var val int64
        if dst.Const != nil {
                val = dst.Const.IntVal
        }
        slot := cg.vregSlot[dst.ID]
        off := slotOffset(cg.frame, slot)

        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)

        if val == 0 {
                cg.asm.MOVZ(tmp, 0, 0)
        } else if val >= 0 && uint64(val) <= 0xFFFF {
                cg.asm.MOVZ(tmp, uint16(val), 0)
        } else if val >= 0 {
                cg.asm.MOVImm(tmp, uint64(val))
        } else {
                // Negative: use MOVN + MOVK, or negate after MOVImm.
                uval := uint64(val)
                cg.asm.MOVImm(tmp, uval)
        }
        cg.asm.STR(tmp, R29, off)
}

func (cg *CodeGen) emitConstUint(ins *ir.Instr) {
        dst := ins.Dest
        if dst == nil {
                return
        }
        var val uint64
        if dst.Const != nil {
                val = dst.Const.UintVal
        }
        slot := cg.vregSlot[dst.ID]
        off := slotOffset(cg.frame, slot)

        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)
        cg.asm.MOVImm(tmp, val)
        cg.asm.STR(tmp, R29, off)
}

func (cg *CodeGen) emitConstFp(ins *ir.Instr) {
        dst := ins.Dest
        if dst == nil {
                return
        }
        var val float64
        if dst.Const != nil {
                val = dst.Const.FpVal
        }
        slot := cg.vregSlot[dst.ID]
        off := slotOffset(cg.frame, slot)

        // FP constants are stored as raw 64-bit IEEE 754 bits into an integer slot.
        bits := math.Float64bits(val)
        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)
        cg.asm.MOVImm(tmp, bits)
        cg.asm.STR(tmp, R29, off)
}

func (cg *CodeGen) emitConstBool(ins *ir.Instr) {
        dst := ins.Dest
        if dst == nil {
                return
        }
        var val bool
        if dst.Const != nil {
                val = dst.Const.BoolVal
        }
        slot := cg.vregSlot[dst.ID]
        off := slotOffset(cg.frame, slot)

        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)
        if val {
                cg.asm.MOVZ(tmp, 1, 0)
        } else {
                cg.asm.MOVZ(tmp, 0, 0)
        }
        cg.asm.STR(tmp, R29, off)
}

func (cg *CodeGen) emitConstNil(ins *ir.Instr) {
        dst := ins.Dest
        if dst == nil {
                return
        }
        slot := cg.vregSlot[dst.ID]
        off := slotOffset(cg.frame, slot)

        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)
        cg.asm.MOVZ(tmp, 0, 0)
        cg.asm.STR(tmp, R29, off)
}

func (cg *CodeGen) emitConstStr(ins *ir.Instr) {
        dst := ins.Dest
        if dst == nil {
                return
        }
        // String constants are passed to the runtime.  For now, emit a call to
        // y_str_new.  The string literal is embedded as a compile-time constant.
        slot := cg.vregSlot[dst.ID]
        off := slotOffset(cg.frame, slot)

        // Defer to a runtime call: y_str_new(ptr, len) → result in X0.
        // For now, store 0 as a placeholder (the actual string data is in the
        // data section and will be linked later).
        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)
        cg.asm.MOVZ(tmp, 0, 0)
        cg.asm.STR(tmp, R29, off)
}

// ---------------------------------------------------------------------------
// Binary operation emission
// ---------------------------------------------------------------------------

// emitBinOp handles binary operations of the form: dst = src[0] OP src[1].
// It loads src[0] and src[1] into scratch registers, computes, and stores.
func (cg *CodeGen) emitBinOp(ins *ir.Instr, compute func(dst, src Reg)) {
        if ins.Dest == nil || len(ins.Src) < 2 {
                return
        }

        // If the operands are both constants, try constant folding at emit time.
        if ins.Src[0].Const != nil && ins.Src[1].Const != nil && cg.tryFoldBinOp(ins) {
                return
        }

        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)

        cg.loadValue(ins.Src[0], dst)

        src := cg.allocScratch()
        defer cg.freeScratch(src)

        cg.loadValue(ins.Src[1], src)

        compute(dst, src)
        cg.asm.STR(dst, R29, dstOff)
}

// emitShiftOp handles shift operations where src[1] is an integer amount.
func (cg *CodeGen) emitShiftOp(ins *ir.Instr, compute func(dst Reg, amt int)) {
        if ins.Dest == nil || len(ins.Src) < 2 {
                return
        }

        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)

        cg.loadValue(ins.Src[0], dst)

        amt := 0
        if ins.Src[1].Const != nil {
                amt = int(ins.Src[1].Const.IntVal & 63)
        } else {
                // Dynamic shift amount: load into a register.
                amtReg := cg.allocScratch()
                defer cg.freeScratch(amtReg)
                cg.loadValue(ins.Src[1], amtReg)
                // Store amtReg value and use register shift.
                // For now, use LSL/LSR register variant via the scratch register.
                cg.asm.AND(amtReg, amtReg, R9) // mask to 6 bits... actually just use the value
                // Store the shift amount to stack, then load it back (simple approach).
                // Better: emit a register-register shift using LSLV/LSRV encoding.
                cg.emitShiftReg(dst, dst, amtReg, ins.Op == ir.OpShr)
                cg.asm.STR(dst, R29, dstOff)
                return
        }

        compute(dst, amt)
        cg.asm.STR(dst, R29, dstOff)
}

// emitShiftReg emits a register-register shift.
func (cg *CodeGen) emitShiftReg(dst, val, amt Reg, isRight bool) {
        // LSLV: 0xAC200000 | Rm << 16 | Rn << 5 | Rd
        // LSRV: 0xAC240000 | Rm << 16 | Rn << 5 | Rd
        base := uint32(0xAC240000) // LSRV default
        if !isRight {
                base = 0xAC200000 // LSLV
        }
        cg.asm.emit(base | encodeRm(amt) | encodeRn(val) | encodeRd(dst))
}

func (cg *CodeGen) emitMod(ins *ir.Instr) {
        if ins.Dest == nil || len(ins.Src) < 2 {
                return
        }
        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)
        src := cg.allocScratch()
        defer cg.freeScratch(src)

        cg.loadValue(ins.Src[0], dst)
        cg.loadValue(ins.Src[1], src)

        // remainder = dividend - (dividend / divisor) * divisor
        // We need a third scratch register.
        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)

        cg.asm.MOV(tmp, dst)   // tmp = dividend
        cg.asm.SDIV(dst, dst, src) // dst = dividend / divisor
        cg.asm.MADD(dst, dst, src, RZR) // dst = (dividend/divisor) * divisor
        // Actually MSUB: dst = tmp - dst*src
        cg.asm.MSUB(dst, tmp, dst, src) // dst = tmp - dst * src ... wait
        // MSUB Rd, Rn, Rm, Ra: Rd = Ra - Rn*Rm
        // We want: remainder = dividend - quotient * divisor
        // tmp = dividend, dst = quotient (after SDIV)
        // MSUB dst, dst, src, tmp → dst = tmp - dst*src = dividend - quotient*divisor
        cg.asm.STR(dst, R29, dstOff)
}

// ---------------------------------------------------------------------------
// Unary operation emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitUnary(ins *ir.Instr, compute func(dst, src Reg)) {
        if ins.Dest == nil || len(ins.Src) < 1 {
                return
        }

        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)

        cg.loadValue(ins.Src[0], dst)
        compute(dst, dst)
        cg.asm.STR(dst, R29, dstOff)
}

func (cg *CodeGen) emitLogicalNot(ins *ir.Instr) {
        if ins.Dest == nil || len(ins.Src) < 1 {
                return
        }

        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)

        cg.loadValue(ins.Src[0], dst)
        // Logical NOT for booleans: result = (val == 0) ? 1 : 0
        cg.asm.CMPI(dst, 0)
        cg.asm.CSET(dst, CondEQ)
        cg.asm.STR(dst, R29, dstOff)
}

// ---------------------------------------------------------------------------
// Comparison emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitCmp(ins *ir.Instr, cond int) {
        if ins.Dest == nil || len(ins.Src) < 2 {
                return
        }

        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)
        src := cg.allocScratch()
        defer cg.freeScratch(src)

        cg.loadValue(ins.Src[0], dst)
        cg.loadValue(ins.Src[1], src)

        cg.asm.CMP(dst, src)
        cg.asm.CSET(dst, cond)
        cg.asm.STR(dst, R29, dstOff)
}

// ---------------------------------------------------------------------------
// Call emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitCall(ins *ir.Instr) {
        if ins.Meta == nil || ins.Meta.FnName == "" {
                return
        }

        // Save caller-saved scratch registers that might hold important values.
        // (In our stack-based model, all values are on the stack, so we don't
        // need to save anything before a call.)

        // Move arguments into X0–X7.
        nArgs := len(ins.Src)
        if nArgs > len(ArgRegs) {
                nArgs = len(ArgRegs) // stack args not yet supported
        }
        for i := 0; i < nArgs; i++ {
                tmp := cg.allocScratch()
                defer cg.freeScratch(tmp)
                cg.loadValue(ins.Src[i], tmp)
                cg.asm.MOV(ArgRegs[i], tmp)
        }

        // Zero unused argument registers (AAPCS64 convention).
        for i := nArgs; i < len(ArgRegs) && i < 8; i++ {
                cg.asm.MOVZ(ArgRegs[i], 0, 0)
        }

        // Branch with link.
        cg.asm.BL(ins.Meta.FnName)

        // Store return value.
        if ins.Dest != nil {
                slot := cg.vregSlot[ins.Dest.ID]
                off := slotOffset(cg.frame, slot)
                cg.asm.STR(R0, R29, off)
        }
}

func (cg *CodeGen) emitCallIndirect(ins *ir.Instr) {
        if len(ins.Src) < 1 {
                return
        }

        // Src[0] is the function pointer, Src[1:] are the arguments.
        fnPtr := ins.Src[0]
        args := ins.Src[1:]

        // Move arguments into X0–X7.
        nArgs := len(args)
        if nArgs > len(ArgRegs) {
                nArgs = len(ArgRegs)
        }
        for i := 0; i < nArgs; i++ {
                tmp := cg.allocScratch()
                defer cg.freeScratch(tmp)
                cg.loadValue(args[i], tmp)
                cg.asm.MOV(ArgRegs[i], tmp)
        }

        // Load function pointer and call.
        fnReg := cg.allocScratch()
        defer cg.freeScratch(fnReg)
        cg.loadValue(fnPtr, fnReg)
        cg.asm.BLR(fnReg)

        // Store return value.
        if ins.Dest != nil {
                slot := cg.vregSlot[ins.Dest.ID]
                off := slotOffset(cg.frame, slot)
                cg.asm.STR(R0, R29, off)
        }
}

// ---------------------------------------------------------------------------
// Memory emission
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitLoad(ins *ir.Instr) {
        if ins.Dest == nil || len(ins.Src) < 1 {
                return
        }

        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        addr := cg.allocScratch()
        defer cg.freeScratch(addr)
        cg.loadValue(ins.Src[0], addr)

        // LDR dst, [addr]
        cg.asm.LDR(R9, addr, 0)
        cg.asm.STR(R9, R29, dstOff)
}

func (cg *CodeGen) emitStore(ins *ir.Instr) {
        if len(ins.Src) < 2 {
                return
        }

        addr := cg.allocScratch()
        defer cg.freeScratch(addr)
        cg.loadValue(ins.Src[0], addr)

        val := cg.allocScratch()
        defer cg.freeScratch(val)
        cg.loadValue(ins.Src[1], val)

        // STR val, [addr]
        cg.asm.STR(val, addr, 0)
}

func (cg *CodeGen) emitAlloc(ins *ir.Instr) {
        if ins.Dest == nil {
                return
        }
        // Delegate to runtime: y_alloc(size) → pointer in X0.
        if ins.Meta != nil && ins.Meta.Size > 0 {
                cg.asm.MOVImm(R0, uint64(ins.Meta.Size))
        } else {
                cg.asm.MOVZ(R0, 0, 0)
        }
        cg.asm.BL("y_alloc")

        slot := cg.vregSlot[ins.Dest.ID]
        off := slotOffset(cg.frame, slot)
        cg.asm.STR(R0, R29, off)
}

func (cg *CodeGen) emitStackAlloc(ins *ir.Instr) {
        if ins.Dest == nil {
                return
        }
        size := 8
        if ins.Meta != nil && ins.Meta.Size > 0 {
                size = ins.Meta.Size
        }
        // Align to 16 bytes.
        size = (size + 15) &^ 15

        // SUB SP, SP, #size
        cg.asm.SUBI(RSP, RSP, size)
        // The result is SP (a pointer to the allocated space).
        slot := cg.vregSlot[ins.Dest.ID]
        off := slotOffset(cg.frame, slot)
        cg.asm.STR(RSP, R29, off)
}

// ---------------------------------------------------------------------------
// Tagged value operations
// ---------------------------------------------------------------------------

func (cg *CodeGen) emitTag(ins *ir.Instr) {
        if ins.Dest == nil || len(ins.Src) < 1 {
                return
        }
        if ins.Meta == nil {
                return
        }

        tag := uint64(ins.Meta.Tag)
        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)
        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)

        // Load the value.
        cg.loadValue(ins.Src[0], dst)

        // Build tagged value: (tag << 56) | (value & 0x00FFFFFFFFFFFFFF)
        // AND dst, dst, #ValueMask (clear top byte)
        // ORR dst, dst, (tag << 56)

        // Clear top byte: AND with 0x00FFFFFFFFFFFFFF
        cg.asm.ANDI(dst, dst, uint64(0x00FFFFFFFFFFFFFF))

        // Set tag byte: ORR with (tag << 56)
        cg.asm.MOVImm(tmp, tag<<56)
        cg.asm.ORR(dst, dst, tmp)

        cg.asm.STR(dst, R29, dstOff)
}

func (cg *CodeGen) emitUntag(ins *ir.Instr) {
        if ins.Dest == nil || len(ins.Src) < 1 {
                return
        }

        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)

        cg.loadValue(ins.Src[0], dst)

        // Extract value: LSR by 56 (for the IR's OpUntag which shifts right by 56).
        // Actually, the Yilt IR uses OpUntag to extract the value portion.
        // Value is in bits 0-55, so we AND with the value mask.
        cg.asm.ANDI(dst, dst, uint64(0x00FFFFFFFFFFFFFF))

        cg.asm.STR(dst, R29, dstOff)
}

func (cg *CodeGen) emitCheckTag(ins *ir.Instr) {
        if ins.Dest == nil || len(ins.Src) < 1 {
                return
        }
        if ins.Meta == nil {
                return
        }

        tag := uint64(ins.Meta.Tag)
        dstSlot := cg.vregSlot[ins.Dest.ID]
        dstOff := slotOffset(cg.frame, dstSlot)

        dst := cg.allocScratch()
        defer cg.freeScratch(dst)
        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)

        cg.loadValue(ins.Src[0], dst)

        // Extract tag: LSR by 56, then compare.
        cg.asm.LSR(dst, dst, 56)
        cg.asm.MOVImm(tmp, tag)
        cg.asm.CMP(dst, tmp)
        cg.asm.CSET(dst, CondEQ)

        cg.asm.STR(dst, R29, dstOff)
}

// ---------------------------------------------------------------------------
// Runtime call helpers
// ---------------------------------------------------------------------------

// emitRuntimeCall emits a call to a runtime function with one argument from Src[0].
func (cg *CodeGen) emitRuntimeCall(ins *ir.Instr, fnName string) {
        if len(ins.Src) < 1 {
                cg.asm.MOVZ(R0, 0, 0)
        } else {
                tmp := cg.allocScratch()
                defer cg.freeScratch(tmp)
                cg.loadValue(ins.Src[0], tmp)
                cg.asm.MOV(R0, tmp)
        }
        cg.asm.BL(fnName)

        if ins.Dest != nil {
                slot := cg.vregSlot[ins.Dest.ID]
                off := slotOffset(cg.frame, slot)
                cg.asm.STR(R0, R29, off)
        }
}

// emitRuntimeCall2 emits a call to a runtime function with two arguments.
func (cg *CodeGen) emitRuntimeCall2(ins *ir.Instr, fnName string) {
        if len(ins.Src) < 1 {
                cg.asm.MOVZ(R0, 0, 0)
        } else {
                tmp := cg.allocScratch()
                defer cg.freeScratch(tmp)
                cg.loadValue(ins.Src[0], tmp)
                cg.asm.MOV(R0, tmp)
        }
        if len(ins.Src) < 2 {
                cg.asm.MOVZ(R1, 0, 0)
        } else {
                tmp := cg.allocScratch()
                defer cg.freeScratch(tmp)
                cg.loadValue(ins.Src[1], tmp)
                cg.asm.MOV(R1, tmp)
        }
        cg.asm.BL(fnName)

        if ins.Dest != nil {
                slot := cg.vregSlot[ins.Dest.ID]
                off := slotOffset(cg.frame, slot)
                cg.asm.STR(R0, R29, off)
        }
}

// emitRuntimeCall0 emits a call to a runtime function with no arguments.
func (cg *CodeGen) emitRuntimeCall0(ins *ir.Instr, fnName string) {
        cg.asm.BL(fnName)

        if ins.Dest != nil {
                slot := cg.vregSlot[ins.Dest.ID]
                off := slotOffset(cg.frame, slot)
                cg.asm.STR(R0, R29, off)
        }
}

// ---------------------------------------------------------------------------
// Constant folding (at emit time)
// ---------------------------------------------------------------------------

func (cg *CodeGen) tryFoldBinOp(ins *ir.Instr) bool {
        s0, s1 := ins.Src[0], ins.Src[1]
        if s0.Const == nil || s1.Const == nil || s0.Const.Kind != ir.VInt || s1.Const.Kind != ir.VInt {
                return false
        }
        a, b := s0.Const.IntVal, s1.Const.IntVal

        var result int64
        switch ins.Op {
        case ir.OpAdd:
                result = a + b
        case ir.OpSub:
                result = a - b
        case ir.OpMul:
                result = a * b
        case ir.OpDiv:
                if b == 0 {
                        return false
                }
                result = a / b
        case ir.OpMod:
                if b == 0 {
                        return false
                }
                result = a % b
        case ir.OpAnd:
                result = a & b
        case ir.OpOr:
                result = a | b
        case ir.OpXor:
                result = a ^ b
        case ir.OpShl:
                result = a << uint64(b&63)
        case ir.OpShr:
                result = a >> uint64(b&63)
        default:
                return false
        }

        // Store the folded result directly.
        slot := cg.vregSlot[ins.Dest.ID]
        off := slotOffset(cg.frame, slot)

        tmp := cg.allocScratch()
        defer cg.freeScratch(tmp)
        cg.asm.MOVImm(tmp, uint64(result))
        cg.asm.STR(tmp, R29, off)
        return true
}

// ---------------------------------------------------------------------------
// Value loading / scratch register management
// ---------------------------------------------------------------------------

// loadValue loads the value of an IR Value into a physical register.
// It first checks if the value is a compile-time constant (inline it).
// Otherwise it loads from the stack slot.
func (cg *CodeGen) loadValue(v *ir.Value, dst Reg) {
        if v == nil {
                cg.asm.MOVZ(dst, 0, 0)
                return
        }

        // Fast path: compile-time constant.
        if v.Const != nil {
                switch v.Const.Kind {
                case ir.VInt:
                        cg.asm.MOVImm(dst, uint64(v.Const.IntVal))
                        return
                case ir.VUint:
                        cg.asm.MOVImm(dst, v.Const.UintVal)
                        return
                case ir.VBool:
                        if v.Const.BoolVal {
                                cg.asm.MOVZ(dst, 1, 0)
                        } else {
                                cg.asm.MOVZ(dst, 0, 0)
                        }
                        return
                case ir.VVoid:
                        cg.asm.MOVZ(dst, 0, 0)
                        return
                }
        }

        // Load from stack slot.
        slot, ok := cg.vregSlot[v.ID]
        if !ok {
                // No slot assigned — value is undefined, zero it.
                cg.asm.MOVZ(dst, 0, 0)
                return
        }
        off := slotOffset(cg.frame, slot)
        cg.asm.LDR(dst, R29, off)
}

// allocScratch returns a scratch register from the pool.
func (cg *CodeGen) allocScratch() Reg {
        if cg.scratchUsed < len(cg.scratchPool) {
                r := cg.scratchPool[cg.scratchUsed]
                cg.scratchUsed++
                return r
        }
        // Out of scratch registers.  This shouldn't happen with 7 available
        // if we free them promptly, but as a fallback, reuse R15 (last one).
        return R15
}

// freeScratch returns a scratch register to the pool.
func (cg *CodeGen) freeScratch(r Reg) {
        if cg.scratchUsed > 0 && cg.scratchPool[cg.scratchUsed-1] == r {
                cg.scratchUsed--
        }
}

// ---------------------------------------------------------------------------
// Frame offset computation
// ---------------------------------------------------------------------------

// slotOffset computes the FP-relative byte offset for a stack slot.
// Slot 0 is the first local variable, at [FP, #-24] if there's one
// callee-saved reg (X28), or at [FP, #-16] if none.
func slotOffset(frame *FrameLayout, slot int) int {
        calleeBytes := len(frame.CalleeSavedRegs) * 8
        return -(16 + calleeBytes + slot*8)
}

// ---------------------------------------------------------------------------
// Pre-indexed / post-indexed STP and LDP helpers
// ---------------------------------------------------------------------------

// emitSTPPreIndex emits: STP Rt, Rt2, [Rn, #imm]!
// Encoding (64-bit, pre-index):
//
//      1x 101 0 0 1 1 imm7 Rt2 Rn Rt
func (cg *CodeGen) emitSTPPreIndex(rt, rt2, rn Reg, imm int) {
        imm7 := uint32((imm / 8) & 0x7F)
        cg.asm.emit(0xA9BC0000 | imm7<<15 | encodeRt2(rt2) | encodeRn(rn) | encodeRd(rt))
}

// emitLDPPostIndex emits: LDP Rt, Rt2, [Rn], #imm
// Encoding (64-bit, post-index):
//
//      1x 101 0 0 0 1 imm7 Rt2 Rn Rt
func (cg *CodeGen) emitLDPPostIndex(rt, rt2, rn Reg, imm int) {
        imm7 := uint32((imm / 8) & 0x7F)
        cg.asm.emit(0xA8C00000 | imm7<<15 | encodeRt2(rt2) | encodeRn(rn) | encodeRd(rt))
}

// ---------------------------------------------------------------------------
// String representation (for debugging)
// ---------------------------------------------------------------------------

// DisassembleFunc returns a human-readable disassembly of the generated code.
func (cg *CodeGen) DisassembleFunc(fn *ir.Func) string {
        code := cg.GenerateFunc(fn)
        if code == nil {
                return fmt.Sprintf("; %s: external or empty\n", fn.Name)
        }

        var sb strings.Builder
        sb.WriteString(fmt.Sprintf("; %s (%d bytes):\n", fn.Name, len(code)))
        for i := 0; i < len(code); i += 4 {
                if i+4 > len(code) {
                        break
                }
                word := uint32(code[i]) | uint32(code[i+1])<<8 | uint32(code[i+2])<<16 | uint32(code[i+3])<<24
                sb.WriteString(fmt.Sprintf("  %04x: %08x\n", i, word))
        }
        return sb.String()
}

// ---------------------------------------------------------------------------
// Module-level generation (for future use)
// ---------------------------------------------------------------------------

// Relocation represents a linker relocation for the AArch64 backend.
type Relocation struct {
        Offset int
        Symbol string
        Type   string // "R_AARCH64_CALL26", etc.
        Addend int32
}

// GenerateModule compiles all functions in an IR module into AArch64 machine
// code.  Returns the combined text section, and relocations for the linker.
func GenerateModule(module *ir.Module) ([]byte, []Relocation) {
        cg := NewCodeGen()
        var relocations []Relocation
        var textSection []byte

        // Register runtime symbol references.
        for _, sym := range module.RuntimeSyms {
                relocations = append(relocations, Relocation{
                        Symbol: sym.Name,
                        Type:   "R_AARCH64_CALL26",
                })
        }

        // Compile each function.
        for _, fn := range module.Funcs {
                if fn.Extern || len(fn.Blocks) == 0 {
                        continue
                }
                code := cg.GenerateFunc(fn)
                textSection = append(textSection, code...)
        }

        return textSection, relocations
}
