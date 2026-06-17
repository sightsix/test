package runtime

import "github.com/yilt/yiltc/internal/codegen/x86_64"

// genPure_EnumEq: y_enum_eq(a: RDI, b: RSI) → tagged_bool (RAX)
// Structural equality for enum values (table-backed).
// Compares _v (variant index) fields, then _p (payload) fields.
func genPure_EnumEq(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_enum_eq", rd)
        a := fb.a

        _v_str := "_v"
        _p_str := "_p"

        // Ensure strings are in rodata for emitLEA_RodataStr lookups
        rd.add(_v_str)
        rd.add(_p_str)

        // Fast path: same pointer → equal
        diffLabel := a.GenLabel("eeq_diff")
        a.CmpRR(x86_64.RDI, x86_64.RSI)
        a.JNE(diffLabel)
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.RET()

        a.Label(diffLabel)

        // Save callee-saved regs and original args
        a.PUSH(x86_64.R15)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.RBP)
        a.PUSH(x86_64.RDI) // [RSP+0]  = 'b'
        a.PUSH(x86_64.RSI) // [RSP+8]  = wait, push order...

        // Build "_v" key once, reuse for both lookups
        fb.emitLEA_RodataStr(x86_64.RDI, _v_str)
        a.MovRM(x86_64.RSI, 2) // len("_v") = 2
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.MovRR(x86_64.R12, x86_64.RAX) // R12 = "_v" key

        // Get a._v (saved at [RSP+0] — RSI was pushed last)
        a.MovMemR(x86_64.RDI, x86_64.MemBase(x86_64.RSP, 0))
        a.MovRR(x86_64.RSI, x86_64.R12)
        a.CALL("y_tab_get")
        fb.addRelocText("y_tab_get")
        a.MovRR(x86_64.R13, x86_64.RAX) // R13 = a._v

        // Get b._v (saved at [RSP+8] — RDI was pushed before RSI)
        a.MovMemR(x86_64.RDI, x86_64.MemBase(x86_64.RSP, 8))
        a.MovRR(x86_64.RSI, x86_64.R12)
        a.CALL("y_tab_get")
        fb.addRelocText("y_tab_get")
        // RAX = b._v

        // If _v not equal → return false
        retFalseLabel := a.GenLabel("eeq_ret_false")
        a.CmpRR(x86_64.R13, x86_64.RAX)
        a.JNE(retFalseLabel)

        // _v equal. Get a._p
        fb.emitLEA_RodataStr(x86_64.RDI, _p_str)
        a.MovRM(x86_64.RSI, 2) // len("_p") = 2
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = "_p" key

        a.MovMemR(x86_64.RDI, x86_64.MemBase(x86_64.RSP, 0))
        a.MovRR(x86_64.RSI, x86_64.R14)
        a.CALL("y_tab_get")
        fb.addRelocText("y_tab_get")
        a.MovRR(x86_64.R15, x86_64.RAX) // R15 = a._p

        // Get b._p
        a.MovMemR(x86_64.RDI, x86_64.MemBase(x86_64.RSP, 8))
        a.MovRR(x86_64.RSI, x86_64.R14)
        a.CALL("y_tab_get")
        fb.addRelocText("y_tab_get")
        // RAX = b._p

        // Compare _p via y_val_eq (pointer identity for heap, value for immediate)
        a.MovRR(x86_64.RDI, x86_64.R15)
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.CALL("y_val_eq")
        fb.addRelocText("y_val_eq")
        // RAX = result — fall through to cleanup

        cleanupLabel := a.GenLabel("eeq_cleanup")
        a.JMP(cleanupLabel)

        a.Label(retFalseLabel)
        a.MovZeroR64(x86_64.RAX)

        a.Label(cleanupLabel)
        // Restore: pop saved 'a' and 'b' then callee-saved regs
        a.AddRI(x86_64.RSP, 16) // pop saved RDI, RSI
        a.POP(x86_64.RBP)
        a.POP(x86_64.R12)
        a.POP(x86_64.R13)
        a.POP(x86_64.R14)
        a.POP(x86_64.R15)
        a.RET()

        return fb.finalize()
}
