// Package wasm implements a WebAssembly code generator for the Yilt compiler.
// It produces a valid WebAssembly binary module (.wasm) targeting the MVP specification
// with extensions for sign-extension, mutable globals, and bulk memory operations.
//
// Value model: Yilt's tagged values are mapped to i64 for integers/special values
// and f64 for float operations. The runtime provides boxing/unboxing helpers.
package wasm

import (
        "encoding/binary"
        "fmt"
        "math"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/diag"
)

// =============================================================================
// WASM binary format constants
// =============================================================================

// Magic number and version.
const (
        WasmMagic       = 0x6D736100 // "\0asm"
        WasmVersion     = 0x01        // version 1
)

// Section IDs.
const (
        SecCustom   = 0
        SecType     = 1
        SecImport   = 2
        SecFunction = 3
        SecTable    = 4
        SecMemory   = 5
        SecGlobal   = 6
        SecExport   = 7
        SecStart    = 8
        SecElem     = 9
        SecCode     = 10
        SecData     = 11
        SecDataCount = 12
)

// Value types.
const (
        TypeI32 = 0x7F
        TypeI64 = 0x7E
        TypeF32 = 0x7D
        TypeF64 = 0x7C
        TypeFuncRef = 0x70
        TypeExternRef = 0x6F
        TypeFunc = 0x60 // block type for function
        TypeEmptyBlock = 0x40 // void block type
)

// Function type encoding.
// funcType = 0x60 paramcount param* resultcount result*

// Import/export kinds.
const (
        ImportFunc   = 0
        ImportTable  = 1
        ImportMemory = 2
        ImportGlobal = 3
        ExportFunc   = 0
        ExportTable  = 1
        ExportMemory = 2
        ExportGlobal = 3
)

// Global kinds.
const (
        GlobalImmutable = 0
        GlobalMutable   = 1
)

// Limits.
const (
        LimitsNoMax   = 0
        LimitsHasMax  = 1
        LimitsShared  = 2
)

// Table element kinds.
const (
        ElemFuncRef = 0x00
)

// Opcodes.
const (
        // Control instructions.
        OpUnreachable = 0x00
        OpNop         = 0x01
        OpBlock       = 0x02
        OpLoop        = 0x03
        OpIf          = 0x04
        OpElse        = 0x05
        OpEnd         = 0x0B
        OpBr          = 0x0C
        OpBrIf        = 0x0D
        OpBrTable     = 0x0E
        OpReturn      = 0x0F
        OpCall        = 0x10
        OpCallIndirect = 0x11

        // Parametric instructions.
        OpDrop   = 0x1A
        OpSelect = 0x1B

        // Variable instructions.
        OpLocalGet  = 0x20
        OpLocalSet  = 0x21
        OpLocalTee  = 0x22
        OpGlobalGet = 0x23
        OpGlobalSet = 0x24

        // Memory instructions.
        OpI32Load      = 0x28
        OpI64Load      = 0x29
        OpF32Load      = 0x2A
        OpF64Load      = 0x2B
        OpI32Load8S    = 0x2C
        OpI32Load8U    = 0x2D
        OpI32Load16S   = 0x2E
        OpI32Load16U   = 0x2F
        OpI64Load8S    = 0x30
        OpI64Load8U    = 0x31
        OpI64Load16S   = 0x32
        OpI64Load16U   = 0x33
        OpI64Load32S   = 0x34
        OpI64Load32U   = 0x35
        OpI32Store     = 0x36
        OpI64Store     = 0x37
        OpF32Store     = 0x38
        OpF64Store     = 0x39
        OpI32Store8    = 0x3A
        OpI32Store16   = 0x3B
        OpI64Store8    = 0x3C
        OpI64Store16   = 0x3D
        OpI64Store32   = 0x3E
        OpMemorySize   = 0x3F
        OpMemoryGrow   = 0x40
        OpMemoryInit   = 0x08
        OpDataDrop     = 0x09
        OpMemoryCopy   = 0x0A
        OpMemoryFill   = 0x0B
        OpTableInit    = 0x0C
        OpElemDrop     = 0x0D
        OpTableCopy    = 0x0E
        OpTableGrow    = 0x0F
        OpTableSize    = 0x10
        OpTableFill    = 0x11

        // Constants.
        OpI32Const = 0x41
        OpI64Const = 0x42
        OpF32Const = 0x43
        OpF64Const = 0x44

        // i32 comparison.
        OpI32Eqz  = 0x45
        OpI32Eq   = 0x46
        OpI32Ne   = 0x47
        OpI32LtS  = 0x48
        OpI32LtU  = 0x49
        OpI32GtS  = 0x4A
        OpI32GtU  = 0x4B
        OpI32LeS  = 0x4C
        OpI32LeU  = 0x4D
        OpI32GeS  = 0x4E
        OpI32GeU  = 0x4F

        // i64 comparison.
        OpI64Eqz  = 0x50
        OpI64Eq   = 0x51
        OpI64Ne   = 0x52
        OpI64LtS  = 0x53
        OpI64LtU  = 0x54
        OpI64GtS  = 0x55
        OpI64GtU  = 0x56
        OpI64LeS  = 0x57
        OpI64LeU  = 0x58
        OpI64GeS  = 0x59
        OpI64GeU  = 0x5A

        // f32 comparison.
        OpF32Eq   = 0x5B
        OpF32Ne   = 0x5C
        OpF32Lt   = 0x5D
        OpF32Gt   = 0x5E
        OpF32Le   = 0x5F
        OpF32Ge   = 0x60

        // f64 comparison.
        OpF64Eq   = 0x61
        OpF64Ne   = 0x62
        OpF64Lt   = 0x63
        OpF64Gt   = 0x64
        OpF64Le   = 0x65
        OpF64Ge   = 0x66

        // i32 arithmetic.
        OpI32Clz    = 0x67
        OpI32Ctz    = 0x68
        OpI32Popcnt = 0x69
        OpI32Add    = 0x6A
        OpI32Sub    = 0x6B
        OpI32Mul    = 0x6C
        OpI32DivS   = 0x6D
        OpI32DivU   = 0x6E
        OpI32RemS   = 0x6F
        OpI32RemU   = 0x70
        OpI32And    = 0x71
        OpI32Or     = 0x72
        OpI32Xor    = 0x73
        OpI32Shl    = 0x74
        OpI32ShrS   = 0x75
        OpI32ShrU   = 0x76
        OpI32RotL   = 0x77
        OpI32RotR   = 0x78

        // i64 arithmetic.
        OpI64Clz    = 0x79
        OpI64Ctz    = 0x7A
        OpI64Popcnt = 0x7B
        OpI64Add    = 0x7C
        OpI64Sub    = 0x7D
        OpI64Mul    = 0x7E
        OpI64DivS   = 0x7F
        OpI64DivU   = 0x80
        OpI64RemS   = 0x81
        OpI64RemU   = 0x82
        OpI64And    = 0x83
        OpI64Or     = 0x84
        OpI64Xor    = 0x85
        OpI64Shl    = 0x86
        OpI64ShrS   = 0x87
        OpI64ShrU   = 0x88
        OpI64RotL   = 0x89
        OpI64RotR   = 0x8A

        // f32 arithmetic.
        OpF32Abs    = 0x8B
        OpF32Neg    = 0x8C
        OpF32Ceil   = 0x8D
        OpF32Floor  = 0x8E
        OpF32Trunc  = 0x8F
        OpF32Nearest = 0x90
        OpF32Sqrt   = 0x91
        OpF32Add    = 0x92
        OpF32Sub    = 0x93
        OpF32Mul    = 0x94
        OpF32Div    = 0x95
        OpF32Min    = 0x96
        OpF32Max    = 0x97
        OpF32Copysign = 0x98

        // f64 arithmetic.
        OpF64Abs    = 0x99
        OpF64Neg    = 0x9A
        OpF64Ceil   = 0x9B
        OpF64Floor  = 0x9C
        OpF64Trunc  = 0x9D
        OpF64Nearest = 0x9E
        OpF64Sqrt   = 0x9F
        OpF64Add    = 0xA0
        OpF64Sub    = 0xA1
        OpF64Mul    = 0xA2
        OpF64Div    = 0xA3
        OpF64Min    = 0xA4
        OpF64Max    = 0xA5
        OpF64Copysign = 0xA6

        // i32 conversions.
        OpI32WrapI64      = 0xA7
        OpI32TruncF32S    = 0xA8
        OpI32TruncF32U    = 0xA9
        OpI32TruncF64S    = 0xAA
        OpI32TruncF64U    = 0xAB
        OpI64ExtendI32S   = 0xAC
        OpI64ExtendI32U   = 0xAD
        OpI64TruncF32S    = 0xAE
        OpI64TruncF32U    = 0xAF
        OpI64TruncF64S    = 0xB0
        OpI64TruncF64U    = 0xB1
        OpF32ConvertI32S  = 0xB2
        OpF32ConvertI32U  = 0xB3
        OpF32ConvertI64S  = 0xB4
        OpF32ConvertI64U  = 0xB5
        OpF64ConvertI32S  = 0xB6
        OpF64ConvertI32U  = 0xB7
        OpF64ConvertI64S  = 0xB8
        OpF64ConvertI64U  = 0xB9
        OpI32ReinterpretF32 = 0xBC
        OpI64ReinterpretF64 = 0xBD
        OpF32ReinterpretI32 = 0xBE
        OpF64ReinterpretI64 = 0xBF

        // Sign extension (proposed extension).
        OpI32Extend8S  = 0xC0
        OpI32Extend16S = 0xC1
        OpI64Extend8S  = 0xC2
        OpI64Extend16S = 0xC3
        OpI64Extend32S = 0xC4

        // Saturating truncation (proposal).
        OpI32TruncSatF64S = 0xFC // followed by 0x00
        OpI32TruncSatF64U = 0xFC // followed by 0x01
)

// Memory alignment and offset encoding.
const (
        Align1  = 0 // 2^0 = 1 byte
        Align2  = 1 // 2^1 = 2 bytes
        Align4  = 2 // 2^2 = 4 bytes
        Align8  = 3 // 2^3 = 8 bytes
)

// =============================================================================
// Tagged value encoding for WASM
// =============================================================================
// Yilt uses i64 as the universal value type on WASM.
// Tagging scheme:
//
//      [63]        = tag: 0 = int, 1 = special (bool/nil)
//      [62:0]      = for int: 63-bit signed integer
//                  = for special: 0 = nil, 1 = true, 2 = false
//
// Floats are stored as f64 in a separate value type, with boxing/unboxing
// to/from i64 using bitwise reinterpretation.

const (
        // TagBit shifts and masks for the i64 tagged value model.
        WasmTagBit uint64 = 1 << 63

        // WasmTagIntMask is the mask for integer values (all bits except tag).
        WasmTagIntMask uint64 = ^(uint64(1) << 63)

        // Special value encodings (when tag bit is set).
        WasmNil   uint64 = (1 << 63) | 0 // 0x8000000000000000
        WasmTrue  uint64 = (1 << 63) | 1 // 0x8000000000000001
        WasmFalse uint64 = (1 << 63) | 2 // 0x8000000000000002
)

// TagWasmInt creates a tagged i64 integer value.
func TagWasmInt(v int64) uint64 {
        return uint64(v) & WasmTagIntMask
}

// TagWasmBool creates a tagged i64 boolean value.
func TagWasmBool(b bool) uint64 {
        if b {
                return WasmTrue
        }
        return WasmFalse
}

// IsWasmFloat checks if we need to unbox a value from i64 to f64.
// In our model, float values are carried as f64 directly on the WASM stack.
// Tagged i64 values represent ints, bools, nil.

// =============================================================================
// WASM binary writer
// =============================================================================

// WasmWriter is a helper for writing the WASM binary format.
type WasmWriter struct {
        buf []byte
}

// NewWasmWriter creates a new WASM binary writer.
func NewWasmWriter() *WasmWriter {
        return &WasmWriter{buf: make([]byte, 0, 4096)}
}

// Bytes returns the written bytes.
func (w *WasmWriter) Bytes() []byte {
        return w.buf
}

// Len returns the current length.
func (w *WasmWriter) Len() int {
        return len(w.buf)
}

// WriteByte writes a single byte.
func (w *WasmWriter) WriteByte(b byte) error {
        w.buf = append(w.buf, b)
        return nil
}

// WriteBytes writes a slice of bytes.
func (w *WasmWriter) WriteBytes(b []byte) {
        w.buf = append(w.buf, b...)
}

// WriteU32 writes a 32-bit unsigned integer as LEB128.
func (w *WasmWriter) WriteU32(v uint32) {
        for {
                b := byte(v & 0x7F)
                v >>= 7
                if v != 0 {
                        b |= 0x80
                }
                w.buf = append(w.buf, b)
                if v == 0 {
                        break
                }
        }
}

// WriteU64 writes a 64-bit unsigned integer as LEB128.
func (w *WasmWriter) WriteU64(v uint64) {
        for {
                b := byte(v & 0x7F)
                v >>= 7
                if v != 0 {
                        b |= 0x80
                }
                w.buf = append(w.buf, b)
                if v == 0 {
                        break
                }
        }
}

// WriteI32 writes a 32-bit signed integer as signed LEB128.
func (w *WasmWriter) WriteI32(v int32) {
        uv := uint32(v)
        more := true
        for more {
                b := byte(uv & 0x7F)
                uv >>= 7
                // Sign bit of byte is the high bit of the next byte
                if (uv == 0 && (b&0x40) == 0) || (uv == ^uint32(0) && (b&0x40) != 0) {
                        more = false
                } else {
                        b |= 0x80
                }
                w.buf = append(w.buf, b)
        }
}

// WriteI64 writes a 64-bit signed integer as signed LEB128.
func (w *WasmWriter) WriteI64(v int64) {
        uv := uint64(v)
        more := true
        for more {
                b := byte(uv & 0x7F)
                uv >>= 7
                if (uv == 0 && (b&0x40) == 0) || (uv == ^uint64(0) && (b&0x40) != 0) {
                        more = false
                } else {
                        b |= 0x80
                }
                w.buf = append(w.buf, b)
        }
}

// WriteF32 writes a 32-bit float in IEEE 754 format.
func (w *WasmWriter) WriteF32(v float32) {
        bits := math.Float32bits(v)
        var buf [4]byte
        binary.LittleEndian.PutUint32(buf[:], bits)
        w.buf = append(w.buf, buf[:]...)
}

// WriteF64 writes a 64-bit float in IEEE 754 format.
func (w *WasmWriter) WriteF64(v float64) {
        bits := math.Float64bits(v)
        var buf [8]byte
        binary.LittleEndian.PutUint64(buf[:], bits)
        w.buf = append(w.buf, buf[:]...)
}

// WriteName writes a WASM name (length-prefixed UTF-8 string).
func (w *WasmWriter) WriteName(s string) {
        w.WriteU32(uint32(len(s)))
        w.WriteBytes([]byte(s))
}

// WriteSectionHeader writes a section ID and placeholder size.
// Returns a function that should be called to patch the size.
func (w *WasmWriter) WriteSectionHeader(id byte) (patch func()) {
        w.WriteByte(id)
        sizePos := len(w.buf)
        w.WriteU32(0) // placeholder
        return func() {
                sectionSize := uint32(len(w.buf) - sizePos - leb128Size(0))
                // Overwrite the placeholder with the actual size
                patchLEB128U32(w.buf[sizePos:], sectionSize)
        }
}

// WriteMagic writes the WASM magic number and version.
func (w *WasmWriter) WriteMagic() {
        var buf [8]byte
        binary.LittleEndian.PutUint32(buf[0:4], WasmMagic)
        binary.LittleEndian.PutUint32(buf[4:8], WasmVersion)
        w.WriteBytes(buf[:])
}

// leb128Size returns the encoded size of a uint32 LEB128 value.
func leb128Size(v uint32) int {
        size := 1
        for v >= 0x80 {
                v >>= 7
                size++
        }
        return size
}

// patchLEB128U32 patches a LEB128 uint32 at the given position in buf.
func patchLEB128U32(buf []byte, v uint32) {
        i := 0
        for {
                b := byte(v & 0x7F)
                v >>= 7
                if v != 0 {
                        b |= 0x80
                }
                buf[i] = b
                i++
                if v == 0 {
                        break
                }
        }
}

// =============================================================================
// Function signature representation
// =============================================================================

// FuncType represents a WebAssembly function type.
type FuncType struct {
        Params  []byte // value types
        Results []byte // value types
}

// Key returns a unique string key for this function type (for deduplication).
func (ft *FuncType) Key() string {
        key := fmt.Sprintf("func(%v)->(%v)", ft.Params, ft.Results)
        return key
}

// Encode writes the function type to a WASM writer.
func (ft *FuncType) Encode(w *WasmWriter) {
        w.WriteByte(TypeFunc)
        w.WriteU32(uint32(len(ft.Params)))
        for _, p := range ft.Params {
                w.WriteByte(p)
        }
        w.WriteU32(uint32(len(ft.Results)))
        for _, r := range ft.Results {
                w.WriteByte(r)
        }
}

// Common function types.
var (
        TypeVoid     = &FuncType{Params: nil, Results: nil}
        TypeI32Void  = &FuncType{Params: []byte{TypeI32}, Results: nil}
        TypeI64Void  = &FuncType{Params: []byte{TypeI64}, Results: nil}
        TypeI32I32   = &FuncType{Params: []byte{TypeI32}, Results: []byte{TypeI32}}
        TypeI64I64   = &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI64}}
        TypeI32I64   = &FuncType{Params: []byte{TypeI32}, Results: []byte{TypeI64}}
        TypeI64I32   = &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI32}}
        TypeI64I64I64 = &FuncType{Params: []byte{TypeI64, TypeI64}, Results: []byte{TypeI64}}
        FuncI32      = &FuncType{Params: nil, Results: []byte{TypeI32}}
        FuncI64      = &FuncType{Params: nil, Results: []byte{TypeI64}}
        FuncF64      = &FuncType{Params: nil, Results: []byte{TypeF64}}
        TypeI64F64   = &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeF64}}
        TypeF64F64   = &FuncType{Params: []byte{TypeF64}, Results: []byte{TypeF64}}
)

// =============================================================================
// Import representation
// =============================================================================

// Import represents a WebAssembly import.
type Import struct {
        Module string
        Name   string
        Kind   byte // ImportFunc, ImportTable, ImportMemory, ImportGlobal
        Desc   interface{} // *FuncType for funcs, etc.
}

// Encode writes the import to a WASM writer.
func (imp *Import) Encode(w *WasmWriter) {
        w.WriteName(imp.Module)
        w.WriteName(imp.Name)
        w.WriteByte(imp.Kind)
        switch imp.Kind {
        case ImportFunc:
                ft := imp.Desc.(*FuncType)
                ft.Encode(w)
        case ImportMemory:
                mem := imp.Desc.(*MemoryDesc)
                w.WriteByte(LimitsNoMax)
                w.WriteU32(mem.Initial)
        }
}

// MemoryDesc describes a memory import.
type MemoryDesc struct {
        Initial uint32
        Max     uint32
        Shared  bool
}

// =============================================================================
// Global representation
// =============================================================================

// Global represents a WebAssembly global variable.
type Global struct {
        Type     byte // value type
        Mutable  bool
        InitExpr []byte // initialization expression bytes
}

// =============================================================================
// Export representation
// =============================================================================

// Export represents a WebAssembly export.
type Export struct {
        Name  string
        Kind  byte
        Index uint32
}

// Encode writes the export to a WASM writer.
func (exp *Export) Encode(w *WasmWriter) {
        w.WriteName(exp.Name)
        w.WriteByte(exp.Kind)
        w.WriteU32(exp.Index)
}

// =============================================================================
// Local variable representation
// =============================================================================

// LocalDecl represents a local variable declaration (count + type).
type LocalDecl struct {
        Count uint32
        Type  byte
}

// =============================================================================
// Function body builder
// =============================================================================

// FuncBody is a builder for a WebAssembly function body.
type FuncBody struct {
        Locals  []LocalDecl
        Code    []byte
        writer  *WasmWriter
}

// NewFuncBody creates a new function body builder.
func NewFuncBody() *FuncBody {
        return &FuncBody{
                Locals: make([]LocalDecl, 0),
                Code:   make([]byte, 0, 128),
                writer: &WasmWriter{},
        }
}

// AddLocal adds a local variable declaration.
func (fb *FuncBody) AddLocal(count uint32, typ byte) {
        fb.Locals = append(fb.Locals, LocalDecl{Count: count, Type: typ})
}

// AddLocals adds N local variables of the given type.
func (fb *FuncBody) AddLocals(n uint32, typ byte) {
        if n == 0 {
                return
        }
        // Merge with existing if same type
        for i := range fb.Locals {
                if fb.Locals[i].Type == typ {
                        fb.Locals[i].Count += n
                        return
                }
        }
        fb.Locals = append(fb.Locals, LocalDecl{Count: n, Type: typ})
}

// Emit emits a single byte (opcode or operand).
func (fb *FuncBody) Emit(b ...byte) {
        fb.Code = append(fb.Code, b...)
}

// EmitU32 emits a uint32 LEB128 value.
func (fb *FuncBody) EmitU32(v uint32) {
        var buf [5]byte
        n := 0
        for {
                b := byte(v & 0x7F)
                v >>= 7
                if v != 0 {
                        b |= 0x80
                }
                buf[n] = b
                n++
                if v == 0 {
                        break
                }
        }
        fb.Code = append(fb.Code, buf[:n]...)
}

// EmitI32 emits a signed int32 LEB128 value.
func (fb *FuncBody) EmitI32(v int32) {
        uv := uint32(v)
        for {
                b := byte(uv & 0x7F)
                uv >>= 7
                if (uv == 0 && (b&0x40) == 0) || (uv == ^uint32(0) && (b&0x40) != 0) {
                        fb.Code = append(fb.Code, b)
                        break
                }
                b |= 0x80
                fb.Code = append(fb.Code, b)
        }
}

// EmitI64 emits a signed int64 LEB128 value.
func (fb *FuncBody) EmitI64(v int64) {
        uv := uint64(v)
        for {
                b := byte(uv & 0x7F)
                uv >>= 7
                if (uv == 0 && (b&0x40) == 0) || (uv == ^uint64(0) && (b&0x40) != 0) {
                        fb.Code = append(fb.Code, b)
                        break
                }
                b |= 0x80
                fb.Code = append(fb.Code, b)
        }
}

// EmitF64 emits a 64-bit float constant.
func (fb *FuncBody) EmitF64(v float64) {
        fb.Code = append(fb.Code, OpF64Const)
        bits := math.Float64bits(v)
        var buf [8]byte
        binary.LittleEndian.PutUint64(buf[:], bits)
        fb.Code = append(fb.Code, buf[:]...)
}

// EmitF32 emits a 32-bit float constant.
func (fb *FuncBody) EmitF32(v float32) {
        fb.Code = append(fb.Code, OpF32Const)
        bits := math.Float32bits(v)
        var buf [4]byte
        binary.LittleEndian.PutUint32(buf[:], bits)
        fb.Code = append(fb.Code, buf[:]...)
}

// Encode serializes the function body in the WASM binary format.
func (fb *FuncBody) Encode(w *WasmWriter) {
        // Build the full body: locals + code + end
        var body *WasmWriter
        if fb.writer == nil {
                body = NewWasmWriter()
        } else {
                fb.writer.buf = fb.writer.buf[:0]
                body = fb.writer
        }

        // Encode locals
        body.WriteU32(uint32(len(fb.Locals)))
        for _, l := range fb.Locals {
                body.WriteU32(l.Count)
                body.WriteByte(l.Type)
        }

        // Code
        body.WriteBytes(fb.Code)

        // End opcode
        body.WriteByte(OpEnd)

        // Write as a sized section
        w.WriteU32(uint32(body.Len()))
        w.WriteBytes(body.Bytes())
}

// CodeLen returns the length of the code without the end byte.
func (fb *FuncBody) CodeLen() int {
        return len(fb.Code)
}

// =============================================================================
// WASM instruction builders
// =============================================================================

// These methods on FuncBody provide a fluent interface for emitting instructions.

// --- Control ---

func (fb *FuncBody) Block(bt byte) { fb.Emit(OpBlock, bt) }
func (fb *FuncBody) Loop(bt byte)  { fb.Emit(OpLoop, bt) }
func (fb *FuncBody) If(bt byte)    { fb.Emit(OpIf, bt) }
func (fb *FuncBody) Else()         { fb.Emit(OpElse) }
func (fb *FuncBody) End()          { fb.Emit(OpEnd) }
func (fb *FuncBody) Br(depth uint32)       { fb.Emit(OpBr); fb.EmitU32(depth) }
func (fb *FuncBody) BrIf(depth uint32)     { fb.Emit(OpBrIf); fb.EmitU32(depth) }
func (fb *FuncBody) BrTable(depths []uint32, defaultDepth uint32) {
        fb.Emit(OpBrTable)
        fb.EmitU32(uint32(len(depths)))
        for _, d := range depths {
                fb.EmitU32(d)
        }
        fb.EmitU32(defaultDepth)
}
func (fb *FuncBody) Return()       { fb.Emit(OpReturn) }
func (fb *FuncBody) Call(idx uint32) { fb.Emit(OpCall); fb.EmitU32(idx) }
func (fb *FuncBody) CallIndirect(typeIdx uint32, tableIdx uint32) {
        fb.Emit(OpCallIndirect)
        fb.EmitU32(typeIdx)
        fb.EmitU32(tableIdx)
}
func (fb *FuncBody) Unreachable()  { fb.Emit(OpUnreachable) }
func (fb *FuncBody) Nop()          { fb.Emit(OpNop) }

// --- Parametric ---

func (fb *FuncBody) Drop()         { fb.Emit(OpDrop) }
func (fb *FuncBody) Select()       { fb.Emit(OpSelect) }

// --- Variable ---

func (fb *FuncBody) LocalGet(idx uint32)  { fb.Emit(OpLocalGet); fb.EmitU32(idx) }
func (fb *FuncBody) LocalSet(idx uint32)  { fb.Emit(OpLocalSet); fb.EmitU32(idx) }
func (fb *FuncBody) LocalTee(idx uint32)  { fb.Emit(OpLocalTee); fb.EmitU32(idx) }
func (fb *FuncBody) GlobalGet(idx uint32) { fb.Emit(OpGlobalGet); fb.EmitU32(idx) }
func (fb *FuncBody) GlobalSet(idx uint32) { fb.Emit(OpGlobalSet); fb.EmitU32(idx) }

// --- Memory ---

func (fb *FuncBody) I32Load(align, offset uint32)   { fb.Emit(OpI32Load); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Load(align, offset uint32)   { fb.Emit(OpI64Load); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) F64Load(align, offset uint32)   { fb.Emit(OpF64Load); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I32Store(align, offset uint32)  { fb.Emit(OpI32Store); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Store(align, offset uint32)  { fb.Emit(OpI64Store); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) F64Store(align, offset uint32)  { fb.Emit(OpF64Store); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I32Load8S(align, offset uint32) { fb.Emit(OpI32Load8S); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I32Load8U(align, offset uint32) { fb.Emit(OpI32Load8U); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I32Load16S(align, offset uint32) { fb.Emit(OpI32Load16S); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I32Load16U(align, offset uint32) { fb.Emit(OpI32Load16U); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Load8S(align, offset uint32) { fb.Emit(OpI64Load8S); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Load8U(align, offset uint32) { fb.Emit(OpI64Load8U); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Load16S(align, offset uint32) { fb.Emit(OpI64Load16S); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Load16U(align, offset uint32) { fb.Emit(OpI64Load16U); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Load32S(align, offset uint32) { fb.Emit(OpI64Load32S); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Load32U(align, offset uint32) { fb.Emit(OpI64Load32U); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I32Store8(align, offset uint32)  { fb.Emit(OpI32Store8); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I32Store16(align, offset uint32) { fb.Emit(OpI32Store16); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Store8(align, offset uint32)  { fb.Emit(OpI64Store8); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Store16(align, offset uint32) { fb.Emit(OpI64Store16); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) I64Store32(align, offset uint32) { fb.Emit(OpI64Store32); fb.EmitU32(align); fb.EmitU32(offset) }
func (fb *FuncBody) MemorySize(memIdx uint32)        { fb.Emit(OpMemorySize); fb.EmitU32(memIdx) }
func (fb *FuncBody) MemoryGrow(memIdx uint32)        { fb.Emit(OpMemoryGrow); fb.EmitU32(memIdx) }

// --- Constants ---

func (fb *FuncBody) I32Const(v int32)  { fb.Emit(OpI32Const); fb.EmitI32(v) }
func (fb *FuncBody) I64Const(v int64)  { fb.Emit(OpI64Const); fb.EmitI64(v) }

// --- i32 comparison ---

func (fb *FuncBody) I32Eqz()  { fb.Emit(OpI32Eqz) }
func (fb *FuncBody) I32Eq()   { fb.Emit(OpI32Eq) }
func (fb *FuncBody) I32Ne()   { fb.Emit(OpI32Ne) }
func (fb *FuncBody) I32LtS()  { fb.Emit(OpI32LtS) }
func (fb *FuncBody) I32LtU()  { fb.Emit(OpI32LtU) }
func (fb *FuncBody) I32GtS()  { fb.Emit(OpI32GtS) }
func (fb *FuncBody) I32GtU()  { fb.Emit(OpI32GtU) }
func (fb *FuncBody) I32LeS()  { fb.Emit(OpI32LeS) }
func (fb *FuncBody) I32LeU()  { fb.Emit(OpI32LeU) }
func (fb *FuncBody) I32GeS()  { fb.Emit(OpI32GeS) }
func (fb *FuncBody) I32GeU()  { fb.Emit(OpI32GeU) }

// --- i64 comparison ---

func (fb *FuncBody) I64Eqz()  { fb.Emit(OpI64Eqz) }
func (fb *FuncBody) I64Eq()   { fb.Emit(OpI64Eq) }
func (fb *FuncBody) I64Ne()   { fb.Emit(OpI64Ne) }
func (fb *FuncBody) I64LtS()  { fb.Emit(OpI64LtS) }
func (fb *FuncBody) I64LtU()  { fb.Emit(OpI64LtU) }
func (fb *FuncBody) I64GtS()  { fb.Emit(OpI64GtS) }
func (fb *FuncBody) I64GtU()  { fb.Emit(OpI64GtU) }
func (fb *FuncBody) I64LeS()  { fb.Emit(OpI64LeS) }
func (fb *FuncBody) I64LeU()  { fb.Emit(OpI64LeU) }
func (fb *FuncBody) I64GeS()  { fb.Emit(OpI64GeS) }
func (fb *FuncBody) I64GeU()  { fb.Emit(OpI64GeU) }

// --- f32 comparison ---

func (fb *FuncBody) F32Eq() { fb.Emit(OpF32Eq) }
func (fb *FuncBody) F32Ne() { fb.Emit(OpF32Ne) }
func (fb *FuncBody) F32Lt() { fb.Emit(OpF32Lt) }
func (fb *FuncBody) F32Gt() { fb.Emit(OpF32Gt) }
func (fb *FuncBody) F32Le() { fb.Emit(OpF32Le) }
func (fb *FuncBody) F32Ge() { fb.Emit(OpF32Ge) }

// --- f64 comparison ---

func (fb *FuncBody) F64Eq() { fb.Emit(OpF64Eq) }
func (fb *FuncBody) F64Ne() { fb.Emit(OpF64Ne) }
func (fb *FuncBody) F64Lt() { fb.Emit(OpF64Lt) }
func (fb *FuncBody) F64Gt() { fb.Emit(OpF64Gt) }
func (fb *FuncBody) F64Le() { fb.Emit(OpF64Le) }
func (fb *FuncBody) F64Ge() { fb.Emit(OpF64Ge) }

// --- i32 arithmetic ---

func (fb *FuncBody) I32Clz()    { fb.Emit(OpI32Clz) }
func (fb *FuncBody) I32Ctz()    { fb.Emit(OpI32Ctz) }
func (fb *FuncBody) I32Popcnt() { fb.Emit(OpI32Popcnt) }
func (fb *FuncBody) I32Add()    { fb.Emit(OpI32Add) }
func (fb *FuncBody) I32Sub()    { fb.Emit(OpI32Sub) }
func (fb *FuncBody) I32Mul()    { fb.Emit(OpI32Mul) }
func (fb *FuncBody) I32DivS()   { fb.Emit(OpI32DivS) }
func (fb *FuncBody) I32DivU()   { fb.Emit(OpI32DivU) }
func (fb *FuncBody) I32RemS()   { fb.Emit(OpI32RemS) }
func (fb *FuncBody) I32RemU()   { fb.Emit(OpI32RemU) }
func (fb *FuncBody) I32And()    { fb.Emit(OpI32And) }
func (fb *FuncBody) I32Or()     { fb.Emit(OpI32Or) }
func (fb *FuncBody) I32Xor()    { fb.Emit(OpI32Xor) }
func (fb *FuncBody) I32Shl()    { fb.Emit(OpI32Shl) }
func (fb *FuncBody) I32ShrS()   { fb.Emit(OpI32ShrS) }
func (fb *FuncBody) I32ShrU()   { fb.Emit(OpI32ShrU) }
func (fb *FuncBody) I32RotL()   { fb.Emit(OpI32RotL) }
func (fb *FuncBody) I32RotR()   { fb.Emit(OpI32RotR) }

// --- i64 arithmetic ---

func (fb *FuncBody) I64Clz()    { fb.Emit(OpI64Clz) }
func (fb *FuncBody) I64Ctz()    { fb.Emit(OpI64Ctz) }
func (fb *FuncBody) I64Popcnt() { fb.Emit(OpI64Popcnt) }
func (fb *FuncBody) I64Add()    { fb.Emit(OpI64Add) }
func (fb *FuncBody) I64Sub()    { fb.Emit(OpI64Sub) }
func (fb *FuncBody) I64Mul()    { fb.Emit(OpI64Mul) }
func (fb *FuncBody) I64DivS()   { fb.Emit(OpI64DivS) }
func (fb *FuncBody) I64DivU()   { fb.Emit(OpI64DivU) }
func (fb *FuncBody) I64RemS()   { fb.Emit(OpI64RemS) }
func (fb *FuncBody) I64RemU()   { fb.Emit(OpI64RemU) }
func (fb *FuncBody) I64And()    { fb.Emit(OpI64And) }
func (fb *FuncBody) I64Or()     { fb.Emit(OpI64Or) }
func (fb *FuncBody) I64Xor()    { fb.Emit(OpI64Xor) }
func (fb *FuncBody) I64Shl()    { fb.Emit(OpI64Shl) }
func (fb *FuncBody) I64ShrS()   { fb.Emit(OpI64ShrS) }
func (fb *FuncBody) I64ShrU()   { fb.Emit(OpI64ShrU) }
func (fb *FuncBody) I64RotL()   { fb.Emit(OpI64RotL) }
func (fb *FuncBody) I64RotR()   { fb.Emit(OpI64RotR) }

// --- f32 arithmetic ---

func (fb *FuncBody) F32Abs()    { fb.Emit(OpF32Abs) }
func (fb *FuncBody) F32Neg()    { fb.Emit(OpF32Neg) }
func (fb *FuncBody) F32Ceil()   { fb.Emit(OpF32Ceil) }
func (fb *FuncBody) F32Floor()  { fb.Emit(OpF32Floor) }
func (fb *FuncBody) F32Trunc()  { fb.Emit(OpF32Trunc) }
func (fb *FuncBody) F32Nearest() { fb.Emit(OpF32Nearest) }
func (fb *FuncBody) F32Sqrt()   { fb.Emit(OpF32Sqrt) }
func (fb *FuncBody) F32Add()    { fb.Emit(OpF32Add) }
func (fb *FuncBody) F32Sub()    { fb.Emit(OpF32Sub) }
func (fb *FuncBody) F32Mul()    { fb.Emit(OpF32Mul) }
func (fb *FuncBody) F32Div()    { fb.Emit(OpF32Div) }
func (fb *FuncBody) F32Min()    { fb.Emit(OpF32Min) }
func (fb *FuncBody) F32Max()    { fb.Emit(OpF32Max) }
func (fb *FuncBody) F32Copysign() { fb.Emit(OpF32Copysign) }

// --- f64 arithmetic ---

func (fb *FuncBody) F64Abs()    { fb.Emit(OpF64Abs) }
func (fb *FuncBody) F64Neg()    { fb.Emit(OpF64Neg) }
func (fb *FuncBody) F64Ceil()   { fb.Emit(OpF64Ceil) }
func (fb *FuncBody) F64Floor()  { fb.Emit(OpF64Floor) }
func (fb *FuncBody) F64Trunc()  { fb.Emit(OpF64Trunc) }
func (fb *FuncBody) F64Nearest() { fb.Emit(OpF64Nearest) }
func (fb *FuncBody) F64Sqrt()   { fb.Emit(OpF64Sqrt) }
func (fb *FuncBody) F64Add()    { fb.Emit(OpF64Add) }
func (fb *FuncBody) F64Sub()    { fb.Emit(OpF64Sub) }
func (fb *FuncBody) F64Mul()    { fb.Emit(OpF64Mul) }
func (fb *FuncBody) F64Div()    { fb.Emit(OpF64Div) }
func (fb *FuncBody) F64Min()    { fb.Emit(OpF64Min) }
func (fb *FuncBody) F64Max()    { fb.Emit(OpF64Max) }
func (fb *FuncBody) F64Copysign() { fb.Emit(OpF64Copysign) }

// --- Conversions ---

func (fb *FuncBody) I32WrapI64()       { fb.Emit(OpI32WrapI64) }
func (fb *FuncBody) I64ExtendI32S()    { fb.Emit(OpI64ExtendI32S) }
func (fb *FuncBody) I64ExtendI32U()    { fb.Emit(OpI64ExtendI32U) }
func (fb *FuncBody) I32TruncF64S()     { fb.Emit(OpI32TruncF64S) }
func (fb *FuncBody) I32TruncF64U()     { fb.Emit(OpI32TruncF64U) }
func (fb *FuncBody) I64TruncF64S()     { fb.Emit(OpI64TruncF64S) }
func (fb *FuncBody) I64TruncF64U()     { fb.Emit(OpI64TruncF64U) }
func (fb *FuncBody) F64ConvertI32S()   { fb.Emit(OpF64ConvertI32S) }
func (fb *FuncBody) F64ConvertI32U()   { fb.Emit(OpF64ConvertI32U) }
func (fb *FuncBody) F64ConvertI64S()   { fb.Emit(OpF64ConvertI64S) }
func (fb *FuncBody) F64ConvertI64U()   { fb.Emit(OpF64ConvertI64U) }
func (fb *FuncBody) F32ConvertI32S()   { fb.Emit(OpF32ConvertI32S) }
func (fb *FuncBody) F32ConvertI32U()   { fb.Emit(OpF32ConvertI32U) }
func (fb *FuncBody) F32ConvertI64S()   { fb.Emit(OpF32ConvertI64S) }
func (fb *FuncBody) F32ConvertI64U()   { fb.Emit(OpF32ConvertI64U) }
func (fb *FuncBody) I32ReinterpretF32() { fb.Emit(OpI32ReinterpretF32) }
func (fb *FuncBody) I64ReinterpretF64() { fb.Emit(OpI64ReinterpretF64) }
func (fb *FuncBody) F32ReinterpretI32() { fb.Emit(OpF32ReinterpretI32) }
func (fb *FuncBody) F64ReinterpretI64() { fb.Emit(OpF64ReinterpretI64) }
func (fb *FuncBody) F64ConvertI32S_t()  { fb.Emit(OpF64ConvertI32S) }
func (fb *FuncBody) I32TruncSatF64S() {
        fb.Emit(OpI32TruncSatF64S)
        fb.EmitU32(0x00)
}
func (fb *FuncBody) I32TruncSatF64U() {
        fb.Emit(OpI32TruncSatF64U)
        fb.EmitU32(0x01)
}

// --- Sign extension ---

func (fb *FuncBody) I32Extend8S()  { fb.Emit(OpI32Extend8S) }
func (fb *FuncBody) I32Extend16S() { fb.Emit(OpI32Extend16S) }
func (fb *FuncBody) I64Extend8S()  { fb.Emit(OpI64Extend8S) }
func (fb *FuncBody) I64Extend16S() { fb.Emit(OpI64Extend16S) }
func (fb *FuncBody) I64Extend32S() { fb.Emit(OpI64Extend32S) }

// =============================================================================
// Memory layout
// =============================================================================

// Memory layout for WASM linear memory:
//
//      [0x0000 ... 0x00FF]     Reserved / runtime header
//      [0x0100 ... ]           Heap start (bump allocator)
//                               String data (inline in heap objects)
//                               Table objects (hash map internal data)
//                               Stack space for WASM execution

const (
        // MemoryPageSize is 64KB.
        MemoryPageSize = 65536

        // DefaultMemoryPages is the default number of initial memory pages.
        DefaultMemoryPages = 1

        // MaxMemoryPages is the maximum number of memory pages.
        MaxMemoryPages = 256

        // HeapStart is the byte offset where the heap begins.
        HeapStart = 256

        // StackPointerOffset is the offset of the stack pointer global in linear memory.
        StackPointerOffset = 0
)

// =============================================================================
// Variable information for code generation
// =============================================================================

// VarInfo describes a variable's location during compilation.
type VarInfo struct {
        LocalIdx uint32 // WASM local index
        Type     byte   // value type (TypeI64, TypeF64, etc.)
        IsFloat  bool   // whether this is a float variable
}

// =============================================================================
// Code Generator
// =============================================================================

// Compiler is the top-level WebAssembly code generator.
type Compiler struct {
        diag *diag.DiagnosticHandler

        // Module components
        imports    []*Import
        funcTypes  []*FuncType
        typeMap    map[string]uint32 // type key -> index
        funcs      []*FuncBody
        funcMap    map[string]uint32 // function name -> index
        globals    []*Global
        exports    []*Export

        // Compilation state
        scope      *WasmScope
        curFunc    *FuncBody
        curFuncIdx uint32
        tmpCount   int

        // Memory layout
        nextHeapOff  uint32 // next free heap offset
        stringSlots  map[string]uint32 // string -> heap offset

        // Import function indices (resolved at module build time)
        importFuncCount uint32
        runtimeFuncs    map[string]uint32 // runtime function name -> import index

        // Control flow tracking
        labelDepth int // current label nesting depth for break/continue
        loopStack  []loopInfo
}

type loopInfo struct {
        loopLabel    uint32 // label index of the loop
        continueLabel uint32 // label index for continue (outer block)
        breakLabel   uint32 // label index for break
}

// WasmScope holds variable bindings in the WASM local index space.
type WasmScope struct {
        parent   *WasmScope
        vars     map[string]*VarInfo
        nextFree uint32 // next free local index
}

func newWasmScope(parent *WasmScope, nextFree uint32) *WasmScope {
        return &WasmScope{
                parent:   parent,
                vars:     make(map[string]*VarInfo),
                nextFree: nextFree,
        }
}

func (s *WasmScope) Define(name string, info *VarInfo) {
        info.LocalIdx = s.nextFree
        s.vars[name] = info
        s.nextFree++
}

func (s *WasmScope) Lookup(name string) *VarInfo {
        if v, ok := s.vars[name]; ok {
                return v
        }
        if s.parent != nil {
                return s.parent.Lookup(name)
        }
        return nil
}

func (s *WasmScope) NextFree() uint32 {
        return s.nextFree
}

// NewCompiler creates a new WASM code generator.
func NewCompiler(dh *diag.DiagnosticHandler) *Compiler {
        c := &Compiler{
                diag:         dh,
                imports:      make([]*Import, 0),
                funcTypes:    make([]*FuncType, 0),
                typeMap:      make(map[string]uint32),
                funcs:        make([]*FuncBody, 0),
                funcMap:      make(map[string]uint32),
                globals:      make([]*Global, 0),
                exports:      make([]*Export, 0),
                stringSlots:  make(map[string]uint32),
                runtimeFuncs: make(map[string]uint32),
                nextHeapOff:  HeapStart,
                loopStack:    make([]loopInfo, 0),
        }

        // Register default function types
        c.addFuncType(TypeVoid)
        c.addFuncType(TypeI32Void)
        c.addFuncType(TypeI64Void)
        c.addFuncType(TypeI64I64)
        c.addFuncType(FuncI64)
        c.addFuncType(FuncF64)
        c.addFuncType(TypeI64I64I64)

        // Register runtime imports
        c.addRuntimeImport("yilt_alloc", &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI32}})
        c.addRuntimeImport("yilt_table_new", &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI64}})
        c.addRuntimeImport("yilt_table_get", &FuncType{Params: []byte{TypeI64, TypeI64}, Results: []byte{TypeI64}})
        c.addRuntimeImport("yilt_table_set", &FuncType{Params: []byte{TypeI64, TypeI64, TypeI64}, Results: nil})
        c.addRuntimeImport("yilt_table_len", &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI64}})
        c.addRuntimeImport("runtime_iter_new", &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI64}})
        c.addRuntimeImport("runtime_iter_next", &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI64}})
        c.addRuntimeImport("runtime_iter_get_key", &FuncType{Params: nil, Results: []byte{TypeI64}})
        c.addRuntimeImport("runtime_iter_get_val", &FuncType{Params: nil, Results: []byte{TypeI64}})
        c.addRuntimeImport("runtime_iter_get_next", &FuncType{Params: nil, Results: []byte{TypeI64}})
        c.addRuntimeImport("yilt_spawn", &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI64}})
        c.addRuntimeImport("yilt_await", &FuncType{Params: []byte{TypeI64}, Results: []byte{TypeI64}})
        c.addRuntimeImport("yilt_print_i64", &FuncType{Params: []byte{TypeI64}, Results: nil})
        c.addRuntimeImport("yilt_print_f64", &FuncType{Params: []byte{TypeF64}, Results: nil})
        c.addRuntimeImport("yilt_error", &FuncType{Params: []byte{TypeI32}, Results: nil})

        // Import memory
        c.imports = append(c.imports, &Import{
                Module: "env",
                Name:   "memory",
                Kind:   ImportMemory,
                Desc:   &MemoryDesc{Initial: DefaultMemoryPages},
        })

        return c
}

// addRuntimeImport adds a runtime function import and returns its index.
func (c *Compiler) addRuntimeImport(name string, ft *FuncType) uint32 {
        _ = c.addFuncType(ft)
        idx := uint32(c.importFuncCount)
        c.imports = append(c.imports, &Import{
                Module: "yilt",
                Name:   name,
                Kind:   ImportFunc,
                Desc:   ft,
        })
        c.runtimeFuncs[name] = idx
        c.importFuncCount++
        return idx
}

// addFuncType registers a function type and returns its index.
func (c *Compiler) addFuncType(ft *FuncType) uint32 {
        key := ft.Key()
        if idx, ok := c.typeMap[key]; ok {
                return idx
        }
        idx := uint32(len(c.funcTypes))
        c.funcTypes = append(c.funcTypes, ft)
        c.typeMap[key] = idx
        return idx
}

// runtimeCallIdx returns the import index for a runtime function.
func (c *Compiler) runtimeCallIdx(name string) uint32 {
        if idx, ok := c.runtimeFuncs[name]; ok {
                return idx
        }
        return 0
}

// funcIdx returns the function index (accounting for imports) for a user-defined function.
func (c *Compiler) funcIdx(name string) uint32 {
        if idx, ok := c.funcMap[name]; ok {
                return c.importFuncCount + idx
        }
        return 0
}

func (c *Compiler) nextTemp(prefix string) string {
        c.tmpCount++
        return fmt.Sprintf("%s_%d", prefix, c.tmpCount)
}

// =============================================================================
// Expression compilation
// =============================================================================
// All expression compilation functions push their result onto the WASM stack.
// The "return type" of expressions is encoded in the VarInfo.Type field.

func (c *Compiler) compileExpr(fb *FuncBody, expr ast.Expr, scope *WasmScope) byte {
        switch e := expr.(type) {
        case *ast.IntLit:
                return c.compileIntLit(fb, e)
        case *ast.FloatLit:
                return c.compileFloatLit(fb, e)
        case *ast.StringLit:
                return c.compileStringLit(fb, e)
        case *ast.BoolLit:
                return c.compileBoolLit(fb, e)
        case *ast.NilLit:
                return c.compileNilLit(fb, e)
        case *ast.Ident:
                return c.compileIdent(fb, e, scope)
        case *ast.BinOp:
                return c.compileBinOp(fb, e, scope)
        case *ast.UnaryOp:
                return c.compileUnaryOp(fb, e, scope)
        case *ast.CallExpr:
                return c.compileCallExpr(fb, e, scope)
        case *ast.IndexExpr:
                return c.compileIndexExpr(fb, e, scope)
        case *ast.MemberExpr:
                return c.compileMemberExpr(fb, e, scope)
        case *ast.TableLit:
                return c.compileTableLit(fb, e, scope)
        case *ast.AssignExpr:
                return c.compileAssignExpr(fb, e, scope)
        case *ast.IndexAssignExpr:
                return c.compileIndexAssignExpr(fb, e, scope)
        case *ast.MemberAssignExpr:
                // TODO: implement member assignment for WASM
                c.compileExpr(fb, e.Obj, scope)
                c.compileExpr(fb, e.Value, scope)
                return 0
        case *ast.SpawnExpr:
                return c.compileSpawnExpr(fb, e, scope)
        case *ast.AwaitExpr:
                return c.compileAwaitExpr(fb, e, scope)
        default:
                c.diag.Error("", 0, 0, 0, fmt.Sprintf("wasm: unhandled expression type %T", expr))
                fb.I64Const(0)
                return TypeI64
        }
}

func (c *Compiler) compileIntLit(fb *FuncBody, e *ast.IntLit) byte {
        fb.I64Const(e.Value)
        return TypeI64
}

func (c *Compiler) compileFloatLit(fb *FuncBody, e *ast.FloatLit) byte {
        fb.EmitF64(e.Value)
        return TypeF64
}

func (c *Compiler) compileStringLit(fb *FuncBody, e *ast.StringLit) byte {
        // Strings are stored as pointers into linear memory.
        // We allocate the string data and return the offset as i64.
        if off, ok := c.stringSlots[e.Value]; ok {
                fb.I64Const(int64(off))
                return TypeI64
        }

        off := c.nextHeapOff
        data := []byte(e.Value)
        // Store length prefix (i32) followed by bytes + null terminator
        c.nextHeapOff += uint32(4 + len(data) + 1)
        c.nextHeapOff = (c.nextHeapOff + 7) &^ 7 // align to 8

        c.stringSlots[e.Value] = off

        // The string data will be written to the data section.
        // For now, emit the offset.
        fb.I64Const(int64(off))
        return TypeI64
}

func (c *Compiler) compileBoolLit(fb *FuncBody, e *ast.BoolLit) byte {
        if e.Value {
                fb.I64Const(1)
        } else {
                fb.I64Const(0)
        }
        return TypeI64
}

func (c *Compiler) compileNilLit(fb *FuncBody, e *ast.NilLit) byte {
        _ = fb
        _ = e
        fb.I64Const(0)
        return TypeI64
}

func (c *Compiler) compileIdent(fb *FuncBody, e *ast.Ident, scope *WasmScope) byte {
        info := scope.Lookup(e.Name)
        if info == nil {
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("undefined variable: %s", e.Name))
                fb.I64Const(0)
                return TypeI64
        }
        fb.LocalGet(info.LocalIdx)
        return info.Type
}

func (c *Compiler) compileBinOp(fb *FuncBody, e *ast.BinOp, scope *WasmScope) byte {
        // Short-circuit for logical operators
        switch e.Op {
        case ast.TAnd:
                return c.compileLogicalAnd(fb, e, scope)
        case ast.TOr:
                return c.compileLogicalOr(fb, e, scope)
        }

        leftType := c.compileExpr(fb, e.Left, scope)
        rightType := c.compileExpr(fb, e.Right, scope)

        // Handle float operations
        if leftType == TypeF64 || rightType == TypeF64 {
                return c.compileFloatBinOp(fb, e.Op, leftType, rightType)
        }

        // Integer operations (i64)
        switch e.Op {
        case ast.TPlus:
                fb.I64Add()
        case ast.TMinus:
                fb.I64Sub()
        case ast.TStar:
                fb.I64Mul()
        case ast.TSlash:
                fb.I64DivS()
        case ast.TPercent:
                fb.I64RemS()
        case ast.TAmp:
                fb.I64And()
        case ast.TPipe:
                fb.I64Or()
        case ast.TCaret:
                fb.I64Xor()
        case ast.TLShift:
                fb.I64Shl()
        case ast.TRShift:
                fb.I64ShrS()
        case ast.TEq:
                fb.I64Eq()
                // Convert i32 result to i64
                fb.I64ExtendI32S()
        case ast.TNeq:
                fb.I64Ne()
                fb.I64ExtendI32S()
        case ast.TLt:
                fb.I64LtS()
                fb.I64ExtendI32S()
        case ast.TLe:
                fb.I64LeS()
                fb.I64ExtendI32S()
        case ast.TGt:
                fb.I64GtS()
                fb.I64ExtendI32S()
        case ast.TGe:
                fb.I64GeS()
                fb.I64ExtendI32S()
        default:
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("unhandled binary operator: %s", e.Op))
                fb.Drop()
                fb.I64Const(0)
        }
        return TypeI64
}

func (c *Compiler) compileFloatBinOp(fb *FuncBody, op ast.Token, leftType, rightType byte) byte {
        // If either operand is i64, convert it to f64
        if leftType == TypeI64 {
                fb.I64TruncF64S() // Wait, we need i64->f64 not f64->i64
                // Actually: f64.convert_i64_s converts i64 on stack to f64
                // We need to emit the right instruction
        }
        if rightType == TypeI64 {
                fb.F64ConvertI64S()
        }

        switch op {
        case ast.TPlus:
                fb.F64Add()
        case ast.TMinus:
                fb.F64Sub()
        case ast.TStar:
                fb.F64Mul()
        case ast.TSlash:
                fb.F64Div()
        case ast.TEq:
                fb.F64Eq()
        case ast.TNeq:
                fb.F64Ne()
        case ast.TLt:
                fb.F64Lt()
        case ast.TLe:
                fb.F64Le()
        case ast.TGt:
                fb.F64Gt()
        case ast.TGe:
                fb.F64Ge()
        default:
                c.diag.Error("", 0, 0, 0, fmt.Sprintf("wasm: unhandled float binary operator: %s", op))
                fb.Drop()
                fb.EmitF64(0.0)
        }
        return TypeF64
}

func (c *Compiler) compileLogicalAnd(fb *FuncBody, e *ast.BinOp, scope *WasmScope) byte {
        leftType := c.compileExpr(fb, e.Left, scope)
        if leftType == TypeF64 {
                fb.F64Eq() // convert to i32 (0 or 1)
        }
        fb.I64Eqz() // convert to i32: 0 if truthy, 1 if falsy
        // if truthy, continue evaluating right side
        fb.If(TypeI64)
        rightType := c.compileExpr(fb, e.Right, scope)
        if rightType == TypeF64 {
                fb.Drop()
                fb.I64Const(0)
        }
        fb.Else()
        fb.I64Const(0)
        fb.End()
        return TypeI64
}

func (c *Compiler) compileLogicalOr(fb *FuncBody, e *ast.BinOp, scope *WasmScope) byte {
        leftType := c.compileExpr(fb, e.Left, scope)
        if leftType == TypeF64 {
                fb.F64Eq()
        }
        fb.I64Eqz() // 0 if truthy, 1 if falsy
        fb.If(TypeI64)
        fb.I64Const(1) // left was truthy
        fb.Else()
        rightType := c.compileExpr(fb, e.Right, scope)
        if rightType == TypeF64 {
                fb.Drop()
                fb.I64Const(0)
        }
        fb.Else()
        fb.End()
        return TypeI64
}

func (c *Compiler) compileUnaryOp(fb *FuncBody, e *ast.UnaryOp, scope *WasmScope) byte {
        operandType := c.compileExpr(fb, e.Operand, scope)

        switch e.Op {
        case ast.TMinus:
                if operandType == TypeF64 {
                        fb.F64Neg()
                        return TypeF64
                }
                fb.I64Const(0)
                fb.I64Sub()
                return TypeI64
        case ast.TNot:
                if operandType == TypeF64 {
                        // Logical not on float: !(val == val) for NaN, else !val
                        fb.F64Eq()
                        fb.I32Eqz()
                        fb.I64ExtendI32S()
                        return TypeI64
                }
                fb.I64Eqz()
                fb.I64ExtendI32S()
                return TypeI64
        case ast.TTilde:
                fb.I64Const(-1)
                fb.I64Xor()
                return TypeI64
        default:
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("unhandled unary operator: %s", e.Op))
                return operandType
        }
}

func (c *Compiler) compileCallExpr(fb *FuncBody, e *ast.CallExpr, scope *WasmScope) byte {
        // Compile arguments
        for _, arg := range e.Args {
                c.compileExpr(fb, arg, scope)
        }

        // Determine the callee
        if ident, ok := e.Func.(*ast.Ident); ok {
                if idx, ok := c.funcMap[ident.Name]; ok {
                        // User-defined function
                        fb.Call(c.importFuncCount + idx)
                        // Return type: check the function type
                        if c.curFuncIdx < uint32(len(c.funcs)) {
                                // Look up the function's return type
                                fi := c.funcs[idx]
                                if fi != nil && len(fi.Locals) > 0 {
                                        return TypeI64 // default
                                }
                        }
                        return TypeI64
                }
                if idx, ok := c.runtimeFuncs[ident.Name]; ok {
                        fb.Call(idx)
                        return TypeI64
                }
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("undefined function: %s", ident.Name))
                fb.I64Const(0)
                return TypeI64
        }

        // Indirect call (member access, etc.)
        _ = c.compileExpr(fb, e.Func, scope)
        fb.CallIndirect(0, 0) // TODO: proper type index
        return TypeI64
}

func (c *Compiler) compileIndexExpr(fb *FuncBody, e *ast.IndexExpr, scope *WasmScope) byte {
        c.compileExpr(fb, e.Obj, scope)
        c.compileExpr(fb, e.Key, scope)
        fb.Call(c.runtimeCallIdx("yilt_table_get"))
        return TypeI64
}

func (c *Compiler) compileMemberExpr(fb *FuncBody, e *ast.MemberExpr, scope *WasmScope) byte {
        info := scope.Lookup(e.Field)
        if info != nil {
                fb.LocalGet(info.LocalIdx)
                return info.Type
        }
        fb.I64Const(0)
        c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                fmt.Sprintf("undefined member: %s", e.Field))
        return TypeI64
}

func (c *Compiler) compileTableLit(fb *FuncBody, e *ast.TableLit, scope *WasmScope) byte {
        fb.I64Const(int64(len(e.Entries)))
        fb.Call(c.runtimeCallIdx("yilt_table_new"))

        // Store the table pointer in a temporary local for the set calls
        tableLocalIdx := scope.NextFree()
        fb.AddLocals(1, TypeI64)
        fb.LocalTee(tableLocalIdx)

        for _, entry := range e.Entries {
                fb.LocalGet(tableLocalIdx)
                c.compileExpr(fb, entry.Key, scope)
                c.compileExpr(fb, entry.Value, scope)
                fb.Call(c.runtimeCallIdx("yilt_table_set"))
        }

        fb.LocalGet(tableLocalIdx)
        return TypeI64
}

func (c *Compiler) compileAssignExpr(fb *FuncBody, e *ast.AssignExpr, scope *WasmScope) byte {
        valType := c.compileExpr(fb, e.Value, scope)

        if ident, ok := e.Target.(*ast.Ident); ok {
                info := scope.Lookup(ident.Name)
                if info != nil {
                        if info.Type == TypeI64 && valType == TypeF64 {
                                // Convert f64 to i64 (truncation)
                                fb.I64TruncF64S()
                        } else if info.Type == TypeF64 && valType == TypeI64 {
                                // Convert i64 to f64
                                fb.F64ConvertI64S()
                        }
                        fb.LocalSet(info.LocalIdx)
                        fb.LocalGet(info.LocalIdx)
                        return info.Type
                }
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        fmt.Sprintf("undefined variable: %s", ident.Name))
        } else {
                c.diag.Error("", e.Pos().Line, e.Pos().Col, e.Pos().Offset,
                        "invalid assignment target")
        }
        return valType
}

func (c *Compiler) compileIndexAssignExpr(fb *FuncBody, e *ast.IndexAssignExpr, scope *WasmScope) byte {
        c.compileExpr(fb, e.Obj, scope)
        c.compileExpr(fb, e.Key, scope)
        c.compileExpr(fb, e.Value, scope)
        fb.Call(c.runtimeCallIdx("yilt_table_set"))
        return TypeI64
}

func (c *Compiler) compileSpawnExpr(fb *FuncBody, e *ast.SpawnExpr, scope *WasmScope) byte {
        if e.Call == nil {
                fb.I64Const(0)
                return TypeI64
        }

        // Compile the call expression to get the function reference
        c.compileExpr(fb, e.Call.Func, scope)
        for _, arg := range e.Call.Args {
                c.compileExpr(fb, arg, scope)
        }
        // Call the spawn runtime function
        fb.Call(c.runtimeCallIdx("yilt_spawn"))
        return TypeI64
}

func (c *Compiler) compileAwaitExpr(fb *FuncBody, e *ast.AwaitExpr, scope *WasmScope) byte {
        c.compileExpr(fb, e.Handle, scope)
        fb.Call(c.runtimeCallIdx("yilt_await"))
        return TypeI64
}

// =============================================================================
// Statement compilation
// =============================================================================

func (c *Compiler) compileStmt(fb *FuncBody, stmt ast.Stmt, scope *WasmScope) {
        switch s := stmt.(type) {
        case *ast.LetStmt:
                c.compileLetStmt(fb, s, scope)
        case *ast.ExprStmt:
                typ := c.compileExpr(fb, s.Expr, scope)
                if typ != TypeEmptyBlock {
                        fb.Drop()
                }
        case *ast.ReturnStmt:
                c.compileReturnStmt(fb, s, scope)
        case *ast.IfStmt:
                c.compileIfStmt(fb, s, scope)
        case *ast.WhileStmt:
                c.compileWhileStmt(fb, s, scope)
        case *ast.ForStmt:
                c.compileForStmt(fb, s, scope)
        case *ast.MatchStmt:
                c.compileMatchStmt(fb, s, scope)
        case *ast.BreakStmt:
                c.compileBreakStmt(fb, s)
        case *ast.ContinueStmt:
                c.compileContinueStmt(fb, s)
        default:
                c.diag.Error("", 0, 0, 0, fmt.Sprintf("wasm: unhandled statement type %T", stmt))
        }
}

func (c *Compiler) compileLetStmt(fb *FuncBody, s *ast.LetStmt, scope *WasmScope) {
        valType := c.compileExpr(fb, s.Value, scope)

        // Determine the WASM local type
        wasmType := byte(TypeI64)
        if valType == TypeF64 {
                wasmType = byte(TypeF64)
        }

        info := &VarInfo{Type: wasmType, IsFloat: valType == TypeF64}
        scope.Define(s.Name, info)
        fb.AddLocals(1, wasmType)
        fb.LocalSet(info.LocalIdx)
}

func (c *Compiler) compileReturnStmt(fb *FuncBody, s *ast.ReturnStmt, scope *WasmScope) {
        if s.Value != nil {
                c.compileExpr(fb, s.Value, scope)
        }
        fb.Return()
}

func (c *Compiler) compileIfStmt(fb *FuncBody, s *ast.IfStmt, scope *WasmScope) {
        for i, branch := range s.Branches {
                condType := c.compileExpr(fb, branch.Cond, scope)
                if condType == TypeF64 {
                        fb.F64Eq() // produces i32: 1 if NaN or not equal to self
                        // Actually for truthiness: we need 0 for falsy, non-zero for truthy
                        // f64.eq converts any f64 to i32 (0 or 1). We want non-zero = truthy.
                        // So we need: result = (f64 != 0.0)
                }
                fb.If(TypeEmptyBlock)

                childScope := newWasmScope(scope, scope.NextFree())
                for _, stmt := range branch.Body {
                        c.compileStmt(fb, stmt, childScope)
                }

                if i < len(s.Branches)-1 || len(s.Else) > 0 {
                        fb.Else()
                }
        }

        if len(s.Else) > 0 {
                childScope := newWasmScope(scope, scope.NextFree())
                for _, stmt := range s.Else {
                        c.compileStmt(fb, stmt, childScope)
                }
        }

        fb.End()
}

func (c *Compiler) compileWhileStmt(fb *FuncBody, s *ast.WhileStmt, scope *WasmScope) {
        // WASM while loop:
        //   block $break
        //     loop $continue
        //       ;; condition
        //       br_if $break (exit if false)
        //       ;; body
        //       br $continue (back to top)
        //     end
        //   end

        c.labelDepth += 2

        breakDepth := uint32(c.labelDepth - 2)
        continueDepth := uint32(c.labelDepth - 1)

        ctx := loopInfo{
                loopLabel:    continueDepth,
                continueLabel: continueDepth,
                breakLabel:   breakDepth,
        }
        c.loopStack = append(c.loopStack, ctx)

        fb.Block(TypeEmptyBlock)
        fb.Loop(TypeEmptyBlock)

        // Condition
        condType := c.compileExpr(fb, s.Cond, scope)
        if condType == TypeF64 {
                fb.F64Eq() // convert to i32
        }
        fb.I64Eqz() // 0 if truthy, 1 if falsy
        fb.BrIf(breakDepth) // exit if falsy

        // Body
        childScope := newWasmScope(scope, scope.NextFree())
        for _, stmt := range s.Body {
                c.compileStmt(fb, stmt, childScope)
        }

        // Continue (jump back to loop start)
        fb.Br(continueDepth)

        fb.End() // end loop
        fb.End() // end block

        c.loopStack = c.loopStack[:len(c.loopStack)-1]
        c.labelDepth -= 2
}

func (c *Compiler) compileForStmt(fb *FuncBody, s *ast.ForStmt, scope *WasmScope) {
        c.labelDepth += 2
        breakDepth := uint32(c.labelDepth - 2)
        continueDepth := uint32(c.labelDepth - 1)

        ctx := loopInfo{
                loopLabel:    continueDepth,
                continueLabel: continueDepth,
                breakLabel:   breakDepth,
        }
        c.loopStack = append(c.loopStack, ctx)

        // Create iterator: runtime_iter_new(table) -> iter_index
        c.compileExpr(fb, s.Over, scope)
        fb.Call(c.runtimeCallIdx("runtime_iter_new"))

        iterLocalIdx := scope.NextFree()
        fb.AddLocals(1, TypeI64)
        fb.LocalTee(iterLocalIdx)

        fb.Block(TypeEmptyBlock)
        fb.Loop(TypeEmptyBlock)

        // Advance iterator: runtime_iter_next(iter) -> has_more (i64, 0 or 1)
        fb.LocalGet(iterLocalIdx)
        fb.Call(c.runtimeCallIdx("runtime_iter_next"))
        fb.I64Eqz() // 0 if more (non-zero return), 1 if done (zero return)
        fb.BrIf(breakDepth) // exit if done

        // Get the advanced iterator index for the next iteration
        fb.Call(c.runtimeCallIdx("runtime_iter_get_next"))
        fb.LocalSet(iterLocalIdx)

        // Get key and value from the iterator result
        keyLocalIdx := scope.NextFree()
        valLocalIdx := scope.NextFree()
        fb.AddLocals(1, TypeI64)
        fb.AddLocals(1, TypeI64)

        fb.Call(c.runtimeCallIdx("runtime_iter_get_key"))
        fb.LocalSet(keyLocalIdx)
        fb.Call(c.runtimeCallIdx("runtime_iter_get_val"))
        fb.LocalSet(valLocalIdx)

        // Bind key and value in child scope
        childScope := newWasmScope(scope, scope.NextFree())
        if s.Key != "" {
                childScope.Define(s.Key, &VarInfo{LocalIdx: keyLocalIdx, Type: TypeI64})
        }
        if s.Value != "" {
                childScope.Define(s.Value, &VarInfo{LocalIdx: valLocalIdx, Type: TypeI64})
        }

        for _, stmt := range s.Body {
                c.compileStmt(fb, stmt, childScope)
        }

        // Continue
        fb.Br(continueDepth)
        fb.End() // loop
        fb.End() // block

        c.loopStack = c.loopStack[:len(c.loopStack)-1]
        c.labelDepth -= 2
}

func (c *Compiler) compileMatchStmt(fb *FuncBody, s *ast.MatchStmt, scope *WasmScope) {
        // Compile match as a series of if/else chains
        c.compileExpr(fb, s.Subject, scope)

        subjectLocal := scope.NextFree()
        fb.AddLocals(1, TypeI64)
        fb.LocalTee(subjectLocal)

        for _, mc := range s.Cases {
                fb.LocalGet(subjectLocal)
                c.compileExpr(fb, mc.Value, scope)
                fb.I64Ne() // if not equal, skip
                fb.If(TypeEmptyBlock)

                childScope := newWasmScope(scope, scope.NextFree())
                for _, stmt := range mc.Body {
                        c.compileStmt(fb, stmt, childScope)
                }

                if len(s.Default) > 0 || mc.Span != s.Cases[len(s.Cases)-1].Span {
                        fb.Else()
                }
        }

        // Default case
        if len(s.Default) > 0 {
                childScope := newWasmScope(scope, scope.NextFree())
                for _, stmt := range s.Default {
                        c.compileStmt(fb, stmt, childScope)
                }
        }

        // Close all open if/else blocks
        for range s.Cases {
                fb.End()
        }
}

func (c *Compiler) compileBreakStmt(fb *FuncBody, s *ast.BreakStmt) {
        if len(c.loopStack) == 0 {
                c.diag.Error("", s.Pos().Line, s.Pos().Col, s.Pos().Offset, "break outside of loop")
                return
        }
        ctx := c.loopStack[len(c.loopStack)-1]
        fb.Br(ctx.breakLabel)
}

func (c *Compiler) compileContinueStmt(fb *FuncBody, s *ast.ContinueStmt) {
        if len(c.loopStack) == 0 {
                c.diag.Error("", s.Pos().Line, s.Pos().Col, s.Pos().Offset, "continue outside of loop")
                return
        }
        ctx := c.loopStack[len(c.loopStack)-1]
        fb.Br(ctx.continueLabel)
}

// =============================================================================
// Function compilation
// =============================================================================

func (c *Compiler) compileFunction(fn *ast.FnDecl) {
        fb := NewFuncBody()

        // Parameters are mapped to locals 0..N
        paramScope := newWasmScope(nil, 0)
        for _, param := range fn.Params {
                var typ byte = TypeI64
                switch param.Type.Kind {
                case ast.KindFp:
                        typ = TypeF64
                case ast.KindInt, ast.KindUint, ast.KindBool, ast.KindStr, ast.KindTable:
                        typ = TypeI64
                default:
                        typ = TypeI64
                }
                paramScope.Define(param.Name, &VarInfo{Type: typ, LocalIdx: paramScope.NextFree(), IsFloat: typ == TypeF64})
                fb.AddLocals(1, typ)
        }

        // Compile body
        for _, stmt := range fn.Body {
                c.compileStmt(fb, stmt, paramScope)
        }

        idx := uint32(len(c.funcs))
        c.funcs = append(c.funcs, fb)
        c.funcMap[fn.Name] = idx
}

// =============================================================================
// Top-level compilation
// =============================================================================

// Compile compiles a full Yilt program into a WebAssembly binary module.
func (c *Compiler) Compile(program *ast.Program) ([]byte, error) {
        if c.diag.HasErrors() {
                return nil, fmt.Errorf("cannot compile with %d errors", c.diag.ErrorCount())
        }

        // Collect function declarations
        funcDecls := make([]*ast.FnDecl, 0)
        for _, file := range program.Files {
                for _, decl := range file.Decls {
                        if fn, ok := decl.(*ast.FnDecl); ok {
                                if !fn.Extern {
                                        funcDecls = append(funcDecls, fn)
                                }
                        }
                }
        }

        // Compile each function
        for _, fn := range funcDecls {
                c.compileFunction(fn)
        }

        // Build the WASM binary module
        return c.buildModule()
}

// buildModule assembles the complete WASM binary module.
func (c *Compiler) buildModule() ([]byte, error) {
        w := NewWasmWriter()

        // Magic number and version
        w.WriteMagic()

        // === Type section ===
        if len(c.funcTypes) > 0 {
                patch := w.WriteSectionHeader(SecType)
                w.WriteU32(uint32(len(c.funcTypes)))
                for _, ft := range c.funcTypes {
                        ft.Encode(w)
                }
                patch()
        }

        // === Import section ===
        if len(c.imports) > 0 {
                patch := w.WriteSectionHeader(SecImport)
                w.WriteU32(uint32(len(c.imports)))
                for _, imp := range c.imports {
                        imp.Encode(w)
                }
                patch()
        }

        // === Function section ===
        if len(c.funcs) > 0 {
                patch := w.WriteSectionHeader(SecFunction)
                w.WriteU32(uint32(len(c.funcs)))
                for range c.funcs {
                        // Each function references a type index
                        // We use type 0 (void->void) as default for simplicity
                        // A production compiler would resolve the actual type
                        w.WriteU32(0)
                }
                patch()
        }

        // === Export section ===
        // Export main function and any public functions
        var exportsToWrite []*Export
        for name, idx := range c.funcMap {
                if name == "main" {
                        exportsToWrite = append(exportsToWrite, &Export{
                                Name:  "main",
                                Kind:  ExportFunc,
                                Index: c.importFuncCount + idx,
                        })
                }
        }
        // Also export _start for WASI-like environments
        if _, ok := c.funcMap["main"]; ok {
                exportsToWrite = append(exportsToWrite, &Export{
                        Name:  "_start",
                        Kind:  ExportFunc,
                        Index: c.importFuncCount + c.funcMap["main"],
                })
        }

        // Export memory
        exportsToWrite = append(exportsToWrite, &Export{
                Name:  "memory",
                Kind:  ExportMemory,
                Index: 0,
        })

        if len(exportsToWrite) > 0 {
                patch := w.WriteSectionHeader(SecExport)
                w.WriteU32(uint32(len(exportsToWrite)))
                for _, exp := range exportsToWrite {
                        exp.Encode(w)
                }
                patch()
        }

        // === Code section ===
        if len(c.funcs) > 0 {
                patch := w.WriteSectionHeader(SecCode)
                w.WriteU32(uint32(len(c.funcs)))
                for _, fn := range c.funcs {
                        fn.Encode(w)
                }
                patch()
        }

        // === Data section ===
        // Write string data to linear memory
        if len(c.stringSlots) > 0 {
                patch := w.WriteSectionHeader(SecData)
                w.WriteU32(uint32(len(c.stringSlots)))
                for str, off := range c.stringSlots {
                        w.WriteU32(0) // memory index
                        // Active segment with offset
                        w.WriteByte(OpI32Const)
                        w.WriteI32(int32(off))
                        w.WriteByte(OpEnd)
                        data := []byte(str)
                        w.WriteU32(uint32(4 + len(data) + 1))
                        // Write length prefix
                        var lenBuf [4]byte
                        binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(data)))
                        w.WriteBytes(lenBuf[:])
                        w.WriteBytes(data)
                        w.WriteByte(0) // null terminator
                }
                patch()
        }

        return w.Bytes(), nil
}

// =============================================================================
// Output
// =============================================================================

// Output represents the compiled WASM module output.
type Output struct {
        Bytes    []byte
        Functions int
        Imports   int
}

// =============================================================================
// Utility functions
// =============================================================================

// DecodeLEBU32 decodes a LEB128-encoded uint32 from a byte slice.
// Returns the value and the number of bytes consumed.
func DecodeLEBU32(data []byte) (uint32, int) {
        var result uint32
        var shift uint32
        for i := 0; i < len(data); i++ {
                b := data[i]
                result |= uint32(b&0x7F) << shift
                if b&0x80 == 0 {
                        return result, i + 1
                }
                shift += 7
                if shift >= 35 {
                        return 0, i + 1
                }
        }
        return result, len(data)
}

// DecodeLEBI32 decodes a LEB128-encoded int32 from a byte slice.
func DecodeLEBI32(data []byte) (int32, int) {
        var result int32
        var shift uint32
        var b byte
        for i := 0; i < len(data); i++ {
                b = data[i]
                result |= int32(b&0x7F) << shift
                shift += 7
                if b&0x80 == 0 {
                        if shift < 32 && (b&0x40) != 0 {
                                result |= ^int32(0) << shift
                        }
                        return result, i + 1
                }
        }
        return result, len(data)
}

// DecodeLEBI64 decodes a LEB128-encoded int64 from a byte slice.
func DecodeLEBI64(data []byte) (int64, int) {
        var result int64
        var shift uint32
        var b byte
        for i := 0; i < len(data); i++ {
                b = data[i]
                result |= int64(b&0x7F) << shift
                shift += 7
                if b&0x80 == 0 {
                        if shift < 64 && (b&0x40) != 0 {
                                result |= ^int64(0) << shift
                        }
                        return result, i + 1
                }
        }
        return result, len(data)
}

// OpcodeName returns the mnemonic for a WASM opcode.
func OpcodeName(op byte) string {
        switch op {
        case OpUnreachable: return "unreachable"
        case OpNop: return "nop"
        case OpBlock: return "block"
        case OpLoop: return "loop"
        case OpIf: return "if"
        case OpElse: return "else"
        case OpEnd: return "end"
        case OpBr: return "br"
        case OpBrIf: return "br_if"
        case OpBrTable: return "br_table"
        case OpReturn: return "return"
        case OpCall: return "call"
        case OpCallIndirect: return "call_indirect"
        case OpDrop: return "drop"
        case OpSelect: return "select"
        case OpLocalGet: return "local.get"
        case OpLocalSet: return "local.set"
        case OpLocalTee: return "local.tee"
        case OpGlobalGet: return "global.get"
        case OpGlobalSet: return "global.set"
        case OpI32Load: return "i32.load"
        case OpI64Load: return "i64.load"
        case OpF64Load: return "f64.load"
        case OpI32Store: return "i32.store"
        case OpI64Store: return "i64.store"
        case OpF64Store: return "f64.store"
        case OpMemorySize: return "memory.size"
        case OpMemoryGrow: return "memory.grow"
        case OpI32Const: return "i32.const"
        case OpI64Const: return "i64.const"
        case OpF64Const: return "f64.const"
        case OpI32Eqz: return "i32.eqz"
        case OpI32Eq: return "i32.eq"
        case OpI32Ne: return "i32.ne"
        case OpI32LtS: return "i32.lt_s"
        case OpI32LtU: return "i32.lt_u"
        case OpI32GtS: return "i32.gt_s"
        case OpI32GtU: return "i32.gt_u"
        case OpI32LeS: return "i32.le_s"
        case OpI32LeU: return "i32.le_u"
        case OpI32GeS: return "i32.ge_s"
        case OpI32GeU: return "i32.ge_u"
        case OpI64Eqz: return "i64.eqz"
        case OpI64Eq: return "i64.eq"
        case OpI64Ne: return "i64.ne"
        case OpI64LtS: return "i64.lt_s"
        case OpI64LtU: return "i64.lt_u"
        case OpI64GtS: return "i64.gt_s"
        case OpI64GtU: return "i64.gt_u"
        case OpI64LeS: return "i64.le_s"
        case OpI64LeU: return "i64.le_u"
        case OpI64GeS: return "i64.ge_s"
        case OpI64GeU: return "i64.ge_u"
        case OpF64Eq: return "f64.eq"
        case OpF64Ne: return "f64.ne"
        case OpF64Lt: return "f64.lt"
        case OpF64Gt: return "f64.gt"
        case OpF64Le: return "f64.le"
        case OpF64Ge: return "f64.ge"
        case OpI32Add: return "i32.add"
        case OpI32Sub: return "i32.sub"
        case OpI32Mul: return "i32.mul"
        case OpI32DivS: return "i32.div_s"
        case OpI32DivU: return "i32.div_u"
        case OpI32RemS: return "i32.rem_s"
        case OpI32RemU: return "i32.rem_u"
        case OpI32And: return "i32.and"
        case OpI32Or: return "i32.or"
        case OpI32Xor: return "i32.xor"
        case OpI32Shl: return "i32.shl"
        case OpI32ShrS: return "i32.shr_s"
        case OpI32ShrU: return "i32.shr_u"
        case OpI64Add: return "i64.add"
        case OpI64Sub: return "i64.sub"
        case OpI64Mul: return "i64.mul"
        case OpI64DivS: return "i64.div_s"
        case OpI64DivU: return "i64.div_u"
        case OpI64RemS: return "i64.rem_s"
        case OpI64RemU: return "i64.rem_u"
        case OpI64And: return "i64.and"
        case OpI64Or: return "i64.or"
        case OpI64Xor: return "i64.xor"
        case OpI64Shl: return "i64.shl"
        case OpI64ShrS: return "i64.shr_s"
        case OpI64ShrU: return "i64.shr_u"
        case OpF64Add: return "f64.add"
        case OpF64Sub: return "f64.sub"
        case OpF64Mul: return "f64.mul"
        case OpF64Div: return "f64.div"
        case OpF64Neg: return "f64.neg"
        case OpF64Abs: return "f64.abs"
        case OpF64Sqrt: return "f64.sqrt"
        case OpF64Ceil: return "f64.ceil"
        case OpF64Floor: return "f64.floor"
        case OpF64Trunc: return "f64.trunc"
        case OpF64Min: return "f64.min"
        case OpF64Max: return "f64.max"
        case OpI32WrapI64: return "i32.wrap_i64"
        case OpI64ExtendI32S: return "i64.extend_i32_s"
        case OpI64ExtendI32U: return "i64.extend_i32_u"
        case OpI64TruncF64S: return "i64.trunc_f64_s"
        case OpI64TruncF64U: return "i64.trunc_f64_u"
        case OpF64ConvertI64S: return "f64.convert_i64_s"
        case OpF64ConvertI64U: return "f64.convert_i64_u"
        case OpF64ConvertI32S: return "f64.convert_i32_s"
        case OpF64ConvertI32U: return "f64.convert_i32_u"
        case OpI32Extend8S: return "i32.extend8_s"
        case OpI32Extend16S: return "i32.extend16_s"
        case OpI64Extend8S: return "i64.extend8_s"
        case OpI64Extend16S: return "i64.extend16_s"
        case OpI64Extend32S: return "i64.extend32_s"
        case OpI32ReinterpretF32: return "i32.reinterpret_f32"
        case OpI64ReinterpretF64: return "i64.reinterpret_f64"
        case OpF32ReinterpretI32: return "f32.reinterpret_i32"
        case OpF64ReinterpretI64: return "f64.reinterpret_i64"
        default:
                return fmt.Sprintf("unknown(0x%02x)", op)
        }
}

// TypeName returns the name of a WASM value type.
func TypeName(t byte) string {
        switch t {
        case TypeI32: return "i32"
        case TypeI64: return "i64"
        case TypeF32: return "f32"
        case TypeF64: return "f64"
        case TypeFuncRef: return "funcref"
        case TypeExternRef: return "externref"
        case TypeFunc: return "func"
        case TypeEmptyBlock: return "void"
        default: return fmt.Sprintf("unknown(0x%02x)", t)
        }
}

// ValidateModule checks if a WASM binary starts with the correct magic and version.
func ValidateModule(data []byte) error {
        if len(data) < 8 {
                return fmt.Errorf("wasm: module too short (%d bytes)", len(data))
        }
        magic := binary.LittleEndian.Uint32(data[0:4])
        if magic != WasmMagic {
                return fmt.Errorf("wasm: invalid magic number: 0x%08X (expected 0x%08X)", magic, WasmMagic)
        }
        version := binary.LittleEndian.Uint32(data[4:8])
        if version != WasmVersion {
                return fmt.Errorf("wasm: unsupported version: %d (expected %d)", version, WasmVersion)
        }
        return nil
}
