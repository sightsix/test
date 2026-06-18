package runtime

// ---------------------------------------------------------------------------
// Pure-Go Runtime: String, Table, and Iterator Functions
//
// This file implements the remaining runtime functions that were previously
// stubs, replacing C-compiled code with pure-Go generated x86_64 machine
// code. This eliminates the dependency on gcc for the Yilt compiler.
//
// Functions implemented here:
//   - y_str_new: allocate StrHeader + copy data via mmap
//   - y_str_concat: concatenate two strings
//   - y_table_new: allocate TableHeader + entries via mmap
//   - y_tab_len: return table entry count
//   - y_tab_get/set/has/del: hash table operations
//   - runtime_iter_new/next/get_key/get_val/get_next: table iteration
//
// Helper functions (internal, called via PLT32 from table ops):
//   - pure_hash_tagged: FNV-1a hash of a tagged value
//   - pure_values_equal: content-based equality of two tagged values
//   - pure_table_rehash: resize and re-insert all entries
// ---------------------------------------------------------------------------

import (
        "math"

        "github.com/yilt/yiltc/internal/codegen/x86_64"
)

// ---------------------------------------------------------------------------
// Memory layout constants (must match tables.go and strings.go)
// ---------------------------------------------------------------------------

const (
        rtTableHeaderSize    = 64
        rtEntrySize          = 32
        rtTableOffCount      = 0
        rtTableOffCapacity   = 8
        rtTableOffThreshold  = 16
        rtTableOffMask       = 24
        rtTableOffEntries    = 32
        rtTableOffEntryCap   = 40
        rtTableOffTombstones = 48
        rtEntryOffKey        = 0
        rtEntryOffValue      = 8
        rtEntryOffHash       = 16
        rtEntryOffOccupied   = 24
        rtEntryEmpty         = 0
        rtEntryOccupied      = 1
        rtEntryTombstone     = 2
        rtFalseVal           = uint64(2) << 56 // MK_TAG(TAG_BOOL, 0)
        sysMunmap            = 11

        // FNV-1a 64-bit constants (var to allow runtime uint64→int64 conversion)
        fnv1aOffsetBasis = uint64(0xCBF29CE484222325)
        fnv1aPrime        = uint64(0x00000100000001B3)

        // Murmur3 finalizer constants (for int/bool hash)
        murmur3C2 = uint64(0xC4CEB9FE1A85EC53)
)

// ---------------------------------------------------------------------------
// emitMemcpy: copy count bytes from [src] to [dst] using REP MOVSB.
// Clobbers RCX, RSI, RDI. Preserves all other registers.
// ---------------------------------------------------------------------------

func (fb *rtFuncBuilder) emitMemcpy(dst, src, count x86_64.Reg) {
        a := fb.a
        if count.Code != x86_64.RCX.Code {
                a.MovRR(x86_64.RCX, count)
        }
        if src.Code != x86_64.RSI.Code {
                a.MovRR(x86_64.RSI, src)
        }
        if dst.Code != x86_64.RDI.Code {
                a.MovRR(x86_64.RDI, dst)
        }
        skip := a.GenLabel("memcpy_skip")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(skip)
        a.EmitBytes([]byte{0xF3, 0xA4}) // REP MOVSB
        a.Label(skip)
}

// ---------------------------------------------------------------------------
// emitMemcmp: compare count bytes from [a] and [b]. Returns 0 if equal,
// non-zero otherwise. Clobbers RCX, RSI, RDI, RAX. Sets ZF on result.
// ---------------------------------------------------------------------------

func (fb *rtFuncBuilder) emitMemcmp(a_reg, b_reg, count x86_64.Reg) {
        // Save count, set up for REP CMPSB (which compares [RSI] to [RDI], decrements RCX)
        a := fb.a
        if count.Code != x86_64.RCX.Code {
                a.MovRR(x86_64.RCX, count)
        }
        if a_reg.Code != x86_64.RSI.Code {
                a.MovRR(x86_64.RSI, a_reg)
        }
        if b_reg.Code != x86_64.RDI.Code {
                a.MovRR(x86_64.RDI, b_reg)
        }
        skip := a.GenLabel("memcmp_skip")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(skip)
        // Clear DF (forward direction) — should be 0 on entry, but be safe
        a.EmitBytes([]byte{0xFC}) // CLD
        // REPE CMPSB (0xF3 0xA6): repeat while bytes are EQUAL.
        // Stops when a mismatch is found (ZF=0) or RCX reaches 0.
        // After: ZF=1 if all bytes matched, ZF=0 if mismatch.
        //
        // BUG FIX: previously emitted 0xF2 (REPNE) which repeats while
        // bytes are NOT equal — it stops at the first matching byte,
        // making "abc"=="abd" return true because byte 0 ('a') matches.
        a.EmitBytes([]byte{0xF3, 0xA6}) // REPE CMPSB
        a.Label(skip)
        // After REPZ CMPSB: ZF=1 if all bytes matched, ZF=0 if mismatch
}

// ---------------------------------------------------------------------------
// genPure_StrNew: y_str_new(data_ptr: RDI, len: RSI) -> tagged_str
//
// Allocates StrHeader (24 bytes) + data + null terminator via mmap,
// copies the data, and returns a tagged string value.
//
// StrHeader layout: [refcount:8 | len:8 | cap:8 | data[]]
// Total allocation: 24 + len + 1, aligned to 8 bytes.
// ---------------------------------------------------------------------------

func genPure_StrNew(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_new", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // Save arguments
        a.MovRR(x86_64.R12, x86_64.RDI) // R12 = data_ptr
        a.MovRR(x86_64.RBX, x86_64.RSI) // RBX = len

        // Compute allocation size: 24 + len + 1, aligned to 8
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.AddRI(x86_64.RAX, 25) // + header(24) + null(1)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))

        // mmap(NULL, size, PROT_READ|PROT_WRITE, MAP_PRIVATE|MAP_ANON, -1, 0)
        a.MovZeroR64(x86_64.RDI)
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        okLabel := a.GenLabel("str_new_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(okLabel)
        // RAX = StrHeader*, R12 = data_ptr, RBX = len

        // Set header fields (refcount at +0 is already 0 from mmap)
        a.MovRMMem(x86_64.MemBase(x86_64.RAX, 8), x86_64.RBX) // len
        a.MovRR(x86_64.RCX, x86_64.RBX)
        a.AddRI(x86_64.RCX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.RAX, 16), x86_64.RCX) // cap

        // memcpy([RAX+24], [R12], RBX)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.RAX, 24))
        a.MovRR(x86_64.RSI, x86_64.R12)
        a.MovRR(x86_64.RCX, x86_64.RBX)
        skipCopy := a.GenLabel("str_new_copy_done")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(skipCopy)
        fb.emitMemcpy(x86_64.RDI, x86_64.RSI, x86_64.RCX)
        a.Label(skipCopy)

        // Null terminator at [RAX+24+RBX] is already 0 (mmap returns zeroed pages)

        // Return MK_TAG(TAG_STR, RAX)
        fb.mkTag(rtTagStr, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrConcat: y_str_concat(a: RDI, b: RSI) -> tagged_str
//
// Concatenates two tagged strings. Returns a new string with the
// combined content.
// ---------------------------------------------------------------------------

func genPure_StrConcat(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_concat", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // Check both tags are TAG_STR
        notStr1 := a.GenLabel("cat_not_str1")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr1)
        notStr2 := a.GenLabel("cat_not_str2")
        a.MovRR(x86_64.RAX, x86_64.RSI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr2)

        // Extract pointers
        a.MovRR(x86_64.R12, x86_64.RDI) // R12 = a tagged
        a.MovRR(x86_64.R13, x86_64.RSI) // R13 = b tagged
        fb.getPtr(x86_64.R12, x86_64.R12) // ha = GET_PTR(a)
        fb.getPtr(x86_64.R13, x86_64.R13) // hb = GET_PTR(b)

        // Check for null pointers
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notStr1)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(notStr2)

        // Read lengths
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, 8)) // ha->len
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R13, 8)) // hb->len

        // new_len = ha->len + hb->len
        a.MovRR(x86_64.RBX, x86_64.R14) // RBX = ha->len (save)
        a.AddRR(x86_64.R14, x86_64.R15) // R14 = new_len

        // Compute allocation size: 24 + new_len + 1, aligned to 8
        a.MovRR(x86_64.RAX, x86_64.R14)
        a.AddRI(x86_64.RAX, 25)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))

        // mmap
        a.MovZeroR64(x86_64.RDI)
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        okLabel := a.GenLabel("cat_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)

        // Error / tag mismatch: return nil
        a.Label(notStr1)
        a.Label(notStr2)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(okLabel)
        // RAX = new StrHeader*
        // R12 = ha, R13 = hb, RBX = ha->len, R14 = new_len, R15 = hb->len

        // Save StrHeader ptr in R11 (caller-saved, but safe within our function
        // since we don't call anything after mmap)
        a.MovRR(x86_64.R11, x86_64.RAX) // R11 = new StrHeader

        // Set header fields
        a.MovRMMem(x86_64.MemBase(x86_64.R11, 8), x86_64.R14) // len = new_len
        a.MovRR(x86_64.RCX, x86_64.R14)
        a.AddRI(x86_64.RCX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R11, 16), x86_64.RCX) // cap

        // First memcpy: h->data <- ha->data, ha->len bytes
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R11, 24))
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R12, 24))
        a.MovRR(x86_64.RCX, x86_64.RBX) // ha->len
        skip1 := a.GenLabel("cat_skip1")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(skip1)
        fb.emitMemcpy(x86_64.RDI, x86_64.RSI, x86_64.RCX)
        a.Label(skip1)

        // Second memcpy: h->data + ha->len <- hb->data, hb->len bytes
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.R11, x86_64.RBX, 1, 24))
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R13, 24))
        a.MovRR(x86_64.RCX, x86_64.R15) // hb->len
        skip2 := a.GenLabel("cat_skip2")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(skip2)
        fb.emitMemcpy(x86_64.RDI, x86_64.RSI, x86_64.RCX)
        a.Label(skip2)

        // Return MK_TAG(TAG_STR, R11) — result must be in RAX per the
        // System V AMD64 calling convention.  Previously this called
        // mkTag(rtTagStr, R11, R11) which left the tagged value in R11
        // while the caller read RAX — so callers saw whatever mmap had
        // left in RAX (an untagged pointer) and treated it as nil.
        fb.mkTag(rtTagStr, x86_64.R11, x86_64.RAX)

        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TableNew: y_table_new(cap_hint: RDI) -> tagged_table
//
// Creates a new hash table with power-of-2 capacity (minimum 16).
// Allocates TableHeader (64 bytes) and entry array via mmap.
// ---------------------------------------------------------------------------

func genPure_TableNew(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_table_new", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // Round cap_hint up to power of 2, minimum 16
        useHint := a.GenLabel("tn_use_hint")
        a.CmpRI(x86_64.RDI, 16)
        a.JAE(useHint)
        a.MovRM(x86_64.RDI, 16)
        a.Label(useHint)

        // Power-of-2 rounding
        a.SubRI(x86_64.RDI, 1)
        a.MovRR(x86_64.RAX, x86_64.RDI); a.ShrRI(x86_64.RAX, 1); a.OrRR(x86_64.RDI, x86_64.RAX)
        a.MovRR(x86_64.RAX, x86_64.RDI); a.ShrRI(x86_64.RAX, 2); a.OrRR(x86_64.RDI, x86_64.RAX)
        a.MovRR(x86_64.RAX, x86_64.RDI); a.ShrRI(x86_64.RAX, 4); a.OrRR(x86_64.RDI, x86_64.RAX)
        a.MovRR(x86_64.RAX, x86_64.RDI); a.ShrRI(x86_64.RAX, 8); a.OrRR(x86_64.RDI, x86_64.RAX)
        a.MovRR(x86_64.RAX, x86_64.RDI); a.ShrRI(x86_64.RAX, 16); a.OrRR(x86_64.RDI, x86_64.RAX)
        a.MovRR(x86_64.RAX, x86_64.RDI); a.ShrRI(x86_64.RAX, 32); a.OrRR(x86_64.RDI, x86_64.RAX)
        a.AddRI(x86_64.RDI, 1)

        // RBX = capacity
        a.MovRR(x86_64.RBX, x86_64.RDI)

        // mmap TableHeader (64 bytes)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, rtTableHeaderSize)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        okLabel := a.GenLabel("tn_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(okLabel)
        // RAX = TableHeader*, RBX = capacity
        a.MovRR(x86_64.R12, x86_64.RAX) // R12 = TableHeader*

        // Set header fields
        // count at +0 = 0 (already zero)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffCapacity), x86_64.RBX) // capacity
        // threshold = cap * 3 / 4 = cap - cap/4
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.ShrRI(x86_64.RAX, 2)
        a.MovRR(x86_64.RCX, x86_64.RBX)
        a.SubRR(x86_64.RCX, x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffThreshold), x86_64.RCX) // threshold
        // mask = cap - 1
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.SubRI(x86_64.RAX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffMask), x86_64.RAX) // mask

        // Allocate entries array: 32 * capacity, aligned to 8
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.ShlRI(x86_64.RAX, 5) // RAX = cap * 32
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))

        // mmap entries
        a.MovZeroR64(x86_64.RDI)
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        okLabel2 := a.GenLabel("tn_ent_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel2)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(okLabel2)
        // Store entries ptr and entry_cap
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffEntries), x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffEntryCap), x86_64.RBX)

        // Return MK_TAG(TAG_TABLE, R12)
        a.MovRR(x86_64.RAX, x86_64.R12)
        fb.mkTag(rtTagTable, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabLen: y_tab_len(table: RDI) -> tagged_int
//
// Returns the number of occupied entries.
// ---------------------------------------------------------------------------

func genPure_TabLen(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_len", rd)
        a := fb.a

        // Check tag
        notTable := a.GenLabel("tl_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract pointer
        fb.getPtr(x86_64.RAX, x86_64.RDI)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        jzLabel := a.GenLabel("tl_null")
        a.JZ(jzLabel)

        // Read count at offset 0
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RAX, rtTableOffCount))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        a.Label(notTable)
        a.Label(jzLabel)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// pure_hash_tagged: FNV-1a hash of a tagged value
//
// hash_tagged(val: RDI) -> RAX = uint64 hash
//
// Called via PLT32 from table operations. Implemented as a proper
// standalone function with the standard calling convention.
// ---------------------------------------------------------------------------

func genPure_HashTagged(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("pure_hash_tagged", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)

        a.MovRR(x86_64.R12, x86_64.RDI) // R12 = val

        // Get tag
        a.MovRR(x86_64.RAX, x86_64.R12)
        a.ShrRI(x86_64.RAX, 56)

        // Check for TAG_STR (4)
        isStr := a.GenLabel("ht_is_str")
        hashDone := a.GenLabel("hash_done")
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(isStr)

        // --- String hashing: FNV-1a over string bytes ---
        fb.getPtr(x86_64.RAX, x86_64.R12) // RAX = StrHeader*
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(hashDone) // null pointer -> hash 0 (RAX is already 0)

        // Read length and data ptr
        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.RAX, 8)) // len
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(hashDone) // empty string
        a.LEA(x86_64.RBX, x86_64.MemBase(x86_64.RAX, rtStrHeaderSize)) // data ptr

        // Initialize FNV-1a state (use EmitBytes to load uint64 without int64 overflow)
        basis := fnv1aOffsetBasis
        a.MovRM64(x86_64.RAX, int64(basis))

        // FNV-1a loop: hash ^= byte; hash *= prime
        // Load FNV-1a prime into R14 (callee-saved, free in this path)
        a.MovRM64(x86_64.R14, int64(fnv1aPrime))

        loopLabel := a.GenLabel("ht_str_loop")
        a.Label(loopLabel)

        // hash ^= (unsigned char)[RBX]
        a.MovZX8Mem(x86_64.RCX, x86_64.MemBase(x86_64.RBX, 0))
        a.XorRR(x86_64.RAX, x86_64.RCX)

        // hash *= prime
        a.IMul2RR(x86_64.RAX, x86_64.R14) // RAX = RAX * prime (truncated to 64 bits)

        // Advance pointer and decrement counter
        a.LEA(x86_64.RBX, x86_64.MemBase(x86_64.RBX, 1))
        a.SubRI(x86_64.R13, 1)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JNZ(loopLabel)

        a.JMP(hashDone)

        // --- TAG_INT (1) or TAG_BOOL (2): murmur3 finalizer ---
        a.Label(isStr)
        intOrBool := a.GenLabel("ht_int_bool")
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JE(intOrBool) // tag==1 (INT) → murmur3
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JE(intOrBool) // tag==2 (BOOL) → murmur3
        jneDefault := a.GenLabel("ht_fnv_default")
        a.JMP(jneDefault) // else → default FNV-1a

        a.Label(intOrBool)

        // Extract payload
        a.MovRR(x86_64.RAX, x86_64.R12)
        a.ShlRI(x86_64.RAX, 8)  // clear tag
        a.ShrRI(x86_64.RAX, 8)

        // Murmur3 finalizer mixing
        a.MovRR(x86_64.RCX, x86_64.RAX)
        a.ShrRI(x86_64.RCX, 33)
        a.XorRR(x86_64.RAX, x86_64.RCX)
        a.IMul2RR(x86_64.RAX, x86_64.RAX)
        a.MovRR(x86_64.RCX, x86_64.RAX)
        a.ShrRI(x86_64.RCX, 33)
        a.XorRR(x86_64.RAX, x86_64.RCX)
        a.IMul2RR(x86_64.RAX, x86_64.RAX)
        a.MovRR(x86_64.RCX, x86_64.RAX)
        a.ShrRI(x86_64.RCX, 33)
        a.XorRR(x86_64.RAX, x86_64.RCX)

        a.JMP(hashDone)

        // --- Default: FNV-1a over raw 8 bytes ---
        a.Label(jneDefault) // declared above

        // hash = FNV1a_offset_basis
        basis2 := fnv1aOffsetBasis
        a.MovRM64(x86_64.RAX, int64(basis2))

        // Loop 8 times: hash ^= (val >> shift) & 0xFF; hash *= prime
        // Use R14 for the FNV-1a prime (callee-saved, free in this path)
        a.MovRM64(x86_64.R14, int64(fnv1aPrime))
        a.MovRM(x86_64.R13, 8) // counter = 8
        defaultLoop := a.GenLabel("ht_def_loop")
        a.Label(defaultLoop)

        a.MovRR(x86_64.RCX, x86_64.R12)
        a.AndRI(x86_64.RCX, int64(0xFF))
        a.XorRR(x86_64.RAX, x86_64.RCX)
        a.IMul2RR(x86_64.RAX, x86_64.R14) // hash *= prime

        // Shift R12 right by 8 for next byte (MSB first)
        a.ShrRI(x86_64.R12, 8)

        a.SubRI(x86_64.R13, 1)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JNZ(defaultLoop)

        a.JMP(hashDone)

        // --- hashDone: common epilogue ---
        a.Label(hashDone)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// pure_values_equal: content-based equality of two tagged values
//
// values_equal(a: RDI, b: RSI) -> RAX (1 if equal, 0 if not)
//
// For strings: pointer identity, then length, then byte comparison.
// For all other types: bitwise comparison.
// ---------------------------------------------------------------------------

func genPure_ValuesEqual(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("pure_values_equal", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        a.MovRR(x86_64.R12, x86_64.RDI) // R12 = a
        a.MovRR(x86_64.R13, x86_64.RSI) // R13 = b

        // Compare tags
        notEqual := a.GenLabel("ve_ne")
        a.MovRR(x86_64.RAX, x86_64.R12)
        a.ShrRI(x86_64.RAX, 56)
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.ShrRI(x86_64.RCX, 56)
        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.JNE(notEqual)

        // Check for TAG_STR (4)
        notStr := a.GenLabel("ve_not_str")
        equal := a.GenLabel("ve_equal")
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // --- String comparison ---
        // Extract pointers
        fb.getPtr(x86_64.RAX, x86_64.R12) // ha
        fb.getPtr(x86_64.RCX, x86_64.R13) // hb

        // Fast path: same pointer
        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.JE(equal)

        // If either pointer is null but not both (both-null caught above), not equal
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(notStr) // ha null, hb non-null → not equal
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(notStr) // hb null, ha non-null → not equal

        // Both non-null: save pointers, then load lengths
        a.MovRR(x86_64.RBX, x86_64.RAX)  // RBX = ha ptr (save)
        a.MovRR(x86_64.R8, x86_64.RCX)   // R8 = hb ptr (save)

        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RBX, 8)) // RAX = ha->len
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R8, 8))  // RCX = hb->len
        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.JNE(notStr)

        // Lengths match: byte comparison memcmp(ha->data, hb->data, ha->len)
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.RBX, rtStrHeaderSize)) // ha data ptr
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R8, rtStrHeaderSize))  // hb data ptr
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RAX) // compare ha->len bytes
        a.JE(equal)
        a.JMP(notStr)

        // --- Non-string: bitwise comparison ---
        a.Label(notStr)
        a.CmpRR(x86_64.R12, x86_64.R13)
        a.JNE(notEqual)

        a.Label(equal)
        a.MovRM(x86_64.RAX, 1) // return 1
        veDone := a.GenLabel("ve_done")
        a.JMP(veDone) // ← CRITICAL: do NOT fall through to notEqual

        a.Label(notEqual)
        a.MovZeroR64(x86_64.RAX) // return 0

        a.Label(veDone)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabGet: y_tab_get(table: RDI, key: RSI) -> tagged
//
// Looks up key in the hash table. Returns value or NilValue (0).
//
// Register allocation:
//   R12 = TableHeader* (callee-saved)
//   R13 = hash (callee-saved)
//   R14 = mask (callee-saved, NEVER clobbered)
//   RSI = original key (preserved across calls)
//   RAX = idx in probe loop
//   RCX = entry byte offset (idx << 5)
//   RBX = entries ptr (loaded fresh each iteration)
//   R15 = temporary
// ---------------------------------------------------------------------------

func genPure_TabGet(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_get", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // Check table tag
        notTable := a.GenLabel("tg_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = TableHeader*
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Read mask (keep in R14 throughout — never clobber)
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, rtTableOffMask))

        // Hash the key: save RSI (original key) and callee-saves across call
        a.PUSH(x86_64.RSI) // save original key
        a.PUSH(x86_64.R12) // save table ptr
        a.PUSH(x86_64.R14) // save mask
        a.PUSH(x86_64.R15) // alignment
        a.PUSH(x86_64.R13) // alignment
        a.MovRR(x86_64.RDI, x86_64.RSI) // key → RDI
        a.CALL("pure_hash_tagged")
        fb.addRelocText("pure_hash_tagged")
        // Save hash to RBX (callee-saved, already pushed in prologue) before
        // POPs clobber R13. Then restore hash to R13 after all POPs.
        a.MovRR(x86_64.RBX, x86_64.RAX) // RBX = hash (temp)
        a.POP(x86_64.R13)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14) // restore mask
        a.POP(x86_64.R12) // restore table ptr
        a.POP(x86_64.RSI) // restore original key
        a.MovRR(x86_64.R13, x86_64.RBX) // R13 = hash (restored)

        // idx = hash & mask
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.AndRR(x86_64.RAX, x86_64.R14) // RAX = idx

        // Probe loop
        probeLoop := a.GenLabel("tg_probe")
        a.Label(probeLoop)

        // Load entries ptr and compute entry byte offset each iteration
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries)) // RBX = entries
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = idx
        a.ShlRI(x86_64.RCX, 5)          // RCX = idx * 32

        // Read occupied flag using proper indexed addressing
        a.MovMemR(x86_64.R15, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied))

        // Empty → not found (no wrapping bounds check needed; mask guarantees it)
        a.CmpRI(x86_64.R15, rtEntryEmpty)
        a.JE(notTable)

        // Tombstone → skip
        tgAdvanceNoPop := a.GenLabel("tg_adv")
        a.CmpRI(x86_64.R15, rtEntryOccupied)
        a.JNE(tgAdvanceNoPop)

        // Occupied: check hash
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash))
        a.CmpRR(x86_64.RDX, x86_64.R13)
        a.JNE(tgAdvanceNoPop)

        // Hash matches: check key equality.
        // For non-string keys, do inline comparison (avoids CALL overhead and
        // potential relocation issues). For string keys, fall through to CALL.
        a.PUSH(x86_64.RAX) // save idx
        a.PUSH(x86_64.RBX) // save entries
        a.PUSH(x86_64.RCX) // save entry offset
        a.MovRR(x86_64.RDI, x86_64.RSI) // original key → RDI
        a.MovMemR(x86_64.RSI, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey)) // entry key → RSI
        // Inline key comparison for non-string keys
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.MovRR(x86_64.RDX, x86_64.RSI)
        a.ShrRI(x86_64.RDX, 56)
        a.CmpRR(x86_64.RAX, x86_64.RDX)
        veNotEq := a.GenLabel("tg_ve_ne")
        a.JNE(veNotEq)
        a.CmpRI(x86_64.RAX, rtTagStr)
        veCallVE := a.GenLabel("tg_ve_call")
        a.JE(veCallVE)
        // Non-string: bitwise comparison
        a.CmpRR(x86_64.RDI, x86_64.RSI)
        a.JNE(veNotEq)
        a.MovRM(x86_64.RAX, 1) // equal
        veDone := a.GenLabel("tg_ve_done")
        a.JMP(veDone)
        a.Label(veNotEq)
        a.MovZeroR64(x86_64.RAX) // not equal
        a.JMP(veDone)
        a.Label(veCallVE)
        a.CALL("pure_values_equal")
        fb.addRelocText("pure_values_equal")
        a.Label(veDone)
        // RAX = 1 if equal, 0 if not
        tgAdvanceFromCall := a.GenLabel("tg_adv_from_call")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(tgAdvanceFromCall) // NOT equal → continue probing (3 extra on stack)

        // Key matches! Pop saved state, load value, return.
        a.POP(x86_64.RCX)   // restore entry offset
        a.POP(x86_64.RBX)   // restore entries
        a.POP(x86_64.RAX)   // restore idx (unused but balanced)
        a.MovMemR(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffValue))
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(tgAdvanceFromCall)
        // 3 extra pushes on stack: pop them
        a.POP(x86_64.RCX)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RAX)
        // fall through to advance

        a.Label(tgAdvanceNoPop)
        // idx = (idx + 1) & mask  (R14 = mask, always valid)
        a.AddRI(x86_64.RAX, 1)
        a.AndRR(x86_64.RAX, x86_64.R14)
        a.JMP(probeLoop)

        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX) // NilValue = 0
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabHas: y_tab_has(table: RDI, key: RSI) -> tagged_bool
//
// Returns TrueValue if the table contains key, FalseValue otherwise.
// Same probe logic as genPure_TabGet but returns bool instead of value.
// ---------------------------------------------------------------------------

func genPure_TabHas(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_has", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // Check table tag
        notTable := a.GenLabel("th_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = TableHeader*
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Read mask (keep in R14 throughout — never clobber)
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, rtTableOffMask))

        // Hash the key: save RSI (original key) and callee-saves across call
        a.PUSH(x86_64.RSI) // save original key
        a.PUSH(x86_64.R12) // save table ptr
        a.PUSH(x86_64.R14) // save mask
        a.PUSH(x86_64.R15) // alignment
        a.PUSH(x86_64.R13) // alignment
        a.MovRR(x86_64.RDI, x86_64.RSI) // key → RDI
        a.CALL("pure_hash_tagged")
        fb.addRelocText("pure_hash_tagged")
        // Save hash to RBX before POPs clobber R13.
        a.MovRR(x86_64.RBX, x86_64.RAX) // RBX = hash (temp)
        a.POP(x86_64.R13)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14) // restore mask
        a.POP(x86_64.R12) // restore table ptr
        a.POP(x86_64.RSI) // restore original key
        a.MovRR(x86_64.R13, x86_64.RBX) // R13 = hash (restored)

        // idx = hash & mask
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.AndRR(x86_64.RAX, x86_64.R14) // RAX = idx

        // Probe loop
        probeLoop := a.GenLabel("th_probe")
        a.Label(probeLoop)

        // Load entries ptr and compute entry byte offset each iteration
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries)) // RBX = entries
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = idx
        a.ShlRI(x86_64.RCX, 5)          // RCX = idx * 32

        // Read occupied flag using proper indexed addressing
        a.MovMemR(x86_64.R15, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied))

        // Empty → not found (mask guarantees wrap-around)
        a.CmpRI(x86_64.R15, rtEntryEmpty)
        a.JE(notTable)

        // Tombstone → skip
        thAdvanceNoPop := a.GenLabel("th_adv")
        a.CmpRI(x86_64.R15, rtEntryOccupied)
        a.JNE(thAdvanceNoPop)

        // Occupied: check hash
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash))
        a.CmpRR(x86_64.RDX, x86_64.R13)
        a.JNE(thAdvanceNoPop)

        // Hash matches: check key equality via pure_values_equal
        a.PUSH(x86_64.RAX) // save idx
        a.PUSH(x86_64.RBX) // save entries
        a.PUSH(x86_64.RCX) // save entry offset
        a.MovRR(x86_64.RDI, x86_64.RSI) // original key → RDI
        a.MovMemR(x86_64.RSI, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey)) // entry key → RSI
        a.CALL("pure_values_equal")
        fb.addRelocText("pure_values_equal")
        // RAX = 1 if equal, 0 if not
        thAdvanceFromCall := a.GenLabel("th_adv_from_call")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(thAdvanceFromCall) // NOT equal → continue probing (3 extra on stack)

        // Key matches! Pop saved state, return TrueValue.
        a.POP(x86_64.RCX)   // restore entry offset
        a.POP(x86_64.RBX)   // restore entries
        a.POP(x86_64.RAX)   // restore idx
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(thAdvanceFromCall)
        // 3 extra pushes on stack: pop them
        a.POP(x86_64.RCX)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RAX)
        // fall through to advance

        a.Label(thAdvanceNoPop)
        // idx = (idx + 1) & mask  (R14 = mask, always valid)
        a.AddRI(x86_64.RAX, 1)
        a.AndRR(x86_64.RAX, x86_64.R14)
        a.JMP(probeLoop)

        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX) // FalseValue = 0
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabDel: y_tab_del(table: RDI, key: RSI) -> tagged_bool
//
// Removes key from the table. Returns TrueValue if found, FalseValue otherwise.
// Marks the slot as a tombstone.
// ---------------------------------------------------------------------------

func genPure_TabDel(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_del", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        notTable := a.GenLabel("td_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = TableHeader*
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Read mask (keep in R14 throughout — never clobber)
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, rtTableOffMask))

        // Hash the key: save RSI (original key) and callee-saves across call
        a.PUSH(x86_64.RSI) // save original key
        a.PUSH(x86_64.R12) // save table ptr
        a.PUSH(x86_64.R14) // save mask
        a.PUSH(x86_64.R15) // alignment
        a.PUSH(x86_64.R13) // alignment
        a.MovRR(x86_64.RDI, x86_64.RSI) // key → RDI
        a.CALL("pure_hash_tagged")
        fb.addRelocText("pure_hash_tagged")
        // Save hash to RBX before POPs clobber R13.
        a.MovRR(x86_64.RBX, x86_64.RAX) // RBX = hash (temp)
        a.POP(x86_64.R13)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14) // restore mask
        a.POP(x86_64.R12) // restore table ptr
        a.POP(x86_64.RSI) // restore original key
        a.MovRR(x86_64.R13, x86_64.RBX) // R13 = hash (restored)

        // idx = hash & mask
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.AndRR(x86_64.RAX, x86_64.R14) // RAX = idx

        // Probe loop
        probeLoop := a.GenLabel("td_probe")
        a.Label(probeLoop)

        // Load entries ptr and compute entry byte offset each iteration
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries)) // RBX = entries
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = idx
        a.ShlRI(x86_64.RCX, 5)          // RCX = idx * 32

        // Read occupied flag using proper indexed addressing
        a.MovMemR(x86_64.R15, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied))

        // Empty → not found (mask guarantees wrap-around)
        a.CmpRI(x86_64.R15, rtEntryEmpty)
        a.JE(notTable)

        // Tombstone → skip (no extra stack values)
        tdAdvanceNoPop := a.GenLabel("td_adv")
        a.CmpRI(x86_64.R15, rtEntryOccupied)
        a.JNE(tdAdvanceNoPop)

        // Occupied: check hash
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash))
        a.CmpRR(x86_64.RDX, x86_64.R13)
        a.JNE(tdAdvanceNoPop)

        // Hash matches: verify key equality via pure_values_equal
        a.PUSH(x86_64.RAX) // save idx
        a.PUSH(x86_64.RBX) // save entries
        a.PUSH(x86_64.RCX) // save entry offset
        a.MovRR(x86_64.RDI, x86_64.RSI) // original key → RDI
        a.MovMemR(x86_64.RSI, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey)) // entry key → RSI
        a.CALL("pure_values_equal")
        fb.addRelocText("pure_values_equal")
        // RAX = 1 if equal, 0 if not
        tdAdvanceFromCall := a.GenLabel("td_adv_from_call")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(tdAdvanceFromCall) // NOT equal → continue probing (3 extra on stack)

        // Key matches! Pop saved state, mark as tombstone.
        a.POP(x86_64.RCX)   // restore entry offset
        a.POP(x86_64.RBX)   // restore entries
        a.POP(x86_64.RAX)   // restore idx

        // entry->occupied = ENTRY_TOMBSTONE (2)
        a.MovRM(x86_64.RCX, rtEntryTombstone)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied), x86_64.RCX)
        // Zero out key, value, hash
        a.MovZeroR64(x86_64.RDX)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey), x86_64.RDX)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffValue), x86_64.RDX)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash), x86_64.RDX)
        // Decrement count, increment tombstones
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.R12, rtTableOffCount))
        a.SubRI(x86_64.RDX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffCount), x86_64.RDX)
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.R12, rtTableOffTombstones))
        a.AddRI(x86_64.RDX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffTombstones), x86_64.RDX)

        // Return TrueValue
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(tdAdvanceFromCall)
        // 3 extra pushes on stack: pop them
        a.POP(x86_64.RCX)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RAX)
        // fall through to advance

        a.Label(tdAdvanceNoPop)
        a.AddRI(x86_64.RAX, 1)
        a.AndRR(x86_64.RAX, x86_64.R14)
        a.JMP(probeLoop)

        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX) // FalseValue = 0
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabSet: y_tab_set(table: RDI, key: RSI, val: RDX) -> void
//
// Inserts or updates key -> value in the hash table.
// Triggers rehashing if the load factor is exceeded.
// ---------------------------------------------------------------------------

func genPure_TabSet(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_set", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // Check table tag
        notTable := a.GenLabel("ts_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = TableHeader*
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Ensure entries are allocated (check entries ptr at offset 32)
        hasEntries := a.GenLabel("ts_has_entries")
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JNZ(hasEntries)

        // Allocate entries: capacity=16, 32 bytes each
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 16*32) // 512 bytes
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JL(notTable)
        // Store entries pointer and entry_cap
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffEntries), x86_64.RAX)
        a.MovRM(x86_64.RCX, 16)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffEntryCap), x86_64.RCX)

        a.Label(hasEntries)

        // --- Save val (RDX) on stack. It stays there for the entire probe loop. ---
        // Stack layout during probe: [RSP+0]=val, [RSP+8..48]=callee saves
        a.PUSH(x86_64.RDX) // save val on stack

        // Hash the key. R12/R14/R15 are callee-saved so pure_hash_tagged preserves them.
        // PUSH(val) already gives correct CALL alignment (RSP ≡ 8 mod 16).
        a.MovRR(x86_64.RDI, x86_64.RSI) // key → RDI
        a.CALL("pure_hash_tagged")
        fb.addRelocText("pure_hash_tagged")
        a.MovRR(x86_64.R13, x86_64.RAX) // R13 = hash

        // Initialize first_tombstone = -1 (no tombstone found yet)
        a.MovRM(x86_64.R14, int64(-1)) // R14 = first_tombstone (NOT capacity!)

        // Read mask and capacity into dedicated registers
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R12, rtTableOffMask))    // R15 = mask
        a.MovMemR(x86_64.R8, x86_64.MemBase(x86_64.R12, rtTableOffCapacity))  // R8 = capacity

        // idx = hash & mask
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.AndRR(x86_64.RAX, x86_64.R15) // RAX = idx

        // Probe loop — idx lives in RAX between iterations (saved/restored via PUSH/POP)
        probeLoop := a.GenLabel("ts_probe")
        a.Label(probeLoop)

        // Bounds check: idx >= capacity → table full
        tsFull := a.GenLabel("ts_full")
        a.CmpRR(x86_64.RAX, x86_64.R8) // idx vs capacity (R8, not R14!)
        a.JAE(tsFull)

        // Load entries ptr and compute entry byte offset
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries)) // RBX = entries
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = idx
        a.ShlRI(x86_64.RCX, 5)          // RCX = idx * 32

        // Read occupied flag
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied))

        // Check EMPTY (0)
        isEmpty := a.GenLabel("ts_empty")
        isOccupied := a.GenLabel("ts_occupied")
        tsProbeAdv := a.GenLabel("ts_probe_adv")
        a.CmpRI(x86_64.RDX, rtEntryEmpty)
        a.JE(isEmpty)

        // Check OCCUPIED (1)
        a.CmpRI(x86_64.RDX, rtEntryOccupied)
        a.JE(isOccupied)

        // TOMBSTONE (2): record first tombstone if not yet recorded
        a.CmpRI(x86_64.R14, int64(-1)) // first_tombstone == -1?
        a.JNE(tsProbeAdv)
        a.MovRR(x86_64.R14, x86_64.RAX) // first_tombstone = idx

        a.Label(tsProbeAdv)
        // Advance probe: idx = (idx + 1) & mask
        a.AddRI(x86_64.RAX, 1)
        a.AndRR(x86_64.RAX, x86_64.R15) // mask
        a.JMP(probeLoop)

        a.Label(isEmpty)
        // Empty slot found! Check if we should reuse a recorded tombstone.
        reuseTomb := a.GenLabel("ts_reuse_tomb")
        a.CmpRI(x86_64.R14, int64(-1)) // any tombstone recorded?
        a.JNE(reuseTomb)

        // --- No tombstone: insert at current idx, increment count ---
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey), x86_64.RSI)      // key
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 0)) // load val from stack
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffValue), x86_64.RDX) // val
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash), x86_64.R13)  // hash
        a.MovRM(x86_64.RDX, rtEntryOccupied)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied), x86_64.RDX)
        // count++
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.R12, rtTableOffCount))
        a.AddRI(x86_64.RDX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffCount), x86_64.RDX)

        tsCheckRehash := a.GenLabel("ts_check_rehash")
        a.JMP(tsCheckRehash)

        a.Label(reuseTomb)
        // Reuse tombstone slot: recompute entry address for first_tombstone
        a.MovRR(x86_64.RCX, x86_64.R14) // first_tombstone index
        a.ShlRI(x86_64.RCX, 5)          // entry_addr = first_tombstone * 32
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey), x86_64.RSI)      // key
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 0)) // load val from stack
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffValue), x86_64.RDX) // val
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash), x86_64.R13)  // hash
        a.MovRM(x86_64.RDX, rtEntryOccupied)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied), x86_64.RDX)
        // count stays the same (replacing tombstone doesn't add)

        a.Label(tsCheckRehash)
        // Check load factor: count + tombstones >= threshold?
        tsDone := a.GenLabel("ts_done")
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.R12, rtTableOffCount))
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, rtTableOffTombstones))
        a.AddRR(x86_64.RDX, x86_64.RCX)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, rtTableOffThreshold))
        a.CmpRR(x86_64.RDX, x86_64.RCX)
        a.JL(tsDone)

        // Need rehash: call pure_table_rehash(table_ptr)
        // RDI = table_ptr already in R12
        a.MovRR(x86_64.RDI, x86_64.R12)
        a.PUSH(x86_64.RSI) // save original key
        a.CALL("pure_table_rehash")
        fb.addRelocText("pure_table_rehash")
        a.POP(x86_64.RSI) // restore original key

        a.Label(tsDone)
        a.MovZeroR64(x86_64.RAX) // void return
        a.POP(x86_64.RDX)       // clean up val from stack
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(isOccupied)
        // Check hash match
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash))
        a.CmpRR(x86_64.RDX, x86_64.R13)
        a.JNE(tsProbeAdv) // hash mismatch → continue probing

        // Hash matches: check key equality via pure_values_equal
        // Must save ALL live registers across the CALL (except RDI, RSI which are args)
        a.PUSH(x86_64.RAX)  // save idx
        a.PUSH(x86_64.RCX)  // save entry offset
        a.PUSH(x86_64.RBX)  // save entries ptr
        a.PUSH(x86_64.R13)  // save hash
        a.PUSH(x86_64.R14)  // save first_tombstone
        a.PUSH(x86_64.R15)  // save mask
        a.PUSH(x86_64.R12)  // save table ptr

        a.MovRR(x86_64.RDI, x86_64.RSI) // original key → RDI
        a.MovMemR(x86_64.RSI, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey)) // entry key → RSI
        a.CALL("pure_values_equal")
        fb.addRelocText("pure_values_equal")

        // Test result IMMEDIATELY (before POPs clobber RAX)
        tsAdvFromCall := a.GenLabel("ts_adv_from_call")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(tsAdvFromCall) // not equal → restore and continue probing

        // Key found! Update value, then return.
        a.POP(x86_64.R12)  // restore table ptr
        a.POP(x86_64.R15)  // restore mask
        a.POP(x86_64.R14)  // restore first_tombstone
        a.POP(x86_64.R13)  // restore hash
        a.POP(x86_64.RBX)  // restore entries ptr
        a.POP(x86_64.RCX)  // restore entry offset
        a.POP(x86_64.RAX)  // restore idx (unused)
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RSP, 0)) // load val from stack
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffValue), x86_64.RDX)
        a.MovZeroR64(x86_64.RAX) // void return
        a.POP(x86_64.RDX)   // clean up val from stack
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(tsAdvFromCall)
        // Not equal: restore all saved registers and continue probing
        a.POP(x86_64.R12)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RCX)
        a.POP(x86_64.RAX)  // restore idx
        a.JMP(tsProbeAdv)

        a.Label(tsFull)
        // Table full panic
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "panic: table full\n")
        a.MovRM(x86_64.RDX, 18)
        a.SYSCALL()
        a.MovRM(x86_64.RAX, sysExit)
        a.MovRM(x86_64.RDI, 1)
        a.SYSCALL()

        // notTable: early exit
        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX) // void return
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// pure_table_rehash: resize a hash table and re-insert all occupied entries
//
// pure_table_rehash(table_ptr: RDI) -> void
//
// Doubles the table capacity, allocates new entries, re-inserts all
// occupied entries, frees the old entries via munmap.
// ---------------------------------------------------------------------------

func genPure_TableRehash(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("pure_table_rehash", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // R12 = table_ptr = RDI
        a.MovRR(x86_64.R12, x86_64.RDI)

        // Save old entries pointer and capacity
        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, rtTableOffEntries)) // old_entries
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, rtTableOffCapacity)) // old_cap

        // Double capacity, cap at 2^28
        a.MovRR(x86_64.R15, x86_64.R14)
        a.ShlRI(x86_64.R15, 1)
        capCheck := a.GenLabel("tr_cap_check")
        a.CmpRI(x86_64.R15, 1<<28)
        a.JLE(capCheck)
        a.MovRM(x86_64.R15, 1<<28)
        a.Label(capCheck)

        // Update table header
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffCapacity), x86_64.R15) // capacity
        a.MovRR(x86_64.RAX, x86_64.R15)
        a.SubRI(x86_64.RAX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffMask), x86_64.RAX) // mask = cap - 1
        // threshold = cap - cap/4
        a.MovRR(x86_64.RAX, x86_64.R15)
        a.ShrRI(x86_64.RAX, 2)
        a.MovRR(x86_64.RCX, x86_64.R15)
        a.SubRR(x86_64.RCX, x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffThreshold), x86_64.RCX) // threshold
        // count = 0, tombstones = 0 (already zero from mmap, just be explicit)
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffCount), x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffTombstones), x86_64.RAX)

        // Allocate new entries: 32 * new_cap, aligned to 8
        a.MovRR(x86_64.RAX, x86_64.R15)
        a.ShlRI(x86_64.RAX, 5)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))

        a.MovZeroR64(x86_64.RDI)          // addr = NULL
        a.MovRR(x86_64.RSI, x86_64.RAX)   // size (must copy BEFORE setting RAX to sysMmap)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()
        a.CmpRI(x86_64.RAX, int64(-4096))
        rehashAllocOK := a.GenLabel("tr_alloc_ok")
        a.JGE(rehashAllocOK)

        // mmap failed: panic
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2)
        fb.emitLEA_RodataStr(x86_64.RSI, "panic: rehash mmap failed\n")
        a.MovRM(x86_64.RDX, 28)
        a.SYSCALL()
        a.MovRM(x86_64.RAX, sysExit)
        a.MovRM(x86_64.RDI, 1)
        a.SYSCALL()

        a.Label(rehashAllocOK)
        // Store new entries pointer
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffEntries), x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffEntryCap), x86_64.R15)

        // Re-insert all occupied entries from old array
        // Register allocation:
        //   R12 = table_ptr (callee-saved, fixed)
        //   R13 = old_entries (callee-saved, fixed)
        //   R14 = old_cap (callee-saved, fixed)
        //   RBX = new_entries ptr (callee-saved, fixed)
        //   RAX = loop counter i
        //   RDI = key (callee-saved, preserved across probe)
        //   R8  = hash (caller-saved but no calls in loop)
        //   RSI = probe idx (temp in probe loop)
        //   R15 = mask (temp in probe loop)
        //   Stack: value (pushed before probe, popped after)
        a.MovRR(x86_64.RBX, x86_64.RAX) // new entries ptr

        a.MovZeroR64(x86_64.RAX) // i = 0
        reinsertLoop := a.GenLabel("tr_reinsert")
        a.Label(reinsertLoop)

        // Bounds check: i >= old_cap -> done
        a.CmpRR(x86_64.RAX, x86_64.R14)
        rehashDone := a.GenLabel("tr_done")
        reinsertNext := a.GenLabel("tr_reinsert_next")
        a.JGE(rehashDone)

        // Compute entry byte offset: RCX = i * 32
        a.MovRR(x86_64.RCX, x86_64.RAX)
        a.ShlRI(x86_64.RCX, 5) // i * 32

        // Read old entry occupied flag: [R13 + RCX + 24]
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtEntryOffOccupied))
        a.CmpRI(x86_64.RDX, rtEntryOccupied)
        a.JNE(reinsertNext)

        // Occupied entry: read key, value, hash from old entry at [R13 + RCX + off]
        a.MovMemR(x86_64.RDI, x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtEntryOffKey))    // key → RDI (callee-saved)
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtEntryOffValue))  // value → RDX
        a.PUSH(x86_64.RDX) // save value on stack (RDX will be clobbered)
        a.MovMemR(x86_64.R8, x86_64.MemIndex(x86_64.R13, x86_64.RCX, 1, rtEntryOffHash))   // hash → R8 (no calls in loop)

        // Compute new_idx = hash & mask
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R12, rtTableOffMask)) // mask → R15
        a.MovRR(x86_64.RSI, x86_64.R8)  // hash → RSI
        a.AndRR(x86_64.RSI, x86_64.R15) // new_idx = hash & mask

        // Probe new table for empty slot
        reinsertProbe := a.GenLabel("tr_reinsert_probe")
        a.Label(reinsertProbe)

        // Compute byte offset for probe index: RCX = RSI * 32
        a.MovRR(x86_64.RCX, x86_64.RSI)
        a.ShlRI(x86_64.RCX, 5) // RCX = new_idx * 32

        // Check [RBX + RCX + 24] == EMPTY?
        a.MovMemR(x86_64.RCX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied))
        probe2Found := a.GenLabel("tr_probe2_found")
        a.CmpRI(x86_64.RCX, rtEntryEmpty)
        a.JE(probe2Found)

        // Not empty: advance idx = (idx + 1) & mask
        a.AddRI(x86_64.RSI, 1)
        a.AndRR(x86_64.RSI, x86_64.R15)
        a.JMP(reinsertProbe)

        a.Label(probe2Found)
        // Found empty slot at index RSI.
        // Recompute byte offset (RCX was clobbered by occupied flag read)
        a.MovRR(x86_64.RCX, x86_64.RSI)
        a.ShlRI(x86_64.RCX, 5) // RCX = new_idx * 32
        // Pop value from stack.
        a.POP(x86_64.RDX) // value

        // Store key, value, hash, occupied into new entry
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey), x86_64.RDI)     // key
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffValue), x86_64.RDX)   // value
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash), x86_64.R8)     // hash
        a.MovRM(x86_64.RDX, rtEntryOccupied)
        a.MovRMMem(x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied), x86_64.RDX) // occupied = 1

        // Increment table count
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, rtTableOffCount))
        a.AddRI(x86_64.RCX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R12, rtTableOffCount), x86_64.RCX)

        a.Label(reinsertNext)
        // Advance i
        a.AddRI(x86_64.RAX, 1)
        a.JMP(reinsertLoop)

        a.Label(rehashDone)
        // Free old entries via munmap
        // munmap(old_entries, old_cap * 32)
        // syscall(SYS_munmap, addr=old_entries, len=old_cap*32)
        a.MovRR(x86_64.RDI, x86_64.R13) // addr = old_entries → RDI
        a.MovRR(x86_64.RSI, x86_64.R14) // len = old_cap → RSI
        a.ShlRI(x86_64.RSI, 5)          // len = old_cap * 32
        a.MovRM(x86_64.RAX, sysMunmap)
        a.MovZeroR64(x86_64.RDX)
        a.MovZeroR64(x86_64.R10)
        a.MovZeroR64(x86_64.R8)
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        // Epilogue
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_IterNew: runtime_iter_new(table: RDI) -> tagged_int
//
// Stores the table pointer in a global and returns -1 (initial iterator
// index). Uses writable data section globals.
// ---------------------------------------------------------------------------

func genPure_IterNew(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("runtime_iter_new", rd)
        a := fb.a

        // Store table in global g_iter_table
        // The linker must allocate a writable 8-byte slot and provide its
        // address. We use an absolute 32-bit relocation targeting a special
        // section ".yilt.rt.data" that the linker places in writable memory.
        a.MovRMMem(x86_64.MemDispl(0), x86_64.RDI)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S, // absolute signed 32-bit
                Target: ".yilt.rt.data",
                Addend: 0, // offset 0 in .yilt.rt.data = g_iter_table
                Symbol: "",
        })

        // Return MK_TAG(TAG_INT, -1)
        a.MovRM64(x86_64.RAX, int64(-1))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_IterNext: runtime_iter_next(iter_idx: RDI) -> tagged_bool
//
// Advances the iterator and returns TrueValue if there are more entries,
// FalseValue if iteration is complete.
// Uses globals for table, cached key/val, and next index.
// ---------------------------------------------------------------------------

func genPure_IterNext(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("runtime_iter_next", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        // Call y_tab_iter_next(g_iter_table, iter_idx)
        // g_iter_table is at .yilt.rt.data+0, iter_idx is in RDI
        a.MovRM64(x86_64.RSI, int64(-1)) // iter_idx = -1 for initial

        // Load g_iter_table from .yilt.rt.data+0
        a.MovRM(x86_64.RDI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                        Target: ".yilt.rt.data",
                Addend: 0,
                Symbol: "",
        })

        // Call y_tab_iter_next(g_iter_table, -1)
        // y_tab_iter_next(table, iter) takes table in RDI, iter in RSI
        // It returns int64_t in RAX. -1 means done.
        // RSI already holds iter_idx = -1 (set above)
        a.PUSH(x86_64.RDI) // save g_iter_table across call? No - it's global
        a.CALL("y_tab_iter_next")
        fb.addRelocText("y_tab_iter_next")

        // RAX = next index (-1 if done)

        // Check if done (RAX == -1)
        a.CmpRI(x86_64.RAX, int64(-1))
        iterDone := a.GenLabel("iter_done")
        a.JE(iterDone)

        // Has more entries: call y_tab_iter_key and y_tab_iter_val
        // to cache key and value, then store next index
        a.MovRR(x86_64.RBX, x86_64.RAX) // save next idx

        // Store next index in .yilt.rt.data+24
        a.MovRM(x86_64.RSI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 24, // offset 24 = g_iter_next_idx
                Symbol: "",
        })
        a.MovRMMem(x86_64.MemDispl(0), x86_64.RBX)

        // Load g_iter_table for key/val calls
        a.MovRM(x86_64.RDI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 0,
                Symbol: "",
        })

        // y_tab_iter_key(table, MK_TAG(TAG_INT, next)) in RDI
        a.MovRR(x86_64.RDI, x86_64.RDI) // table ptr
        fb.mkTag(rtTagInt, x86_64.RBX, x86_64.RDI) // iter_idx as tagged int
        a.PUSH(x86_64.RBX) // save next idx
        a.CALL("y_tab_iter_key")
        fb.addRelocText("y_tab_iter_key")
        // Store result in g_iter_result_key (.yilt.rt.data+8)
        a.MovRM(x86_64.RSI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 8, // g_iter_result_key at offset 8
                Symbol: "",
        })
        a.MovRMMem(x86_64.MemDispl(0), x86_64.RAX)
        a.POP(x86_64.RBX) // restore next idx

        // y_tab_iter_val(table, MK_TAG(TAG_INT, next)) in RDI
        a.MovRR(x86_64.RDI, x86_64.RDI) // table ptr
        fb.mkTag(rtTagInt, x86_64.RBX, x86_64.RDI)
        a.PUSH(x86_64.RBX)
        a.CALL("y_tab_iter_val")
        fb.addRelocText("y_tab_iter_val")
        // Store result in g_iter_result_val (.yilt.data+16)
        a.MovRM(x86_64.RSI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                        Target: ".yilt.rt.data",
                Addend: 16, // g_iter_result_val at offset 16
                Symbol: "",
        })
        a.MovRMMem(x86_64.MemDispl(0), x86_64.RAX)
        a.POP(x86_64.RBX)

        // Return TrueValue (has more)
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(iterDone)
        // Mark done: store -1 in next_idx, 0 in result_key/val
        a.MovRM(x86_64.RSI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 24,
        })
        a.MovRM(x86_64.RAX, int64(-1))
        a.MovRMMem(x86_64.MemDispl(0), x86_64.RAX)
        // Also zero result_key
        a.MovRM(x86_64.RSI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 8,
        })
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemDispl(0), x86_64.RAX)
        // Zero result_val
        a.MovRM(x86_64.RSI, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 16,
        })
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemDispl(0), x86_64.RAX)

        // Return FalseValue (0)
        a.MovZeroR64(x86_64.RAX) // FalseValue = 0
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_IterGetKey: runtime_iter_get_key() -> tagged
//
// Returns the cached key from the most recent iterator advance.
// ---------------------------------------------------------------------------

func genPure_IterGetKey(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("runtime_iter_get_key", rd)
        a := fb.a

        // Load g_iter_result_key from .yilt.data+8
        a.MovRM(x86_64.RAX, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 8,
        })
        a.MovMemR(x86_64.RAX, x86_64.MemDispl(0))
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_IterGetVal: runtime_iter_get_val() -> tagged
//
// Returns the cached value from the most recent iterator advance.
// ---------------------------------------------------------------------------

func genPure_IterGetVal(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("runtime_iter_get_val", rd)
        a := fb.a

        // Load g_iter_result_val from .yilt.data+16
        a.MovRM(x86_64.RAX, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 16,
        })
        a.MovMemR(x86_64.RAX, x86_64.MemDispl(0))
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_IterGetNext: runtime_iter_get_next() -> tagged_int
//
// Returns the cached next iterator index from the most recent advance.
// ---------------------------------------------------------------------------

func genPure_IterGetNext(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("runtime_iter_get_next", rd)
        a := fb.a

        // Load g_iter_next_idx from .yilt.data+24
        a.MovRM(x86_64.RAX, 0)
        fb.relocs = append(fb.relocs, puregenReloc{
                Offset: uint64(fb.a.Offset() - 4),
                Type:   PuregenRelocAbs32S,
                Target: ".yilt.rt.data",
                Addend: 24,
        })
        a.MovMemR(x86_64.RAX, x86_64.MemDispl(0))
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrLen: y_str_len(str: RDI) -> tagged_int
//
// Returns the length of a tagged string.
// ---------------------------------------------------------------------------
func genPure_StrLen(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_len", rd)
        a := fb.a

        nullLabel := a.GenLabel("strlen_null")
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nullLabel)

        fb.getPtr(x86_64.RAX, x86_64.RDI)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(nullLabel)

        // Read len at offset +8
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RAX, 8))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        a.Label(nullLabel)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Lower: y_lower(str: RDI) -> tagged_str
//
// Returns a lowercase copy of the string.
// ---------------------------------------------------------------------------
func genPure_Lower(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_lower", rd)
        a := fb.a

        nilLabel := a.GenLabel("lower_nil")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)

        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = StrHeader*
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = len

        // Compute allocation size: 24 + len + 1, aligned to 8
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.AddRI(x86_64.RAX, 25)
        a.AddRI(x86_64.RAX, 7)
        a.AndRI(x86_64.RAX, ^int64(7))

        // mmap
        a.MovZeroR64(x86_64.RDI)
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        okLabel := a.GenLabel("lower_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(okLabel)
        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = new header

        // Set header fields
        a.MovRMMem(x86_64.MemBase(x86_64.R14, 8), x86_64.R13) // len
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.AddRI(x86_64.RCX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R14, 16), x86_64.RCX) // cap

        // Copy and convert using LODSB/STOSB
        a.EmitBytes([]byte{0xFC}) // CLD
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R12, 24)) // source data
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R14, 24)) // dest data
        a.MovRR(x86_64.RCX, x86_64.R13)                     // count

        loopLabel := a.GenLabel("lower_loop")
        storeLabel := a.GenLabel("lower_store")
        doneLabel := a.GenLabel("lower_done")

        a.Label(loopLabel)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(doneLabel)

        a.EmitBytes([]byte{0xAC}) // LODSB: AL = *RSI++

        // Check if 'A' <= AL <= 'Z'
        a.CmpRI(x86_64.RAX, int64('A'))
        a.JL(storeLabel)
        a.CmpRI(x86_64.RAX, int64('Z'))
        a.JG(storeLabel)

        // Convert to lowercase
        a.AddRI(x86_64.RAX, 32) // 'a' - 'A' = 32

        a.Label(storeLabel)
        a.EmitBytes([]byte{0xAA}) // STOSB: *RDI++ = AL
        a.SubRI(x86_64.RCX, 1)
        a.JNZ(loopLabel)

        a.Label(doneLabel)
        fb.mkTag(rtTagStr, x86_64.R14, x86_64.RAX)

        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Upper: y_upper(str: RDI) -> tagged_str
//
// Returns an uppercase copy of the string.
// ---------------------------------------------------------------------------
func genPure_Upper(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_upper", rd)
        a := fb.a

        nilLabel := a.GenLabel("upper_nil")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)

        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)

        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // len

        // mmap
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.AddRI(x86_64.RAX, 25)
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

        okLabel := a.GenLabel("upper_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(okLabel)
        a.MovRR(x86_64.R14, x86_64.RAX)

        // Set header fields
        a.MovRMMem(x86_64.MemBase(x86_64.R14, 8), x86_64.R13) // len
        a.MovRR(x86_64.RCX, x86_64.R13)
        a.AddRI(x86_64.RCX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R14, 16), x86_64.RCX) // cap

        // Copy and convert using LODSB/STOSB
        a.EmitBytes([]byte{0xFC}) // CLD
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R12, 24)) // source data
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R14, 24)) // dest data
        a.MovRR(x86_64.RCX, x86_64.R13)

        loopLabel := a.GenLabel("upper_loop")
        storeLabel := a.GenLabel("upper_store")
        doneLabel := a.GenLabel("upper_done")

        a.Label(loopLabel)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(doneLabel)

        a.EmitBytes([]byte{0xAC}) // LODSB

        // Check if 'a' <= AL <= 'z'
        a.CmpRI(x86_64.RAX, int64('a'))
        a.JL(storeLabel)
        a.CmpRI(x86_64.RAX, int64('z'))
        a.JG(storeLabel)

        // Convert to uppercase
        a.SubRI(x86_64.RAX, 32)

        a.Label(storeLabel)
        a.EmitBytes([]byte{0xAA}) // STOSB
        a.SubRI(x86_64.RCX, 1)
        a.JNZ(loopLabel)

        a.Label(doneLabel)
        fb.mkTag(rtTagStr, x86_64.R14, x86_64.RAX)

        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Trim: y_trim(str: RDI) -> tagged_str
//
// Returns a copy of the string with leading and trailing whitespace removed.
// Whitespace is any byte with value <= 32 (space, tab, newline, CR, etc.).
// ---------------------------------------------------------------------------
func genPure_Trim(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_trim", rd)
        a := fb.a

        nilLabel := a.GenLabel("trim_nil")
        allWsLabel := a.GenLabel("trim_all_ws")
        trimDoneLabel := a.GenLabel("trim_done")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)

        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // len
        a.LEA(x86_64.R14, x86_64.MemBase(x86_64.R12, 24))    // data ptr

        // Skip leading whitespace: RBX = first non-ws index
        a.MovZeroR64(x86_64.RBX) // i = 0

        skipLeadLabel := a.GenLabel("trim_skip_lead")
        leadDoneLabel := a.GenLabel("trim_lead_done")

        a.Label(skipLeadLabel)
        a.CmpRR(x86_64.RBX, x86_64.R13)
        a.JGE(allWsLabel)

        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R14, x86_64.RBX, 1, 0))
        a.CmpRI(x86_64.RAX, 33) // byte >= 33 means not whitespace
        a.JGE(leadDoneLabel)

        a.AddRI(x86_64.RBX, 1)
        a.JMP(skipLeadLabel)

        a.Label(leadDoneLabel)
        // RBX = first non-ws index (start)

        // Find trailing whitespace: R15 scans from len-1 down to start
        a.MovRR(x86_64.R15, x86_64.R13)
        a.SubRI(x86_64.R15, 1) // R15 = len - 1

        trailLabel := a.GenLabel("trim_trail")
        trailDoneLabel := a.GenLabel("trim_trail_done")

        a.Label(trailLabel)
        a.CmpRR(x86_64.R15, x86_64.RBX)
        a.JL(allWsLabel)

        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R14, x86_64.R15, 1, 0))
        a.CmpRI(x86_64.RAX, 33)
        a.JGE(trailDoneLabel)

        a.SubRI(x86_64.R15, 1)
        a.JMP(trailLabel)

        a.Label(trailDoneLabel)
        // RBX = start, R15 = end (inclusive)
        // trimmed_len = R15 - RBX + 1
        a.MovRR(x86_64.RAX, x86_64.R15)
        a.SubRR(x86_64.RAX, x86_64.RBX)
        a.AddRI(x86_64.RAX, 1) // RAX = trimmed_len

        // data_ptr = &data[RBX]
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.R14, x86_64.RBX, 1, 0))
        a.MovRR(x86_64.RSI, x86_64.RAX) // len
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(trimDoneLabel)

        a.Label(allWsLabel)
        // All whitespace: return empty string
        a.MovZeroR64(x86_64.RSI)                     // len = 0
        a.MovRR(x86_64.RDI, x86_64.R14)              // data ptr (doesn't matter for len=0)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(trimDoneLabel)

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(trimDoneLabel)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Substr: y_substr(str: RDI, start: RSI, end: RDX) -> tagged_str
//
// Returns a substring. Both start and end are tagged ints.
// Clamps start to [0, len] and end to [start, len].
// ---------------------------------------------------------------------------
func genPure_Substr(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_substr", rd)
        a := fb.a

        nilLabel := a.GenLabel("substr_nil")
        doneLabel := a.GenLabel("substr_done")
        startOkLabel := a.GenLabel("substr_start_ok")
        startClampedLabel := a.GenLabel("substr_start_clamped")
        endOkLabel := a.GenLabel("substr_end_ok")
        endClampedLabel := a.GenLabel("substr_end_clamped")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)

        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // str_len

        // GET_INT(start) → RBX
        fb.getInt(x86_64.RBX, x86_64.RSI)

        // GET_INT(end) → RAX
        fb.getInt(x86_64.RAX, x86_64.RDX)

        // Clamp start to [0, str_len]
        a.CmpRI(x86_64.RBX, 0)
        a.JGE(startOkLabel)
        a.MovZeroR64(x86_64.RBX)
        a.Label(startOkLabel)
        a.CmpRR(x86_64.RBX, x86_64.R13)
        a.JLE(startClampedLabel)
        a.MovRR(x86_64.RBX, x86_64.R13)
        a.Label(startClampedLabel)

        // Clamp end to [start, str_len]
        a.CmpRR(x86_64.RAX, x86_64.RBX)
        a.JGE(endOkLabel)
        a.MovRR(x86_64.RAX, x86_64.RBX) // end = start
        a.JMP(endClampedLabel)
        a.Label(endOkLabel)
        a.CmpRR(x86_64.RAX, x86_64.R13)
        a.JLE(endClampedLabel)
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.Label(endClampedLabel)

        // len = end - start
        a.SubRR(x86_64.RAX, x86_64.RBX)

        // RDI = data + start, RSI = len
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, 24))
        a.MovRR(x86_64.RSI, x86_64.RAX) // len
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(doneLabel)

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(doneLabel)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Contains: y_contains(str: RDI, sub: RSI) -> tagged_bool
//
// Returns true if sub is found within str, false otherwise.
// ---------------------------------------------------------------------------
func genPure_Contains(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_contains", rd)
        a := fb.a

        nilLabel := a.GenLabel("contains_nil")
        trueLabel := a.GenLabel("contains_true")
        falseLabel := a.GenLabel("contains_false")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // Extract both pointers
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI) // str header
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R13, x86_64.RSI) // sub header
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nilLabel)

        // Read lengths
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, 8)) // str_len
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R13, 8)) // sub_len

        // sub_len == 0 → true
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(trueLabel)

        // sub_len > str_len → false
        a.CmpRR(x86_64.R14, x86_64.R15)
        a.JB(falseLabel)

        // Loop: RBX = i from 0 to str_len - sub_len
        a.MovZeroR64(x86_64.RBX)

        loopLabel := a.GenLabel("contains_loop")
        notMatchLabel := a.GenLabel("contains_not_match")

        a.Label(loopLabel)
        // Check i <= str_len - sub_len
        a.MovRR(x86_64.RAX, x86_64.R14)
        a.SubRR(x86_64.RAX, x86_64.R15) // max_i = str_len - sub_len
        a.CmpRR(x86_64.RBX, x86_64.RAX)
        a.JG(falseLabel)

        // Save loop counter, compare str[i..] with sub[0..]
        a.PUSH(x86_64.RBX)
        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, 24)) // &str[i]
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R13, 24))                  // &sub[0]
        a.MovRR(x86_64.RCX, x86_64.R15)
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RCX)
        a.POP(x86_64.RBX) // restore loop counter

        a.JNE(notMatchLabel)
        a.JMP(trueLabel)

        a.Label(notMatchLabel)
        a.AddRI(x86_64.RBX, 1)
        a.JMP(loopLabel)

        a.Label(nilLabel)
        a.Label(falseLabel)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagBool, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(trueLabel)
        a.MovRM(x86_64.RAX, 1)
        fb.mkTag(rtTagBool, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StartsWith: y_starts_with(str: RDI, prefix: RSI) -> tagged_bool
//
// Returns true if str starts with prefix.
// ---------------------------------------------------------------------------
func genPure_StartsWith(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_starts_with", rd)
        a := fb.a

        falseLabel := a.GenLabel("sw_false")
        trueLabel := a.GenLabel("sw_true")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)

        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(falseLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(falseLabel)

        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(falseLabel)
        fb.getPtr(x86_64.R13, x86_64.RSI)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(falseLabel)

        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, 8)) // str_len
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R13, 8))  // prefix_len

        // prefix_len > str_len → false
        a.CmpRR(x86_64.R14, x86_64.RBX)
        a.JB(falseLabel)

        // prefix_len == 0 → true
        a.TestRR(x86_64.RBX, x86_64.RBX)
        a.JZ(trueLabel)

        // Compare str[0..prefix_len-1] with prefix[0..prefix_len-1]
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R12, 24)) // str data
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R13, 24)) // prefix data
        a.MovRR(x86_64.RCX, x86_64.RBX)
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RCX)

        a.JNE(falseLabel)
        a.JMP(trueLabel)

        a.Label(falseLabel)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagBool, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(trueLabel)
        a.MovRM(x86_64.RAX, 1)
        fb.mkTag(rtTagBool, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_EndsWith: y_ends_with(str: RDI, suffix: RSI) -> tagged_bool
//
// Returns true if str ends with suffix.
// ---------------------------------------------------------------------------
func genPure_EndsWith(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_ends_with", rd)
        a := fb.a

        falseLabel := a.GenLabel("ew_false")
        trueLabel := a.GenLabel("ew_true")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)

        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(falseLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(falseLabel)

        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(falseLabel)
        fb.getPtr(x86_64.R13, x86_64.RSI)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(falseLabel)

        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, 8)) // str_len
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R13, 8))  // suffix_len

        // suffix_len > str_len → false
        a.CmpRR(x86_64.R14, x86_64.RBX)
        a.JB(falseLabel)

        // suffix_len == 0 → true
        a.TestRR(x86_64.RBX, x86_64.RBX)
        a.JZ(trueLabel)

        // offset = str_len - suffix_len
        a.MovRR(x86_64.RAX, x86_64.R14)
        a.SubRR(x86_64.RAX, x86_64.RBX)

        // Compare str[offset..str_len-1] with suffix[0..suffix_len-1]
        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.RAX, 1, 24)) // str data + offset
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R13, 24))                  // suffix data
        a.MovRR(x86_64.RCX, x86_64.RBX)
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RCX)

        a.JNE(falseLabel)
        a.JMP(trueLabel)

        a.Label(falseLabel)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagBool, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(trueLabel)
        a.MovRM(x86_64.RAX, 1)
        fb.mkTag(rtTagBool, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Find: y_find(str: RDI, sub: RSI) -> tagged_int
//
// Returns the index of the first occurrence of sub in str, or -1 if not found.
// ---------------------------------------------------------------------------
func genPure_Find(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_find", rd)
        a := fb.a

        nilLabel := a.GenLabel("find_nil")
        foundZeroLabel := a.GenLabel("find_zero")
        notFoundLabel := a.GenLabel("find_not_found")
        foundLabel := a.GenLabel("find_found")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI) // str header
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R13, x86_64.RSI) // sub header
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, 8)) // str_len
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R13, 8)) // sub_len

        // sub_len == 0 → return 0
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(foundZeroLabel)

        // sub_len > str_len → return -1
        a.CmpRR(x86_64.R14, x86_64.R15)
        a.JB(notFoundLabel)

        // Loop: RBX = i from 0 to str_len - sub_len
        a.MovZeroR64(x86_64.RBX)

        loopLabel := a.GenLabel("find_loop")
        notMatchLabel := a.GenLabel("find_not_match")

        a.Label(loopLabel)
        a.MovRR(x86_64.RAX, x86_64.R14)
        a.SubRR(x86_64.RAX, x86_64.R15) // max_i
        a.CmpRR(x86_64.RBX, x86_64.RAX)
        a.JG(notFoundLabel)

        a.PUSH(x86_64.RBX)
        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, 24))
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R13, 24))
        a.MovRR(x86_64.RCX, x86_64.R15)
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RCX)
        a.POP(x86_64.RBX)

        a.JNE(notMatchLabel)
        a.JMP(foundLabel)

        a.Label(notMatchLabel)
        a.AddRI(x86_64.RBX, 1)
        a.JMP(loopLabel)

        a.Label(nilLabel)
        a.Label(notFoundLabel)
        a.MovRM(x86_64.RAX, int64(-1))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(foundZeroLabel)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(foundLabel)
        a.MovRR(x86_64.RAX, x86_64.RBX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrSplit: y_str_split(str: RDI, sep: RSI) -> tagged_table
//
// Splits str by separator sep. Returns a table of integer-indexed substrings.
// Edge cases: null/empty str → empty table, null sep → nil, empty sep → split
// each character.
// ---------------------------------------------------------------------------
func genPure_StrSplit(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_split", rd)
        a := fb.a

        nilLabel := a.GenLabel("split_nil")
        emptySepLabel := a.GenLabel("split_empty_sep")
        mainLoop := a.GenLabel("split_main_loop")
        noMatch := a.GenLabel("split_no_match")
        match := a.GenLabel("split_match")
        done := a.GenLabel("split_done")
        esLoop := a.GenLabel("split_es_loop")
        esDone := a.GenLabel("split_es_done")

        a.PUSH(x86_64.RBP)
        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)
        a.SubRI(x86_64.RSP, 16) // [RSP+0]=table_idx, [RSP+8]=seg_start

        // Validate str (RDI)
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI) // str header
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        // Validate sep (RSI)
        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R13, x86_64.RSI) // sep header
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nilLabel)

        // Read lengths
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, 8)) // str_len → R14
        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R13, 8)) // sep_len → R15

        // Check empty separator
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(emptySepLabel)

        // Create table with capacity str_len + 1
        a.MovRR(x86_64.RDI, x86_64.R14)
        a.AddRI(x86_64.RDI, 1)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        // RAX = tagged table
        a.MovRR(x86_64.RBP, x86_64.RAX) // RBP = table_tagged

        // Initialize state
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.RAX) // table_idx = 0
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 8), x86_64.RAX) // seg_start = 0
        a.MovZeroR64(x86_64.RBX)                                // pos = 0

        // Main loop
        a.Label(mainLoop)
        // Check: pos + sep_len > str_len → done
        a.MovRR(x86_64.RAX, x86_64.RBX) // pos
        a.AddRR(x86_64.RAX, x86_64.R15) // pos + sep_len
        a.CmpRR(x86_64.RAX, x86_64.R14) // vs str_len
        a.JA(done)

        // memcmp(str_data + pos, sep_data, sep_len)
        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize))
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R13, rtStrHeaderSize))
        a.MovRR(x86_64.RCX, x86_64.R15)
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RCX)
        a.JNE(noMatch)

        // Match found at pos
        a.Label(match)
        // Create substring: y_str_new(str_data + seg_start, pos - seg_start)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 8))       // seg_start
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.R12, x86_64.RAX, 1, rtStrHeaderSize))
        a.MovRR(x86_64.RAX, x86_64.RBX)                            // pos
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 8))       // seg_start
        a.SubRR(x86_64.RAX, x86_64.RCX)                            // len = pos - seg_start
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        // Store in table: y_tab_set(table, MK_TAG(TAG_INT, table_idx), substring)
        a.MovRR(x86_64.RDX, x86_64.RAX)                            // val = substring
        a.MovRR(x86_64.RDI, x86_64.RBP)                            // table
        a.MovMemR(x86_64.RSI, x86_64.MemBase(x86_64.RSP, 0))       // table_idx
        fb.mkTag(rtTagInt, x86_64.RSI, x86_64.RSI)                 // key
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        // Update: table_idx++, seg_start = pos + sep_len, pos = seg_start
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0))
        a.AddRI(x86_64.RAX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.RAX) // table_idx++
        a.MovRR(x86_64.RAX, x86_64.RBX)                        // pos
        a.AddRR(x86_64.RAX, x86_64.R15)                        // pos + sep_len
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 8), x86_64.RAX) // seg_start = pos + sep_len
        a.MovRR(x86_64.RBX, x86_64.RAX)                        // pos = seg_start
        a.JMP(mainLoop)

        // No match at this position
        a.Label(noMatch)
        a.AddRI(x86_64.RBX, 1) // pos++
        a.JMP(mainLoop)

        // Done: create final substring from seg_start to end
        a.Label(done)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 8))       // seg_start
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.R12, x86_64.RAX, 1, rtStrHeaderSize))
        a.MovRR(x86_64.RAX, x86_64.R14)                            // str_len
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 8))       // seg_start
        a.SubRR(x86_64.RAX, x86_64.RCX)                            // len = str_len - seg_start
        a.MovRR(x86_64.RSI, x86_64.RAX)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        a.MovRR(x86_64.RDI, x86_64.RBP)
        a.MovMemR(x86_64.RSI, x86_64.MemBase(x86_64.RSP, 0))
        fb.mkTag(rtTagInt, x86_64.RSI, x86_64.RSI)
        a.MovRR(x86_64.RDX, x86_64.RAX)
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        // Return table_tagged
        a.MovRR(x86_64.RAX, x86_64.RBP)
        a.AddRI(x86_64.RSP, 16)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        // Empty separator: split each character
        a.Label(emptySepLabel)
        a.MovRR(x86_64.RDI, x86_64.R14) // str_len
        a.AddRI(x86_64.RDI, 1)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        a.MovRR(x86_64.RBP, x86_64.RAX) // table_tagged

        a.MovZeroR64(x86_64.RBX) // i = 0
        a.Label(esLoop)
        a.CmpRR(x86_64.RBX, x86_64.R14) // i vs str_len
        a.JAE(esDone)

        // y_str_new(str_data + i, 1)
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize))
        a.MovRM(x86_64.RSI, 1)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        // y_tab_set(table, MK_TAG(TAG_INT, i), substring)
        a.MovRR(x86_64.RDX, x86_64.RAX) // val
        a.MovRR(x86_64.RDI, x86_64.RBP) // table
        a.MovRR(x86_64.RSI, x86_64.RBX) // i
        fb.mkTag(rtTagInt, x86_64.RSI, x86_64.RSI)
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        a.AddRI(x86_64.RBX, 1)
        a.JMP(esLoop)

        // Empty string for last entry
        a.Label(esDone)
        a.LEA(x86_64.RDI, x86_64.MemIndex(x86_64.R12, x86_64.R14, 1, rtStrHeaderSize))
        a.MovZeroR64(x86_64.RSI)
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")

        a.MovRR(x86_64.RDX, x86_64.RAX)
        a.MovRR(x86_64.RDI, x86_64.RBP)
        a.MovRR(x86_64.RSI, x86_64.R14)
        fb.mkTag(rtTagInt, x86_64.RSI, x86_64.RSI)
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        a.MovRR(x86_64.RAX, x86_64.RBP)
        a.AddRI(x86_64.RSP, 16)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        // nil return
        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.AddRI(x86_64.RSP, 16)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrReplace: y_str_replace(str: RDI, old: RSI, new_s: RDX) -> tagged_str
//
// Replaces all occurrences of old in str with new_s. Returns new string.
// Uses two passes: first count, then copy with substitution.
// ---------------------------------------------------------------------------
func genPure_StrReplace(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_replace", rd)
        a := fb.a

        nilLabel := a.GenLabel("repl_nil")
        noChange := a.GenLabel("repl_no_change")
        countLoop := a.GenLabel("repl_count_loop")
        countNoMatch := a.GenLabel("repl_count_no_match")
        checkCount := a.GenLabel("repl_check_count")
        copyLoop := a.GenLabel("repl_copy_loop")
        copyNoMatch := a.GenLabel("repl_copy_no_match")
        copyMatch := a.GenLabel("repl_copy_match")
        copyDone := a.GenLabel("repl_copy_done")

        a.PUSH(x86_64.RBP)
        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)
        a.SubRI(x86_64.RSP, 32) // [RSP+0]=count, [RSP+8]=new_len, [RSP+16]=old_len, [RSP+24]=dst_cursor

        // Validate str (RDI)
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI) // str header
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        // Save RDX (new_s tagged) before it gets clobbered
        a.MovRR(x86_64.R13, x86_64.RDX) // save new_s tagged

        // Validate old (RSI)
        a.TestRR(x86_64.RSI, x86_64.RSI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R14, x86_64.RSI) // old header
        a.TestRR(x86_64.R14, x86_64.R14)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R15, x86_64.MemBase(x86_64.R12, 8)) // str_len → R15
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.R14, 8)) // old_len
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 16), x86_64.RAX) // [RSP+16] = old_len

        // old_len == 0 → return str itself
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(noChange)

        // Validate new_s and get new_s header ptr into R9
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nilLabel)
        a.MovRR(x86_64.RAX, x86_64.R13)
        fb.getPtr(x86_64.RAX, x86_64.RAX) // new_s header
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RAX, 8)) // new_len
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 8), x86_64.RAX) // [RSP+8] = new_len

        // First pass: count occurrences of old in str
        a.MovZeroR64(x86_64.RAX)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.RAX) // count = 0
        a.MovZeroR64(x86_64.RBX)                                // pos = 0

        a.Label(countLoop)
        a.MovRR(x86_64.RAX, x86_64.RBX)                            // pos
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 16))     // old_len
        a.AddRR(x86_64.RAX, x86_64.RCX)                            // pos + old_len
        a.CmpRR(x86_64.RAX, x86_64.R15)                            // vs str_len
        a.JA(checkCount)

        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize))
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R14, rtStrHeaderSize))
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 16))
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RCX)
        a.JNE(countNoMatch)

        // Match: count++, advance past old
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0))
        a.AddRI(x86_64.RAX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.RAX)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 16))
        a.AddRR(x86_64.RBX, x86_64.RAX)
        a.JMP(countLoop)

        a.Label(countNoMatch)
        a.AddRI(x86_64.RBX, 1)
        a.JMP(countLoop)

        a.Label(checkCount)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(noChange)

        // Compute new total length = str_len - count*old_len + count*new_len
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 16)) // old_len
        a.IMul2RR(x86_64.RAX, x86_64.RAX)                      // count * old_len
        a.MovRR(x86_64.RBP, x86_64.R15)                        // RBP = str_len
        a.SubRR(x86_64.RBP, x86_64.RAX)                        // str_len - count*old_len
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 0))  // count
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 8))  // new_len
        a.IMul2RR(x86_64.RAX, x86_64.RAX)                      // count * new_len
        a.AddRR(x86_64.RBP, x86_64.RAX)                        // RBP = final new_len

        // Allocate: 24 + new_len + 1, aligned to 8
        a.MovRR(x86_64.RAX, x86_64.RBP)
        a.AddRI(x86_64.RAX, 25)
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

        okLabel := a.GenLabel("repl_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)
        a.JMP(nilLabel)

        a.Label(okLabel)
        a.MovRR(x86_64.R8, x86_64.RAX) // R8 = new StrHeader*

        a.MovRMMem(x86_64.MemBase(x86_64.R8, 8), x86_64.RBP) // len
        a.MovRR(x86_64.RCX, x86_64.RBP)
        a.AddRI(x86_64.RCX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.R8, 16), x86_64.RCX) // cap

        // Get new_s header ptr (reload since R9 was clobbered)
        a.MovRR(x86_64.RAX, x86_64.R13)
        fb.getPtr(x86_64.R9, x86_64.RAX) // R9 = new_s header ptr

        // Second pass: copy str to new buffer, replacing old with new_s
        // R8  = new StrHeader*
        // R9  = new_s header ptr
        // R12 = str header ptr
        // R14 = old header ptr
        // R15 = str_len
        // [RSP+8]  = new_len (per replacement)
        // [RSP+16] = old_len
        // [RSP+24] = dst_cursor (current write offset into new data)

        a.LEA(x86_64.RAX, x86_64.MemBase(x86_64.R8, rtStrHeaderSize))
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 24), x86_64.RAX) // dst_cursor = new_data
        a.MovZeroR64(x86_64.RBX) // pos = 0

        a.Label(copyLoop)
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 16))
        a.AddRR(x86_64.RAX, x86_64.RCX)
        a.CmpRR(x86_64.RAX, x86_64.R15)
        a.JA(copyDone)

        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize))
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R14, rtStrHeaderSize))
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 16))
        fb.emitMemcmp(x86_64.RSI, x86_64.RDI, x86_64.RCX)
        a.JNE(copyNoMatch)

        // Match: write new_s to dst instead
        a.Label(copyMatch)
        a.MovMemR(x86_64.RDI, x86_64.MemBase(x86_64.RSP, 24)) // dst cursor
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.R9, rtStrHeaderSize)) // new_s data
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 8)) // new_len
        skipNewCopy := a.GenLabel("repl_skip_new_copy")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(skipNewCopy)
        fb.emitMemcpy(x86_64.RDI, x86_64.RSI, x86_64.RCX)
        a.Label(skipNewCopy)

        // Advance dst_cursor by new_len
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 24))
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.RSP, 8))
        a.AddRR(x86_64.RAX, x86_64.RCX)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 24), x86_64.RAX)

        // Advance pos past old
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 16))
        a.AddRR(x86_64.RBX, x86_64.RAX) // pos += old_len
        a.JMP(copyLoop)

        // No match: copy one byte from str[pos] to dst
        a.Label(copyNoMatch)
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RSP, 24)) // dst cursor
        a.MovZX8Mem(x86_64.RCX, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize))
        a.MovRMMem(x86_64.MemBase(x86_64.RAX, 0), x86_64.RCX) // *dst = str[pos]
        a.AddRI(x86_64.RAX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 24), x86_64.RAX) // dst_cursor++
        a.AddRI(x86_64.RBX, 1) // pos++
        a.JMP(copyLoop)

        // Done: copy remaining bytes
        a.Label(copyDone)
        // remaining = str_len - pos
        a.MovRR(x86_64.RAX, x86_64.R15) // str_len
        a.SubRR(x86_64.RAX, x86_64.RBX) // remaining
        skipRestCopy := a.GenLabel("repl_skip_rest")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(skipRestCopy)

        a.MovMemR(x86_64.RDI, x86_64.MemBase(x86_64.RSP, 24)) // dst cursor
        a.LEA(x86_64.RSI, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize)) // str_data + pos
        a.MovRR(x86_64.RCX, x86_64.RAX) // remaining
        fb.emitMemcpy(x86_64.RDI, x86_64.RSI, x86_64.RCX)
        a.Label(skipRestCopy)

        // Return MK_TAG(TAG_STR, R8)
        fb.mkTag(rtTagStr, x86_64.R8, x86_64.RAX)
        a.AddRI(x86_64.RSP, 32)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        a.Label(noChange)
        // Return original str (RDI was clobbered — need to use saved tagged value)
        // Actually, RDI was the original str tagged value but we extracted ptr from it.
        // We need to reconstruct it. Since R12 still holds the str header ptr:
        // Wait, R12 = GET_PTR(RDI), so MK_TAG(TAG_STR, R12) = RDI.
        // Actually, getPtr just masks with VALUE_MASK. So RDI = (tag << 56) | R12.
        // If we do MK_TAG(TAG_STR, R12), that should give us back the original tagged value.
        fb.mkTag(rtTagStr, x86_64.R12, x86_64.RAX)
        a.AddRI(x86_64.RSP, 32)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.AddRI(x86_64.RSP, 32)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrRepeat: y_str_repeat(str: RDI, count: RSI) -> tagged_str
//
// Repeats str count times. Returns new string.
// Edge cases: null → nil, count<=0 → empty string, count==1 → str itself.
// ---------------------------------------------------------------------------
func genPure_StrRepeat(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_repeat", rd)
        a := fb.a

        nilLabel := a.GenLabel("repeat_nil")
        mkEmptyLabel := a.GenLabel("repeat_mk_empty")
        returnSelf := a.GenLabel("repeat_return_self")
        copyLoop := a.GenLabel("repeat_copy_loop")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)

        // Validate str (RDI)
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI) // str header
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // str_len

        // Get integer count from RSI (tagged int)
        fb.getInt(x86_64.RBX, x86_64.RSI) // RBX = count

        // count <= 0 → create empty string
        a.TestRR(x86_64.RBX, x86_64.RBX)
        a.JG(mkEmptyLabel) // if count > 0, skip

        // count == 0 or negative: create empty string via y_str_new(ptr, 0)
        a.LEA(x86_64.RDI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // any valid ptr
        a.MovZeroR64(x86_64.RSI)                                        // len = 0
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // count == 1 → return str itself
        a.Label(mkEmptyLabel)
        a.CmpRI(x86_64.RBX, 1)
        a.JE(returnSelf)

        // count > 1: allocate new buffer with len*count bytes
        // new_len = str_len * count
        a.MovRR(x86_64.RAX, x86_64.R13) // str_len
        a.IMul2RR(x86_64.RAX, x86_64.RBX) // str_len * count = new_len
        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = new_len

        // Allocate: 24 + new_len + 1, aligned to 8
        a.AddRI(x86_64.RAX, 25)
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

        okLabel := a.GenLabel("repeat_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)
        a.JMP(nilLabel)

        a.Label(okLabel)
        // RAX = new StrHeader*
        // Set header fields
        a.MovRMMem(x86_64.MemBase(x86_64.RAX, 8), x86_64.R14) // len = new_len
        a.MovRR(x86_64.RCX, x86_64.R14)
        a.AddRI(x86_64.RCX, 1)
        a.MovRMMem(x86_64.MemBase(x86_64.RAX, 16), x86_64.RCX) // cap

        // Copy str data count times
        // R12 = str header, R14 = new_len, RBX = count
        // RAX = new header
        a.MovRR(x86_64.R15, x86_64.RAX) // R15 = new header (save)
        a.MovZeroR64(x86_64.RAX)       // copied = 0

        a.Label(copyLoop)
        a.CmpRR(x86_64.RAX, x86_64.R14) // copied vs new_len
        repeatDone := a.GenLabel("repeat_done")
        a.JAE(repeatDone)

        // Each iteration: copy str_len bytes from str_data to new_data + copied
        a.MovRR(x86_64.RCX, x86_64.R13)                             // str_len
        a.MovMemR(x86_64.RSI, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // str data
        a.MovMemR(x86_64.RDI, x86_64.MemBase(x86_64.R15, rtStrHeaderSize)) // new data base
        a.AddRR(x86_64.RDI, x86_64.RAX)                               // new data + copied

        skipCopy := a.GenLabel("repeat_skip_copy")
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(skipCopy)
        fb.emitMemcpy(x86_64.RDI, x86_64.RSI, x86_64.RCX)
        a.Label(skipCopy)

        a.AddRR(x86_64.RAX, x86_64.R13) // copied += str_len
        a.JMP(copyLoop)

        a.Label(repeatDone)
        // Return MK_TAG(TAG_STR, R15)
        fb.mkTag(rtTagStr, x86_64.R15, x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(returnSelf)
        a.MovRR(x86_64.RAX, x86_64.RDI) // return original tagged str
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrBytes: y_str_bytes(str: RDI) -> tagged_table
//
// Returns a table mapping byte index → byte value (as tagged ints).
// ---------------------------------------------------------------------------
func genPure_StrBytes(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_bytes", rd)
        a := fb.a

        nilLabel := a.GenLabel("bytes_nil")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)

        // Validate str (RDI)
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI) // str header
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // str_len → R13

        // Create table with capacity str_len
        a.MovRR(x86_64.RDI, x86_64.R13)
        a.CALL("y_table_new")
        fb.addRelocText("y_table_new")
        // RAX = tagged table
        a.MovRR(x86_64.R14, x86_64.RAX) // R14 = table_tagged

        // Loop: for i = 0 to str_len-1
        a.MovZeroR64(x86_64.RBX) // i = 0

        loopLabel := a.GenLabel("bytes_loop")
        a.Label(loopLabel)
        a.CmpRR(x86_64.RBX, x86_64.R13) // i vs str_len
        bytesDone := a.GenLabel("bytes_done")
        a.JAE(bytesDone)

        // Read byte at str_data[i]
        a.MovZX8Mem(x86_64.RAX, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize))
        // RAX = byte value (zero-extended)
        // MK_TAG(TAG_INT, byte_value) → RDX (val)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RDX) // RDX = val

        // MK_TAG(TAG_INT, i) → RSI (key)
        a.MovRR(x86_64.RSI, x86_64.RBX)
        fb.mkTag(rtTagInt, x86_64.RSI, x86_64.RSI) // RSI = key

        // y_tab_set(table, key, val)
        a.MovRR(x86_64.RDI, x86_64.R14) // table
        a.CALL("y_tab_set")
        fb.addRelocText("y_tab_set")

        a.AddRI(x86_64.RBX, 1)
        a.JMP(loopLabel)

        a.Label(bytesDone)
        a.MovRR(x86_64.RAX, x86_64.R14) // return table
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX) // nil
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrToInt: y_str_to_int(str: RDI) -> tagged_int
//
// Parses a decimal integer from str. Returns 0 on null/empty/invalid.
// Handles optional leading '-' sign.
// ---------------------------------------------------------------------------
func genPure_StrToInt(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_to_int", rd)
        a := fb.a

        nilLabel := a.GenLabel("toi_nil")
        noNegLabel := a.GenLabel("toi_no_neg")
        loopLabel := a.GenLabel("toi_loop")
        doneLabel := a.GenLabel("toi_done")
        negateLabel := a.GenLabel("toi_negate")

        a.PUSH(x86_64.RBP)
        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        // Validate str (RDI)
        a.TestRR(x86_64.RDI, x86_64.RDI)
        a.JZ(nilLabel)
        fb.getPtr(x86_64.R12, x86_64.RDI) // str header
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(nilLabel)

        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // len → R13

        // Check empty
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(nilLabel)

        // Register allocation:
        // R12 = str header, R13 = len
        // RBX = pos, RAX = result
        // RBP = 0 (non-negative) or 1 (negative) — sign flag

        a.MovZeroR64(x86_64.RAX) // result = 0
        a.MovZeroR64(x86_64.RBX) // pos = 0
        a.MovZeroR64(x86_64.RBP) // negative flag = 0

        // Check for leading '-'
        a.MovZX8Mem(x86_64.RCX, x86_64.MemBase(x86_64.R12, rtStrHeaderSize))
        a.CmpRI(x86_64.RCX, int64('-'))
        a.JNE(noNegLabel)

        // Negative: set flag, skip '-'
        a.MovRM(x86_64.RBP, 1) // negative flag = 1
        a.AddRI(x86_64.RBX, 1) // pos = 1

        // Check if we've consumed the entire string
        a.CmpRR(x86_64.RBX, x86_64.R13)
        a.JAE(nilLabel) // just "-" → return 0

        a.Label(noNegLabel)

        // Digit parsing loop
        a.Label(loopLabel)
        a.CmpRR(x86_64.RBX, x86_64.R13) // pos vs len
        a.JAE(doneLabel)

        a.MovZX8Mem(x86_64.RCX, x86_64.MemIndex(x86_64.R12, x86_64.RBX, 1, rtStrHeaderSize))

        // Check if '0' <= c <= '9'
        a.CmpRI(x86_64.RCX, int64('0'))
        a.JB(doneLabel)
        a.CmpRI(x86_64.RCX, int64('9'))
        a.JA(doneLabel)

        // c -= '0' (RCX now holds digit value 0-9)
        a.SubRI(x86_64.RCX, int64('0'))

        // result = result * 10 + digit
        // Multiply by 10 using SHL+ADD trick:
        // result * 10 = result * 8 + result * 2 = (result << 3) + (result << 1)
        // Use R8 as temporary (caller-saved, OK since no function calls in loop)
        a.MovRR(x86_64.R8, x86_64.RAX)  // R8 = result
        a.ShlRI(x86_64.RAX, 3)          // RAX = result * 8
        a.MovRR(x86_64.R9, x86_64.R8)  // R9 = result
        a.ShlRI(x86_64.R9, 1)          // R9 = result * 2
        a.AddRR(x86_64.RAX, x86_64.R9) // RAX = result * 8 + result * 2 = result * 10
        a.AddRR(x86_64.RAX, x86_64.RCX) // RAX = result * 10 + digit

        a.AddRI(x86_64.RBX, 1) // pos++
        a.JMP(loopLabel)

        // Done: check negative flag and negate if needed
        a.Label(doneLabel)
        a.TestRR(x86_64.RBP, x86_64.RBP)
        a.JZ(negateLabel) // if negative flag == 0, skip negation

        // Negate result: RAX = -RAX
        a.NegR(x86_64.RAX)

        a.Label(negateLabel)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        a.Label(nilLabel)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RBP)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_StrToFloat: y_str_to_float(str: RDI) -> tagged_fp
//
// Parses a string as a float64. Handles:
//   - Leading whitespace (spaces, tabs, newlines)
//   - Optional sign (+/-)
//   - Integer part: digits accumulated as int64 mantissa
//   - Optional fractional part after '.'
//   - Optional exponent after 'e'/'E' with optional sign
//
// Returns MK_TAG(TAG_FP, ptr) where ptr points to an 8-byte allocation
// containing the IEEE 754 double. Returns MK_TAG(TAG_FP, 0) on failure.
//
// Register allocation:
//   R12 = data pointer (callee-saved)
//   R13 = remaining length (callee-saved)
//   R14 = mantissa (integer representation) (callee-saved)
//   R15 = negative flag (0 or 1) (callee-saved)
//   RAX  = current byte
//   RBX  = temp
//   RCX  = temp
//   XMM0 = double result
// ---------------------------------------------------------------------------
func genPure_StrToFloat(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_str_to_float", rd)
        a := fb.a

        // Pre-generate all labels so they can be referenced before definition
        notStr := a.GenLabel("stf_not_str")
        skipWSNext := a.GenLabel("stf_skip_ws_next")
        skipWSDone := a.GenLabel("stf_skip_ws_done")
        checkPlus := a.GenLabel("stf_check_plus")
        signDone := a.GenLabel("stf_sign_done")
        intDone := a.GenLabel("stf_int_done")
        afterFrac := a.GenLabel("stf_after_frac")
        expVal := a.GenLabel("stf_exp_val")
        hasExp := a.GenLabel("stf_has_exp")
        expCheckPlus := a.GenLabel("stf_exp_check_plus")
        expSignDone := a.GenLabel("stf_exp_sign_done")
        expDone := a.GenLabel("stf_exp_done")
        expApplyDone := a.GenLabel("stf_exp_done2")
        mulLoop := a.GenLabel("stf_mul_loop")
        divLoop := a.GenLabel("stf_div_loop")
        mmapOk := a.GenLabel("stf_mmap_ok")
        allocResult := a.GenLabel("stf_alloc")
        skipWS := a.GenLabel("stf_skip_ws")
        intLoop := a.GenLabel("stf_int_loop")
        fracLoop := a.GenLabel("stf_frac_loop")
        expLoop := a.GenLabel("stf_exp_loop")

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // Extract string pointer and length
        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = StrHeader*
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notStr)
        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, 8)) // R13 = length
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(notStr)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, rtStrHeaderSize)) // R12 = data ptr

        // R14 = mantissa (start at 0)
        a.MovZeroR64(x86_64.R14)
        // R15 = has_digits flag
        a.MovZeroR64(x86_64.R15)
        // RBX = fractional digit count (0 for integer part)
        a.MovZeroR64(x86_64.RBX)

        // Skip leading whitespace (space=0x20, tab=0x09, newline=0x0A, carriage return=0x0D)
        a.Label(skipWS)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(notStr) // empty after whitespace
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.CmpRI(x86_64.RAX, 0x20)
        a.JE(skipWSNext)
        a.CmpRI(x86_64.RAX, 0x09)
        a.JE(skipWSNext)
        a.CmpRI(x86_64.RAX, 0x0A)
        a.JE(skipWSNext)
        a.CmpRI(x86_64.RAX, 0x0D)
        a.JNE(skipWSDone)
        a.Label(skipWSNext)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(skipWS)
        a.Label(skipWSDone)

        // Handle optional sign
        a.MovRR(x86_64.RAX, x86_64.R12)
        a.MovZX8Mem(x86_64.RCX, x86_64.MemBase(x86_64.RAX, 0))
        a.CmpRI(x86_64.RCX, '-')
        a.JNE(checkPlus)
        // Negative: set flag and skip
        a.MovRM(x86_64.R15, 1)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(signDone)
        a.Label(checkPlus)
        a.CmpRI(x86_64.RCX, '+')
        a.JNE(signDone)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.Label(signDone)

        // Re-check length after sign
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(notStr)

        // Parse integer part digits
        a.Label(intLoop)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(intDone)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        // Check if digit: '0'(0x30) <= c <= '9'(0x39)
        a.SubRI(x86_64.RAX, 0x30)
        a.CmpRI(x86_64.RAX, 9)
        a.JA(intDone) // not a digit

        // R14 = R14 * 10 + digit
        a.MovRR(x86_64.RCX, x86_64.R14)
        a.IMul3(x86_64.R14, x86_64.RCX, 10)
        a.AddRR(x86_64.R14, x86_64.RAX)

        a.MovRM(x86_64.R15, 1) // mark has_digits
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(intLoop)
        a.Label(intDone)

        // Check for '.'
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(afterFrac)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.CmpRI(x86_64.RAX, '.')
        a.JNE(afterFrac)

        // Skip '.'
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)

        // Parse fractional digits
        a.Label(fracLoop)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(afterFrac)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.SubRI(x86_64.RAX, 0x30)
        a.CmpRI(x86_64.RAX, 9)
        a.JA(afterFrac)

        // R14 = R14 * 10 + digit (accumulate as if no decimal point)
        a.MovRR(x86_64.RCX, x86_64.R14)
        a.IMul3(x86_64.R14, x86_64.RCX, 10)
        a.AddRR(x86_64.R14, x86_64.RAX)

        a.AddRI(x86_64.RBX, 1) // increment fractional digit count
        a.MovRM(x86_64.R15, 1) // mark has_digits
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(fracLoop)
        a.Label(afterFrac)

        // Check for 'e' or 'E' (exponent)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(expVal)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.CmpRI(x86_64.RAX, 'e')
        a.JE(hasExp)
        a.CmpRI(x86_64.RAX, 'E')
        a.JNE(expVal)

        a.Label(hasExp)
        // Skip 'e'/'E'
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)

        // Optional sign for exponent
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(expVal)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.MovRM(x86_64.RCX, 0) // RCX = exponent (start 0)
        a.CmpRI(x86_64.RAX, '-')
        a.JNE(expCheckPlus)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(expSignDone)
        a.Label(expCheckPlus)
        // Check for '+' and skip
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.CmpRI(x86_64.RAX, '+')
        a.JNE(expSignDone)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.Label(expSignDone)

        // Parse exponent digits
        a.Label(expLoop)
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JZ(expDone)
        a.MovZX8Mem(x86_64.RAX, x86_64.MemBase(x86_64.R12, 0))
        a.SubRI(x86_64.RAX, 0x30)
        a.CmpRI(x86_64.RAX, 9)
        a.JA(expDone)
        // RCX = RCX * 10 + digit (RAX)
        a.IMul3(x86_64.RCX, x86_64.RCX, 10)
        a.AddRR(x86_64.RCX, x86_64.RAX)
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.R12, 1))
        a.SubRI(x86_64.R13, 1)
        a.JMP(expLoop)
        a.Label(expDone)

        a.Label(expVal)
        a.MovZeroR64(x86_64.RCX) // no exponent parsed
        a.MovZeroR64(x86_64.RAX) // positive

        // Now we have:
        //   R14 = integer mantissa (all digits concatenated)
        //   RBX = fractional digit count
        //   RCX = exponent adjustment (from e/E)
        //   R15 = negative flag (0 or 1)
        //
        // Net exponent = RCX - RBX (e.g. "1.23e4" → mantissa=123, frac_digits=2, exp=4 → net_exp=4-2=2)

        // Adjust exponent: net_exp = RCX - RBX
        a.SubRR(x86_64.RCX, x86_64.RBX)

        // Convert mantissa (R14) to double
        a.MovRR(x86_64.RAX, x86_64.R14)
        a.CVTSI2SD64(x86_64.XMM0, x86_64.RegOp(x86_64.RAX))

        // Apply negative exponent: divide by 10^|net_exp|
        // net_exp < 0 means we need to divide
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JZ(expApplyDone)

        // If RCX > 0: multiply by 10, RCX times
        // If RCX < 0: divide by 10, |RCX| times
        // Load 10.0 into XMM1
        a.MovRM64(x86_64.RAX, int64(math.Float64bits(10.0)))
        a.MovSD_RM(x86_64.XMM1, x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)))

        // Check sign of RCX
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.Jcc(x86_64.CondS, divLoop) // if negative, go to div

        // Multiply loop: XMM0 *= 10.0, RCX times
        a.Label(mulLoop)
        a.MulSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1))
        a.SubRI(x86_64.RCX, 1)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JNZ(mulLoop)
        a.JMP(expApplyDone)

        // Divide loop: XMM0 /= 10.0, |RCX| times
        a.Label(divLoop)
        a.DivSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1))
        a.AddRI(x86_64.RCX, 1) // RCX is negative, increment toward 0
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JNZ(divLoop)

        a.Label(expApplyDone)

        // Apply sign: if R15 != 0, negate XMM0
        a.TestRR(x86_64.R15, x86_64.R15)
        a.JZ(allocResult)
        // Negate by XOR with sign bit mask
        a.MovRM64(x86_64.RAX, int64(math.Float64bits(-0.0)))
        a.MovSD_RM(x86_64.XMM1, x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)))
        a.XorPS(x86_64.XMM0, x86_64.XMM1)

        a.Label(allocResult)
        // Allocate 8 bytes via mmap
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JAE(mmapOk)
        // mmap failed: return MK_TAG(TAG_FP, 0)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(mmapOk)
        // Store XMM0 to [RAX]
        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)
        // Return MK_TAG(TAG_FP, RAX)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Error path: return MK_TAG(TAG_FP, 0)
        a.Label(notStr)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabIterValid: y_tab_iter_valid(table: RDI, idx: RSI) -> tagged_bool
//
// Checks if idx points to a valid occupied entry in the table.
// Returns TrueValue or 0 (FalseValue).
// ---------------------------------------------------------------------------
func genPure_TabIterValid(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_iter_valid", rd)
        a := fb.a

        // DEBUG: write 'V' to stderr via rodata
        fb.emitLEA_RodataStr(x86_64.RSI, "V")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2)
        a.MovRM(x86_64.RDX, 1)
        a.SYSCALL()

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // Check table tag
        notTable := a.GenLabel("tiv_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Check entries pointer is not null
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(notTable)

        // GET_INT(idx) from RSI
        fb.getInt(x86_64.RAX, x86_64.RSI)

        // Bounds check: idx < 0 || idx >= capacity
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, rtTableOffCapacity))
        // If idx < 0 (signed), fail
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.Jcc(x86_64.CondS, notTable)
        // If idx >= capacity (unsigned), fail
        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.JAE(notTable)

        // Load entries and compute entry offset = idx * 32
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.ShlRI(x86_64.RAX, 5) // idx * 32

        // Check entry.occupied == ENTRY_OCCUPIED (1)
        a.MovMemR(x86_64.RCX, x86_64.MemIndex(x86_64.RBX, x86_64.RAX, 1, rtEntryOffOccupied))
        a.CmpRI(x86_64.RCX, rtEntryOccupied)
        a.JNE(notTable)

        // Return TrueValue
        a.MovRM64(x86_64.RAX, int64(rtTrueVal))
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX) // FalseValue = 0
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabIterKey: y_tab_iter_key(table: RDI, idx: RSI) -> tagged
//
// Returns the key at the iterator position, or nil if invalid.
// ---------------------------------------------------------------------------
func genPure_TabIterKey(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_iter_key", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // Check table tag
        notTable := a.GenLabel("tik_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Check entries pointer
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(notTable)

        // GET_INT(idx)
        fb.getInt(x86_64.RAX, x86_64.RSI)

        // Bounds check
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, rtTableOffCapacity))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.Jcc(x86_64.CondS, notTable)
        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.JAE(notTable)

        // Compute entry offset = idx * 32
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.ShlRI(x86_64.RAX, 5) // idx * 32

        // Check occupied == ENTRY_OCCUPIED
        a.MovMemR(x86_64.RCX, x86_64.MemIndex(x86_64.RBX, x86_64.RAX, 1, rtEntryOffOccupied))
        a.CmpRI(x86_64.RCX, rtEntryOccupied)
        a.JNE(notTable)

        // Return entry.key (at entry_offset + 0)
        a.MovMemR(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RAX, 1, rtEntryOffKey))
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX) // nil
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabIterVal: y_tab_iter_val(table: RDI, idx: RSI) -> tagged
//
// Returns the value at the iterator position, or nil if invalid.
// ---------------------------------------------------------------------------
func genPure_TabIterVal(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_iter_val", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // Check table tag
        notTable := a.GenLabel("tivl_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Check entries pointer
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(notTable)

        // GET_INT(idx)
        fb.getInt(x86_64.RAX, x86_64.RSI)

        // Bounds check
        a.MovMemR(x86_64.RCX, x86_64.MemBase(x86_64.R12, rtTableOffCapacity))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.Jcc(x86_64.CondS, notTable)
        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.JAE(notTable)

        // Compute entry offset = idx * 32
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.ShlRI(x86_64.RAX, 5)

        // Check occupied == ENTRY_OCCUPIED
        a.MovMemR(x86_64.RCX, x86_64.MemIndex(x86_64.RBX, x86_64.RAX, 1, rtEntryOffOccupied))
        a.CmpRI(x86_64.RCX, rtEntryOccupied)
        a.JNE(notTable)

        // Return entry.value (at entry_offset + 8)
        a.MovMemR(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RAX, 1, rtEntryOffValue))
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX) // nil
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabIterNext: y_tab_iter_next(table: RDI, idx: RSI) -> tagged_int
//
// Starting from idx+1, linear probes for the next occupied entry.
// Returns MK_TAG(TAG_INT, index) or MK_TAG(TAG_INT, -1) if none found.
// ---------------------------------------------------------------------------
func genPure_TabIterNext(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_iter_next", rd)
        a := fb.a

        // DEBUG: write 'N' to stderr via rodata
        fb.emitLEA_RodataStr(x86_64.RSI, "N")
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2)
        a.MovRM(x86_64.RDX, 1)
        a.SYSCALL()

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        // Check table tag
        notTable := a.GenLabel("tin_not_table")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notTable)

        // Check entries pointer
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(notTable)

        // GET_INT(idx) from RSI
        fb.getInt(x86_64.RAX, x86_64.RSI)

        // If idx < 0, set idx = -1 (normalizes -1 to -1, same as C code)
        noNeg := a.GenLabel("tin_no_neg")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.Jcc(x86_64.CondNS, noNeg)
        a.MovRM(x86_64.RAX, int64(-1))
        a.Label(noNeg)

        // idx++
        a.AddRI(x86_64.RAX, 1)

        // R13 = capacity
        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.R12, rtTableOffCapacity))

        // Probe loop: while idx < capacity
        probeLoop := a.GenLabel("tin_probe")
        a.Label(probeLoop)

        // Check idx < capacity
        a.CmpRR(x86_64.RAX, x86_64.R13)
        a.JAE(notTable) // idx >= capacity → done, return -1

        // Load entries ptr
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries))

        // Compute entry offset = idx * 32
        a.MovRR(x86_64.RCX, x86_64.RAX)
        a.ShlRI(x86_64.RCX, 5)

        // Check entries[idx].occupied == ENTRY_OCCUPIED
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied))
        a.CmpRI(x86_64.RDX, rtEntryOccupied)
        foundLabel := a.GenLabel("tin_found")
        a.JE(foundLabel)

        // idx++
        a.AddRI(x86_64.RAX, 1)
        a.JMP(probeLoop)

        // Found: return MK_TAG(TAG_INT, idx)
        a.Label(foundLabel)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        // Not found: return MK_TAG(TAG_INT, -1)
        a.Label(notTable)
        a.MovRM(x86_64.RAX, int64(-1))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_ToInt: y_to_int(val: RDI) -> tagged_int
//
// Dispatches on tag:
//   TAG_INT (1):  identity — return val
//   TAG_BOOL (2): return 1 if true, 0 if false
//   TAG_FP (3):   truncate float toward zero, return as tagged int
//   TAG_STR (4):  stub — return 0 (requires full atoi implementation)
//   default:      return 0
// ---------------------------------------------------------------------------
func genPure_ToInt(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_to_int", rd)
        a := fb.a

        // Extract tag
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)

        // TAG_INT (1): identity
        notInt := a.GenLabel("toi_not_int")
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JNE(notInt)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.RET()

        // TAG_BOOL (2)
        a.Label(notInt)
        notBool := a.GenLabel("toi_not_bool")
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JNE(notBool)

        // Extract payload
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShlRI(x86_64.RAX, 8)  // clear tag
        a.ShrRI(x86_64.RAX, 8)
        // If payload != 0, result = 1; else result = 0
        isZero := a.GenLabel("toi_bool_zero")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(isZero)
        a.MovRM(x86_64.RAX, 1)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        a.Label(isZero)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        // TAG_FP (3): convert float to int by truncating toward zero
        a.Label(notBool)
        notFp := a.GenLabel("toi_not_fp")
        a.CmpRI(x86_64.RAX, rtTagFp)
        a.JNE(notFp)

        // Extract pointer from tagged float: RAX = RDI & VALUE_MASK
        fb.getPtr(x86_64.RAX, x86_64.RDI)
        // Load 8-byte IEEE 754 double into XMM0
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)))
        // Truncate toward zero: RAX = (int64)XMM0
        a.CVTTSD2SI64(x86_64.RAX, x86_64.XMM0)
        // Tag as int and return
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        // Default (TAG_STR, etc.): return MK_TAG(TAG_INT, 0)
        // TAG_STR (4) stub: string-to-int parsing requires a full atoi implementation
        a.Label(notFp)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_ToFp: y_to_fp(val: RDI) -> tagged_fp
//
// Converts a tagged int to a tagged float.
// ---------------------------------------------------------------------------
func genPure_ToFp(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_to_fp", rd)
        a := fb.a

        a.PUSH(x86_64.R12)

        // Extract int value: R12 = GET_INT(RDI)
        fb.getInt(x86_64.R12, x86_64.RDI)

        // Convert int64 to double: XMM0 = (double)R12
        a.CVTSI2SD64(x86_64.XMM0, x86_64.RegOp(x86_64.R12))

        // Allocate 8 bytes for result via mmap
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        // Store result
        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_FpNew: y_fp_new(raw_bits: RDI) -> tagged_fp
//
// Takes raw IEEE 754 double bits in RDI, allocates 8 bytes via mmap,
// stores the bits, returns tagged pointer.
// ---------------------------------------------------------------------------
func genPure_FpNew(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fp_new", rd)
        a := fb.a

        a.PUSH(x86_64.R12)
        a.MovRR(x86_64.R12, x86_64.RDI) // save float bits

        // mmap 8 bytes
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        // RAX = pointer to 8 zeroed bytes; store float bits
        a.MovRMMem(x86_64.MemBase(x86_64.RAX, 0), x86_64.R12)

        // Return MK_TAG(TAG_FP, RAX)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genFpBinOp: helper for y_fp_add, y_fp_sub, y_fp_mul, y_fp_div
// ---------------------------------------------------------------------------
func genFpBinOp(name string, rd *rodataBuilder, emitOp func(*x86_64.Asm)) puregenFunc {
        fb := newRtFuncBuilder(name, rd)
        a := fb.a

        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        // Extract pointers from tagged values
        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = ptr_a
        fb.getPtr(x86_64.R13, x86_64.RSI) // R13 = ptr_b

        // Load float values
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.R12, 0)))
        a.MovSD_RM(x86_64.XMM1, x86_64.MemOp(x86_64.MemBase(x86_64.R13, 0)))

        // Perform operation: XMM0 = XMM0 op XMM1
        emitOp(a)

        // Allocate 8 bytes for result
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        // Store result
        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)

        // Return MK_TAG(TAG_FP, RAX)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

func genPure_FpAdd(rd *rodataBuilder) puregenFunc {
        return genFpBinOp("y_fp_add", rd, func(a *x86_64.Asm) { a.AddSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1)) })
}
func genPure_FpSub(rd *rodataBuilder) puregenFunc {
        return genFpBinOp("y_fp_sub", rd, func(a *x86_64.Asm) { a.SubSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1)) })
}
func genPure_FpMul(rd *rodataBuilder) puregenFunc {
        return genFpBinOp("y_fp_mul", rd, func(a *x86_64.Asm) { a.MulSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1)) })
}
func genPure_FpDiv(rd *rodataBuilder) puregenFunc {
        return genFpBinOp("y_fp_div", rd, func(a *x86_64.Asm) { a.DivSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1)) })
}

// ---------------------------------------------------------------------------
// genPure_FpNeg: y_fp_neg(val: RDI) -> tagged_fp
//
// Negates a float by flipping the sign bit (XOR with 0x8000000000000000).
// ---------------------------------------------------------------------------
func genPure_FpNeg(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_fp_neg", rd)
        a := fb.a

        a.PUSH(x86_64.R12)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = ptr

        // Load float value
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.R12, 0)))

        // Flip sign bit: XOR with 0x8000000000000000
        // Use stack for sign mask constant
        a.SubRI(x86_64.RSP, 8)
        a.MovZeroR64(x86_64.R12)       // clear R12
        a.OrRI(x86_64.R12, 1)           // R12 = 1
        a.ShlRI(x86_64.R12, 63)         // R12 = 0x8000000000000000
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.R12)
        a.MovSD_RM(x86_64.XMM1, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)))
        a.XorPS(x86_64.XMM0, x86_64.XMM1)

        // Allocate 8 bytes for result
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.AddRI(x86_64.RSP, 8)
        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Sqrt: y_sqrt(val: RDI) -> tagged_fp
//
// Computes square root of a float value.
// ---------------------------------------------------------------------------
func genPure_Sqrt(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_sqrt", rd)
        a := fb.a

        a.PUSH(x86_64.R12)

        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.R12, 0)))
        a.SqrtSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM0))

        // Allocate 8 bytes for result
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Floor: y_floor(val: RDI) -> tagged_fp
//
// Computes floor of a float using truncation + conditional adjust.
// floor(x) = trunc(x) if x >= trunc(x), else trunc(x) - 1
// ---------------------------------------------------------------------------
func genPure_Floor(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_floor", rd)
        a := fb.a

        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = ptr
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.R12, 0)))

        // Truncate toward zero
        a.CVTTSD2SI64(x86_64.R12, x86_64.XMM0) // R12 = trunc(x)
        a.CVTSI2SD64(x86_64.XMM1, x86_64.RegOp(x86_64.R12))  // XMM1 = (double)trunc(x)

        // Compare: if x < trunc(x) then floor = trunc(x) - 1
        // This happens for negative numbers with fractional part
        a.UCOMISD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1))
        // UCOMISD sets CF if XMM0 < XMM1, ZF if equal
        // JAE (above or equal) means XMM0 >= XMM1 (no adjustment needed)
        done := a.GenLabel("floor_done")
        a.Jcc(x86_64.CondAE, done)

        // x < trunc(x) → adjust down by 1
        a.SubRI(x86_64.R12, 1)
        a.Label(done)

        // Convert result back to double
        a.CVTSI2SD64(x86_64.XMM0, x86_64.RegOp(x86_64.R12))

        // Allocate 8 bytes for result
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Ceil: y_ceil(val: RDI) -> tagged_fp
//
// Computes ceil of a float using truncation + conditional adjust.
// ceil(x) = trunc(x) if x <= trunc(x), else trunc(x) + 1
// ---------------------------------------------------------------------------
func genPure_Ceil(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_ceil", rd)
        a := fb.a

        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.R12, 0)))

        a.CVTTSD2SI64(x86_64.R12, x86_64.XMM0) // R12 = trunc(x)
        a.CVTSI2SD64(x86_64.XMM1, x86_64.RegOp(x86_64.R12))  // XMM1 = (double)trunc(x)

        // Compare: if trunc(x) < x then ceil = trunc(x) + 1
        // UCOMISD XMM1, XMM0: CF if XMM1 < XMM0, ZF if equal
        // JBE (below or equal) means XMM1 <= XMM0 (no adjustment)
        done := a.GenLabel("ceil_done")
        a.UCOMISD(x86_64.RegOp(x86_64.XMM1), x86_64.RegOp(x86_64.XMM0))
        a.Jcc(x86_64.CondBE, done)

        // trunc(x) < x → adjust up by 1
        a.AddRI(x86_64.R12, 1)
        a.Label(done)

        a.CVTSI2SD64(x86_64.XMM0, x86_64.RegOp(x86_64.R12))

        // Allocate 8 bytes for result
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Round: y_round(val: RDI) -> tagged_fp
//
// Rounds a float: trunc(x + 0.5) for positive, trunc(x - 0.5) for negative.
// This implements round-half-away-from-zero.
// ---------------------------------------------------------------------------
func genPure_Round(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_round", rd)
        a := fb.a

        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)

        fb.getPtr(x86_64.R12, x86_64.RDI)
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.R12, 0)))

        // Store original to stack to extract sign
        a.SubRI(x86_64.RSP, 8)
        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0)), x86_64.XMM0)

        // Get sign bit: load from stack as GPR, shift right 63
        a.MovMemR(x86_64.R13, x86_64.MemBase(x86_64.RSP, 0))
        a.ShrRI(x86_64.R13, 63) // R13 = 0 (positive) or 1 (negative)

        // Build 0.5 constant: 0x3FE0000000000000
        a.MovRM64(x86_64.R12, int64(0x3FE0000000000000))
        a.MovRMMem(x86_64.MemBase(x86_64.RSP, 0), x86_64.R12)
        a.MovSD_RM(x86_64.XMM1, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 0))) // XMM1 = 0.5

        // Reload original value
        a.MovSD_RM(x86_64.XMM0, x86_64.MemOp(x86_64.MemBase(x86_64.RSP, 8))) // reload original

        // If positive: XMM0 += 0.5, if negative: XMM0 -= 0.5
        isNeg := a.GenLabel("round_neg")
        a.TestRR(x86_64.R13, x86_64.R13)
        a.JNZ(isNeg)

        // Positive path: XMM0 += 0.5
        a.AddSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1))
        doTrunc := a.GenLabel("round_trunc")
        a.JMP(doTrunc)

        a.Label(isNeg)
        // Negative path: XMM0 -= 0.5
        a.SubSD(x86_64.RegOp(x86_64.XMM0), x86_64.RegOp(x86_64.XMM1))

        a.Label(doTrunc)
        // Truncate toward zero
        a.CVTTSD2SI64(x86_64.R12, x86_64.XMM0)
        a.CVTSI2SD64(x86_64.XMM0, x86_64.RegOp(x86_64.R12))

        a.AddRI(x86_64.RSP, 8)

        // Allocate 8 bytes for result
        a.MovZeroR64(x86_64.RDI)
        a.MovRM(x86_64.RSI, 8)
        a.MovRM(x86_64.RAX, sysMmap)
        a.MovRM(x86_64.RDX, mmapRW)
        a.MovRM(x86_64.R10, mmapAnonPriv)
        a.MovRM(x86_64.R8, int64(-1))
        a.MovZeroR64(x86_64.R9)
        a.SYSCALL()

        a.MovSD_MR(x86_64.MemOp(x86_64.MemBase(x86_64.RAX, 0)), x86_64.XMM0)
        fb.mkTag(rtTagFp, x86_64.RAX, x86_64.RAX)

        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Input: y_input(prompt: RDI) -> tagged_str
//
// Reads a line from stdin. The prompt (if a non-nil, non-empty tagged_str) is
// printed to stdout first. Returns the line as a tagged string (stripped of
// the trailing newline). On EOF/error returns an empty string.
//
// Register allocation:
//   PUSH RBX, R12 (callee-saved)
//   R12 = stack buffer pointer (256 bytes)
//   RCX = byte count from SYS_read
//   RAX = scratch
//
// Algorithm:
//   1. Check tag == TAG_STR, else return nil
//   2. If prompt is non-nil/non-empty, write it to stdout (SYS_write)
//   3. SYS_read(0, buf, 255) into stack buffer
//   4. If bytes <= 0, return y_str_new("", 0)
//   5. Scan for '\n' (0x0A); length = newline pos or full count
//   6. Call y_str_new(buf, length) and return
// ---------------------------------------------------------------------------
func genPure_Input(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_input", rd)
        a := fb.a

        // --- Labels ---
        notStrLabel := a.GenLabel("input_not_str")
        doReadLabel := a.GenLabel("input_do_read")
        emptyReadLabel := a.GenLabel("input_empty_read")
        scanLoopLabel := a.GenLabel("input_scan_loop")
        scanDoneLabel := a.GenLabel("input_scan_done")
        foundNlLabel := a.GenLabel("input_found_nl")
        epilogueLabel := a.GenLabel("input_epilogue")

        // --- Prologue ---
        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.SubRI(x86_64.RSP, 256) // stack buffer for read
        a.LEA(x86_64.R12, x86_64.MemBase(x86_64.RSP, 0)) // R12 = buf ptr

        // --- Step 1: Check tag == TAG_STR (4) ---
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStrLabel)

        // --- Step 2: Print prompt if non-nil/non-empty ---
        fb.getPtr(x86_64.RAX, x86_64.RDI) // RAX = StrHeader*
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(doReadLabel) // nil prompt → skip

        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RAX, 8)) // RDX = len
        a.TestRR(x86_64.RDX, x86_64.RDX)
        a.JZ(doReadLabel) // empty prompt → skip

        // Print prompt: sys_write(1, data_ptr, len)
        a.MovRM(x86_64.RSI, rtStrHeaderSize) // RSI = 24
        a.AddRR(x86_64.RSI, x86_64.RAX)      // RSI = header + 24 = data ptr
        a.MovRM(x86_64.RAX, sysWrite)         // syscall number
        a.MovRM(x86_64.RDI, 1)                // fd = stdout
        a.SYSCALL()

        // --- Step 3: Read from stdin ---
        a.Label(doReadLabel)
        a.MovRM(x86_64.RAX, sysRead)    // syscall number = 0
        a.MovRM(x86_64.RDI, 0)          // fd = stdin
        a.MovRR(x86_64.RSI, x86_64.R12) // buf ptr
        a.MovRM(x86_64.RDX, 255)        // max bytes to read
        a.SYSCALL()
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = bytes read

        // --- Step 4: Check for EOF/error ---
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JLE(emptyReadLabel) // bytes <= 0 → return empty string

        // --- Step 5: Scan for '\n' (0x0A) ---
        a.MovZeroR64(x86_64.RAX) // RAX = offset = 0

        a.Label(scanLoopLabel)
        a.CmpRR(x86_64.RAX, x86_64.RCX) // offset >= count?
        a.JGE(scanDoneLabel)             // yes → use full count

        a.MovZX8Mem(x86_64.RBX, x86_64.MemIndex(x86_64.R12, x86_64.RAX, 1, 0))
        a.CmpRI(x86_64.RBX, 0x0A) // is it '\n'?
        a.JE(foundNlLabel)

        a.AddRI(x86_64.RAX, 1) // offset++
        a.JMP(scanLoopLabel)

        // Found newline: RAX = position of '\n' = length of line
        a.Label(foundNlLabel)

        // scan_done: RAX = length (either newline pos or full count)
        a.Label(scanDoneLabel)
        a.MovRR(x86_64.RDI, x86_64.R12) // RDI = buf ptr
        a.MovRR(x86_64.RSI, x86_64.RAX) // RSI = length
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(epilogueLabel)

        // --- Empty read: return y_str_new(buf, 0) ---
        a.Label(emptyReadLabel)
        a.MovRR(x86_64.RDI, x86_64.R12) // RDI = buf ptr
        a.MovZeroR64(x86_64.RSI)        // RSI = 0
        a.CALL("y_str_new")
        fb.addRelocText("y_str_new")
        a.JMP(epilogueLabel)

        // --- Not a string tag: return nil ---
        a.Label(notStrLabel)
        a.MovZeroR64(x86_64.RAX) // nil

        // --- Epilogue ---
        a.Label(epilogueLabel)
        a.AddRI(x86_64.RSP, 256)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Len: y_len(val: RDI) -> tagged_int
//
// Returns length of string or table count as tagged int.
//   TAG_STR (4):   read StrHeader->len at offset +8
//   TAG_TABLE (5): read TableHeader->count at offset +0
//   default:       return 0
// ---------------------------------------------------------------------------
func genPure_Len(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_len", rd)
        a := fb.a

        // Extract tag
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)

        // TAG_STR (4)
        notStr := a.GenLabel("len_not_str")
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // Extract pointer
        fb.getPtr(x86_64.RAX, x86_64.RDI)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        strNull := a.GenLabel("len_str_null")
        a.JZ(strNull)

        // Read len at offset +8
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RAX, 8))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        a.Label(strNull)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        // TAG_TABLE (5)
        a.Label(notStr)
        notTable := a.GenLabel("len_not_table")
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notTable)

        // Extract pointer
        fb.getPtr(x86_64.RAX, x86_64.RDI)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        tabNull := a.GenLabel("len_tab_null")
        a.JZ(tabNull)

        // Read count at offset +0
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RAX, rtTableOffCount))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        a.Label(tabNull)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        // Default: return 0
        a.Label(notTable)
        a.MovZeroR64(x86_64.RAX)
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_TabGetValType: y_tab_get_val_type(table: RDI, key: RSI) -> tagged_str
//
// Stub: returns nil (no implementation yet).
// ---------------------------------------------------------------------------
func genPure_TabGetValType(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_tab_get_val_type", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)
        a.PUSH(x86_64.R13)
        a.PUSH(x86_64.R14)
        a.PUSH(x86_64.R15)

        // Check table tag
        notFound := a.GenLabel("tgvt_not_found")
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagTable)
        a.JNE(notFound)

        // Extract table pointer
        fb.getPtr(x86_64.R12, x86_64.RDI) // R12 = TableHeader*
        a.TestRR(x86_64.R12, x86_64.R12)
        a.JZ(notFound)

        // Read mask (keep in R14 throughout — never clobber)
        a.MovMemR(x86_64.R14, x86_64.MemBase(x86_64.R12, rtTableOffMask))

        // Hash the key: save RSI (original key) and callee-saves across call
        a.PUSH(x86_64.RSI) // save original key
        a.PUSH(x86_64.R12) // save table ptr
        a.PUSH(x86_64.R14) // save mask
        a.PUSH(x86_64.R15) // alignment
        a.PUSH(x86_64.R13) // alignment
        a.MovRR(x86_64.RDI, x86_64.RSI) // key → RDI
        a.CALL("pure_hash_tagged")
        fb.addRelocText("pure_hash_tagged")
        // Save hash to RBX before POPs clobber R13.
        a.MovRR(x86_64.RBX, x86_64.RAX) // RBX = hash (temp)
        a.POP(x86_64.R13)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14) // restore mask
        a.POP(x86_64.R12) // restore table ptr
        a.POP(x86_64.RSI) // restore original key
        a.MovRR(x86_64.R13, x86_64.RBX) // R13 = hash (restored)

        // idx = hash & mask
        a.MovRR(x86_64.RAX, x86_64.R13)
        a.AndRR(x86_64.RAX, x86_64.R14) // RAX = idx

        // Probe loop
        probeLoop := a.GenLabel("tgvt_probe")
        a.Label(probeLoop)

        // Load entries ptr and compute entry byte offset each iteration
        a.MovMemR(x86_64.RBX, x86_64.MemBase(x86_64.R12, rtTableOffEntries)) // RBX = entries
        a.MovRR(x86_64.RCX, x86_64.RAX) // RCX = idx
        a.ShlRI(x86_64.RCX, 5)          // RCX = idx * 32

        // Read occupied flag using proper indexed addressing
        a.MovMemR(x86_64.R15, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffOccupied))

        // Empty → not found (mask guarantees wrap-around)
        a.CmpRI(x86_64.R15, rtEntryEmpty)
        a.JE(notFound)

        // Tombstone → skip
        tgvtAdvanceNoPop := a.GenLabel("tgvt_adv")
        a.CmpRI(x86_64.R15, rtEntryOccupied)
        a.JNE(tgvtAdvanceNoPop)

        // Occupied: check hash
        a.MovMemR(x86_64.RDX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffHash))
        a.CmpRR(x86_64.RDX, x86_64.R13)
        a.JNE(tgvtAdvanceNoPop)

        // Hash matches: check key equality via pure_values_equal
        a.PUSH(x86_64.RAX) // save idx
        a.PUSH(x86_64.RBX) // save entries
        a.PUSH(x86_64.RCX) // save entry offset
        a.MovRR(x86_64.RDI, x86_64.RSI) // original key → RDI
        a.MovMemR(x86_64.RSI, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffKey)) // entry key → RSI
        a.CALL("pure_values_equal")
        fb.addRelocText("pure_values_equal")
        // RAX = 1 if equal, 0 if not
        tgvtAdvanceFromCall := a.GenLabel("tgvt_adv_from_call")
        a.TestRR(x86_64.RAX, x86_64.RAX)
        a.JZ(tgvtAdvanceFromCall) // NOT equal → continue probing (3 extra on stack)

        // Key matches! Pop saved state, extract value's tag, return MK_TAG(TAG_INT, tag_byte).
        a.POP(x86_64.RCX)   // restore entry offset
        a.POP(x86_64.RBX)   // restore entries
        a.POP(x86_64.RAX)   // restore idx (unused but balanced)
        a.MovMemR(x86_64.RAX, x86_64.MemIndex(x86_64.RBX, x86_64.RCX, 1, rtEntryOffValue)) // load value
        a.ShrRI(x86_64.RAX, 56) // extract tag byte
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX) // MK_TAG(TAG_INT, tag_byte)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(tgvtAdvanceFromCall)
        // 3 extra pushes on stack: pop them
        a.POP(x86_64.RCX)
        a.POP(x86_64.RBX)
        a.POP(x86_64.RAX)
        // fall through to advance

        a.Label(tgvtAdvanceNoPop)
        // idx = (idx + 1) & mask  (R14 = mask, always valid)
        a.AddRI(x86_64.RAX, 1)
        a.AndRR(x86_64.RAX, x86_64.R14)
        a.JMP(probeLoop)

        a.Label(notFound)
        // Return MK_TAG(TAG_INT, -1)
        a.MovRM64(x86_64.RAX, int64(-1))
        fb.mkTag(rtTagInt, x86_64.RAX, x86_64.RAX)
        a.POP(x86_64.R15)
        a.POP(x86_64.R14)
        a.POP(x86_64.R13)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Panic: y_panic(msg: RDI) -> void
//
// Prints "panic: " + optional string message to stderr, then exit(1).
// If msg is a tagged string (TAG_STR), prints the string data.
// Otherwise (nil, other types), prints just "panic: \n".
//
// C equivalent:
//   void y_panic(uint64_t msg) {
//       raw_write(2, "panic: ", 7);
//       if (GET_TAG(msg) == TAG_STR) {
//           StrHeader *h = (StrHeader *)GET_PTR(msg);
//           if (h) raw_write(2, h->data, (size_t)h->len);
//       }
//       raw_write(2, "\n", 1);
//       raw_exit(1);
//   }
// ---------------------------------------------------------------------------
func genPure_Panic(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_panic", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.MovRR(x86_64.RBX, x86_64.RDI) // RBX = msg (callee-saved)

        rd.add("panic: ")
        rd.add("\n")

        // Write "panic: " to stderr (7 bytes)
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "panic: ")
        a.MovRM(x86_64.RDX, 7)
        a.SYSCALL()

        // Check if msg is a tagged string
        notStr := a.GenLabel("panic_not_str")
        a.MovRR(x86_64.RAX, x86_64.RBX)
        a.ShrRI(x86_64.RAX, 56)
        a.CmpRI(x86_64.RAX, rtTagStr)
        a.JNE(notStr)

        // Extract pointer, check non-null
        fb.getPtr(x86_64.RAX, x86_64.RBX)
        a.TestRR(x86_64.RAX, x86_64.RAX)
        skipStr := a.GenLabel("panic_skip_str")
        a.JZ(skipStr)

        // Read string length
        a.MovMemR(x86_64.RDX, x86_64.MemBase(x86_64.RAX, 8)) // h->len
        a.TestRR(x86_64.RDX, x86_64.RDX)
        a.JZ(skipStr)

        // Write string data: sys_write(2, h->data, len)
        a.LEA(x86_64.RSI, x86_64.MemBase(x86_64.RAX, rtStrHeaderSize))
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        a.SYSCALL()

        a.Label(skipStr)

        // Write "\n" to stderr (1 byte)
        a.Label(notStr)
        a.MovRM(x86_64.RAX, sysWrite)
        a.MovRM(x86_64.RDI, 2) // stderr
        fb.emitLEA_RodataStr(x86_64.RSI, "\n")
        a.MovRM(x86_64.RDX, 1)
        a.SYSCALL()

        // exit(1)
        a.MovRM(x86_64.RAX, sysExit)
        a.MovRM(x86_64.RDI, 1)
        a.SYSCALL()

        // unreachable
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_Assert: y_assert(condition: RDI, msg: RSI) -> void
//
// Checks if condition is truthy. Truthy means:
//   - TAG_BOOL with non-zero payload (true)
//   - TAG_INT with non-zero payload (non-zero integer)
// If falsy, calls y_panic(msg) to report the failure.
//
// C equivalent:
//   void y_assert(uint64_t cond, uint64_t msg) {
//       int tag = GET_TAG(cond);
//       uint64_t payload = cond & VALUE_MASK;
//       int is_true = (tag == TAG_BOOL && payload != 0) ||
//                     (tag == TAG_INT && payload != 0);
//       if (!is_true) y_panic(msg);
//   }
// ---------------------------------------------------------------------------
func genPure_Assert(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_assert", rd)
        a := fb.a

        // Extract tag from condition (RDI)
        a.MovRR(x86_64.RAX, x86_64.RDI)
        a.ShrRI(x86_64.RAX, 56) // RAX = tag

        // Check TAG_BOOL (2)
        isTrue := a.GenLabel("assert_true")
        notBool := a.GenLabel("assert_not_bool")
        a.CmpRI(x86_64.RAX, rtTagBool)
        a.JNE(notBool)
        // Extract payload, check non-zero
        a.MovRR(x86_64.RCX, x86_64.RDI)
        a.ShlRI(x86_64.RCX, 8)  // clear tag
        a.ShrRI(x86_64.RCX, 8)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JNZ(isTrue)
        // false -> panic
        a.JMP("assert_panic")

        a.Label(notBool)
        // Check TAG_INT (1)
        a.CmpRI(x86_64.RAX, rtTagInt)
        a.JNE("assert_panic")
        // Extract payload, check non-zero
        a.MovRR(x86_64.RCX, x86_64.RDI)
        a.ShlRI(x86_64.RCX, 8)  // clear tag
        a.ShrRI(x86_64.RCX, 8)
        a.TestRR(x86_64.RCX, x86_64.RCX)
        a.JNZ(isTrue)
        // Zero int -> panic (fall through)

        // Panic: call y_panic(msg). msg is in RSI, y_panic expects it in RDI.
        assertPanic := a.GenLabel("assert_panic")
        a.Label(assertPanic)
        a.MovRR(x86_64.RDI, x86_64.RSI) // msg → RDI
        a.CALL("y_panic")
        fb.addRelocText("y_panic")

        a.Label(isTrue)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_ClosureNew: y_closure_new(n_captures: RDI) -> raw_ptr
//
// Allocates a closure struct on the heap via mmap.
// Closure layout: [fn_ptr: u64, n_captures: u64, capture_0: u64, ...]
// Total size: (2 + n_captures) * 8 bytes, rounded up to page size.
// ---------------------------------------------------------------------------

func genPure_ClosureNew(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_closure_new", rd)
        a := fb.a

        a.PUSH(x86_64.RBX)
        a.PUSH(x86_64.R12)

        // Calculate size: (2 + RDI) * 8, save in R12
        a.MovRR(x86_64.R12, x86_64.RDI) // R12 = n_captures
        a.AddRI(x86_64.R12, 2)          // R12 = 2 + n_captures
        a.ShlRI(x86_64.R12, 3)          // R12 = size in bytes

        // mmap(NULL, size, PROT_READ|PROT_WRITE, MAP_ANON|MAP_PRIVATE, -1, 0)
        a.MovRM(x86_64.RAX, sysMmap)           // syscall number = 9
        a.MovZeroR64(x86_64.RDI)             // addr = NULL
        a.MovRR(x86_64.RSI, x86_64.R12)      // length = size
        a.MovRM(x86_64.RDX, mmapRW)           // prot
        a.MovRM(x86_64.R10, mmapAnonPriv)      // flags
        a.MovRM(x86_64.R8, int64(-1))          // fd
        a.MovZeroR64(x86_64.R9)                // offset
        a.SYSCALL()

        // Check for error (mmap returns -errno on failure, mapped address on success)
        okLabel := a.GenLabel("cn_ok")
        a.CmpRI(x86_64.RAX, int64(-4096))
        a.JB(okLabel)
        // On error, return 0
        a.MovZeroR64(x86_64.RAX)
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        a.Label(okLabel)
        // RAX = mmap result (pointer to allocated memory)
        // mmap on Linux already zeroes pages (MAP_ANON), so no need to zero manually.

        // Return the mmap pointer in RAX
        a.POP(x86_64.R12)
        a.POP(x86_64.RBX)
        a.RET()

        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_ClosureSet: y_closure_set(closure: RDI, index: RSI, value: RDX) -> void
//
// Stores a 64-bit value into the closure struct at the given index.
// ---------------------------------------------------------------------------

func genPure_ClosureSet(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_closure_set", rd)
        a := fb.a

        // Compute address: RDI + RSI * 8
        a.ShlRI(x86_64.RSI, 3) // index * 8
        a.AddRR(x86_64.RDI, x86_64.RSI) // base + offset

        // Store [RDI], RDX
        a.MovRMMem(x86_64.MemBase(x86_64.RDI, 0), x86_64.RDX)

        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_ClosureGet: y_closure_get(closure: RDI, index: RSI) -> value
//
// Loads a 64-bit value from the closure struct at the given index.
// ---------------------------------------------------------------------------

func genPure_ClosureGet(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_closure_get", rd)
        a := fb.a

        // Compute address: RDI + RSI * 8
        a.ShlRI(x86_64.RSI, 3) // index * 8
        a.AddRR(x86_64.RDI, x86_64.RSI) // base + offset

        // Load [RDI] -> RAX
        a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RDI, 0))

        a.RET()
        return fb.finalize()
}

// ---------------------------------------------------------------------------
// genPure_ClosureTrampoline: y_closure_trampoline(closure: RDI, ...) -> result
//
// Trampoline for calling closures across function boundaries.
// RDI = tagged closure value (TagFunc)
// RSI, RDX, RCX, R8, R9, ... = user arguments (SysV ABI)
//
// This function:
// 1. Strips the tag from RDI to get the raw closure pointer
// 2. Reads fn_ptr from closure[0]
// 3. Replaces RDI with raw closure pointer (as __env_ptr for the target function)
// 4. Tail-jumps to fn_ptr (user args already in correct registers)
// ---------------------------------------------------------------------------

func genPure_ClosureTrampoline(rd *rodataBuilder) puregenFunc {
        fb := newRtFuncBuilder("y_closure_trampoline", rd)
        a := fb.a

        // Strip tag from RDI to get raw closure ptr.
        // The tag occupies bits 63-56. We clear it with AND mask 0x00FFFFFFFFFFFFFF.
        // Use RBX as temp (callee-saved, but we PUSH/POP).
        a.PUSH(x86_64.RBX)
        a.MovRR(x86_64.RBX, x86_64.RDI) // RBX = tagged closure
        a.AndRI(x86_64.RBX, int64(0x00FFFFFFFFFFFFFF)) // RBX = raw closure ptr

        // Load fn_ptr from closure[0] into R11 (indirect jump target)
        a.MovMemR(x86_64.R11, x86_64.MemBase(x86_64.RBX, 0)) // R11 = fn_ptr

        // Replace RDI with raw closure ptr (__env_ptr for the anon function)
        a.MovRR(x86_64.RDI, x86_64.RBX) // RDI = __env_ptr

        a.POP(x86_64.RBX)

        // Tail call: JMP R11 (not CALL — we want RET in the target to return
        // directly to y_closure_trampoline's caller).
        a.JMPReg(x86_64.R11)

        return fb.finalize()
}
