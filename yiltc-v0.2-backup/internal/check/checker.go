// Package check implements semantic analysis and type checking for the Yilt compiler.
//
// The checker operates in two passes:
//  1. Declaration collection: gather all top-level function signatures and imports
//     across every source file so that forward references and mutual recursion work.
//  2. Body checking: type-check every function body, resolving identifiers, validating
//     operations, and annotating each expression with its resolved type. This pass may
//     run concurrently because function bodies only read the immutable global scope.
//
// The checker produces a CheckedProgram that maps every function to its resolved
// signature and body, with a complete expression-type map.
package check

import (
        "fmt"
        "math"
        "path/filepath"
        "sort"
        "strconv"
        "strings"
        "sync"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/diag"
        "github.com/yilt/yiltc/internal/lex"
)

// ---------------------------------------------------------------------------
// Type system
// ---------------------------------------------------------------------------

// TType enumerates the fully resolved types recognised by the checker.
type TType int

const (
        TVoid TType = iota
        TInt
        TUint
        TFp
        TBool
        TStr
        TTable
        TGen   // generic / unresolved / dynamic
        TNamed // user-defined type alias
        TFunc  // function type (first-class)
        THdl   // spawn handle
        TTuple // tuple type
        TStruct // struct (nominal record type)
        TEnum   // enum (sum type with variants)
)

func (t TType) String() string {
        switch t {
        case TVoid:
                return "void"
        case TInt:
                return "int"
        case TUint:
                return "uint"
        case TFp:
                return "fp"
        case TBool:
                return "bool"
        case TStr:
                return "str"
        case TTable:
                return "table"
        case TGen:
                return "gen"
        case TNamed:
                return "named"
        case TFunc:
                return "fn"
        case THdl:
                return "handle"
        case TTuple:
                return "tuple"
        case TStruct:
                return "struct"
        case TEnum:
                return "enum"
        default:
                return "<unknown>"
        }
}

// TypeDesc is a complete, resolved type description.  For table types it may
// carry optional key/value element types; for function types it stores the
// parameter and return types.
type TypeDesc struct {
        Kind TType

        // Name is set when Kind == TNamed.
        Name string

        // Func fields — used when Kind == TFunc.
        Params      []*TypeDesc
        Ret         *TypeDesc
        Variadic    bool // true if the last parameter is variadic (...Type)
        NumDefaults int  // number of params with default values (0 = none, set by registerFn)

        // Table element types — used when Kind == TTable.
        KeyType *TypeDesc
        ValType *TypeDesc

        // Struct fields — used when Kind == TStruct.
        StructFields map[string]*TypeDesc // field name -> field type

        // Enum variants — used when Kind == TEnum.
        EnumVariants []EnumVariantInfo // ordered list of variants
        EnumVariantIndex map[string]int   // variant name -> ordinal index
        EnumVariantPayload map[string]*TypeDesc // variant name -> payload type (nil = simple)
}

// String returns a human-readable representation of the type.
func (t TypeDesc) String() string {
        switch t.Kind {
        case TTuple:
                parts := make([]string, len(t.Params))
                for i, p := range t.Params {
                        parts[i] = p.String()
                }
                return fmt.Sprintf("(%s)", strings.Join(parts, ", "))
        case TFunc:
                params := make([]string, len(t.Params))
                for i, p := range t.Params {
                        params[i] = p.String()
                }
                retStr := ""
                if t.Ret != nil {
                        retStr = t.Ret.String()
                }
                return fmt.Sprintf("fn(%s) -> %s", strings.Join(params, ", "), retStr)
        case TTable:
                if t.KeyType != nil && t.ValType != nil {
                        return fmt.Sprintf("table[%s:%s]", t.KeyType.String(), t.ValType.String())
                }
                return "table"
        case TNamed:
                return t.Name
        case TStruct:
                if len(t.StructFields) > 0 {
                        parts := make([]string, 0, len(t.StructFields))
                        for name, ft := range t.StructFields {
                                parts = append(parts, fmt.Sprintf("%s: %s", name, ft.String()))
                        }
                        sort.Strings(parts)
                        return t.Name + "{" + strings.Join(parts, ", ") + "}"
                }
                return t.Name
        default:
                return t.Kind.String()
        }
}

// Equals reports whether two type descriptions are structurally identical.
func (t TypeDesc) Equals(other TypeDesc) bool {
        if t.Kind != other.Kind {
                return false
        }
        if t.Kind == TNamed {
                return t.Name == other.Name
        }
        if t.Kind == TFunc {
                if len(t.Params) != len(other.Params) {
                        return false
                }
                for i := range t.Params {
                        if !t.Params[i].Equals(*other.Params[i]) {
                                return false
                        }
                }
                if t.Ret == nil || other.Ret == nil {
                        return t.Ret == other.Ret
                }
                return t.Ret.Equals(*other.Ret)
        }
        return true
}

// IsNumeric returns true for int, uint, and fp.
func (t TypeDesc) IsNumeric() bool {
        return t.Kind == TInt || t.Kind == TUint || t.Kind == TFp
}

// IsInteger returns true for int and uint.
func (t TypeDesc) IsInteger() bool {
        return t.Kind == TInt || t.Kind == TUint
}

// IsConcrete returns true for every type except TGen.
func (t TypeDesc) IsConcrete() bool {
        return t.Kind != TGen
}

// IsVoid returns true for the void type.
func (t TypeDesc) IsVoid() bool {
        return t.Kind == TVoid
}

// Common type descriptors.
var (
        VoidDesc = TypeDesc{Kind: TVoid}
        IntDesc  = TypeDesc{Kind: TInt}
        UintDesc = TypeDesc{Kind: TUint}
        FpDesc   = TypeDesc{Kind: TFp}
        BoolDesc = TypeDesc{Kind: TBool}
        StrDesc  = TypeDesc{Kind: TStr}
        TableDesc = TypeDesc{Kind: TTable}
        GenDesc  = TypeDesc{Kind: TGen}
        HdlDesc  = TypeDesc{Kind: THdl}
)

// ---------------------------------------------------------------------------
// Bindings and scopes
// ---------------------------------------------------------------------------

// BindingKind describes what category a resolved name belongs to.
type BindingKind int

const (
        BndVar BindingKind = iota
        BndFn
        BndConst
        BndImport
        BndModule
        BndFFI
        BndType // struct type binding
)

func (k BindingKind) String() string {
        switch k {
        case BndVar:
                return "variable"
        case BndFn:
                return "function"
        case BndConst:
                return "constant"
        case BndImport:
                return "import"
        case BndModule:
                return "module"
        case BndFFI:
                return "ffi"
        case BndType:
                return "type"
        default:
                return "<unknown>"
        }
}

// StructInfo holds the checked fields of a struct declaration.
// MethodInfo describes a user-defined method on a struct type.
// Methods are detected by convention: functions named "StructName_methodname"
// whose first parameter's type matches the struct type.
type MethodInfo struct {
        FuncName string    // full function name (e.g. "Point_distance")
        FuncType TypeDesc  // function type WITHOUT the receiver parameter
}

type StructInfo struct {
        Fields     []StructFieldInfo
        FieldTypes map[string]*TypeDesc
        Methods    map[string]*MethodInfo // method name -> method info
}

// StructFieldInfo describes a single field within a struct.
type StructFieldInfo struct {
        Name     string
        Type     TypeDesc
        Mutable bool
}

// EnumVariantInfo describes a single variant within an enum.
type EnumVariantInfo struct {
        Name     string
        Index    int    // ordinal position (0, 1, 2, ...)
        Payload  *TypeDesc // nil for simple variants
}

// EnumInfo holds the checked variants of an enum declaration.
type EnumInfo struct {
        Variants      []EnumVariantInfo
        VariantIndex   map[string]int  // variant name -> index
        VariantPayload map[string]*TypeDesc // variant name -> payload type (nil for simple)
}

// Binding represents a resolved name in the symbol table.
type Binding struct {
        Name    string
        Kind    BindingKind
        Type    TypeDesc
        Mutable bool
        Public  bool
        File    string
        Decl    ast.Decl
        Pos     ast.Pos

        StructInfo *StructInfo // set when Kind == BndType
        EnumInfo  *EnumInfo  // set when Kind == BndType and Type.Kind == TEnum
}

// Scope is a lexical scope that maps names to bindings.
type Scope struct {
        parent *Scope
        names  map[string]*Binding
}

// NewScope creates a new scope nested inside parent (which may be nil).
func NewScope(parent *Scope) *Scope {
        return &Scope{
                parent: parent,
                names:  make(map[string]*Binding),
        }
}

// Define inserts a binding into the scope.  It returns false if the name is
// already defined in this exact scope (shadowing is allowed but flagged).
func (s *Scope) Define(name string, b *Binding) bool {
        if _, ok := s.names[name]; ok {
                return false
        }
        s.names[name] = b
        return true
}

// Lookup searches for a name starting in this scope and walking up to parents.
func (s *Scope) Lookup(name string) *Binding {
        if b, ok := s.names[name]; ok {
                return b
        }
        if s.parent != nil {
                return s.parent.Lookup(name)
        }
        return nil
}

// LookupLocal checks only this scope (no parent walk).
func (s *Scope) LookupLocal(name string) (*Binding, bool) {
        b, ok := s.names[name]
        return b, ok
}

// Each calls fn for every binding defined directly in this scope (no parent walk).
func (s *Scope) Each(fn func(name string, b *Binding)) {
        for name, b := range s.names {
                fn(name, b)
        }
}

// Names returns all names defined directly in this scope.
func (s *Scope) Names() []string {
        out := make([]string, 0, len(s.names))
        for n := range s.names {
                out = append(out, n)
        }
        return out
}

// ---------------------------------------------------------------------------
// Import tracking
// ---------------------------------------------------------------------------

// ImportInfo records a fully resolved import declaration.
type ImportInfo struct {
        ModuleName string // original module name as written in 'use' (e.g., "sys", "std::math")
        ModulePath string // resolved filesystem path (empty for stdlib)
        IsStd      bool   // true for built-in stdlib modules
        IsFFI      bool   // true for FFI modules
        Alias      string // alias used in the importing file
        Symbols    map[string]*Binding // resolved symbols (for selective imports)
        File       string // source file that contains the import
}

// ---------------------------------------------------------------------------
// Checked program output
// ---------------------------------------------------------------------------

// CheckedParam is a type-checked function parameter.
type CheckedParam struct {
        Name       string
        Type       TypeDesc
        Mutable    bool
        Variadic   bool // true if declared as 'name ...Type'
        HasDefault bool // true if parameter has a default value
        Pos        ast.Pos
}

// CheckedFn is a type-checked function ready for IR lowering.
type CheckedFn struct {
        Name    string
        Params  []CheckedParam
        RetType TypeDesc
        Body    []ast.Stmt
        Public  bool
        Extern  bool
        Pos     ast.Pos
        Decl    ast.Node // original AST declaration (for default param exprs)

        // exprTypes maps every expression in this function's body to its
        // resolved type.  It is populated by the checker and consumed by the
        // IR lowering pass.
        exprTypes map[ast.Expr]TypeDesc
}

// ExprType returns the resolved type of the given expression, or GenDesc
// if the expression was not type-checked.
func (f *CheckedFn) ExprType(e ast.Expr) TypeDesc {
        if f.exprTypes == nil {
                return GenDesc
        }
        if t, ok := f.exprTypes[e]; ok {
                return t
        }
        return GenDesc
}

// bindingType returns the function type descriptor for this function's binding.
// The returned type includes all parameters (including the receiver) and the
// return type, suitable for method resolution.
func (f *CheckedFn) bindingType() TypeDesc {
        params := make([]*TypeDesc, len(f.Params))
        for i, p := range f.Params {
                params[i] = &p.Type
        }
        return TypeDesc{
                Kind:   TFunc,
                Params: params,
                Ret:    &f.RetType,
        }
}

// CheckedConst is a type-checked top-level constant.
type CheckedConst struct {
        Name  string
        Value ast.Expr
        Type  TypeDesc
        Pos   ast.Pos
}

// CheckedProgram is the output of semantic analysis.
type CheckedProgram struct {
        Functions  map[string]*CheckedFn
        Consts     map[string]*CheckedConst
        Imports    []ImportInfo
        ExprTypes  map[ast.Expr]TypeDesc    // type of every checked expression
        EnumInfos  map[string]*EnumInfo     // enum name → variant info (for lowerer)
}

// ---------------------------------------------------------------------------
// Standard library stubs
// ---------------------------------------------------------------------------

// stdModuleExports lists the known symbols for each standard library module.
var stdModuleExports = map[string][]struct {
        Name string
        Type TypeDesc
}{
        "sys": {
                {"args", TypeDesc{Kind: TTable, KeyType: &IntDesc, ValType: &StrDesc}},
                {"env", TypeDesc{Kind: TTable, KeyType: &StrDesc, ValType: &StrDesc}},
                {"exit", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&IntDesc}, Ret: &VoidDesc}},
                {"stdin", GenDesc},
                {"stdout", GenDesc},
                {"stderr", GenDesc},
                {"print", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &VoidDesc}},
                {"println", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &VoidDesc}},
                {"clock", TypeDesc{Kind: TFunc, Params: nil, Ret: &FpDesc}},
                {"cwd", StrDesc},
                {"platform", StrDesc},
                {"os", StrDesc},
                {"arch", StrDesc},
                {"setenv", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc, &StrDesc}, Ret: &IntDesc}},
                {"getenv", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"argv", TypeDesc{Kind: TTable, KeyType: &IntDesc, ValType: &StrDesc}},
        },
        "fs": {
                {"read", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"write", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc, &StrDesc}, Ret: &IntDesc}},
                {"exists", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &BoolDesc}},
                {"mkdir", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &IntDesc}},
                {"readdir", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &TableDesc}},
                {"remove", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &IntDesc}},
                {"stat", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &TableDesc}},
                {"copy", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc, &StrDesc}, Ret: &IntDesc}},
                {"rename", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc, &StrDesc}, Ret: &IntDesc}},
                {"is_file", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &BoolDesc}},
                {"is_dir", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &BoolDesc}},
                {"size", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &IntDesc}},
        },
        "path": {
                {"join", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc, &StrDesc}, Ret: &StrDesc}},
                {"base", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"dir", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"ext", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"abs", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"rel", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc, &StrDesc}, Ret: &StrDesc}},
                // Aliases used in tests
                {"basename", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"dirname", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"extname", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"clean", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &StrDesc}},
                {"exists", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &BoolDesc}},
        },
        "json": {
                {"parse", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &GenDesc}},
                {"stringify", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &StrDesc}},
                {"encode", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &StrDesc}},
                {"decode", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&StrDesc}, Ret: &GenDesc}},
        },
        "math": {
                {"abs", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &GenDesc}},
                {"sqrt", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &FpDesc}},
                {"floor", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &FpDesc}},
                {"ceil", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &FpDesc}},
                {"round", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &FpDesc}},
                {"pow", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc, &GenDesc}, Ret: &FpDesc}},
                {"min", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc, &GenDesc}, Ret: &GenDesc}},
                {"max", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc, &GenDesc}, Ret: &GenDesc}},
                {"sin", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &FpDesc}},
                {"cos", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &FpDesc}},
                {"tan", TypeDesc{Kind: TFunc, Params: []*TypeDesc{&GenDesc}, Ret: &FpDesc}},
                {"pi", FpDesc},
                {"e", FpDesc},
        },
}

// ---------------------------------------------------------------------------
// fnCtx — per-function concurrent checking context
// ---------------------------------------------------------------------------

// fnCtx holds all mutable state needed while type-checking a single function
// body.  Each goroutine gets its own fnCtx so no locking is required for the
// scope chain or expression-type map.
type fnCtx struct {
        checker   *Checker
        decl      *ast.FnDecl
        checked   *CheckedFn
        scopes    []*Scope
        loopDepth int
        exprTypes map[ast.Expr]TypeDesc
        hasReturn  bool
        unreachable bool // set after return/break/continue to detect dead code

        // usedBindings tracks which local bindings have been referenced.
        // Populated during checkIdent/checkMemberExpr.  After body checking,
        // any binding NOT in this set is reported as unused.
        usedBindings map[string]bool

        // definitelyAssigned tracks which local variables are known to be
        // assigned a non-nil value.  Used for nil-safety warnings (W0200).
        definitelyAssigned map[string]bool

        // assignStack holds snapshots of definitelyAssigned before each
        // pushScope so that popScope can restore the previous state.
        assignStack []map[string]bool

        // closureRetStack holds the return types of enclosing closures.
        // When a return statement is encountered inside a closure, it should
        // be validated against the innermost closure's return type (top of
        // stack), NOT the outer named function's return type (ctx.checked.RetType).
        closureRetStack []TypeDesc
}

// ---------------------------------------------------------------------------
// Checker
// ---------------------------------------------------------------------------

// GenericFnDef stores a generic function template (type parameters + original decl).
// The checker creates monomorphized instances from this template at each call site
// that provides concrete type arguments.
type GenericFnDef struct {
        Name       string
        TypeParams []string // e.g. ["T", "U"]
        Decl       *ast.FnDecl
        File       string
}

// Checker is the semantic analyser.  It is safe for concurrent use of the
// Check method provided that a fresh Checker is created per program.
type Checker struct {
        mu       sync.Mutex // protects handler during error reporting
        handler  *diag.DiagnosticHandler
        program  *ast.Program
        global   *Scope // read-only after collectDecls

        imports    []ImportInfo
        consts     map[string]*CheckedConst
        funcs      map[string]*CheckedFn
        genericFns map[string]*GenericFnDef // generic function templates
        monomorphs map[string]bool          // set of monomorphized instance names already created
        fileList   map[string]*ast.File
}

// NewChecker creates a checker for the given AST program.
func NewChecker(prog *ast.Program, h *diag.DiagnosticHandler) *Checker {
        return &Checker{
                handler:  h,
                program:  prog,
                global:   NewScope(nil),
                consts:      make(map[string]*CheckedConst),
                funcs:       make(map[string]*CheckedFn),
                genericFns:  make(map[string]*GenericFnDef),
                monomorphs:  make(map[string]bool),
                fileList:    make(map[string]*ast.File, len(prog.Files)),
        }
}

// Check runs the full two-pass semantic analysis and returns the checked
// program.  The boolean indicates whether any errors were found.
func (c *Checker) Check() (*CheckedProgram, bool) {
        // Index files by full path and by base name.
        for _, f := range c.program.Files {
                c.fileList[filepath.Clean(f.Path)] = f
                // Also index by base name so that `use utils` can find `utils.yilt`
                // without needing to match the full absolute path.
                c.fileList[filepath.Base(f.Path)] = f
        }

        // Pass 1: collect all top-level declarations and resolve imports.
        c.collectDecls()

        // Pass 1.5: detect struct methods by convention (StructName_methodname).
        c.collectStructMethods()

        // Pass 2: type-check function bodies (potentially concurrent).
        c.checkFunctionBodies()

        // Pass 2.5: detect dead (unused) functions.
        c.checkDeadFunctions()

        // Merge per-function expression-type maps into a single map.
        merged := make(map[ast.Expr]TypeDesc)
        for _, fn := range c.funcs {
                for e, t := range fn.exprTypes {
                        merged[e] = t
                }
        }

        // Collect enum infos from the global scope for the lowerer.
        enumInfos := make(map[string]*EnumInfo)
        c.global.Each(func(name string, b *Binding) {
                if b.Kind == BndType && b.EnumInfo != nil {
                        enumInfos[name] = b.EnumInfo
                }
        })

        result := &CheckedProgram{
                Functions: c.funcs,
                Consts:    c.consts,
                Imports:   c.imports,
                ExprTypes: merged,
                EnumInfos: enumInfos,
        }
        return result, c.handler.HasErrors()
}

// ---------------------------------------------------------------------------
// Pass 1 — declaration collection
// ---------------------------------------------------------------------------

// collectDecls walks every file and registers function signatures and import
// bindings into the global scope.  After this pass the global scope is frozen
// and may be read concurrently.
func (c *Checker) collectDecls() {
        // Register built-in functions that are always available without imports.
        c.registerBuiltins()

        // Register nil as a global constant.
        c.global.Define("nil", &Binding{
                Name:   "nil",
                Kind:   BndVar,
                Type:   GenDesc,
                Mutable: false,
                File:    "<builtin>",
        })

        // Register stdlib modules as implicit globals so that
        // json.encode(), path.join(), sys.args etc. work without
        // explicit 'use' statements.
        for modName := range stdModuleExports {
                c.global.Define(modName, &Binding{
                        Name:   modName,
                        Kind:   BndModule,
                        Type:   GenDesc,
                        Mutable: false,
                        File:    "<stdlib>",
                })
        }

        for _, file := range c.program.Files {
                for _, d := range file.Decls {
                        switch decl := d.(type) {
                        case *ast.ConstDecl:
                                c.registerConst(file.Path, decl)
                        case *ast.FnDecl:
                                c.registerFn(file.Path, decl)
                        case *ast.UseDecl:
                                c.registerUse(file.Path, decl)
                        case *ast.FFIUseDecl:
                                c.registerFFIUse(file.Path, decl)
                        case *ast.StructDecl:
                                c.registerStruct(file.Path, decl)
                        case *ast.EnumDecl:
                                c.registerEnum(file.Path, decl)
                        default:
                                c.handler.Errorf(file.Path, 0, 0, 0,
                                        "internal: unhandled declaration type %T in checker", d)
                        }
                }
        }
}

// collectStructMethods scans all registered functions for the naming convention
// "StructName_methodname" and registers them as methods on the struct type.
// A function qualifies as a method if:
//   - Its name contains an underscore: "StructName_methodname"
//   - The part before the underscore matches a known struct type
//   - The function has at least one parameter
//   - The first parameter's type matches the struct type (or is mutable struct)
func (c *Checker) collectStructMethods() {
        for name, fn := range c.funcs {
                idx := strings.Index(name, "_")
                if idx <= 0 {
                        continue
                }
                structName := name[:idx]
                methodName := name[idx+1:]

                // Look up the struct type
                b := c.global.Lookup(structName)
                if b == nil || b.Kind != BndType || b.StructInfo == nil {
                        continue
                }

                // The function must have at least one parameter whose type is the struct
                if len(fn.Params) == 0 {
                        continue
                }
                firstParamType := fn.Params[0].Type
                // Check if first param type matches the struct (allow mutable wrapper)
                if firstParamType.Kind != TStruct || firstParamType.Name != structName {
                        continue
                }

                // Register this function as a method on the struct.
                // Build the method type WITHOUT the receiver parameter, so that
                // p.distance(other) validates as 1 arg (other), not 2 (self, other).
                methodParams := make([]*TypeDesc, 0, len(fn.Params)-1)
                for i := 1; i < len(fn.Params); i++ {
                        methodParams = append(methodParams, &fn.Params[i].Type)
                }
                mi := &MethodInfo{
                        FuncName: name,
                        FuncType: TypeDesc{
                                Kind:   TFunc,
                                Params: methodParams,
                                Ret:    &fn.RetType,
                        },
                }
                b.StructInfo.Methods[methodName] = mi
        }
}

// registerBuiltins adds globally available built-in functions to the scope.
// These are functions available without any import statement.
func (c *Checker) registerBuiltins() {
        builtins := []struct {
                name    string
                params  []*TypeDesc
                ret     *TypeDesc
        }{
                // Error creation
                {"error", []*TypeDesc{&StrDesc}, &GenDesc},

                // Math functions — operate on numeric types (accept gen for flexibility)
                {"abs", []*TypeDesc{&GenDesc}, &GenDesc},
                {"sqrt", []*TypeDesc{&GenDesc}, &FpDesc},
                {"floor", []*TypeDesc{&GenDesc}, &FpDesc},
                {"ceil", []*TypeDesc{&GenDesc}, &FpDesc},
                {"round", []*TypeDesc{&GenDesc}, &FpDesc},
                {"trunc", []*TypeDesc{&GenDesc}, &FpDesc},
                {"fract", []*TypeDesc{&GenDesc}, &FpDesc},
                {"sign", []*TypeDesc{&GenDesc}, &IntDesc},
                {"min", []*TypeDesc{&GenDesc, &GenDesc}, &GenDesc},
                {"max", []*TypeDesc{&GenDesc, &GenDesc}, &GenDesc},
                {"clamp", []*TypeDesc{&GenDesc, &GenDesc, &GenDesc}, &GenDesc},
                {"pow", []*TypeDesc{&GenDesc, &GenDesc}, &FpDesc},
                {"log", []*TypeDesc{&GenDesc}, &FpDesc},
                {"log2", []*TypeDesc{&GenDesc}, &FpDesc},
                {"log10", []*TypeDesc{&GenDesc}, &FpDesc},
                {"exp", []*TypeDesc{&GenDesc}, &FpDesc},

                // String functions
                {"len", []*TypeDesc{&GenDesc}, &IntDesc},
                {"to_str", []*TypeDesc{&GenDesc}, &StrDesc},
                {"to_int", []*TypeDesc{&GenDesc}, &IntDesc},
                {"to_fp", []*TypeDesc{&GenDesc}, &FpDesc},
                {"char", []*TypeDesc{&IntDesc}, &StrDesc},
                {"ord", []*TypeDesc{&StrDesc}, &IntDesc},
                {"format", []*TypeDesc{&StrDesc, &GenDesc}, &StrDesc},

                // Type checking
                {"typeof", []*TypeDesc{&GenDesc}, &StrDesc},
                {"is_nil", []*TypeDesc{&GenDesc}, &BoolDesc},

                // Table functions
                {"has", []*TypeDesc{&GenDesc, &GenDesc}, &BoolDesc},
                {"keys", []*TypeDesc{&TableDesc}, &TableDesc},
                {"values", []*TypeDesc{&TableDesc}, &TableDesc},

                // I/O convenience (available globally)
                {"print", []*TypeDesc{&GenDesc}, &VoidDesc},
                {"println", []*TypeDesc{&GenDesc}, &VoidDesc},
                {"input", []*TypeDesc{}, &StrDesc},
                {"eprint", []*TypeDesc{&GenDesc}, &VoidDesc},
                {"eprintln", []*TypeDesc{&GenDesc}, &VoidDesc},

                // Higher-order utilities
                {"compose", []*TypeDesc{&GenDesc, &GenDesc}, &GenDesc},
        }

        for _, b := range builtins {
                binding := &Binding{
                        Name:   b.name,
                        Kind:   BndFn,
                        Type:   TypeDesc{Kind: TFunc, Params: b.params, Ret: b.ret},
                        Mutable: false,
                        Public:  false,
                        File:    "<builtin>",
                        Pos:     ast.Pos{},
                }
                c.global.Define(b.name, binding)
        }
}

// registerConst adds a top-level constant to the global scope and const table.
func (c *Checker) registerConst(file string, decl *ast.ConstDecl) {
        if !c.isConstValue(decl.Value) {
                c.errorCodeAt(decl.Span, "E0301", "const value must be a literal (int, float, str, bool, or nil)")
                return
        }

        valType := c.constExprType(decl.Value)

        // Check for duplicate.
        if existing := c.global.Lookup(decl.Name); existing != nil {
                // Allow user consts to shadow builtins with a warning.
                if existing.File == "<builtin>" {
                        c.warnCodeAt(decl.Span, "W0001", "declaration of '%s' shadows builtin (previously declared at %s:%d:%d)",
                                decl.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        delete(c.global.names, decl.Name)
                } else {
                        c.errorCodeAt(decl.Span, "E0801", "redeclaration of '%s' (previously declared at %s:%d:%d)",
                                decl.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        return
                }
        }

        binding := &Binding{
                Name:   decl.Name,
                Kind:   BndConst,
                Type:   valType,
                Mutable: decl.Mutable,
                Public: false,
                File:   file,
                Pos:    decl.Span,
        }
        c.global.Define(decl.Name, binding)
        c.consts[decl.Name] = &CheckedConst{
                Name:  decl.Name,
                Value: decl.Value,
                Type:  valType,
                Pos:   decl.Span,
        }
}

// isConstValue reports whether an expression is a valid compile-time constant.
// Allowed: IntLit, FloatLit, BoolLit, StringLit, NilLit, UnaryOp(-lit), UnaryOp(not lit),
// BinOp on two const values (integer arithmetic, string concatenation).
func (c *Checker) isConstValue(e ast.Expr) bool {
        switch v := e.(type) {
        case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit, *ast.NilLit:
                return true
        case *ast.StructLit:
                for _, f := range v.Fields {
                        if !c.isConstValue(f.Value) {
                                return false
                        }
                }
                return true
        case *ast.TableLit:
                // Allow empty table literal {} as a top-level constant.  This
                // enables the module pattern: `let state = {}` at top level,
                // populated by an init() function at runtime.  Non-empty tables
                // are rejected because their entries may reference other
                // top-level bindings (which would require flow analysis).
                return len(v.Entries) == 0
        case *ast.UnaryOp:
                // Allow -lit and not lit
                if v.Op == ast.TMinus || v.Op == ast.TNot {
                        return c.isConstValue(v.Operand)
                }
        case *ast.BinOp:
                // Allow binary operations on two constant operands
                // (integer arithmetic, string concatenation, comparisons)
                return c.isConstValue(v.Left) && c.isConstValue(v.Right)
        }
        return false
}

// constExprType returns the type of a constant expression without full type checking.
func (c *Checker) constExprType(e ast.Expr) TypeDesc {
        switch v := e.(type) {
        case *ast.IntLit:
                return IntDesc
        case *ast.FloatLit:
                return FpDesc
        case *ast.BoolLit:
                return BoolDesc
        case *ast.StringLit:
                return StrDesc
        case *ast.NilLit:
                return GenDesc
        case *ast.StructLit:
                if b := c.global.Lookup(v.Name); b != nil && b.Kind == BndType {
                        return b.Type
                }
                return GenDesc
        case *ast.TableLit:
                // Empty table literal {} — return TableDesc with gen key/val types
                // so it can be assigned to any table-typed binding.
                return TableDesc
        case *ast.UnaryOp:
                // Derive the type from the operand so that const x = -3.14 gets FpDesc
                // and const y = -42 gets IntDesc, instead of always falling back to GenDesc.
                return c.constExprType(v.Operand)
        }
        return GenDesc
}

// registerFn adds a function signature to the global scope.
func (c *Checker) registerFn(file string, fn *ast.FnDecl) {
        // Generic functions are stored as templates; they are NOT registered
        // in the global scope or funcs map directly.  Monomorphized instances
        // are created on-demand at call sites that provide type arguments.
        if len(fn.TypeParams) > 0 {
                c.genericFns[fn.Name] = &GenericFnDef{
                        Name:       fn.Name,
                        TypeParams: fn.TypeParams,
                        Decl:       fn,
                        File:       file,
                }
                return
        }

        // Functions without an explicit return type annotation start as GenDesc
        // so that recursive calls during body checking don't produce false
        // "void operand" errors.  The actual return type is inferred after
        // the body has been checked (see inferReturnType).
        retType := GenDesc
        if fn.ReturnType != nil {
                retType = c.resolveType(fn.ReturnType)
        }
        // Handle tuple return types: fn foo() (int, str)  [bare, no arrow]
        if len(fn.RetTypes) > 0 {
                elemTypes := make([]*TypeDesc, len(fn.RetTypes))
                for i, rt := range fn.RetTypes {
                        resolved := c.resolveType(&rt)
                        elemTypes[i] = &resolved
                }
                retType = TypeDesc{
                        Kind:   TTuple,
                        Params: elemTypes,
                }
        }
        variadic := false
        if len(fn.Params) > 0 {
                variadic = fn.Params[len(fn.Params)-1].Variadic
                // Enforce: variadic must be the last parameter
                for _, p := range fn.Params[:len(fn.Params)-1] {
                        if p.Variadic {
                                c.reportErrorWithCode(nil, p.Span, "E0301",
                                        "variadic parameter '%s' must be the last parameter", p.Name)
                                variadic = false
                                break
                        }
                }
        }

        // Validate default parameter values and enforce ordering:
        // parameters with defaults must come after all parameters without defaults.
        // Variadic parameters cannot have defaults.
        seenDefault := false
        for _, p := range fn.Params {
                if p.Variadic && p.Default != nil {
                        c.reportErrorWithCode(nil, p.Span, "E0301",
                                "variadic parameter '%s' cannot have a default value", p.Name)
                        continue
                }
                if p.Default != nil {
                        seenDefault = true
                        // Default values must be constant expressions.
                        if !c.isConstValue(p.Default) {
                                c.reportErrorWithCode(nil, p.Span, "E0301",
                                        "default value for parameter '%s' must be a constant expression", p.Name)
                        }
                } else if seenDefault {
                        // Non-default param after a default param is an error.
                        c.reportErrorWithCode(nil, p.Span, "E0301",
                                "parameter '%s' without default value cannot follow parameter with default value", p.Name)
                }
        }

        params := make([]CheckedParam, len(fn.Params))
        for i, p := range fn.Params {
                params[i] = CheckedParam{
                        Name:       p.Name,
                        Type:       c.resolveType(&p.Type),
                        Mutable:    p.Mutable,
                        Variadic:   p.Variadic,
                        HasDefault: p.Default != nil,
                        Pos:        p.Span,
                }
        }

        // Compute numDefaults: count of params with default values.
        numDefaults := 0
        for _, p := range params {
                if p.HasDefault {
                        numDefaults++
                }
        }

        checked := &CheckedFn{
                Name:    fn.Name,
                Params:  params,
                RetType: retType,
                Body:    fn.Body,
                Public:  fn.Public,
                Extern:  fn.Extern,
                Pos:     fn.Span,
                Decl:    fn,
        }

        binding := &Binding{
                Name:   fn.Name,
                Kind:   BndFn,
                Type: TypeDesc{
                        Kind:       TFunc,
                        Params:     make([]*TypeDesc, len(params)),
                        Ret:        &retType,
                        Variadic:   variadic,
                        NumDefaults: numDefaults,
                },
                Mutable: false,
                Public:  fn.Public,
                File:    file,
                Decl:    fn,
                Pos:     fn.Span,
        }
        for i, p := range params {
                binding.Type.Params[i] = &p.Type
        }

        // Check for duplicate.
        if existing := c.global.Lookup(fn.Name); existing != nil {
                // Allow user functions to shadow builtins with a warning.
                if existing.File == "<builtin>" {
                        c.warnCodeAt(fn.Span, "W0001", "declaration of '%s' shadows builtin (previously declared at %s:%d:%d)",
                                fn.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        // Override the builtin by deleting it from the global scope.
                        delete(c.global.names, fn.Name)
                } else if existing.Decl == fn {
                        // The existing binding points to the same declaration (e.g. it was
                        // created by a selective import from another file). Just update the
                        // binding with the full type info from registerFn.
                        delete(c.global.names, fn.Name)
                } else {
                        c.errorCodeAt(fn.Span, "E0801", "redeclaration of '%s' (previously declared at %s:%d:%d)",
                                fn.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        return
                }
        }

        c.global.Define(fn.Name, binding)
        c.funcs[fn.Name] = checked
}

// registerUse handles a use/from declaration for local or stdlib modules.
func (c *Checker) registerUse(file string, u *ast.UseDecl) {
        // Determine the visible name for the module.
        alias := u.Alias
        if alias == "" {
                alias = u.Module
        }

        info := ImportInfo{
                ModuleName: u.Module,
                Alias:      alias,
                File:       file,
                Symbols:    make(map[string]*Binding),
        }

        // Check if a local module file exists before treating as stdlib.
        // Local modules shadow stdlib modules with the same name.
        info.IsStd = lex.IsStdModule(u.Module)
        if info.IsStd {
                for _, ext := range []string{".ylt", ".yilt", ""} {
                        if _, ok := c.fileList[filepath.Clean(u.Module+ext)]; ok {
                                info.IsStd = false
                                break
                        }
                }
        }

        // Selective import: from mod use sym1, sym2
        if len(u.Symbols) > 0 {
                exports := c.resolveModuleExports(u.Module, file)
                for _, sym := range u.Symbols {
                        visibleName := sym.Alias
                        if visibleName == "" {
                                visibleName = sym.Name
                        }
                        b, ok := exports[sym.Name]
                        if !ok {
                                c.errorCodeAt(sym.Span, "E0701", "module '%s' has no symbol '%s'", u.Module, sym.Name)
                                continue
                        }
                        // Check for name clash before defining.
                        if existing := c.global.Lookup(visibleName); existing != nil {
                                // Allow shadowing builtins with a warning.
                                if existing.File == "<builtin>" || existing.File == "<stdlib>" {
                                        c.warnCodeAt(sym.Span, "W0002",
                                                "import '%s' shadows builtin; the imported version will be used",
                                                visibleName)
                                        delete(c.global.names, visibleName)
                                } else if existing.Decl != nil && existing.File == b.File {
                                        // The binding comes from the same module file —
                                        // the selective import is redundant but not an error.
                                        // Skip silently; the function is already accessible.
                                        info.Symbols[visibleName] = existing
                                        continue
                                } else {
                                        c.errorCodeAt(sym.Span, "E0801",
                                                "import '%s' conflicts with existing binding '%s' (declared at %s:%d:%d)",
                                                visibleName, visibleName, existing.File, existing.Pos.Line, existing.Pos.Col)
                                        continue
                                }
                        }
                        // Copy the binding so we can give it the local visible name.
                        local := *b
                        local.Name = visibleName
                        local.Pos = sym.Span
                        if !c.global.Define(visibleName, &local) {
                                // Should not happen since we checked above, but be defensive.
                                c.errorCodeAt(sym.Span, "E0801",
                                        "import '%s' conflicts with existing binding", visibleName)
                                continue
                        }
                        info.Symbols[visibleName] = &local
                }
                c.imports = append(c.imports, info)
                return
        }

        // Whole-module import: use mod [as alias].
        if info.IsStd {
                binding := &Binding{
                        Name:   alias,
                        Kind:   BndModule,
                        Type:   GenDesc,
                        Public: false,
                        File:   file,
                        Pos:    u.Span,
                }
                c.global.Define(alias, binding)
        } else {
                // Local module — try to resolve to a file in the program.
                found := false
                for _, ext := range []string{".ylt", ".yilt", ""} {
                        if _, ok := c.fileList[filepath.Clean(u.Module+ext)]; ok {
                                binding := &Binding{
                                        Name:   alias,
                                        Kind:   BndModule,
                                        Type:   GenDesc,
                                        Public: false,
                                        File:   file,
                                        Pos:    u.Span,
                                }
                                c.global.Define(alias, binding)
                                info.ModulePath = filepath.Clean(u.Module + ext)
                                found = true
                                break
                        }
                }
                if !found {
                        c.errorCodeAt(u.Span, "E0702", "unknown module '%s'", u.Module)
                }
        }
        c.imports = append(c.imports, info)
}

// registerFFIUse handles an FFI import declaration.
func (c *Checker) registerFFIUse(file string, u *ast.FFIUseDecl) {
        info := ImportInfo{
                IsFFI:   true,
                ModulePath: u.Module,
                Alias:   u.Alias,
                File:    file,
                Symbols: make(map[string]*Binding),
        }

        // Selective FFI import: from "ffi:mod" use sym1, sym2
        if len(u.Symbols) > 0 {
                for _, sym := range u.Symbols {
                        visibleName := sym.Alias
                        if visibleName == "" {
                                visibleName = sym.Name
                        }
                        b := &Binding{
                                Name:   visibleName,
                                Kind:   BndFFI,
                                Type:   GenDesc, // FFI symbol types are resolved at link time.
                                File:   file,
                                Pos:    sym.Span,
                        }
                        c.global.Define(visibleName, b)
                        info.Symbols[visibleName] = b
                }
                c.imports = append(c.imports, info)
                return
        }

        // Whole-module FFI import: use "ffi:mod" as alias.
        alias := u.Alias
        if alias == "" {
                alias = u.Module
        }
        binding := &Binding{
                Name:   alias,
                Kind:   BndFFI,
                Type:   GenDesc,
                File:   file,
                Pos:    u.Span,
        }
        c.global.Define(alias, binding)
        info.Alias = alias
        c.imports = append(c.imports, info)
}

// ---------------------------------------------------------------------------
// Generic monomorphization helpers
// ---------------------------------------------------------------------------

// monoName builds a mangled name for a monomorphized function instance.
// Example: identity[int] → "identity$int", swap[int,str] → "swap$int$str"
func monoName(baseName string, typeArgs []TypeDesc) string {
        b := strings.Builder{}
        b.WriteString(baseName)
        b.WriteByte('$')
        for i, ta := range typeArgs {
                if i > 0 {
                        b.WriteByte('$')
                }
                b.WriteString(ta.String())
        }
        return b.String()
}

// typeArgsString formats a slice of TypeRefs as a comma-separated string.
func typeArgsString(args []ast.TypeRef) string {
        parts := make([]string, len(args))
        for i, a := range args {
                parts[i] = a.Name
                if parts[i] == "" {
                        parts[i] = a.Kind.String()
                }
        }
        return strings.Join(parts, ", ")
}

// substituteType replaces type parameter names with concrete types.
// If `t` is TNamed and `name` matches a type param in `subst`, the
// corresponding concrete type is returned.  Otherwise `t` is returned
// unchanged (structural types like int, str, table are never substituted).
func substituteType(t TypeDesc, subst map[string]TypeDesc) TypeDesc {
        if t.Kind == TNamed {
                if concrete, ok := subst[t.Name]; ok {
                        return concrete
                }
        }
        if t.Kind == TFunc {
                // Substitute in parameter and return types.
                newParams := make([]*TypeDesc, len(t.Params))
                for i, p := range t.Params {
                        st := substituteType(*p, subst)
                        newParams[i] = &st
                }
                var newRet *TypeDesc
                if t.Ret != nil {
                        st := substituteType(*t.Ret, subst)
                        newRet = &st
                }
                return TypeDesc{
                        Kind:       TFunc,
                        Params:     newParams,
                        Ret:        newRet,
                        Variadic:   t.Variadic,
                        NumDefaults: t.NumDefaults,
                }
        }
        if t.Kind == TTuple {
                newElems := make([]*TypeDesc, len(t.Params))
                for i, p := range t.Params {
                        st := substituteType(*p, subst)
                        newElems[i] = &st
                }
                return TypeDesc{Kind: TTuple, Params: newElems}
        }
        return t
}

// resolveTypeArg converts an ast.TypeRef (from TypeArgs in a CallExpr)
// to a concrete TypeDesc.  Type parameters (like "T") are resolved as
// TNamed; concrete types (like "int") are resolved normally.
func (c *Checker) resolveTypeArg(ref ast.TypeRef) TypeDesc {
        switch ref.Kind {
        case ast.KindInt:
                return IntDesc
        case ast.KindUint:
                return UintDesc
        case ast.KindFp:
                return FpDesc
        case ast.KindBool:
                return BoolDesc
        case ast.KindStr:
                return StrDesc
        case ast.KindVoid:
                return VoidDesc
        case ast.KindTable:
                return TypeDesc{Kind: TTable}
        case ast.KindNamed:
                // Could be a user-defined type name (struct/enum) or a type parameter.
                // The monomorphize caller will substitute type params later.
                return TypeDesc{Kind: TNamed, Name: ref.Name}
        default:
                return GenDesc
        }
}

// monomorphize creates a monomorphized function instance from a generic template.
// It resolves type parameters, builds the substituted signature, registers the
// instance in c.funcs and c.global, and returns the mangled name.
// If the instance was already created, it returns the existing name.
// This method is safe to call concurrently from multiple goroutines.
func (c *Checker) monomorphize(gdef *GenericFnDef, typeArgs []TypeDesc) string {
        mangled := monoName(gdef.Name, typeArgs)

        // Already created — just return the name.
        if c.monomorphs[mangled] {
                return mangled
        }
        c.monomorphs[mangled] = true

        // Build substitution map: type param name → concrete type.
        subst := make(map[string]TypeDesc, len(gdef.TypeParams))
        for i, tp := range gdef.TypeParams {
                if i < len(typeArgs) {
                        subst[tp] = typeArgs[i]
                } else {
                        subst[tp] = GenDesc // fallback
                }
        }

        // Build substituted parameter types.
        fn := gdef.Decl
        params := make([]CheckedParam, len(fn.Params))
        for i, p := range fn.Params {
                rawType := c.resolveType(&p.Type)
                params[i] = CheckedParam{
                        Name:     p.Name,
                        Type:     substituteType(rawType, subst),
                        Mutable:  p.Mutable,
                        Variadic: p.Variadic,
                        HasDefault: p.Default != nil,
                        Pos:      p.Span,
                }
        }

        // Build substituted return type.
        var retType TypeDesc
        if fn.ReturnType != nil {
                rawRet := c.resolveType(fn.ReturnType)
                retType = substituteType(rawRet, subst)
        } else {
                retType = GenDesc
        }
        if len(fn.RetTypes) > 0 {
                elemTypes := make([]*TypeDesc, len(fn.RetTypes))
                for i, rt := range fn.RetTypes {
                        resolved := substituteType(c.resolveType(&rt), subst)
                        elemTypes[i] = &resolved
                }
                retType = TypeDesc{Kind: TTuple, Params: elemTypes}
        }

        // Validate variadic constraints.
        variadic := false
        if len(fn.Params) > 0 {
                variadic = fn.Params[len(fn.Params)-1].Variadic
        }

        numDefaults := 0
        for _, p := range params {
                if p.HasDefault {
                        numDefaults++
                }
        }

        checked := &CheckedFn{
                Name:    mangled,
                Params:  params,
                RetType: retType,
                Body:    fn.Body, // share the same body AST — type checking will use the right types
                Public:  fn.Public,
                Extern:  fn.Extern,
                Pos:     fn.Span,
                Decl:    fn,
        }

        // Register in the function map and global scope (thread-safe).
        binding := &Binding{
                Name:   mangled,
                Kind:   BndFn,
                Type: TypeDesc{
                        Kind:       TFunc,
                        Params:     make([]*TypeDesc, len(params)),
                        Ret:        &retType,
                        Variadic:   variadic,
                        NumDefaults: numDefaults,
                },
                Mutable: false,
                Public:  fn.Public,
                File:    gdef.File,
                Decl:    fn,
                Pos:     fn.Span,
        }
        for i, p := range params {
                binding.Type.Params[i] = &p.Type
        }
        c.funcs[mangled] = checked
        c.global.Define(mangled, binding)

        return mangled
}

// registerStruct adds a struct type declaration to the global scope.
func (c *Checker) registerStruct(file string, decl *ast.StructDecl) {
        if existing := c.global.Lookup(decl.Name); existing != nil {
                // Allow user structs to shadow builtins with a warning.
                if existing.File == "<builtin>" {
                        c.warnCodeAt(decl.Span, "W0001", "declaration of '%s' shadows builtin (previously declared at %s:%d:%d)",
                                decl.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        delete(c.global.names, decl.Name)
                } else if existing.Decl == decl {
                        delete(c.global.names, decl.Name)
                } else {
                        c.errorCodeAt(decl.Span, "E0801", "redeclaration of '%s' (previously declared at %s:%d:%d)",
                                decl.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        return
                }
        }

        fieldTypes := make(map[string]*TypeDesc, len(decl.Fields))
        fields := make([]StructFieldInfo, len(decl.Fields))
        for i, f := range decl.Fields {
                ft := c.resolveType(&f.Type)
                fieldTypes[f.Name] = &ft
                fields[i] = StructFieldInfo{
                        Name:     f.Name,
                        Type:     ft,
                        Mutable: f.Mutable,
                }
        }

        c.global.Define(decl.Name, &Binding{
                Name:   decl.Name,
                Kind:   BndType,
                Type: TypeDesc{
                        Kind:         TStruct,
                        Name:         decl.Name,
                        StructFields: fieldTypes,
                },
                StructInfo: &StructInfo{
                        Fields:     fields,
                        FieldTypes: fieldTypes,
                        Methods:    make(map[string]*MethodInfo),
                },
                Public:  decl.Public,
                Mutable: false,
                File:    file,
                Pos:     decl.Span,
                Decl:    decl,
        })
}

// registerEnum registers an enum type declaration in the global scope.
func (c *Checker) registerEnum(file string, decl *ast.EnumDecl) {
        if existing := c.global.Lookup(decl.Name); existing != nil {
                if existing.File == "<builtin>" {
                        c.warnCodeAt(decl.Span, "W0001", "declaration of '%s' shadows builtin (previously declared at %s:%d:%d)",
                                decl.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        delete(c.global.names, decl.Name)
                } else {
                        c.errorCodeAt(decl.Span, "E0801", "redeclaration of '%s' (previously declared at %s:%d:%d)",
                                decl.Name, existing.File, existing.Pos.Line, existing.Pos.Col)
                        return
                }
        }

        variantIndex := make(map[string]int, len(decl.Variants))
        variantPayload := make(map[string]*TypeDesc, len(decl.Variants))
        variants := make([]EnumVariantInfo, len(decl.Variants))
        for i, v := range decl.Variants {
                variantIndex[v.Name] = i
                var payloadType *TypeDesc
                if v.Payload != nil {
                        pt := c.resolveType(v.Payload)
                        payloadType = &pt
                }
                variantPayload[v.Name] = payloadType
                variants[i] = EnumVariantInfo{
                        Name:    v.Name,
                        Index:   i,
                        Payload: payloadType,
                }
        }

        c.global.Define(decl.Name, &Binding{
                Name:   decl.Name,
                Kind:   BndType,
                Type: TypeDesc{
                        Kind:              TEnum,
                        Name:              decl.Name,
                        EnumVariants:      variants,
                        EnumVariantIndex:   variantIndex,
                        EnumVariantPayload: variantPayload,
                },
                EnumInfo: &EnumInfo{
                        Variants:      variants,
                        VariantIndex:   variantIndex,
                        VariantPayload: variantPayload,
                },
                Public:  decl.Public,
                Mutable: false,
                File:    file,
                Pos:     decl.Span,
                Decl:    decl,
        })
}

// resolveModuleExports returns the exported symbols for a module name.
func (c *Checker) resolveModuleExports(modName string, fromFile string) map[string]*Binding {
        exports := make(map[string]*Binding)

        // Standard library.
        if lex.IsStdModule(modName) {
                syms, ok := stdModuleExports[modName]
                if !ok {
                        return exports
                }
                for _, s := range syms {
                        exports[s.Name] = &Binding{
                                Name: s.Name,
                                Kind: BndImport,
                                Type: s.Type,
                                File: "stdlib:" + modName,
                        }
                }
                return exports
        }

        // Local module — gather public declarations from the target file.
        for _, ext := range []string{".ylt", ".yilt", ""} {
                cleanPath := filepath.Clean(modName + ext)
                f, ok := c.fileList[cleanPath]
                if !ok {
                        continue
                }
                for _, d := range f.Decls {
                        switch decl := d.(type) {
                        case *ast.FnDecl:
                                if !decl.Public {
                                        continue
                                }
                                // Build a full TFunc type so that selective imports
                                // and module member access can resolve calls with
                                // proper argument count/type checking.
                                paramTypes := make([]*TypeDesc, len(decl.Params))
                                for i, p := range decl.Params {
                                        pt := c.resolveType(&p.Type)
                                        paramTypes[i] = &pt
                                }
                                retType := c.resolveType(decl.ReturnType)
                                exports[decl.Name] = &Binding{
                                        Name: decl.Name, Kind: BndFn,
                                        Type: TypeDesc{
                                                Kind:   TFunc,
                                                Params: paramTypes,
                                                Ret:    &retType,
                                        },
                                        File: f.Path, Decl: decl, Pos: decl.Span,
                                }
                        case *ast.StructDecl:
                                if !decl.Public {
                                        continue
                                }
                                exports[decl.Name] = &Binding{
                                        Name: decl.Name, Kind: BndType,
                                        Type: TypeDesc{Kind: TStruct, Name: decl.Name},
                                        File: f.Path, Decl: decl, Pos: decl.Span,
                                }
                        case *ast.EnumDecl:
                                if !decl.Public {
                                        continue
                                }
                                exports[decl.Name] = &Binding{
                                        Name: decl.Name, Kind: BndType,
                                        Type: TypeDesc{Kind: TEnum, Name: decl.Name},
                                        File: f.Path, Decl: decl, Pos: decl.Span,
                                }
                        case *ast.ConstDecl:
                                // Export constants. Infer a basic type from
                                // the literal value for downstream type checking.
                                constType := GenDesc
                                if decl.Value != nil {
                                        constType = c.inferConstType(decl.Value)
                                }
                                exports[decl.Name] = &Binding{
                                        Name: decl.Name, Kind: BndConst,
                                        Type: constType,
                                        File: f.Path, Decl: decl, Pos: decl.Span,
                                }
                        }
                }
                return exports
        }

        c.handler.Errorf(fromFile, 0, 0, 0, "unknown module '%s'", modName)
        return exports
}

// inferConstType returns a basic TypeDesc for a constant expression
// without full type checking.  Used by resolveModuleExports to give
// exported constants a type.
func (c *Checker) inferConstType(expr ast.Expr) TypeDesc {
        switch e := expr.(type) {
        case *ast.IntLit:
                return IntDesc
        case *ast.FloatLit:
                return FpDesc
        case *ast.StringLit:
                return StrDesc
        case *ast.BoolLit:
                return BoolDesc
        case *ast.NilLit:
                return GenDesc
        case *ast.UnaryOp:
                if e.Op == ast.TMinus {
                        return c.inferConstType(e.Operand)
                }
                if e.Op == ast.TNot {
                        return BoolDesc
                }
        case *ast.BinOp:
                // For binary ops, infer from the left operand (simple heuristic).
                return c.inferConstType(e.Left)
        }
        return GenDesc
}

// resolveType converts an AST TypeRef into a TypeDesc.
func (c *Checker) resolveType(ref *ast.TypeRef) TypeDesc {
        if ref == nil {
                return VoidDesc
        }
        switch ref.Kind {
        case ast.KindVoid:
                return VoidDesc
        case ast.KindInt:
                return IntDesc
        case ast.KindUint:
                return UintDesc
        case ast.KindFp:
                return FpDesc
        case ast.KindBool:
                return BoolDesc
        case ast.KindStr:
                return StrDesc
        case ast.KindTable:
                return TableDesc
        case ast.KindGen:
                return GenDesc
        case ast.KindNamed:
                if b := c.global.Lookup(ref.Name); b != nil && b.Kind == BndType {
                        return b.Type
                }
                return TypeDesc{Kind: TNamed, Name: ref.Name}
        default:
                return GenDesc
        }
}

// ---------------------------------------------------------------------------
// Pass 2 — concurrent function body checking
// ---------------------------------------------------------------------------

// checkFunctionBodies type-checks every non-extern function body.
// Multiple rounds may be needed because checking a body can trigger
// monomorphization of generic functions (creating new entries in c.funcs).
func (c *Checker) checkFunctionBodies() {
        // We may need multiple rounds: checking a function body can trigger
        // monomorphization of generic functions, which creates new entries in
        // c.funcs that also need their bodies checked.
        // We run sequentially to avoid data races on c.funcs/c.global
        // when goroutines call monomorphize concurrently.
        checked := make(map[string]bool)

        for {
                newFuncs := false
                for name, fn := range c.funcs {
                        if checked[name] {
                                continue
                        }
                        checked[name] = true
                        if fn.Extern {
                                continue
                        }
                        // Look up the original AST declaration to get the body.
                        decl := c.findFnDeclForMono(name)
                        if decl == nil {
                                continue
                        }
                        ctx := &fnCtx{
                                checker:           c,
                                decl:              decl,
                                checked:           fn,
                                scopes:            nil,
                                loopDepth:         0,
                                exprTypes:         make(map[ast.Expr]TypeDesc),
                                hasReturn:         false,
                                usedBindings:      make(map[string]bool),
                                definitelyAssigned: make(map[string]bool),
                        }
                        c.checkFnBody(ctx)
                        fn.exprTypes = ctx.exprTypes
                }

                // Check if any new functions were added during body checking.
                for name := range c.funcs {
                        if !checked[name] {
                                newFuncs = true
                                break
                        }
                }
                if !newFuncs {
                        break
                }
        }
}

// findFnDeclForMono retrieves the original AST FnDecl for a function.
// For monomorphized functions (name contains '$'), it strips the type
// suffix and looks up the generic template's decl.
func (c *Checker) findFnDeclForMono(name string) *ast.FnDecl {
        // Fast path: direct name match.
        for _, file := range c.program.Files {
                for _, d := range file.Decls {
                        if fn, ok := d.(*ast.FnDecl); ok && fn.Name == name {
                                return fn
                        }
                }
        }
        // Slow path: mangled name (e.g. "identity$int").
        // Strip everything after the first '$' to get the generic base name.
        idx := strings.Index(name, "$")
        if idx > 0 {
                baseName := name[:idx]
                for _, file := range c.program.Files {
                        for _, d := range file.Decls {
                                if fn, ok := d.(*ast.FnDecl); ok && fn.Name == baseName {
                                        return fn
                                }
                        }
                }
        }
        return nil
}

// findFnDecl retrieves the original AST declaration for a named function.
func (c *Checker) findFnDecl(name string) *ast.FnDecl {
        for _, file := range c.program.Files {
                for _, d := range file.Decls {
                        if fn, ok := d.(*ast.FnDecl); ok && fn.Name == name {
                                return fn
                        }
                }
        }
        return nil
}

// checkFnBody type-checks the body of a single function.
func (c *Checker) checkFnBody(ctx *fnCtx) {
        // Create the function scope and bind parameters.
        c.pushScope(ctx)

        for _, p := range ctx.checked.Params {
                pType := p.Type
                // Variadic parameters receive a table at runtime (int-keyed).
                if p.Variadic {
                        pType = TypeDesc{Kind: TTable}
                }
                b := &Binding{
                        Name:    p.Name,
                        Kind:    BndVar,
                        Type:    pType,
                        Mutable: p.Mutable,
                        Pos:     p.Pos,
                }
                c.define(ctx, p.Name, b)
                c.markAssigned(ctx, p.Name) // parameters are always initialised
        }

        // Check each statement in the body.
        for _, s := range ctx.decl.Body {
                c.checkStmt(ctx, s)
        }

        // Check for unused local variables before popping the body scope.
        // This must happen while all scopes are still on the stack so that
        // checkUnusedVars can iterate body-local bindings.
        if ctx.usedBindings != nil {
                c.checkUnusedVars(ctx)
        }

        c.popScope(ctx)

        // Infer return type: if the function was declared without an explicit
        // return type annotation (GenDesc) but contains return statements
        // with values, infer the return type from the body.
        if ctx.checked.RetType.Kind == TGen && ctx.hasReturn {
                if inferred := c.inferReturnType(ctx); inferred.Kind != TVoid {
                        ctx.checked.RetType = inferred
                        // Update the binding in the global scope so subsequent calls
                        // to this function see the inferred type.
                        c.updateFnReturnType(ctx.decl.Name, inferred)
                }
                return
        }

        // Warn about missing return in non-void, non-generic functions.
        if !ctx.hasReturn && ctx.checked.RetType.Kind != TGen && !ctx.checked.RetType.IsVoid() && !ctx.checked.Extern {
                c.reportErrorWithCode(ctx, ctx.decl.Pos(), "E0402",
                        "function '%s' does not return a value on all paths", ctx.decl.Name)
        }
}

// inferReturnType scans the function body recursively for return
// statements with values and returns the inferred return type.  It walks
// into if/while/for/match bodies so that returns inside nested blocks are
// found.  If multiple return statements have different concrete types, an
// error is reported.  Falls back to the type of the first return with a
// concrete type (or VoidDesc if none found).
func (c *Checker) inferReturnType(ctx *fnCtx) TypeDesc {
        first := GenDesc // start as generic so the first concrete type wins
        c.collectReturnTypes(ctx, ctx.decl.Body, &first)
        if first.Kind != TGen {
                return first
        }
        return VoidDesc
}

// collectReturnTypes walks a list of statements recursively, collecting
// the types of return values and checking consistency.
func (c *Checker) collectReturnTypes(ctx *fnCtx, stmts []ast.Stmt, first *TypeDesc) {
        for _, s := range stmts {
                switch stmt := s.(type) {
                case *ast.ReturnStmt:
                        if stmt.Value == nil {
                                continue
                        }
                        t, ok := ctx.exprTypes[stmt.Value]
                        if !ok {
                                continue
                        }
                        if first.Kind == TGen && t.Kind != TGen {
                                *first = t
                                continue
                        }
                        // Both are concrete — check consistency.
                        if first.Kind != TGen && t.Kind != TGen && !first.Equals(t) {
                                c.reportErrorWithCode(ctx, stmt.Value.Pos(), "E0401",
                                        "inconsistent return type '%s'; function previously returned '%s'",
                                        t.String(), first.String())
                        }
                case *ast.IfStmt:
                        for _, br := range stmt.Branches {
                                c.collectReturnTypes(ctx, br.Body, first)
                        }
                        c.collectReturnTypes(ctx, stmt.Else, first)
                case *ast.WhileStmt:
                        c.collectReturnTypes(ctx, stmt.Body, first)
                case *ast.ForStmt:
                        c.collectReturnTypes(ctx, stmt.Body, first)
                case *ast.MatchStmt:
                        for _, cs := range stmt.Cases {
                                c.collectReturnTypes(ctx, cs.Body, first)
                        }
                        c.collectReturnTypes(ctx, stmt.Default, first)
                }
        }
}

// updateFnReturnType updates the return type in the global scope binding
// for the named function.  This must be called from the goroutine that
// type-checked the function (before the merge step).
func (c *Checker) updateFnReturnType(name string, ret TypeDesc) {
        c.mu.Lock()
        defer c.mu.Unlock()
        if b, ok := c.global.names[name]; ok {
                b.Type.Ret = &ret
        }
}

// ---------------------------------------------------------------------------
// Scope management (per fnCtx)
// ---------------------------------------------------------------------------

func (c *Checker) pushScope(ctx *fnCtx) {
        // Snapshot the current definitelyAssigned map so we can restore it on pop.
        if ctx.definitelyAssigned != nil {
                snapshot := make(map[string]bool, len(ctx.definitelyAssigned))
                for k, v := range ctx.definitelyAssigned {
                        snapshot[k] = v
                }
                ctx.assignStack = append(ctx.assignStack, snapshot)
        }

        if len(ctx.scopes) == 0 {
                ctx.scopes = append(ctx.scopes, c.global)
        }
        parent := ctx.scopes[len(ctx.scopes)-1]
        ctx.scopes = append(ctx.scopes, NewScope(parent))
}

func (c *Checker) popScope(ctx *fnCtx) {
        if len(ctx.scopes) > 1 {
                ctx.scopes = ctx.scopes[:len(ctx.scopes)-1]
        }
        // Restore the definitelyAssigned snapshot from before the matching pushScope.
        if len(ctx.assignStack) > 0 {
                ctx.definitelyAssigned = ctx.assignStack[len(ctx.assignStack)-1]
                ctx.assignStack = ctx.assignStack[:len(ctx.assignStack)-1]
        }
}

func (c *Checker) currentScope(ctx *fnCtx) *Scope {
        if len(ctx.scopes) == 0 {
                return c.global
        }
        return ctx.scopes[len(ctx.scopes)-1]
}

func (c *Checker) define(ctx *fnCtx, name string, b *Binding) {
        s := c.currentScope(ctx)
        if !s.Define(name, b) {
                c.reportErrorWithCode(ctx, b.Pos, "E0801", "redeclaration of '%s' in the same scope", name)
        }
}

func (c *Checker) lookup(ctx *fnCtx, name string) (*Binding, bool) {
        b := c.currentScope(ctx).Lookup(name)
        return b, b != nil
}

// isDefinitelyAssigned reports whether a local variable is known to have been
// assigned a non-nil value.  Globals, parameters, and non-variable bindings
// are always considered assigned.  Returns true if the variable is not found
// (avoids false positives on unresolved names).
func (c *Checker) isDefinitelyAssigned(ctx *fnCtx, name string) bool {
        b, found := c.lookup(ctx, name)
        if !found || b.Kind != BndVar {
                return true // not a local var — always "assigned"
        }
        return ctx.definitelyAssigned[name]
}

// markAssigned records that a local variable has been definitely assigned.
func (c *Checker) markAssigned(ctx *fnCtx, name string) {
        ctx.definitelyAssigned[name] = true
}

// ---------------------------------------------------------------------------
// Statement checking
// ---------------------------------------------------------------------------

func (c *Checker) checkStmt(ctx *fnCtx, s ast.Stmt) {
        if s == nil {
                return
        }
        // Detect unreachable code after return, break, or continue.
        if ctx.unreachable {
                c.reportErrorWithCode(ctx, s.Pos(), "E0901", "unreachable code after return, break, or continue")
                return // don't cascade errors on subsequent statements
        }
        switch stmt := s.(type) {
        case *ast.ConstStmt:
                c.checkConstStmtLocal(ctx, stmt)
        case *ast.AssertStmt:
                c.checkAssertStmt(ctx, stmt)
        case *ast.LetStmt:
                c.checkLetStmt(ctx, stmt)
        case *ast.ExprStmt:
                c.checkExprStmt(ctx, stmt)
        case *ast.ReturnStmt:
                c.checkReturnStmt(ctx, stmt)
        case *ast.IfStmt:
                c.checkIfStmt(ctx, stmt)
        case *ast.WhileStmt:
                c.checkWhileStmt(ctx, stmt)
        case *ast.ForStmt:
                c.checkForStmt(ctx, stmt)
        case *ast.MatchStmt:
                c.checkMatchStmt(ctx, stmt)
        case *ast.BreakStmt:
                c.checkBreakStmt(ctx, stmt)
        case *ast.ContinueStmt:
                c.checkContinueStmt(ctx, stmt)
        case *ast.TupleDestructStmt:
                c.checkTupleDestructStmt(ctx, stmt)
        default:
                c.reportError(ctx, s.Pos(), "unknown statement type")
        }
}

// checkConstStmtLocal checks a local const statement (inside function bodies).
func (c *Checker) checkConstStmtLocal(ctx *fnCtx, s *ast.ConstStmt) {
        if !c.isConstValue(s.Value) {
                c.reportErrorWithCode(ctx, s.Pos(), "E0301", "const value must be a literal (int, float, str, bool, or nil)")
                return
        }
        valType := c.checkExpr(ctx, s.Value)
        b := &Binding{
                Name:   s.Name,
                Kind:   BndConst,
                Type:   valType,
                Mutable: false,
                Pos:    s.Span,
        }
        c.define(ctx, s.Name, b)
}

// checkAssertStmt checks an assert statement.
func (c *Checker) checkAssertStmt(ctx *fnCtx, s *ast.AssertStmt) {
        condType := c.checkExpr(ctx, s.Cond)
        if condType.IsConcrete() && condType.Kind != TBool && condType.Kind != TGen {
                c.reportError(ctx, s.Cond.Pos(), "assert condition must be bool, got '%s'", condType.String())
        }
        if s.Message != nil {
                msgType := c.checkExpr(ctx, s.Message)
                if msgType.IsConcrete() && msgType.Kind != TStr && msgType.Kind != TGen {
                        c.reportError(ctx, s.Message.Pos(), "assert message must be a string, got '%s'", msgType.String())
                }
        }
}

func (c *Checker) checkLetStmt(ctx *fnCtx, s *ast.LetStmt) {
        // Special case: if the value is a FnExpr, define the binding first
        // so the function can call itself recursively.
        if fnExpr, ok := s.Value.(*ast.FnExpr); ok {
                // Pre-define the binding with a generic type so the function
                // body can reference itself for recursion.
                preBinding := &Binding{
                        Name:    s.Name,
                        Kind:    BndFn,
                        Type:    GenDesc,
                        Mutable: s.Mutable,
                        Pos:     s.Pos(),
                }
                c.define(ctx, s.Name, preBinding)
                c.markAssigned(ctx, s.Name) // FnExpr initialiser counts as assignment

                valType := c.checkFnExpr(ctx, fnExpr)

                // Store the FnExpr type in the expression type map so that
                // downstream passes (ownership analysis) can look it up.
                ctx.exprTypes[fnExpr] = valType

                // If an explicit type annotation is present, check compatibility.
                if s.Type != nil {
                        annotated := c.resolveType(s.Type)
                        if annotated.IsConcrete() && valType.IsConcrete() && !c.assignable(annotated, valType) {
                                c.reportErrorWithCode(ctx, s.Pos(), "E0201",
                                        "cannot assign '%s' to '%s'", valType.String(), annotated.String())
                        }
                        valType = annotated
                }

                // Update the pre-defined binding with the actual type.
                preBinding.Type = valType
                return
        }

        valType := c.checkExpr(ctx, s.Value)

        // Mark as definitely assigned — it has an initialiser.
        c.markAssigned(ctx, s.Name)

        // If an explicit type annotation is present, check compatibility.
        if s.Type != nil {
                annotated := c.resolveType(s.Type)
                if annotated.IsConcrete() && valType.IsConcrete() && !c.assignable(annotated, valType) {
                        c.reportErrorWithCode(ctx, s.Pos(), "E0201",
                                "cannot assign '%s' to '%s'", valType.String(), annotated.String())
                }
                valType = annotated
        }

        b := &Binding{
                Name:    s.Name,
                Kind:    BndVar,
                Type:    valType,
                Mutable: s.Mutable,
                Pos:     s.Pos(),
        }
        c.define(ctx, s.Name, b)
}

func (c *Checker) checkExprStmt(ctx *fnCtx, s *ast.ExprStmt) {
        c.checkExpr(ctx, s.Expr)
}

func (c *Checker) checkReturnStmt(ctx *fnCtx, s *ast.ReturnStmt) {
        ctx.hasReturn = true
        ctx.unreachable = true

        // Determine the expected return type.  If we're inside a closure,
        // use the innermost closure's return type (from the stack), not
        // the outer named function's return type.
        var expectedRet TypeDesc
        if len(ctx.closureRetStack) > 0 {
                expectedRet = ctx.closureRetStack[len(ctx.closureRetStack)-1]
        } else {
                expectedRet = ctx.checked.RetType
        }

        if s.Value == nil {
                if !expectedRet.IsVoid() && expectedRet.Kind != TGen {
                        c.reportError(ctx, s.Pos(),
                                "empty return in %s expecting '%s'",
                                closureOrFnName(ctx), expectedRet.String())
                }
                return
        }

        valType := c.checkExpr(ctx, s.Value)

        // When the target was declared without an explicit return type
        // (gen by default) but has a return with a value, we are in
        // inference mode.  Skip the type mismatch check — it will be
        // resolved after the body has been fully checked.
        if expectedRet.Kind == TGen {
                return
        }

        if expectedRet.IsConcrete() && valType.IsConcrete() &&
                !c.assignable(expectedRet, valType) {
                c.reportError(ctx, s.Pos(),
                        "return type '%s' does not match %s return type '%s'",
                        valType.String(), closureOrFnName(ctx), expectedRet.String())
        }
}

// closureOrFnName returns "<closure>" if the return is inside a closure,
// otherwise the enclosing function's name.
func closureOrFnName(ctx *fnCtx) string {
        if len(ctx.closureRetStack) > 0 {
                return "<closure>"
        }
        return ctx.decl.Name
}

func (c *Checker) checkIfStmt(ctx *fnCtx, s *ast.IfStmt) {
        allUnreachable := true
        for _, br := range s.Branches {
                condType := c.checkExpr(ctx, br.Cond)
                if condType.IsConcrete() && condType.Kind != TBool && condType.Kind != TGen {
                        c.reportErrorWithCode(ctx, br.Cond.Pos(), "E0601",
                                "if condition must be bool, got '%s'", condType.String())
                }
                c.pushScope(ctx)
                saved := ctx.unreachable
                ctx.unreachable = false
                for _, st := range br.Body {
                        c.checkStmt(ctx, st)
                }
                branchUnreachable := ctx.unreachable
                ctx.unreachable = saved
                c.popScope(ctx)
                if !branchUnreachable {
                        allUnreachable = false
                }
        }

        // W0400: warn about dead branches when the condition is a compile-time constant.
        c.checkDeadCodeIfBranch(ctx, s)

        if len(s.Else) > 0 {
                c.pushScope(ctx)
                saved := ctx.unreachable
                ctx.unreachable = false
                for _, st := range s.Else {
                        c.checkStmt(ctx, st)
                }
                elseUnreachable := ctx.unreachable
                ctx.unreachable = saved
                c.popScope(ctx)
                if !elseUnreachable {
                        allUnreachable = false
                }
        } else {
                allUnreachable = false // no else means not all paths return
        }

        // If all branches are unreachable, subsequent code is unreachable.
        if allUnreachable {
                ctx.unreachable = true
        }
}

func (c *Checker) checkWhileStmt(ctx *fnCtx, s *ast.WhileStmt) {
        condType := c.checkExpr(ctx, s.Cond)
        if condType.IsConcrete() && condType.Kind != TBool && condType.Kind != TGen {
                c.reportErrorWithCode(ctx, s.Cond.Pos(), "E0601",
                        "while condition must be bool, got '%s'", condType.String())
        }
        c.pushScope(ctx)
        saved := ctx.unreachable
        ctx.unreachable = false
        ctx.loopDepth++
        for _, st := range s.Body {
                c.checkStmt(ctx, st)
        }
        ctx.unreachable = saved // while body doesn't make outer code unreachable
        ctx.loopDepth--
        c.popScope(ctx)
}

// checkRangeForStmt type-checks a range for-loop: for i in low..high.
func (c *Checker) checkRangeForStmt(ctx *fnCtx, s *ast.ForStmt) {
        rng := s.Over.(*ast.RangeExpr)
        // checkExpr on the RangeExpr will validate bounds types.
        c.checkExpr(ctx, rng)

        if s.Value != "" {
                c.reportError(ctx, s.Pos(),
                        "range for-loop does not support key-value iteration; use 'for i in 0..N'")
        }

        c.pushScope(ctx)
        saved := ctx.unreachable
        ctx.unreachable = false
        ctx.loopDepth++

        c.define(ctx, s.Key, &Binding{
                Name:    s.Key,
                Kind:    BndVar,
                Type:    IntDesc,
                Mutable: false,
                Pos:     s.Pos(),
        })

        for _, st := range s.Body {
                c.checkStmt(ctx, st)
        }
        ctx.unreachable = saved
        ctx.loopDepth--
        c.popScope(ctx)
}

func (c *Checker) checkForStmt(ctx *fnCtx, s *ast.ForStmt) {
        // Check if this is a range for-loop: for i in low..high
        if _, ok := s.Over.(*ast.RangeExpr); ok {
                c.checkRangeForStmt(ctx, s)
                return
        }

        iterType := c.checkExpr(ctx, s.Over)
        if iterType.IsConcrete() && iterType.Kind != TTable && iterType.Kind != TStr && iterType.Kind != TGen {
                c.reportError(ctx, s.Over.Pos(),
                        "for-in requires a table or string, got '%s'", iterType.String())
        }

        c.pushScope(ctx)
        saved := ctx.unreachable
        ctx.unreachable = false
        ctx.loopDepth++

        // Determine key/value types based on the iterable type.
        keyType := GenDesc
        valType := GenDesc
        if iterType.Kind == TTable {
                if iterType.KeyType != nil {
                        keyType = *iterType.KeyType
                }
                if iterType.ValType != nil {
                        valType = *iterType.ValType
                }
        } else if iterType.Kind == TStr {
                // String iteration: key is the byte index (int), value is undefined.
                keyType = IntDesc
        }

        c.define(ctx, s.Key, &Binding{
                Name:    s.Key,
                Kind:    BndVar,
                Type:    keyType,
                Mutable: false,
                Pos:     s.Pos(),
        })
        if s.Value != "" {
                c.define(ctx, s.Value, &Binding{
                        Name:    s.Value,
                        Kind:    BndVar,
                        Type:    valType,
                        Mutable: false,
                        Pos:     s.Pos(),
                })
        }

        for _, st := range s.Body {
                c.checkStmt(ctx, st)
        }
        ctx.unreachable = saved // for body doesn't make outer code unreachable
        ctx.loopDepth--
        c.popScope(ctx)
}

func (c *Checker) checkMatchStmt(ctx *fnCtx, s *ast.MatchStmt) {
        subjectType := c.checkExpr(ctx, s.Subject)
        allUnreachable := true
        seen := make(map[string]int) // key -> first case index

        for i, cs := range s.Cases {
                caseType := c.checkExpr(ctx, cs.Value)
                // Case value type should be compatible with the subject type.
                if subjectType.IsConcrete() && caseType.IsConcrete() &&
                        !c.assignable(subjectType, caseType) && !c.assignable(caseType, subjectType) {
                        c.reportError(ctx, cs.Value.Pos(),
                                "match case type '%s' is not compatible with subject type '%s'",
                                caseType.String(), subjectType.String())
                }
                // Duplicate pattern detection.
                if key := matchCaseKey(cs.Value); key != "" {
                        if prevIdx, dup := seen[key]; dup {
                                c.reportError(ctx, cs.Value.Pos(),
                                        "duplicate match case '%s' (previously matched on case %d)",
                                        key, prevIdx+1)
                        } else {
                                seen[key] = i
                        }
                }
                c.pushScope(ctx)
                saved := ctx.unreachable
                ctx.unreachable = false
                for _, st := range cs.Body {
                        c.checkStmt(ctx, st)
                }
                if !ctx.unreachable {
                        allUnreachable = false
                }
                ctx.unreachable = saved
                c.popScope(ctx)
        }

        // Enum exhaustiveness: if matching on an enum type and NOT all variants
        // are covered and no default branch exists, warn about non-exhaustiveness.
        if subjectType.Kind == TEnum {
                if b := c.global.Lookup(subjectType.Name); b != nil && b.EnumInfo != nil {
                        ei := b.EnumInfo
                        covered := 0
                        for _, v := range ei.Variants {
                                for _, cs := range s.Cases {
                                        if isEnumCaseVariant(cs.Value, subjectType.Name, v.Name) {
                                                covered++
                                                break
                                        }
                                }
                        }
                        if covered < len(ei.Variants) && len(s.Default) == 0 {
                                // Find the first missing variant for the error message.
                                var missing string
                                for _, v := range ei.Variants {
                                        found := false
                                        for _, cs := range s.Cases {
                                                if isEnumCaseVariant(cs.Value, subjectType.Name, v.Name) {
                                                        found = true
                                                        break
                                                }
                                        }
                                        if !found {
                                                missing = v.Name
                                                break
                                        }
                                }
                                c.reportWarnWithCode(ctx, s.Span, "W0500",
                                        "match on enum '%s' is not exhaustive — missing variant '%s'",
                                        subjectType.Name, missing)
                        }
                }
        }

        if len(s.Default) > 0 {
                c.pushScope(ctx)
                saved := ctx.unreachable
                ctx.unreachable = false
                for _, st := range s.Default {
                        c.checkStmt(ctx, st)
                }
                if !ctx.unreachable {
                        allUnreachable = false
                }
                ctx.unreachable = saved
                c.popScope(ctx)
        } else {
                allUnreachable = false
        }

        if allUnreachable {
                ctx.unreachable = true
        }
}

func (c *Checker) checkBreakStmt(ctx *fnCtx, s *ast.BreakStmt) {
        if ctx.loopDepth == 0 {
                c.reportErrorWithCode(ctx, s.Pos(), "E0501", "break outside of a loop")
        }
        ctx.unreachable = true
}

func (c *Checker) checkContinueStmt(ctx *fnCtx, s *ast.ContinueStmt) {
        if ctx.loopDepth == 0 {
                c.reportErrorWithCode(ctx, s.Pos(), "E0502", "continue outside of a loop")
        }
        ctx.unreachable = true
}

// checkIncrDecrExpr type-checks an increment/decrement expression.
// The operand must be a mutable identifier of numeric type.
func (c *Checker) checkIncrDecrExpr(ctx *fnCtx, n *ast.IncrDecrExpr) TypeDesc {
        ident, ok := n.Operand.(*ast.Ident)
        if !ok {
                c.reportError(ctx, n.Pos(), "increment/decrement operand must be a variable")
                return IntDesc
        }

        b, found := c.lookup(ctx, ident.Name)
        if !found {
                c.reportErrorWithCode(ctx, n.Pos(), "E0101", "undefined identifier '%s'", ident.Name)
                return IntDesc
        }
        if !b.Mutable {
                c.reportErrorWithCode(ctx, n.Pos(), "E0202", "'%s' is immutable; use 'mut' to declare mutable bindings", ident.Name)
        }

        if b.Type.IsConcrete() && !b.Type.IsNumeric() && b.Type.Kind != TGen {
                c.reportError(ctx, n.Pos(), "increment/decrement requires a numeric type, got '%s'", b.Type.String())
        }

        // Mark binding as used
        if ctx.usedBindings != nil {
                ctx.usedBindings[ident.Name] = true
        }

        return b.Type
}

// checkRangeExpr type-checks a range expression: low..high.
// Both operands must be integers. The result type is int (used as an iterator range).
func (c *Checker) checkRangeExpr(ctx *fnCtx, n *ast.RangeExpr) TypeDesc {
        lowType := c.checkExpr(ctx, n.Low)
        highType := c.checkExpr(ctx, n.High)

        if lowType.IsConcrete() && !lowType.IsNumeric() && lowType.Kind != TGen {
                c.reportError(ctx, n.Low.Pos(), "range lower bound must be numeric, got '%s'", lowType.String())
        }
        if highType.IsConcrete() && !highType.IsNumeric() && highType.Kind != TGen {
                c.reportError(ctx, n.High.Pos(), "range upper bound must be numeric, got '%s'", highType.String())
        }

        return IntDesc
}

// checkTupleExpr type-checks a tuple expression.
func (c *Checker) checkTupleExpr(ctx *fnCtx, n *ast.TupleExpr) TypeDesc {
        var elemTypes []*TypeDesc
        for _, elt := range n.Elts {
                t := c.checkExpr(ctx, elt)
                tt := t
                elemTypes = append(elemTypes, &tt)
        }
        return TypeDesc{
                Kind:     TTuple,
                Params:   elemTypes, // reuse Params field for tuple element types
        }
}

// checkTupleDestructStmt type-checks a tuple destructuring let statement.
func (c *Checker) checkTupleDestructStmt(ctx *fnCtx, s *ast.TupleDestructStmt) {
        valType := c.checkExpr(ctx, s.Value)

        // Resolve element types from the tuple value's type when available.
        var elemTypes []*TypeDesc
        if valType.Kind == TTuple && len(valType.Params) > 0 {
                elemTypes = valType.Params
        }

        for i, name := range s.Names {
                var typ TypeDesc
                if i < len(elemTypes) && elemTypes[i] != nil {
                        typ = *elemTypes[i]
                } else {
                        typ = GenDesc
                }
                b := &Binding{
                        Name:    name,
                        Kind:    BndVar,
                        Type:    typ,
                        Mutable: s.Mutable,
                        Pos:     s.Pos(),
                }
                c.define(ctx, name, b)
                c.markAssigned(ctx, name)
        }

        // Error if the number of names doesn't match the tuple arity.
        if valType.Kind == TTuple && len(valType.Params) != len(s.Names) {
                c.reportError(ctx, s.Span,
                        "tuple destructuring: expected %d values, got %d",
                        len(valType.Params), len(s.Names))
        }
}

// ---------------------------------------------------------------------------
// Expression checking
// ---------------------------------------------------------------------------

// checkExpr resolves and annotates the type of an expression.
func (c *Checker) checkExpr(ctx *fnCtx, e ast.Expr) TypeDesc {
        if e == nil {
                return VoidDesc
        }

        var t TypeDesc
        switch expr := e.(type) {
        case *ast.IntLit:
                t = c.checkIntLit(ctx, expr)
        case *ast.FloatLit:
                t = c.checkFloatLit(ctx, expr)
        case *ast.StringLit:
                t = c.checkStringLit(ctx, expr)
        case *ast.BoolLit:
                t = c.checkBoolLit(ctx, expr)
        case *ast.NilLit:
                t = c.checkNilLit(ctx, expr)
        case *ast.Ident:
                t = c.checkIdent(ctx, expr)
        case *ast.BinOp:
                t = c.checkBinOp(ctx, expr)
        case *ast.UnaryOp:
                t = c.checkUnaryOp(ctx, expr)
        case *ast.CallExpr:
                t = c.checkCallExpr(ctx, expr)
        case *ast.IndexExpr:
                t = c.checkIndexExpr(ctx, expr)
        case *ast.MemberExpr:
                t = c.checkMemberExpr(ctx, expr)
        case *ast.TableLit:
                t = c.checkTableLit(ctx, expr)
        case *ast.StructLit:
                t = c.checkStructLit(ctx, expr)
        case *ast.EnumLit:
                t = c.checkEnumLit(ctx, expr)
        case *ast.EnumMatchPattern:
                t = c.checkEnumMatchPattern(ctx, expr)
        case *ast.AssignExpr:
                t = c.checkAssignExpr(ctx, expr)
        case *ast.IndexAssignExpr:
                t = c.checkIndexAssignExpr(ctx, expr)
        case *ast.MemberAssignExpr:
                t = c.checkMemberAssignExpr(ctx, expr)
        case *ast.ErrorPropExpr:
                t = c.checkErrorPropExpr(ctx, expr)
        case *ast.SpawnExpr:
                t = c.checkSpawnExpr(ctx, expr)
        case *ast.AwaitExpr:
                t = c.checkAwaitExpr(ctx, expr)
        case *ast.FnExpr:
                t = c.checkFnExpr(ctx, expr)
        case *ast.IfExpr:
                t = c.checkIfExpr(ctx, expr)
        case *ast.MatchExpr:
                t = c.checkMatchExpr(ctx, expr)
        case *ast.InterpStr:
                t = c.checkInterpStr(ctx, expr)
        case *ast.IncrDecrExpr:
                t = c.checkIncrDecrExpr(ctx, expr)
        case *ast.RangeExpr:
                t = c.checkRangeExpr(ctx, expr)
        case *ast.TupleExpr:
                t = c.checkTupleExpr(ctx, expr)
        case *ast.TypeArgIdent:
                // TypeArgIdent is an intermediate parse node that should have been
                // resolved to a CallExpr or EnumLit by the postfix parser.
                // If it reaches here, it's a standalone type-arg identifier (error).
                c.reportError(ctx, expr.Pos(),
                        "type arguments '%s[%s]' cannot be used as a standalone expression",
                        expr.Name, typeArgsString(expr.TypeArgs))
                t = GenDesc
        default:
                c.reportError(ctx, e.Pos(), "unknown expression type")
                t = GenDesc
        }

        ctx.exprTypes[e] = t
        return t
}

func (c *Checker) checkIntLit(ctx *fnCtx, n *ast.IntLit) TypeDesc {
        return IntDesc
}

func (c *Checker) checkFloatLit(ctx *fnCtx, n *ast.FloatLit) TypeDesc {
        return FpDesc
}

func (c *Checker) checkStringLit(ctx *fnCtx, n *ast.StringLit) TypeDesc {
        return StrDesc
}

// checkInterpStr type-checks an f-string interpolation expression.
// Every interpolated expression is checked; the result type is always str.
// Function references cannot be interpolated — they are not printable.
func (c *Checker) checkInterpStr(ctx *fnCtx, n *ast.InterpStr) TypeDesc {
        for _, part := range n.Parts {
                if part.Expr != nil {
                        t := c.checkExpr(ctx, part.Expr)
                        if t.Kind == TFunc {
                                c.reportError(ctx, part.Expr.Pos(),
                                        "cannot interpolate a function reference in an f-string")
                        }
                }
        }
        return StrDesc
}

func (c *Checker) checkBoolLit(ctx *fnCtx, n *ast.BoolLit) TypeDesc {
        return BoolDesc
}

func (c *Checker) checkNilLit(ctx *fnCtx, n *ast.NilLit) TypeDesc {
        return GenDesc
}

func (c *Checker) checkIdent(ctx *fnCtx, n *ast.Ident) TypeDesc {
        b, ok := c.lookup(ctx, n.Name)
        if !ok {
                c.reportErrorWithCode(ctx, n.Pos(), "E0101", "undefined identifier '%s'", n.Name)
                // Suggest similar names from all visible scopes.
                var candidates []string
                scope := c.currentScope(ctx)
                for scope != nil {
                        candidates = append(candidates, scope.Names()...)
                        scope = scope.parent
                }
                candidates = uniqueStrings(candidates)
                if suggestion, _ := suggestSimilar(n.Name, candidates); suggestion != "" {
                        c.reportHelp(ctx, n.Pos(), "did you mean '%s'?", suggestion)
                }
                return GenDesc
        }
        // Mark this binding as used for unused-variable detection.
        // Only track local variables, not parameters or globals.
        if b.Kind == BndVar && ctx.usedBindings != nil {
                ctx.usedBindings[n.Name] = true
        }
        // Const bindings are not tracked for unused-variable warnings.
        return b.Type
}

func (c *Checker) checkBinOp(ctx *fnCtx, n *ast.BinOp) TypeDesc {
        leftType := c.checkExpr(ctx, n.Left)
        rightType := c.checkExpr(ctx, n.Right)

        // Overflow check (W0300): if both operands are integer literals,
        // compute the exact result and warn on overflow.
        c.checkIntOverflow(ctx, n)

        switch n.Op {
        case ast.TAnd, ast.TOr:
                // Logical operators require bool operands.
                if leftType.IsConcrete() && leftType.Kind != TBool && leftType.Kind != TGen {
                        c.reportError(ctx, n.Left.Pos(),
                                "left operand of '%s' must be bool, got '%s'", n.Op, leftType.String())
                }
                if rightType.IsConcrete() && rightType.Kind != TBool && rightType.Kind != TGen {
                        c.reportError(ctx, n.Right.Pos(),
                                "right operand of '%s' must be bool, got '%s'", n.Op, rightType.String())
                }
                return BoolDesc

        case ast.TEq, ast.TNeq:
                // Equality works between any two values of the same type.
                if leftType.IsConcrete() && rightType.IsConcrete() && !leftType.Equals(rightType) {
                        // Allow int/uint and uint/int comparison with a warning.
                        if !(leftType.Kind == TInt && rightType.Kind == TUint) &&
                                !(leftType.Kind == TUint && rightType.Kind == TInt) {
                                c.reportError(ctx, n.Pos(), "cannot compare '%s' and '%s'", leftType.String(), rightType.String())

                        }
                }
                return BoolDesc

        case ast.TLt, ast.TLe, ast.TGt, ast.TGe:
                // Ordered comparison requires numeric or string operands.
                if leftType.IsConcrete() && rightType.IsConcrete() {
                        if !leftType.IsNumeric() && leftType.Kind != TStr {
                                c.reportError(ctx, n.Left.Pos(),
                                        "'%s' requires ordered operands, got '%s'", n.Op, leftType.String())
                        }
                        if !rightType.IsNumeric() && rightType.Kind != TStr {
                                c.reportError(ctx, n.Right.Pos(),
                                        "'%s' requires ordered operands, got '%s'", n.Op, rightType.String())
                        }
                        if (leftType.IsNumeric() && rightType.Kind == TStr) ||
                                (leftType.Kind == TStr && rightType.IsNumeric()) {
                                c.reportError(ctx, n.Pos(), "cannot compare '%s' and '%s'", leftType.String(), rightType.String())

                        }
                }
                return BoolDesc

        case ast.TPlus, ast.TMinus, ast.TStar, ast.TSlash, ast.TPercent:
                // + supports string concatenation (str + str).
                // + also auto-coerces: str + <any>, <any> + str → str.
                // This makes constructs like "x = " + 42 and 1 + ".0" ergonomic.
                if n.Op == ast.TPlus {
                        if leftType.Kind == TStr && rightType.Kind == TStr {
                                return StrDesc
                        }
                        // Auto-coercion: if either side is str and the other is
                        // concrete non-str, the non-str side is implicitly converted.
                        if leftType.Kind == TStr && rightType.IsConcrete() {
                                return StrDesc
                        }
                        if rightType.Kind == TStr && leftType.IsConcrete() {
                                return StrDesc
                        }
                        if leftType.Kind == TStr || rightType.Kind == TStr {
                                return StrDesc // one side gen + one side str
                        }
                }
                // Arithmetic operators require numeric operands.
                // Only report errors when BOTH operands are concrete (gen absorbs anything).
                if leftType.Kind != TGen && rightType.Kind != TGen {
                        if leftType.IsConcrete() && !leftType.IsNumeric() {
                                c.reportError(ctx, n.Left.Pos(),
                                        "left operand of '%s' must be numeric, got '%s'", n.Op, leftType.String())
                        }
                        if rightType.IsConcrete() && !rightType.IsNumeric() {
                                c.reportError(ctx, n.Right.Pos(),
                                        "right operand of '%s' must be numeric, got '%s'", n.Op, rightType.String())
                        }
                }
                // Determine result type: if either operand is gen, result is gen.
                if leftType.Kind == TGen || rightType.Kind == TGen {
                        return GenDesc
                }
                // Determine result type: if either operand is fp, result is fp.
                if leftType.Kind == TFp || rightType.Kind == TFp {
                        return FpDesc
                }
                if leftType.Kind == TUint || rightType.Kind == TUint {
                        return UintDesc
                }
                return IntDesc

        case ast.TAmp, ast.TPipe, ast.TCaret:
                // Bitwise operators require integer operands.
                // Only report errors when BOTH operands are concrete.
                if leftType.Kind != TGen && rightType.Kind != TGen {
                        if leftType.IsConcrete() && !leftType.IsInteger() {
                                c.reportError(ctx, n.Left.Pos(),
                                        "left operand of '%s' must be integer, got '%s'", n.Op, leftType.String())
                        }
                        if rightType.IsConcrete() && !rightType.IsInteger() {
                                c.reportError(ctx, n.Right.Pos(),
                                        "right operand of '%s' must be integer, got '%s'", n.Op, rightType.String())
                        }
                }
                if leftType.Kind == TGen || rightType.Kind == TGen {
                        return GenDesc
                }
                if leftType.Kind == TUint || rightType.Kind == TUint {
                        return UintDesc
                }
                return IntDesc

        case ast.TLShift, ast.TRShift:
                // Shift operators require integer operands.
                if leftType.Kind != TGen && rightType.Kind != TGen {
                        if leftType.IsConcrete() && !leftType.IsInteger() {
                                c.reportError(ctx, n.Left.Pos(),
                                        "left operand of '%s' must be integer, got '%s'", n.Op, leftType.String())
                        }
                        if rightType.IsConcrete() && !rightType.IsInteger() {
                                c.reportError(ctx, n.Right.Pos(),
                                        "right operand of '%s' must be integer, got '%s'", n.Op, rightType.String())
                        }
                }
                if leftType.Kind == TGen || rightType.Kind == TGen {
                        return GenDesc
                }
                return leftType

        default:
                c.reportError(ctx, n.Pos(), "unknown binary operator '%s'", n.Op)
                return GenDesc
        }
}

func (c *Checker) checkUnaryOp(ctx *fnCtx, n *ast.UnaryOp) TypeDesc {
        operandType := c.checkExpr(ctx, n.Operand)

        switch n.Op {
        case ast.TMinus:
                if operandType.IsConcrete() && !operandType.IsNumeric() {
                        c.reportError(ctx, n.Operand.Pos(),
                                "negation requires a numeric operand, got '%s'", operandType.String())
                }
                return operandType

        case ast.TNot:
                if operandType.IsConcrete() && operandType.Kind != TBool && operandType.Kind != TGen {
                        c.reportError(ctx, n.Operand.Pos(),
                                "'not' requires a bool operand, got '%s'", operandType.String())
                }
                return BoolDesc

        case ast.TTilde:
                if operandType.IsConcrete() && !operandType.IsInteger() {
                        c.reportError(ctx, n.Operand.Pos(),
                                "bitwise complement requires an integer operand, got '%s'", operandType.String())
                }
                return operandType

        default:
                c.reportError(ctx, n.Pos(), "unknown unary operator '%s'", n.Op)
                return GenDesc
        }
}

func (c *Checker) checkCallExpr(ctx *fnCtx, n *ast.CallExpr) TypeDesc {
        // Handle generic function calls: identity[int](x)
        // When TypeArgs is present and the callee is an identifier, look up
        // the generic template, monomorphize it, and rewrite the call.
        if len(n.TypeArgs) > 0 {
                if ident, ok := n.Func.(*ast.Ident); ok {
                        gdef, isGeneric := c.genericFns[ident.Name]
                        if isGeneric {
                                // Validate type argument count.
                                if len(n.TypeArgs) != len(gdef.TypeParams) {
                                        c.reportError(ctx, n.Func.Pos(),
                                                "generic function '%s' expects %d type argument(s), got %d",
                                                ident.Name, len(gdef.TypeParams), len(n.TypeArgs))
                                        return GenDesc
                                }
                                // Resolve type arguments to TypeDescs.
                                concreteArgs := make([]TypeDesc, len(n.TypeArgs))
                                for i, ta := range n.TypeArgs {
                                        concreteArgs[i] = c.resolveTypeArg(ta)
                                }
                                // Monomorphize and get the mangled name.
                                mangled := c.monomorphize(gdef, concreteArgs)
                                // Rewrite the callee to the mangled name.
                                n.Func = &ast.Ident{Name: mangled, Span: n.Func.Pos()}
                        } else {
                                c.reportError(ctx, n.Func.Pos(),
                                        "'%s' is not a generic function", ident.Name)
                                return GenDesc
                        }
                }
        }

        // Check all argument expressions first.
        argTypes := make([]TypeDesc, len(n.Args))
        for i, a := range n.Args {
                argTypes[i] = c.checkExpr(ctx, a)
        }

        // Check the spread expression if present.
        if n.Spread != nil {
                c.checkExpr(ctx, *n.Spread)
        }

        // Nil-safety check (W0200): warn if the callee is a local variable
        // that has not been definitely assigned (calling a possibly nil value).
        if ident, ok := n.Func.(*ast.Ident); ok {
                if !c.isDefinitelyAssigned(ctx, ident.Name) {
                        c.reportWarnWithCode(ctx, n.Func.Pos(), "W0200",
                                "variable '%s' may be nil; calling it may panic", ident.Name)
                }
        }

        // Resolve the callee.
        fnType := c.checkExpr(ctx, n.Func)
        if fnType.Kind == TFunc {
                // Validate spread usage: spread is only allowed with variadic functions.
                if n.Spread != nil && !fnType.Variadic {
                        c.reportErrorWithCode(ctx, (*n.Spread).Pos(), "E0301",
                                "spread '...' can only be used with variadic functions")
                }

                // We have a resolved function type.
                // Compute minimum required args: total params minus defaults, minus variadic slot.
                minArgs := len(fnType.Params) - fnType.NumDefaults
                if fnType.Variadic && minArgs > 0 {
                        minArgs--
                }
                if len(argTypes) < minArgs {
                        c.reportErrorWithCode(ctx, n.Pos(), "E0301", "function expects at least %d argument(s), got %d",
                                minArgs, len(argTypes))
                } else if !fnType.Variadic && len(argTypes) > len(fnType.Params) {
                        c.reportErrorWithCode(ctx, n.Pos(), "E0301", "function expects at most %d argument(s), got %d",
                                len(fnType.Params), len(argTypes))
                }
                // Check individual argument types where possible.
                for i, at := range argTypes {
                        if i < len(fnType.Params) && at.IsConcrete() && fnType.Params[i].IsConcrete() {
                                if !c.assignable(*fnType.Params[i], at) {
                                        c.reportError(ctx, n.Args[i].Pos(),
                                                "argument %d: expected '%s', got '%s'",
                                                i+1, fnType.Params[i].String(), at.String())
                                }
                        }
                }
                // Type-check extra variadic arguments against the variadic element type.
                if fnType.Variadic && n.Spread == nil {
                        variadicElemType := fnType.Params[len(fnType.Params)-1]
                        for i := len(fnType.Params); i < len(argTypes); i++ {
                                if argTypes[i].IsConcrete() && variadicElemType.IsConcrete() &&
                                        !c.assignable(*variadicElemType, argTypes[i]) {
                                        c.reportError(ctx, n.Args[i].Pos(),
                                                "variadic argument %d: expected '%s', got '%s'",
                                                i+1, variadicElemType.String(), argTypes[i].String())
                                }
                        }
                }
                if fnType.Ret != nil {
                        return *fnType.Ret
                }
                return GenDesc
        }

        // Fallback: if the callee is an identifier, try looking it up as a
        // function or FFI binding (gen-typed callables accept any arguments).
        if ident, ok := n.Func.(*ast.Ident); ok {
                b, found := c.lookup(ctx, ident.Name)
                if found && (b.Kind == BndFFI || b.Kind == BndFn) {
                        // For FFI / gen-typed functions we skip argument checks.
                        if b.Type.Ret != nil {
                                return *b.Type.Ret
                        }
                        return GenDesc
                }
                // Allow calling gen-typed variables (higher-order functions).
                if found && b.Kind == BndVar && b.Type.Kind == TGen {
                        return GenDesc
                }
        }

        // Allow calling any gen-typed expression (e.g. curried calls
        // like compose(f, g)(x) or make_adder(10)(32)).
        if fnType.Kind == TGen {
                return GenDesc
        }

        c.reportErrorWithCode(ctx, n.Func.Pos(), "E0302", "cannot call non-function expression")
        // If the callee is an identifier, suggest similar function names
        // from the global scope that the user might have intended to call.
        if ident, ok := n.Func.(*ast.Ident); ok {
                var candidates []string
                for _, name := range c.global.Names() {
                        if b := c.global.Lookup(name); b != nil && (b.Kind == BndFn || b.Kind == BndImport) {
                                candidates = append(candidates, name)
                        }
                }
                if suggestion, _ := suggestSimilar(ident.Name, candidates); suggestion != "" {
                        c.reportHelp(ctx, n.Func.Pos(), "did you mean '%s'?", suggestion)
                }
        }
        return GenDesc
}

func (c *Checker) checkIndexExpr(ctx *fnCtx, n *ast.IndexExpr) TypeDesc {
        objType := c.checkExpr(ctx, n.Obj)
        _ = c.checkExpr(ctx, n.Key) // key type checked for side effects

        // Nil-safety check (W0200): warn if the object is a local variable
        // that has not been definitely assigned.
        if ident, ok := n.Obj.(*ast.Ident); ok {
                if !c.isDefinitelyAssigned(ctx, ident.Name) {
                        c.reportWarnWithCode(ctx, n.Obj.Pos(), "W0200",
                                "variable '%s' may be nil; index access requires a non-nil value", ident.Name)
                }
        }

        if objType.IsConcrete() && objType.Kind != TTable && objType.Kind != TGen {
                c.reportError(ctx, n.Obj.Pos(),
                        "indexing requires a table, got '%s'", objType.String())
        }

        // For table types with known value type, return it.
        if objType.Kind == TTable && objType.ValType != nil {
                return *objType.ValType
        }

        return GenDesc
}

func (c *Checker) checkMemberExpr(ctx *fnCtx, n *ast.MemberExpr) TypeDesc {
        objType := c.checkExpr(ctx, n.Obj)

        // Nil-safety check (W0200): warn if the object is a local variable
        // that has not been definitely assigned.
        if ident, ok := n.Obj.(*ast.Ident); ok {
                if !c.isDefinitelyAssigned(ctx, ident.Name) {
                        c.reportWarnWithCode(ctx, n.Obj.Pos(), "W0200",
                                "variable '%s' may be nil; access requires a non-nil value", ident.Name)
                }
        }

        // Member access is valid on module bindings.
        if ident, ok := n.Obj.(*ast.Ident); ok {
                b, found := c.lookup(ctx, ident.Name)
                if found && (b.Kind == BndModule || b.Kind == BndFFI) {
                        // Look up the member as a symbol in the module.
                        moduleName := ident.Name
                        // For FFI modules, the member is an FFI symbol.
                        if b.Kind == BndFFI {
                                // The member access creates a new FFI binding reference.
                                return GenDesc
                        }
                        // Look up the original module name (before aliasing) from imports.
                        origModule := moduleName
                        isStd := false
                        hasLocalImport := false
                        for _, imp := range c.imports {
                                if imp.Alias == moduleName {
                                        origModule = imp.ModuleName
                                        isStd = imp.IsStd
                                        hasLocalImport = true
                                        break
                                }
                        }
                        // If the user wrote `sys.platform` (or any stdlib
                        // module name) WITHOUT an explicit `use sys`, treat
                        // it as an implicit stdlib import.  Stdlib modules
                        // are pre-registered as global bindings (see
                        // registerStdlibModules), so the binding lookup
                        // above succeeded — we just need to recognise that
                        // the module name maps to a stdlib module here.
                        //
                        // BUT: if there is a local import with the same
                        // alias and IsStd=false, the user has shadowed the
                        // stdlib module with a local file (e.g. `use math`
                        // resolving to ./math.yilt).  In that case, do NOT
                        // fall back to the stdlib — let the local-module
                        // code path handle it.
                        if !isStd && !hasLocalImport {
                                if _, isStdMod := stdModuleExports[moduleName]; isStdMod {
                                        origModule = moduleName
                                        isStd = true
                                }
                        }
                        // For stdlib modules, look up the export.
                        if isStd {
                                exports, ok := stdModuleExports[origModule]
                                if ok {
                                        for _, sym := range exports {
                                                if sym.Name == n.Field {
                                                        return sym.Type
                                                }
                                        }
                                }
                                // Check built-in type method names (e.g. str.len, table.has).
                                if methodType, ok := c.resolveBuiltinMethod(origModule, n.Field); ok {
                                        return methodType
                                }
                                c.reportErrorWithCode(ctx, n.Pos(), "E0701", "module '%s' has no symbol '%s'", moduleName, n.Field)
                                // Suggest similar symbols from the module's exports.
                                if exports, ok := stdModuleExports[origModule]; ok {
                                        var candidates []string
                                        for _, sym := range exports {
                                                candidates = append(candidates, sym.Name)
                                        }
                                        if suggestion, _ := suggestSimilar(n.Field, candidates); suggestion != "" {
                                                c.reportHelp(ctx, n.Pos(), "did you mean '%s.%s'?", origModule, suggestion)
                                        }
                                }

                                return GenDesc
                        }
                        // For local modules, look up public declarations (functions, structs, enums, consts).
                        for _, imp := range c.imports {
                                if imp.Alias == moduleName || imp.ModulePath == moduleName {
                                        for _, file := range c.program.Files {
                                                if filepath.Clean(file.Path) != imp.ModulePath && imp.ModulePath != "" {
                                                        continue
                                                }
                                                for _, d := range file.Decls {
                                                        switch decl := d.(type) {
                                                        case *ast.FnDecl:
                                                                if !decl.Public || decl.Name != n.Field {
                                                                        continue
                                                                }
                                                                // Return the full function type, not just the return type.
                                                                paramTypes := make([]*TypeDesc, len(decl.Params))
                                                                for i, p := range decl.Params {
                                                                        pt := c.resolveType(&p.Type)
                                                                        paramTypes[i] = &pt
                                                                }
                                                                retType := c.resolveType(decl.ReturnType)
                                                                return TypeDesc{Kind: TFunc, Params: paramTypes, Ret: &retType}
                                                        case *ast.StructDecl:
                                                                if !decl.Public || decl.Name != n.Field {
                                                                        continue
                                                                }
                                                                return TypeDesc{Kind: TStruct, Name: decl.Name}
                                                        case *ast.EnumDecl:
                                                                if !decl.Public || decl.Name != n.Field {
                                                                        continue
                                                                }
                                                                return TypeDesc{Kind: TEnum, Name: decl.Name}
                                                        case *ast.ConstDecl:
                                                                if decl.Name != n.Field {
                                                                        continue
                                                                }
                                                                return c.inferConstType(decl.Value)
                                                        }
                                                }
                                        }
                                }
                        }
                        c.reportErrorWithCode(ctx, n.Pos(), "E0701", "module '%s' has no symbol '%s'", moduleName, n.Field)
                        // Suggest similar symbols from the local module's public declarations.
                        var localCandidates []string
                        for _, imp := range c.imports {
                                if imp.Alias == moduleName || imp.ModulePath == moduleName {
                                        for _, file := range c.program.Files {
                                                if filepath.Clean(file.Path) != imp.ModulePath && imp.ModulePath != "" {
                                                        continue
                                                }
                                                for _, d := range file.Decls {
                                                        var name string
                                                        switch decl := d.(type) {
                                                        case *ast.FnDecl:
                                                                if !decl.Public { continue }
                                                                name = decl.Name
                                                        case *ast.StructDecl:
                                                                if !decl.Public { continue }
                                                                name = decl.Name
                                                        case *ast.EnumDecl:
                                                                if !decl.Public { continue }
                                                                name = decl.Name
                                                        case *ast.ConstDecl:
                                                                name = decl.Name
                                                        }
                                                        if name != "" {
                                                                localCandidates = append(localCandidates, name)
                                                        }
                                                }
                                        }
                                }
                        }
                        localCandidates = uniqueStrings(localCandidates)
                        if suggestion, _ := suggestSimilar(n.Field, localCandidates); suggestion != "" {
                                c.reportHelp(ctx, n.Pos(), "did you mean '%s.%s'?", moduleName, suggestion)
                        }

                        return GenDesc
                }
        }

        // Value method calls: s.trim(), t.len(), t.has(), etc.
        // These appear as MemberExpr inside a CallExpr. We need to return a
        // function type so the call checker can resolve the call properly.
        if methodType, ok := c.resolveValueMethod(objType, n.Field); ok {
                return methodType
        }

        // Struct field access
        if objType.Kind == TStruct && c.global != nil {
                if b := c.global.Lookup(objType.Name); b != nil && b.StructInfo != nil {
                        if ft, ok := b.StructInfo.FieldTypes[n.Field]; ok {
                                return *ft
                        }
                        // Check if this is a struct method call (e.g. p.distance(other)).
                        // Methods are functions named "StructName_methodname" with the
                        // struct as first parameter, detected during collectStructMethods().
                        if mi, ok := b.StructInfo.Methods[n.Field]; ok {
                                // Return the method's function type with the receiver
                                // parameter included so the call checker can validate args.
                                return mi.FuncType
                        }
                        // Struct type is known but field/method doesn't exist — report a clear error.
                        c.reportError(ctx, n.Pos(), "struct '%s' has no field '%s'", objType.Name, n.Field)
                        // Offer "did you mean?" suggestions for struct fields AND methods.
                        fieldNames := make([]string, 0, len(b.StructInfo.Fields)+len(b.StructInfo.Methods))
                        for _, fi := range b.StructInfo.Fields {
                                fieldNames = append(fieldNames, fi.Name)
                        }
                        for mName := range b.StructInfo.Methods {
                                fieldNames = append(fieldNames, mName)
                        }
                        if suggestion, _ := suggestSimilar(n.Field, fieldNames); suggestion != "" {
                                c.reportHelp(ctx, n.Pos(), "did you mean '%s'?", suggestion)
                        }
                        return GenDesc
                }
        }

        // Table member access (dot notation on tables).
        if objType.Kind == TTable || objType.Kind == TGen {
                // We cannot know the field type statically for generic tables.
                return GenDesc
        }

        // Don't report error for value types that might have methods we don't
        // track yet — they may be resolved at runtime.
        if objType.Kind == TStr || objType.Kind == TInt || objType.Kind == TFp {
                return GenDesc
        }

        c.reportError(ctx, n.Pos(), "member access on non-module, non-table type '%s'", objType.String())

        return GenDesc
}

// resolveValueMethod checks if a method call on a value of the given type
// is a known built-in method and returns its type.
func (c *Checker) resolveValueMethod(objType TypeDesc, field string) (TypeDesc, bool) {
        genP := &GenDesc
        switch objType.Kind {
        case TStr:
                switch field {
                case "len", "to_int":
                        return TypeDesc{Kind: TFunc, Ret: &IntDesc}, true
                case "upper", "lower", "trim", "strip", "lstrip", "rstrip":
                        return TypeDesc{Kind: TFunc, Ret: &StrDesc}, true
                case "substr":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP, genP}, Ret: &StrDesc}, true
                case "contains", "starts_with", "ends_with":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP}, Ret: &BoolDesc}, true
                case "find":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP}, Ret: &IntDesc}, true
                case "replace":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP, genP}, Ret: &StrDesc}, true
                case "split":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP}, Ret: &TableDesc}, true
                case "chars", "bytes":
                        return TypeDesc{Kind: TFunc, Ret: &TypeDesc{Kind: TTable, KeyType: &IntDesc, ValType: &IntDesc}}, true
                }
        case TInt, TUint:
                switch field {
                case "to_str":
                        return TypeDesc{Kind: TFunc, Ret: &StrDesc}, true
                case "to_fp":
                        return TypeDesc{Kind: TFunc, Ret: &FpDesc}, true
                case "abs", "neg", "sign", "is_zero":
                        return TypeDesc{Kind: TFunc, Ret: &IntDesc}, true
                case "clamp", "min", "max":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP, genP}, Ret: &IntDesc}, true
                case "bit_length":
                        return TypeDesc{Kind: TFunc, Ret: &IntDesc}, true
                }
        case TFp:
                switch field {
                case "to_str":
                        return TypeDesc{Kind: TFunc, Ret: &StrDesc}, true
                case "to_int":
                        return TypeDesc{Kind: TFunc, Ret: &IntDesc}, true
                case "abs", "neg", "floor", "ceil", "round", "sqrt", "trunc", "fract":
                        return TypeDesc{Kind: TFunc, Ret: &FpDesc}, true
                case "sign":
                        return TypeDesc{Kind: TFunc, Ret: &IntDesc}, true
                case "clamp", "min", "max":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP, genP}, Ret: &FpDesc}, true
                case "is_nan", "is_inf":
                        return TypeDesc{Kind: TFunc, Ret: &BoolDesc}, true
                }
        case TBool:
                switch field {
                case "to_str":
                        return TypeDesc{Kind: TFunc, Ret: &StrDesc}, true
                }
        case TTable:
                switch field {
                case "len", "is_empty":
                        return TypeDesc{Kind: TFunc, Ret: &IntDesc}, true
                case "has":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP}, Ret: &BoolDesc}, true
                case "get":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP}, Ret: &GenDesc}, true
                case "set":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP, genP}, Ret: &GenDesc}, true
                case "remove", "clear":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP}, Ret: &BoolDesc}, true
                case "keys", "values":
                        return TypeDesc{Kind: TFunc, Ret: &TypeDesc{Kind: TTable, KeyType: &IntDesc, ValType: &GenDesc}}, true
                case "merge":
                        return TypeDesc{Kind: TFunc, Params: []*TypeDesc{genP}, Ret: &GenDesc}, true
                case "clone":
                        return TypeDesc{Kind: TFunc, Ret: &GenDesc}, true
                }
        }
        return GenDesc, false
}

// resolveBuiltinMethod resolves module-level "type methods" that appear as
// module.field when the module name matches a type name (e.g. str.len).
func (c *Checker) resolveBuiltinMethod(moduleName string, field string) (TypeDesc, bool) {
        var baseType TType
        switch moduleName {
        case "str":
                baseType = TStr
        case "int", "uint":
                baseType = TInt
        case "fp":
                baseType = TFp
        case "bool":
                baseType = TBool
        case "table":
                baseType = TTable
        default:
                return GenDesc, false
        }
        return c.resolveValueMethod(TypeDesc{Kind: baseType}, field)
}

func (c *Checker) checkStructLit(ctx *fnCtx, n *ast.StructLit) TypeDesc {
        // Look up the struct type
        var b *Binding
        if n.TypeExpr != nil {
                // Qualified struct literal: shapes.Point{...}
                // Resolve through the member expression to get the struct binding.
                typeDesc := c.checkExpr(ctx, n.TypeExpr)
                if typeDesc.Kind == TStruct {
                        b = c.global.Lookup(n.Name)
                }
                if b == nil || b.Kind != BndType {
                        c.reportError(ctx, n.Pos(), "'%s.%s' is not a struct type", "module", n.Name)
                        return GenDesc
                }
        } else {
                b = c.global.Lookup(n.Name)
                if b == nil {
                        c.reportError(ctx, n.Pos(), "unknown type '%s'", n.Name)
                        return GenDesc
                }
        }
        if b.Kind != BndType {
                c.reportError(ctx, n.Pos(), "'%s' is not a struct type", n.Name)
                return GenDesc
        }

        // Check each field
        for _, finit := range n.Fields {
                valType := c.checkExpr(ctx, finit.Value)
                if ft, ok := b.StructInfo.FieldTypes[finit.Name]; ok {
                        if valType.IsConcrete() && ft.IsConcrete() && !c.assignable(*ft, valType) {
                                c.reportWarn(ctx, finit.Span,
                                        "struct field '%s.%s': expected %s, got %s", n.Name, finit.Name, ft.String(), valType.String())
                        }
                } else {
                        c.reportError(ctx, finit.Span,
                                "struct '%s' has no field '%s'", n.Name, finit.Name)
                }
        }

        // Warn about omitted fields (they will default to nil at runtime).
        initialized := make(map[string]bool, len(n.Fields))
        for _, finit := range n.Fields {
                initialized[finit.Name] = true
        }
        for _, fi := range b.StructInfo.Fields {
                if !initialized[fi.Name] {
                        c.reportError(ctx, n.Span,
                                "struct field '%s.%s' is not initialized", n.Name, fi.Name)
                }
        }

        return b.Type
}

// checkEnumLit checks an enum variant literal: Color.Red or Result.Ok(42).
func (c *Checker) checkEnumLit(ctx *fnCtx, n *ast.EnumLit) TypeDesc {
        // Look up the enum type
        b := c.global.Lookup(n.EnumName)
        if b == nil {
                c.reportError(ctx, n.Pos(), "unknown type '%s'", n.EnumName)
                return GenDesc
        }
        if b.Kind != BndType {
                c.reportError(ctx, n.Pos(), "'%s' is not an enum type", n.EnumName)
                return GenDesc
        }

        ei := b.EnumInfo
        if ei == nil {
                c.reportError(ctx, n.Pos(), "'%s' is not an enum type", n.EnumName)
                return GenDesc
        }

        idx, ok := ei.VariantIndex[n.VariantName]
        if !ok {
                c.reportError(ctx, n.Pos(),
                        "enum '%s' has no variant '%s'", n.EnumName, n.VariantName)
                return GenDesc
        }

        vi := ei.Variants[idx]
        // Validate payload type if variant has one.
        if vi.Payload != nil && n.Payload != nil {
                payloadType := c.checkExpr(ctx, n.Payload)
                if payloadType.IsConcrete() && vi.Payload.IsConcrete() &&
                        !c.assignable(*vi.Payload, payloadType) {
                        c.reportError(ctx, n.Payload.Pos(),
                                "enum '%s.%s' payload: expected %s, got %s",
                                n.EnumName, n.VariantName, vi.Payload.String(), payloadType.String())
                }
        } else if vi.Payload != nil && n.Payload == nil {
                // Variant expects a payload but none was provided.
                c.reportError(ctx, n.Pos(),
                        "enum variant '%s.%s' expects a payload of type %s",
                        n.EnumName, n.VariantName, vi.Payload.String())
        } else if vi.Payload == nil && n.Payload != nil {
                // Variant does not take a payload but one was provided.
                c.reportError(ctx, n.Payload.Pos(),
                        "enum variant '%s.%s' does not take a payload",
                        n.EnumName, n.VariantName)
        }

        return b.Type
}

// checkEnumMatchPattern checks an enum pattern in a match arm:
// e.g., `case Result.Ok(value)` binds the payload to variable `value`.
func (c *Checker) checkEnumMatchPattern(ctx *fnCtx, n *ast.EnumMatchPattern) TypeDesc {
        b := c.global.Lookup(n.EnumName)
        if b == nil {
                c.reportError(ctx, n.Pos(), "unknown type '%s'", n.EnumName)
                return GenDesc
        }
        if b.Kind != BndType {
                c.reportError(ctx, n.Pos(), "'%s' is not an enum type", n.EnumName)
                return GenDesc
        }

        ei := b.EnumInfo
        if ei == nil {
                c.reportError(ctx, n.Pos(), "'%s' is not an enum type", n.EnumName)
                return GenDesc
        }

        idx, ok := ei.VariantIndex[n.VariantName]
        if !ok {
                c.reportError(ctx, n.Pos(),
                        "enum '%s' has no variant '%s'", n.EnumName, n.VariantName)
                return GenDesc
        }

        vi := ei.Variants[idx]

        if n.BindVar != "" {
                if vi.Payload == nil {
                        c.reportError(ctx, n.Pos(),
                                "enum variant '%s.%s' does not take a payload; cannot bind '%s'",
                                n.EnumName, n.VariantName, n.BindVar)
                } else {
                        // Bind the variable in the current scope with the payload type.
                        binding := &Binding{
                                Name:    n.BindVar,
                                Kind:    BndVar,
                                Type:    *vi.Payload,
                                Mutable: false,
                                Pos:     n.Span,
                        }
                        c.define(ctx, n.BindVar, binding)
                }
        }

        return b.Type
}

func (c *Checker) checkTableLit(ctx *fnCtx, n *ast.TableLit) TypeDesc {
        keyType := GenDesc
        valType := GenDesc
        warnedMixedKey := false
        warnedMixedVal := false
        for _, entry := range n.Entries {
                kt := c.checkExpr(ctx, entry.Key)
                vt := c.checkExpr(ctx, entry.Value)
                // All keys should have the same type.
                if keyType.Kind == TGen && kt.IsConcrete() {
                        keyType = kt
                } else if keyType.Kind != TGen && kt.IsConcrete() && keyType.Kind != kt.Kind {
                        if !warnedMixedKey {
                                c.reportWarn(ctx, entry.Key.Pos(),
                                        "mixed key types in table literal ('%s' and '%s'); falling back to generic table",
                                        keyType.String(), kt.String())
                                warnedMixedKey = true
                        }
                        keyType = GenDesc
                }
                // All values should have the same type.
                if valType.Kind == TGen && vt.IsConcrete() {
                        valType = vt
                } else if valType.Kind != TGen && vt.IsConcrete() && valType.Kind != vt.Kind {
                        if !warnedMixedVal {
                                c.reportWarn(ctx, entry.Value.Pos(),
                                        "mixed value types in table literal ('%s' and '%s'); falling back to generic table",
                                        valType.String(), vt.String())
                                warnedMixedVal = true
                        }
                        valType = GenDesc
                }
        }
        // For mixed tables, fall back to generic table type.
        if keyType.Kind == TGen || valType.Kind == TGen {
                return TypeDesc{
                        Kind:    TTable,
                        KeyType: &keyType,
                        ValType: &valType,
                }
        }
        return TypeDesc{
                Kind:    TTable,
                KeyType: &keyType,
                ValType: &valType,
        }
}

func (c *Checker) checkAssignExpr(ctx *fnCtx, n *ast.AssignExpr) TypeDesc {
        // Check the value expression.
        valType := c.checkExpr(ctx, n.Value)

        // The target must be a mutable identifier.
        ident, ok := n.Target.(*ast.Ident)
        if !ok {
                c.reportError(ctx, n.Pos(), "assignment target must be an identifier")
                return valType
        }

        b, found := c.lookup(ctx, ident.Name)
        if !found {
                c.reportErrorWithCode(ctx, n.Pos(), "E0101", "undefined identifier '%s'", ident.Name)
                return valType
        }
        if !b.Mutable {
                c.reportErrorWithCode(ctx, n.Pos(), "E0202", "'%s' is immutable; use 'mut' to declare mutable bindings", ident.Name)

        }

        if valType.IsConcrete() && b.Type.IsConcrete() && !c.assignable(b.Type, valType) {
                c.reportErrorWithCode(ctx, n.Pos(), "E0201", "cannot assign '%s' to '%s'", valType.String(), b.Type.String())

        }

        // Reassignment makes the variable definitely assigned.
        c.markAssigned(ctx, ident.Name)

        return valType
}

func (c *Checker) checkIndexAssignExpr(ctx *fnCtx, n *ast.IndexAssignExpr) TypeDesc {
        objType := c.checkExpr(ctx, n.Obj)
        keyType := c.checkExpr(ctx, n.Key)
        valType := c.checkExpr(ctx, n.Value)

        if objType.IsConcrete() && objType.Kind != TTable && objType.Kind != TGen {
                c.reportError(ctx, n.Obj.Pos(),
                        "index assignment requires a table, got '%s'", objType.String())
        }

        // Validate key type against table's inferred key type.
        if objType.Kind == TTable && objType.KeyType != nil && objType.KeyType.IsConcrete() &&
                keyType.IsConcrete() && !c.assignable(*objType.KeyType, keyType) {
                c.reportError(ctx, n.Key.Pos(),
                        "cannot use '%s' as table key type '%s'", keyType.String(), objType.KeyType.String())
        }

        // Validate value type against table's inferred value type.
        if objType.Kind == TTable && objType.ValType != nil && objType.ValType.IsConcrete() &&
                valType.IsConcrete() && !c.assignable(*objType.ValType, valType) {
                c.reportError(ctx, n.Value.Pos(),
                        "cannot assign '%s' to table value type '%s'", valType.String(), objType.ValType.String())
        }

        // Refine the binding's table type when assigning to a table whose
        // key/value types are still gen.  This allows subsequent accesses
        // to have concrete type information.
        if objType.Kind == TTable {
                if ident, ok := n.Obj.(*ast.Ident); ok {
                        if b, found := c.lookup(ctx, ident.Name); found {
                                // Refine key type
                                if b.Type.KeyType == nil || b.Type.KeyType.Kind == TGen {
                                        if keyType.IsConcrete() {
                                                if b.Type.KeyType == nil {
                                                        kt := keyType
                                                        b.Type.KeyType = &kt
                                                } else {
                                                        *b.Type.KeyType = keyType
                                                }
                                        }
                                }
                                // Refine value type
                                if b.Type.ValType == nil || b.Type.ValType.Kind == TGen {
                                        if valType.IsConcrete() {
                                                if b.Type.ValType == nil {
                                                        vt := valType
                                                        b.Type.ValType = &vt
                                                } else {
                                                        *b.Type.ValType = valType
                                                }
                                        }
                                }
                        }
                }
        }

        // Check mutability of the target table binding.
        if ident, ok := n.Obj.(*ast.Ident); ok {
                if b, found := c.lookup(ctx, ident.Name); found && !b.Mutable {
                        c.reportError(ctx, n.Obj.Pos(),
                                "'%s' is immutable; use 'mut' to declare mutable bindings", ident.Name)
                }
        }

        return valType
}

func (c *Checker) checkMemberAssignExpr(ctx *fnCtx, n *ast.MemberAssignExpr) TypeDesc {
        objType := c.checkExpr(ctx, n.Obj)
        valType := c.checkExpr(ctx, n.Value)

        // Struct field assignment
        if objType.Kind == TStruct && c.global != nil {
                if b := c.global.Lookup(objType.Name); b != nil && b.StructInfo != nil {
                        if _, ok := b.StructInfo.FieldTypes[n.Field]; ok {
                                // Check field mutability from the field info list
                                fieldMutable := false
                                for _, fi := range b.StructInfo.Fields {
                                        if fi.Name == n.Field {
                                                fieldMutable = fi.Mutable
                                                break
                                        }
                                }
                                if !fieldMutable {
                                        c.reportError(ctx, n.Obj.Pos(),
                                                "struct field '%s.%s' is immutable", objType.Name, n.Field)
                                }
                                return valType
                        }
                }
                c.reportError(ctx, n.Obj.Pos(),
                        "struct '%s' has no field '%s'", objType.Name, n.Field)
                return valType
        }

        // Member assignment is only valid on tables.
        if objType.IsConcrete() && objType.Kind != TTable && objType.Kind != TGen {
                c.reportError(ctx, n.Obj.Pos(),
                        "member assignment requires a table, got '%s'", objType.String())
        }

        // Check mutability of the target table binding.
        if ident, ok := n.Obj.(*ast.Ident); ok {
                if b, found := c.lookup(ctx, ident.Name); found && !b.Mutable {
                        c.reportError(ctx, n.Obj.Pos(),
                                "'%s' is immutable; use 'mut' to declare mutable bindings", ident.Name)
                }
        }

        return valType
}

func (c *Checker) checkErrorPropExpr(ctx *fnCtx, n *ast.ErrorPropExpr) TypeDesc {
        innerType := c.checkExpr(ctx, n.Expr)

        // The ? operator propagates errors. Ideally the enclosing function
        // should have a non-void return type, but since return type inference
        // happens after body checking, we don't enforce this strictly here.
        // If the function truly returns void, the runtime handles it gracefully.

        // The type of expr? is the same as expr (the non-error variant).
        return innerType
}

func (c *Checker) checkSpawnExpr(ctx *fnCtx, n *ast.SpawnExpr) TypeDesc {
        if n.Call == nil {
                c.reportError(ctx, n.Pos(), "spawn requires a function call")
                return HdlDesc
        }

        // Type-check the inner call expression and capture the return type
        // so that 'await' on this spawn result can resolve correctly.
        callRetType := c.checkExpr(ctx, n.Call)

        // Store the call's return type on the spawn expression itself so
        // checkAwaitExpr can retrieve it when the handle is used directly.
        // We encode it as a handle type with the return type attached.
        if callRetType.Kind != TVoid {
                ctx.exprTypes[n] = TypeDesc{Kind: THdl, Ret: &callRetType}
        }

        return HdlDesc
}

func (c *Checker) checkAwaitExpr(ctx *fnCtx, n *ast.AwaitExpr) TypeDesc {
        handleType := c.checkExpr(ctx, n.Handle)

        if handleType.IsConcrete() && handleType.Kind != THdl && handleType.Kind != TGen {
                c.reportError(ctx, n.Handle.Pos(),
                        "await requires a spawn handle, got '%s'", handleType.String())
        }

        // If the handle comes directly from a spawn expression, we know the
        // spawned function's return type.  Look it up in exprTypes.
        if spawnExpr, ok := n.Handle.(*ast.SpawnExpr); ok {
                if t, ok := ctx.exprTypes[spawnExpr]; ok && t.Kind == THdl && t.Ret != nil {
                        return *t.Ret
                }
        }

        // For indirect handles (stored in variables, passed as args),
        // we cannot statically determine the return type.
        return GenDesc
}

func (c *Checker) checkFnExpr(ctx *fnCtx, n *ast.FnExpr) TypeDesc {
        // Closures without an explicit return type default to GenDesc (not VoidDesc)
        // so that recursive calls and return value expressions don't produce false
        // "void operand" errors.  The actual return type is inferred after body
        // checking via collectReturnTypes.
        retType := GenDesc
        if n.ReturnType != nil {
                retType = c.resolveType(n.ReturnType)
        }
        paramTypes := make([]*TypeDesc, len(n.Params))
        for i, p := range n.Params {
                paramTypes[i] = new(TypeDesc)
                *paramTypes[i] = c.resolveType(&p.Type)
        }

        // Use the resolved (or GenDesc) return type for the self-referential
        // binding.  This is already correct: unannotated = GenDesc,
        // annotated = the declared type.
        selfRefRet := &retType

        // Create a nested scope for the function body.
        c.pushScope(ctx)

        // Save/restore unreachable so inner function returns don't affect outer.
        savedUnreachable := ctx.unreachable
        ctx.unreachable = false

        // Push the closure's return type so that checkReturnStmt validates
        // return statements against this closure, not the outer function.
        ctx.closureRetStack = append(ctx.closureRetStack, retType)

        // If the FnExpr has a name, define it in the scope for self-reference.
        if n.Name != "" {
                fnType := TypeDesc{
                        Kind:   TFunc,
                        Params: paramTypes,
                        Ret:    selfRefRet,
                }
                b := &Binding{
                        Name: n.Name,
                        Kind: BndFn,
                        Type: fnType,
                        Pos:  n.Span,
                }
                c.define(ctx, n.Name, b)
        }

        // Bind parameters.
        for i, p := range n.Params {
                b := &Binding{
                        Name:    p.Name,
                        Kind:    BndVar,
                        Type:    *paramTypes[i],
                        Mutable: p.Mutable,
                        Pos:     p.Span,
                }
                c.define(ctx, p.Name, b)
        }

        // Check the body statements.
        for _, s := range n.Body {
                c.checkStmt(ctx, s)
        }

        c.popScope(ctx)
        ctx.unreachable = savedUnreachable

        // Pop the closure return type stack.
        ctx.closureRetStack = ctx.closureRetStack[:len(ctx.closureRetStack)-1]

        // Infer return type if not explicitly annotated and body has returns.
        // Use collectReturnTypes which recurses into nested if/while/for/match blocks.
        if n.ReturnType == nil {
                first := GenDesc
                c.collectReturnTypes(ctx, n.Body, &first)
                if first.Kind != TGen {
                        retType = first
                }
        }

        return TypeDesc{
                Kind:   TFunc,
                Params: paramTypes,
                Ret:    &retType,
        }
}

func (c *Checker) checkIfExpr(ctx *fnCtx, n *ast.IfExpr) TypeDesc {
        var resultType TypeDesc

        for _, br := range n.Branches {
                condType := c.checkExpr(ctx, br.Cond)
                if condType.IsConcrete() && condType.Kind != TBool && condType.Kind != TGen {
                        c.reportErrorWithCode(ctx, br.Cond.Pos(), "E0601",
                                "if condition must be bool, got '%s'", condType.String())
                }
                c.pushScope(ctx)
                for _, st := range br.Body {
                        c.checkStmt(ctx, st)
                }
                if t := c.lastExprType(ctx, br.Body); t != nil {
                        resultType = *t
                }
                c.popScope(ctx)
        }

        if len(n.Else) > 0 {
                c.pushScope(ctx)
                for _, st := range n.Else {
                        c.checkStmt(ctx, st)
                }
                if t := c.lastExprType(ctx, n.Else); t != nil {
                        resultType = *t
                }
                c.popScope(ctx)
        }

        return resultType
}

func (c *Checker) checkMatchExpr(ctx *fnCtx, n *ast.MatchExpr) TypeDesc {
        subjectType := c.checkExpr(ctx, n.Subject)
        var resultType TypeDesc
        seen := make(map[string]int) // key -> first case index

        for i, cs := range n.Cases {
                caseType := c.checkExpr(ctx, cs.Value)
                if subjectType.IsConcrete() && caseType.IsConcrete() &&
                        !c.assignable(subjectType, caseType) && !c.assignable(caseType, subjectType) {
                        c.reportError(ctx, cs.Value.Pos(),
                                "match case type '%s' is not compatible with subject type '%s'",
                                caseType.String(), subjectType.String())
                }
                // Duplicate pattern detection.
                if key := matchCaseKey(cs.Value); key != "" {
                        if prevIdx, dup := seen[key]; dup {
                                c.reportError(ctx, cs.Value.Pos(),
                                        "duplicate match case '%s' (previously matched on case %d)",
                                        key, prevIdx+1)
                        } else {
                                seen[key] = i
                        }
                }
                c.pushScope(ctx)
                for _, st := range cs.Body {
                        c.checkStmt(ctx, st)
                }
                if t := c.lastExprType(ctx, cs.Body); t != nil {
                        resultType = *t
                }
                c.popScope(ctx)
        }

        // Enum exhaustiveness for match expressions (same logic as checkMatchStmt).
        if subjectType.Kind == TEnum {
                if b := c.global.Lookup(subjectType.Name); b != nil && b.EnumInfo != nil {
                        ei := b.EnumInfo
                        covered := 0
                        for _, v := range ei.Variants {
                                for _, cs := range n.Cases {
                                        if isEnumCaseVariant(cs.Value, subjectType.Name, v.Name) {
                                                covered++
                                                break
                                        }
                                }
                        }
                        if covered < len(ei.Variants) && len(n.Default) == 0 {
                                var missing string
                                for _, v := range ei.Variants {
                                        found := false
                                        for _, cs := range n.Cases {
                                                if isEnumCaseVariant(cs.Value, subjectType.Name, v.Name) {
                                                        found = true
                                                        break
                                                }
                                        }
                                        if !found {
                                                missing = v.Name
                                                break
                                        }
                                }
                                c.reportWarnWithCode(ctx, n.Span, "W0500",
                                        "match on enum '%s' is not exhaustive — missing variant '%s'",
                                        subjectType.Name, missing)
                        }
                }
        }

        return resultType
}

// lastExprType extracts the type of the last expression statement in a body.
// This is used by if-expressions and match-expressions to determine their type.
func (c *Checker) lastExprType(ctx *fnCtx, stmts []ast.Stmt) *TypeDesc {
        if len(stmts) == 0 {
                return nil
        }
        last := stmts[len(stmts)-1]
        if es, ok := last.(*ast.ExprStmt); ok {
                if t, ok := ctx.exprTypes[es.Expr]; ok && t.Kind != TVoid {
                        return &t
                }
        }
        return nil
}

// matchCaseKey extracts a comparable key from a match case expression.
// Returns "" for non-constant expressions (which cannot be checked for duplication).
// The key encodes both the type and value to avoid false positives across types
// (e.g., the string "1" should not collide with the integer 1).
// isEnumCaseVariant returns true if the match case value matches the
// given enum name and variant name.  Handles both EnumLit and
// EnumMatchPattern (destructuring patterns).
func isEnumCaseVariant(val ast.Expr, enumName, variantName string) bool {
        switch v := val.(type) {
        case *ast.EnumLit:
                return v.EnumName == enumName && v.VariantName == variantName
        case *ast.EnumMatchPattern:
                return v.EnumName == enumName && v.VariantName == variantName
        }
        return false
}

func matchCaseKey(e ast.Expr) string {
        switch v := e.(type) {
        case *ast.IntLit:
                return "int:" + strconv.FormatInt(v.Value, 10)
        case *ast.FloatLit:
                return "fp:" + strconv.FormatFloat(v.Value, 'g', -1, 64)
        case *ast.StringLit:
                return "str:" + v.Value
        case *ast.BoolLit:
                if v.Value {
                        return "bool:true"
                }
                return "bool:false"
        case *ast.NilLit:
                return "nil"
        case *ast.Ident:
                // Constants resolved by the checker may appear as idents.
                // Use the name as a fallback key; this catches duplicate const names.
                return "ident:" + v.Name
        case *ast.EnumLit:
                return "enum:" + v.EnumName + "." + v.VariantName
        case *ast.EnumMatchPattern:
                return "enum:" + v.EnumName + "." + v.VariantName
        case *ast.UnaryOp:
                // Handle -42, ~0xFF, not true as match cases.
                if v.Op == ast.TMinus {
                        if lit, ok := v.Operand.(*ast.IntLit); ok {
                                return "int:" + strconv.FormatInt(-lit.Value, 10)
                        }
                        if lit, ok := v.Operand.(*ast.FloatLit); ok {
                                return "fp:" + strconv.FormatFloat(-lit.Value, 'g', -1, 64)
                        }
                }
                if v.Op == ast.TTilde {
                        if lit, ok := v.Operand.(*ast.IntLit); ok {
                                return "int:" + strconv.FormatInt(^lit.Value, 10)
                        }
                }
                if v.Op == ast.TNot {
                        if lit, ok := v.Operand.(*ast.BoolLit); ok {
                                return "bool:" + strconv.FormatBool(!lit.Value)
                        }
                }
        }
        return "" // non-constant expression, skip duplicate check
}

// ---------------------------------------------------------------------------
// Type compatibility helpers
// ---------------------------------------------------------------------------

// assignable reports whether a value of type src can be assigned to a
// variable of type dst.  The rules are:
//   - gen is assignable to everything and from everything.
//   - Identical concrete types are assignable.
//   - int is assignable to fp (implicit widening).
//   - int is assignable to uint and vice versa (with potential data loss).
//   - void is assignable to void.
func (c *Checker) assignable(dst, src TypeDesc) bool {
        if dst.Kind == TGen || src.Kind == TGen {
                return true
        }
        if dst.Kind == TVoid && src.Kind == TVoid {
                return true
        }
        if dst.Kind == src.Kind {
                // For named types, also compare names.
                if dst.Kind == TNamed && src.Kind == TNamed {
                        return dst.Name == src.Name
                }
                return true
        }
        // Implicit numeric widening: int -> fp, uint -> fp.
        if dst.Kind == TFp && (src.Kind == TInt || src.Kind == TUint) {
                return true
        }
        // Allow int <-> uint assignment (with a warning elsewhere).
        if (dst.Kind == TInt && src.Kind == TUint) || (dst.Kind == TUint && src.Kind == TInt) {
                return true
        }
        return false
}

// ---------------------------------------------------------------------------
// Unused variable detection
// ---------------------------------------------------------------------------

// checkUnusedVars scans all scopes (except the function scope which holds
// parameters) for bindings that were defined but never referenced, and
// emits a warning for each.  Variables whose name starts with '_' are
// silently skipped — the underscore prefix is the conventional way to
// indicate intentionally unused bindings.
func (c *Checker) checkUnusedVars(ctx *fnCtx) {
        // The first scope is the function scope (parameters).  Start from index 1
        // to skip parameters — parameter usage warnings are rarely useful.
        for i := 1; i < len(ctx.scopes); i++ {
                scope := ctx.scopes[i]
                for _, name := range scope.Names() {
                        // Skip variables already prefixed with '_' — they are
                        // intentionally unused and should not produce a warning.
                        if strings.HasPrefix(name, "_") {
                                continue
                        }
                        if !ctx.usedBindings[name] {
                                b, ok := scope.LookupLocal(name)
                                if ok && b.Kind == BndVar {
                                        c.reportWarnWithCode(ctx, b.Pos, "W0101",
                                                "unused variable '%s'", name)
                                        c.reportHelp(ctx, b.Pos,
                                                "prefix with '_' to suppress: '_%s'", name)
                                }
                        }
                }
        }
}

// ---------------------------------------------------------------------------
// Dead function detection
// ---------------------------------------------------------------------------

// checkDeadFunctions reports a warning for every user-defined function that is
// never called from any function body.  Entry-point functions (main, _start,
// yilt_main_abi) are always considered "used".  Functions whose name starts
// with '_' are exempt to allow intentionally-dead helpers.
func (c *Checker) checkDeadFunctions() {
        called := make(map[string]bool)

        // Entry points are always considered called.
        called["main"] = true
        called["_start"] = true
        called["yilt_main_abi"] = true

        // Also mark functions referenced by import bindings as called.
        for _, imp := range c.imports {
                for _, b := range imp.Symbols {
                        if b.Kind == BndFn {
                                called[b.Name] = true
                        }
                }
        }

        // Walk every function body, recording which user-defined functions are
        // referenced.
        for _, fn := range c.funcs {
                if fn.Extern {
                        continue
                }
                if fn.exprTypes == nil {
                        continue
                }
                c.walkExprs(fn.Body, func(e ast.Expr) {
                        if ident, ok := e.(*ast.Ident); ok {
                                if _, exists := c.funcs[ident.Name]; exists {
                                        called[ident.Name] = true
                                }
                        }
                        // Also check CallExpr.Func for member access calls (mod.fn).
                        if call, ok := e.(*ast.CallExpr); ok {
                                if member, ok := call.Func.(*ast.MemberExpr); ok {
                                        if obj, ok := member.Obj.(*ast.Ident); ok {
                                                called[obj.Name] = true
                                        }
                                        // Check for struct method calls: p.distance(other)
                                        // The type of the object determines the method function name.
                                        if td, ok := fn.exprTypes[member.Obj]; ok && td.Kind == TStruct {
                                                methodName := td.Name + "_" + member.Field
                                                if _, exists := c.funcs[methodName]; exists {
                                                        called[methodName] = true
                                                }
                                        }
                                }
                                // Check for enum variant constructor calls: Color.Red,
                                // Result.Ok(val). The lowerer generates helper
                                // functions named "EnumName__VariantName".
                                if ident, ok := call.Func.(*ast.Ident); ok {
                                        if _, exists := c.funcs[ident.Name]; exists {
                                                called[ident.Name] = true
                                        }
                                }
                        }
                        // Also check EnumLit references the enum name.
                        if enumLit, ok := e.(*ast.EnumLit); ok {
                                called[enumLit.EnumName] = true
                        }
                })

        }

        // Report any non-entry, non-extern function that was never called.
        for name, fn := range c.funcs {
                if fn.Extern {
                        continue
                }
                // Skip functions prefixed with '_' (convention for intentionally unused).
                if len(name) > 0 && name[0] == '_' {
                        continue
                }
                if !called[name] {
                        c.mu.Lock()
                        c.warnCodeAt(fn.Pos, "W0102",
                                "function '%s' is defined but never used", name)
                        c.handler.Help("remove unused function or prefix with '_' to suppress")
                        c.mu.Unlock()
                }
        }
}

// walkExprs visits every expression reachable from a list of statements.
func (c *Checker) walkExprs(stmts []ast.Stmt, visit func(ast.Expr)) {
        for _, s := range stmts {
                c.walkStmtExprs(s, visit)
        }
}

// walkStmtExprs visits every expression inside a single statement.
func (c *Checker) walkStmtExprs(s ast.Stmt, visit func(ast.Expr)) {
        switch stmt := s.(type) {
        case *ast.LetStmt:
                c.walkExpr(stmt.Value, visit)
        case *ast.ExprStmt:
                c.walkExpr(stmt.Expr, visit)
        case *ast.ReturnStmt:
                if stmt.Value != nil {
                        c.walkExpr(stmt.Value, visit)
                }
        case *ast.IfStmt:
                for _, br := range stmt.Branches {
                        c.walkExpr(br.Cond, visit)
                        c.walkExprs(br.Body, visit)
                }
                c.walkExprs(stmt.Else, visit)
        case *ast.WhileStmt:
                c.walkExpr(stmt.Cond, visit)
                c.walkExprs(stmt.Body, visit)
        case *ast.ForStmt:
                c.walkExpr(stmt.Over, visit)
                c.walkExprs(stmt.Body, visit)
        case *ast.MatchStmt:
                c.walkExpr(stmt.Subject, visit)
                for _, cs := range stmt.Cases {
                        c.walkExpr(cs.Value, visit)
                        c.walkExprs(cs.Body, visit)
                }
                c.walkExprs(stmt.Default, visit)
        case *ast.BreakStmt, *ast.ContinueStmt:
                // no expressions
        case *ast.ConstStmt:
                c.walkExpr(stmt.Value, visit)
        case *ast.AssertStmt:
                c.walkExpr(stmt.Cond, visit)
                if stmt.Message != nil {
                        c.walkExpr(stmt.Message, visit)
                }
        }
}

// walkExpr recursively visits an expression and its sub-expressions.
func (c *Checker) walkExpr(e ast.Expr, visit func(ast.Expr)) {
        if e == nil {
                return
        }
        visit(e)
        switch expr := e.(type) {
        case *ast.BinOp:
                c.walkExpr(expr.Left, visit)
                c.walkExpr(expr.Right, visit)
        case *ast.UnaryOp:
                c.walkExpr(expr.Operand, visit)
        case *ast.CallExpr:
                c.walkExpr(expr.Func, visit)
                for _, arg := range expr.Args {
                        c.walkExpr(arg, visit)
                }
        case *ast.IndexExpr:
                c.walkExpr(expr.Obj, visit)
                c.walkExpr(expr.Key, visit)
        case *ast.MemberExpr:
                c.walkExpr(expr.Obj, visit)
        case *ast.TableLit:
                for _, entry := range expr.Entries {
                        c.walkExpr(entry.Key, visit)
                        c.walkExpr(entry.Value, visit)
                }
        case *ast.StructLit:
                for _, f := range expr.Fields {
                        c.walkExpr(f.Value, visit)
                }
        case *ast.EnumLit:
                if expr.Payload != nil {
                        c.walkExpr(expr.Payload, visit)
                }
        case *ast.TypeArgIdent:
                // No sub-expressions to walk
        case *ast.AssignExpr:
                c.walkExpr(expr.Target, visit)
                c.walkExpr(expr.Value, visit)
        case *ast.IndexAssignExpr:
                c.walkExpr(expr.Obj, visit)
                c.walkExpr(expr.Key, visit)
                c.walkExpr(expr.Value, visit)
        case *ast.MemberAssignExpr:
                c.walkExpr(expr.Obj, visit)
                c.walkExpr(expr.Value, visit)
        case *ast.ErrorPropExpr:
                c.walkExpr(expr.Expr, visit)
        case *ast.SpawnExpr:
                c.walkExpr(expr.Call, visit)
        case *ast.AwaitExpr:
                c.walkExpr(expr.Handle, visit)
        case *ast.FnExpr:
                for _, s := range expr.Body {
                        c.walkStmtExprs(s, visit)
                }
        case *ast.IfExpr:
                for _, br := range expr.Branches {
                        c.walkExpr(br.Cond, visit)
                        c.walkExprs(br.Body, visit)
                }
                c.walkExprs(expr.Else, visit)
        case *ast.MatchExpr:
                c.walkExpr(expr.Subject, visit)
                for _, cs := range expr.Cases {
                        c.walkExpr(cs.Value, visit)
                        c.walkExprs(cs.Body, visit)
                }
                c.walkExprs(expr.Default, visit)
        case *ast.InterpStr:
                for _, part := range expr.Parts {
                        if part.Expr != nil {
                                c.walkExpr(part.Expr, visit)
                        }
                }
        }
}

// ---------------------------------------------------------------------------
// W0200 — Nil safety
// ---------------------------------------------------------------------------
// See: checkMemberExpr, checkCallExpr, checkIndexExpr for the actual
// warning emission.  The isDefinitelyAssigned / markAssigned helpers above
// drive the analysis.

// ---------------------------------------------------------------------------
// W0300 — Integer overflow checking
// ---------------------------------------------------------------------------

// checkIntOverflow warns (W0300) when a binary operation between two integer
// literals would overflow int64.  Only arithmetic operators are checked, and
// only when BOTH operands are literal integers.
func (c *Checker) checkIntOverflow(ctx *fnCtx, n *ast.BinOp) {
        // Only check arithmetic operators.
        switch n.Op {
        case ast.TPlus, ast.TMinus, ast.TStar, ast.TSlash:
        default:
                return
        }

        leftLit, okL := n.Left.(*ast.IntLit)
        rightLit, okR := n.Right.(*ast.IntLit)
        if !okL || !okR {
                return // not both literal integers
        }

        a, b := leftLit.Value, rightLit.Value

        var overflow bool
        switch n.Op {
        case ast.TPlus:
                overflow = intAddOverflows(a, b)
        case ast.TMinus:
                overflow = intSubOverflows(a, b)
        case ast.TStar:
                if a == 0 || b == 0 {
                        return // 0 * anything = 0, no overflow
                }
                overflow = intMulOverflows(a, b)
        case ast.TSlash:
                if b == 0 {
                        return // division by zero is a runtime error, not overflow
                }
                // Only int64 overflow case: MinInt64 / -1
                if a == math.MinInt64 && b == -1 {
                        overflow = true
                }
        }

        if overflow {
                c.reportWarnWithCode(ctx, n.Pos(), "W0300",
                        "integer overflow in '%v %s %v'", a, n.Op, b)
        }
}

// intAddOverflows reports whether a + b would overflow int64.
func intAddOverflows(a, b int64) bool {
        if b > 0 && a > math.MaxInt64-b {
                return true
        }
        if b < 0 && a < math.MinInt64-b {
                return true
        }
        return false
}

// intSubOverflows reports whether a - b would overflow int64.
func intSubOverflows(a, b int64) bool {
        if b < 0 && a > math.MaxInt64+b {
                return true
        }
        if b > 0 && a < math.MinInt64+b {
                return true
        }
        return false
}

// intMulOverflows reports whether a * b would overflow int64.
func intMulOverflows(a, b int64) bool {
        if a > 0 {
                if b > 0 {
                        return a > math.MaxInt64/b
                }
                return b < math.MinInt64/a
        }
        if a < 0 {
                if b > 0 {
                        return a < math.MinInt64/b
                }
                if b < 0 {
                        return b < math.MaxInt64/a
                }
        }
        return false // a or b is 0
}

// ---------------------------------------------------------------------------
// W0400 — Dead code detection (constant conditions)
// ---------------------------------------------------------------------------

// checkDeadCodeIfBranch warns about dead branches in if-statements whose
// condition is a compile-time constant.  This should be called from
// checkIfStmt after the condition expression has been type-checked.
func (c *Checker) checkDeadCodeIfBranch(ctx *fnCtx, s *ast.IfStmt) {
        // Only inspect single-branch if with a constant condition.
        // For chains (if/elif/elif/else) we only check each branch's condition.
        for _, br := range s.Branches {
                truthy := isCompileTimeTruthy(br.Cond)
                if truthy == nil {
                        continue // not a compile-time constant
                }
                if *truthy {
                        // Condition is always true — if there is an else branch,
                        // it is dead code.
                        if len(s.Else) > 0 {
                                c.reportWarnWithCode(ctx, s.Else[0].Pos(), "W0400",
                                        "else branch is unreachable; if condition is always true")
                        }
                } else {
                        // Condition is always false — this branch is dead code.
                        if len(br.Body) > 0 {
                                c.reportWarnWithCode(ctx, br.Body[0].Pos(), "W0400",
                                        "branch is unreachable; if condition is always false")
                        }
                }
                // Only report for the first branch in a chain to avoid
                // cascading warnings on elif chains.
                break
        }
}

// isCompileTimeTruthy returns a pointer to bool if the expression is a
// compile-time constant whose truthiness can be determined:
//   - BoolLit: true/false
//   - IntLit: non-zero is truthy, zero is falsy
// Returns nil if the truthiness cannot be determined at compile time.
func isCompileTimeTruthy(e ast.Expr) *bool {
        switch lit := e.(type) {
        case *ast.BoolLit:
                return &lit.Value
        case *ast.IntLit:
                v := lit.Value != 0
                return &v
        }
        return nil
}

// ---------------------------------------------------------------------------
// Diagnostic helpers (thread-safe)
// ---------------------------------------------------------------------------

// reportError emits a diagnostic error, using the mutex to synchronise
// access to the shared DiagnosticHandler.  Respects ast.Pos.SpanLen for
// multi-character source highlighting.
func (c *Checker) reportError(ctx *fnCtx, pos ast.Pos, format string, args ...interface{}) {
        c.mu.Lock()
        defer c.mu.Unlock()
        c.errorAt(pos, format, args...)
}

// reportWarn emits a diagnostic warning (thread-safe).
// Respects ast.Pos.SpanLen for multi-character source highlighting.
func (c *Checker) reportWarn(ctx *fnCtx, pos ast.Pos, format string, args ...interface{}) {
        c.mu.Lock()
        defer c.mu.Unlock()
        c.warnAt(pos, format, args...)
}

// errorAt emits a diagnostic error at pos, using SpanLen if set.
// Caller must hold c.mu (or be in a single-threaded context).
func (c *Checker) errorAt(pos ast.Pos, format string, args ...interface{}) {
        msg := fmt.Sprintf(format, args...)
        if pos.SpanLen > 0 {
                c.handler.ErrorSpan(pos.File, pos.Line, pos.Col, pos.Offset, pos.SpanLen, msg)
        } else {
                c.handler.Error(pos.File, pos.Line, pos.Col, pos.Offset, msg)
        }
}

// warnAt emits a diagnostic warning at pos, using SpanLen if set.
// Caller must hold c.mu (or be in a single-threaded context).
func (c *Checker) warnAt(pos ast.Pos, format string, args ...interface{}) {
        msg := fmt.Sprintf(format, args...)
        if pos.SpanLen > 0 {
                c.handler.WarnSpan(pos.File, pos.Line, pos.Col, pos.Offset, pos.SpanLen, msg)
        } else {
                c.handler.Warn(pos.File, pos.Line, pos.Col, pos.Offset, msg)
        }
}

// errorCodeAt emits a diagnostic error with an error code at pos.
// Caller must hold c.mu (or be in a single-threaded context).
func (c *Checker) errorCodeAt(pos ast.Pos, code string, format string, args ...interface{}) {
        msg := fmt.Sprintf(format, args...)
        c.handler.ErrorWithCode(pos.File, pos.Line, pos.Col, pos.Offset, code, msg)
}

// warnCodeAt emits a diagnostic warning with an error code at pos.
// Caller must hold c.mu (or be in a single-threaded context).
func (c *Checker) warnCodeAt(pos ast.Pos, code string, format string, args ...interface{}) {
        msg := fmt.Sprintf(format, args...)
        c.handler.WarnWithCode(pos.File, pos.Line, pos.Col, pos.Offset, code, msg)
}

// reportErrorWithCode emits a diagnostic error with an error code (thread-safe).
func (c *Checker) reportErrorWithCode(ctx *fnCtx, pos ast.Pos, code string, format string, args ...interface{}) {
        c.mu.Lock()
        defer c.mu.Unlock()
        c.errorCodeAt(pos, code, format, args...)
}

// reportWarnWithCode emits a diagnostic warning with an error code (thread-safe).
func (c *Checker) reportWarnWithCode(ctx *fnCtx, pos ast.Pos, code string, format string, args ...interface{}) {
        c.mu.Lock()
        defer c.mu.Unlock()
        c.warnCodeAt(pos, code, format, args...)
}

// reportHelp attaches a help note to the most recent diagnostic (thread-safe).
func (c *Checker) reportHelp(ctx *fnCtx, pos ast.Pos, format string, args ...interface{}) {
        c.mu.Lock()
        defer c.mu.Unlock()
        c.handler.Help(fmt.Sprintf(format, args...))
}
