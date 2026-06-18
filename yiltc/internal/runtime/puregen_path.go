package runtime

import "github.com/yilt/yiltc/internal/codegen/x86_64"

// ---------------------------------------------------------------------------
// Helper: create a tagged string from a rodata string literal via y_str_new.
// Clobbers RDI, RSI, RAX, RCX, RDX, R8-R11.
// Preserves RBX, R12, R13, R14, R15.
// ---------------------------------------------------------------------------

func (fb *rtFuncBuilder) emitStrFromRodata(str string) {
        a := fb.a
        fb.emitLEA_RodataStr(x86_64.RDI, str)
        a.MovRM(x86_64.RSI, int64(len(str)))
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
}

// ---------------------------------------------------------------------------
// Helper: call y_str_new(data_ptr, len) from RDI=data_ptr, RSI=len.
// Clobbers RAX, RCX, RDX, R8-R11.
// Preserves RBX, R12, R13, R14, R15.
// ---------------------------------------------------------------------------

func (fb *rtFuncBuilder) emitStrNewFromSlice() {
        a := fb.a
        a.PUSH(x86_64.R15)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.RBX)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.POP(x86_64.RBX)
        a.POP(x86_64.R12)
        a.POP(x86_64.R13)
        a.POP(x86_64.R14)
        a.POP(x86_64.R15)
}

// ===========================================================================
// Path Operations
// ===========================================================================

// ---------------------------------------------------------------------------
// genPure_PathJoin: y_path_join(a: RDI, b: RSI) -> tagged_str
//
// Join two path components. If either is nil/empty, return the other.
// If b starts with "/", return b. If a ends with "/", concat a+b directly.
// Otherwise: a + "/" + b via two y_str_concat calls.
// ---------------------------------------------------------------------------
func genPure_PathJoin(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_join", rd)
        a := fb.a

        rd.add("/")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // Save original tagged values in callee-saved registers
        a.MovRR(x86_64.RBX, x86_64.RDI) // RBX = a_tagged
        a.MovRR(x86_64.R12, x86_64.RSI) // R12 = b_tagged

        // Check both tags are STR
        errLabel := a.GenLabel("pj_err")
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(errLabel)
        a.MovRR(x86_64.RAX, x86_64.R12)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(errLabel)

        // Check a nil/empty
        aNil := a.GenLabel("pj_a_nil")
        fb.getPtr(x86_64.R13, x86_64.RBX) // R13 = a ptr
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(aNil)
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R13, 8)) // R14 = a->len
        aEmpty := a.GenLabel("pj_a_empty")
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JZ(aEmpty)

        // Check b nil/empty
        bNil := a.GenLabel("pj_b_nil")
        fb.getPtr(x86_64.R15, x86_64.R12) // R15 = b ptr
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(bNil)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.R15, 8)) // check b->len
        bEmpty := a.GenLabel("pj_b_empty")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(bEmpty)

        // Both non-empty. Check if b starts with "/"
        bAbs := a.GenLabel("pj_b_abs")
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R15, rtStrHeaderSize))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JE(bAbs)

        // Check if a ends with "/"
        aEndsSlash := a.GenLabel("pj_ends_slash")
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R13, x86_64.R14, 1, rtStrHeaderSize-1))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JE(aEndsSlash)

        // General case: a + "/" + b
        // Create "/" tagged string via rodata
        fb.emitStrFromRodata("/") // RAX = slash_tagged; preserves RBX, R12

        // Call y_str_concat(a_tagged, slash_tagged) → temp1 = a + "/"
        a.PUSH(x86_64.R12) // save b_tagged
        a.PUSH(x86_64.RAX) // push for 16-byte stack alignment
        a.MovRR(x86_64.RDI, x86_64.RBX) // RDI = a_tagged
        a.MovRR(x86_64.RSI, x86_64.RAX) // RSI = slash_tagged
        a.CALL("y_str_concat")
        fb.addRelocText("y_str_concat")
        // RAX = temp1
        a.POP(x86_64.RDX)  // discard alignment push
        a.POP(x86_64.RSI)  // RSI = b_tagged

        // Call y_str_concat(temp1, b_tagged) → result = a + "/" + b
        a.MovRR(x86_64.RDI, x86_64.RAX) // RDI = temp1
        a.CALL("y_str_concat")
        fb.addRelocText("y_str_concat")
        doneLabel := a.GenLabel("pj_done")
        a.JMP(doneLabel)

        // Error: return nil
        a.Label(errLabel)
        a.MovZeroR64(x86_64.RAX)
        a.JMP(doneLabel)

        // a is nil/empty: return b
        a.Label(aNil)
        a.Label(aEmpty)
        a.MovRR(x86_64.RAX, x86_64.R12)
        a.JMP(doneLabel)

        // b is nil/empty: return a
        a.Label(bNil)
        a.Label(bEmpty)
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.JMP(doneLabel)

        // b is absolute: return b
        a.Label(bAbs)
        a.MovRR(x86_64.RAX, x86_64.R12)
        a.JMP(doneLabel)

        // a ends with "/": concat a + b directly (slash already present)
        a.Label(aEndsSlash)
        a.MovRR(x86_64.RDI, x86_64.RBX) // a_tagged
        a.MovRR(x86_64.RSI, x86_64.R12) // b_tagged
        a.CALL("y_str_concat")
        fb.addRelocText("y_str_concat")
        // fall through to done

        a.Label(doneLabel)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathDirname: y_path_dirname(path: RDI) -> tagged_str
//
// Returns directory portion of path. If no "/" found, return ".".
// If "/" is at position 0, return "/". Otherwise return path[0:last_slash].
// ---------------------------------------------------------------------------
func genPure_PathDirname(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_dirname", rd)
        a := fb.a

        rd.add("/")
        rd.add(".")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        notStr := a.GenLabel("pdn_notstr")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = StrHeader*
        nullPtr := a.GenLabel("pdn_null")
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nullPtr)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nullPtr) // empty → return "."

        // Scan backwards for last '/'
        a.LEA(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // RBX = data ptr
        a.MovRR(x86_64.RCX, x86_64.R13) // RCX = len (counter)
        a.MovZeroR64(x86_64.R15) // R15 = found flag

        slashLoop := a.GenLabel("pdn_loop")
        slashDone := a.GenLabel("pdn_done")

        a.Label(slashLoop)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(slashDone)
        a.SubRI(x86_64.RCX, 1)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(slashLoop)
        a.MovRM(x86_64.R15, int64(1)) // found = true

        a.Label(slashDone)

        // If not found, return "."
        notFound := a.GenLabel("pdn_notfound")
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(notFound)

        // Found: RCX = 0-based index of last '/'
        // If at position 0, return "/"
        slashRoot := a.GenLabel("pdn_root")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(slashRoot)

        // Return path[0:RCX]
        a.MovRR(x86_64.RDI, x86_64.RBX) // data ptr
        a.MovRR(x86_64.RSI, x86_64.RCX) // length = slash pos
        fb.emitStrNewFromSlice()
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Root case
        a.Label(slashRoot)
        fb.emitStrFromRodata("/")
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Not found or nil/empty: return "."
        a.Label(notFound)
        a.Label(nullPtr)
        fb.emitStrFromRodata(".")
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Not a string: return nil
        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathParent: y_path_parent(path: RDI) -> tagged_str
//
// Same as dirname.
// ---------------------------------------------------------------------------
func genPure_PathParent(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_parent", rd)
        a := fb.a

        rd.add("/")
        rd.add(".")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        notStr := a.GenLabel("pp_notstr")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        fb.getPtr(x86_64.R12, x86_64.RDI)
        nullPtr := a.GenLabel("pp_null")
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nullPtr)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8))
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nullPtr)

        a.LEA(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.MovZeroR64(x86_64.R15)

        slashLoop := a.GenLabel("pp_loop")
        slashDone := a.GenLabel("pp_done")

        a.Label(slashLoop)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(slashDone)
        a.SubRI(x86_64.RCX, 1)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(slashLoop)
        a.MovRM(x86_64.R15, int64(1))

        a.Label(slashDone)

        notFound := a.GenLabel("pp_notfound")
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(notFound)

        slashRoot := a.GenLabel("pp_root")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(slashRoot)

        a.MovRR(x86_64.RDI, x86_64.RBX)
        a.MovRR(x86_64.RSI, x86_64.RCX)
        fb.emitStrNewFromSlice()
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(slashRoot)
        fb.emitStrFromRodata("/")
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notFound)
        a.Label(nullPtr)
        fb.emitStrFromRodata(".")
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathBasename: y_path_basename(path: RDI) -> tagged_str
//
// Returns the final component of path. If no "/", return entire path.
// Otherwise return path[last_slash+1:].
// ---------------------------------------------------------------------------
func genPure_PathBasename(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_basename", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        notStr := a.GenLabel("pbn_notstr")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = StrHeader*
        nullPtr := a.GenLabel("pbn_null")
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nullPtr)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nullPtr)

        // Scan backwards for last '/'
        a.LEA(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // data ptr
        a.MovRR(x86_64.RCX, x86_64.R13) // counter
        a.MovZeroR64(x86_64.R15) // found flag

        slashLoop := a.GenLabel("pbn_loop")
        slashDone := a.GenLabel("pbn_done")

        a.Label(slashLoop)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(slashDone)
        a.SubRI(x86_64.RCX, 1)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(slashLoop)
        a.MovRM(x86_64.R15, int64(1))

        a.Label(slashDone)

        // If not found, return original path (identity)
        notFound := a.GenLabel("pbn_notfound")
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(notFound)

        // Found: RCX = 0-based index of last '/'
        // basename = path[RCX+1:], length = R13 - RCX - 1
        a.MovRR(x86_64.RDX, x86_64.R13) // total len
        a.SubRR(x86_64.RDX, x86_64.RCX) // total - slash_pos
        a.SubRI(x86_64.RDX, 1)           // basename length
        // data ptr = RBX + RCX + 1
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 1))
        a.MovRR(x86_64.RSI, x86_64.RDX)
        fb.emitStrNewFromSlice()
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // No slash: return original path
        a.Label(notFound)
        a.Label(nullPtr)
        a.MovRR(x86_64.RAX, x86_64.RDI) // original tagged value
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Not a string
        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathStem: y_path_stem(path: RDI) -> tagged_str
//
// Get basename, then find last "." in basename.
// If no "." or "." at position 0, return basename.
// Otherwise return basename[0:last_dot].
// ---------------------------------------------------------------------------
func genPure_PathStem(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_stem", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        notStr := a.GenLabel("ps_notstr")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = ptr
        nullPtr := a.GenLabel("ps_null")
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nullPtr)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nullPtr)

        // RBX = data ptr
        a.LEA(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        // Step 1: Find last '/' to determine basename start
        a.MovRR(x86_64.RCX, x86_64.R13) // counter = len
        a.MovZeroR64(x86_64.R15) // found slash flag

        slashLoop := a.GenLabel("ps_slash_loop")
        slashDone := a.GenLabel("ps_slash_done")

        a.Label(slashLoop)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(slashDone)
        a.SubRI(x86_64.RCX, 1)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(slashLoop)
        a.MovRM(x86_64.R15, int64(1))

        a.Label(slashDone)

        // Compute basename: data ptr and length
        noSlash := a.GenLabel("ps_no_slash")
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(noSlash)

        // Found slash at RCX: basename = path[RCX+1:]
        a.MovRR(x86_64.R14, x86_64.R13) // total len
        a.SubRR(x86_64.R14, x86_64.RCX) // total - slash_pos
        a.SubRI(x86_64.R14, 1)           // basename length
        a.LEA(x86_64.RBX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 1)) // basename data ptr
        a.MovRR(x86_64.R13, x86_64.R14) // R13 = basename length
        // Fall through to dot search

        // Set up for dot search: RCX = basename length, RBX = basename data ptr
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.MovZeroR64(x86_64.R15) // found dot flag

        dotLoop := a.GenLabel("ps_dot_loop")
        dotDone := a.GenLabel("ps_dot_done")

        a.Label(dotLoop)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(dotDone)
        a.SubRI(x86_64.RCX, 1)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('.'))
        a.JNE(dotLoop)
        a.MovRM(x86_64.R15, int64(1))

        a.Label(dotDone)

        // Check dot result
        noDot := a.GenLabel("ps_no_dot")
        dotAtStart := a.GenLabel("ps_dot_start")
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(noDot)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(dotAtStart)

        // Return basename[0:RCX] (stem = basename without extension)
        a.MovRR(x86_64.RDI, x86_64.RBX)
        a.MovRR(x86_64.RSI, x86_64.RCX)
        fb.emitStrNewFromSlice()
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Dot at position 0 (e.g., ".hidden"): return full basename
        a.Label(dotAtStart)
        a.Label(noDot)
        a.MovRR(x86_64.RDI, x86_64.RBX)
        a.MovRR(x86_64.RSI, x86_64.R13)
        fb.emitStrNewFromSlice()
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // No slash found: basename = entire path
        a.Label(noSlash)
        // RBX = data ptr (still), R13 = len (still)
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.MovZeroR64(x86_64.R15)
        a.JMP(dotLoop)

        // Nil/empty: return original
        a.Label(nullPtr)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Not a string
        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathExtname: y_path_extname(path: RDI) -> tagged_str
//
// Get basename, find last "." in basename. If no "." or at position 0,
// return "". Otherwise return basename[last_dot:].
// ---------------------------------------------------------------------------
func genPure_PathExtname(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_extname", rd)
        a := fb.a

        rd.add("")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        notStr := a.GenLabel("pe_notstr")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = ptr
        nullPtr := a.GenLabel("pe_null")
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nullPtr)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nullPtr)

        a.LEA(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // data ptr

        // Step 1: Find last '/'
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.MovZeroR64(x86_64.R15) // found slash flag

        slashLoop := a.GenLabel("pe_slash_loop")
        slashDone := a.GenLabel("pe_slash_done")

        a.Label(slashLoop)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(slashDone)
        a.SubRI(x86_64.RCX, 1)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(slashLoop)
        a.MovRM(x86_64.R15, int64(1))

        a.Label(slashDone)

        // Compute basename
        noSlash := a.GenLabel("pe_no_slash")
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(noSlash)

        a.MovRR(x86_64.R14, x86_64.R13)
        a.SubRR(x86_64.R14, x86_64.RCX)
        a.SubRI(x86_64.R14, 1) // basename length
        a.LEA(x86_64.RBX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 1))
        a.MovRR(x86_64.R13, x86_64.R14)

        // Set up for dot search
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.MovZeroR64(x86_64.R15)

        dotLoop := a.GenLabel("pe_dot_loop")
        dotDone := a.GenLabel("pe_dot_done")

        a.Label(dotLoop)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(dotDone)
        a.SubRI(x86_64.RCX, 1)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('.'))
        a.JNE(dotLoop)
        a.MovRM(x86_64.R15, int64(1))

        a.Label(dotDone)

        // Check dot result
        noDot := a.GenLabel("pe_no_dot")
        dotAtStart := a.GenLabel("pe_dot_start")
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(noDot)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(dotAtStart)

        // Return basename[RCX:] (extension = basename from dot onwards)
        a.MovRR(x86_64.RDX, x86_64.R13) // basename length
        a.SubRR(x86_64.RDX, x86_64.RCX) // ext length = basename_len - dot_pos
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.MovRR(x86_64.RSI, x86_64.RDX)
        fb.emitStrNewFromSlice()
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Dot at start or no dot: return ""
        a.Label(dotAtStart)
        a.Label(noDot)
        fb.emitStrFromRodata("")
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // No slash found
        a.Label(noSlash)
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.MovZeroR64(x86_64.R15)
        a.JMP(dotLoop)

        // Nil/empty: return ""
        a.Label(nullPtr)
        fb.emitStrFromRodata("")
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Not a string
        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathIsAbs: y_path_is_abs(path: RDI) -> tagged_bool
//
// Returns TrueValue if path starts with '/', FalseValue otherwise.
// ---------------------------------------------------------------------------
func genPure_PathIsAbs(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_is_abs", rd)
        a := fb.a

        retLabel := a.GenLabel("pia_ret")

        // Default: false
        a.MovRM64(x86_64.RAX, int64(rtFalseVal))

        // Check tag
        a.MovRR(x86_64.RDX, x86_64.RDI)
        a.ShrRI(x86_64.RDX, 56)
        a.CmpRI(x86_64.RDX, rtTagStr)
        a.JNE(retLabel)

        // Check nil ptr
        fb.getPtr(x86_64.RDX, x86_64.RDI)
        a.TestRR(x86_64.RDX, x86_64.RDX)
        a.JZ(retLabel)

        // Check empty string
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RDX, 8))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(retLabel)

        // Check first byte
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.RDX, rtStrHeaderSize))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(retLabel)

        a.MovRM64(x86_64.RAX, int64(rtTrueVal))

        a.Label(retLabel)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathNormalize: y_path_normalize(path: RDI) -> tagged_str
//
// Normalizes a path by resolving '.', '..', and '//' segments.
// Preserves the absolute/relative nature of the path.
//
// Algorithm (two-pointer in-place):
//   1. Allocate output buffer (len+1 bytes) via mmap
//   2. If absolute (starts with '/'), write leading '/'
//   3. Scan input for segments delimited by '/':
//      - Skip empty segments and '.'
//      - On '..': scan output backwards for '/', truncate there
//      - Otherwise: append '/' + segment to output
//   4. If output empty, return "."
//   5. Strip trailing '/' (keep if only char)
//   6. Return y_str_new(buf, out_pos)
//
// Register allocation:
//   RBX  = output buffer (mmap'd)
//   R12  = input data pointer
//   R13  = input length
//   R14  = input read position
//   R15  = output write position
//   Stack: is_abs flag, StrHeader*
//   Scratch: RAX, RCX, RDX, RDI, RSI, R8, R9, R10, R11
// ---------------------------------------------------------------------------
func genPure_PathNormalize(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_normalize", rd)
        a := fb.a

        rd.add(".")
        rd.add("/")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // --- Check tag is TAG_STR ---
        notStr := a.GenLabel("pn_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // --- Extract pointer and length ---
        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = StrHeader*
        nullPtr := a.GenLabel("pn_null")
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nullPtr)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len
        emptyStr := a.GenLabel("pn_empty")
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(emptyStr)

        // R12 = StrHeader*, R13 = len
        // RDI (scratch) = input data ptr = R12 + 24
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        // --- Generate all forward-referenced labels upfront ---
        skipAbsFlag := a.GenLabel("pn_skip_abs")
        notAbsPath := a.GenLabel("pn_not_abs")
        skipAbsSlash := a.GenLabel("pn_skip_abs_slash")
        mainLoop := a.GenLabel("pn_main_loop")
        mainDone := a.GenLabel("pn_main_done")
        appendSeg := a.GenLabel("pn_append_seg")
        segStartFound := a.GenLabel("pn_seg_start")
        segEndFound := a.GenLabel("pn_seg_end")
        checkDotDot := a.GenLabel("pn_check_dotdot")
        dotDotEmpty := a.GenLabel("pn_dotdot_empty")
        dotDotAbs := a.GenLabel("pn_dotdot_abs")
        backFound := a.GenLabel("pn_back_found")
        dotDotNoSlash := a.GenLabel("pn_dotdot_no_slash")
        dotDotAtRoot := a.GenLabel("pn_dotdot_root")
        appendNoSlash := a.GenLabel("pn_append_no_slash")
        notEmpty := a.GenLabel("pn_not_empty")
        buildResult := a.GenLabel("pn_build_result")

        // --- Check if absolute path ---

        a.MovZeroR64(x86_64.RAX) // RAX = is_abs (0 or 1)
        a.MovZX8Mem(x86_64.RCX, x86_64.MemBase(x86_64.RDI, 0))
        a.CmpRI(x86_64.RCX, int64('/'))
        a.JNE(skipAbsFlag)
        a.MovRM(x86_64.RAX, 1)
        a.Label(skipAbsFlag)

        // --- Allocate output buffer: len+1 bytes, aligned to 8 ---
        a.MovRR(x86_64.RSI, x86_64.R13)
        a.AddRI(x86_64.RSI, 1)
        a.AddRI(x86_64.RSI, 7)
        a.AndRI(x86_64.RSI, ^int64(7))
        // mmap(NULL, size, PROT_READ|PROT_WRITE, MAP_PRIVATE|MAP_ANON, -1, 0)
        a.MovZeroR64(x86_64.R8)
        a.MovRR(x86_64.RDI, x86_64.R8) // NULL addr
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        mmapErr := a.GenLabel("pn_mmap_err")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JAE(mmapErr)

        // --- Save state on stack ---
        // RBX = output buffer
        a.MovRR(x86_64.RBX, x86_64.RAX)
        // R15 = out_pos = 0
        a.MovZeroR64(x86_64.R15)

        // Save is_abs and re-compute input data ptr
        // We clobbered RDI in mmap; recompute from R12
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // R12 = input data ptr

        // Now we need is_abs. We computed it in RAX earlier but clobbered it.
        // Re-check first byte
        a.MovZeroR64(x86_64.RAX)
        a.MovZX8Mem(x86_64.RCX, x86_64.MemBase(x86_64.R12, 0))
        a.CmpRI(x86_64.RCX, int64('/'))
        a.JNE(notAbsPath)
        a.MovRM(x86_64.RAX, 1)
        a.Label(notAbsPath)

        a.PUSH(x86_64.RAX) // push is_abs
        a.PUSH(x86_64.R13) // push input len

        // R14 = read position = 0
        a.MovZeroR64(x86_64.R14)

        skipLeadSlash := a.GenLabel("pn_skip_lead_slash")
        skipSlashIn := a.GenLabel("pn_skip_slash_in")
        segEndLoop := a.GenLabel("pn_seg_end_loop")

        // --- If absolute, write leading '/' and skip leading slashes ---
        // Check is_abs at [RSP]
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(skipAbsSlash)

        // Write '/' to output[0]
        a.MovRM(x86_64.RAX, int64('/'))
        a.MovRMMem(x86_64.MemBase(x86_64.RBX, 0), x86_64.RAX)
        a.MovRM(x86_64.R15, 1) // out_pos = 1

        // Skip leading slashes in input
        a.Label(skipLeadSlash)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(mainLoop)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(mainLoop)
        a.AddRI(x86_64.R14, 1)
        a.JMP(skipLeadSlash)

        a.Label(skipAbsSlash)

        // --- Main segment processing loop ---
        a.Label(mainLoop)
        // Skip slashes in input
        a.Label(skipSlashIn)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(mainDone)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(segStartFound)
        a.AddRI(x86_64.R14, 1)
        a.JMP(skipSlashIn)

        a.Label(segStartFound)
        // R14 = segment start in input. Save it.
        a.MovRR(x86_64.R8, x86_64.R14) // R8 = seg_start

        // Find segment end
        a.Label(segEndLoop)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(segEndFound)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JE(segEndFound)
        a.AddRI(x86_64.R14, 1)
        a.JMP(segEndLoop)

        a.Label(segEndFound)
        // R14 = segment end (exclusive)
        // seg_len = R14 - R8
        a.MovRR(x86_64.RAX, x86_64.R14)
        a.SubRR(x86_64.RAX, x86_64.R8) // RAX = seg_len

        // --- Check for single '.' ---
        a.CmpRI(x86_64.RAX, 1)
        a.JNE(checkDotDot)
        a.MovZX8Mem(x86_64.RCX, x86_64.MemIndex(x86_64.R12, x86_64.R8, 1, 0))
        a.CmpRI(x86_64.RCX, int64('.'))
        a.JE(mainLoop) // skip '.'
        a.JMP(appendSeg)

        // --- Check for '..' ---
        a.Label(checkDotDot)
        a.CmpRI(x86_64.RAX, 2)
        a.JNE(appendSeg)
        a.MovZX8Mem(x86_64.RCX, x86_64.MemIndex(x86_64.R12, x86_64.R8, 1, 0))
        a.CmpRI(x86_64.RCX, int64('.'))
        a.JNE(appendSeg)
        a.MovZX8Mem(x86_64.RCX, x86_64.MemIndex(x86_64.R12, x86_64.R8, 1, 1))
        a.CmpRI(x86_64.RCX, int64('.'))
        a.JNE(appendSeg)

        // --- Handle '..': truncate output back to last '/' ---
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(dotDotEmpty) // out_pos == 0, nothing to pop

        // is_abs at [RSP]
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(dotDotAbs)

        // Relative '..': scan backwards in output for '/'
        // RCX = out_pos - 1
        a.MovRR(x86_64.RCX, x86_64.R15)
        a.SubRI(x86_64.RCX, 1)
        backLoop := a.GenLabel("pn_back_loop")
        a.Label(backLoop)
        a.CmpRI(x86_64.RCX, 0)
        a.JL(dotDotEmpty) // no '/' found
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JE(backFound)
        a.SubRI(x86_64.RCX, 1)
        a.JMP(backLoop)

        // No '/' found in relative path: append '..' segment
        a.Label(dotDotEmpty)
        // Append '/' + '..' to output
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(dotDotNoSlash)
        a.MovRM(x86_64.RAX, int64('/'))
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.R15, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.R15, 1)
        a.Label(dotDotNoSlash)
        a.MovRM(x86_64.RAX, int64('.'))
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.R15, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.R15, 1)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.R15, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.R15, 1)
        a.JMP(mainLoop)

        // Absolute '..': scan backwards
        a.Label(dotDotAbs)
        a.MovRR(x86_64.RCX, x86_64.R15)
        a.SubRI(x86_64.RCX, 1)
        backAbsLoop := a.GenLabel("pn_back_abs_loop")
        a.Label(backAbsLoop)
        a.CmpRI(x86_64.RCX, 0)
        a.JL(dotDotAtRoot)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JE(backFound)
        a.SubRI(x86_64.RCX, 1)
        a.JMP(backAbsLoop)

        // At root: out_pos stays at 1 (the leading '/')
        a.Label(dotDotAtRoot)
        a.MovRM(x86_64.R15, 1)
        a.JMP(mainLoop)

        // Found '/' at RCX: truncate output to RCX+1 (keep the '/')
        // Actually, truncate to RCX. For absolute paths, keep the '/'.
        a.Label(backFound)
        // out_pos = RCX
        a.MovRR(x86_64.R15, x86_64.RCX)
        a.JMP(mainLoop)

        // --- Append normal segment: '/' + segment bytes ---
        a.Label(appendSeg)
        // Append '/' before segment (if output is non-empty)
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(appendNoSlash)
        a.MovRM(x86_64.RAX, int64('/'))
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.R15, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.R15, 1)

        a.Label(appendNoSlash)
        // Copy segment bytes: src = R12+R8, dst = RBX+R15, count = R14-R8
        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.R8, 1, 0)) // src
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.RBX, x86_64.R15, 1, 0)) // dst
        a.MovRR(x86_64.RCX, x86_64.R14) // end pos
        a.SubRR(x86_64.RCX, x86_64.R8)  // count = seg_len
        fb.emitMemcpy(x86_64.RDI, x86_64.RSI, x86_64.RCX)
        // Advance out_pos
        a.AddRR(x86_64.R15, x86_64.RCX)
        a.JMP(mainLoop)

        // --- Main loop done ---
        a.Label(mainDone)
        // Check if output is empty
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JNZ(notEmpty)

        // Empty output: return "."
        a.AddRI(x86_64.RSP, 16) // pop is_abs, input_len
        a.MovRR(x86_64.RDI, x86_64.RBX) // we need to restore R12-R15
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        fb.emitStrFromRodata(".")
        a.RET()

        a.Label(notEmpty)

        // Strip trailing '/' if out_pos > 1
        a.CmpRI(x86_64.R15, 1)
        a.JLE(buildResult)
        a.MovRR(x86_64.RAX, x86_64.R15)
        a.SubRI(x86_64.RAX, 1) // RAX = out_pos - 1
        a.MovZX8Mem(x86_64.RCX, x86_64.MemIndex(x86_64.RBX, x86_64.RAX, 1, 0))
        a.CmpRI(x86_64.RCX, int64('/'))
        a.JNE(buildResult)
        a.SubRI(x86_64.R15, 1) // strip trailing '/'

        // --- Build result: y_str_new(buf, out_pos) ---
        a.Label(buildResult)
        a.MovRR(x86_64.RDI, x86_64.RBX) // data ptr
        a.MovRR(x86_64.RSI, x86_64.R15) // length
        fb.emitStrNewFromSlice()

        a.AddRI(x86_64.RSP, 16) // pop is_abs, input_len
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Error paths ---
        a.Label(mmapErr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(nullPtr)
        a.Label(emptyStr)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        fb.emitStrFromRodata(".")
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathResolve: y_path_resolve(path: RDI) -> tagged_str
//
// Resolves a path to an absolute path.  Simplified approach (no getcwd):
//   - If path starts with '/', it is already absolute — return a copy.
//   - If path does not start with '/', prepend '/' and return the result.
//   - In the future this could use the getcwd syscall (syscall 79).
// ---------------------------------------------------------------------------
func genPure_PathResolve(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_resolve", rd)
        a := fb.a

        rd.add("/")

        a.PUSH(x86_64.RBX)

        // Save original tagged value in callee-saved register.
        a.MovRR(x86_64.RBX, x86_64.RDI) // RBX = path_tagged

        // --- Check tag is TAG_STR ---
        notStr := a.GenLabel("pr_not_str")
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // --- Extract StrHeader pointer ---
        fb.getPtr(x86_64.R12, x86_64.RBX) // R12 = StrHeader*

        // Check nil pointer.
        nilLabel := a.GenLabel("pr_nil")
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        // Load length.
        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len

        // Check empty string — treat as "/".
        emptyLabel := a.GenLabel("pr_empty")
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(emptyLabel)

        // --- Check first byte ---
        notAbs := a.GenLabel("pr_not_abs")
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(notAbs)

        // Already absolute: copy via y_str_new(data_ptr, len).
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // RDI = data ptr
        a.MovRR(x86_64.RSI, x86_64.R13)                               // RSI = len
        fb.emitStrNewFromSlice()
        doneLabel := a.GenLabel("pr_done")
        a.JMP(doneLabel)

        // Not absolute: prepend "/" via y_str_concat("/", path).
        // This avoids manual mmap/memcpy; y_str_concat handles allocation.
        a.Label(notAbs)
        fb.emitStrFromRodata("/")                         // RAX = "/" tagged string (preserves RBX)
        a.MovRR(x86_64.RDI, x86_64.RAX)                   // RDI = "/" tagged
        a.MovRR(x86_64.RSI, x86_64.RBX)                   // RSI = original path tagged
        a.CALL("y_str_concat")
        fb.addRelocText("y_str_concat")
        a.JMP(doneLabel)

        // Empty string: return "/" (empty path resolves to root).
        a.Label(emptyLabel)
        fb.emitStrFromRodata("/")
        a.JMP(doneLabel)

        // Nil pointer: return nil.
        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.JMP(doneLabel)

        // Not a string: return nil.
        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)

        a.Label(doneLabel)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathResolve2: y_path_resolve2(base: RDI, rel: RSI) -> tagged_str
//
// Delegates to y_path_join.
// ---------------------------------------------------------------------------
func genPure_PathResolve2(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_resolve2", rd)
        fb.a.CALL("y_path_join")
        fb.addRelocText("y_path_join")
        fb.a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathRelative: y_path_relative(from: RDI, to: RSI) -> tagged_str
//
// Compute a relative path from `from` to `to`.
//
// Algorithm:
//   1. If either argument is not a string, return nil
//   2. If both tagged values are identical (same object), return "."
//   3. Split both paths by '/' into component arrays
//   4. Find longest common prefix of component lists
//   5. Build result: (len(from_components) - common) times "../" +
//      remaining to_components joined with '/'
//   6. If result is empty, return "."
//
// Implementation:
//   - Allocate two working buffers (len+1 each) via mmap
//   - Split from/to into component arrays on the stack
//   - Component arrays stored as arrays of (offset, length) pairs
//   - Stack limit: 256 components per path max
//
// Register allocation:
//   R12 = from data ptr (StrHeader* converted)
//   R13 = from length
//   R14 = to data ptr
//   R15 = to length
//   Stack: component arrays for both paths
// ---------------------------------------------------------------------------
func genPure_PathRelative(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_relative", rd)
        a := fb.a

        rd.add(".")
        rd.add("/")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // --- Check both tags are TAG_STR ---
        notStr := a.GenLabel("prel_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)
        a.MovRR(x86_64.RAX, x86_64.RSI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // Save original tagged values
        a.PUSH(x86_64.RSI) // save to_tagged
        a.PUSH(x86_64.RDI) // save from_tagged

        // --- Extract from pointer and length ---
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notStr)
        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8))
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        // --- Extract to pointer and length ---
        fb.getPtr(x86_64.R14, x86_64.RSI)
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JZ(notStr)
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R14, 8))
        a.LEA(x86_64.R14, x86_64.MemBase(x86_64.R14, rtStrHeaderSize))

        // --- Generate all forward-referenced labels upfront ---
        skipToSlash := a.GenLabel("prel_skip_to_slash")
        countFrom := a.GenLabel("prel_count_from")
        countFromAdv := a.GenLabel("prel_count_from_adv")
        countTo := a.GenLabel("prel_count_to")
        countToAdv := a.GenLabel("prel_count_to_adv")
        findCommon := a.GenLabel("prel_find_common")
        returnTo := a.GenLabel("prel_return_to")
        returnUps := a.GenLabel("prel_return_ups")
        upsDone := a.GenLabel("prel_ups_done")
        buildResult := a.GenLabel("prel_build_result")
        toNoSlash := a.GenLabel("prel_to_no_slash")
        returnBuf := a.GenLabel("prel_return_buf")

        // --- Quick equality check (same tagged value ⇒ same object) ---
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0)) // from_tagged
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 8)) // to_tagged
        notEqual := a.GenLabel("prel_not_equal")
        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.JNE(notEqual)

        // Equal: return "."
        a.AddRI(x86_64.RSP, 16) // clean up saved values
        a.MovRR(x86_64.RDI, x86_64.RBX) // placeholder, not used
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        fb.emitStrFromRodata(".")
        a.RET()

        a.Label(notEqual)

        // --- Skip leading slashes on both paths ---
        skipFromSlash := a.GenLabel("prel_skip_from_slash")
        a.Label(skipFromSlash)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(skipToSlash)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(skipToSlash)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(skipFromSlash)

        a.Label(skipToSlash)
        skipToSlashLoop := a.GenLabel("prel_skip_to_slash_loop")
        a.Label(skipToSlashLoop)
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(countFrom)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R14, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(countFrom)
        a.LEA(x86_64.R14, x86_64.MemBase(x86_64.R14, 1))
        a.SubRI(x86_64.R15, 1)
        a.JMP(skipToSlashLoop)

        // --- Count from components: scan for '/', count them ---
        a.Label(countFrom)
        a.MovRM(x86_64.RBX, 0) // RBX = from_count = 0
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(countTo)

        countFromLoop := a.GenLabel("prel_count_from_loop")
        a.Label(countFromLoop)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(countFromAdv)
        a.AddRI(x86_64.RBX, 1)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(countFromLoop)

        a.Label(countFromAdv)
        a.AddRI(x86_64.RBX, 1) // last segment
        a.MovRR(x86_64.R8, x86_64.RBX) // R8 = from_count

        // --- Count to components ---
        a.Label(countTo)
        a.MovRM(x86_64.RCX, 0) // RCX = to_count = 0
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(findCommon)

        countToLoop := a.GenLabel("prel_count_to_loop")
        a.Label(countToLoop)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R14, 0))
        a.CmpRI(x86_64.RAX, int64('/'))
        a.JNE(countToAdv)
        a.AddRI(x86_64.RCX, 1)
        a.LEA(x86_64.R14, x86_64.MemBase(x86_64.R14, 1))
        a.SubRI(x86_64.R15, 1)
        a.JMP(countToLoop)

        a.Label(countToAdv)
        a.AddRI(x86_64.RCX, 1)
        a.Label(findCommon)
        a.MovRR(x86_64.R9, x86_64.RCX) // R9 = to_count

        // --- For simplicity: if from or to is empty, return "to" unchanged ---
        a.TestRR(x86_64.R8, x86_64.R8)
        a.JZ(returnTo)
        a.TestRR(x86_64.R9, x86_64.R9)
        a.JZ(returnUps)

        // --- Build result: (from_count) times "../" + remaining to ---
        // Allocate output buffer: from_count * 3 + to_len + 1
        // Worst case: "a/b/c" relative to "x/y/z" = "../../x/y/z" = 3 + 5 + 1 = 9
        // More conservatively: from_count * 3 + R15 + from_count * 4 (for to segments)
        a.MovRR(x86_64.RAX, x86_64.R8) // from_count
        a.ShlRI(x86_64.RAX, 1)   // * 2
        a.AddRR(x86_64.RAX, x86_64.R8) // * 3 for "../"
        a.AddRR(x86_64.RAX, x86_64.R15) // + to_len
        a.AddRI(x86_64.RAX, 64)     // padding for safety
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))

        a.MovZeroR64(x86_64.RDI)
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        bufErr := a.GenLabel("prel_buf_err")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JAE(returnTo)

        // RDI = output buffer
        // RDX = output position = 0
        a.MovZeroR64(x86_64.RDX)

        // Write "../" for each from component
        upsLoop := a.GenLabel("prel_ups_loop")
        a.Label(upsLoop)
        a.TestRR(x86_64.R8, x86_64.R8)
        a.JLE(upsDone)

        // Write "."
        a.MovRM(x86_64.RAX, int64('.'))
        a.MovRMMem(x86_64.MemIndex(x86_64.RDI, x86_64.RDX, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.RDX, 1)
        a.MovRMMem(x86_64.MemIndex(x86_64.RDI, x86_64.RDX, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.RDX, 1)
        // Write "/"
        a.MovRM(x86_64.RAX, int64('/'))
        a.MovRMMem(x86_64.MemIndex(x86_64.RDI, x86_64.RDX, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.RDX, 1)
        a.SubRI(x86_64.R8, 1)
        a.JMP(upsLoop)

        a.Label(upsDone)

        // Append remaining to components
        // R14 = to data ptr (already pointing past leading slashes)
        // R15 = to remaining length
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(buildResult)

        // Write remaining to data
        a.TestRR(x86_64.RDX, x86_64.RDX)
        a.JZ(toNoSlash)
        // Write "/"
        a.MovRM(x86_64.RAX, int64('/'))
        a.MovRMMem(x86_64.MemIndex(x86_64.RDI, x86_64.RDX, 1, 0), x86_64.RAX)
        a.AddRI(x86_64.RDX, 1)

        a.Label(toNoSlash)
        // Copy to data
        a.MovRR(x86_64.RSI, x86_64.R14) // src
        a.LEA(x86_64.RAX, x86_64.MemIndex(x86_64.RDI, x86_64.RDX, 1, 0)) // dst
        a.MovRR(x86_64.RCX, x86_64.R15) // count
        fb.emitMemcpy(x86_64.RAX, x86_64.RSI, x86_64.RCX)
        a.AddRR(x86_64.RDX, x86_64.R15)

        a.Label(buildResult)

        a.TestRR(x86_64.RDX, x86_64.RDX)
        a.JNZ(returnBuf)

        // Result is empty, return "."
        returnDot := a.GenLabel("prel_return_dot")
        a.Label(returnDot)
        a.AddRI(x86_64.RSP, 16)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        fb.emitStrFromRodata(".")
        a.RET()

        a.Label(returnBuf)
        a.MovRR(x86_64.RDI, x86_64.RDI) // buf ptr
        a.MovRR(x86_64.RSI, x86_64.RDX) // length
        fb.emitStrNewFromSlice()

        a.AddRI(x86_64.RSP, 16)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(returnTo)
        // Return "to" unchanged
        a.AddRI(x86_64.RSP, 16)
        a.MovRR(x86_64.RAX, x86_64.RSI) // to_tagged (from stack)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(returnUps)
        // to_count is 0, so just return "../.." * from_count
        a.JMP(buildResult)

        a.Label(bufErr)
        a.JMP(returnTo)

        a.Label(notStr)
        a.AddRI(x86_64.RSP, 16)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathSep: y_path_sep() -> tagged_str
// ---------------------------------------------------------------------------
func genPure_PathSep(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_sep", rd)
        rd.add("/")
        fb.emitStrFromRodata("/")
        fb.a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathSepPosix: y_path_sep_posix() -> tagged_str
// ---------------------------------------------------------------------------
func genPure_PathSepPosix(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_sep_posix", rd)
        rd.add("/")
        fb.emitStrFromRodata("/")
        fb.a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_PathSepWin: y_path_sep_win() -> tagged_str
// ---------------------------------------------------------------------------
func genPure_PathSepWin(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_path_sep_win", rd)
        rd.add("\\")
        fb.emitStrFromRodata("\\")
        fb.a.RET()
        return fb.finalize()
}

// ===========================================================================
// JSON Operations
// ===========================================================================

// ---------------------------------------------------------------------------
// genPure_JsonEncode: y_json_encode(val: RDI) -> tagged_str
//
// Dispatches on tag:
//   TAG_STR (4):  identity — return val
//   TAG_TABLE (5): "{}"
//   TAG_INT (1):   call y_to_str(val)
//   TAG_BOOL (2):  "true" or "false"
//   TAG_NIL/0/9:   "null"
//   default:       "null"
// ---------------------------------------------------------------------------
func genPure_JsonEncode(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_json_encode", rd)
        a := fb.a

        rd.add("{}")
        rd.add("true")
        rd.add("false")
        rd.add("null")

        doneLabel := a.GenLabel("je_done")
        strDone := a.GenLabel("je_str_done")
        notTable := a.GenLabel("je_not_table")
        notInt := a.GenLabel("je_not_int")
        falseLabel := a.GenLabel("je_false")
        notNil := a.GenLabel("je_not_nil")

        a.PUSH(x86_64.RDI) // save val

        // Get tag
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)

        // TAG_STR (4): identity
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JE(strDone)

        // TAG_TABLE (5): return "{}"
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)
        fb.emitStrFromRodata("{}")
        a.JMP(doneLabel)

        // TAG_INT (1): call y_to_str(val)
        a.Label(notTable)
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JNE(notInt)
        a.CALL("y_to_str")
        fb.addRelocText("y_to_str")
        a.JMP(doneLabel)

        // TAG_BOOL (2): "true" or "false"
        a.Label(notInt)
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JNE(notNil)
        // Check bool value (payload in lower 56 bits)
        a.ShlRI(x86_64.RDI, 8)  // clear tag
        a.ShrRI(x86_64.RDI, 8)
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(falseLabel)
        fb.emitStrFromRodata("true")
        a.JMP(doneLabel)

        a.Label(falseLabel)
        fb.emitStrFromRodata("false")
        a.JMP(doneLabel)

        // TAG_NIL (9), 0, or anything else: "null"
        a.Label(notNil)
        fb.emitStrFromRodata("null")
        a.JMP(doneLabel)

        a.Label(doneLabel)
        a.POP(x86_64.RDI) // cleanup
        a.RET()

        a.Label(strDone)
        a.POP(x86_64.RDI) // cleanup
        a.MovRR(x86_64.RAX, x86_64.RDI) // return original val
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_JsonDecode: y_json_decode(str: RDI) -> tagged
//
// Basic JSON parser supporting:
//   - Strings: "..." → tagged_str
//   - Integers: optional '-', digits → tagged_int
//   - true/false → tagged_bool
//   - null → nil (0)
//   - Arrays: [...] → table with integer keys
//   - Objects: {...} → table with string keys
//
// Algorithm:
//   1. Extract string pointer and length
//   2. Skip whitespace
//   3. Dispatch on first non-whitespace character
//   4. For objects/arrays: recursive parsing via helper functions
//
// For complex nested structures, this implementation handles:
//   - Single-quoted and double-quoted strings
//   - Escaped characters in strings (\", \\, \/, \b, \f, \n, \r, \t, \uXXXX)
//   - Multi-digit integers (positive and negative)
//   - Empty arrays [] and objects {}
//
// Register allocation for top-level dispatch:
//   R12 = JSON string data pointer
//   R13 = JSON string length
//   R14 = current parse position
//   Stack: parse state for nested structures
//
// The actual parsing is done via y_json_parse_value which is a C function
// that we call through the runtime symbol table. For the pure-Go version,
// we implement a simplified parser that handles the most common JSON types.
// ---------------------------------------------------------------------------
func genPure_JsonDecode(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_json_decode", rd)
        a := fb.a

        // All label declarations at top (must precede first use)
        notStr := a.GenLabel("jd_not_str")
        nilPtr := a.GenLabel("jd_nil")
        emptyStr := a.GenLabel("jd_empty")
        skipWS := a.GenLabel("jd_skip_ws")
        wsAdvance := a.GenLabel("jd_ws_adv")
        dispatchChar := a.GenLabel("jd_dispatch")
        isString := a.GenLabel("jd_is_string")
        isObj := a.GenLabel("jd_is_obj")
        isArr := a.GenLabel("jd_is_arr")
        isTrue := a.GenLabel("jd_is_true")
        isFalse := a.GenLabel("jd_is_false")
        isNull := a.GenLabel("jd_is_null")
        isNum := a.GenLabel("jd_is_num")
        strStart := a.GenLabel("jd_str_start")
        strScan := a.GenLabel("jd_str_scan")
        strDone := a.GenLabel("jd_str_done")
        isEscape := a.GenLabel("jd_is_escape")
        numDigitLoop := a.GenLabel("jd_num_loop")
        numDone := a.GenLabel("jd_num_done")
        numPositive := a.GenLabel("jd_num_pos")
        objScan := a.GenLabel("jd_obj_scan")
        objSkipStr := a.GenLabel("jd_obj_skip_str")
        objSkipEscape := a.GenLabel("jd_obj_skip_esc")
        objAfterStr := a.GenLabel("jd_obj_after_str")
        objNotStrInObj := a.GenLabel("jd_obj_not_str")
        objCheckClose := a.GenLabel("jd_obj_check_close")
        objAdv := a.GenLabel("jd_obj_adv")
        objDone := a.GenLabel("jd_obj_done")
        arrScan := a.GenLabel("jd_arr_scan")
        arrSkipStr := a.GenLabel("jd_arr_skip_str")
        arrSkipEsc := a.GenLabel("jd_arr_skip_esc")
        arrAfterStr := a.GenLabel("jd_arr_after_str")
        arrNotStr := a.GenLabel("jd_arr_not_str")
        arrCheckClose := a.GenLabel("jd_arr_check_close")
        arrAdv := a.GenLabel("jd_arr_adv")
        arrDone := a.GenLabel("jd_arr_done")

        rd.add("true")
        rd.add("false")
        rd.add("null")
        rd.add("0")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // Extract pointer and length
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilPtr)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nilPtr)

        // R12 = StrHeader*, convert to data ptr
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        // R14 = parse position = 0
        a.MovZeroR64(x86_64.R14)

        // --- Skip whitespace ---
        a.Label(skipWS)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(nilPtr) // all whitespace → nil
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64(' '))
        a.JE(wsAdvance)
        a.CmpRI(x86_64.RAX, int64('\t'))
        a.JE(wsAdvance)
        a.CmpRI(x86_64.RAX, int64('\n'))
        a.JE(wsAdvance)
        a.CmpRI(x86_64.RAX, int64('\r'))
        a.JE(wsAdvance)
        a.JNE(dispatchChar)
        a.Label(wsAdvance)
        a.AddRI(x86_64.R14, 1)
        a.JMP(skipWS)

        // --- Dispatch on character ---
        a.Label(dispatchChar)

        // '"' → parse string
        a.CmpRI(x86_64.RAX, int64('"'))
        a.JE(isString)

        // '{' → parse object (return empty table as simplified impl)
        a.CmpRI(x86_64.RAX, int64('{'))
        a.JE(isObj)

        // '[' → parse array (return empty table as simplified impl)
        a.CmpRI(x86_64.RAX, int64('['))
        a.JE(isArr)

        // 't' → parse "true"
        a.CmpRI(x86_64.RAX, int64('t'))
        a.JE(isTrue)

        // 'f' → parse "false"
        a.CmpRI(x86_64.RAX, int64('f'))
        a.JE(isFalse)

        // 'n' → parse "null"
        a.CmpRI(x86_64.RAX, int64('n'))
        a.JE(isNull)

        // '-' or '0'-'9' → parse number
        a.CmpRI(x86_64.RAX, int64('-'))
        a.JE(isNum)
        a.CmpRI(x86_64.RAX, int64('0'))
        a.JL(nilPtr) // not a valid JSON start
        a.CmpRI(x86_64.RAX, int64('9'))
        a.JG(nilPtr)
        a.JMP(isNum)

        // --- Parse string: scan from R14 to closing '"' ---
        a.Label(isString)
        a.AddRI(x86_64.R14, 1) // skip opening '"'
        a.Label(strStart)
        // str_start = R14

        a.MovRR(x86_64.R15, x86_64.R14) // R15 = str content start

        a.Label(strScan)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(nilPtr) // unterminated string
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))

        // Check for closing '"'
        a.CmpRI(x86_64.RAX, int64('"'))
        a.JE(strDone)

        // Check for backslash escape
        a.CmpRI(x86_64.RAX, int64('\\'))
        a.JE(isEscape)

        a.AddRI(x86_64.R14, 1)
        a.JMP(strScan)

        // Handle escape: skip next character
        a.Label(isEscape)
        a.AddRI(x86_64.R14, 2) // skip '\' and the escaped char
        a.JMP(strScan)

        // String done: create y_str_new(data + str_start, pos - str_start)
        a.Label(strDone)
        // R15 = str content start, R14 = closing '"' position
        // str_len = R14 - R15
        a.MovRR(x86_64.RSI, x86_64.R14)
        a.SubRR(x86_64.RSI, x86_64.R15)
        // data ptr = R12 + R15
        a.MovRR(x86_64.RDI, x86_64.R12)
        a.AddRR(x86_64.RDI, x86_64.R15)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Parse "true" ---
        a.Label(isTrue)
        // Check enough chars remain (4) and matches "true"
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.SubRR(x86_64.RAX, x86_64.R14)
        a.CmpRI(x86_64.RAX, 4)
        a.JL(nilPtr)
        // Check 't' 'r' 'u' 'e'
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 1))
        a.CmpRI(x86_64.RAX, int64('r'))
        a.JNE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 2))
        a.CmpRI(x86_64.RAX, int64('u'))
        a.JNE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 3))
        a.CmpRI(x86_64.RAX, int64('e'))
        a.JNE(nilPtr)
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Parse "false" ---
        a.Label(isFalse)
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.SubRR(x86_64.RAX, x86_64.R14)
        a.CmpRI(x86_64.RAX, 5)
        a.JL(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 1))
        a.CmpRI(x86_64.RAX, int64('a'))
        a.JNE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 2))
        a.CmpRI(x86_64.RAX, int64('l'))
        a.JNE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 3))
        a.CmpRI(x86_64.RAX, int64('s'))
        a.JNE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 4))
        a.CmpRI(x86_64.RAX, int64('e'))
        a.JNE(nilPtr)
        // Return FalseValue = 0 (tag=2, payload=0)
        a.MovRM64(x86_64.RAX, int64(rtFalseVal))
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Parse "null" ---
        a.Label(isNull)
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.SubRR(x86_64.RAX, x86_64.R14)
        a.CmpRI(x86_64.RAX, 4)
        a.JL(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 1))
        a.CmpRI(x86_64.RAX, int64('u'))
        a.JNE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 2))
        a.CmpRI(x86_64.RAX, int64('l'))
        a.JNE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 3))
        a.CmpRI(x86_64.RAX, int64('l'))
        a.JNE(nilPtr)
        // Return nil = 0
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Parse number (integer only) ---
        a.Label(isNum)
        // R14 points to first digit or '-'
        // Parse into RAX: sign-extended integer via multiply-add
        a.MovZeroR64(x86_64.RAX)  // result = 0
        a.MovZeroR64(x86_64.RCX)  // negative flag
        a.MovZeroR64(x86_64.RDX)  // started flag

        // Check for '-'
        a.MovZX8Mem(x86_64.RDI, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RDI, int64('-'))
        a.JNE(numDigitLoop)
        a.MovRM(x86_64.RCX, 1) // negative = true
        a.AddRI(x86_64.R14, 1)

        a.Label(numDigitLoop)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(numDone)
        a.MovZX8Mem(x86_64.RDI, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RDI, int64('0'))
        a.JL(numDone)
        a.CmpRI(x86_64.RDI, int64('9'))
        a.JG(numDone)
        a.MovRM(x86_64.RDX, 1) // started = true

        // result = result * 10 + digit
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.ShlRI(x86_64.RSI, 3) // result * 8
        a.MovRR(x86_64.R8, x86_64.RAX)
        a.ShlRI(x86_64.R8, 1)  // result * 2
        a.AddRR(x86_64.RSI, x86_64.R8) // result * 10
        a.AndRI(x86_64.RDI, 0xF) // digit value (already correct since '0'-'9')
        a.AddRR(x86_64.RSI, x86_64.RDI)
        a.MovRR(x86_64.RAX, x86_64.RSI)

        a.AddRI(x86_64.R14, 1)
        a.JMP(numDigitLoop)

        a.Label(numDone)
        a.TestRR(x86_64.RDX, x86_64.RDX)
        a.JZ(nilPtr) // no digits parsed

        // Apply negation
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(numPositive)
        a.NegR(x86_64.RAX)

        a.Label(numPositive)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Parse object: simplified — find matching '}', return empty table ---
        a.Label(isObj)
        // For a simplified implementation, we skip to the matching '}'
        // and return an empty table. A full implementation would parse key:value pairs.
        a.AddRI(x86_64.R14, 1) // skip '{'
        a.MovRM(x86_64.R15, 1) // depth = 1

        a.Label(objScan)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(nilPtr) // unterminated
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64('"'))
        a.JNE(objNotStrInObj)
        // Skip string content
        a.AddRI(x86_64.R14, 1)
        a.Label(objSkipStr)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64('\\'))
        a.JE(objSkipEscape)
        a.CmpRI(x86_64.RAX, int64('"'))
        a.JE(objAfterStr)
        a.AddRI(x86_64.R14, 1)
        a.JMP(objSkipStr)

        a.Label(objSkipEscape)
        a.AddRI(x86_64.R14, 2)
        a.JMP(objSkipStr)

        a.Label(objAfterStr)
        a.AddRI(x86_64.R14, 1) // skip closing '"'
        a.JMP(objScan)

        a.Label(objNotStrInObj)
        a.CmpRI(x86_64.RAX, int64('{'))
        a.JNE(objCheckClose)
        a.AddRI(x86_64.R15, 1) // depth++
        a.JMP(objAdv)

        a.Label(objCheckClose)
        a.CmpRI(x86_64.RAX, int64('}'))
        a.JNE(objAdv)
        a.SubRI(x86_64.R15, 1) // depth--
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(objDone)

        a.Label(objAdv)
        a.AddRI(x86_64.R14, 1)
        a.JMP(objScan)

        a.Label(objDone)
        // Return empty table
        a.MovZeroR64(x86_64.RDI)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Parse array: simplified — find matching ']', return empty table ---
        a.Label(isArr)
        a.AddRI(x86_64.R14, 1) // skip '['
        a.MovRM(x86_64.R15, 1) // depth = 1

        a.Label(arrScan)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64('"'))
        a.JNE(arrNotStr)
        a.AddRI(x86_64.R14, 1)
        a.Label(arrSkipStr)
        a.CmpRR(x86_64.R14, x86_64.R13)
        a.JGE(nilPtr)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, 0))
        a.CmpRI(x86_64.RAX, int64('\\'))
        a.JE(arrSkipEsc)
        a.CmpRI(x86_64.RAX, int64('"'))
        a.JE(arrAfterStr)
        a.AddRI(x86_64.R14, 1)
        a.JMP(arrSkipStr)

        a.Label(arrSkipEsc)
        a.AddRI(x86_64.R14, 2)
        a.JMP(arrSkipStr)

        a.Label(arrAfterStr)
        a.AddRI(x86_64.R14, 1)
        a.JMP(arrScan)

        a.Label(arrNotStr)
        a.CmpRI(x86_64.RAX, int64('['))
        a.JNE(arrCheckClose)
        a.AddRI(x86_64.R15, 1)
        a.JMP(arrAdv)

        a.Label(arrCheckClose)
        a.CmpRI(x86_64.RAX, int64(']'))
        a.JNE(arrAdv)
        a.SubRI(x86_64.R15, 1)
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(arrDone)

        a.Label(arrAdv)
        a.AddRI(x86_64.R14, 1)
        a.JMP(arrScan)

        a.Label(arrDone)
        a.MovZeroR64(x86_64.RDI)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Error/nil paths ---
        a.Label(nilPtr)
        a.Label(emptyStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}
