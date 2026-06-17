package x86_64

import (
        "fmt"
        "math"
        "sort"
        "strings"
)

// ======================================================================
// Intermediate Representation (IR)
// ======================================================================

// IROp enumerates all IR operations that the code generator understands.
type IROp int

const (
        IRInvalid IROp = iota

        // Constants
        IRConstInt     // integer constant
        IRConstFloat   // float constant
        IRConstBool    // boolean constant
        IRConstString  // string constant (referenced by index)
        IRConstNil     // nil value

        // Arithmetic
        IRAdd  // integer add
        IRSub  // integer subtract
        IRMul  // integer multiply (signed)
        IRDiv  // integer divide (signed)
        IRMod  // integer modulo (signed)
        IRNeg  // integer negate
        IRFAdd // float add
        IRFSub // float subtract
        IRFMul // float multiply
        IRFDiv // float divide
        IRFNeg // float negate

        // Bitwise
        IRAnd  // bitwise and
        IROr   // bitwise or
        IRXor  // bitwise xor
        IRNot  // bitwise not
        IRShl  // shift left
        IRShr  // logical shift right
        IRSar  // arithmetic shift right

        // Comparison
        IREq  // equal
        IRNe  // not equal
        IRLt  // less than (signed for int, unordered for float)
        IRGt  // greater than
        IRLe  // less or equal
        IRGe  // greater or equal

        // Tagged value operations
        IRTagInt   // tag an integer value: (value << 8) | INT_TAG
        IRUntagInt // untag an integer: value >> 8
        IRTagBool  // tag a boolean: (value << 8) | BOOL_TAG
        IRUntagBool
        IRIsTag    // check if tag matches: (value >> 56) == tag
        IRGetTag   // extract tag byte: value >> 56
        IRGetVal   // extract value bits: value & 0x00FFFFFFFFFFFFFF
        IRSetTag   // set tag: (value & 0x00FFFFFFFFFFFFFF) | (tag << 56)
        IRWrapTag  // generic: set tag on value

        // Memory / Variables
        IRLoad    // load from local variable slot
        IRStore   // store to local variable slot
        IRLoadGlobal // load from global
        IRStoreGlobal

        // Stack / Spill
        IRSpill   // spill value to stack
        IRReload  // reload value from stack

        // Control flow
        IRJump    // unconditional jump
        IRCondJmp // conditional jump
        IRLabel   // label definition
        IRRet     // return (with optional value)
        IRCall    // function call
        IRCallInd // indirect function call

        // Conversions
        IRIntToF32 // integer to float32
        IRIntToF64 // integer to float64
        IRF32ToInt // float32 to integer (trunc)
        IRF64ToInt // float64 to integer (trunc)
        IRF32ToF64 // float32 to float64
        IRF64ToF32 // float64 to float32

        // Address computation
        IRAddrOf   // address of a local/global
        IRGetElementPtr // array/table element pointer

        // Table operations (delegated to runtime)
        IRTableNew   // create new table
        IRTableGet   // table[key]
        IRTableSet   // table[key] = value
        IRTableLen   // table length

        // String operations (delegated to runtime)
        IRStrNew    // create string from data
        IRStrLen    // string length
        IRStrCat    // string concatenation
        IRStrEq     // string equality
        IRStrSlice  // string slice

        // Error handling
        IRMakeErr // wrap value as error
        IRIsErr   // check if value is an error
        IRUnwrapErr // unwrap error (propagate or return)

        // Arena
        IRArenaPush  // push arena scope
        IRArenaPop   // pop arena scope
        IRArenaAlloc // allocate from arena

        // Misc
        IRNop
        IRUnreachable
        IRPhi // phi node (handled during SSA lowering)
)

func (op IROp) String() string {
        switch op {
        case IRConstInt:
                return "ConstInt"
        case IRConstFloat:
                return "ConstFloat"
        case IRConstBool:
                return "ConstBool"
        case IRConstString:
                return "ConstString"
        case IRConstNil:
                return "ConstNil"
        case IRAdd:
                return "Add"
        case IRSub:
                return "Sub"
        case IRMul:
                return "Mul"
        case IRDiv:
                return "Div"
        case IRMod:
                return "Mod"
        case IRNeg:
                return "Neg"
        case IRFAdd:
                return "FAdd"
        case IRFSub:
                return "FSub"
        case IRFMul:
                return "FMul"
        case IRFDiv:
                return "FDiv"
        case IRFNeg:
                return "FNeg"
        case IRAnd:
                return "And"
        case IROr:
                return "Or"
        case IRXor:
                return "Xor"
        case IRNot:
                return "Not"
        case IRShl:
                return "Shl"
        case IRShr:
                return "Shr"
        case IRSar:
                return "Sar"
        case IREq:
                return "Eq"
        case IRNe:
                return "Ne"
        case IRLt:
                return "Lt"
        case IRGt:
                return "Gt"
        case IRLe:
                return "Le"
        case IRGe:
                return "Ge"
        case IRTagInt:
                return "TagInt"
        case IRTagBool:
                return "TagBool"
        case IRIsTag:
                return "IsTag"
        case IRGetTag:
                return "GetTag"
        case IRGetVal:
                return "GetVal"
        case IRSetTag:
                return "SetTag"
        case IRLoad:
                return "Load"
        case IRStore:
                return "Store"
        case IRSpill:
                return "Spill"
        case IRReload:
                return "Reload"
        case IRJump:
                return "Jump"
        case IRCondJmp:
                return "CondJmp"
        case IRLabel:
                return "Label"
        case IRRet:
                return "Ret"
        case IRCall:
                return "Call"
        case IRCallInd:
                return "CallInd"
        case IRIntToF32:
                return "IntToF32"
        case IRIntToF64:
                return "IntToF64"
        case IRF32ToInt:
                return "F32ToInt"
        case IRF64ToInt:
                return "F64ToInt"
        case IRF32ToF64:
                return "F32ToF64"
        case IRF64ToF32:
                return "F64ToF32"
        case IRTableNew:
                return "TableNew"
        case IRTableGet:
                return "TableGet"
        case IRTableSet:
                return "TableSet"
        case IRTableLen:
                return "TableLen"
        case IRStrNew:
                return "StrNew"
        case IRStrLen:
                return "StrLen"
        case IRStrCat:
                return "StrCat"
        case IRStrEq:
                return "StrEq"
        case IRMakeErr:
                return "MakeErr"
        case IRIsErr:
                return "IsErr"
        case IRUnwrapErr:
                return "UnwrapErr"
        case IRArenaPush:
                return "ArenaPush"
        case IRArenaPop:
                return "ArenaPop"
        case IRArenaAlloc:
                return "ArenaAlloc"
        case IRNop:
                return "Nop"
        case IRUnreachable:
                return "Unreachable"
        default:
                return fmt.Sprintf("IR(%d)", op)
        }
}

// IRValKind describes what kind of value an IR operand holds.
type IRValKind int

const (
        IRValNone IRValKind = iota
        IRValReg        // virtual register
        IRValInt        // integer constant
        IRValFloat      // float constant
        IRValString     // string constant index
        IRValLabel      // label reference
        IRValSlot       // stack slot index
        IRValFunc       // function name/index
        IRValGlobal     // global variable name/index
)

// IRVal is an IR operand value.
type IRVal struct {
        Kind  IRValKind
        Reg   VReg
        Int   int64
        Float float64
        Str   string // function name, label, or global name
        Slot  int    // stack slot or local variable index
}

// RegVal creates an IRVal holding a virtual register.
func RegVal(v VReg) IRVal {
        return IRVal{Kind: IRValReg, Reg: v}
}

// IntVal creates an IRVal holding an integer constant.
func IntVal(v int64) IRVal {
        return IRVal{Kind: IRValInt, Int: v}
}

// FloatVal creates an IRVal holding a float constant.
func FloatVal(v float64) IRVal {
        return IRVal{Kind: IRValFloat, Float: v}
}

// LabelVal creates an IRVal holding a label reference.
func LabelVal(name string) IRVal {
        return IRVal{Kind: IRValLabel, Str: name}
}

// SlotVal creates an IRVal holding a stack slot index.
func SlotVal(idx int) IRVal {
        return IRVal{Kind: IRValSlot, Slot: idx}
}

// FuncVal creates an IRVal holding a function reference.
func FuncVal(name string) IRVal {
        return IRVal{Kind: IRValFunc, Str: name}
}

// GlobalVal creates an IRVal holding a global variable reference.
func GlobalVal(name string) IRVal {
        return IRVal{Kind: IRValGlobal, Str: name}
}

// IRInstr is a single IR instruction.
type IRInstr struct {
        Op   IROp
        Dst  VReg    // destination virtual register (NoVReg if none)
        Src  [3]IRVal // source operands
}

// String returns a human-readable representation of the instruction.
func (i IRInstr) String() string {
        var sb strings.Builder
        sb.WriteString(i.Op.String())
        if i.Dst != NoVReg {
                sb.WriteString(fmt.Sprintf(" v%d", i.Dst))
        }
        for j := 0; j < 3; j++ {
                s := i.Src[j]
                if s.Kind == IRValNone {
                        break
                }
                sb.WriteString(" ")
                switch s.Kind {
                case IRValReg:
                        sb.WriteString(fmt.Sprintf("v%d", s.Reg))
                case IRValInt:
                        sb.WriteString(fmt.Sprintf("%d", s.Int))
                case IRValFloat:
                        sb.WriteString(fmt.Sprintf("%f", s.Float))
                case IRValLabel:
                        sb.WriteString(s.Str)
                case IRValSlot:
                        sb.WriteString(fmt.Sprintf("[slot%d]", s.Slot))
                case IRValFunc:
                        sb.WriteString(fmt.Sprintf("@%s", s.Str))
                case IRValGlobal:
                        sb.WriteString(fmt.Sprintf("$%s", s.Str))
                }
        }
        return sb.String()
}

// IRFunc represents a compiled function in IR form.
type IRFunc struct {
        Name       string
        Params     []IRParam
        Returns    []IRType
        Locals     int          // number of local variable slots
        Instructions []IRInstr
        HasVAArgs  bool         // variable arguments
}

// IRParam is a function parameter in IR.
type IRParam struct {
        Name string
        Type IRType
        VReg VReg // virtual register holding the parameter
}

// IRType represents an IR type.
type IRType int

const (
        IRTypeVoid IRType = iota
        IRTypeI64
        IRTypeI32
        IRTypeF64
        IRTypeF32
        IRTypeBool
        IRTypePtr
        IRTypeTagged // tagged 64-bit value
        IRTypeStr
        IRTypeTable
        IRTypeErr
)

func (t IRType) String() string {
        switch t {
        case IRTypeVoid:
                return "void"
        case IRTypeI64:
                return "i64"
        case IRTypeI32:
                return "i32"
        case IRTypeF64:
                return "f64"
        case IRTypeF32:
                return "f32"
        case IRTypeBool:
                return "bool"
        case IRTypePtr:
                return "ptr"
        case IRTypeTagged:
                return "tagged"
        case IRTypeStr:
                return "str"
        case IRTypeTable:
                return "table"
        case IRTypeErr:
                return "err"
        default:
                return "?"
        }
}

// IsFloat returns true for floating-point types.
func (t IRType) IsFloat() bool {
        return t == IRTypeF32 || t == IRTypeF64
}

// Size returns the size in bytes of the IR type.
func (t IRType) Size() int {
        switch t {
        case IRTypeVoid:
                return 0
        case IRTypeI64, IRTypeF64, IRTypePtr, IRTypeTagged, IRTypeStr, IRTypeTable, IRTypeErr:
                return 8
        case IRTypeI32, IRTypeF32, IRTypeBool:
                return 4
        default:
                return 8
        }
}

// IRModule is the full IR module containing all functions and globals.
type IRModule struct {
        Funcs      []*IRFunc
        Globals    []IRGlobal
        Strings    []string      // string constants table
        Floats     []float64     // float constants table
        DataSection []byte       // additional data section bytes
}

// IRGlobal is a global variable definition.
type IRGlobal struct {
        Name   string
        Type   IRType
        Init   IRVal // initial value
        Offset int   // offset in data section
}

// ======================================================================
// Tagged Value Encoding
// ======================================================================

// Yilt uses NaN-boxed / tagged 64-bit values.
// Layout: [TAG(8 bits)][VALUE(56 bits)]
// Tag is stored in the most significant byte (bits 56-63).
// Value occupies the lower 56 bits (bits 0-55).

const (
        TagNone  uint64 = 0x00
        TagInt   uint64 = 0x01
        TagBool  uint64 = 0x02
        TagFP    uint64 = 0x03
        TagStr   uint64 = 0x04
        TagTable uint64 = 0x05
        TagFunc  uint64 = 0x06
        TagHandle uint64 = 0x07
        TagNil   uint64 = 0x09
        TagErr   uint64 = 0x0E

        TagShift    = 56
        TagMask     = uint64(0xFF) << TagShift
        ValueMask   = uint64(0x00FFFFFFFFFFFFFF)

        TrueVal  uint64 = 1
        FalseVal uint64 = 0

        NilTaggedVal uint64 = TagNil << TagShift
)

// TagInt64 encodes a 56-bit integer as a tagged value.
func TagInt64(v int64) uint64 {
        return (uint64(v) & ValueMask) | (TagInt << TagShift)
}

// UntagInt extracts the integer value from a tagged integer.
func UntagInt(v uint64) int64 {
        return int64(v & ValueMask)
}

// TagBoolVal encodes a boolean as a tagged value.
func TagBoolVal(v bool) uint64 {
        if v {
                return TrueVal | (TagBool << TagShift)
        }
        return FalseVal | (TagBool << TagShift)
}

// UntagBool extracts the boolean from a tagged boolean.
func UntagBool(v uint64) bool {
        return (v & ValueMask) != 0
}

// GetTag extracts the tag byte from a tagged value.
func GetTag(v uint64) uint64 {
        return (v >> TagShift) & 0xFF
}

// IsTagged checks if a tagged value has the expected tag.
func IsTagged(v uint64, tag uint64) bool {
        return GetTag(v) == tag
}

// MakeTagged creates a tagged value with the given tag and raw value.
func MakeTagged(tag uint64, value uint64) uint64 {
        return (value & ValueMask) | (tag << TagShift)
}

// ======================================================================
// Code Generator
// ======================================================================

// CodeGen is the main x86-64 code generator. It takes an IR module and
// produces native machine code for each function.
type CodeGen struct {
        asm  *Asm
        abi  *ABIInfo
        module *IRModule
        target Target

        // Per-function state
        fn          *IRFunc
        frame       *FrameLayout
        regAlloc    *RegAlloc

        // Control flow
        loopStacks  []loopContext
        breakLabels []string
        continueLabels []string

        // String/float constant offsets (filled during emission)
        stringOffsets map[string]int
        floatOffsets  map[int]int // index -> offset

        // Output sections
        textSection []byte
        dataSection []byte
        relocations []Relocation
}

// Relocation represents a linker relocation.
type Relocation struct {
        Offset  int
        Symbol  string
        Type    string // "R_X86_64_PC32", "R_X86_64_PLT32", etc.
        Addend  int32
}

// loopContext tracks break/continue targets for nested loops.
type loopContext struct {
        breakLabel    string
        continueLabel string
        headerLabel   string
}

// NewCodeGen creates a new code generator for the given target.
func NewCodeGen(target Target) *CodeGen {
        abi := GetABIInfo(target)
        return &CodeGen{
                asm:     NewAsm(),
                abi:     abi,
                target:  target,
                stringOffsets: make(map[string]int),
                floatOffsets:  make(map[int]int),
        }
}

// Generate compiles the entire IR module into machine code.
// Returns the text (code) section bytes and data section bytes.
func (cg *CodeGen) Generate(module *IRModule) ([]byte, []byte, []Relocation) {
        cg.module = module

        // Generate data section (string constants, float constants)
        cg.genDataSection()

        // Generate code for each function
        for _, fn := range module.Funcs {
                if len(fn.Instructions) == 0 {
                        continue
                }
                cg.genFunction(fn)
        }

        return cg.textSection, cg.dataSection, cg.relocations
}

// GenerateFunc compiles a single IR function into machine code.
func (cg *CodeGen) GenerateFunc(fn *IRFunc) []byte {
        cg.genFunction(fn)
        return fnInstructions
}

// genDataSection emits string and float constants into the data section.
func (cg *CodeGen) genDataSection() {
        var data Asm
        data.code = make([]byte, 0, 1024)

        // Emit string constants: each string is prefixed by its length (8 bytes) followed by the bytes.
        for _, s := range cg.module.Strings {
                cg.stringOffsets[s] = len(data.code)
                data.EmitInt64(int64(len(s)))
                data.EmitBytes([]byte(s))
                // Align to 8 bytes
                padding := (8 - (len(s) % 8)) % 8
                data.EmitZeros(padding)
        }

        // Emit float constants (as 64-bit IEEE 754)
        for i, f := range cg.module.Floats {
                cg.floatOffsets[i] = len(data.code)
                data.EmitFloat64(f)
        }

        cg.dataSection = data.Bytes()
}

// genFunction compiles a single function from IR to x86-64 machine code.
func (cg *CodeGen) genFunction(fn *IRFunc) {
        // Create new assembler and register allocator for this function
        cg.asm = NewAsm()
        cg.fn = fn
        cg.regAlloc = NewRegAlloc(cg.abi)
        cg.loopStacks = nil
        cg.breakLabels = nil
        cg.continueLabels = nil

        // First pass: compute liveness and allocate registers
        cg.computeLiveness(fn)
        totalSpill := cg.regAlloc.SpillAreaSize()

        // Compute frame layout
        localSize := fn.Locals * 8 // 8 bytes per local slot
        numStackArgs := cg.numStackArgs(fn)
        cg.frame = cg.abi.ComputeFrameLayout(totalSpill, localSize, numStackArgs)

        // Emit function prologue
        cg.emitPrologue(fn)

        // Second pass: emit code for each instruction
        for idx := range fn.Instructions {
                cg.genInstr(&fn.Instructions[idx])
        }

        // If the last instruction wasn't a return, emit one
        if len(fn.Instructions) == 0 || fn.Instructions[len(fn.Instructions)-1].Op != IRRet {
                cg.emitEpilogue(fn)
        }

        // Resolve all label fixups
        for name := range cg.asm.Labels() {
                cg.asm.ResolveFixups(name)
        }

        // Append to text section
        code := cg.asm.Bytes()
        cg.textSection = append(cg.textSection, code...)
}

// fnInstructions is a package-level reference used for single-function compilation.
var fnInstructions []byte

// computeLiveness does a simple liveness analysis pass over the IR instructions
// to inform the register allocator about value lifetimes.
func (cg *CodeGen) computeLiveness(fn *IRFunc) {
        // Build a map of where each virtual register is defined and last used.
        defMap := make(map[VReg]int)
        useMap := make(map[VReg]int)

        for idx, ins := range fn.Instructions {
                // Track definitions
                if ins.Dst != NoVReg {
                        if _, ok := defMap[ins.Dst]; !ok {
                                defMap[ins.Dst] = idx
                        }
                }
                // Track uses
                for j := 0; j < 3; j++ {
                        if ins.Src[j].Kind == IRValReg && ins.Src[j].Reg != NoVReg {
                                useMap[ins.Src[j].Reg] = idx
                        }
                }
        }

        // Store the last-use information for the register allocator
        lastUse = useMap
}

// lastUse maps virtual registers to their last use instruction index.
var lastUse map[VReg]int // package-level to avoid struct field issues

// isLive returns true if the virtual register is still live at the given instruction index.
func (cg *CodeGen) isLive(v VReg, idx int) bool {
        if lu, ok := lastUse[v]; ok {
                return lu >= idx
        }
        return false
}

// numStackArgs counts how many arguments must be passed on the stack.
func (cg *CodeGen) numStackArgs(fn *IRFunc) int {
        n := len(fn.Params)
        switch cg.target.ABI {
        case ABIWindows:
                if n <= 4 {
                        return 0
                }
                return n - 4
        default: // System V
                if n <= 6 {
                        return 0
                }
                return n - 6
        }
}

// ======================================================================
// Prologue and Epilogue
// ======================================================================

// emitPrologue generates the function prologue.
func (cg *CodeGen) emitPrologue(fn *IRFunc) {
        a := cg.asm
        frame := cg.frame

        // Determine which callee-saved registers we actually use
        usedCalleeSaved := cg.usedCalleeSavedRegs(fn)

        // Push RBP (frame pointer)
        a.PUSH(RBP)

        // MOV RBP, RSP
        a.MovRR(RBP, RSP)

        // Push callee-saved registers we use
        for _, r := range usedCalleeSaved {
                a.PUSH(r)
        }

        // Reserve stack space for locals and spill area
        // SUB RSP, frameSize - 8 (for RBP push) - len(usedCalleeSaved)*8
        totalPush := 8 + len(usedCalleeSaved)*8
        adjust := frame.FrameSize - totalPush
        if adjust > 0 {
                // Align to 16 bytes
                adjust = (adjust + 15) & ^15
                if adjust > 0 {
                        a.SubRI(RSP, int64(adjust))
                }
        } else if adjust < 0 {
                // We might need to add to RSP if frame size is smaller than pushes
                // This shouldn't normally happen, but handle it
                a.AddRI(RSP, int64(-adjust))
        }

        // Store incoming arguments to their local slots
        cg.storeIncomingArgs(fn, usedCalleeSaved)

        // Zero-initialize local variables (if requested by IR)
        for i := 0; i < fn.Locals; i++ {
                offset := -(8 + len(usedCalleeSaved)*8 + adjust + i*8)
                a.MovRMMem(MemBase(RBP, int32(offset)), RAX) // MOV [rbp-offset], rax (already 0)
        }
}

// emitEpilogue generates the function epilogue (return sequence).
func (cg *CodeGen) emitEpilogue(fn *IRFunc) {
        a := cg.asm
        usedCalleeSaved := cg.usedCalleeSavedRegs(fn)

        // Determine frame adjustment
        totalPush := 8 + len(usedCalleeSaved)*8
        adjust := cg.frame.FrameSize - totalPush
        if adjust > 0 {
                adjust = (adjust + 15) & ^15
        }

        // Restore stack pointer
        if adjust > 0 {
                a.AddRI(RSP, int64(adjust))
        }

        // Pop callee-saved registers (in reverse order)
        for i := len(usedCalleeSaved) - 1; i >= 0; i-- {
                a.POP(usedCalleeSaved[i])
        }

        // POP RBP
        a.POP(RBP)

        // RET
        a.RET()
}

// storeIncomingArgs copies incoming register arguments to their stack slots.
func (cg *CodeGen) storeIncomingArgs(fn *IRFunc, usedCalleeSaved []Reg) {
        a := cg.asm
        abi := cg.abi

        totalPush := 8 + len(usedCalleeSaved)*8
        adjust := cg.frame.FrameSize - totalPush
        if adjust > 0 {
                adjust = (adjust + 15) & ^15
        }

        for i, p := range fn.Params {
                // Determine which register holds this argument
                isFloat := p.Type.IsFloat()
                var argReg *Reg
                if isFloat {
                        argReg = abi.FloatArgReg(i)
                } else {
                        argReg = abi.IntArgReg(i)
                }

                if argReg != nil {
                        // Register argument: store to local slot
                        // Store to slot index = i
                        slotOffset := -(totalPush + adjust + i*8)
                        if isFloat {
                                a.MovSD_MR(MemOp(MemBase(RBP, int32(slotOffset))), *argReg)
                        } else {
                                a.MovRMMem(MemBase(RBP, int32(slotOffset)), *argReg)
                        }
                }
                // Stack arguments are already on the stack at known offsets
        }
}

// usedCalleeSavedRegs determines which callee-saved registers are used by the function.
// For now, we conservatively push R15 (arena register) always, and RBX if needed.
func (cg *CodeGen) usedCalleeSavedRegs(fn *IRFunc) []Reg {
        used := make(map[Reg]bool)

        // Always preserve the arena register
        used[R15] = true

        // Scan instructions for callee-saved register usage
        // (In a real implementation, this would be done after register allocation)
        for _, ins := range fn.Instructions {
                for j := 0; j < 3; j++ {
                        if ins.Src[j].Kind == IRValReg {
                                // Check if this vreg gets a callee-saved register
                                // Conservative: assume RBX might be used
                        }
                }
        }

        // For now, just preserve R15 (arena) and RBX (general use)
        result := make([]Reg, 0, 2)
        if used[R15] {
                result = append(result, R15)
        }
        // Add RBX conservatively for non-trivial functions
        if len(fn.Instructions) > 2 {
                result = append(result, RBX)
        }

        return result
}

// ======================================================================
// Instruction Code Generation
// ======================================================================

// genInstr generates x86-64 machine code for a single IR instruction.
func (cg *CodeGen) genInstr(ins *IRInstr) {
        a := cg.asm

        switch ins.Op {
        // ========== Constants ==========
        case IRConstInt:
                cg.genConstInt(ins)
        case IRConstFloat:
                cg.genConstFloat(ins)
        case IRConstBool:
                cg.genConstBool(ins)
        case IRConstNil:
                cg.genConstNil(ins)
        case IRConstString:
                cg.genConstString(ins)

        // ========== Arithmetic ==========
        case IRAdd:
                cg.genAdd(ins)
        case IRSub:
                cg.genSub(ins)
        case IRMul:
                cg.genMul(ins)
        case IRDiv:
                cg.genDiv(ins)
        case IRMod:
                cg.genMod(ins)
        case IRNeg:
                cg.genNeg(ins)
        case IRFAdd:
                cg.genFAdd(ins)
        case IRFSub:
                cg.genFSub(ins)
        case IRFMul:
                cg.genFMul(ins)
        case IRFDiv:
                cg.genFDiv(ins)
        case IRFNeg:
                cg.genFNeg(ins)

        // ========== Bitwise ==========
        case IRAnd:
                cg.genAnd(ins)
        case IROr:
                cg.genOr(ins)
        case IRXor:
                cg.genXor(ins)
        case IRNot:
                cg.genNot(ins)
        case IRShl:
                cg.genShl(ins)
        case IRShr:
                cg.genShr(ins)
        case IRSar:
                cg.genSar(ins)

        // ========== Comparison ==========
        case IREq:
                cg.genCmp(CondCodeEq, ins)
        case IRNe:
                cg.genCmp(CondCodeNe, ins)
        case IRLt:
                cg.genCmp(CondCodeLt, ins)
        case IRGt:
                cg.genCmp(CondCodeGt, ins)
        case IRLe:
                cg.genCmp(CondCodeLe, ins)
        case IRGe:
                cg.genCmp(CondCodeGe, ins)

        // ========== Tagged Value Operations ==========
        case IRTagInt:
                cg.genTagInt(ins)
        case IRTagBool:
                cg.genTagBool(ins)
        case IRIsTag:
                cg.genIsTag(ins)
        case IRGetTag:
                cg.genGetTag(ins)
        case IRGetVal:
                cg.genGetVal(ins)
        case IRSetTag:
                cg.genSetTag(ins)

        // ========== Memory / Variables ==========
        case IRLoad:
                cg.genLoad(ins)
        case IRStore:
                cg.genStore(ins)
        case IRSpill:
                cg.genSpill(ins)
        case IRReload:
                cg.genReload(ins)

        // ========== Control Flow ==========
        case IRJump:
                a.JMP(ins.Src[0].Str)
        case IRCondJmp:
                cg.genCondJump(ins)
        case IRLabel:
                a.Label(ins.Src[0].Str)
        case IRRet:
                cg.genRet(ins)
        case IRCall:
                cg.genCall(ins)
        case IRCallInd:
                cg.genCallInd(ins)

        // ========== Conversions ==========
        case IRIntToF64:
                cg.genIntToF64(ins)
        case IRIntToF32:
                cg.genIntToF32(ins)
        case IRF64ToInt:
                cg.genF64ToInt(ins)
        case IRF32ToInt:
                cg.genF32ToInt(ins)
        case IRF64ToF32:
                cg.genF64ToF32(ins)
        case IRF32ToF64:
                cg.genF32ToF64(ins)

        // ========== Table operations ==========
        case IRTableNew:
                cg.genTableNew(ins)
        case IRTableGet:
                cg.genTableGet(ins)
        case IRTableSet:
                cg.genTableSet(ins)
        case IRTableLen:
                cg.genTableLen(ins)

        // ========== String operations ==========
        case IRStrCat:
                cg.genStrCat(ins)
        case IRStrLen:
                cg.genStrLen(ins)
        case IRStrEq:
                cg.genStrEq(ins)

        // ========== Error handling ==========
        case IRMakeErr:
                cg.genMakeErr(ins)
        case IRIsErr:
                cg.genIsErr(ins)
        case IRUnwrapErr:
                cg.genUnwrapErr(ins)

        // ========== Arena ==========
        case IRArenaPush:
                cg.genArenaPush(ins)
        case IRArenaPop:
                cg.genArenaPop(ins)
        case IRArenaAlloc:
                cg.genArenaAlloc(ins)

        // ========== Misc ==========
        case IRNop:
                a.NOP()
        case IRUnreachable:
                a.UD2()
        }
}

// ======================================================================
// Constant Generation
// ======================================================================

func (cg *CodeGen) genConstInt(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)
        val := ins.Src[0].Int

        if val == 0 {
                a.MovZeroR64(dst)
        } else if fitsI32(val) {
                a.MovRM(dst, val)
        } else {
                a.MovRM64(dst, val)
        }
}

func (cg *CodeGen) genConstFloat(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocXMM(ins.Dst)
        val := ins.Src[0].Float

        // Load the float from the data section
        // For now, emit it inline using MOVSD with a RIP-relative reference
        // We'll use the float constants table
        idx := -1
        for i, f := range cg.module.Floats {
                if f == val {
                        idx = i
                        break
                }
        }

        if idx >= 0 {
                // For simplicity, use MOV immediate via stack
                tmp := cg.allocTempGPR()
                a.MovRM64(tmp, int64(math.Float64bits(val)))
                a.MovSD_RM(dst, MemOp(MemBase(tmp, 0)))
                cg.freeTempGPR(tmp)
        } else {
                // Inline the bits
                tmp := cg.allocTempGPR()
                a.MovRM64(tmp, int64(math.Float64bits(val)))
                a.MovSD_RM(dst, MemOp(MemBase(tmp, 0)))
                cg.freeTempGPR(tmp)
        }
}

func (cg *CodeGen) genConstBool(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)
        val := ins.Src[0].Int != 0

        if val {
                a.MovRM(dst, 1)
        } else {
                a.MovZeroR64(dst)
        }
}

func (cg *CodeGen) genConstNil(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)
        a.MovRM64(dst, int64(NilTaggedVal))
}

func (cg *CodeGen) genConstString(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)

        // The string index is in Src[0].Int
        // We create a pointer to the string data in the data section
        // For now, encode as a tagged string value: tag | pointer
        strIdx := int(ins.Src[0].Int)
        if strIdx >= 0 && strIdx < len(cg.module.Strings) {
                // Use the string offset from the data section
                offset := cg.stringOffsets[cg.module.Strings[strIdx]]
                // Create a tagged string value
                a.MovRM64(dst, int64(MakeTagged(TagStr, uint64(offset))))
        } else {
                a.MovRM64(dst, int64(NilTaggedVal))
        }
}

// ======================================================================
// Arithmetic Code Generation
// ======================================================================

func (cg *CodeGen) genAdd(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        switch ins.Src[0].Kind {
        case IRValReg:
                a.AddRR(dst, src)
        case IRValInt:
                a.AddRI(dst, ins.Src[0].Int)
        }
}

func (cg *CodeGen) genSub(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        switch ins.Src[0].Kind {
        case IRValReg:
                a.SubRR(dst, src)
        case IRValInt:
                a.SubRI(dst, ins.Src[0].Int)
        }
}

func (cg *CodeGen) genMul(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        switch ins.Src[0].Kind {
        case IRValReg:
                a.IMul2RR(dst, src)
        case IRValInt:
                a.IMul3(dst, dst, int32(ins.Src[0].Int))
        }
}

func (cg *CodeGen) genDiv(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        // IDIV uses RDX:RAX as implicit operands
        // Save destination to RAX if different
        if dst.Code != RAX.Code {
                a.MovRR(RAX, dst)
        }
        // Sign-extend RAX into RDX:RAX
        a.CQO()
        // Perform division
        if src.Code != dst.Code {
                a.IDivRR(src)
        }
        // Move result from RAX to destination
        if dst.Code != RAX.Code {
                a.MovRR(dst, RAX)
        }
}

func (cg *CodeGen) genMod(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        // IMOD: same as IDIV but result is in RDX (remainder)
        if dst.Code != RAX.Code {
                a.MovRR(RAX, dst)
        }
        a.CQO()
        a.IDivRR(src)
        if dst.Code != RDX.Code {
                a.MovRR(dst, RDX)
        }
}

func (cg *CodeGen) genNeg(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        a.NegR(dst)
}

// ======================================================================
// Floating-Point Arithmetic
// ======================================================================

func (cg *CodeGen) genFAdd(ins *IRInstr) {
        a := cg.asm
        dst := cg.getXMM(ins.Dst)
        src := cg.getOperandXMM(&ins.Src[0])

        a.AddSD(RegOp(dst), src)
}

func (cg *CodeGen) genFSub(ins *IRInstr) {
        a := cg.asm
        dst := cg.getXMM(ins.Dst)
        src := cg.getOperandXMM(&ins.Src[0])

        a.SubSD(RegOp(dst), src)
}

func (cg *CodeGen) genFMul(ins *IRInstr) {
        a := cg.asm
        dst := cg.getXMM(ins.Dst)
        src := cg.getOperandXMM(&ins.Src[0])

        a.MulSD(RegOp(dst), src)
}

func (cg *CodeGen) genFDiv(ins *IRInstr) {
        a := cg.asm
        dst := cg.getXMM(ins.Dst)
        src := cg.getOperandXMM(&ins.Src[0])

        a.DivSD(RegOp(dst), src)
}

func (cg *CodeGen) genFNeg(ins *IRInstr) {
        a := cg.asm
        dst := cg.getXMM(ins.Dst)

        // Negate by XOR with sign bit mask
        // We can use a temporary XMM register loaded with the sign mask,
        // or use the stack.
        // Simplest: use memory-based XORPS
        // For now, load sign mask into temp register
        tmp := cg.allocTempXMM()
        a.XorPS(tmp, tmp) // zero tmp
        // Set bit 63 (sign bit for double)
        a.MovRM64(RAX, -9223372036854775808)
        a.MovSD_RM(tmp, MemOp(MemBase(RAX, 0)))
        a.XorPS(dst, tmp)
        cg.freeTempXMM(tmp)
}

// ======================================================================
// Bitwise Operations
// ======================================================================

func (cg *CodeGen) genAnd(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        switch ins.Src[0].Kind {
        case IRValReg:
                a.AndRR(dst, src)
        case IRValInt:
                a.AndRI(dst, ins.Src[0].Int)
        }
}

func (cg *CodeGen) genOr(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        switch ins.Src[0].Kind {
        case IRValReg:
                a.OrRR(dst, src)
        case IRValInt:
                a.OrRI(dst, ins.Src[0].Int)
        }
}

func (cg *CodeGen) genXor(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        switch ins.Src[0].Kind {
        case IRValReg:
                a.XorRR(dst, src)
        case IRValInt:
                a.XorRI(dst, ins.Src[0].Int)
        }
}

func (cg *CodeGen) genNot(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        a.NotR(dst)
}

func (cg *CodeGen) genShl(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        if ins.Src[0].Kind == IRValInt {
                a.ShlRI(dst, uint8(ins.Src[0].Int))
        } else if ins.Src[0].Kind == IRValReg {
                src := cg.getReg(ins.Src[0].Reg, 64)
                // Move shift count to CL
                if src.Code != RCX.Code {
                        a.MovRR(RCX, src)
                }
                a.ShlRCL(dst)
        }
}

func (cg *CodeGen) genShr(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        if ins.Src[0].Kind == IRValInt {
                a.ShrRI(dst, uint8(ins.Src[0].Int))
        } else if ins.Src[0].Kind == IRValReg {
                src := cg.getReg(ins.Src[0].Reg, 64)
                if src.Code != RCX.Code {
                        a.MovRR(RCX, src)
                }
                a.ShrRCL(dst)
        }
}

func (cg *CodeGen) genSar(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        if ins.Src[0].Kind == IRValInt {
                a.SarRI(dst, uint8(ins.Src[0].Int))
        } else if ins.Src[0].Kind == IRValReg {
                src := cg.getReg(ins.Src[0].Reg, 64)
                if src.Code != RCX.Code {
                        a.MovRR(RCX, src)
                }
                a.SarRCL(dst)
        }
}

// ======================================================================
// Comparison Code Generation
// ======================================================================

// CondCodeOp maps IR comparison operations to x86 condition codes.
var CondCodeOp = map[IROp]CondCode{
        IREq: CondE,
        IRNe: CondNE,
        IRLt: CondL,
        IRGt: CondG,
        IRLe: CondLE,
        IRGe: CondGE,
}

type ccKind int

const (
        CondCodeEq ccKind = iota
        CondCodeNe
        CondCodeLt
        CondCodeGt
        CondCodeLe
        CondCodeGe
)

func cgCondCode(kind ccKind) CondCode {
        switch kind {
        case CondCodeEq:
                return CondE
        case CondCodeNe:
                return CondNE
        case CondCodeLt:
                return CondL
        case CondCodeGt:
                return CondG
        case CondCodeLe:
                return CondLE
        case CondCodeGe:
                return CondGE
        default:
                return CondE
        }
}

func (cg *CodeGen) genCmp(kind ccKind, ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        // CMP dst, src
        switch ins.Src[0].Kind {
        case IRValReg:
                a.CmpRR(dst, src)
        case IRValInt:
                a.CmpRI(dst, ins.Src[0].Int)
        }

        // SETcc result
        tmp := cg.allocTempGPR()
        a.SetCC(cgCondCode(kind), tmp.SubReg(8))
        a.MovZeroR64(dst)
        a.MovZX8_64(dst, tmp.SubReg(8))
        cg.freeTempGPR(tmp)
}

// ======================================================================
// Tagged Value Operations
// ======================================================================

func (cg *CodeGen) genTagInt(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        // Tagged int: (value << 8) | INT_TAG
        // But in our scheme: value in bits 0-55, tag in bits 56-63
        // IRTagInt: dst = (dst & ValueMask) | (TagInt << TagShift)

        // AND dst, ValueMask
        a.AndRI(dst, int64(ValueMask))
        // OR dst, TagInt << TagShift
        a.OrRI(dst, int64(TagInt<<TagShift))
}

func (cg *CodeGen) genTagBool(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        // Tagged bool: value in bit 0, tag in bits 56-63
        // AND with 1 to get boolean value
        a.AndRI(dst, 1)
        // OR with tag
        a.OrRI(dst, int64(TagBool<<TagShift))
}

func (cg *CodeGen) genIsTag(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        // Check if tag matches: (value >> 56) == expected_tag
        // Expected tag is in Src[1].Int
        expectedTag := uint8(ins.Src[1].Int)

        // Shift right by 56
        tmp := cg.allocTempGPR()
        if dst.Code != tmp.Code {
                a.MovRR(tmp, dst)
        }
        a.ShrRI(tmp, 56)
        a.CmpRI(tmp, int64(expectedTag))
        a.SetE(tmp.SubReg(8))
        a.MovZeroR64(dst)
        a.MovZX8_64(dst, tmp.SubReg(8))
        cg.freeTempGPR(tmp)
}

func (cg *CodeGen) genGetTag(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        // Extract tag byte: value >> 56
        a.ShrRI(dst, 56)
}

func (cg *CodeGen) genGetVal(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        // Extract value bits: value & ValueMask
        a.AndRI(dst, int64(ValueMask))
}

func (cg *CodeGen) genSetTag(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        // Set tag: (value & ValueMask) | (tag << TagShift)
        // tag is in Src[0].Int
        tag := ins.Src[0].Int
        a.AndRI(dst, int64(ValueMask))
        a.OrRI(dst, tag<<TagShift)
}

// ======================================================================
// Memory / Variable Operations
// ======================================================================

func (cg *CodeGen) genLoad(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)

        // Load from local variable slot
        // Slot index is in Src[0].Slot
        slotOffset := cg.slotOffset(ins.Src[0].Slot)
        a.MovMemR(dst, MemBase(RBP, int32(slotOffset)))
}

func (cg *CodeGen) genStore(ins *IRInstr) {
        a := cg.asm

        // Store to local variable slot
        src := cg.getOperandGPR(&ins.Src[0], 64)
        slotOffset := cg.slotOffset(ins.Src[1].Slot)
        a.MovRMMem(MemBase(RBP, int32(slotOffset)), src)
}

func (cg *CodeGen) genSpill(ins *IRInstr) {
        a := cg.asm

        // Spill a virtual register to the stack
        src := cg.getReg(ins.Dst, 64)
        spillOffset := cg.regAlloc.SpillSlot(ins.Dst)
        a.MovRMMem(MemBase(RBP, int32(spillOffset)), src)
}

func (cg *CodeGen) genReload(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)

        spillOffset := cg.regAlloc.SpillSlot(ins.Dst)
        a.MovMemR(dst, MemBase(RBP, int32(spillOffset)))
}

// slotOffset computes the RBP-relative offset for a local variable slot.
func (cg *CodeGen) slotOffset(slot int) int {
        usedCalleeSaved := cg.usedCalleeSavedRegs(cg.fn)
        totalPush := 8 + len(usedCalleeSaved)*8
        adjust := cg.frame.FrameSize - totalPush
        if adjust > 0 {
                adjust = (adjust + 15) & ^15
        }
        return -(totalPush + adjust + slot*8)
}

// ======================================================================
// Control Flow
// ======================================================================

func (cg *CodeGen) genCondJump(ins *IRInstr) {
        a := cg.asm

        // Src[0] is the condition VReg
        // Src[1] is the true label (jump if condition != 0)
        // Src[2] is the false label (fall-through or explicit)
        cond := cg.getOperandGPR(&ins.Src[0], 64)

        // Yilt bools are tagged: true = (TAG_BOOL << 56) | 1, false = (TAG_BOOL << 56) | 0.
        // Both are non-zero in the full 64-bit representation (the tag byte is 0x02),
        // so TEST cond, cond would always be "truthy".  We must test the PAYLOAD bit
        // (bit 0) instead.  TEST cond, 1 checks if bit 0 is set, which correctly
        // distinguishes true (1) from false (0).
        //
        // For raw integer conditions (e.g. from `if x` where x is int), bit 0 also
        // correctly reflects truthiness for all non-zero integers except even ones.
        // The Yilt checker requires `if` conditions to be bool, so in practice the
        // condition is always a tagged bool and this is correct.
        a.TestRI(cond, 1)
        a.JNE(ins.Src[1].Str)

        // If there's a false label, jump there too (after the true test fails)
        if ins.Src[2].Kind == IRValLabel && ins.Src[2].Str != "" {
                a.JMP(ins.Src[2].Str)
        }
}

func (cg *CodeGen) genRet(ins *IRInstr) {
        a := cg.asm

        if ins.Src[0].Kind == IRValReg {
                // Move return value to RAX
                src := cg.getOperandGPR(&ins.Src[0], 64)
                if src.Code != RAX.Code {
                        a.MovRR(RAX, src)
                }
        }

        cg.emitEpilogue(cg.fn)
}

// ======================================================================
// Function Calls
// ======================================================================

func (cg *CodeGen) genCall(ins *IRInstr) {
        a := cg.asm
        funcName := ins.Src[0].Str
        numArgs := int(ins.Src[1].Int)

        // Save caller-saved registers that are currently in use
        cg.saveCallerSaved(a)

        // Set up arguments according to ABI
        // Arguments are passed as Src[2..] virtual registers
        cg.setupCallArgs(a, numArgs, ins.Src[2:])

        // Windows: allocate shadow space if needed
        if cg.target.ABI == ABIWindows {
                a.SubRI(RSP, 32) // 32 bytes shadow space
        }

        // Emit the call
        a.CALL(funcName)

        // Clean up shadow space
        if cg.target.ABI == ABIWindows {
                a.AddRI(RSP, 32)
        }

        // Clean up stack-passed arguments
        if numArgs > len(cg.abi.IntArgRegs) {
                stackArgs := (numArgs - len(cg.abi.IntArgRegs)) * 8
                a.AddRI(RSP, int64(stackArgs))
        }

        // Restore caller-saved registers
        cg.restoreCallerSaved(a)

        // Move return value to destination
        if ins.Dst != NoVReg {
                dst := cg.allocGPR(ins.Dst, 64)
                if dst.Code != RAX.Code {
                        a.MovRR(dst, RAX)
                }
        }
}

func (cg *CodeGen) genCallInd(ins *IRInstr) {
        a := cg.asm
        numArgs := int(ins.Src[1].Int)

        cg.saveCallerSaved(a)

        // Load function pointer into RAX (temporary)
        funcPtr := cg.getOperandGPR(&ins.Src[0], 64)
        if funcPtr.Code != R11.Code {
                a.MovRR(R11, funcPtr)
        }

        cg.setupCallArgs(a, numArgs, ins.Src[2:])

        if cg.target.ABI == ABIWindows {
                a.SubRI(RSP, 32)
        }

        a.CALLReg(R11)

        if cg.target.ABI == ABIWindows {
                a.AddRI(RSP, 32)
        }

        if numArgs > len(cg.abi.IntArgRegs) {
                stackArgs := (numArgs - len(cg.abi.IntArgRegs)) * 8
                a.AddRI(RSP, int64(stackArgs))
        }

        cg.restoreCallerSaved(a)

        if ins.Dst != NoVReg {
                dst := cg.allocGPR(ins.Dst, 64)
                if dst.Code != RAX.Code {
                        a.MovRR(dst, RAX)
                }
        }
}

// setupCallArgs moves arguments into the correct registers/stack positions.
func (cg *CodeGen) setupCallArgs(a *Asm, numArgs int, argVals []IRVal) {
        intRegIdx := 0
        floatRegIdx := 0
        stackArgIdx := 0

        for i := 0; i < numArgs && i < len(argVals); i++ {
                v := argVals[i]

                // Determine if this is a float argument based on value kind
                isFloat := v.Kind == IRValFloat

                if isFloat {
                        reg := cg.abi.FloatArgReg(floatRegIdx)
                        if reg != nil {
                                // Move float value to the XMM register
                                xmmSrc := cg.getOperandXMM(&v)
                                a.MovSD_RM(*reg, xmmSrc)
                                floatRegIdx++
                        } else {
                                // Pass on stack - store float to stack
                                // Reserve stack space
                                a.SubRI(RSP, 8)
                                // Store the float value from XMM register
                                xmmSrc := cg.getOperandXMM(&v)
                                a.MovSD_MR(MemOp(MemBase(RSP, 0)), xmmSrc.Reg)
                                stackArgIdx++
                        }
                } else {
                        reg := cg.abi.IntArgReg(intRegIdx)
                        if reg != nil {
                                src := cg.getOperandGPR(&v, 64)
                                if src.Code != reg.Code {
                                        a.MovRR(*reg, src)
                                }
                                intRegIdx++
                        } else {
                                // Pass on stack
                                src := cg.getOperandGPR(&v, 64)
                                a.PUSH(src)
                                stackArgIdx++
                        }
                }
        }
}

// ======================================================================
// Runtime Call Helpers
// ======================================================================

// emitCall maps physical registers to ABI argument registers and emits a CALL.
// It handles Windows shadow space and stack-passed argument cleanup.
// argRegs holds physical registers that already contain the argument values.
func (cg *CodeGen) emitCall(name string, argRegs ...Reg) {
        a := cg.asm

        // Move arguments into ABI-correct integer argument registers.
        for i, arg := range argRegs {
                if reg := cg.abi.IntArgReg(i); reg != nil {
                        if arg.Code != reg.Code || arg.Extended != reg.Extended {
                                a.MovRR(*reg, arg)
                        }
                } else {
                        // Overflow: pass on stack.
                        a.PUSH(arg)
                }
        }

        // Windows x64: allocate 32-byte shadow space.
        if cg.target.ABI == ABIWindows {
                a.SubRI(RSP, 32)
        }

        a.CALL(name)

        // Clean up shadow space.
        if cg.target.ABI == ABIWindows {
                a.AddRI(RSP, 32)
        }

        // Clean up stack-passed arguments.
        if len(argRegs) > len(cg.abi.IntArgRegs) {
                stackArgs := (len(argRegs) - len(cg.abi.IntArgRegs)) * 8
                a.AddRI(RSP, int64(stackArgs))
        }
}

// emitRuntimeCall wraps emitCall with caller-saved register save/restore.
// This is the standard pattern for calling runtime helper functions.
func (cg *CodeGen) emitRuntimeCall(name string, argRegs ...Reg) {
        cg.saveCallerSaved(cg.asm)
        cg.emitCall(name, argRegs...)
        cg.restoreCallerSaved(cg.asm)
}

// moveRetToDst moves the function return value (RAX) to the destination vreg.
func (cg *CodeGen) moveRetToDst(dst VReg) {
        if dst != NoVReg {
                d := cg.allocGPR(dst, 64)
                if d.Code != RAX.Code {
                        cg.asm.MovRR(d, RAX)
                }
        }
}

// ======================================================================
// Conversion Operations
// ======================================================================

func (cg *CodeGen) genIntToF64(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocXMM(ins.Dst)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        a.CVTSI2SD64(dst, RegOp(src))
}

func (cg *CodeGen) genIntToF32(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocXMM(ins.Dst)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        a.CVTSI2SS64(dst, RegOp(src))
}

func (cg *CodeGen) genF64ToInt(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)
        src := cg.getXMM(ins.Src[0].Reg)

        a.CVTTSD2SI64(dst, src)
}

func (cg *CodeGen) genF32ToInt(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)
        src := cg.getXMM(ins.Src[0].Reg)

        a.CVTTSS2SI64(dst, src)
}

func (cg *CodeGen) genF64ToF32(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocXMM(ins.Dst)
        src := cg.getXMM(ins.Src[0].Reg)

        a.CVTSD2SS(RegOp(dst), RegOp(src))
}

func (cg *CodeGen) genF32ToF64(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocXMM(ins.Dst)
        src := cg.getXMM(ins.Src[0].Reg)

        a.CVTSS2SD(RegOp(dst), RegOp(src))
}

// ======================================================================
// Table Operations (Runtime Helpers)
// ======================================================================

func (cg *CodeGen) genTableNew(ins *IRInstr) {
        // Call runtime helper: yilt_table_new(arena, capacity)
        // arg[0] = arena (R15), arg[1] = capacity (immediate).
        cg.saveCallerSaved(cg.asm)
        capReg := cg.abi.IntArgReg(1) // RSI (SysV) or RDX (Windows)
        cg.asm.MovRM(*capReg, ins.Src[0].Int)
        cg.emitCall("yilt_table_new", R15, *capReg)
        cg.restoreCallerSaved(cg.asm)
        cg.moveRetToDst(ins.Dst)
}

func (cg *CodeGen) genTableGet(ins *IRInstr) {
        // Call runtime helper: yilt_table_get(arena, table, key)
        table := cg.getOperandGPR(&ins.Src[0], 64)
        key := cg.getOperandGPR(&ins.Src[1], 64)
        cg.emitRuntimeCall("yilt_table_get", R15, table, key)
        cg.moveRetToDst(ins.Dst)
}

func (cg *CodeGen) genTableSet(ins *IRInstr) {
        // Call runtime helper: yilt_table_set(arena, table, key, value)
        table := cg.getOperandGPR(&ins.Src[0], 64)
        key := cg.getOperandGPR(&ins.Src[1], 64)
        val := cg.getOperandGPR(&ins.Src[2], 64)
        cg.emitRuntimeCall("yilt_table_set", R15, table, key, val)
        cg.moveRetToDst(ins.Dst)
}

func (cg *CodeGen) genTableLen(ins *IRInstr) {
        // Call runtime helper: yilt_table_len(arena, table)
        table := cg.getOperandGPR(&ins.Src[0], 64)
        cg.emitRuntimeCall("yilt_table_len", R15, table)
        cg.moveRetToDst(ins.Dst)
}

// ======================================================================
// String Operations (Runtime Helpers)
// ======================================================================

func (cg *CodeGen) genStrCat(ins *IRInstr) {
        // Call runtime helper: yilt_str_cat(arena, str1, str2)
        s1 := cg.getOperandGPR(&ins.Src[0], 64)
        s2 := cg.getOperandGPR(&ins.Src[1], 64)
        cg.emitRuntimeCall("yilt_str_cat", R15, s1, s2)
        cg.moveRetToDst(ins.Dst)
}

func (cg *CodeGen) genStrLen(ins *IRInstr) {
        // Call runtime helper: yilt_str_len(str)
        s := cg.getOperandGPR(&ins.Src[0], 64)
        cg.emitRuntimeCall("yilt_str_len", s)
        cg.moveRetToDst(ins.Dst)
}

func (cg *CodeGen) genStrEq(ins *IRInstr) {
        // Call runtime helper: yilt_str_eq(str1, str2)
        s1 := cg.getOperandGPR(&ins.Src[0], 64)
        s2 := cg.getOperandGPR(&ins.Src[1], 64)
        cg.emitRuntimeCall("yilt_str_eq", s1, s2)
        cg.moveRetToDst(ins.Dst)
}

// ======================================================================
// Error Handling
// ======================================================================

func (cg *CodeGen) genMakeErr(ins *IRInstr) {
        a := cg.asm
        dst := cg.getReg(ins.Dst, 64)

        // Make error: set tag to TagErr
        a.AndRI(dst, int64(ValueMask))
        a.OrRI(dst, int64(TagErr<<TagShift))
}

func (cg *CodeGen) genIsErr(ins *IRInstr) {
        a := cg.asm
        dst := cg.allocGPR(ins.Dst, 64)
        src := cg.getOperandGPR(&ins.Src[0], 64)

        // Check if tag == TagErr
        tmp := cg.allocTempGPR()
        if src.Code != tmp.Code {
                a.MovRR(tmp, src)
        }
        a.ShrRI(tmp, 56)
        a.CmpRI(tmp, int64(TagErr))
        a.SetE(tmp.SubReg(8))
        a.MovZeroR64(dst)
        a.MovZX8_64(dst, tmp.SubReg(8))
        cg.freeTempGPR(tmp)
}

func (cg *CodeGen) genUnwrapErr(ins *IRInstr) {
        a := cg.asm

        // ? operator: check if value is an error, if so return it
        src := cg.getOperandGPR(&ins.Src[0], 64)
        trueLabel := cg.asm.GenLabel("unwrap_ok")

        // Check if tag is TagErr
        tmp := cg.allocTempGPR()
        if src.Code != tmp.Code {
                a.MovRR(tmp, src)
        }
        a.ShrRI(tmp, 56)
        a.CmpRI(tmp, int64(TagErr))
        cg.freeTempGPR(tmp)

        // If not error, jump to ok
        a.JNE(trueLabel)

        // If error: return the error value
        if src.Code != RAX.Code {
                a.MovRR(RAX, src)
        }
        cg.emitEpilogue(cg.fn)

        // OK: continue, move value to destination
        a.Label(trueLabel)
        if ins.Dst != NoVReg {
                dst := cg.getReg(ins.Dst, 64)
                if src.Code != dst.Code {
                        a.MovRR(dst, src)
                }
        }
}

// ======================================================================
// Arena Operations
// ======================================================================

func (cg *CodeGen) genArenaPush(ins *IRInstr) {
        // Inline arena push: save current arena pointer
        // PUSH R15 (save current arena state)
        cg.asm.PUSH(R15)
}

func (cg *CodeGen) genArenaPop(ins *IRInstr) {
        // Inline arena pop: restore arena pointer
        cg.asm.POP(R15)
}

func (cg *CodeGen) genArenaAlloc(ins *IRInstr) {
        // Call runtime helper: yilt_arena_alloc(arena, size)
        // arg[0] = arena (R15), arg[1] = size (immediate).
        cg.saveCallerSaved(cg.asm)
        sizeReg := cg.abi.IntArgReg(1) // RSI (SysV) or RDX (Windows)
        cg.asm.MovRM(*sizeReg, ins.Src[0].Int)
        cg.emitCall("yilt_arena_alloc", R15, *sizeReg)
        cg.restoreCallerSaved(cg.asm)

        // Update arena pointer (R15) from return value.
        cg.asm.MovRR(R15, RAX)
        cg.moveRetToDst(ins.Dst)
}

// ======================================================================
// Register Allocation Helpers
// ======================================================================

// savedCallerRegs tracks caller-saved registers that need saving/restoring.
var savedCallerRegs []Reg

func (cg *CodeGen) saveCallerSaved(a *Asm) {
        // Save any caller-saved registers that the register allocator has given out
        savedCallerRegs = nil
        for _, r := range cg.abi.CallerSaved {
                // Check if this register is in use
                if r.Code == RSP.Code || r.Code == RBP.Code {
                        continue
                }
                // Simple heuristic: save registers that are not RAX (return value)
                // and not argument registers (they'll be set up by setupCallArgs)
                a.PUSH(r)
                savedCallerRegs = append(savedCallerRegs, r)
        }
}

func (cg *CodeGen) restoreCallerSaved(a *Asm) {
        // Restore in reverse order
        for i := len(savedCallerRegs) - 1; i >= 0; i-- {
                a.POP(savedCallerRegs[i])
        }
        savedCallerRegs = nil
}

func (cg *CodeGen) allocGPR(v VReg, size int) Reg {
        r := cg.regAlloc.AllocGPR(v, size)
        if r.Code == RBP.Code && cg.regAlloc.IsSpilled(v) {
                // Register was spilled, we need to return RBP sentinel
                // The caller should check IsSpilled
        }
        return r
}

func (cg *CodeGen) allocXMM(v VReg) Reg {
        return cg.regAlloc.AllocXMM(v)
}

func (cg *CodeGen) allocTempGPR() Reg {
        return cg.regAlloc.AllocTempGPR()
}

func (cg *CodeGen) freeTempGPR(r Reg) {
        cg.regAlloc.FreeTempGPR(r)
}

func (cg *CodeGen) allocTempXMM() Reg {
        return cg.regAlloc.AllocTempXMM()
}

func (cg *CodeGen) freeTempXMM(r Reg) {
        cg.regAlloc.FreeTempXMM(r)
}

func (cg *CodeGen) getReg(v VReg, size int) Reg {
        if cg.regAlloc.HasPhysReg(v) {
                return cg.regAlloc.PhysReg(v)
        }
        // Allocate if not already allocated
        r := cg.regAlloc.AllocGPR(v, size)
        if cg.regAlloc.IsSpilled(v) {
                // Need to load from spill slot
                a := cg.asm
                offset := cg.regAlloc.SpillSlot(v)
                // Use a temporary register that we allocate
                tmp := cg.regAlloc.AllocTempGPR()
                a.MovMemR(tmp, MemBase(RBP, int32(offset)))
                return tmp
        }
        return r
}

func (cg *CodeGen) getXMM(v VReg) Reg {
        if cg.regAlloc.HasPhysReg(v) {
                return cg.regAlloc.PhysReg(v)
        }
        return cg.regAlloc.AllocXMM(v)
}

func (cg *CodeGen) getOperandGPR(v *IRVal, size int) Reg {
        if v.Kind == IRValReg {
                return cg.getReg(v.Reg, size)
        }
        // For constants, allocate a temp and load
        tmp := cg.allocTempGPR()
        switch v.Kind {
        case IRValInt:
                if v.Int == 0 {
                        cg.asm.MovZeroR64(tmp)
                } else if fitsI32(v.Int) {
                        cg.asm.MovRM(tmp, v.Int)
                } else {
                        cg.asm.MovRM64(tmp, v.Int)
                }
        }
        return tmp
}

func (cg *CodeGen) getOperandXMM(v *IRVal) Operand {
        if v.Kind == IRValReg {
                return RegOp(cg.getXMM(v.Reg))
        }
        // For float constants, load into temp XMM
        tmp := cg.allocTempXMM()
        cg.asm.XorPS(tmp, tmp) // zero
        if v.Kind == IRValFloat {
                gprTmp := cg.allocTempGPR()
                cg.asm.MovRM64(gprTmp, int64(math.Float64bits(v.Float)))
                cg.asm.MovSD_RM(tmp, MemOp(MemBase(gprTmp, 0)))
                cg.freeTempGPR(gprTmp)
        }
        return RegOp(tmp)
}

// ======================================================================
// Loop Context Management (for break/continue)
// ======================================================================

// PushLoop starts tracking a new loop for break/continue labels.
func (cg *CodeGen) PushLoop(breakLabel, continueLabel, headerLabel string) {
        cg.loopStacks = append(cg.loopStacks, loopContext{
                breakLabel:    breakLabel,
                continueLabel: continueLabel,
                headerLabel:   headerLabel,
        })
}

// PopLoop ends the current loop tracking.
func (cg *CodeGen) PopLoop() {
        if len(cg.loopStacks) > 0 {
                cg.loopStacks = cg.loopStacks[:len(cg.loopStacks)-1]
        }
}

// CurrentBreakLabel returns the label for the nearest enclosing loop's break target.
func (cg *CodeGen) CurrentBreakLabel() string {
        if len(cg.loopStacks) > 0 {
                return cg.loopStacks[len(cg.loopStacks)-1].breakLabel
        }
        return ""
}

// CurrentContinueLabel returns the label for the nearest enclosing loop's continue target.
func (cg *CodeGen) CurrentContinueLabel() string {
        if len(cg.loopStacks) > 0 {
                return cg.loopStacks[len(cg.loopStacks)-1].continueLabel
        }
        return ""
}

// ======================================================================
// ELF Object File Output (Minimal)
// ======================================================================

// ELF constants
const (
        ELFMAG0 = 0x7F
        ELFMAG1 = 'E'
        ELFMAG2 = 'L'
        ELFMAG3 = 'F'
        ELFCLASS64 = 2
        ELFDATA2LSB = 1
        EV_CURRENT   = 1
        ET_EXEC      = 2
        ET_DYN       = 3
        EM_X86_64    = 62
)

// ELFHeader represents a minimal 64-bit ELF header.
type ELFHeader struct {
        Ident     [16]byte
        Type      uint16
        Machine   uint16
        Version   uint32
        Entry     uint64
        Phoff     uint64
        Shoff     uint64
        Flags     uint32
        Ehsize    uint16
        Phentsize uint16
        Phnum     uint16
        Shentsize uint16
        Shnum     uint16
        Shstrndx  uint16
}

// ProgramHeader represents a minimal ELF program header (PT_LOAD).
type ProgramHeader struct {
        Type   uint32
        Flags  uint32
        Offset uint64
        Vaddr  uint64
        Paddr  uint64
        Filesz uint64
        Memsz  uint64
        Align  uint64
}

const (
        PT_LOAD = 1
        PF_X    = 1
        PF_W    = 2
        PF_R    = 4
)

// WriteELF produces a minimal ELF executable from the generated code.
// This is a simplified implementation suitable for testing and standalone execution.
func (cg *CodeGen) WriteELF(entryPoint string) []byte {
        // This is a placeholder for ELF generation.
        // A full implementation would create proper ELF headers, program headers,
        // and section headers. For now, return just the raw code.
        return cg.textSection
}

// ======================================================================
// Disassembly Helpers (for debugging)
// ======================================================================

// DisasmOption controls disassembly output format.
type DisasmOption int

const (
        DisasmNone DisasmOption = iota
        DisasmBytes
        DisasmComments
)

// DisassembleIR returns a human-readable listing of the IR instructions.
func DisassembleIR(fn *IRFunc) string {
        var sb strings.Builder
        sb.WriteString(fmt.Sprintf("fn %s(", fn.Name))
        for i, p := range fn.Params {
                if i > 0 {
                        sb.WriteString(", ")
                }
                sb.WriteString(fmt.Sprintf("%s: %s", p.Name, p.Type))
        }
        sb.WriteString(") -> [")
        for i, r := range fn.Returns {
                if i > 0 {
                        sb.WriteString(", ")
                }
                sb.WriteString(r.String())
        }
        sb.WriteString("]\n")

        for i, ins := range fn.Instructions {
                sb.WriteString(fmt.Sprintf("  %4d  %s\n", i, ins.String()))
        }
        return sb.String()
}

// ======================================================================
// Module-Level Utilities
// ======================================================================

// SortFunctions sorts module functions by name for deterministic output.
func SortFunctions(module *IRModule) {
        sort.Slice(module.Funcs, func(i, j int) bool {
                return module.Funcs[i].Name < module.Funcs[j].Name
        })
}

// AddFunction adds a function to the IR module.
func (m *IRModule) AddFunction(fn *IRFunc) {
        m.Funcs = append(m.Funcs, fn)
}

// AddString adds a string constant to the module and returns its index.
func (m *IRModule) AddString(s string) int {
        for i, existing := range m.Strings {
                if existing == s {
                        return i
                }
        }
        idx := len(m.Strings)
        m.Strings = append(m.Strings, s)
        return idx
}

// AddFloat adds a float constant to the module and returns its index.
func (m *IRModule) AddFloat(f float64) int {
        for i, existing := range m.Floats {
                if existing == f {
                        return i
                }
        }
        idx := len(m.Floats)
        m.Floats = append(m.Floats, f)
        return idx
}

// NewIRModule creates a new empty IR module.
func NewIRModule() *IRModule {
        return &IRModule{
                Funcs:  make([]*IRFunc, 0),
                Globals: make([]IRGlobal, 0),
                Strings: make([]string, 0),
                Floats:  make([]float64, 0),
        }
}

// NewIRFunc creates a new IR function.
func NewIRFunc(name string) *IRFunc {
        return &IRFunc{
                Name:        name,
                Params:      make([]IRParam, 0),
                Returns:     make([]IRType, 0),
                Instructions: make([]IRInstr, 0),
        }
}

// AddParam adds a parameter to the function.
func (f *IRFunc) AddParam(name string, typ IRType, vreg VReg) {
        f.Params = append(f.Params, IRParam{
                Name: name,
                Type: typ,
                VReg: vreg,
        })
}

// AddReturn adds a return type to the function.
func (f *IRFunc) AddReturn(typ IRType) {
        f.Returns = append(f.Returns, typ)
}

// Emit adds an instruction to the function.
func (f *IRFunc) Emit(op IROp, dst VReg, src ...IRVal) {
        ins := IRInstr{Op: op, Dst: dst}
        for i := 0; i < len(src) && i < 3; i++ {
                ins.Src[i] = src[i]
        }
        f.Instructions = append(f.Instructions, ins)
}
