package wasm

import (
        "bytes"
        "encoding/binary"
        "math"
        "testing"
)

func TestWasmWriterBasics(t *testing.T) {
        w := NewWasmWriter()

        w.WriteByte(0x42)
        if w.Len() != 1 || w.Bytes()[0] != 0x42 {
                t.Error("WriteByte failed")
        }

        w.WriteBytes([]byte{0x01, 0x02})
        if w.Len() != 3 {
                t.Errorf("WriteBytes: expected len 3, got %d", w.Len())
        }
}

func TestWasmWriterLEB128(t *testing.T) {
        w := NewWasmWriter()

        // Single byte
        w.WriteU32(0)
        if w.Len() != 1 || w.Bytes()[0] != 0 {
                t.Error("WriteU32(0) failed")
        }

        // Multi-byte
        w2 := NewWasmWriter()
        w2.WriteU32(300)
        if w2.Len() != 2 {
                t.Errorf("WriteU32(300): expected 2 bytes, got %d", w2.Len())
        }
        if w2.Bytes()[0] != 0xAC || w2.Bytes()[1] != 0x02 {
                t.Errorf("WriteU32(300): wrong bytes %v", w2.Bytes())
        }
}

func TestWasmWriterI64LEB128(t *testing.T) {
        w := NewWasmWriter()
        w.WriteI64(42)
        if w.Len() != 1 || w.Bytes()[0] != 42 {
                t.Error("WriteI64(42) failed")
        }

        w2 := NewWasmWriter()
        w2.WriteI64(-1)
        if w2.Len() == 0 {
                t.Error("WriteI64(-1) produced no bytes")
        }
}

func TestWasmWriterF64(t *testing.T) {
        w := NewWasmWriter()
        w.WriteF64(3.14)
        if w.Len() != 8 {
                t.Errorf("WriteF64: expected 8 bytes, got %d", w.Len())
        }
        bits := math.Float64bits(3.14)
        got := binary.LittleEndian.Uint64(w.Bytes())
        if got != bits {
                t.Errorf("WriteF64: bits mismatch")
        }
}

func TestWasmWriterName(t *testing.T) {
        w := NewWasmWriter()
        w.WriteName("hello")
        // Length (1 byte) + "hello" (5 bytes) = 6 bytes
        if w.Len() != 6 {
                t.Errorf("WriteName: expected 6 bytes, got %d", w.Len())
        }
}

func TestWasmWriterMagic(t *testing.T) {
        w := NewWasmWriter()
        w.WriteMagic()
        if w.Len() != 8 {
                t.Errorf("WriteMagic: expected 8 bytes, got %d", w.Len())
        }
        magic := binary.LittleEndian.Uint32(w.Bytes()[0:4])
        if magic != WasmMagic {
                t.Errorf("magic: expected 0x%08X, got 0x%08X", WasmMagic, magic)
        }
        version := binary.LittleEndian.Uint32(w.Bytes()[4:8])
        if version != WasmVersion {
                t.Errorf("version: expected 0x%08X, got 0x%08X", WasmVersion, version)
        }
}

func TestFuncBodyBasic(t *testing.T) {
        fb := NewFuncBody()
        fb.I64Const(42)
        fb.Return()

        code := fb.Code
        if len(code) < 2 {
                t.Error("FuncBody: expected at least 2 bytes of code")
        }
        if code[0] != OpI64Const {
                t.Errorf("first opcode: expected 0x%02X (I64Const), got 0x%02X", OpI64Const, code[0])
        }
}

func TestFuncBodyEncode(t *testing.T) {
        fb := NewFuncBody()
        fb.I32Const(0)
        fb.Return()

        w := NewWasmWriter()
        fb.Encode(w)

        data := w.Bytes()
        if len(data) == 0 {
                t.Error("Encode produced no data")
        }
        // Should end with OpEnd
        if data[len(data)-1] != OpEnd {
                t.Errorf("last byte: expected 0x%02X (End), got 0x%02X", OpEnd, data[len(data)-1])
        }
}

func TestFuncBodyLocals(t *testing.T) {
        fb := NewFuncBody()
        fb.AddLocal(2, TypeI64)
        fb.AddLocal(3, TypeF64)
        fb.AddLocals(4, TypeI32)

        if len(fb.Locals) != 3 {
                t.Errorf("Locals: expected 3 entries, got %d", len(fb.Locals))
        }
}

func TestFuncBodyControlFlow(t *testing.T) {
        fb := NewFuncBody()
        fb.Block(TypeEmptyBlock)
        fb.Loop(TypeEmptyBlock)
        fb.I32Const(1)
        fb.BrIf(0)
        fb.Br(1)
        fb.End()
        fb.End()

        if fb.CodeLen() == 0 {
                t.Error("control flow produced no code")
        }
}

func TestFuncBodyCall(t *testing.T) {
        fb := NewFuncBody()
        fb.Call(0)
        code := fb.Code
        if len(code) == 0 {
                t.Error("Call produced no code")
        }
        if code[0] != OpCall {
                t.Errorf("Call: expected opcode 0x%02X, got 0x%02X", OpCall, code[0])
        }
}

func TestFuncBodyLocalsAndGet(t *testing.T) {
        fb := NewFuncBody()
        fb.LocalGet(0)
        fb.LocalSet(1)
        fb.LocalTee(2)

        code := fb.Code
        if len(code) < 3 {
                t.Error("variable ops produced insufficient code")
        }
        if code[0] != OpLocalGet {
                t.Errorf("LocalGet: expected 0x%02X, got 0x%02X", OpLocalGet, code[0])
        }
}

func TestFuncBodyMemoryOps(t *testing.T) {
        fb := NewFuncBody()
        fb.I64Load(Align8, 0)
        fb.I64Store(Align8, 0)
        fb.F64Load(Align8, 0)
        fb.F64Store(Align8, 0)

        code := fb.Code
        if len(code) == 0 {
                t.Error("memory ops produced no code")
        }
}

func TestTaggedValues(t *testing.T) {
        // TagWasmInt
        v := TagWasmInt(42)
        if v != 42 {
                t.Errorf("TagWasmInt(42) = %d, want 42", v)
        }

        // TagWasmBool true
        bt := TagWasmBool(true)
        if bt != WasmTrue {
                t.Errorf("TagWasmBool(true) = 0x%016X, want 0x%016X", bt, WasmTrue)
        }

        // TagWasmBool false
        bf := TagWasmBool(false)
        if bf != WasmFalse {
                t.Errorf("TagWasmBool(false) = 0x%016X, want 0x%016X", bf, WasmFalse)
        }

        // WasmNil
        if WasmNil != (uint64(1)<<63) {
                t.Error("WasmNil should have tag bit set and value 0")
        }

        // Negative integer
        neg := TagWasmInt(-1)
        if neg == WasmTrue || neg == WasmFalse || neg == WasmNil {
                t.Error("TagWasmInt(-1) should not collide with special values")
        }
}

func TestFuncType(t *testing.T) {
        ft := TypeVoid
        key := ft.Key()
        if key != "func([])->([])" {
                t.Errorf("TypeVoid.Key() = %q", key)
        }

        w := NewWasmWriter()
        ft.Encode(w)
        data := w.Bytes()
        if len(data) == 0 {
                t.Error("FuncType.Encode produced no data")
        }
        if data[0] != TypeFunc {
                t.Errorf("FuncType: expected 0x%02X (TypeFunc), got 0x%02X", TypeFunc, data[0])
        }
}

func TestFuncTypeWithParams(t *testing.T) {
        ft := TypeI64I64 // (i64) -> (i64)
        w := NewWasmWriter()
        ft.Encode(w)
        data := w.Bytes()
        if len(data) == 0 {
                t.Error("Encode produced no data")
        }
        // 0x60 (func) + 0x01 (1 param) + 0x7E (i64) + 0x01 (1 result) + 0x7E (i64)
        if !bytes.Contains(data, []byte{TypeFunc, 0x01, TypeI64, 0x01, TypeI64}) {
                t.Errorf("TypeI64I64 encoding: got %v", data)
        }
}

func TestExport(t *testing.T) {
        exp := &Export{Name: "main", Kind: ExportFunc, Index: 0}
        w := NewWasmWriter()
        exp.Encode(w)
        data := w.Bytes()
        if len(data) == 0 {
                t.Error("Export.Encode produced no data")
        }
}

func TestCommonOpcodes(t *testing.T) {
        // Verify key opcode values
        if OpUnreachable != 0x00 { t.Error("OpUnreachable wrong") }
        if OpNop != 0x01 { t.Error("OpNop wrong") }
        if OpBlock != 0x02 { t.Error("OpBlock wrong") }
        if OpLoop != 0x03 { t.Error("OpLoop wrong") }
        if OpIf != 0x04 { t.Error("OpIf wrong") }
        if OpElse != 0x05 { t.Error("OpElse wrong") }
        if OpEnd != 0x0B { t.Error("OpEnd wrong") }
        if OpBr != 0x0C { t.Error("OpBr wrong") }
        if OpReturn != 0x0F { t.Error("OpReturn wrong") }
        if OpCall != 0x10 { t.Error("OpCall wrong") }
        if OpI64Const != 0x42 { t.Error("OpI64Const wrong") }
        if OpI64Add != 0x7C { t.Error("OpI64Add wrong") }
        if OpI64Sub != 0x7D { t.Error("OpI64Sub wrong") }
        if OpI64Mul != 0x7E { t.Error("OpI64Mul wrong") }
}

func TestCompilerCreation(t *testing.T) {
        c := NewCompiler(nil)
        if c == nil {
                t.Fatal("NewCompiler returned nil")
        }
        if c.funcTypes == nil || c.funcs == nil || c.exports == nil {
                t.Error("Compiler slices not initialized")
        }
}

func TestImport(t *testing.T) {
        imp := &Import{
                Module: "env",
                Name:   "print",
                Kind:   ImportFunc,
                Desc:   TypeI64Void,
        }
        w := NewWasmWriter()
        imp.Encode(w)
        data := w.Bytes()
        if len(data) == 0 {
                t.Error("Import.Encode produced no data")
        }
}
