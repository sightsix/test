package runtime

// ---------------------------------------------------------------------------
// Pure-Go Runtime: Filesystem Operations
//
// Implements filesystem functions using raw Linux syscalls.
// All functions follow the Yilt tagged value calling convention.
// ---------------------------------------------------------------------------

import (
        "github.com/yilt/yiltc/internal/codegen/x86_64"
)

// ---------------------------------------------------------------------------
// Filesystem syscall and flag constants
// ---------------------------------------------------------------------------

const (
        sysStat   = 4
        sysFstat  = 5
        sysOpen   = 2
        sysClose  = 3
        sysUnlink = 87
        sysMkdir  = 83
        sysRmdir  = 84
        sysRename = 82

        oRDOnly   = 0
        oWROnly   = 1
        oRDWR     = 2
        oCreat    = 0x40
        oTrunc    = 0x200
        oAppend   = 0x400

        sIFREG  = 0x8000
        sIFDIR  = 0x4000
        sIMode  = 0xF000 // mask for file type bits

        statBufSize = 144
        statModeOff = 24
        statSizeOff = 48
)

// ---------------------------------------------------------------------------
// genFS_ExtractStrPath: extracts data pointer and length from a tagged string.
//
// Input:  tagged_str in RDI
// Output: R12 = data pointer, R13 = length
// Clobbers: RAX, RCX. Preserves RBX and callee-saves.
// On nil/invalid: R12=0, R13=0
// ---------------------------------------------------------------------------

func emitFS_ExtractStr(fb *rtFuncBuilder, taggedReg x86_64.Reg, dataOut, lenOut x86_64.Reg, errorLabel string) {
        a := fb.a

        // Extract StrHeader pointer
        fb.getPtr(x86_64.RAX, taggedReg)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(errorLabel)

        // Read length from offset +8
        a.MovRR(lenOut, x86_64.RAX)
        a.MovMemR(lenOut, x86_64.MemBase(lenOut, 8))

        // Compute data pointer at offset +24
        a.MovRR(dataOut, x86_64.RAX)
        a.AddRI(dataOut, rtStrHeaderSize) // data = header + 24
}

// ---------------------------------------------------------------------------
// genPure_FsExists: y_fs_exists(path: RDI) -> tagged_bool
//
// Returns TrueValue if path exists, 0 otherwise.
// syscall: stat(path, buf); return rax == 0
// ---------------------------------------------------------------------------

func genPure_FsExists(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_exists", rd)
        a := fb.a

        // Check path tag is string
        notStr := a.GenLabel("fex_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        a.PUSH(x86_64.RBX)

        // Extract string data pointer and length
        errPath := a.GenLabel("fex_err_path")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Allocate stat buffer on stack (144 bytes, 16-byte aligned)
        a.SubRI(x86_64.RSP, statBufSize+8) // extra 8 for alignment
        a.MovRR(x86_64.RAX, x86_64.RSP)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))
        // RAX is the aligned stat buffer address; save in RBX
        a.MovRR(x86_64.RBX, x86_64.RAX)

        // Null-terminate path: write 0 at [R12 + len]
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8)) // length
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Compute path data pointer
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        // syscall(SYS_stat, path_ptr, stat_buf)
        a.MovRM(x86_64.RAX, sysStat)
        a.MovRR(x86_64.RSI, x86_64.RBX)
        a.SYSCALL()

        // Restore stat buffer length to path: restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Restore stack
        a.AddRI(x86_64.RSP, statBufSize+8)

        // Check result: rax == 0 means success
        doneLabel := a.GenLabel("fex_done")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(doneLabel) // nonzero = error -> return 0

        // Success: return TrueValue
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(doneLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(errPath)
        a.AddRI(x86_64.RSP, statBufSize+8)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsIsFile: y_fs_is_file(path: RDI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsIsFile(rd *rodataBuilder) puregenFunc {
        return genPure_FsStatCheck(rd, sIFREG)
}

// ---------------------------------------------------------------------------
// genPure_FsIsDir: y_fs_is_dir(path: RDI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsIsDir(rd *rodataBuilder) puregenFunc {
        return genPure_FsStatCheck(rd, sIFDIR)
}

// ---------------------------------------------------------------------------
// genPure_FsStatCheck: shared implementation for is_file/is_dir
// ---------------------------------------------------------------------------

func genPure_FsStatCheck(rd *rodataBuilder, modeMask uint64) puregenFunc {
        var name string
        if modeMask == sIFREG {
                name = "y_fs_is_file"
        } else {
                name = "y_fs_is_dir"
        }
        fb := newRtFuncBuilder(name, rd)
        a := fb.a

        // Check path tag
        notStr := a.GenLabel("fsc_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        a.PUSH(x86_64.RBX)

        // Extract StrHeader pointer
        errPath := a.GenLabel("fsc_err")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Stat buffer on stack
        a.SubRI(x86_64.RSP, statBufSize+8)
        a.MovRR(x86_64.RAX, x86_64.RSP)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))
        a.MovRR(x86_64.RBX, x86_64.RAX)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Path data pointer
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        // syscall(SYS_stat, path, buf)
        a.MovRM(x86_64.RAX, sysStat)
        a.MovRR(x86_64.RSI, x86_64.RBX)
        a.SYSCALL()

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Restore stack
        a.AddRI(x86_64.RSP, statBufSize+8)

        // Check stat success
        doneLabel := a.GenLabel("fsc_done")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(doneLabel) // stat failed

        // Read st_mode from stat buf (offset 24)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RBX, statModeOff))
        a.AndRI(x86_64.RAX, int64(sIMode))
        a.CmpRI(x86_64.RAX, int64(modeMask))
        a.JNE(doneLabel)

        // Match!
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(doneLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(errPath)
        a.AddRI(x86_64.RSP, statBufSize+8)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsFileSize: y_fs_file_size(path: RDI) -> tagged_int
// ---------------------------------------------------------------------------

func genPure_FsFileSize(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_file_size", rd)
        a := fb.a

        notStr := a.GenLabel("fsz_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        a.PUSH(x86_64.RBX)

        errPath := a.GenLabel("fsz_err")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Stat buffer
        a.SubRI(x86_64.RSP, statBufSize+8)
        a.MovRR(x86_64.RAX, x86_64.RSP)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))
        a.MovRR(x86_64.RBX, x86_64.RAX)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        a.MovRM(x86_64.RAX, sysStat)
        a.MovRR(x86_64.RSI, x86_64.RBX)
        a.SYSCALL()

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        a.AddRI(x86_64.RSP, statBufSize+8)

        // Check stat success
        doneLabel := a.GenLabel("fsz_done")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(doneLabel) // stat failed -> return 0

        // Read st_size at offset 48
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RBX, statSizeOff))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(doneLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(errPath)
        a.AddRI(x86_64.RSP, statBufSize+8)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsRemove: y_fs_remove(path: RDI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsRemove(rd *rodataBuilder) puregenFunc {
        return genPure_FsUnlink(rd, "y_fs_remove", sysUnlink)
}

// ---------------------------------------------------------------------------
// genPure_FsUnlink: shared helper for remove
// ---------------------------------------------------------------------------

func genPure_FsUnlink(rd *rodataBuilder, name string, syscallNR int64) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        a := fb.a

        notStr := a.GenLabel("fun_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        a.PUSH(x86_64.RBX)

        errPath := a.GenLabel("fun_err")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Path data pointer
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        // syscall(unlink/mkdir/rmdir, path)
        a.MovRM(x86_64.RAX, syscallNR)
        a.SYSCALL()

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        doneLabel := a.GenLabel("fun_done")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(doneLabel)

        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(doneLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(errPath)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsMkdir: y_fs_mkdir(path: RDI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsMkdir(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_mkdir", rd)
        a := fb.a

        notStr := a.GenLabel("fmd_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        a.PUSH(x86_64.RBX)

        errPath := a.GenLabel("fmd_err")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))

        // syscall(SYS_mkdir, path, 0o755)
        a.MovRM(x86_64.RAX, sysMkdir)
        a.MovRM(x86_64.RSI, 0o755) // mode
        a.SYSCALL()

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        doneLabel := a.GenLabel("fmd_done")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(doneLabel)

        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(doneLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(errPath)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsRmdir: y_fs_rmdir(path: RDI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsRmdir(rd *rodataBuilder) puregenFunc {
        return genPure_FsUnlink(rd, "y_fs_rmdir", sysRmdir)
}

// ---------------------------------------------------------------------------
// genPure_FsRename: y_fs_rename(old: RDI, new: RSI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsRename(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_rename", rd)
        a := fb.a

        notStr1 := a.GenLabel("frn_not_str1")
        // Check old path tag
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr1)

        notStr2 := a.GenLabel("frn_not_str2")
        // Check new path tag
        a.MovRR(x86_64.RAX, x86_64.RSI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr2)

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        errPath := a.GenLabel("frn_err")
        // R12 = old StrHeader, R13 = new StrHeader
        a.MovRR(x86_64.R12, x86_64.RDI)
        a.MovRR(x86_64.R13, x86_64.RSI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        fb.getPtr(x86_64.R13, x86_64.R13)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(errPath)

        // Null-terminate old path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Null-terminate new path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R13, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // syscall(SYS_rename, old, new)
        a.MovRM(x86_64.RAX, sysRename)
        // RDI = old data ptr
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        // RSI = new data ptr
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R13, rtStrHeaderSize))
        a.SYSCALL()

        // Restore null terminators
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R13, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        doneLabel := a.GenLabel("frn_done")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(doneLabel)

        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(doneLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(errPath)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr1)
        a.Label(notStr2)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsReadText: y_fs_read_text(path: RDI) -> tagged_str
//
// Opens a file, reads its contents, and returns as a tagged string.
// ---------------------------------------------------------------------------

func genPure_FsReadText(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_read_text", rd)
        a := fb.a

        notStr := a.GenLabel("frt_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // R12 = StrHeader* for path
        errPath := a.GenLabel("frt_err")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Stat buffer for fstat
        a.SubRI(x86_64.RSP, statBufSize+8)
        a.MovRR(x86_64.RAX, x86_64.RSP)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))
        a.MovRR(x86_64.RBX, x86_64.RAX) // RBX = stat buf

        // open(path, O_RDONLY, 0)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.MovRM(x86_64.RAX, sysOpen)
        a.MovZeroR64(x86_64.RSI)  // O_RDONLY
        a.MovZeroR64(x86_64.RDX)  // mode
        a.SYSCALL()

        // Check open error
        openErr := a.GenLabel("frt_open_err")
        a.MovRR(x86_64.R13, x86_64.RAX) // R13 = fd
        a.CmpRI(x86_64.R13, 0)
        a.Jcc(x86_64.CondS, openErr) // negative fd = error

        // fstat(fd, stat_buf) to get size
        a.MovRM(x86_64.RAX, sysFstat)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.MovRR(x86_64.RSI, x86_64.RBX)
        a.SYSCALL()

        // Read st_size at offset 48
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.RBX, statSizeOff)) // R14 = file size

        // If size is 0, close and return empty string
        isEmpty := a.GenLabel("frt_empty")
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JZ(isEmpty)

        // mmap buffer for reading: mmap(NULL, size, PROT_READ|PROT_WRITE, MAP_PRIVATE|MAP_ANON, -1, 0)
        a.MovZeroR64(x86_64.RDI)
        a.MovRR(x86_64.RSI, x86_64.R14)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        mmapErr := a.GenLabel("frt_mmap_err")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JAE(mmapErr)

        // RAX = buffer ptr
        a.MovRR(x86_64.R15, x86_64.RAX) // R15 = buffer

        // read(fd, buffer, size)
        a.MovRM(x86_64.RAX, sysRead)
        a.MovRR(x86_64.RDI, x86_64.R13)  // fd
        a.MovRR(x86_64.RSI, x86_64.R15)  // buffer
        a.MovRR(x86_64.RDX, x86_64.R14)  // size
        a.SYSCALL()

        // bytes_read in RAX (may be less than size for partial reads)
        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = bytes read

        // close(fd)
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()

        // Restore stat buffer
        a.AddRI(x86_64.RSP, statBufSize+8)

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Call y_str_new(buffer, bytes_read)
        a.MovRR(x86_64.RDI, x86_64.R15) // data ptr
        a.MovRR(x86_64.RSI, x86_64.R14) // length
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Error paths ---
        a.Label(isEmpty)
        // Close fd and return empty string
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()
        a.AddRI(x86_64.RSP, statBufSize+8)
        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        // Return empty string
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagStr, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(mmapErr)
        // Close fd, restore stack, restore null terminator
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()
        a.Label(openErr)
        a.AddRI(x86_64.RSP, statBufSize+8)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.Label(errPath)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsWriteText: y_fs_write_text(path: RDI, content: RSI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsWriteText(rd *rodataBuilder) puregenFunc {
        return genPure_FsWriteImpl(rd, "y_fs_write_text", oWROnly|oCreat|oTrunc)
}

// ---------------------------------------------------------------------------
// genPure_FsAppendText: y_fs_append_text(path: RDI, content: RSI) -> tagged_bool
// ---------------------------------------------------------------------------

func genPure_FsAppendText(rd *rodataBuilder) puregenFunc {
        return genPure_FsWriteImpl(rd, "y_fs_append_text", oWROnly|oCreat|oAppend)
}

// ---------------------------------------------------------------------------
// genPure_FsWriteImpl: shared implementation for write_text/append_text
// ---------------------------------------------------------------------------

func genPure_FsWriteImpl(rd *rodataBuilder, name string, flags int64) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        a := fb.a

        notStr1 := a.GenLabel("fwr_not_str1")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr1)

        notStr2 := a.GenLabel("fwr_not_str2")
        a.MovRR(x86_64.RAX, x86_64.RSI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr2)

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        errPath := a.GenLabel("fwr_err")
        // R12 = path StrHeader, R13 = content StrHeader
        a.MovRR(x86_64.R12, x86_64.RDI)
        a.MovRR(x86_64.R13, x86_64.RSI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        fb.getPtr(x86_64.R13, x86_64.R13)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(errPath)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // open(path, flags, 0o644)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.MovRM(x86_64.RAX, sysOpen)
        a.MovRM(x86_64.RSI, flags)
        a.MovRM(x86_64.RDX, 0o644)
        a.SYSCALL()

        // Check open error
        openErr := a.GenLabel("fwr_open_err")
        a.CmpRI(x86_64.RAX, 0)
        a.Jcc(x86_64.CondS, openErr)

        // Save fd in RBX
        a.MovRR(x86_64.RBX, x86_64.RAX)

        // Get content data pointer and length
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.R13, 8)) // length
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R13, rtStrHeaderSize)) // data ptr

        // write(fd, data, len)
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRR(x86_64.RDI, x86_64.RBX) // fd
        a.SYSCALL()

        // close(fd)
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.RBX)
        a.SYSCALL()

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(openErr)
        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.Label(errPath)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr1)
        a.Label(notStr2)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsReadLines: y_fs_read_lines(path: RDI) -> tagged_table
//
// Reads entire file, splits by '\n', returns table with integer keys.
// ---------------------------------------------------------------------------

func genPure_FsReadLines(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_read_lines", rd)
        a := fb.a

        notStr := a.GenLabel("frl_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        errPath := a.GenLabel("frl_err")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Stat buffer
        a.SubRI(x86_64.RSP, statBufSize+8)
        a.MovRR(x86_64.RAX, x86_64.RSP)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))
        a.MovRR(x86_64.RBX, x86_64.RAX)

        // open(path, O_RDONLY, 0)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.MovRM(x86_64.RAX, sysOpen)
        a.MovZeroR64(x86_64.RSI)
        a.MovZeroR64(x86_64.RDX)
        a.SYSCALL()

        openErr := a.GenLabel("frl_open_err")
        a.MovRR(x86_64.R13, x86_64.RAX)
        a.CmpRI(x86_64.R13, 0)
        a.Jcc(x86_64.CondS, openErr)

        // fstat(fd, stat_buf)
        a.MovRM(x86_64.RAX, sysFstat)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.MovRR(x86_64.RSI, x86_64.RBX)
        a.SYSCALL()

        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.RBX, statSizeOff)) // R14 = file size

        // If empty, close and return empty table
        isEmpty := a.GenLabel("frl_empty")
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JZ(isEmpty)

        // mmap buffer
        a.MovZeroR64(x86_64.RDI)
        a.MovRR(x86_64.RSI, x86_64.R14)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        mmapErr := a.GenLabel("frl_mmap_err")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JAE(mmapErr)

        // R15 = buffer
        a.MovRR(x86_64.R15, x86_64.RAX)

        // read(fd, buffer, size)
        a.MovRM(x86_64.RAX, sysRead)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.MovRR(x86_64.RSI, x86_64.R15)
        a.MovRR(x86_64.RDX, x86_64.R14)
        a.SYSCALL()
        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = bytes read

        // close(fd)
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()

        a.AddRI(x86_64.RSP, statBufSize+8)

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Now count '\n' characters to determine table capacity
        // R15 = data ptr, R14 = bytes read, RBX = counter
        a.MovZeroR64(x86_64.RBX)
        a.MovZeroR64(x86_64.R8) // line index counter

        countLoop := a.GenLabel("frl_count")
        countNoNL := a.GenLabel("frl_count_no_nl")
        countDone := a.GenLabel("frl_count_done")
        a.Label(countLoop)
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JZ(countDone)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R15, 0))
        a.CmpRI(x86_64.RAX, '\n')
        a.JNE(countNoNL)
        a.AddRI(x86_64.RBX, 1)
        a.Label(countNoNL)
        a.LEA(x86_64.R15, x86_64.MemBase(x86_64.R15, 1))
        a.SubRI(x86_64.R14, 1)
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JNZ(countLoop)

        // If no newlines found (single line), still count 1
        a.TestRR(x86_64.RBX, x86_64.RBX)
        a.JNZ(countDone)
        a.MovRM(x86_64.RBX, 1)
        a.Label(countDone)

        // Estimate capacity: count + count/2 (for hash table efficiency)
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.ShrRI(x86_64.RAX, 1)
        a.AddRR(x86_64.RBX, x86_64.RAX) // RBX = capacity estimate

        // Create table: y_table_new(capacity)
        // Save R15 (data ptr) and R14 (bytes read)
        a.PUSH(x86_64.R15)
        a.PUSH(x86_64.R14)

        a.MovRR(x86_64.RDI, x86_64.RBX)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        // RAX = tagged_table

        a.MovRR(x86_64.R13, x86_64.RAX) // R13 = table

        a.POP(x86_64.R14) // bytes read
        a.POP(x86_64.R15) // data ptr

        // Check if table creation succeeded
        tableErr := a.GenLabel("frl_table_err")
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(tableErr)

        // Now scan through data and insert lines into table
        // R15 = current scan ptr, R14 = remaining bytes
        // R8 = line index (1-based)
        a.MovRM(x86_64.R8, 1)

        // Save line start ptr in R9
        a.MovRR(x86_64.R9, x86_64.R15)

        scanLoop := a.GenLabel("frl_scan")
        scanDone := a.GenLabel("frl_scan_done")
        scanAdv := a.GenLabel("frl_scan_adv")
        a.Label(scanLoop)

        // Check if we've consumed all bytes
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JZ(scanDone)

        // Load current byte
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R15, 0))
        a.CmpRI(x86_64.RAX, '\n')
        a.JNE(scanAdv)

        // Found '\n': create substring from R9 to R15, length = R15 - R9
        // line_len = R15 - R9
        a.MovRR(x86_64.RCX, x86_64.R15)
        a.SubRR(x86_64.RCX, x86_64.R9) // RCX = line length

        // Skip empty lines (length 0) — just continue
        scanSkip := a.GenLabel("frl_scan_skip")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(scanSkip)

        // Create line string: y_str_new(R9, RCX)
        a.PUSH(x86_64.R15)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R9)
        a.PUSH(x86_64.R8)
        a.PUSH(x86_64.R13)

        a.MovRR(x86_64.RDI, x86_64.R9)  // data ptr
        a.MovRR(x86_64.RSI, x86_64.RCX) // length
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        // RAX = tagged_str
        a.POP(x86_64.R13) // table
        a.POP(x86_64.R8)   // line index
        a.POP(x86_64.R9)   // line start
        a.POP(x86_64.R14)  // remaining bytes
        a.POP(x86_64.R15)  // current ptr

        // Set table entry: y_tab_set(table, line_index, line_str)
        // Need to push more to preserve across call
        a.PUSH(x86_64.R15)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R9)
        a.PUSH(x86_64.R8)

        a.MovRR(x86_64.RDI, x86_64.R13) // table
        fb.mkTag(rtTagInt, x86_64.R8, x86_64.RSI) // key = line index as tagged int
        a.MovRR(x86_64.RDX, x86_64.RAX)          // val = line string
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        a.POP(x86_64.R8)
        a.POP(x86_64.R9)
        a.POP(x86_64.R14)
        a.POP(x86_64.R15)

        // Increment line index
        a.AddRI(x86_64.R8, 1)

        a.Label(scanSkip)
        // Advance past the newline
        a.LEA(x86_64.R15, x86_64.MemBase(x86_64.R15, 1))
        a.SubRI(x86_64.R14, 1)
        // New line start = current position
        a.MovRR(x86_64.R9, x86_64.R15)
        a.JMP(scanLoop)

        a.Label(scanAdv)
        // Not a newline, just advance
        a.LEA(x86_64.R15, x86_64.MemBase(x86_64.R15, 1))
        a.SubRI(x86_64.R14, 1)
        a.JMP(scanLoop)

        a.Label(scanDone)
        // Handle last line (if non-empty)
        a.MovRR(x86_64.RCX, x86_64.R15)
        a.SubRR(x86_64.RCX, x86_64.R9) // RCX = last line length
        finalDone := a.GenLabel("frl_final_done")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(finalDone)

        // Create last line string
        a.MovRR(x86_64.RDI, x86_64.R9)
        a.MovRR(x86_64.RSI, x86_64.RCX)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        // Set in table
        a.MovRR(x86_64.RDI, x86_64.R13) // table
        fb.mkTag(rtTagInt, x86_64.R8, x86_64.RSI) // key
        a.MovRR(x86_64.RDX, x86_64.RAX)          // val
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        a.Label(finalDone)
        // Return table
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(tableErr)
        a.Label(isEmpty)
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()
        a.AddRI(x86_64.RSP, statBufSize+8)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
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

        a.Label(mmapErr)
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()
        a.Label(openErr)
        a.AddRI(x86_64.RSP, statBufSize+8)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.Label(errPath)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsReadDir: y_fs_read_dir(path: RDI) -> tagged_table
//
// Reads directory entries using getdents64 (syscall 217).
// Returns a table with integer keys mapping to entry names (tagged strings).
//
// linux_dirent64 layout:
//   offset  0: d_ino    (uint64)
//   offset  8: d_off    (uint64)
//   offset 16: d_reclen (uint16)
//   offset 18: d_type   (uint8)
//   offset 19: d_name[] (null-terminated)
//
// Algorithm:
//   1. Open directory with sys_open(path, O_RDONLY|O_DIRECTORY)
//   2. mmap a 32KB buffer for getdents64
//   3. Call getdents64 in a loop until it returns 0
//   4. Parse each dirent: extract d_name, create tagged string, insert in table
//   5. Close fd, return table
// ---------------------------------------------------------------------------

const (
        sysGetdents64 = 217
        oDirectory    = 0o200000 // O_DIRECTORY
        dtDir         = 4
        dtReg         = 8
        dtLnk         = 10
)

func genPure_FsReadDir(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_read_dir", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        notStr := a.GenLabel("frd_not_str")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // R12 = StrHeader* for path
        errPath := a.GenLabel("frd_err_path")
        a.MovRR(x86_64.R12, x86_64.RDI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)

        // Null-terminate path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // open(path, O_RDONLY | O_DIRECTORY, 0)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.MovRM(x86_64.RAX, sysOpen)
        a.MovZeroR64(x86_64.RSI)                         // O_RDONLY
        a.MovRM(x86_64.RDX, oRDOnly|oDirectory)          // O_RDONLY | O_DIRECTORY
        a.SYSCALL()

        openErr := a.GenLabel("frd_open_err")
        a.MovRR(x86_64.R13, x86_64.RAX) // R13 = fd
        a.CmpRI(x86_64.R13, 0)
        a.Jcc(x86_64.CondS, openErr)

        // Allocate getdents64 buffer (32768 bytes)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 32768)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        bufErr := a.GenLabel("frd_buf_err")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JAE(bufErr)

        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = buf

        // Create result table with estimated capacity (32)
        a.MovRM(x86_64.RDI, 32)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        a.MovRR(x86_64.RBX, x86_64.RAX) // RBX = result table
        a.MovRM(x86_64.R15, 0)          // R15 = entry index counter

        // --- getdents64 loop ---
        getdentsLoop := a.GenLabel("frd_getdents")
        a.Label(getdentsLoop)

        // syscall(SYS_getdents64, fd, buf, 32768)
        a.MovRM(x86_64.RAX, sysGetdents64)
        a.MovRR(x86_64.RDI, x86_64.R13) // fd
        a.MovRR(x86_64.RSI, x86_64.R14) // buf
        a.MovRM(x86_64.RDX, 32768)      // count
        a.SYSCALL()

        // If returned <= 0, we're done
        doneGetdents := a.GenLabel("frd_done_getdents")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JLE(doneGetdents)

        // RAX = total bytes returned
        // R8 = current offset within buf
        a.MovZeroR64(x86_64.R8)

        // --- Parse dirents ---
        parseLoop := a.GenLabel("frd_parse")
        a.Label(parseLoop)

        // Check if we've consumed all bytes
        a.CmpRR(x86_64.R8, x86_64.RAX)
        a.JGE(getdentsLoop) // all consumed, call getdents64 again

        // Read d_reclen at buf+offset+16 (uint16)
        a.MovRR(x86_64.RCX, x86_64.R8)
        a.MovZX16Mem(x86_64.RCX, x86_64.MemIndex(x86_64.R14, x86_64.RCX, 1, 16))
        // RCX = d_reclen

        // d_name starts at buf+offset+19
        // Compute name length by scanning for null terminator
        a.MovRR(x86_64.RDX, x86_64.R8)
        a.AddRI(x86_64.RDX, 19) // RDX = offset of d_name in buf

        // Skip '.' and '..' entries
        // Check if d_name[0] == '.'
        notDotEntry := a.GenLabel("frd_not_dot")
        dotDotEntry := a.GenLabel("frd_dotdot")
        advanceEntry := a.GenLabel("frd_advance")
        nameLenDone := a.GenLabel("frd_namelen_done")

        a.MovZX8Mem(x86_64.RDI, x86_64.MemIndex(x86_64.R14, x86_64.RDX, 1, 0))
        a.CmpRI(x86_64.RDI, int64('.'))
        a.JNE(notDotEntry)

        // Check if d_name[1] == 0 (just '.') or d_name[1] == '.' and d_name[2] == 0 (just '..')
        a.MovZX8Mem(x86_64.RSI, x86_64.MemIndex(x86_64.R14, x86_64.RDX, 1, 1))
        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(dotDotEntry) // '.' entry → skip
        a.CmpRI(x86_64.RSI, int64('.'))
        a.JNE(notDotEntry)
        a.MovZX8Mem(x86_64.RSI, x86_64.MemIndex(x86_64.R14, x86_64.RDX, 1, 2))
        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JNZ(notDotEntry) // not '..' → process normally

        // Skip '.' or '..'
        a.Label(dotDotEntry)
        a.AddRR(x86_64.R8, x86_64.RCX) // advance by reclen
        a.JMP(parseLoop)

        a.Label(notDotEntry)

        // Compute name length: scan from d_name for null byte
        a.MovRR(x86_64.RDI, x86_64.RDX) // RDI = name ptr in buf
        nameLenLoop := a.GenLabel("rd_name_len_loop")
        a.Label(nameLenLoop)
        a.MovZX8Mem(x86_64.RSI, x86_64.MemIndex(x86_64.R14, x86_64.RDI, 1, 0))
        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(nameLenDone)
        a.AddRI(x86_64.RDI, 1)
        a.JMP(nameLenLoop)

        a.Label(nameLenDone)
        // RDI = end of name, RDX = start of name
        // name_len = RDI - RDX
        a.MovRR(x86_64.RSI, x86_64.RDI)
        a.SubRR(x86_64.RSI, x86_64.RDX) // RSI = name_len

        // Skip empty names (shouldn't happen but be safe)
        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(advanceEntry)

        // Create string: y_str_new(name_ptr, name_len)
        // Need to save across call: RBX (table), R8 (offset), R14 (buf), R15 (index), R13 (fd)
        a.PUSH(x86_64.R15)
        a.PUSH(x86_64.R8)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.RBX)

        a.MovRR(x86_64.RDI, x86_64.RDX)         // name ptr (in buf)
        // wait, RDX was name start but we clobbered RDI with the pointer into buf
        // Let me recalculate: name ptr = R14 + original_name_start_offset
        // Actually RDX = original offset of d_name in buf, but after the nameLenLoop,
        // RDI was being used as a running pointer. RDX still holds the original offset.
        // But wait, RDX is the offset in the buffer, not the actual pointer.
        // name_ptr = R14 + RDX. Let me use that.
        a.MovRR(x86_64.RDI, x86_64.R14)
        a.AddRR(x86_64.RDI, x86_64.RDX)        // RDI = &buf[name_offset]
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        a.POP(x86_64.RBX)   // table
        a.POP(x86_64.R13)   // fd
        a.POP(x86_64.R14)   // buf
        a.POP(x86_64.R8)    // offset
        a.POP(x86_64.R15)   // index

        // RAX = tagged string
        // Insert into table: y_tab_set(table, index, name_str)
        a.PUSH(x86_64.R15)
        a.PUSH(x86_64.R8)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R13)

        a.MovRR(x86_64.RDI, x86_64.RBX) // table
        fb.mkTag(rtTagInt, x86_64.R15, x86_64.RSI) // key = index as tagged int
        a.MovRR(x86_64.RDX, x86_64.RAX) // val = name string
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        a.POP(x86_64.R13)
        a.POP(x86_64.R14)
        a.POP(x86_64.R8)
        a.POP(x86_64.R15)

        a.AddRI(x86_64.R15, 1) // increment index

        // Advance to next entry
        a.Label(advanceEntry)
        a.AddRR(x86_64.R8, x86_64.RCX) // offset += reclen
        a.JMP(parseLoop)

        // --- Done with getdents64 ---
        a.Label(doneGetdents)

        // Close fd
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()

        // Restore null terminator
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Return table
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // --- Error paths ---
        a.Label(bufErr)
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.SYSCALL()
        a.Label(openErr)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.Label(errPath)
        a.MovZeroR64(x86_64.RDI)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr)
        a.MovZeroR64(x86_64.RDI)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FsCopy: y_fs_copy(src: RDI, dst: RSI) -> tagged_bool
//
// Copies file from src to dst by reading/writing in 4096-byte chunks.
// ---------------------------------------------------------------------------

func genPure_FsCopy(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fs_copy", rd)
        a := fb.a

        notStr1 := a.GenLabel("fcp_not_str1")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr1)

        notStr2 := a.GenLabel("fcp_not_str2")
        a.MovRR(x86_64.RAX, x86_64.RSI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr2)

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        errPath := a.GenLabel("fcp_err")
        // R12 = src StrHeader, R13 = dst StrHeader
        a.MovRR(x86_64.R12, x86_64.RDI)
        a.MovRR(x86_64.R13, x86_64.RSI)
        fb.getPtr(x86_64.R12, x86_64.R12)
        fb.getPtr(x86_64.R13, x86_64.R13)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(errPath)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(errPath)

        // Null-terminate src path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Null-terminate dst path
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R13, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        // Allocate 4096-byte buffer via mmap
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 4096)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        bufErr := a.GenLabel("fcp_buf_err")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JAE(bufErr)

        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = buffer

        // open(src, O_RDONLY, 0)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.MovRM(x86_64.RAX, sysOpen)
        a.MovZeroR64(x86_64.RSI)
        a.MovZeroR64(x86_64.RDX)
        a.SYSCALL()

        srcErr := a.GenLabel("fcp_src_err")
        a.MovRR(x86_64.R15, x86_64.RAX) // R15 = src fd
        a.CmpRI(x86_64.R15, 0)
        a.Jcc(x86_64.CondS, srcErr)

        // open(dst, O_WRONLY|O_CREAT|O_TRUNC, 0o644)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R13, rtStrHeaderSize))
        a.MovRM(x86_64.RAX, sysOpen)
        a.MovRM(x86_64.RSI, oWROnly|oCreat|oTrunc)
        a.MovRM(x86_64.RDX, 0o644)
        a.SYSCALL()

        dstErr := a.GenLabel("fcp_dst_err")
        a.MovRR(x86_64.RBX, x86_64.RAX) // RBX = dst fd
        a.CmpRI(x86_64.RBX, 0)
        a.Jcc(x86_64.CondS, dstErr)

        // Copy loop: read 4096 bytes, write to dst, until read returns 0
        copyLoop := a.GenLabel("fcp_loop")
        a.Label(copyLoop)

        // read(src_fd, buffer, 4096)
        a.MovRM(x86_64.RAX, sysRead)
        a.MovRR(x86_64.RDI, x86_64.R15) // src fd
        a.MovRR(x86_64.RSI, x86_64.R14) // buffer
        a.MovRM(x86_64.RDX, 4096)
        a.SYSCALL()

        // Check for EOF or error
        copyDone := a.GenLabel("fcp_done")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(copyDone)    // EOF
        a.Jcc(x86_64.CondS, copyDone) // error

        // write(dst_fd, buffer, bytes_read)
        a.MovRR(x86_64.RDX, x86_64.RAX) // bytes from read
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRR(x86_64.RDI, x86_64.RBX) // dst fd
        a.MovRR(x86_64.RSI, x86_64.R14) // buffer
        a.SYSCALL()
        a.JMP(copyLoop)

        a.Label(copyDone)
        // Close both fds
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R15)
        a.SYSCALL()
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.RBX)
        a.SYSCALL()

        // Restore null terminators
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R13, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)

        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Error paths
        a.Label(dstErr)
        // Close src fd
        a.MovRM(x86_64.RAX, sysClose)
        a.MovRR(x86_64.RDI, x86_64.R15)
        a.SYSCALL()
        a.Label(srcErr)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R13, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.Label(errPath)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(bufErr)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R12, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R13, 8))
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtStrHeaderSize), x86_64.RAX)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notStr1)
        a.Label(notStr2)
        a.MovZeroR64(x86_64.RAX)
        a.RET()

        return fb.finalize()
}
