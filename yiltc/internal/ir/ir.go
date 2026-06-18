// Package ir defines a simple SSA-like intermediate representation that bridges
// the Yilt type-checked AST and the machine-code backends (x86-64, AArch64,
// RISC-V, WASM).
//
// Design goals:
//   - Every value is defined exactly once (SSA property).
//   - Basic blocks are terminated by exactly one control-flow instruction
//     (jump, branch, or return).
//   - Block parameters replace traditional φ-nodes, making the IR easier to
//     construct and lower.
//   - Tagged-value operations expose the Yilt 64-bit tagging scheme so that
//     backends can emit efficient tag/untag sequences.
//   - Arena push/pop and alloc instructions give the backend control over
//     short-lived allocations.
//
// The Builder provides a fluent API for constructing IR from the checked AST.
package ir

import (
        "fmt"
        "strings"

        "github.com/yilt/yiltc/internal/ast"
)

// ---------------------------------------------------------------------------
// Tag constants for the Yilt tagged 64-bit value encoding.
//
// The high 8 bits (bits 56–63) of a 64-bit tagged value encode the type
// tag.  The remaining 56 bits hold the payload (integer value, pointer,
// etc.).  For NaN-boxed floating-point values the encoding is more complex
// (see OpTag / OpUntag).  These constants are exposed so that backends can
// generate efficient dispatch code.
// ---------------------------------------------------------------------------

const (
        TagVoid   uint8 = 0
        TagInt    uint8 = 1
        TagBool   uint8 = 2
        TagFp     uint8 = 3
        TagStr    uint8 = 4
        TagTable  uint8 = 5
        TagFunc   uint8 = 6
        TagHandle uint8 = 7
        TagError  uint8 = 14
        TagNil    uint8 = 9
)

// TagName returns a human-readable name for a tag value.
func TagName(tag uint8) string {
        switch tag {
        case TagVoid:
                return "void"
        case TagInt:
                return "int"
        case TagBool:
                return "bool"
        case TagFp:
                return "fp"
        case TagStr:
                return "str"
        case TagTable:
                return "table"
        case TagFunc:
                return "fn"
        case TagHandle:
                return "handle"
        case TagError:
                return "error"
        case TagNil:
                return "nil"
        default:
                return fmt.Sprintf("tag<%d>", tag)
        }
}

// ---------------------------------------------------------------------------
// Value types
// ---------------------------------------------------------------------------

// ValType describes the machine-level type of an IR value.
type ValType int

const (
        VInt    ValType = iota // signed 64-bit integer
        VUint                   // unsigned 64-bit integer
        VFp                     // 64-bit IEEE-754 float
        VBool                   // 1-bit boolean (stored as 64-bit)
        VStr                    // pointer to string header
        VTable                  // pointer to table header
        VFunc                   // pointer to function descriptor
        VHandle                 // spawn / task handle (opaque integer)
        VVoid                   // no value (side-effect only)
        VTagged                 // tagged 64-bit Yilt value (dynamic type)
        VRaw                    // raw pointer / untyped machine word
)

func (vt ValType) String() string {
        switch vt {
        case VInt:
                return "i64"
        case VUint:
                return "u64"
        case VFp:
                return "f64"
        case VBool:
                return "bool"
        case VStr:
                return "str"
        case VTable:
                return "table"
        case VFunc:
                return "fn"
        case VHandle:
                return "handle"
        case VVoid:
                return "void"
        case VTagged:
                return "tagged"
        case VRaw:
                return "raw"
        default:
                return "<unknown>"
        }
}

// ---------------------------------------------------------------------------
// Opcodes
// ---------------------------------------------------------------------------

// Op is the opcode of an IR instruction.
type Op int

const (
        // ---- Constants ----
        OpConst Op = iota
        OpConstInt
        OpConstUint
        OpConstFp
        OpConstStr
        OpConstBool
        OpConstNil
        OpConstRawInt
        OpConstTaggedStr

        // ---- Arithmetic ----
        OpAdd
        OpSub
        OpMul
        OpDiv
        OpMod

        // ---- Bitwise ----
        OpAnd
        OpOr
        OpXor
        OpShl
        OpShr

        // ---- Unary ----
        OpNeg
        OpNot
        OpBitNot

        // ---- Comparison ----
        OpEq
        OpNeq
        OpLt
        OpLe
        OpGt
        OpGe

        // ---- Call / Return ----
        OpCall
        OpCallIndirect
        OpReturn

        // ---- Control flow ----
        OpJump
        OpBranch

        // ---- Memory ----
        OpCopy      // copy value from Src[0] into Dest's slot
        OpLoad
        OpStore
        OpAlloc
        OpStackAlloc

        // ---- Table operations ----
        OpTableNew
        OpTableGet
        OpTableSet
        OpTableLen
        OpTableDelete

        // ---- Index & member ----
        OpIndexGet
        OpIndexSet
        OpMemberGet

        // ---- Tagged value operations ----
        OpTag
        OpUntag
        OpCheckTag

        // ---- Arena / GC ----
        OpArenaPush
        OpArenaPop

        // ---- Concurrency ----
        OpSpawn
        OpAwait

        // ---- Misc ----
        OpParam    // block parameter definition (pseudo-instruction)
        OpNop      // no-operation (placeholder)
        OpPanic    // abort with a message
)

func (op Op) String() string {
        switch op {
        case OpConst:
                return "const"
        case OpConstInt:
                return "const.i64"
        case OpConstUint:
                return "const.u64"
        case OpConstFp:
                return "const.f64"
        case OpConstStr:
                return "const.str"
        case OpConstBool:
                return "const.bool"
        case OpConstNil:
                return "const.nil"
        case OpConstRawInt:
                return "const.raw"
        case OpConstTaggedStr:
                return "const.tagstr"
        case OpAdd:
                return "add"
        case OpSub:
                return "sub"
        case OpMul:
                return "mul"
        case OpDiv:
                return "div"
        case OpMod:
                return "mod"
        case OpAnd:
                return "and"
        case OpOr:
                return "or"
        case OpXor:
                return "xor"
        case OpShl:
                return "shl"
        case OpShr:
                return "shr"
        case OpNeg:
                return "neg"
        case OpNot:
                return "not"
        case OpBitNot:
                return "bitnot"
        case OpEq:
                return "eq"
        case OpNeq:
                return "neq"
        case OpLt:
                return "lt"
        case OpLe:
                return "le"
        case OpGt:
                return "gt"
        case OpGe:
                return "ge"
        case OpCall:
                return "call"
        case OpCallIndirect:
                return "call.indirect"
        case OpReturn:
                return "return"
        case OpJump:
                return "jump"
        case OpBranch:
                return "branch"
        case OpLoad:
                return "load"
        case OpStore:
                return "store"
        case OpAlloc:
                return "alloc"
        case OpStackAlloc:
                return "stackalloc"
        case OpTableNew:
                return "table.new"
        case OpTableGet:
                return "table.get"
        case OpTableSet:
                return "table.set"
        case OpTableLen:
                return "table.len"
        case OpTableDelete:
                return "table.del"
        case OpIndexGet:
                return "index.get"
        case OpIndexSet:
                return "index.set"
        case OpMemberGet:
                return "member.get"
        case OpTag:
                return "tag"
        case OpUntag:
                return "untag"
        case OpCheckTag:
                return "checktag"
        case OpArenaPush:
                return "arena.push"
        case OpArenaPop:
                return "arena.pop"
        case OpSpawn:
                return "spawn"
        case OpAwait:
                return "await"
        case OpParam:
                return "param"
        case OpCopy:
                return "copy"
        case OpNop:
                return "nop"
        case OpPanic:
                return "panic"
        default:
                return fmt.Sprintf("op<%d>", op)
        }
}

// ---------------------------------------------------------------------------
// Constant value
// ---------------------------------------------------------------------------

// ConstVal holds a literal constant.
type ConstVal struct {
        Kind   ValType
        IntVal int64
        UintVal uint64
        FpVal  float64
        StrVal string
        BoolVal bool
}

// String returns a debug representation.
func (c ConstVal) String() string {
        switch c.Kind {
        case VInt:
                return fmt.Sprintf("%d", c.IntVal)
        case VUint:
                return fmt.Sprintf("%du", c.UintVal)
        case VFp:
                return fmt.Sprintf("%g", c.FpVal)
        case VStr:
                return fmt.Sprintf("%q", c.StrVal)
        case VBool:
                if c.BoolVal {
                        return "true"
                }
                return "false"
        case VVoid:
                return "void"
        default:
                return "nil"
        }
}

// ---------------------------------------------------------------------------
// Value — virtual register (SSA)
// ---------------------------------------------------------------------------

// Value represents an SSA virtual register.  Each Value is defined exactly
// once by a single instruction (or as a function/block parameter).
type Value struct {
        ID    int      // unique register ID (monotonic, module-wide)
        Type  ValType  // machine type of the value
        Name  string   // optional human-readable name for debugging
        Const *ConstVal // non-nil if this value is a compile-time constant
}

// IsConst returns true if the value is a compile-time constant.
func (v *Value) IsConst() bool { return v.Const != nil }

// String returns a debug representation: "%name" or "vN".
func (v *Value) String() string {
        if v.Name != "" {
                return "%" + v.Name
        }
        return fmt.Sprintf("v%d", v.ID)
}

// ---------------------------------------------------------------------------
// Instruction metadata
// ---------------------------------------------------------------------------

// BranchTarget describes one target of a jump or branch instruction.
type BranchTarget struct {
        Block *Block  // destination basic block
        Args  []*Value // arguments to the block's parameters
}

// InstrMeta holds supplementary information for certain instructions.
type InstrMeta struct {
        // Pos is the source location that produced this instruction.
        Pos ast.Pos

        // FnName is the symbol name of the callee (for OpCall).
        FnName string

        // Field is the member name (for OpMemberGet).
        Field string

        // Tag is the tag value (for OpTag, OpCheckTag).
        Tag uint8

        // Size is the allocation size in bytes (for OpAlloc, OpStackAlloc, OpArenaPush).
        Size int

        // Capacity is the initial capacity for OpTableNew.
        Capacity int

        // Message is the panic message string (for OpPanic).
        Message string

        // Jump / branch targets (for OpJump, OpBranch).
        Jump *BranchTarget
        Then *BranchTarget
        Else *BranchTarget
}

// ---------------------------------------------------------------------------
// Instruction
// ---------------------------------------------------------------------------

// Instr is a single IR instruction.
type Instr struct {
        Op     Op       // opcode
        Dest   *Value   // destination virtual register (nil for side-effect ops)
        Src    []*Value // source operands
        Meta   *InstrMeta // supplementary metadata (may be nil)
        NoFold bool     // if true, constant folder should skip this instruction
}

// String returns a human-readable disassembly line.
func (i *Instr) String() string {
        var sb strings.Builder

        if i.Dest != nil {
                sb.WriteString(i.Dest.String())
                sb.WriteString(" = ")
        }
        sb.WriteString(i.Op.String())

        // Emit operands.
        if len(i.Src) > 0 {
                sb.WriteString(" ")
                for j, v := range i.Src {
                        if j > 0 {
                                sb.WriteString(", ")
                        }
                        sb.WriteString(v.String())
                }
        }

        // Emit metadata.
        if i.Meta != nil {
                if i.Meta.FnName != "" {
                        sb.WriteString(" @")
                        sb.WriteString(i.Meta.FnName)
                }
                if i.Meta.Field != "" {
                        sb.WriteString(" .")
                        sb.WriteString(i.Meta.Field)
                }
                if i.Meta.Tag != 0 {
                        sb.WriteString(" tag=")
                        sb.WriteString(TagName(i.Meta.Tag))
                }
                if i.Meta.Size != 0 {
                        sb.WriteString(fmt.Sprintf(" size=%d", i.Meta.Size))
                }
                if i.Meta.Message != "" {
                        sb.WriteString(" msg=")
                        sb.WriteString(fmt.Sprintf("%q", i.Meta.Message))
                }
                if i.Meta.Jump != nil {
                        sb.WriteString(" -> ")
                        sb.WriteString(i.formatBranchTarget(i.Meta.Jump))
                }
                if i.Meta.Then != nil {
                        sb.WriteString(" then -> ")
                        sb.WriteString(i.formatBranchTarget(i.Meta.Then))
                }
                if i.Meta.Else != nil {
                        sb.WriteString(" else -> ")
                        sb.WriteString(i.formatBranchTarget(i.Meta.Else))
                }
        }

        return sb.String()
}

func (i *Instr) formatBranchTarget(bt *BranchTarget) string {
        if bt.Block == nil {
                return "<nil>"
        }
        var sb strings.Builder
        sb.WriteString(bt.Block.Label)
        if len(bt.Args) > 0 {
                sb.WriteString("(")
                for j, a := range bt.Args {
                        if j > 0 {
                                sb.WriteString(", ")
                        }
                        sb.WriteString(a.String())
                }
                sb.WriteString(")")
        }
        return sb.String()
}

// IsTerminator returns true for instructions that end a basic block.
func (i *Instr) IsTerminator() bool {
        switch i.Op {
        case OpJump, OpBranch, OpReturn, OpPanic:
                return true
        default:
                return false
        }
}

// ---------------------------------------------------------------------------
// Basic block
// ---------------------------------------------------------------------------

// Block is a basic block in SSA form.  It contains a list of instructions
// and a list of block parameters (a simplified alternative to φ-nodes).
// The block must be terminated by exactly one terminator instruction.
type Block struct {
        // Label is the block's name (must be unique within the function).
        Label string

        // Params are the block parameters.  Predecessor jumps must supply
        // matching arguments.
        Params []*Value

        // Instrs is the instruction sequence, ending with a single terminator.
        Instrs []*Instr
}

// NewBlock creates an empty basic block with the given label.
func NewBlock(label string) *Block {
        return &Block{
                Label: label,
        }
}

// AddInstr appends an instruction to the block.
func (b *Block) AddInstr(i *Instr) {
        b.Instrs = append(b.Instrs, i)
}

// MarkNoFold sets NoFold on the last instruction in the block.
// This is used to prevent the constant folder from folding identity
// operations that are needed for mutable variable slot allocation.
func (b *Block) MarkNoFold() {
        if n := len(b.Instrs); n > 0 {
                b.Instrs[n-1].NoFold = true
        }
}

// AddParam appends a block parameter and returns the corresponding Value.
func (b *Block) AddParam(id int, typ ValType, name string) *Value {
        v := &Value{ID: id, Type: typ, Name: name}
        b.Params = append(b.Params, v)
        return v
}

// Terminator returns the block's terminator instruction, or nil if the
// block is not yet terminated.
func (b *Block) Terminator() *Instr {
        if len(b.Instrs) == 0 {
                return nil
        }
        last := b.Instrs[len(b.Instrs)-1]
        if last.IsTerminator() {
                return last
        }
        return nil
}

// IsTerminated returns true if the block ends with a terminator instruction.
func (b *Block) IsTerminated() bool {
        return b.Terminator() != nil
}

// ParamCount returns the number of block parameters.
func (b *Block) ParamCount() int {
        return len(b.Params)
}

// String returns a debug dump of the block.
func (b *Block) String() string {
        var sb strings.Builder
        sb.WriteString(b.Label)
        if len(b.Params) > 0 {
                sb.WriteString("(")
                for i, p := range b.Params {
                        if i > 0 {
                                sb.WriteString(", ")
                        }
                        sb.WriteString(p.Type.String())
                        sb.WriteString(" ")
                        sb.WriteString(p.String())
                }
                sb.WriteString(")")
        }
        sb.WriteString(":\n")
        for _, instr := range b.Instrs {
                sb.WriteString("  ")
                sb.WriteString(instr.String())
                sb.WriteString("\n")
        }
        return sb.String()
}

// ---------------------------------------------------------------------------
// Function
// ---------------------------------------------------------------------------

// Func represents a compiled function in the IR.
type Func struct {
        // Name is the symbol name (may be module-qualified).
        Name string

        // Params are the function parameters.  These are implicitly defined
        // at the entry block and correspond to physical arguments.
        Params []*Value

        // RetType is the machine-level return type.
        RetType ValType

        // Blocks is the ordered list of basic blocks.  The first block is
        // the entry point.
        Blocks []*Block

        // Entry is a convenience pointer to Blocks[0]; it may be nil if the
        // function has not been built yet.
        Entry *Block

        // Public is true if the function is exported from the module.
        Public bool

        // Extern is true for FFI / external functions (no body).
        Extern bool

        // Pos is the source location of the original function declaration.
        Pos ast.Pos
}

// NewFunc creates a function with an empty entry block labelled "entry".
func NewFunc(name string, retType ValType, pub, ext bool) *Func {
        entry := NewBlock("entry")
        f := &Func{
                Name:    name,
                RetType: retType,
                Blocks:  []*Block{entry},
                Entry:   entry,
                Public:  pub,
                Extern:  ext,
        }
        return f
}

// NewParam adds a function parameter and returns the corresponding Value.
// The ID should come from the module-level register counter.
func (f *Func) NewParam(id int, typ ValType, name string) *Value {
        v := &Value{ID: id, Type: typ, Name: name}
        f.Params = append(f.Params, v)
        return v
}

// AddBlock appends a new block and returns it.
func (f *Func) AddBlock(label string) *Block {
        b := NewBlock(label)
        f.Blocks = append(f.Blocks, b)
        return b
}

// LookupBlock finds a block by label.  Returns nil if not found.
func (f *Func) LookupBlock(label string) *Block {
        for _, b := range f.Blocks {
                if b.Label == label {
                        return b
                }
        }
        return nil
}

// BlockCount returns the number of basic blocks.
func (f *Func) BlockCount() int {
        return len(f.Blocks)
}

// ParamCount returns the number of function parameters.
func (f *Func) ParamCount() int {
        return len(f.Params)
}

// String returns a debug dump of the function.
func (f *Func) String() string {
        var sb strings.Builder

        vis := "fn "
        if f.Public {
                vis = "pub fn "
        }
        if f.Extern {
                vis = "extern fn "
        }

        sb.WriteString(vis)
        sb.WriteString(f.Name)
        sb.WriteString("(")
        for i, p := range f.Params {
                if i > 0 {
                        sb.WriteString(", ")
                }
                sb.WriteString(p.Type.String())
                sb.WriteString(" ")
                sb.WriteString(p.String())
        }
        sb.WriteString(")")
        if f.RetType != VVoid {
                sb.WriteString(" -> ")
                sb.WriteString(f.RetType.String())
        }
        sb.WriteString(":\n")

        for _, b := range f.Blocks {
                sb.WriteString(b.String())
        }
        return sb.String()
}

// ---------------------------------------------------------------------------
// Runtime symbol reference
// ---------------------------------------------------------------------------

// RuntimeSym describes a symbol provided by the Yilt runtime library.
// Backends must emit relocations for these at link time.
type RuntimeSym struct {
        Name string   // mangled symbol name
        Type ValType  // type of the symbol's value
        Desc string   // human-readable description
}

// String returns the symbol name.
func (s RuntimeSym) String() string { return s.Name }

// ---------------------------------------------------------------------------
// Module
// ---------------------------------------------------------------------------

// Module is the top-level IR container.  It holds all compiled functions
// and runtime symbol references.
type Module struct {
        // Funcs maps function names to their IR definitions.
        Funcs map[string]*Func

        // RuntimeSyms lists runtime library symbols that the code references.
        RuntimeSyms []RuntimeSym

        // nextRegID is the monotonically increasing register counter.
        nextRegID int
}

// NewModule creates an empty IR module.
func NewModule() *Module {
        return &Module{
                Funcs: make(map[string]*Func),
        }
}

// AddFunc inserts a function into the module and returns it.
func (m *Module) AddFunc(name string, retType ValType, pub, ext bool) *Func {
        f := NewFunc(name, retType, pub, ext)
        // Ensure parameter values use the module's register namespace.
        for _, p := range f.Params {
                p.ID = m.allocID()
        }
        m.Funcs[name] = f
        return f
}

// LookupFunc finds a function by name.
func (m *Module) LookupFunc(name string) *Func {
        return m.Funcs[name]
}

// AddRuntimeSym registers a runtime symbol reference.
func (m *Module) AddRuntimeSym(name string, typ ValType, desc string) {
        m.RuntimeSyms = append(m.RuntimeSyms, RuntimeSym{
                Name: name,
                Type: typ,
                Desc: desc,
        })
}

// allocID returns the next available register ID.
func (m *Module) allocID() int {
        id := m.nextRegID
        m.nextRegID++
        return id
}

// AllocID allocates a globally unique register ID.
func (m *Module) AllocID() int {
        return m.allocID()
}

// FuncCount returns the number of functions in the module.
func (m *Module) FuncCount() int { return len(m.Funcs) }

// String returns a debug dump of the entire module.
func (m *Module) String() string {
        var sb strings.Builder
        sb.WriteString("--- module ---\n")
        for _, f := range m.Funcs {
                sb.WriteString(f.String())
                sb.WriteString("\n")
        }
        if len(m.RuntimeSyms) > 0 {
                sb.WriteString("--- runtime symbols ---\n")
                for _, s := range m.RuntimeSyms {
                        sb.WriteString(fmt.Sprintf("  %s : %s  (%s)\n", s.Name, s.Type, s.Desc))
                }
        }
        return sb.String()
}

// ---------------------------------------------------------------------------
// Builder — fluent API for constructing IR
// ---------------------------------------------------------------------------

// Builder provides a stateful, fluent interface for emitting IR instructions
// into a function.  The builder tracks the current insertion block and a
// module-scoped register counter.
type Builder struct {
        mod    *Module
        fn     *Func
        cur    *Block
        nextID int // local counter, synced with the module counter
}

// NewBuilder creates a builder targeting the given function.
func NewBuilder(m *Module, f *Func) *Builder {
        b := &Builder{
                mod:    m,
                fn:     f,
                cur:    f.Entry,
                nextID: m.nextRegID,
        }
        return b
}

// SetBlock switches the insertion point to the given block.
func (b *Builder) SetBlock(blk *Block) {
        b.cur = blk
}

// CurBlock returns the current insertion block.
func (b *Builder) CurBlock() *Block {
        return b.cur
}

// allocReg creates a new virtual register.
func (b *Builder) allocReg(typ ValType, name string) *Value {
        v := &Value{ID: b.nextID, Type: typ, Name: name}
        b.nextID++
        // Keep the module-level counter in sync so that other builders see
        // globally unique IDs.
        b.mod.nextRegID = b.nextID
        return v
}

// syncModule ensures the module's nextRegID is at least as large as ours.
func (b *Builder) syncModule() {
        if b.nextID > b.mod.nextRegID {
                b.mod.nextRegID = b.nextID
        }
}

// SyncFromModule updates the builder's local register counter to match
// the module's.  This must be called after another builder (e.g., one
// created for an anonymous function) has advanced the module counter,
// to prevent ID collisions.
func (b *Builder) SyncFromModule() {
        if b.mod.nextRegID > b.nextID {
                b.nextID = b.mod.nextRegID
        }
}

// ---------------------------------------------------------------------------
// Constant emission
// ---------------------------------------------------------------------------

// ConstInt emits an integer constant.
func (b *Builder) ConstInt(val int64, name string) *Value {
        v := b.allocReg(VInt, name)
        v.Const = &ConstVal{Kind: VInt, IntVal: val}
        b.cur.AddInstr(&Instr{Op: OpConstInt, Dest: v})
        return v
}

// ConstUint emits an unsigned integer constant.
func (b *Builder) ConstUint(val uint64, name string) *Value {
        v := b.allocReg(VUint, name)
        v.Const = &ConstVal{Kind: VUint, UintVal: val}
        b.cur.AddInstr(&Instr{Op: OpConstUint, Dest: v})
        return v
}

// ConstFp emits a floating-point constant.
func (b *Builder) ConstFp(val float64, name string) *Value {
        v := b.allocReg(VFp, name)
        v.Const = &ConstVal{Kind: VFp, FpVal: val}
        b.cur.AddInstr(&Instr{Op: OpConstFp, Dest: v})
        return v
}

// ConstRawInt emits a raw (untagged) integer constant.
// Used for passing raw lengths and sizes to runtime functions.
func (b *Builder) ConstRawInt(val int64, name string) *Value {
        v := b.allocReg(VRaw, name)
        v.Const = &ConstVal{Kind: VInt, IntVal: val}
        b.cur.AddInstr(&Instr{Op: OpConstRawInt, Dest: v})
        return v
}

// ConstStr emits a string constant.
func (b *Builder) ConstStr(val string, name string) *Value {
        v := b.allocReg(VStr, name)
        v.Const = &ConstVal{Kind: VStr, StrVal: val}
        b.cur.AddInstr(&Instr{Op: OpConstStr, Dest: v})
        return v
}

// ConstTaggedStr emits a pre-tagged string constant: a StrHeader+data blob
// stored in .rodata, loaded as a tagged value (TAG_STR | pointer).
// Unlike ConstStr + y_str_new, this requires NO runtime allocation —
// the string is baked into the binary at compile time.
func (b *Builder) ConstTaggedStr(val string, name string) *Value {
        v := b.allocReg(VTagged, name)
        v.Const = &ConstVal{Kind: VStr, StrVal: val}
        b.cur.AddInstr(&Instr{Op: OpConstTaggedStr, Dest: v})
        return v
}

// ConstBool emits a boolean constant.
func (b *Builder) ConstBool(val bool, name string) *Value {
        v := b.allocReg(VBool, name)
        v.Const = &ConstVal{Kind: VBool, BoolVal: val}
        b.cur.AddInstr(&Instr{Op: OpConstBool, Dest: v})
        return v
}

// ConstNil emits a nil / void constant.
func (b *Builder) ConstNil(name string) *Value {
        v := b.allocReg(VVoid, name)
        v.Const = &ConstVal{Kind: VVoid}
        b.cur.AddInstr(&Instr{Op: OpConstNil, Dest: v})
        return v
}

// ---------------------------------------------------------------------------
// Arithmetic
// ---------------------------------------------------------------------------

// Add emits lhs + rhs.
func (b *Builder) Add(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpAdd, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Sub emits lhs - rhs.
func (b *Builder) Sub(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpSub, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Mul emits lhs * rhs.
func (b *Builder) Mul(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpMul, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Div emits lhs / rhs.
func (b *Builder) Div(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpDiv, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Mod emits lhs % rhs.
func (b *Builder) Mod(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpMod, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// ---------------------------------------------------------------------------
// Bitwise
// ---------------------------------------------------------------------------

// And emits lhs & rhs (bitwise).
func (b *Builder) And(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpAnd, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Or emits lhs | rhs (bitwise).
func (b *Builder) Or(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpOr, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Xor emits lhs ^ rhs.
func (b *Builder) Xor(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpXor, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Shl emits lhs << rhs.
func (b *Builder) Shl(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpShl, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Shr emits lhs >> rhs (arithmetic or logical depending on type).
func (b *Builder) Shr(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(lhs.Type, name)
        b.cur.AddInstr(&Instr{Op: OpShr, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// ---------------------------------------------------------------------------
// Unary
// ---------------------------------------------------------------------------

// Neg emits -operand.
func (b *Builder) Neg(operand *Value, name string) *Value {
        v := b.allocReg(operand.Type, name)
        b.cur.AddInstr(&Instr{Op: OpNeg, Dest: v, Src: []*Value{operand}})
        return v
}

// Not emits logical not (operand must be bool).
func (b *Builder) Not(operand *Value, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{Op: OpNot, Dest: v, Src: []*Value{operand}})
        return v
}

// BitNot emits ~operand.
func (b *Builder) BitNot(operand *Value, name string) *Value {
        v := b.allocReg(operand.Type, name)
        b.cur.AddInstr(&Instr{Op: OpBitNot, Dest: v, Src: []*Value{operand}})
        return v
}

// ---------------------------------------------------------------------------
// Comparison
// ---------------------------------------------------------------------------

// Eq emits lhs == rhs, producing a bool.
func (b *Builder) Eq(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{Op: OpEq, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Neq emits lhs != rhs, producing a bool.
func (b *Builder) Neq(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{Op: OpNeq, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Lt emits lhs < rhs, producing a bool.
func (b *Builder) Lt(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{Op: OpLt, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Le emits lhs <= rhs, producing a bool.
func (b *Builder) Le(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{Op: OpLe, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Gt emits lhs > rhs, producing a bool.
func (b *Builder) Gt(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{Op: OpGt, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// Ge emits lhs >= rhs, producing a bool.
func (b *Builder) Ge(lhs, rhs *Value, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{Op: OpGe, Dest: v, Src: []*Value{lhs, rhs}})
        return v
}

// ---------------------------------------------------------------------------
// Call / Return
// ---------------------------------------------------------------------------

// Call emits a direct function call.  retType is the type of the return
// value; use VVoid for procedures.
func (b *Builder) Call(fnName string, args []*Value, retType ValType, name string) *Value {
        v := b.allocReg(retType, name)
        b.cur.AddInstr(&Instr{
                Op:   OpCall,
                Dest: v,
                Src:  args,
                Meta: &InstrMeta{FnName: fnName},
        })
        return v
}

// CallIndirect emits an indirect call through a function pointer.
func (b *Builder) CallIndirect(fnPtr *Value, args []*Value, retType ValType, name string) *Value {
        v := b.allocReg(retType, name)
        srcs := make([]*Value, 0, len(args)+1)
        srcs = append(srcs, fnPtr)
        srcs = append(srcs, args...)
        b.cur.AddInstr(&Instr{
                Op:   OpCallIndirect,
                Dest: v,
                Src:  srcs,
        })
        return v
}

// Return emits a return instruction.  val may be nil for void returns.
func (b *Builder) Return(val *Value) {
        var src []*Value
        if val != nil {
                src = []*Value{val}
        }
        b.cur.AddInstr(&Instr{Op: OpReturn, Src: src})
}

// ---------------------------------------------------------------------------
// Control flow
// ---------------------------------------------------------------------------

// Jump emits an unconditional jump to target, passing args as the block's
// parameter values.  The number of args must match target.ParamCount().
func (b *Builder) Jump(target *Block, args []*Value) {
        b.cur.AddInstr(&Instr{
                Op: OpJump,
                Meta: &InstrMeta{
                        Jump: &BranchTarget{Block: target, Args: args},
                },
        })
}

// Branch emits a conditional branch.  If cond is true, control transfers to
// thenBlock with thenArgs; otherwise to elseBlock with elseArgs.
func (b *Builder) Branch(cond *Value, thenBlock, elseBlock *Block, thenArgs, elseArgs []*Value) {
        b.cur.AddInstr(&Instr{
                Op:  OpBranch,
                Src: []*Value{cond},
                Meta: &InstrMeta{
                        Then: &BranchTarget{Block: thenBlock, Args: thenArgs},
                        Else: &BranchTarget{Block: elseBlock, Args: elseArgs},
                },
        })
}

// ---------------------------------------------------------------------------
// Memory operations
// ---------------------------------------------------------------------------

// Load emits a load from a pointer address, producing a value of the given type.
func (b *Builder) Load(addr *Value, typ ValType, name string) *Value {
        v := b.allocReg(typ, name)
        b.cur.AddInstr(&Instr{Op: OpLoad, Dest: v, Src: []*Value{addr}})
        return v
}

// Store emits a store of val to the pointer address.
func (b *Builder) Store(addr, val *Value) {
        b.cur.AddInstr(&Instr{Op: OpStore, Src: []*Value{addr, val}})
}

// Alloc emits a heap allocation of size bytes, returning a pointer.
func (b *Builder) Alloc(size int, name string) *Value {
        v := b.allocReg(VRaw, name)
        b.cur.AddInstr(&Instr{
                Op:   OpAlloc,
                Dest: v,
                Meta: &InstrMeta{Size: size},
        })
        return v
}

// StackAlloc emits a stack (arena) allocation of size bytes.
func (b *Builder) StackAlloc(size int, name string) *Value {
        v := b.allocReg(VRaw, name)
        b.cur.AddInstr(&Instr{
                Op:   OpStackAlloc,
                Dest: v,
                Meta: &InstrMeta{Size: size},
        })
        return v
}

// ---------------------------------------------------------------------------
// Table operations
// ---------------------------------------------------------------------------

// TableNew emits a table creation with the given initial capacity.
func (b *Builder) TableNew(cap int, name string) *Value {
        v := b.allocReg(VTable, name)
        b.cur.AddInstr(&Instr{
                Op:   OpTableNew,
                Dest: v,
                Meta: &InstrMeta{Capacity: cap},
        })
        return v
}

// TableGet emits a table read: table[key].
func (b *Builder) TableGet(table, key *Value, name string) *Value {
        v := b.allocReg(VTagged, name)
        b.cur.AddInstr(&Instr{Op: OpTableGet, Dest: v, Src: []*Value{table, key}})
        return v
}

// TableSet emits a table write: table[key] = val.
func (b *Builder) TableSet(table, key, val *Value) {
        b.cur.AddInstr(&Instr{Op: OpTableSet, Src: []*Value{table, key, val}})
}

// TableLen emits a table length query.
func (b *Builder) TableLen(table *Value, name string) *Value {
        v := b.allocReg(VInt, name)
        b.cur.AddInstr(&Instr{Op: OpTableLen, Dest: v, Src: []*Value{table}})
        return v
}

// TableDelete emits a table entry deletion.
func (b *Builder) TableDelete(table, key *Value) {
        b.cur.AddInstr(&Instr{Op: OpTableDelete, Src: []*Value{table, key}})
}

// ---------------------------------------------------------------------------
// Index / member access
// ---------------------------------------------------------------------------

// IndexGet emits an index read: obj[key].
func (b *Builder) IndexGet(obj, key *Value, name string) *Value {
        v := b.allocReg(VTagged, name)
        b.cur.AddInstr(&Instr{Op: OpIndexGet, Dest: v, Src: []*Value{obj, key}})
        return v
}

// IndexSet emits an index write: obj[key] = val.
func (b *Builder) IndexSet(obj, key, val *Value) {
        b.cur.AddInstr(&Instr{Op: OpIndexSet, Src: []*Value{obj, key, val}})
}

// MemberGet emits a member read: obj.field.
func (b *Builder) MemberGet(obj *Value, field string, name string) *Value {
        v := b.allocReg(VTagged, name)
        b.cur.AddInstr(&Instr{
                Op:   OpMemberGet,
                Dest: v,
                Src:  []*Value{obj},
                Meta: &InstrMeta{Field: field},
        })
        return v
}

// ---------------------------------------------------------------------------
// Tagged value operations
// ---------------------------------------------------------------------------

// Tag emits a tag operation: wraps a raw value with the given type tag to
// produce a tagged 64-bit Yilt value.
func (b *Builder) Tag(val *Value, tag uint8, name string) *Value {
        v := b.allocReg(VTagged, name)
        b.cur.AddInstr(&Instr{
                Op:   OpTag,
                Dest: v,
                Src:  []*Value{val},
                Meta: &InstrMeta{Tag: tag},
        })
        return v
}

// Untag extracts the raw payload from a tagged value.
func (b *Builder) Untag(tagged *Value, rawType ValType, name string) *Value {
        v := b.allocReg(rawType, name)
        b.cur.AddInstr(&Instr{
                Op:   OpUntag,
                Dest: v,
                Src:  []*Value{tagged},
        })
        return v
}

// CheckTag tests whether a tagged value has the expected type tag, producing
// a boolean result.
func (b *Builder) CheckTag(tagged *Value, expected uint8, name string) *Value {
        v := b.allocReg(VBool, name)
        b.cur.AddInstr(&Instr{
                Op:   OpCheckTag,
                Dest: v,
                Src:  []*Value{tagged},
                Meta: &InstrMeta{Tag: expected},
        })
        return v
}

// ---------------------------------------------------------------------------
// Arena / GC operations
// ---------------------------------------------------------------------------

// ArenaPush pushes a new GC arena with the given capacity (in bytes).
func (b *Builder) ArenaPush(size int) {
        b.cur.AddInstr(&Instr{
                Op:   OpArenaPush,
                Meta: &InstrMeta{Size: size},
        })
}

// ArenaPop pops the current GC arena, releasing all allocations within it.
func (b *Builder) ArenaPop() {
        b.cur.AddInstr(&Instr{Op: OpArenaPop})
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

// Spawn emits a task spawn, calling fnName with args.  Returns a handle
// that can be passed to Await.
func (b *Builder) Spawn(fnName string, args []*Value, name string) *Value {
        v := b.allocReg(VHandle, name)
        b.cur.AddInstr(&Instr{
                Op:   OpSpawn,
                Dest: v,
                Src:  args,
                Meta: &InstrMeta{FnName: fnName},
        })
        return v
}

// Await waits for a spawned task to complete and returns its result.
func (b *Builder) Await(handle *Value, name string) *Value {
        v := b.allocReg(VTagged, name)
        b.cur.AddInstr(&Instr{
                Op:   OpAwait,
                Dest: v,
                Src:  []*Value{handle},
        })
        return v
}

// ---------------------------------------------------------------------------
// Misc
// ---------------------------------------------------------------------------

// Panic emits an unconditional abort with a message.
func (b *Builder) Panic(msg string) {
        b.cur.AddInstr(&Instr{
                Op:   OpPanic,
                Meta: &InstrMeta{Message: msg},
        })
}

// Nop emits a no-operation instruction (useful as a placeholder).
func (b *Builder) Nop() {
        b.cur.AddInstr(&Instr{Op: OpNop})
}

// Copy emits a copy instruction that reads Src[0] and writes the result to
// Dest's slot.  This is used for mutable variable assignments: the source
// is the new value and Dest is the original mutable variable's value, so
// the slot gets updated in place.
func (b *Builder) Copy(src, dst *Value) {
        b.cur.AddInstr(&Instr{Op: OpCopy, Dest: dst, Src: []*Value{src}})
}

// Param emits a block-parameter pseudo-instruction at the start of the
// current block.  Normally block parameters are added via Block.AddParam,
// but this instruction form allows the builder to reference them by name.
func (b *Builder) Param(typ ValType, name string) *Value {
        v := b.allocReg(typ, name)
        b.cur.AddInstr(&Instr{Op: OpParam, Dest: v})
        return v
}

// ---------------------------------------------------------------------------
// Predefined runtime symbols
// ---------------------------------------------------------------------------

// StdRuntimeSyms returns the set of runtime symbols that every Yilt module
// implicitly depends on.
func StdRuntimeSyms() []RuntimeSym {
        return []RuntimeSym{
                {Name: "yilt_alloc", Type: VRaw, Desc: "heap allocation"},
                {Name: "yilt_free", Type: VVoid, Desc: "heap deallocation"},
                {Name: "yilt_gc_init", Type: VVoid, Desc: "GC initialisation"},
                {Name: "yilt_gc_collect", Type: VVoid, Desc: "GC collection"},
                {Name: "yilt_panic", Type: VVoid, Desc: "abort with message"},
                {Name: "yilt_print", Type: VVoid, Desc: "print tagged value"},
                {Name: "yilt_println", Type: VVoid, Desc: "print tagged value + newline"},
                {Name: "yilt_string_concat", Type: VStr, Desc: "string concatenation"},
                {Name: "yilt_string_len", Type: VInt, Desc: "string byte length"},
                {Name: "yilt_table_new", Type: VTable, Desc: "create table"},
                {Name: "yilt_table_get", Type: VTagged, Desc: "table read"},
                {Name: "yilt_table_set", Type: VVoid, Desc: "table write"},
                {Name: "yilt_table_len", Type: VInt, Desc: "table length"},
                {Name: "yilt_table_delete", Type: VVoid, Desc: "table entry delete"},
                {Name: "yilt_spawn", Type: VHandle, Desc: "spawn task"},
                {Name: "yilt_await", Type: VTagged, Desc: "await task result"},
                {Name: "yilt_arena_push", Type: VRaw, Desc: "push GC arena"},
                {Name: "yilt_arena_pop", Type: VVoid, Desc: "pop GC arena"},
                {Name: "yilt_error_new", Type: VTagged, Desc: "create error value"},
                {Name: "yilt_error_check", Type: VBool, Desc: "check if value is error"},
        }
}
