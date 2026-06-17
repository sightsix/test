// Package wasmobj implements a WebAssembly output module that combines code
// sections from multiple compilation units into a final .wasm binary. It
// handles import/export resolution and generates a valid WASM module with a
// start function for the _start entry point.
package wasmobj

import (
        "fmt"
        "math"
        "strings"

        "github.com/yilt/yiltc/internal/link/types"
)

// =====================================================================
// WASM binary format constants
// =====================================================================

// Section IDs.
const (
        wasmSecCustom   = 0
        wasmSecType     = 1
        wasmSecImport   = 2
        wasmSecFunction = 3
        wasmSecTable    = 4
        wasmSecMemory   = 5
        wasmSecGlobal   = 6
        wasmSecExport   = 7
        wasmSecStart    = 8
        wasmSecElem     = 9
        wasmSecCode     = 10
        wasmSecData     = 11
        wasmSecDataCount = 12
)

// Value types.
const (
        wasmValI32 = 0x7f
        wasmValI64 = 0x7e
        wasmValF32 = 0x7d
        wasmValF64 = 0x7c
        wasmValFuncref = 0x70
        wasmValExternref = 0x6f
        wasmValEmpty = 0x40 // empty block type
)

// Function types encoding.
const (
        wasmFuncTypeTag = 0x60
)

// External kinds.
const (
        wasmExtFunc   = 0
        wasmExtTable  = 1
        wasmExtMemory = 2
        wasmExtGlobal = 3
)

// Export kinds.
const (
        wasmExportFunc   = 0
        wasmExportTable  = 1
        wasmExportMemory = 2
        wasmExportGlobal = 3
)

// Opcodes.
const (
        wasmOpEnd           = 0x0b
        wasmOpCall          = 0x10
        wasmOpLocalGet      = 0x20
        wasmOpLocalSet      = 0x21
        wasmOpLocalTee      = 0x22
        wasmOpI32Const      = 0x41
        wasmOpI64Const      = 0x42
        wasmOpGlobalGet     = 0x23
        wasmOpGlobalSet     = 0x24
        wasmOpBr            = 0x0c
        wasmOpReturn        = 0x0f
        wasmOpUnreachable   = 0x00
)

// Limits flags.
const (
        wasmLimitsHasMax = 1
)

// =====================================================================
// Internal types
// =====================================================================

// wasmFuncType represents a function signature.
type wasmFuncType struct {
        params  []byte
        results []byte
}

// wasmImport represents a module import.
type wasmImport struct {
        module string
        name   string
        kind   byte   // wasmExtFunc, wasmExtTable, etc.
        typeIdx uint32 // type index for functions
        desc    []byte // additional descriptor bytes
}

// wasmExport represents a module export.
type wasmExport struct {
        name   string
        kind   byte   // wasmExportFunc, etc.
        index  uint32
}

// wasmFunc represents a function body.
type wasmFunc struct {
        typeIdx  uint32
        locals   []wasmLocalEntry
        body     []byte
        codeName string // optional debug name
}

// wasmLocalEntry is a (count, type) pair for locals declarations.
type wasmLocalEntry struct {
        count uint32
        typ   byte
}

// wasmGlobal represents a global variable.
type wasmGlobal struct {
        typ    byte
        mutable bool
        init   []byte
}

// wasmMemory describes a memory segment.
type wasmMemory struct {
        minPages uint32
        maxPages uint32 // 0 = no max
}

// wasmDataSegment describes an initialised data segment.
type wasmDataSegment struct {
        memoryIdx  uint32
        offsetExpr []byte
        data       []byte
        isPassive  bool
}

// wasmCodeUnit represents a compilation unit added via AddCode.
type wasmCodeUnit struct {
        name string
        data []byte // raw function bodies
}

// =====================================================================
// Linker
// =====================================================================

// L implements types.Linker for WASM targets.
type L struct {
        startFunc   string
        exports     []wasmExport
        imports     []wasmImport
        funcs       []wasmFunc
        funcTypes   []wasmFuncType
        globals     []wasmGlobal
        memories    []wasmMemory
        dataSegs    []wasmDataSegment
        codeUnits   []wasmCodeUnit
        customSections []customSec

        // Track which type indices are used for different signatures.
        typeMap map[string]uint32 // "params|results" → type index
}

type customSec struct {
        name string
        data []byte
}

// New creates a new WASM linker.
func New() *L {
        return &L{
                typeMap: make(map[string]uint32),
                memories: []wasmMemory{{minPages: 1}},
        }
}

// ---------- types.Linker interface ----------

// AddCode adds a code section. For WASM, this is expected to be one or more
// encoded function bodies. The linker will wrap them in the WASM code section
// format.
func (l *L) AddCode(name string, data []byte) {
        l.codeUnits = append(l.codeUnits, wasmCodeUnit{
                name: name,
                data: append([]byte(nil), data...),
        })
}

// AddData adds a read-only data section.
func (l *L) AddData(name string, data []byte) {
        // Create a data segment initialised at offset 0 of memory 0.
        // The offset expression is i32.const 0, end.
        offsetExpr := []byte{wasmOpI32Const, 0x00, wasmOpEnd}
        l.dataSegs = append(l.dataSegs, wasmDataSegment{
                memoryIdx:  0,
                offsetExpr: offsetExpr,
                data:       append([]byte(nil), data...),
        })
}

// AddRWData adds a read-write initialised data section (same as AddData for WASM).
func (l *L) AddRWData(name string, data []byte) {
        l.AddData(name, data)
}

// AddBSS reserves zero-initialised bytes. For WASM we grow memory.
func (l *L) AddBSS(name string, size uint64, align uint32) {
        // Ensure memory is large enough.
        pagesNeeded := (size + 0xffff) / 0x10000 // ceil(size / 64KiB)
        if pagesNeeded < 1 {
                pagesNeeded = 1
        }
        if len(l.memories) == 0 {
                l.memories = append(l.memories, wasmMemory{})
        }
        mem := &l.memories[0]
        if mem.minPages < uint32(pagesNeeded) {
                mem.minPages = uint32(pagesNeeded)
        }
}

// AddSymbol adds a function or data export.
func (l *L) AddSymbol(name string, addr uint64, size uint64, kind types.SymbolKind) {
        switch kind {
        case types.SymFunc:
                l.exports = append(l.exports, wasmExport{
                        name:  name,
                        kind:  wasmExportFunc,
                        index: uint32(addr),
                })
        case types.SymData:
                // Data symbols become memory exports (not standard, but useful).
                l.exports = append(l.exports, wasmExport{
                        name:  name,
                        kind:  wasmExportMemory,
                        index: 0,
                })
        case types.SymBSS:
                // BSS doesn't produce an export in WASM.
        }
}

// AddRelocation is a no-op for WASM since relocations are handled
// differently (via import/export resolution).
func (l *L) AddRelocation(section string, offset uint64, sym string, relType types.RelocType, addend int64) {
        // WASM modules use the call_indirect mechanism for external calls,
        // so we don't need to apply binary relocations at link time.
        // The caller is expected to have already resolved all references.
}

// SetEntryPoint sets the start function name.
func (l *L) SetEntryPoint(name string) {
        l.startFunc = name
}

// SetCustomSection adds a raw WASM custom section.
func (l *L) SetCustomSection(name string, data []byte, flags uint32) {
        l.customSections = append(l.customSections, customSec{
                name: name,
                data: data,
        })
}

// ---------- Public API ----------

// AddImport adds an imported function.
func (l *L) AddImport(module, name string, params, results []byte) uint32 {
        _ = typeSig(params, results)
        typeIdx := l.getOrCreateType(params, results)

        l.imports = append(l.imports, wasmImport{
                module:  module,
                name:    name,
                kind:    wasmExtFunc,
                typeIdx: typeIdx,
        })

        return uint32(len(l.imports) - 1)
}

// AddFunc adds a function with explicit type, locals, and body.
func (l *L) AddFunc(params, results []byte, locals []wasmLocalEntry, body []byte) uint32 {
        typeIdx := l.getOrCreateType(params, results)
        l.funcs = append(l.funcs, wasmFunc{
                typeIdx: typeIdx,
                locals:  locals,
                body:    body,
        })
        return uint32(len(l.imports)) + uint32(len(l.funcs)) - 1
}

// AddExportFunc exports a function by index.
func (l *L) AddExportFunc(name string, funcIdx uint32) {
        l.exports = append(l.exports, wasmExport{
                name:  name,
                kind:  wasmExportFunc,
                index: funcIdx,
        })
}

// AddExportMemory exports a memory by index.
func (l *L) AddExportMemory(name string, memIdx uint32) {
        l.exports = append(l.exports, wasmExport{
                name:  name,
                kind:  wasmExportMemory,
                index: memIdx,
        })
}

// SetMinMemory sets the minimum number of 64KiB pages for memory 0.
func (l *L) SetMinMemory(pages uint32) {
        if len(l.memories) == 0 {
                l.memories = append(l.memories, wasmMemory{})
        }
        l.memories[0].minPages = pages
}

// SetMaxMemory sets the maximum number of 64KiB pages for memory 0.
func (l *L) SetMaxMemory(pages uint32) {
        if len(l.memories) == 0 {
                l.memories = append(l.memories, wasmMemory{})
        }
        l.memories[0].maxPages = pages
}

// =====================================================================
// Link – main entry point
// =====================================================================

// Link produces the final .wasm binary.
func (l *L) Link() ([]byte, error) {
        var buf []byte

        // Magic number + version.
        buf = append(buf, 0x00, 0x61, 0x73, 0x6d) // \0asm
        buf = append(buf, 0x01, 0x00, 0x00, 0x00) // version 1

        // Custom sections (e.g., .debug_info, name section).
        for _, cs := range l.customSections {
                buf = l.writeCustomSection(buf, cs.name, cs.data)
        }

        // Type section.
        buf = l.writeTypeSection(buf)

        // Import section.
        buf = l.writeImportSection(buf)

        // Function section (type indices for locally-defined functions).
        buf = l.writeFunctionSection(buf)

        // Memory section.
        buf = l.writeMemorySection(buf)

        // Global section.
        buf = l.writeGlobalSection(buf)

        // Export section.
        buf = l.writeExportSection(buf)

        // Start section.
        buf = l.writeStartSection(buf)

        // Data count section (if we have data segments).
        if len(l.dataSegs) > 0 {
                buf = l.writeDataCountSection(buf)
        }

        // Code section.
        buf = l.writeCodeSection(buf)

        // Data section.
        buf = l.writeDataSection(buf)

        return buf, nil
}

// =====================================================================
// Section writers
// =====================================================================

func (l *L) writeCustomSection(buf []byte, name string, data []byte) []byte {
        // Custom section format:
        //   section_id = 0
        //   section_size = vec(name) + data
        //   name = vec(byte)
        //   data

        var content []byte
        content = appendLEB128String(content, name)
        content = append(content, data...)
        return l.writeSection(buf, wasmSecCustom, content)
}

func (l *L) writeTypeSection(buf []byte) []byte {
        if len(l.funcTypes) == 0 {
                return buf
        }
        var content []byte
        content = appendLEB128(content, uint32(len(l.funcTypes)))
        for _, ft := range l.funcTypes {
                content = append(content, wasmFuncTypeTag)
                content = appendLEB128(content, uint32(len(ft.params)))
                content = append(content, ft.params...)
                content = appendLEB128(content, uint32(len(ft.results)))
                content = append(content, ft.results...)
        }
        return l.writeSection(buf, wasmSecType, content)
}

func (l *L) writeImportSection(buf []byte) []byte {
        if len(l.imports) == 0 {
                return buf
        }
        var content []byte
        content = appendLEB128(content, uint32(len(l.imports)))
        for _, imp := range l.imports {
                content = appendLEB128String(content, imp.module)
                content = appendLEB128String(content, imp.name)
                content = append(content, imp.kind)
                switch imp.kind {
                case wasmExtFunc:
                        content = appendLEB128(content, imp.typeIdx)
                case wasmExtMemory:
                        // limits: flags, min, [max]
                        content = append(content, 0) // no max
                        content = appendLEB128(content, 1) // min 1 page
                case wasmExtGlobal:
                        content = append(content, wasmValI32) // type
                        content = append(content, 0) // immutable
                }
        }
        return l.writeSection(buf, wasmSecImport, content)
}

func (l *L) writeFunctionSection(buf []byte) []byte {
        if len(l.funcs) == 0 && len(l.codeUnits) == 0 {
                return buf
        }
        // Combine explicitly added functions and code units.
        // Code units are treated as function bodies with a default () -> () type.
        // We need to ensure all code units have an associated function entry.

        // Ensure we have the () -> () type.
        voidType := l.getOrCreateType(nil, nil)

        // For code units that don't have a matching func entry, create one.
        totalFuncs := len(l.funcs)
        for _, cu := range l.codeUnits {
                // Each code unit may contain multiple function bodies.
                // We count them by scanning the data.
                numBodies := countWasmBodies(cu.data)
                for i := 0; i < numBodies; i++ {
                        if totalFuncs <= i+len(l.funcs) {
                                // Add a default function entry.
                                l.funcs = append(l.funcs, wasmFunc{
                                        typeIdx: voidType,
                                        body:    nil, // will be read from codeUnits during code section emission
                                })
                        }
                }
        }

        var content []byte
        content = appendLEB128(content, uint32(len(l.funcs)))
        for _, fn := range l.funcs {
                content = appendLEB128(content, fn.typeIdx)
        }
        return l.writeSection(buf, wasmSecFunction, content)
}

func (l *L) writeMemorySection(buf []byte) []byte {
        if len(l.memories) == 0 {
                return buf
        }
        var content []byte
        content = appendLEB128(content, uint32(len(l.memories)))
        for _, mem := range l.memories {
                if mem.maxPages > 0 {
                        content = append(content, wasmLimitsHasMax)
                        content = appendLEB128(content, mem.minPages)
                        content = appendLEB128(content, mem.maxPages)
                } else {
                        content = append(content, 0)
                        content = appendLEB128(content, mem.minPages)
                }
        }
        return l.writeSection(buf, wasmSecMemory, content)
}

func (l *L) writeGlobalSection(buf []byte) []byte {
        if len(l.globals) == 0 {
                return buf
        }
        var content []byte
        content = appendLEB128(content, uint32(len(l.globals)))
        for _, g := range l.globals {
                content = append(content, g.typ)
                if g.mutable {
                        content = append(content, 1)
                } else {
                        content = append(content, 0)
                }
                content = append(content, g.init...)
        }
        return l.writeSection(buf, wasmSecGlobal, content)
}

func (l *L) writeExportSection(buf []byte) []byte {
        if len(l.exports) == 0 {
                return buf
        }
        var content []byte
        content = appendLEB128(content, uint32(len(l.exports)))
        for _, exp := range l.exports {
                content = appendLEB128String(content, exp.name)
                content = append(content, exp.kind)
                content = appendLEB128(content, exp.index)
        }
        return l.writeSection(buf, wasmSecExport, content)
}

func (l *L) writeStartSection(buf []byte) []byte {
        if l.startFunc == "" {
                return buf
        }
        // Find the function index for the start function.
        idx := l.findFuncIndex(l.startFunc)
        if idx == math.MaxUint32 {
                return buf // start function not found, skip
        }
        var content []byte
        content = appendLEB128(content, idx)
        return l.writeSection(buf, wasmSecStart, content)
}

func (l *L) writeDataCountSection(buf []byte) []byte {
        var content []byte
        content = appendLEB128(content, uint32(len(l.dataSegs)))
        return l.writeSection(buf, wasmSecDataCount, content)
}

func (l *L) writeCodeSection(buf []byte) []byte {
        // Combine function bodies from l.funcs and l.codeUnits.
        var funcBodies [][]byte

        // First, explicitly added functions.
        funcIdx := 0
        codeUnitIdx := 0

        // Determine how many funcs come from explicit AddFunc calls.
        numCodeUnits := 0
        for _, cu := range l.codeUnits {
                numCodeUnits += countWasmBodies(cu.data)
        }
        numExplicit := len(l.funcs) - numCodeUnits

        // Write explicitly-added function bodies.
        for i := 0; i < numExplicit && funcIdx < len(l.funcs); i++ {
                fn := l.funcs[funcIdx]
                body := encodeWasmFuncBody(fn.locals, fn.body)
                funcBodies = append(funcBodies, body)
                funcIdx++
        }

        // Write code unit function bodies.
        for _, cu := range l.codeUnits {
                bodies := splitWasmBodies(cu.data)
                for _, body := range bodies {
                        funcBodies = append(funcBodies, body)
                }
                codeUnitIdx++
        }

        // Any remaining functions.
        for ; funcIdx < len(l.funcs); funcIdx++ {
                fn := l.funcs[funcIdx]
                body := encodeWasmFuncBody(fn.locals, fn.body)
                funcBodies = append(funcBodies, body)
        }

        if len(funcBodies) == 0 {
                return buf
        }

        var content []byte
        content = appendLEB128(content, uint32(len(funcBodies)))
        for _, body := range funcBodies {
                content = append(content, body...)
        }
        return l.writeSection(buf, wasmSecCode, content)
}

func (l *L) writeDataSection(buf []byte) []byte {
        if len(l.dataSegs) == 0 {
                return buf
        }
        var content []byte
        content = appendLEB128(content, uint32(len(l.dataSegs)))
        for _, seg := range l.dataSegs {
                if seg.isPassive {
                        content = append(content, 0x01) // passive data segment flag
                        content = append(content, seg.data...)
                } else {
                        content = append(content, 0x00) // active, memory 0
                        content = append(content, seg.offsetExpr...)
                        content = append(content, seg.data...)
                }
        }
        return l.writeSection(buf, wasmSecData, content)
}

// =====================================================================
// Helpers
// =====================================================================

// writeSection writes a complete WASM section: id, size, content.
func (l *L) writeSection(buf []byte, id byte, content []byte) []byte {
        buf = append(buf, id)
        buf = appendLEB128(buf, uint32(len(content)))
        buf = append(buf, content...)
        return buf
}

// getOrCreateType returns the type index for the given signature, creating
// a new entry if needed.
func (l *L) getOrCreateType(params, results []byte) uint32 {
        sig := typeSig(params, results)
        if idx, ok := l.typeMap[sig]; ok {
                return idx
        }
        p := make([]byte, len(params))
        copy(p, params)
        r := make([]byte, len(results))
        copy(r, results)
        idx := uint32(len(l.funcTypes))
        l.funcTypes = append(l.funcTypes, wasmFuncType{params: p, results: r})
        l.typeMap[sig] = idx
        return idx
}

// typeSig creates a string key for a function signature.
func typeSig(params, results []byte) string {
        var b strings.Builder
        b.WriteString("p:")
        for _, v := range params {
                fmt.Fprintf(&b, "%02x", v)
        }
        b.WriteString("r:")
        for _, v := range results {
                fmt.Fprintf(&b, "%02x", v)
        }
        return b.String()
}

// appendLEB128 appends an unsigned LEB128 encoding of v.
func appendLEB128(buf []byte, v uint32) []byte {
        for {
                b := byte(v & 0x7f)
                v >>= 7
                if v != 0 {
                        b |= 0x80
                }
                buf = append(buf, b)
                if v == 0 {
                        break
                }
        }
        return buf
}

// appendLEB128String appends a WASM string (length-prefixed UTF-8).
func appendLEB128String(buf []byte, s string) []byte {
        buf = appendLEB128(buf, uint32(len(s)))
        buf = append(buf, []byte(s)...)
        return buf
}

// appendSignedLEB128 appends a signed LEB128 encoding of v.
func appendSignedLEB128(buf []byte, v int32) []byte {
        for {
                b := byte(v & 0x7f)
                v >>= 7
                if (v == 0 && (b&0x40) == 0) || (v == -1 && (b&0x40) != 0) {
                        buf = append(buf, b)
                        break
                }
                buf = append(buf, b|0x80)
        }
        return buf
}

// encodeWasmFuncBody encodes locals + body into a WASM function body
// with a size prefix.
func encodeWasmFuncBody(locals []wasmLocalEntry, body []byte) []byte {
        var inner []byte

        // Encode locals: vec of (count, type).
        inner = appendLEB128(inner, uint32(len(locals)))
        for _, loc := range locals {
                inner = appendLEB128(inner, loc.count)
                inner = append(inner, loc.typ)
        }

        // Append body.
        inner = append(inner, body...)

        // The inner is the full function body, size-prefixed.
        var result []byte
        result = appendLEB128(result, uint32(len(inner)))
        result = append(result, inner...)
        return result
}

// countWasmBodies counts the number of function bodies in a raw WASM code
// section data stream. Each body starts with a LEB128 size followed by
// locals declarations and bytecodes.
func countWasmBodies(data []byte) int {
        if len(data) == 0 {
                return 0
        }
        count := 0
        off := 0
        for off < len(data) {
                // Read size.
                size, n := readLEB128(data, off)
                if n == 0 {
                        break
                }
                off += int(size) + n
                count++
        }
        return count
}

// splitWasmBodies splits raw code section data into individual function
// bodies (each with its own size prefix).
func splitWasmBodies(data []byte) [][]byte {
        var bodies [][]byte
        off := 0
        for off < len(data) {
                size, n := readLEB128(data, off)
                if n == 0 {
                        break
                }
                bodySize := n + int(size)
                if off+bodySize > len(data) {
                        break
                }
                body := make([]byte, bodySize)
                copy(body, data[off:off+bodySize])
                bodies = append(bodies, body)
                off += bodySize
        }
        return bodies
}

// readLEB128 reads an unsigned LEB128 value from data at offset.
// Returns (value, bytesConsumed).
func readLEB128(data []byte, off int) (uint32, int) {
        var result uint32
        var shift uint
        var n int
        for off+n < len(data) {
                b := data[off+n]
                n++
                result |= uint32(b&0x7f) << shift
                if (b & 0x80) == 0 {
                        break
                }
                shift += 7
        }
        return result, n
}

// findFuncIndex finds the function index by name. Checks exports.
func (l *L) findFuncIndex(name string) uint32 {
        // Check if name matches an export and return its index.
        for _, exp := range l.exports {
                if exp.name == name && exp.kind == wasmExportFunc {
                        return exp.index
                }
        }
        // Not found.
        return math.MaxUint32
}
