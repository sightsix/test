package x86_64

import (
        "encoding/binary"
        "fmt"
        "math"
)

// ========== Operand Types ==========

// OpKind classifies an instruction operand.
type OpKind int

const (
        OpNone OpKind = iota
        OpReg        // register operand
        OpMem        // memory operand (address)
        OpImm        // immediate operand
        OpLabel      // label reference (for jumps/calls)
)

// Operand represents an x86-64 instruction operand.
type Operand struct {
        Kind     OpKind
        Reg      Reg    // for OpReg
        Mem      MemRef // for OpMem
        Imm      int64  // for OpImm
        Label    string // for OpLabel
        ImmBytes int    // immediate size hint (1, 2, 4, 8)
}

// RegOp creates a register operand.
func RegOp(r Reg) Operand {
        return Operand{Kind: OpReg, Reg: r}
}

// MemOp creates a memory operand.
func MemOp(m MemRef) Operand {
        return Operand{Kind: OpMem, Mem: m}
}

// ImmOp creates an immediate operand (32-bit by default).
func ImmOp(v int64) Operand {
        return Operand{Kind: OpImm, Imm: v, ImmBytes: 4}
}

// Imm8 creates an 8-bit immediate operand.
func Imm8(v int64) Operand {
        return Operand{Kind: OpImm, Imm: v, ImmBytes: 1}
}

// Imm32 creates a 32-bit immediate operand.
func Imm32(v int64) Operand {
        return Operand{Kind: OpImm, Imm: v, ImmBytes: 4}
}

// Imm64 creates a 64-bit immediate operand (MOV only).
func Imm64(v int64) Operand {
        return Operand{Kind: OpImm, Imm: v, ImmBytes: 8}
}

// LabelOp creates a label operand for jumps/calls.
func LabelOp(name string) Operand {
        return Operand{Kind: OpLabel, Label: name}
}

// MemRef describes a memory reference: [base + index*scale + displacement].
// Any field can be zero/nil to indicate it is not used.
type MemRef struct {
        Base       Reg  // base register (e.g., RBP)
        Index      Reg  // index register (e.g., RAX)
        Scale      int  // scale factor: 1, 2, 4, or 8
        Displ      int32 // displacement
        HasBase    bool
        HasIndex   bool
        HasDispl   bool
}

// Mem creates a memory reference with just a displacement (RIP-relative addressing).
func MemDispl(d int32) MemRef {
        return MemRef{Displ: d, HasDispl: true}
}

// MemBase creates a simple [base + displacement] memory reference.
func MemBase(base Reg, displ int32) MemRef {
        return MemRef{Base: base, HasBase: true, Displ: displ, HasDispl: displ != 0}
}

// MemIndex creates a [base + index*scale + displacement] memory reference.
func MemIndex(base, index Reg, scale int, displ int32) MemRef {
        return MemRef{
                Base: base, HasBase: true,
                Index: index, HasIndex: true,
                Scale: scale,
                Displ: displ, HasDispl: displ != 0,
        }
}

// MemSIB creates a [displacement] memory reference with SIB byte (for RSP-based or index-based).
func MemSIB(base Reg, displ int32) MemRef {
        return MemRef{Base: base, HasBase: true, Displ: displ, HasDispl: true}
}

// ========== Assembler ==========

// Asm accumulates x86-64 machine code bytes and manages fixups for labels.
type Asm struct {
        code    []byte
        fixups  []Fixup
        labels  map[string]int // label name -> offset in code
        counter int             // for anonymous labels
}

// Fixup represents a location in the code that needs patching when a label is resolved.
type Fixup struct {
        Offset   int    // offset in code where the fixup is
        Label    string // label name
        Size     int    // 1, 2, or 4 bytes for the fixup
        RelKind  RelKind
}

// RelKind is the kind of relocation/fixup.
type RelKind int

const (
        RelAbs32  RelKind = iota // absolute 32-bit displacement (conditional jumps)
        RelRip32                 // RIP-relative 32-bit (near jumps, calls, LEA)
)

// NewAsm creates a new assembler.
func NewAsm() *Asm {
        return &Asm{
                code:   make([]byte, 0, 256),
                labels: make(map[string]int),
        }
}

// Bytes returns the assembled machine code.
func (a *Asm) Bytes() []byte { return a.code }

// Len returns the current code length.
func (a *Asm) Len() int { return len(a.code) }

// Offset returns the current offset (alias for Len).
func (a *Asm) Offset() int { return len(a.code) }

// Label defines a label at the current code offset.
func (a *Asm) Label(name string) {
        if _, ok := a.labels[name]; ok {
                panic(fmt.Sprintf("asm: duplicate label %q", name))
        }
        a.labels[name] = len(a.code)
}

// ResolveFixups patches all outstanding fixups for a newly-defined label.
func (a *Asm) ResolveFixups(name string) {
        target, ok := a.labels[name]
        if !ok {
                return
        }
        for i, f := range a.fixups {
                if f.Label != name {
                        continue
                }
                var rel int32
                switch f.RelKind {
                case RelAbs32:
                        rel = int32(target) - int32(f.Offset+f.Size)
                case RelRip32:
                        rel = int32(target) - int32(f.Offset+f.Size)
                }
                binary.LittleEndian.PutUint32(a.code[f.Offset:f.Offset+f.Size], uint32(rel))
                a.fixups[i].Label = "" // mark resolved
        }
}

// emit adds raw bytes to the code stream.
func (a *Asm) emit(b ...byte) {
        a.code = append(a.code, b...)
}

// emitU16 writes a 16-bit little-endian value.
func (a *Asm) emitU16(v uint16) {
        buf := make([]byte, 2)
        binary.LittleEndian.PutUint16(buf, v)
        a.code = append(a.code, buf...)
}

// emitU32 writes a 32-bit little-endian value.
func (a *Asm) emitU32(v uint32) {
        buf := make([]byte, 4)
        binary.LittleEndian.PutUint32(buf, v)
        a.code = append(a.code, buf...)
}

// emitU64 writes a 64-bit little-endian value.
func (a *Asm) emitU64(v uint64) {
        buf := make([]byte, 8)
        binary.LittleEndian.PutUint64(buf, v)
        a.code = append(a.code, buf...)
}

// emitI32 writes a signed 32-bit little-endian value.
func (a *Asm) emitI32(v int32) {
        a.emitU32(uint32(v))
}

// emitI16 writes a signed 16-bit little-endian value.
func (a *Asm) emitI16(v int16) {
        a.emitU16(uint16(v))
}

// emitI8 writes a signed 8-bit value.
func (a *Asm) emitI8(v int8) {
        a.code = append(a.code, byte(v))
}

// addFixup adds a fixup at the current offset.
func (a *Asm) addFixup(label string, size int, kind RelKind) {
        a.fixups = append(a.fixups, Fixup{
                Offset:  a.Offset(),
                Label:   label,
                Size:    size,
                RelKind: kind,
        })
}

// GenLabel generates a unique anonymous label name.
func (a *Asm) GenLabel(prefix string) string {
        a.counter++
        return fmt.Sprintf(".L%s_%d", prefix, a.counter)
}

// Fixups returns the list of unresolved fixups (for diagnostics).
func (a *Asm) Fixups() []Fixup {
        out := make([]Fixup, 0, len(a.fixups))
        for _, f := range a.fixups {
                if f.Label != "" {
                        out = append(out, f)
                }
        }
        return out
}

// Labels returns all defined labels.
func (a *Asm) Labels() map[string]int {
        return a.labels
}

// ========== REX Prefix Encoding ==========

// REX prefix bits.
const (
        rexW = 0x08 // 64-bit operand size
        rexR = 0x04 // extension of ModR/M reg field
        rexX = 0x02 // extension of SIB index field
        rexB = 0x01 // extension of ModR/M r/m field or SIB base
        rexBase = 0x40
)

// needsREX determines if a REX prefix is needed for the given registers.
func needsREX(reg, rm Reg) bool {
        if reg.Extended || rm.Extended {
                return true
        }
        // REX is needed for SPL, BPL, SIL, DIL (8-bit registers with code >= 4)
        if reg.Size == 8 && reg.Code >= 4 {
                return true
        }
        if rm.Size == 8 && rm.Code >= 4 {
                return true
        }
        return false
}

// needsREXMem determines if a REX prefix is needed for a register + memory operand.
func needsREXMem(reg Reg, m MemRef) bool {
        if reg.Extended {
                return true
        }
        if m.HasBase && m.Base.Extended {
                return true
        }
        if m.HasIndex && m.Index.Extended {
                return true
        }
        return false
}

// needsREXMemOnly determines if a REX prefix is needed for a memory-only operand.
func needsREXMemOnly(m MemRef) bool {
        if m.HasBase && m.Base.Extended {
                return true
        }
        if m.HasIndex && m.Index.Extended {
                return true
        }
        return false
}

// buildREX constructs a REX prefix byte from the individual bits.
func buildREX(w, r, x, b bool) byte {
        var v byte = rexBase
        if w {
                v |= rexW
        }
        if r {
                v |= rexR
        }
        if x {
                v |= rexX
        }
        if b {
                v |= rexB
        }
        return v
}

// emitREX emits a REX prefix. If w is false and no extension bits are set,
// no REX prefix is emitted (it's optional in that case).
func (a *Asm) emitREX(w bool, r, x, b bool) {
        if w || r || x || b {
                a.emit(buildREX(w, r, x, b))
        }
}

// emitREXReg emits a REX prefix for reg-reg operations.
func (a *Asm) emitREXReg(w bool, reg, rm Reg) {
        a.emitREX(w, reg.Extended, false, rm.Extended)
}

// emitREXRegImm emits a REX prefix for reg-imm operations (no r/m extension).
func (a *Asm) emitREXRegImm(w bool, reg Reg) {
        a.emitREX(w, reg.Extended, false, false)
}

// emitREXMem emits a REX prefix for reg-mem operations.
func (a *Asm) emitREXMem(w bool, reg Reg, m MemRef) {
        r := reg.Extended
        var x, b bool
        if m.HasIndex {
                x = m.Index.Extended
        }
        if m.HasBase {
                b = m.Base.Extended
        }
        a.emitREX(w, r, x, b)
}

// emitREXMemOnly emits a REX prefix for memory-only operands (no reg field).
func (a *Asm) emitREXMemOnly(m MemRef) {
        var x, b bool
        if m.HasIndex {
                x = m.Index.Extended
        }
        if m.HasBase {
                b = m.Base.Extended
        }
        a.emitREX(false, false, x, b)
}

// ========== ModR/M and SIB Encoding ==========

// ModR/M fields.
const (
        modIndirect  = 0x00 // [reg]
        modDisp8     = 0x40 // [reg + disp8]
        modDisp32    = 0x80 // [reg + disp32]
        modReg       = 0xC0 // reg (no memory)
)

// modRM constructs a ModR/M byte: mod in bits 7-6, reg in bits 5-3, rm in bits 2-0.
// The mod parameter should be one of modIndirect/modDisp8/modDisp32/modReg.
func modRM(mod, reg, rm byte) byte {
        return mod | (reg&0x07)<<3 | (rm & 0x07)
}

// sib constructs a SIB byte: scale in bits 7-6, index in bits 5-3, base in bits 2-0.
func sib(scale, index, base byte) byte {
        return (scale & 0x03) << 6 | (index & 0x07) << 3 | (base & 0x07)
}

// scaleEncode converts a scale factor (1, 2, 4, 8) to its 2-bit encoding.
func scaleEncode(s int) byte {
        switch s {
        case 1:
                return 0
        case 2:
                return 1
        case 4:
                return 2
        case 8:
                return 3
        default:
                return 0
        }
}

// isRSPorR12 returns true if the register is RSP or R12 (which requires a SIB byte).
func isRSPorR12(r Reg) bool {
        return r.Code == 4 && r.Class == RegGPR && r.Extended == (r.Code == 4 && r.Name == "r12")
}

// isRBPorR13 returns true if the register is RBP or R13 (which requires a disp8/32 with mod=00).
func isRBPorR13(r Reg) bool {
        return r.Code == 5 && r.Class == RegGPR
}

// emitModRMReg emits ModR/M byte for register-register addressing.
func (a *Asm) emitModRMReg(reg, rm Reg) {
        a.emit(modRM(modReg, reg.Code, rm.Code))
}

// emitModRMMem emits ModR/M (and possibly SIB) bytes for a memory operand with a register destination.
func (a *Asm) emitModRMMem(reg Reg, m MemRef) {
        if !m.HasBase && !m.HasIndex {
                // Absolute address: use [RIP + disp32] encoding
                // ModR/M: mod=00, reg=reg, rm=101 (RIP-relative)
                a.emit(modRM(modIndirect, reg.Code, 5))
                a.emitI32(m.Displ)
                return
        }

        if !m.HasIndex {
                // No index: [base + disp]
                rm := m.Base
                rmCode := rm.Code

                if isRSPorR12(rm) {
                        // RSP/R12 requires a SIB byte: [SIB + disp]
                        if !m.HasDispl {
                                a.emit(modRM(modIndirect, reg.Code, 4))
                                a.emit(sib(0, 4, 4)) // SIB: scale=1, index=RSP(none), base=RSP
                        } else if m.Displ >= -128 && m.Displ <= 127 {
                                a.emit(modRM(modDisp8, reg.Code, 4))
                                a.emit(sib(0, 4, 4))
                                a.emitI8(int8(m.Displ))
                        } else {
                                a.emit(modRM(modDisp32, reg.Code, 4))
                                a.emit(sib(0, 4, 4))
                                a.emitI32(m.Displ)
                        }
                } else if isRBPorR13(rm) {
                        // RBP/R13 with mod=00 means [disp32] (no base), so we must use disp8/32
                        if !m.HasDispl {
                                a.emit(modRM(modDisp8, reg.Code, rmCode))
                                a.emitI8(0)
                        } else if m.Displ >= -128 && m.Displ <= 127 {
                                a.emit(modRM(modDisp8, reg.Code, rmCode))
                                a.emitI8(int8(m.Displ))
                        } else {
                                a.emit(modRM(modDisp32, reg.Code, rmCode))
                                a.emitI32(m.Displ)
                        }
                } else {
                        // Normal base register
                        if !m.HasDispl {
                                a.emit(modRM(modIndirect, reg.Code, rmCode))
                        } else if m.Displ >= -128 && m.Displ <= 127 {
                                a.emit(modRM(modDisp8, reg.Code, rmCode))
                                a.emitI8(int8(m.Displ))
                        } else {
                                a.emit(modRM(modDisp32, reg.Code, rmCode))
                                a.emitI32(m.Displ)
                        }
                }
                return
        }

        // Has index: [base + index*scale + disp]
        base := m.Base
        idx := m.Index
        sc := scaleEncode(m.Scale)

        // mod depends on displacement
        var mod byte
        if !m.HasDispl {
                if isRBPorR13(base) {
                        mod = modDisp8
                        // disp8=0 must be emitted AFTER modRM+SIB (handled below)
                } else {
                        mod = modIndirect
                }
        } else if m.Displ >= -128 && m.Displ <= 127 {
                mod = modDisp8
        } else {
                mod = modDisp32
        }

        a.emit(modRM(mod, reg.Code, 4)) // rm=4 means SIB follows

        // SIB byte
        baseCode := base.Code
        indexCode := idx.Code
        a.emit(sib(sc, indexCode, baseCode))

        if mod == modDisp8 {
                a.emitI8(int8(m.Displ)) // 0 when HasDispl is false (RBP/R13 case)
        } else if mod == modDisp32 {
                a.emitI32(m.Displ)
        }
}

// emitModRMNoReg emits ModR/M for instructions that have no reg field (reg field is part of opcode).
func (a *Asm) emitModRMNoReg(opcExt byte, rm Reg) {
        a.emit(modRM(modReg, opcExt, rm.Code))
}

// emitModRMMemNoReg emits ModR/M + SIB for memory operand with no reg field.
func (a *Asm) emitModRMMemNoReg(opcExt byte, m MemRef) {
        // Reuse the mem encoding but with a fake register
        a.emitModRMMem(Reg{Code: opcExt, Class: RegGPR}, m)
}

// emitModRMMemNoRegREX emits ModR/M + SIB for memory operand with no reg field, with REX.
func (a *Asm) emitModRMMemNoRegREX(opcExt byte, m MemRef) {
        a.emitREXMemOnly(m)
        a.emitModRMMemNoReg(opcExt, m)
}

// ========== Immediate Encoding Helpers ==========

// fitsI8 returns true if the value fits in a signed 8-bit immediate.
func fitsI8(v int64) bool {
        return v >= -128 && v <= 127
}

// fitsI16 returns true if the value fits in a signed 16-bit immediate.
func fitsI16(v int64) bool {
        return v >= -32768 && v <= 32767
}

// fitsI32 returns true if the value fits in a signed 32-bit immediate.
func fitsI32(v int64) bool {
        return v >= math.MinInt32 && v <= math.MaxInt32
}

// fitsU8 returns true if the value fits in an unsigned 8-bit immediate.
func fitsU8(v int64) bool {
        return v >= 0 && v <= 255
}

// emitImm emits an immediate value of the specified size.
func (a *Asm) emitImm(size int, v int64) {
        switch size {
        case 1:
                a.emitI8(int8(v))
        case 2:
                a.emitI16(int16(v))
        case 4:
                a.emitI32(int32(v))
        case 8:
                a.emitU64(uint64(v))
        default:
                a.emitI32(int32(v))
        }
}

// emitImmOpt emits the smallest possible immediate encoding for the given value.
func (a *Asm) emitImmOpt(v int64) {
        if fitsI8(v) {
                a.emitI8(int8(v))
        } else if fitsI32(v) {
                a.emitI32(int32(v))
        } else {
                a.emitU64(uint64(v))
        }
}

// emitImm32Or8 emits a 32-bit or 8-bit immediate based on the operand hint.
func (a *Asm) emitImm32Or8(v int64, prefer8 bool) {
        if prefer8 && fitsI8(v) {
                a.emitI8(int8(v))
        } else {
                a.emitI32(int32(v))
        }
}

// ========== Size Prefixes ==========

// emitSizeOverride emits a 0x66 prefix for 16-bit operand size.
func (a *Asm) emitSizeOverride() {
        a.emit(0x66)
}

// emitAddrSizeOverride emits a 0x67 prefix for 32-bit addressing.
func (a *Asm) emitAddrSizeOverride() {
        a.emit(0x67)
}

// ========== MOV Instructions ==========

// MovRM64 emits MOV reg64, imm64.
// Encoding: REX.W + B8+rd + imm64
func (a *Asm) MovRM64(dst Reg, imm int64) {
        a.emitREX(true, false, false, dst.Extended)
        a.emit(0xB8 + dst.Code)
        a.emitImm(8, imm)
}

// MovRI32 emits MOV r/m32, imm32.
// Encoding: B8+rd + imm32
func (a *Asm) MovRI32(dst Reg, imm int32) {
        a.emit(0xB8 + dst.Code)
        a.emitI32(imm)
}

// MovRR emits MOV dst, src (same size determined by register).
// Uses opcodes 89 (r/m, r) and 88 (r/m8, r8): ModR/M reg=src, rm=dst.
func (a *Asm) MovRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x89)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x89)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x89)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x88)
                a.emitModRMReg(src, dst)
        }
}

// MovRM emits MOV r/m64, imm32 (sign-extended). Size is determined by dst register.
// 64-bit: REX.W C7 /0 + imm32
// 32-bit: C7 /0 + imm32
// 16-bit: 66 C7 /0 + imm16
// 8-bit: C6 /0 + imm8
func (a *Asm) MovRM(dst Reg, imm int64) {
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                a.emit(0xC7)
                a.emitModRMNoReg(0, dst)
                a.emitI32(int32(imm))
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xC7)
                a.emitModRMNoReg(0, dst)
                a.emitI32(int32(imm))
        case 16:
                a.emitSizeOverride()
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xC7)
                a.emitModRMNoReg(0, dst)
                a.emitI16(int16(imm))
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0xC6)
                a.emitModRMNoReg(0, dst)
                a.emitI8(int8(imm))
        }
}

// MovRMMem emits MOV [mem], reg (store).
// 64-bit: REX.W 89 /r with mem encoding
// 32-bit: 89 /r with mem encoding
func (a *Asm) MovRMMem(m MemRef, src Reg) {
        switch src.Size {
        case 64:
                a.emitREXMem(true, src, m)
                a.emit(0x89)
                a.emitModRMMem(src, m)
        case 32:
                if needsREXMem(src, m) {
                        a.emitREXMem(false, src, m)
                }
                a.emit(0x89)
                a.emitModRMMem(src, m)
        case 16:
                a.emitSizeOverride()
                if needsREXMem(src, m) {
                        a.emitREXMem(false, src, m)
                }
                a.emit(0x89)
                a.emitModRMMem(src, m)
        case 8:
                if needsREXMem(src, m) {
                        a.emitREXMem(false, src, m)
                }
                a.emit(0x88)
                a.emitModRMMem(src, m)
        }
}

// MovMemR emits MOV reg, [mem] (load).
// 64-bit: REX.W 8B /r with mem encoding
// 32-bit: 8B /r with mem encoding
func (a *Asm) MovMemR(dst Reg, m MemRef) {
        switch dst.Size {
        case 64:
                a.emitREXMem(true, dst, m)
                a.emit(0x8B)
                a.emitModRMMem(dst, m)
        case 32:
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                a.emit(0x8B)
                a.emitModRMMem(dst, m)
        case 16:
                a.emitSizeOverride()
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                a.emit(0x8B)
                a.emitModRMMem(dst, m)
        case 8:
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                a.emit(0x8A)
                a.emitModRMMem(dst, m)
        }
}

// MovMemImm emits MOV [mem], imm32.
// 64-bit: REX.W C7 /0 with mem + imm32
// 32-bit: C7 /0 with mem + imm32
func (a *Asm) MovMemImm(m MemRef, imm int32, size int) {
        if size == 64 {
                a.emitREXMemOnly(m)
                a.emit(0xC7)
                a.emitModRMMemNoReg(0, m)
                a.emitI32(imm)
        } else if size == 32 {
                if needsREXMemOnly(m) {
                        a.emitREXMemOnly(m)
                }
                a.emit(0xC7)
                a.emitModRMMemNoReg(0, m)
                a.emitI32(imm)
        } else if size == 16 {
                a.emitSizeOverride()
                if needsREXMemOnly(m) {
                        a.emitREXMemOnly(m)
                }
                a.emit(0xC7)
                a.emitModRMMemNoReg(0, m)
                a.emitI16(int16(imm))
        } else {
                if needsREXMemOnly(m) {
                        a.emitREXMemOnly(m)
                }
                a.emit(0xC6)
                a.emitModRMMemNoReg(0, m)
                a.emitI8(int8(imm))
        }
}

// MovZX8R emits MOVZX r32, r/m8 (zero-extend byte to 32-bit).
// Encoding: 0F B6 /r
func (a *Asm) MovZX8R(dst Reg, src Reg) {
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0xB6)
        a.emitModRMReg(dst, src)
}

// MovZX8Mem emits MOVZX r32, r/m8 from memory.
func (a *Asm) MovZX8Mem(dst Reg, m MemRef) {
        a.emitREXMem(false, dst, m)
        a.emit(0x0F)
        a.emit(0xB6)
        a.emitModRMMem(dst, m)
}

// MovZX16R emits MOVZX r32, r/m16 (zero-extend word to 32-bit).
// Encoding: 0F B7 /r
func (a *Asm) MovZX16R(dst Reg, src Reg) {
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0xB7)
        a.emitModRMReg(dst, src)
}

// MovZX16Mem emits MOVZX r32, r/m16 (zero-extend word from memory to 32-bit).
// Encoding: 0F B7 /r
func (a *Asm) MovZX16Mem(dst Reg, m MemRef) {
        a.emitREXMem(false, dst, m)
        a.emit(0x0F)
        a.emit(0xB7)
        a.emitModRMMem(dst, m)
}

// MovSXR emits MOVSX r64, r/m32 (sign-extend 32-bit to 64-bit).
// Encoding: REX.W 63 /r
func (a *Asm) MovSXR(dst, src Reg) {
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x63)
        a.emitModRMReg(dst, src)
}

// MovSXMov8_64 emits MOVZX r64, r/m8 (zero-extend byte to 64-bit).
// Encoding: REX.W 0F B6 /r
func (a *Asm) MovZX8_64(dst Reg, src Reg) {
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0xB6)
        a.emitModRMReg(dst, src)
}

// MovSX8_64 emits MOVSX r64, r/m8 (sign-extend byte to 64-bit).
// Encoding: REX.W 0F BE /r
func (a *Asm) MovSX8_64(dst, src Reg) {
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0xBE)
        a.emitModRMReg(dst, src)
}

// MovSX16_64 emits MOVSX r64, r/m16 (sign-extend word to 64-bit).
// Encoding: REX.W 0F BF /r
func (a *Asm) MovSX16_64(dst, src Reg) {
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0xBF)
        a.emitModRMReg(dst, src)
}

// MovSX32_64 emits MOVSXD r64, r/m32 (sign-extend dword to qword).
// Encoding: REX.W 63 /r
func (a *Asm) MovSX32_64(dst, src Reg) {
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x63)
        a.emitModRMReg(dst, src)
}

// MovSX8_32 emits MOVSX r32, r/m8.
// Encoding: 0F BE /r
func (a *Asm) MovSX8_32(dst, src Reg) {
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0xBE)
        a.emitModRMReg(dst, src)
}

// MovSX16_32 emits MOVSX r32, r/m16.
// Encoding: 0F BF /r
func (a *Asm) MovSX16_32(dst, src Reg) {
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0xBF)
        a.emitModRMReg(dst, src)
}

// MovZeroAX32 emits XOR EAX, EAX (idiomatic zero).
func (a *Asm) MovZeroAX32() {
        a.emit(0x31)
        a.emit(0xC0)
}

// MovZeroR64 emits XOR reg, reg to zero a 64-bit register (no REX.W needed to clear upper bits).
func (a *Asm) MovZeroR64(r Reg) {
        a.XorRR(r, r)
}

// ========== XMM MOV Instructions ==========

// MovSS_RM emits MOVSS xmm, r/m32.
// Encoding: F3 0F 10 /r
func (a *Asm) MovSS_RM(dst Reg, src Operand) {
        a.emit(0xF3)
        if needsREX(dst, Reg{}) || (src.Kind == OpReg && src.Reg.Extended) {
                a.emitREX(false, dst.Extended, false, src.Kind == OpReg && src.Reg.Extended)
        }
        a.emit(0x0F)
        a.emit(0x10)
        if src.Kind == OpReg {
                a.emitModRMReg(dst, src.Reg)
        } else {
                a.emitModRMMem(dst, src.Mem)
        }
}

// MovSS_MR emits MOVSS r/m32, xmm.
// Encoding: F3 0F 11 /r
func (a *Asm) MovSS_MR(dst Operand, src Reg) {
        a.emit(0xF3)
        if src.Extended || (dst.Kind == OpReg && dst.Reg.Extended) {
                a.emitREX(false, src.Extended, false, dst.Kind == OpReg && dst.Reg.Extended)
        }
        a.emit(0x0F)
        a.emit(0x11)
        if dst.Kind == OpReg {
                a.emitModRMReg(src, dst.Reg)
        } else {
                a.emitModRMMem(src, dst.Mem)
        }
}

// MovSD_RM emits MOVSD xmm, r/m64.
// Encoding: F2 0F 10 /r
func (a *Asm) MovSD_RM(dst Reg, src Operand) {
        a.emit(0xF2)
        if dst.Extended || (src.Kind == OpReg && src.Reg.Extended) {
                a.emitREX(false, dst.Extended, false, src.Kind == OpReg && src.Reg.Extended)
        }
        a.emit(0x0F)
        a.emit(0x10)
        if src.Kind == OpReg {
                a.emitModRMReg(dst, src.Reg)
        } else {
                a.emitModRMMem(dst, src.Mem)
        }
}

// MovSD_MR emits MOVSD r/m64, xmm.
// Encoding: F2 0F 11 /r
func (a *Asm) MovSD_MR(dst Operand, src Reg) {
        a.emit(0xF2)
        if src.Extended || (dst.Kind == OpReg && dst.Reg.Extended) {
                a.emitREX(false, src.Extended, false, dst.Kind == OpReg && dst.Reg.Extended)
        }
        a.emit(0x0F)
        a.emit(0x11)
        if dst.Kind == OpReg {
                a.emitModRMReg(src, dst.Reg)
        } else {
                a.emitModRMMem(src, dst.Mem)
        }
}

// XorPS emits XORPS xmm, xmm/m128. Used to zero XMM registers.
// Encoding: 0F 57 /r
func (a *Asm) XorPS(dst, src Reg) {
        if dst.Extended || src.Extended {
                a.emitREX(false, dst.Extended, false, src.Extended)
        }
        a.emit(0x0F)
        a.emit(0x57)
        a.emitModRMReg(dst, src)
}

// ========== Arithmetic Instructions ==========

// AddRR emits ADD dst, src (both registers).
// 64-bit: REX.W 01 /r
// 32-bit: 01 /r
// 8-bit: 00 /r
func (a *Asm) AddRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x01)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x01)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x01)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x00)
                a.emitModRMReg(src, dst)
        }
}

// AddRI emits ADD reg, imm.
// 64-bit: REX.W 81 /0 + imm32, or REX.W 83 /0 + imm8
// 32-bit: 81 /0 + imm32, or 83 /0 + imm8
// 8-bit: 80 /0 + imm8
func (a *Asm) AddRI(dst Reg, imm int64) {
        useImm8 := fitsI8(imm)
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(0, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(0, dst)
                        a.emitI32(int32(imm))
                }
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(0, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(0, dst)
                        a.emitI32(int32(imm))
                }
        case 16:
                a.emitSizeOverride()
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(0, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(0, dst)
                        a.emitI16(int16(imm))
                }
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0x80)
                a.emitModRMNoReg(0, dst)
                a.emitI8(int8(imm))
        }
}

// AddRMMem emits ADD [mem], reg.
func (a *Asm) AddRMMem(m MemRef, src Reg) {
        switch src.Size {
        case 64:
                a.emitREXMem(true, src, m)
                a.emit(0x01)
                a.emitModRMMem(src, m)
        default:
                if needsREXMem(src, m) {
                        a.emitREXMem(false, src, m)
                }
                a.emit(0x01)
                a.emitModRMMem(src, m)
        }
}

// AddMemR emits ADD reg, [mem].
func (a *Asm) AddMemR(dst Reg, m MemRef) {
        switch dst.Size {
        case 64:
                a.emitREXMem(true, dst, m)
                a.emit(0x03)
                a.emitModRMMem(dst, m)
        default:
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                a.emit(0x03)
                a.emitModRMMem(dst, m)
        }
}

// SubRR emits SUB dst, src.
// 64-bit: REX.W 29 /r
// 32-bit: 29 /r
func (a *Asm) SubRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x29)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x29)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x29)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x28)
                a.emitModRMReg(src, dst)
        }
}

// SubRI emits SUB reg, imm.
func (a *Asm) SubRI(dst Reg, imm int64) {
        useImm8 := fitsI8(imm)
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(5, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(5, dst)
                        a.emitI32(int32(imm))
                }
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(5, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(5, dst)
                        a.emitI32(int32(imm))
                }
        case 16:
                a.emitSizeOverride()
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(5, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(5, dst)
                        a.emitI16(int16(imm))
                }
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0x80)
                a.emitModRMNoReg(5, dst)
                a.emitI8(int8(imm))
        }
}

// SubRMMem emits SUB [mem], reg.
func (a *Asm) SubRMMem(m MemRef, src Reg) {
        switch src.Size {
        case 64:
                a.emitREXMem(true, src, m)
                a.emit(0x29)
                a.emitModRMMem(src, m)
        default:
                if needsREXMem(src, m) {
                        a.emitREXMem(false, src, m)
                }
                a.emit(0x29)
                a.emitModRMMem(src, m)
        }
}

// SubMemR emits SUB reg, [mem].
func (a *Asm) SubMemR(dst Reg, m MemRef) {
        switch dst.Size {
        case 64:
                a.emitREXMem(true, dst, m)
                a.emit(0x2B)
                a.emitModRMMem(dst, m)
        default:
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                a.emit(0x2B)
                a.emitModRMMem(dst, m)
        }
}

// IMul2RR emits IMUL r64, r/m64 (two-operand form, result in dst).
// Encoding: REX.W 0F AF /r
func (a *Asm) IMul2RR(dst, src Reg) {
        switch dst.Size {
        case 64:
                a.emitREX(true, dst.Extended, false, src.Extended)
                a.emit(0x0F)
                a.emit(0xAF)
                a.emitModRMReg(dst, src)
        case 32:
                if needsREX(dst, src) {
                        a.emitREX(false, dst.Extended, false, src.Extended)
                }
                a.emit(0x0F)
                a.emit(0xAF)
                a.emitModRMReg(dst, src)
        case 16:
                a.emitSizeOverride()
                if needsREX(dst, src) {
                        a.emitREX(false, dst.Extended, false, src.Extended)
                }
                a.emit(0x0F)
                a.emit(0xAF)
                a.emitModRMReg(dst, src)
        }
}

// IMul2RMem emits IMUL r64, [mem].
func (a *Asm) IMul2RMem(dst Reg, m MemRef) {
        switch dst.Size {
        case 64:
                a.emitREXMem(true, dst, m)
                a.emit(0x0F)
                a.emit(0xAF)
                a.emitModRMMem(dst, m)
        default:
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                a.emit(0x0F)
                a.emit(0xAF)
                a.emitModRMMem(dst, m)
        }
}

// IMul3 emits IMUL dst, src, imm (three-operand form).
// Encoding: REX.W 69 /r + imm32
func (a *Asm) IMul3(dst, src Reg, imm int32) {
        switch dst.Size {
        case 64:
                a.emitREX(true, dst.Extended, false, src.Extended)
                if fitsI8(int64(imm)) {
                        a.emit(0x6B)
                        a.emitModRMReg(dst, src)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x69)
                        a.emitModRMReg(dst, src)
                        a.emitI32(imm)
                }
        case 32:
                if needsREX(dst, src) {
                        a.emitREX(false, dst.Extended, false, src.Extended)
                }
                if fitsI8(int64(imm)) {
                        a.emit(0x6B)
                        a.emitModRMReg(dst, src)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x69)
                        a.emitModRMReg(dst, src)
                        a.emitI32(imm)
                }
        }
}

// IMul3Mem emits IMUL dst, [mem], imm.
func (a *Asm) IMul3Mem(dst Reg, m MemRef, imm int32) {
        switch dst.Size {
        case 64:
                a.emitREXMem(true, dst, m)
                if fitsI8(int64(imm)) {
                        a.emit(0x6B)
                        a.emitModRMMem(dst, m)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x69)
                        a.emitModRMMem(dst, m)
                        a.emitI32(imm)
                }
        default:
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                if fitsI8(int64(imm)) {
                        a.emit(0x6B)
                        a.emitModRMMem(dst, m)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x69)
                        a.emitModRMMem(dst, m)
                        a.emitI32(imm)
                }
        }
}

// MulRR emits MUL r/m64 (unsigned multiply, RDX:RAX = RAX * r/m).
// Encoding: REX.W F7 /4
func (a *Asm) MulRR(src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, false, false, src.Extended)
                a.emit(0xF7)
                a.emitModRMNoReg(4, src)
        case 32:
                if src.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xF7)
                a.emitModRMNoReg(4, src)
        }
}

// IMul1RR emits IMUL r/m64 (signed multiply, RDX:RAX = RAX * r/m).
// Encoding: REX.W F7 /5
func (a *Asm) IMul1RR(src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, false, false, src.Extended)
                a.emit(0xF7)
                a.emitModRMNoReg(5, src)
        case 32:
                if src.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xF7)
                a.emitModRMNoReg(5, src)
        }
}

// DivRR emits DIV r/m64 (unsigned divide, RAX = RDX:RAX / r/m, RDX = remainder).
// Encoding: REX.W F7 /6
func (a *Asm) DivRR(src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, false, false, src.Extended)
                a.emit(0xF7)
                a.emitModRMNoReg(6, src)
        case 32:
                if src.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xF7)
                a.emitModRMNoReg(6, src)
        }
}

// IDivRR emits IDIV r/m64 (signed divide).
// Encoding: REX.W F7 /7
func (a *Asm) IDivRR(src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, false, false, src.Extended)
                a.emit(0xF7)
                a.emitModRMNoReg(7, src)
        case 32:
                if src.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xF7)
                a.emitModRMNoReg(7, src)
        }
}

// ========== Bitwise Instructions ==========

// AndRR emits AND dst, src.
func (a *Asm) AndRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x21)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x21)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x21)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x20)
                a.emitModRMReg(src, dst)
        }
}

// AndRI emits AND reg, imm.
func (a *Asm) AndRI(dst Reg, imm int64) {
        useImm8 := fitsI8(imm)
        switch dst.Size {
        case 64:
                if useImm8 {
                        a.emitREX(true, false, false, dst.Extended)
                        a.emit(0x83)
                        a.emitModRMNoReg(4, dst)
                        a.emitI8(int8(imm))
                } else if fitsI32(imm) {
                        a.emitREX(true, false, false, dst.Extended)
                        a.emit(0x81)
                        a.emitModRMNoReg(4, dst)
                        a.emitI32(int32(imm))
                } else {
                        // 64-bit immediate: MOV R11, imm64; AND dst, R11
                        a.MovRM64(R11, imm)
                        a.AndRR(dst, R11)
                }
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(4, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(4, dst)
                        a.emitI32(int32(imm))
                }
        case 16:
                a.emitSizeOverride()
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(4, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(4, dst)
                        a.emitI16(int16(imm))
                }
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0x80)
                a.emitModRMNoReg(4, dst)
                a.emitI8(int8(imm))
        }
}

// OrRR emits OR dst, src.
func (a *Asm) OrRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x09)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x09)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x09)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x08)
                a.emitModRMReg(src, dst)
        }
}

// OrRI emits OR reg, imm.
func (a *Asm) OrRI(dst Reg, imm int64) {
        useImm8 := fitsI8(imm)
        switch dst.Size {
        case 64:
                if useImm8 {
                        a.emitREX(true, false, false, dst.Extended)
                        a.emit(0x83)
                        a.emitModRMNoReg(1, dst)
                        a.emitI8(int8(imm))
                } else if fitsI32(imm) {
                        a.emitREX(true, false, false, dst.Extended)
                        a.emit(0x81)
                        a.emitModRMNoReg(1, dst)
                        a.emitI32(int32(imm))
                } else {
                        // 64-bit immediate: MOV R11, imm64; OR dst, R11
                        a.MovRM64(R11, imm)
                        a.OrRR(dst, R11)
                }
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(1, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(1, dst)
                        a.emitI32(int32(imm))
                }
        case 16:
                a.emitSizeOverride()
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(1, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(1, dst)
                        a.emitI16(int16(imm))
                }
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0x80)
                a.emitModRMNoReg(1, dst)
                a.emitI8(int8(imm))
        }
}

// XorRR emits XOR dst, src.
func (a *Asm) XorRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x31)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x31)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x31)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x30)
                a.emitModRMReg(src, dst)
        }
}

// XorRI emits XOR reg, imm.
func (a *Asm) XorRI(dst Reg, imm int64) {
        useImm8 := fitsI8(imm)
        switch dst.Size {
        case 64:
                if useImm8 {
                        a.emitREX(true, false, false, dst.Extended)
                        a.emit(0x83)
                        a.emitModRMNoReg(6, dst)
                        a.emitI8(int8(imm))
                } else if fitsI32(imm) {
                        a.emitREX(true, false, false, dst.Extended)
                        a.emit(0x81)
                        a.emitModRMNoReg(6, dst)
                        a.emitI32(int32(imm))
                } else {
                        // 64-bit immediate: MOV R11, imm64; XOR dst, R11
                        a.MovRM64(R11, imm)
                        a.XorRR(dst, R11)
                }
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(6, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(6, dst)
                        a.emitI32(int32(imm))
                }
        case 16:
                a.emitSizeOverride()
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(6, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(6, dst)
                        a.emitI16(int16(imm))
                }
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0x80)
                a.emitModRMNoReg(6, dst)
                a.emitI8(int8(imm))
        }
}

// NotR emits NOT r/m64.
// Encoding: REX.W F7 /2
func (a *Asm) NotR(src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, false, false, src.Extended)
                a.emit(0xF7)
                a.emitModRMNoReg(2, src)
        case 32:
                if src.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xF7)
                a.emitModRMNoReg(2, src)
        case 16:
                a.emitSizeOverride()
                a.emit(0xF7)
                a.emitModRMNoReg(2, src)
        case 8:
                if needsREX(Reg{}, src) {
                        a.emitREX(false, false, false, src.Extended)
                }
                a.emit(0xF6)
                a.emitModRMNoReg(2, src)
        }
}

// NegR emits NEG r/m64.
// Encoding: REX.W F7 /3
func (a *Asm) NegR(src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, false, false, src.Extended)
                a.emit(0xF7)
                a.emitModRMNoReg(3, src)
        case 32:
                if src.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xF7)
                a.emitModRMNoReg(3, src)
        case 16:
                a.emitSizeOverride()
                a.emit(0xF7)
                a.emitModRMNoReg(3, src)
        case 8:
                if needsREX(Reg{}, src) {
                        a.emitREX(false, false, false, src.Extended)
                }
                a.emit(0xF6)
                a.emitModRMNoReg(3, src)
        }
}

// ========== Shift Instructions ==========

// ShlRI emits SHL reg, imm8.
// Encoding: REX.W C1 /4 ib
func (a *Asm) ShlRI(dst Reg, count uint8) {
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                a.emit(0xC1)
                a.emitModRMNoReg(4, dst)
                a.emit(count)
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xC1)
                a.emitModRMNoReg(4, dst)
                a.emit(count)
        case 16:
                a.emitSizeOverride()
                a.emit(0xC1)
                a.emitModRMNoReg(4, dst)
                a.emit(count)
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0xC0)
                a.emitModRMNoReg(4, dst)
                a.emit(count)
        }
}

// ShrRI emits SHR reg, imm8 (logical shift right).
func (a *Asm) ShrRI(dst Reg, count uint8) {
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                a.emit(0xC1)
                a.emitModRMNoReg(5, dst)
                a.emit(count)
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xC1)
                a.emitModRMNoReg(5, dst)
                a.emit(count)
        case 16:
                a.emitSizeOverride()
                a.emit(0xC1)
                a.emitModRMNoReg(5, dst)
                a.emit(count)
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0xC0)
                a.emitModRMNoReg(5, dst)
                a.emit(count)
        }
}

// SarRI emits SAR reg, imm8 (arithmetic shift right).
func (a *Asm) SarRI(dst Reg, count uint8) {
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                a.emit(0xC1)
                a.emitModRMNoReg(7, dst)
                a.emit(count)
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xC1)
                a.emitModRMNoReg(7, dst)
                a.emit(count)
        case 16:
                a.emitSizeOverride()
                a.emit(0xC1)
                a.emitModRMNoReg(7, dst)
                a.emit(count)
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0xC0)
                a.emitModRMNoReg(7, dst)
                a.emit(count)
        }
}

// ShlRCL emits SHL reg, CL.
// Encoding: REX.W D3 /4
func (a *Asm) ShlRCL(dst Reg) {
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                a.emit(0xD3)
                a.emitModRMNoReg(4, dst)
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xD3)
                a.emitModRMNoReg(4, dst)
        default:
                a.emitREX(true, false, false, dst.Extended)
                a.emit(0xD3)
                a.emitModRMNoReg(4, dst)
        }
}

// ShrRCL emits SHR reg, CL.
func (a *Asm) ShrRCL(dst Reg) {
        a.emitREX(true, false, false, dst.Extended)
        a.emit(0xD3)
        a.emitModRMNoReg(5, dst)
}

// SarRCL emits SAR reg, CL.
func (a *Asm) SarRCL(dst Reg) {
        a.emitREX(true, false, false, dst.Extended)
        a.emit(0xD3)
        a.emitModRMNoReg(7, dst)
}

// ========== Comparison Instructions ==========

// CmpRR emits CMP dst, src.
// 64-bit: REX.W 39 /r
// 32-bit: 39 /r
func (a *Asm) CmpRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x39)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x39)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x39)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x38)
                a.emitModRMReg(src, dst)
        }
}

// CmpRI emits CMP reg, imm.
func (a *Asm) CmpRI(dst Reg, imm int64) {
        useImm8 := fitsI8(imm)
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(7, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(7, dst)
                        a.emitI32(int32(imm))
                }
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(7, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(7, dst)
                        a.emitI32(int32(imm))
                }
        case 16:
                a.emitSizeOverride()
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                if useImm8 {
                        a.emit(0x83)
                        a.emitModRMNoReg(7, dst)
                        a.emitI8(int8(imm))
                } else {
                        a.emit(0x81)
                        a.emitModRMNoReg(7, dst)
                        a.emitI16(int16(imm))
                }
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0x80)
                a.emitModRMNoReg(7, dst)
                a.emitI8(int8(imm))
        }
}

// CmpMem emits CMP [mem], imm.
func (a *Asm) CmpMem(m MemRef, imm int32, size int) {
        if size == 64 {
                a.emitREXMemOnly(m)
                a.emit(0x81)
                a.emitModRMMemNoReg(7, m)
                a.emitI32(imm)
        } else {
                if needsREXMemOnly(m) {
                        a.emitREXMemOnly(m)
                }
                a.emit(0x81)
                a.emitModRMMemNoReg(7, m)
                a.emitI32(imm)
        }
}

// CmpRMem emits CMP reg, [mem].
func (a *Asm) CmpRMem(dst Reg, m MemRef) {
        switch dst.Size {
        case 64:
                a.emitREXMem(true, dst, m)
                a.emit(0x3B)
                a.emitModRMMem(dst, m)
        default:
                if needsREXMem(dst, m) {
                        a.emitREXMem(false, dst, m)
                }
                a.emit(0x3B)
                a.emitModRMMem(dst, m)
        }
}

// TestRR emits TEST dst, src (bitwise AND, sets flags, discards result).
// 64-bit: REX.W 85 /r
// 32-bit: 85 /r
func (a *Asm) TestRR(dst, src Reg) {
        switch src.Size {
        case 64:
                a.emitREX(true, src.Extended, false, dst.Extended)
                a.emit(0x85)
                a.emitModRMReg(src, dst)
        case 32:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x85)
                a.emitModRMReg(src, dst)
        case 16:
                a.emitSizeOverride()
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x85)
                a.emitModRMReg(src, dst)
        case 8:
                if needsREX(src, dst) {
                        a.emitREX(false, src.Extended, false, dst.Extended)
                }
                a.emit(0x84)
                a.emitModRMReg(src, dst)
        }
}

// TestRI emits TEST reg, imm.
func (a *Asm) TestRI(dst Reg, imm int64) {
        switch dst.Size {
        case 64:
                a.emitREX(true, false, false, dst.Extended)
                a.emit(0xF7)
                a.emitModRMNoReg(0, dst)
                a.emitI32(int32(imm))
        case 32:
                if dst.Extended {
                        a.emitREX(false, false, false, true)
                }
                a.emit(0xF7)
                a.emitModRMNoReg(0, dst)
                a.emitI32(int32(imm))
        case 16:
                a.emitSizeOverride()
                a.emit(0xF7)
                a.emitModRMNoReg(0, dst)
                a.emitI16(int16(imm))
        case 8:
                if needsREX(Reg{}, dst) {
                        a.emitREX(false, false, false, dst.Extended)
                }
                a.emit(0xF6)
                a.emitModRMNoReg(0, dst)
                a.emitI8(int8(imm))
        }
}

// ========== Conditional Jumps ==========

// Each conditional jump is a short/near relative jump.
// Short form: 7x + rel8 (2 bytes)
// Near form: 0F 8x + rel32 (6 bytes)

// condJumpShort returns the opcode for a short conditional jump.
func condJumpShort(cc CondCode) byte {
        switch cc {
        case CondO:
                return 0x70
        case CondNO:
                return 0x71
        case CondB:
                return 0x72
        case CondNB:
                return 0x73
        case CondE:
                return 0x74
        case CondNE:
                return 0x75
        case CondBE:
                return 0x76
        case CondNBE:
                return 0x77
        case CondS:
                return 0x78
        case CondNS:
                return 0x79
        case CondP:
                return 0x7A
        case CondNP:
                return 0x7B
        case CondL:
                return 0x7C
        case CondNL:
                return 0x7D
        case CondLE:
                return 0x7E
        case CondNLE:
                return 0x7F
        default:
                return 0x74 // default to JE
        }
}

// condJumpNear returns the second byte of the near conditional jump (0F xx).
func condJumpNear(cc CondCode) byte {
        switch cc {
        case CondO:
                return 0x80
        case CondNO:
                return 0x81
        case CondB:
                return 0x82
        case CondNB:
                return 0x83
        case CondE:
                return 0x84
        case CondNE:
                return 0x85
        case CondBE:
                return 0x86
        case CondNBE:
                return 0x87
        case CondS:
                return 0x88
        case CondNS:
                return 0x89
        case CondP:
                return 0x8A
        case CondNP:
                return 0x8B
        case CondL:
                return 0x8C
        case CondNL:
                return 0x8D
        case CondLE:
                return 0x8E
        case CondNLE:
                return 0x8F
        default:
                return 0x84
        }
}

// CondCode represents an x86 condition code.
type CondCode int

const (
        CondO   CondCode = iota // overflow
        CondNO                  // no overflow
        CondB                   // below (unsigned <)
        CondNB                  // not below (unsigned >=)
        CondE                   // equal
        CondNE                  // not equal
        CondBE                  // below or equal (unsigned <=)
        CondNBE                 // not below or equal (unsigned >)
        CondS                   // sign
        CondNS                  // not sign
        CondP                   // parity even
        CondNP                  // not parity
        CondL                   // less (signed <)
        CondNL                  // not less (signed >=)
        CondLE                  // less or equal (signed <=)
        CondNLE                 // not less or equal (signed >)
        // Aliases
        CondC  = CondB
        CondNC = CondNB
        CondAE = CondNB
        CondZ  = CondE
        CondNZ = CondNE
        CondA  = CondNBE
        CondNA = CondBE
        CondPE = CondP
        CondPO = CondNP
        CondGE = CondNL
        CondNGE = CondL
        CondG  = CondNLE
        CondNG = CondLE
)

// Jcc emits a near conditional jump (0F 8x + rel32) to a label.
// We always use the near (32-bit displacement) form for simplicity.
func (a *Asm) Jcc(cc CondCode, label string) {
        a.emit(0x0F)
        a.emit(condJumpNear(cc))
        a.addFixup(label, 4, RelAbs32)
        a.emitI32(0) // placeholder, patched by ResolveFixups
}

// JccShort emits a short conditional jump (7x + rel8) to a label.
// The displacement must fit in [-128, 127].
func (a *Asm) JccShort(cc CondCode, label string) {
        a.emit(condJumpShort(cc))
        a.addFixup(label, 1, RelAbs32)
        a.emitI8(0) // placeholder
}

// JE emits JE/JZ (jump if equal).
func (a *Asm) JE(label string)  { a.Jcc(CondE, label) }

// JNE emits JNE/JNZ (jump if not equal).
func (a *Asm) JNE(label string) { a.Jcc(CondNE, label) }

// JL emits JL (jump if less, signed).
func (a *Asm) JL(label string)  { a.Jcc(CondL, label) }

// JG emits JG (jump if greater, signed).
func (a *Asm) JG(label string)  { a.Jcc(CondG, label) }

// JLE emits JLE (jump if less or equal, signed).
func (a *Asm) JLE(label string) { a.Jcc(CondLE, label) }

// JGE emits JGE (jump if greater or equal, signed).
func (a *Asm) JGE(label string) { a.Jcc(CondGE, label) }

// JA emits JA (jump if above, unsigned).
func (a *Asm) JA(label string)  { a.Jcc(CondA, label) }

// JB emits JB (jump if below, unsigned).
func (a *Asm) JB(label string)  { a.Jcc(CondB, label) }

// JAE emits JAE (jump if above or equal, unsigned).
func (a *Asm) JAE(label string) { a.Jcc(CondAE, label) }

// JBE emits JBE (jump if below or equal, unsigned).
func (a *Asm) JBE(label string) { a.Jcc(CondBE, label) }

// JZ is an alias for JE.
func (a *Asm) JZ(label string)  { a.Jcc(CondZ, label) }

// JNZ is an alias for JNE.
func (a *Asm) JNZ(label string) { a.Jcc(CondNZ, label) }

// ========== Unconditional Jumps ==========

// JMP emits a near unconditional jump (E9 + rel32) to a label.
func (a *Asm) JMP(label string) {
        a.emit(0xE9)
        a.addFixup(label, 4, RelRip32)
        a.emitI32(0) // placeholder
}

// JMPShort emits a short unconditional jump (EB + rel8).
func (a *Asm) JMPShort(label string) {
        a.emit(0xEB)
        a.addFixup(label, 1, RelAbs32)
        a.emitI8(0)
}

// JMPReg emits JMP reg (indirect jump through register).
// 64-bit: REX.W FF /4
func (a *Asm) JMPReg(r Reg) {
        a.emitREX(true, false, false, r.Extended)
        a.emit(0xFF)
        a.emitModRMNoReg(4, r)
}

// JMPSIB emits JMP [base + index*scale + disp].
func (a *Asm) JMPSIB(m MemRef) {
        a.emitREXMemOnly(m)
        a.emit(0xFF)
        a.emitModRMMemNoReg(4, m)
}

// ========== Function Call and Return ==========

// CALL emits CALL rel32 (near call to relative address).
func (a *Asm) CALL(label string) {
        a.emit(0xE8)
        a.addFixup(label, 4, RelRip32)
        a.emitI32(0)
}

// CALLReg emits CALL reg (indirect call through register).
// 64-bit: REX.W FF /2
func (a *Asm) CALLReg(r Reg) {
        a.emitREX(true, false, false, r.Extended)
        a.emit(0xFF)
        a.emitModRMNoReg(2, r)
}

// CALLMem emits CALL [mem] (indirect call through memory).
func (a *Asm) CALLMem(m MemRef) {
        a.emitREXMemOnly(m)
        a.emit(0xFF)
        a.emitModRMMemNoReg(2, m)
}

// RET emits RET (near return).
func (a *Asm) RET() {
        a.emit(0xC3)
}

// RETImm16 emits RET imm16 (return and pop imm16 bytes from stack).
func (a *Asm) RETImm16(bytes uint16) {
        a.emit(0xC2)
        a.emitU16(bytes)
}

// ========== Stack Instructions ==========

// PUSH emits PUSH reg.
// 64-bit: 50+rd (no REX needed for 64-bit push)
// For R8-R15: REX.B 50+rd
func (a *Asm) PUSH(r Reg) {
        if r.Extended {
                a.emitREX(false, false, false, true)
        }
        a.emit(0x50 + r.Code)
}

// PUSHImm8 emits PUSH imm8 (sign-extended to 64 bits).
func (a *Asm) PUSHImm8(imm int8) {
        a.emit(0x6A)
        a.emitI8(imm)
}

// PUSHImm32 emits PUSH imm32 (sign-extended to 64 bits).
func (a *Asm) PUSHImm32(imm int32) {
        a.emit(0x68)
        a.emitI32(imm)
}

// PUSHMem emits PUSH [mem64].
func (a *Asm) PUSHMem(m MemRef) {
        a.emitREXMemOnly(m)
        a.emit(0xFF)
        a.emitModRMMemNoReg(6, m)
}

// POP emits POP reg.
func (a *Asm) POP(r Reg) {
        if r.Extended {
                a.emitREX(false, false, false, true)
        }
        a.emit(0x58 + r.Code)
}

// POPMem emits POP [mem64].
func (a *Asm) POPMem(m MemRef) {
        a.emitREXMemOnly(m)
        a.emit(0x8F)
        a.emitModRMMemNoReg(0, m)
}

// ========== LEA ==========

// LEA emits LEA reg, [mem].
// 64-bit: REX.W 8D /r
func (a *Asm) LEA(dst Reg, m MemRef) {
        switch dst.Size {
        case 64:
                a.emitREXMem(true, dst, m)
                a.emit(0x8D)
                a.emitModRMMem(dst, m)
        case 32:
                if needsREXMem(dst, m) {
                        a.emitREX(false, dst.Extended, false, false)
                }
                a.emit(0x8D)
                a.emitModRMMem(dst, m)
        }
}

// ========== SetCC (Set byte on condition) ==========

// SetCC emits SETcc r/m8 (sets byte to 1 if condition is true, 0 otherwise).
func (a *Asm) SetCC(cc CondCode, dst Reg) {
        if needsREX(Reg{}, dst) {
                a.emitREX(false, false, false, dst.Extended)
        }
        a.emit(0x0F)
        a.emit(condJumpNear(cc) - 0x80 + 0x90) // 0F 9x for SETcc
        // The SETcc opcode is 0F 90+cc
        // Since condJumpNear returns 80+cc, we subtract 80 and add 90
        a.emitModRMNoReg(0, dst)
}

// SetE emits SETE/SETZ.
func (a *Asm) SetE(dst Reg) { a.SetCC(CondE, dst) }

// SetNE emits SETNE/SETNZ.
func (a *Asm) SetNE(dst Reg) { a.SetCC(CondNE, dst) }

// SetL emits SETL.
func (a *Asm) SetL(dst Reg) { a.SetCC(CondL, dst) }

// SetG emits SETG.
func (a *Asm) SetG(dst Reg) { a.SetCC(CondG, dst) }

// SetGE emits SETGE.
func (a *Asm) SetGE(dst Reg) { a.SetCC(CondGE, dst) }

// SetLE emits SETLE.
func (a *Asm) SetLE(dst Reg) { a.SetCC(CondLE, dst) }

// SetB emits SETB.
func (a *Asm) SetB(dst Reg) { a.SetCC(CondB, dst) }

// SetA emits SETA.
func (a *Asm) SetA(dst Reg) { a.SetCC(CondA, dst) }

// SetBE emits SETBE.
func (a *Asm) SetBE(dst Reg) { a.SetCC(CondBE, dst) }

// SetAE emits SETAE.
func (a *Asm) SetAE(dst Reg) { a.SetCC(CondAE, dst) }

// ========== CMOV (Conditional Move) ==========

// CMOVcc emits CMOVcc dst, src (conditional move).
// 64-bit: REX.W 0F 4x /r
func (a *Asm) CMOVcc(cc CondCode, dst, src Reg) {
        switch dst.Size {
        case 64:
                a.emitREX(true, dst.Extended, false, src.Extended)
                a.emit(0x0F)
                a.emit(condJumpNear(cc) - 0x80 + 0x40) // CMOVcc opcode: 0F 40+cc
                a.emitModRMReg(dst, src)
        case 32:
                if needsREX(dst, src) {
                        a.emitREX(false, dst.Extended, false, src.Extended)
                }
                a.emit(0x0F)
                a.emit(condJumpNear(cc) - 0x80 + 0x40)
                a.emitModRMReg(dst, src)
        }
}

// CMOVE emits CMOVE/CMOVZ.
func (a *Asm) CMOVE(dst, src Reg) { a.CMOVcc(CondE, dst, src) }

// CMOVNE emits CMOVNE/CMOVNZ.
func (a *Asm) CMOVNE(dst, src Reg) { a.CMOVcc(CondNE, dst, src) }

// CMOVL emits CMOVL.
func (a *Asm) CMOVL(dst, src Reg) { a.CMOVcc(CondL, dst, src) }

// CMOVG emits CMOVG.
func (a *Asm) CMOVG(dst, src Reg) { a.CMOVcc(CondG, dst, src) }

// CMOVGE emits CMOVGE.
func (a *Asm) CMOVGE(dst, src Reg) { a.CMOVcc(CondGE, dst, src) }

// CMOVLE emits CMOVLE.
func (a *Asm) CMOVLE(dst, src Reg) { a.CMOVcc(CondLE, dst, src) }

// CMOVA emits CMOVA.
func (a *Asm) CMOVA(dst, src Reg) { a.CMOVcc(CondA, dst, src) }

// CMOVB emits CMOVB.
func (a *Asm) CMOVB(dst, src Reg) { a.CMOVcc(CondB, dst, src) }

// ========== XMM Arithmetic Instructions ==========

// AddSS emits ADDSS xmm, xmm/m32.
// Encoding: F3 0F 58 /r
func (a *Asm) AddSS(dst, src Operand) {
        a.emit(0xF3)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x58)
        a.emitModRMXMM(dst.Reg, src)
}

// AddSD emits ADDSD xmm, xmm/m64.
// Encoding: F2 0F 58 /r
func (a *Asm) AddSD(dst, src Operand) {
        a.emit(0xF2)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x58)
        a.emitModRMXMM(dst.Reg, src)
}

// SubSS emits SUBSS xmm, xmm/m32.
// Encoding: F3 0F 5C /r
func (a *Asm) SubSS(dst, src Operand) {
        a.emit(0xF3)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x5C)
        a.emitModRMXMM(dst.Reg, src)
}

// SubSD emits SUBSD xmm, xmm/m64.
// Encoding: F2 0F 5C /r
func (a *Asm) SubSD(dst, src Operand) {
        a.emit(0xF2)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x5C)
        a.emitModRMXMM(dst.Reg, src)
}

// MulSS emits MULSS xmm, xmm/m32.
// Encoding: F3 0F 59 /r
func (a *Asm) MulSS(dst, src Operand) {
        a.emit(0xF3)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x59)
        a.emitModRMXMM(dst.Reg, src)
}

// MulSD emits MULSD xmm, xmm/m64.
// Encoding: F2 0F 59 /r
func (a *Asm) MulSD(dst, src Operand) {
        a.emit(0xF2)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x59)
        a.emitModRMXMM(dst.Reg, src)
}

// DivSS emits DIVSS xmm, xmm/m32.
// Encoding: F3 0F 5E /r
func (a *Asm) DivSS(dst, src Operand) {
        a.emit(0xF3)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x5E)
        a.emitModRMXMM(dst.Reg, src)
}

// DivSD emits DIVSD xmm, xmm/m64.
// Encoding: F2 0F 5E /r
func (a *Asm) DivSD(dst, src Operand) {
        a.emit(0xF2)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x5E)
        a.emitModRMXMM(dst.Reg, src)
}

// SqrtSD emits SQRTSD xmm, xmm/m64.
// Encoding: F2 0F 51 /r
func (a *Asm) SqrtSD(dst, src Operand) {
        a.emit(0xF2)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x51)
        a.emitModRMXMM(dst.Reg, src)
}

// UCOMISS emits UCOMISS xmm, xmm/m32 (unordered compare single).
// Encoding: 0F 2E /r
func (a *Asm) UCOMISS(dst, src Operand) {
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x2E)
        a.emitModRMXMM(dst.Reg, src)
}

// UCOMISD emits UCOMISD xmm, xmm/m64 (unordered compare double).
// Encoding: 66 0F 2E /r
func (a *Asm) UCOMISD(dst, src Operand) {
        a.emit(0x66)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x2E)
        a.emitModRMXMM(dst.Reg, src)
}

// ========== Float/Integer Conversion Instructions ==========

// CVTSI2SS emits CVTSI2SS xmm, r/m32 (convert int32 to float).
// Encoding: F3 0F 2A /r
func (a *Asm) CVTSI2SS(dst Reg, src Operand) {
        a.emit(0xF3)
        a.emitREXForMixedXMM(dst, src, false)
        a.emit(0x0F)
        a.emit(0x2A)
        a.emitModRMMixed(dst, src)
}

// CVTSI2SS64 emits CVTSI2SS xmm, r/m64 (convert int64 to float).
// Encoding: REX.W F3 0F 2A /r
func (a *Asm) CVTSI2SS64(dst Reg, src Operand) {
        a.emit(0xF3)
        a.emitREXForMixedXMM(dst, src, true)
        a.emit(0x0F)
        a.emit(0x2A)
        a.emitModRMMixed(dst, src)
}

// CVTSI2SD emits CVTSI2SD xmm, r/m32 (convert int32 to double).
// Encoding: F2 0F 2A /r
func (a *Asm) CVTSI2SD(dst Reg, src Operand) {
        a.emit(0xF2)
        a.emitREXForMixedXMM(dst, src, false)
        a.emit(0x0F)
        a.emit(0x2A)
        a.emitModRMMixed(dst, src)
}

// CVTSI2SD64 emits CVTSI2SD xmm, r/m64 (convert int64 to double).
// Encoding: REX.W F2 0F 2A /r
func (a *Asm) CVTSI2SD64(dst Reg, src Operand) {
        a.emit(0xF2)
        a.emitREXForMixedXMM(dst, src, true)
        a.emit(0x0F)
        a.emit(0x2A)
        a.emitModRMMixed(dst, src)
}

// CVTSS2SI emits CVTSS2SI r32, xmm (convert float to int32).
// Encoding: F3 0F 2D /r
func (a *Asm) CVTSS2SI(dst Reg, src Reg) {
        a.emit(0xF3)
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2D)
        a.emitModRMReg(dst, src)
}

// CVTSS2SI64 emits CVTSS2SI r64, xmm (convert float to int64).
// Encoding: REX.W F3 0F 2D /r
func (a *Asm) CVTSS2SI64(dst Reg, src Reg) {
        a.emit(0xF3)
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2D)
        a.emitModRMReg(dst, src)
}

// CVTSD2SI emits CVTSD2SI r32, xmm (convert double to int32).
// Encoding: F2 0F 2D /r
func (a *Asm) CVTSD2SI(dst Reg, src Reg) {
        a.emit(0xF2)
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2D)
        a.emitModRMReg(dst, src)
}

// CVTSD2SI64 emits CVTSD2SI r64, xmm (convert double to int64).
// Encoding: REX.W F2 0F 2D /r
func (a *Asm) CVTSD2SI64(dst Reg, src Reg) {
        a.emit(0xF2)
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2D)
        a.emitModRMReg(dst, src)
}

// CVTTSD2SI emits CVTTSD2SI r32, xmm (convert double to int32 with truncation).
// Encoding: F2 0F 2C /r
func (a *Asm) CVTTSD2SI(dst Reg, src Reg) {
        a.emit(0xF2)
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2C)
        a.emitModRMReg(dst, src)
}

// CVTTSD2SI64 emits CVTTSD2SI r64, xmm (convert double to int64 with truncation).
// Encoding: REX.W F2 0F 2C /r
func (a *Asm) CVTTSD2SI64(dst Reg, src Reg) {
        a.emit(0xF2)
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2C)
        a.emitModRMReg(dst, src)
}

// CVTTSS2SI emits CVTTSS2SI r32, xmm (convert float to int32 with truncation).
// Encoding: F3 0F 2C /r
func (a *Asm) CVTTSS2SI(dst Reg, src Reg) {
        a.emit(0xF3)
        a.emitREX(false, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2C)
        a.emitModRMReg(dst, src)
}

// CVTTSS2SI64 emits CVTTSS2SI r64, xmm (convert float to int64 with truncation).
// Encoding: REX.W F3 0F 2C /r
func (a *Asm) CVTTSS2SI64(dst Reg, src Reg) {
        a.emit(0xF3)
        a.emitREX(true, dst.Extended, false, src.Extended)
        a.emit(0x0F)
        a.emit(0x2C)
        a.emitModRMReg(dst, src)
}

// CVTSD2SS emits CVTSD2SS xmm, xmm/m64 (convert double to float).
// Encoding: F2 0F 5A /r
func (a *Asm) CVTSD2SS(dst, src Operand) {
        a.emit(0xF2)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x5A)
        a.emitModRMXMM(dst.Reg, src)
}

// CVTSS2SD emits CVTSS2SD xmm, xmm/m32 (convert float to double).
// Encoding: F3 0F 5A /r
func (a *Asm) CVTSS2SD(dst, src Operand) {
        a.emit(0xF3)
        a.emitREXForXMM(dst.Reg, src)
        a.emit(0x0F)
        a.emit(0x5A)
        a.emitModRMXMM(dst.Reg, src)
}

// ========== NOP ==========

// NOP emits a single-byte NOP.
func (a *Asm) NOP() {
        a.emit(0x90)
}

// NOPLong emits a multi-byte NOP (for alignment). Size must be 1-15.
func (a *Asm) NOPLong(size int) {
        for size > 0 {
                n := size
                if n > 15 {
                        n = 15
                }
                switch {
                case n >= 9:
                        a.emit(0x66, 0x0F, 0x1F, 0x84, 0x00, 0x00, 0x00, 0x00, 0x00)
                        size -= 9
                case n >= 5:
                        a.emit(0x0F, 0x1F, 0x44, 0x00, 0x00)
                        size -= 5
                case n >= 3:
                        a.emit(0x0F, 0x1F, 0x00)
                        size -= 3
                case n >= 2:
                        a.emit(0x66, 0x90)
                        size -= 2
                default:
                        a.emit(0x90)
                        size -= 1
                }
        }
}

// ========== Misc Instructions ==========

// CDQ emits CDQ (sign-extend EAX into EDX:EAX for IDIV).
func (a *Asm) CDQ() {
        a.emit(0x99)
}

// CQO emits CQO (sign-extend RAX into RDX:RAX for IDIV, 64-bit).
func (a *Asm) CQO() {
        a.emitREX(true, false, false, false)
        a.emit(0x99)
}

// CBW emits CBW/CWDE/CDQE (sign-extend AL/AX/EAX to AX/EAX/RAX).
// 64-bit: REX.W 98
func (a *Asm) CBW(size int) {
        switch size {
        case 64:
                a.emitREX(true, false, false, false)
                a.emit(0x98)
        case 32:
                a.emit(0x98)
        case 16:
                a.emitSizeOverride()
                a.emit(0x98)
        case 8:
                a.emit(0x98)
        }
}

// CWD emits CWD (sign-extend AX into DX:AX, 16-bit).
func (a *Asm) CWD() {
        a.emitSizeOverride()
        a.emit(0x99)
}

// INT3 emits INT3 (breakpoint trap).
func (a *Asm) INT3() {
        a.emit(0xCC)
}

// UD2 emits UD2 (undefined instruction, guaranteed fault).
func (a *Asm) UD2() {
        a.emit(0x0F)
        a.emit(0x0B)
}

// HLT emits HLT (halt processor).
func (a *Asm) HLT() {
        a.emit(0xF4)
}

// SYSCALL emits the SYSCALL instruction (0x0F 0x05).
func (a *Asm) SYSCALL() {
        a.emit(0x0F)
        a.emit(0x05)
}

// ========== Helper functions for XMM instructions ==========

// emitREXForXMM emits REX prefix for XMM-XMM or XMM-mem operations.
func (a *Asm) emitREXForXMM(dst Reg, src Operand) {
        r := dst.Extended
        var b bool
        switch src.Kind {
        case OpReg:
                b = src.Reg.Extended
        case OpMem:
                b = src.Mem.HasBase && src.Mem.Base.Extended
        }
        _ = r // suppress unused
        if r || b {
                a.emitREX(false, r, false, b)
        }
}

// emitREXForMixedXMM emits REX for XMM dst with GPR/mem src (for CVTSI2SS etc).
func (a *Asm) emitREXForMixedXMM(dst Reg, src Operand, w bool) {
        r := dst.Extended
        var b bool
        switch src.Kind {
        case OpReg:
                b = src.Reg.Extended
        case OpMem:
                b = src.Mem.HasBase && src.Mem.Base.Extended
        }
        if w || r || b {
                a.emitREX(w, r, false, b)
        }
}

// emitModRMXMM emits ModR/M for XMM register-register or XMM-memory operations.
func (a *Asm) emitModRMXMM(dst Reg, src Operand) {
        switch src.Kind {
        case OpReg:
                a.emitModRMReg(dst, src.Reg)
        case OpMem:
                a.emitModRMMem(dst, src.Mem)
        }
}

// emitModRMMixed emits ModR/M for mixed XMM dst with GPR/mem src.
func (a *Asm) emitModRMMixed(dst Reg, src Operand) {
        switch src.Kind {
        case OpReg:
                a.emitModRMReg(dst, src.Reg)
        case OpMem:
                a.emitModRMMem(dst, src.Mem)
        }
}

// ========== Data Declaration Helpers ==========

// EmitInt64 emits a 64-bit integer value (for data sections).
func (a *Asm) EmitInt64(v int64) {
        a.emitU64(uint64(v))
}

// EmitInt32 emits a 32-bit integer value.
func (a *Asm) EmitInt32(v int32) {
        a.emitI32(v)
}

// EmitInt16 emits a 16-bit integer value.
func (a *Asm) EmitInt16(v int16) {
        a.emitI16(v)
}

// EmitInt8 emits an 8-bit integer value.
func (a *Asm) EmitInt8(v int8) {
        a.emitI8(v)
}

// EmitFloat64 emits a 64-bit IEEE 754 float.
func (a *Asm) EmitFloat64(v float64) {
        a.emitU64(math.Float64bits(v))
}

// EmitFloat32 emits a 32-bit IEEE 754 float.
func (a *Asm) EmitFloat32(v float32) {
        bits := math.Float32bits(v)
        a.emit(byte(bits))
        a.emit(byte(bits >> 8))
        a.emit(byte(bits >> 16))
        a.emit(byte(bits >> 24))
}

// EmitBytes emits raw bytes.
func (a *Asm) EmitBytes(b []byte) {
        a.code = append(a.code, b...)
}

// EmitZeros emits n zero bytes (for alignment/padding).
func (a *Asm) EmitZeros(n int) {
        for i := 0; i < n; i++ {
                a.emit(0)
        }
}

// AlignTo emits NOP padding to align the code offset to the given boundary.
func (a *Asm) AlignTo(alignment int) {
        for a.Offset()%alignment != 0 {
                a.NOP()
        }
}

// ========== PatchByte patches a single byte at the given offset.
func (a *Asm) PatchByte(offset int, v byte) {
        if offset < 0 || offset >= len(a.code) {
                panic(fmt.Sprintf("asm: patch offset %d out of range [0,%d)", offset, len(a.code)))
        }
        a.code[offset] = v
}

// PatchU32 patches a 32-bit value at the given offset.
func (a *Asm) PatchU32(offset int, v uint32) {
        if offset < 0 || offset+4 > len(a.code) {
                panic(fmt.Sprintf("asm: patch offset %d out of range [0,%d)", offset, len(a.code)))
        }
        binary.LittleEndian.PutUint32(a.code[offset:offset+4], v)
}
