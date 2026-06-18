package check

import (
        "strings"
        "testing"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/diag"
        "github.com/yilt/yiltc/internal/lex"
        "github.com/yilt/yiltc/internal/parse"
)

// helper builds a CheckedProgram from source, returning the result and any errors.
func checkSource(t *testing.T, src string) (*CheckedProgram, []string) {
        t.Helper()
        dh := diag.NewHandler(false)
        dh.AddSource("test.yilt", []byte(src))

        lexer := lex.New("test.yilt", []byte(src))
        lexer.SetDiag(dh)
        tokens, indents := lexer.Tokenize()
        if dh.HasErrors() {
                return nil, dh.ErrorMessages()
        }

        parser := parse.New("test.yilt", tokens, indents, []byte(src))
        file := parser.ParseFile()
        for _, pe := range parser.Errors() {
                dh.Error(pe.File, pe.Line, pe.Col, pe.Offset, pe.Msg)
        }
        if dh.HasErrors() {
                return nil, dh.ErrorMessages()
        }

        program := &ast.Program{
                Files: []*ast.File{file},
                Root:  ".",
        }
        checker := NewChecker(program, dh)
        checked, _ := checker.Check()
        return checked, dh.ErrorMessages()
}

// checkMultiSource builds a CheckedProgram from multiple source files,
// returning the result and any errors. The map keys are filenames.
func checkMultiSource(t *testing.T, files map[string]string) (*CheckedProgram, []string) {
        t.Helper()
        dh := diag.NewHandler(false)
        var astFiles []*ast.File
        for name, src := range files {
                dh.AddSource(name, []byte(src))
                lexer := lex.New(name, []byte(src))
                lexer.SetDiag(dh)
                tokens, indents := lexer.Tokenize()
                if dh.HasErrors() {
                        return nil, dh.ErrorMessages()
                }
                parser := parse.New(name, tokens, indents, []byte(src))
                file := parser.ParseFile()
                for _, pe := range parser.Errors() {
                        dh.Error(pe.File, pe.Line, pe.Col, pe.Offset, pe.Msg)
                }
                if dh.HasErrors() {
                        return nil, dh.ErrorMessages()
                }
                astFiles = append(astFiles, file)
        }
        program := &ast.Program{
                Files: astFiles,
                Root:  ".",
        }
        checker := NewChecker(program, dh)
        checked, _ := checker.Check()
        return checked, dh.ErrorMessages()
}

// ---------------------------------------------------------------------------
// Type inference tests
// ---------------------------------------------------------------------------

func TestInferReturnType_TopLevel(t *testing.T) {
        prog, errs := checkSource(t, "fn add(a int, b int)\n    return a + b\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        fn, ok := prog.Functions["add"]
        if !ok {
                t.Fatal("function 'add' not found")
        }
        if fn.RetType.Kind != TInt {
                t.Errorf("expected int return type, got %s", fn.RetType.String())
        }
}

func TestInferReturnType_NestedInIf(t *testing.T) {
        prog, errs := checkSource(t, "fn abs_val(x int)\n    if x < 0\n        return 0 - x\n    else\n        return x\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        fn, ok := prog.Functions["abs_val"]
        if !ok {
                t.Fatal("function 'abs_val' not found")
        }
        if fn.RetType.Kind != TInt {
                t.Errorf("expected int return type, got %s", fn.RetType.String())
        }
}

func TestInferReturnType_NestedInFor(t *testing.T) {
        prog, errs := checkSource(t, "fn find_first(items table, target str)\n    for k, v in items\n        if v == target\n            return k\n    return -1\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        fn, ok := prog.Functions["find_first"]
        if !ok {
                t.Fatal("function 'find_first' not found")
        }
        if fn.RetType.Kind != TInt {
                t.Errorf("expected int return type, got %s", fn.RetType.String())
        }
}

func TestInferReturnType_NestedInMatch(t *testing.T) {
        prog, errs := checkSource(t, "fn classify(x int)\n    match x\n        case 1\n            return \"one\"\n        case 2\n            return \"two\"\n        default\n            return \"other\"\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        fn, ok := prog.Functions["classify"]
        if !ok {
                t.Fatal("function 'classify' not found")
        }
        if fn.RetType.Kind != TStr {
                t.Errorf("expected str return type, got %s", fn.RetType.String())
        }
}

func TestInferReturnType_NestedInWhile(t *testing.T) {
        prog, errs := checkSource(t, "fn countdown(n int)\n    let mut i = n\n    while i > 0\n        i = i - 1\n        if i == 0\n            return i\n    return -1\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        fn, ok := prog.Functions["countdown"]
        if !ok {
                t.Fatal("function 'countdown' not found")
        }
        if fn.RetType.Kind != TInt {
                t.Errorf("expected int return type, got %s", fn.RetType.String())
        }
}

func TestInferReturnType_InconsistentTypes(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    if true\n        return 42\n    else\n        return \"oops\"\n")
        if len(errs) == 0 {
                t.Fatal("expected error for inconsistent return types")
        }
        found := false
        for _, e := range errs {
                if strings.Contains(e, "inconsistent return type") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected 'inconsistent return type' error, got: %v", errs)
        }
}

// ---------------------------------------------------------------------------
// Expression type tests
// ---------------------------------------------------------------------------

func TestExprType_IntArithmetic(t *testing.T) {
        prog, errs := checkSource(t, "fn compute()\n    let a = 10 + 20\n    let b = a * 3\n    return b\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["compute"].RetType.Kind != TInt {
                t.Errorf("expected int, got %s", prog.Functions["compute"].RetType.String())
        }
}

func TestExprType_FloatArithmetic(t *testing.T) {
        prog, errs := checkSource(t, "fn compute()\n    let a = 1.5 + 2.5\n    return a\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["compute"].RetType.Kind != TFp {
                t.Errorf("expected fp, got %s", prog.Functions["compute"].RetType.String())
        }
}

func TestExprType_IntFloatPromotion(t *testing.T) {
        prog, errs := checkSource(t, "fn promote()\n    return 3 + 1.5\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["promote"].RetType.Kind != TFp {
                t.Errorf("expected fp (promotion), got %s", prog.Functions["promote"].RetType.String())
        }
}

func TestExprType_StringConcat(t *testing.T) {
        prog, errs := checkSource(t, "fn concat()\n    return \"hello\" + \" \" + \"world\"\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["concat"].RetType.Kind != TStr {
                t.Errorf("expected str, got %s", prog.Functions["concat"].RetType.String())
        }
}

func TestExprType_BoolOps(t *testing.T) {
        prog, errs := checkSource(t, "fn logic()\n    let a = true and false\n    let b = true or false\n    let c = not true\n    return c\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["logic"].RetType.Kind != TBool {
                t.Errorf("expected bool, got %s", prog.Functions["logic"].RetType.String())
        }
}

func TestExprType_Comparison(t *testing.T) {
        prog, errs := checkSource(t, "fn cmp()\n    let a = 10 < 20\n    let b = \"abc\" == \"def\"\n    return a\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["cmp"].RetType.Kind != TBool {
                t.Errorf("expected bool, got %s", prog.Functions["cmp"].RetType.String())
        }
}

func TestExprType_TableLiteral(t *testing.T) {
        prog, errs := checkSource(t, "fn mktable()\n    let t = {}\n    return t\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["mktable"].RetType.Kind != TTable {
                t.Errorf("expected table, got %s", prog.Functions["mktable"].RetType.String())
        }
}

// ---------------------------------------------------------------------------
// String method tests
// ---------------------------------------------------------------------------

func TestStringMethods_ReturnTypes(t *testing.T) {
        tests := []struct {
                name     string
                src      string
                wantKind TType
        }{
                {"len", "fn f()\n    let s = \"hi\"\n    return s.len()\n", TInt},
                {"upper", "fn f()\n    let s = \"hi\"\n    return s.upper()\n", TStr},
                {"trim", "fn f()\n    let s = \"hi\"\n    return s.trim()\n", TStr},
                {"contains", "fn f()\n    let s = \"hi\"\n    return s.contains(\"h\")\n", TBool},
                {"starts_with", "fn f()\n    let s = \"hi\"\n    return s.starts_with(\"h\")\n", TBool},
                {"ends_with", "fn f()\n    let s = \"hi\"\n    return s.ends_with(\"i\")\n", TBool},
                {"find", "fn f()\n    let s = \"hi\"\n    return s.find(\"h\")\n", TInt},
                {"replace", "fn f()\n    let s = \"hi\"\n    return s.replace(\"h\", \"j\")\n", TStr},
                {"split", "fn f()\n    let s = \"hi\"\n    return s.split(\",\")\n", TTable},
                {"substr", "fn f()\n    let s = \"hi\"\n    return s.substr(0, 1)\n", TStr},
                {"to_int", "fn f()\n    let s = \"hi\"\n    return s.to_int()\n", TInt},
        }
        for _, tc := range tests {
                t.Run(tc.name, func(t *testing.T) {
                        prog, errs := checkSource(t, tc.src)
                        if len(errs) > 0 {
                                t.Fatalf("unexpected errors: %v", errs)
                        }
                        fn, ok := prog.Functions["f"]
                        if !ok {
                                t.Fatalf("function 'f' not found")
                        }
                        if fn.RetType.Kind != tc.wantKind {
                                t.Errorf("expected %s, got %s", tc.wantKind.String(), fn.RetType.String())
                        }
                })
        }
}

// ---------------------------------------------------------------------------
// Integer/Float method tests
// ---------------------------------------------------------------------------

func TestIntMethods_ReturnTypes(t *testing.T) {
        tests := []struct {
                name     string
                src      string
                wantKind TType
        }{
                {"to_str", "fn f()\n    let x = 42\n    return x.to_str()\n", TStr},
                {"to_fp", "fn f()\n    let x = 42\n    return x.to_fp()\n", TFp},
                {"abs", "fn f()\n    let x = 42\n    return x.abs()\n", TInt},
                {"neg", "fn f()\n    let x = 42\n    return x.neg()\n", TInt},
                {"sign", "fn f()\n    let x = 42\n    return x.sign()\n", TInt},
                {"is_zero", "fn f()\n    let x = 42\n    return x.is_zero()\n", TInt},
                {"bit_length", "fn f()\n    let x = 42\n    return x.bit_length()\n", TInt},
        }
        for _, tc := range tests {
                t.Run(tc.name, func(t *testing.T) {
                        prog, errs := checkSource(t, tc.src)
                        if len(errs) > 0 {
                                t.Fatalf("unexpected errors: %v", errs)
                        }
                        if prog.Functions["f"].RetType.Kind != tc.wantKind {
                                t.Errorf("expected %s, got %s", tc.wantKind.String(), prog.Functions["f"].RetType.String())
                        }
                })
        }
}

func TestFpMethods_ReturnTypes(t *testing.T) {
        tests := []struct {
                name     string
                src      string
                wantKind TType
        }{
                {"to_str", "fn f()\n    let x = 3.14\n    return x.to_str()\n", TStr},
                {"to_int", "fn f()\n    let x = 3.14\n    return x.to_int()\n", TInt},
                {"floor", "fn f()\n    let x = 3.14\n    return x.floor()\n", TFp},
                {"ceil", "fn f()\n    let x = 3.14\n    return x.ceil()\n", TFp},
                {"sqrt", "fn f()\n    let x = 3.14\n    return x.sqrt()\n", TFp},
                {"is_nan", "fn f()\n    let x = 3.14\n    return x.is_nan()\n", TBool},
                {"is_inf", "fn f()\n    let x = 3.14\n    return x.is_inf()\n", TBool},
                {"sign", "fn f()\n    let x = 3.14\n    return x.sign()\n", TInt},
        }
        for _, tc := range tests {
                t.Run(tc.name, func(t *testing.T) {
                        prog, errs := checkSource(t, tc.src)
                        if len(errs) > 0 {
                                t.Fatalf("unexpected errors: %v", errs)
                        }
                        if prog.Functions["f"].RetType.Kind != tc.wantKind {
                                t.Errorf("expected %s, got %s", tc.wantKind.String(), prog.Functions["f"].RetType.String())
                        }
                })
        }
}

// ---------------------------------------------------------------------------
// Table method tests
// ---------------------------------------------------------------------------

func TestTableMethods_ReturnTypes(t *testing.T) {
        tests := []struct {
                name     string
                src      string
                wantKind TType
        }{
                {"len", "fn f()\n    let t = {}\n    return t.len()\n", TInt},
                {"is_empty", "fn f()\n    let t = {}\n    return t.is_empty()\n", TInt},
                {"has", "fn f()\n    let t = {}\n    return t.has(\"x\")\n", TBool},
                {"get", "fn f()\n    let t = {}\n    return t.get(\"x\")\n", TGen},
                {"keys", "fn f()\n    let t = {}\n    return t.keys()\n", TTable},
                {"values", "fn f()\n    let t = {}\n    return t.values()\n", TTable},
                {"clone", "fn f()\n    let t = {}\n    return t.clone()\n", TGen},
        }
        for _, tc := range tests {
                t.Run(tc.name, func(t *testing.T) {
                        prog, errs := checkSource(t, tc.src)
                        if len(errs) > 0 {
                                t.Fatalf("unexpected errors: %v", errs)
                        }
                        if prog.Functions["f"].RetType.Kind != tc.wantKind {
                                t.Errorf("expected %s, got %s", tc.wantKind.String(), prog.Functions["f"].RetType.String())
                        }
                })
        }
}

// ---------------------------------------------------------------------------
// Negative tests — should produce errors
// ---------------------------------------------------------------------------

func TestError_ArithmeticOnString(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    return \"hello\" - \"world\"\n")
        if len(errs) == 0 {
                t.Fatal("expected error for arithmetic on string")
        }
}

func TestError_NonBoolCondition(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    if 42\n        return 1\n")
        if len(errs) == 0 {
                t.Fatal("expected error for non-bool condition")
        }
}

func TestError_NotOnInt(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    return not 42\n")
        if len(errs) == 0 {
                t.Fatal("expected error for 'not' on int")
        }
}

func TestError_ImmutableAssign(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    let x = 10\n    x = 20\n")
        if len(errs) == 0 {
                t.Fatal("expected error for immutable assignment")
        }
}

func TestError_UndefinedVar(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    return y\n")
        if len(errs) == 0 {
                t.Fatal("expected error for undefined variable")
        }
}

func TestError_DuplicateFunction(t *testing.T) {
        _, errs := checkSource(t, "fn foo()\n    return 1\n\nfn foo()\n    return 2\n")
        if len(errs) == 0 {
                t.Fatal("expected error for duplicate function")
        }
}

func TestError_DuplicateLocalVar(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    let x = 1\n    let x = 2\n")
        if len(errs) == 0 {
                t.Fatal("expected error for duplicate local var")
        }
}

func TestError_BreakOutsideLoop(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    break\n")
        if len(errs) == 0 {
                t.Fatal("expected error for break outside loop")
        }
}

func TestError_ContinueOutsideLoop(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    continue\n")
        if len(errs) == 0 {
                t.Fatal("expected error for continue outside loop")
        }
}

func TestError_IndexNonTable(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    let x = 42\n    return x[0]\n")
        if len(errs) == 0 {
                t.Fatal("expected error for index on non-table")
        }
}

func TestError_MixedTypeComparison(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    return 42 < \"hello\"\n")
        if len(errs) == 0 {
                t.Fatal("expected error for mixed type comparison")
        }
}

func TestError_ImmutableIndexAssign(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    let t = {}\n    t[0] = \"x\"\n")
        if len(errs) == 0 {
                t.Fatal("expected error for index assign on immutable table")
        }
}

func TestError_WrongArgCount(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    return abs()\n")
        if len(errs) == 0 {
                t.Fatal("expected error for wrong argument count")
        }
}

func TestError_UnreachableCode(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    return 1\n    return 2\n")
        if len(errs) == 0 {
                t.Fatal("expected error for unreachable code")
        }
}

func TestError_MissingReturn(t *testing.T) {
        _, errs := checkSource(t, "fn bad() int\n    let x = 10\n")
        if len(errs) == 0 {
                t.Fatal("expected error for missing return")
        }
}

// ---------------------------------------------------------------------------
// Spawn/await tests
// ---------------------------------------------------------------------------

func TestSpawnAwait_Basic(t *testing.T) {
        _, errs := checkSource(t, "fn worker()\n    return 42\n\nfn main()\n    let h = spawn worker()\n    let result = await h\n    return result\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
}

// ---------------------------------------------------------------------------
// Module access tests
// ---------------------------------------------------------------------------

func TestStdlibModuleAccess(t *testing.T) {
        prog, errs := checkSource(t, "fn test()\n    let x = sys.platform\n    let y = math.pi\n    return x\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        fn := prog.Functions["test"]
        if fn.RetType.Kind != TStr {
                t.Errorf("expected str (sys.platform), got %s", fn.RetType.String())
        }
}

func TestUnknownModuleSymbol(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    let x = sys.nonexistent\n    return x\n")
        if len(errs) == 0 {
                t.Fatal("expected error for unknown module symbol")
        }
}

// ---------------------------------------------------------------------------
// Type system unit tests
// ---------------------------------------------------------------------------

func TestTypeDesc_Equals(t *testing.T) {
        tests := []struct {
                a, b TypeDesc
                want bool
        }{
                {IntDesc, IntDesc, true},
                {IntDesc, FpDesc, false},
                {StrDesc, StrDesc, true},
                {GenDesc, IntDesc, false},
                {
                        TypeDesc{Kind: TFunc, Params: []*TypeDesc{&IntDesc}, Ret: &StrDesc},
                        TypeDesc{Kind: TFunc, Params: []*TypeDesc{&IntDesc}, Ret: &StrDesc},
                        true,
                },
                {
                        TypeDesc{Kind: TFunc, Params: []*TypeDesc{&IntDesc}, Ret: &StrDesc},
                        TypeDesc{Kind: TFunc, Params: []*TypeDesc{&IntDesc}, Ret: &IntDesc},
                        false,
                },
                {TypeDesc{Kind: TNamed, Name: "MyType"}, TypeDesc{Kind: TNamed, Name: "MyType"}, true},
                {TypeDesc{Kind: TNamed, Name: "A"}, TypeDesc{Kind: TNamed, Name: "B"}, false},
        }
        for _, tc := range tests {
                got := tc.a.Equals(tc.b)
                if got != tc.want {
                        t.Errorf("%s.Equals(%s) = %v, want %v", tc.a.String(), tc.b.String(), got, tc.want)
                }
        }
}

func TestAssignable(t *testing.T) {
        c := &Checker{}
        tests := []struct {
                dst, src TypeDesc
                want     bool
        }{
                {IntDesc, IntDesc, true},
                {IntDesc, FpDesc, false},
                {FpDesc, IntDesc, true},
                {FpDesc, UintDesc, true},
                {GenDesc, IntDesc, true},
                {IntDesc, GenDesc, true},
                {VoidDesc, VoidDesc, true},
                {BoolDesc, IntDesc, false},
                {IntDesc, UintDesc, true},
                {UintDesc, IntDesc, true},
        }
        for _, tc := range tests {
                got := c.assignable(tc.dst, tc.src)
                if got != tc.want {
                        t.Errorf("assignable(%s, %s) = %v, want %v",
                                tc.dst.String(), tc.src.String(), got, tc.want)
                }
        }
}

// ---------------------------------------------------------------------------
// Anonymous function and higher-order tests
// ---------------------------------------------------------------------------

func TestAnonymousFn(t *testing.T) {
        prog, errs := checkSource(t, "fn apply(f gen, x int)\n    return f(x)\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["apply"].RetType.Kind != TGen {
                t.Errorf("expected gen, got %s", prog.Functions["apply"].RetType.String())
        }
}

func TestRecursiveLocalFn(t *testing.T) {
        _, errs := checkSource(t, "fn outer()\n    let fib = fn fib(n int)\n        if n <= 1\n            return n\n        return fib(n - 1) + fib(n - 2)\n    return fib(10)\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
}

// ---------------------------------------------------------------------------
// Bitwise operation tests
// ---------------------------------------------------------------------------

func TestBitwiseOps(t *testing.T) {
        prog, errs := checkSource(t, "fn bits()\n    let a = 255 & 15\n    let b = 240 | 15\n    let c = 255 ^ 15\n    let d = 1 << 8\n    let e = 256 >> 4\n    return d\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["bits"].RetType.Kind != TInt {
                t.Errorf("expected int, got %s", prog.Functions["bits"].RetType.String())
        }
}

// ---------------------------------------------------------------------------
// Error propagation tests
// ---------------------------------------------------------------------------

func TestErrorPropagation(t *testing.T) {
        _, errs := checkSource(t, "fn may_fail() gen\n    return error(\"something went wrong\")\n\nfn caller()\n    let result = may_fail()?\n    return result\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
}

// ---------------------------------------------------------------------------
// Scoped variable shadowing tests
// ---------------------------------------------------------------------------

func TestScopedShadowing(t *testing.T) {
        _, errs := checkSource(t, "fn scoped()\n    let x = 10\n    if true\n        let x = \"hello\"\n        return x\n    return x\n")
        if len(errs) == 0 {
                t.Fatal("expected error for inconsistent return types (int vs str)")
        }
}

// ---------------------------------------------------------------------------
// String auto-coercion tests
// ---------------------------------------------------------------------------

func TestStringCoercion_StrPlusInt(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    return \"count: \" + 42\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TStr {
                t.Errorf("expected str, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestStringCoercion_IntPlusStr(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    return 42 + \" items\"\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TStr {
                t.Errorf("expected str, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestStringCoercion_StrPlusBool(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    return \"val: \" + true\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TStr {
                t.Errorf("expected str, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestStringCoercion_StrPlusFp(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    return 3.14 + \" is pi\"\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TStr {
                t.Errorf("expected str, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestStringCoercion_StrPlusNil(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    return \"got: \" + nil\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TStr {
                t.Errorf("expected str, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestStringCoercion_MultiConcat(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    let x = 1\n    return \"a\" + x + \"c\"\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TStr {
                t.Errorf("expected str, got %s", prog.Functions["f"].RetType.String())
        }
}

// ---------------------------------------------------------------------------
// Assignment type validation tests
// ---------------------------------------------------------------------------

func TestError_AssignTypeMismatch(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    let mut x int = 10\n    x = \"wrong\"\n")
        if len(errs) == 0 {
                t.Fatal("expected error for assigning str to int variable")
        }
        found := false
        for _, e := range errs {
                if strings.Contains(e, "cannot assign") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected 'cannot assign' error, got: %v", errs)
        }
}

// ---------------------------------------------------------------------------
// Annotated let-binding mismatch tests
// ---------------------------------------------------------------------------

func TestError_AnnotatedLetMismatch(t *testing.T) {
        _, errs := checkSource(t, "fn bad()\n    let x int = \"wrong\"\n")
        if len(errs) == 0 {
                t.Fatal("expected error for int variable initialized with str")
        }
}

// ---------------------------------------------------------------------------
// Return type annotation validation tests
// ---------------------------------------------------------------------------

func TestError_ReturnTypeMismatch(t *testing.T) {
        _, errs := checkSource(t, "fn bad() int\n    return \"not an int\"\n")
        if len(errs) == 0 {
                t.Fatal("expected error for returning str from int function")
        }
        found := false
        for _, e := range errs {
                if strings.Contains(e, "return type") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected 'return type' error, got: %v", errs)
        }
}

// ---------------------------------------------------------------------------
// Numeric promotion tests
// ---------------------------------------------------------------------------

func TestExprType_IntIntArithmetic(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    let a = 10\n    let b = 20\n    return a - b\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TInt {
                t.Errorf("expected int, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestExprType_UintArithmetic(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    let a = 10\n    let b = 20\n    return a & b\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TInt {
                t.Errorf("expected int, got %s", prog.Functions["f"].RetType.String())
        }
}

// ---------------------------------------------------------------------------
// Module access with correct return types
// ---------------------------------------------------------------------------

func TestStdlibModuleTypes(t *testing.T) {
        prog, errs := checkSource(t, "fn test()\n    let x = math.pi\n    let y = sys.platform\n    let z = path.join(\"a\", \"b\")\n    let w = json.parse(\"{}\")\n    return x\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        fn := prog.Functions["test"]
        if fn.RetType.Kind != TFp {
                t.Errorf("expected fp (math.pi), got %s", fn.RetType.String())
        }
}

// ---------------------------------------------------------------------------
// Table type inference from literals
// ---------------------------------------------------------------------------

func TestExprType_TableLiteralInferred(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    let t = {1: \"a\", 2: \"b\"}\n    return t\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TTable {
                t.Errorf("expected table, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestExprType_EmptyTableLiteral(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    let t = {}\n    return t\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TTable {
                t.Errorf("expected table, got %s", prog.Functions["f"].RetType.String())
        }
}

// ---------------------------------------------------------------------------
// Higher-order function tests
// ---------------------------------------------------------------------------

func TestHigherOrder_Apply(t *testing.T) {
        prog, errs := checkSource(t, "fn apply(f gen, x int)\n    return f(x)\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        // f is gen-typed, so return type is gen
        if prog.Functions["apply"].RetType.Kind != TGen {
                t.Errorf("expected gen, got %s", prog.Functions["apply"].RetType.String())
        }
}

func TestHigherOrder_Compose(t *testing.T) {
        _, errs := checkSource(t, "fn use_compose()\n    let f = compose\n    return f\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
}

// ---------------------------------------------------------------------------
// Edge case: deeply nested expressions
// ---------------------------------------------------------------------------

func TestExprType_DeeplyNested(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    return (1 + 2) * (3 + 4)\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TInt {
                t.Errorf("expected int, got %s", prog.Functions["f"].RetType.String())
        }
}

func TestExprType_NegatedComparison(t *testing.T) {
        prog, errs := checkSource(t, "fn f()\n    let a = not (1 < 2)\n    return a\n")
        if len(errs) > 0 {
                t.Fatalf("unexpected errors: %v", errs)
        }
        if prog.Functions["f"].RetType.Kind != TBool {
                t.Errorf("expected bool, got %s", prog.Functions["f"].RetType.String())
        }
}

// ---------------------------------------------------------------------------
// Fuzzy suggestion tests
// ---------------------------------------------------------------------------

func TestLevenshtein(t *testing.T) {
        tests := []struct {
                a, b string
                want int
        }{
                {"", "", 0},
                {"", "abc", 3},
                {"abc", "", 3},
                {"abc", "abc", 0},
                {"abc", "abd", 1},
                {"abc", "abcd", 1},
                {"kitten", "sitting", 3},
                {"print", "pritn", 2},
                {"platform", "platfrom", 2},
                {"sys", "sys", 0},
                {"a", "abcde", 4},
        }
        for _, tc := range tests {
                got := levenshtein(tc.a, tc.b)
                if got != tc.want {
                        t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
                }
        }
}

func TestSuggestSimilar(t *testing.T) {
        candidates := []string{"print", "println", "format", "len", "abs", "sqrt", "platform"}
        tests := []struct {
                input    string
                want     string
                wantDist int
        }{
                {"pritn", "print", 2},
                {"platfrom", "platform", 2},
                {"sqt", "sqrt", 1},
                {"xyz", "", 0},     // too far from everything
                {"", "", 0},         // empty input
                {"aaaaaaaa", "", 0}, // way too far from all candidates
        }
        for _, tc := range tests {
                got, dist := suggestSimilar(tc.input, candidates)
                if got != tc.want {
                        t.Errorf("suggestSimilar(%q, ...) = %q (dist %d), want %q", tc.input, got, dist, tc.want)
                }
        }
}

func TestSuggestSimilar_Empty(t *testing.T) {
        got, dist := suggestSimilar("foo", nil)
        if got != "" {
                t.Errorf("expected empty for nil candidates, got %q", got)
        }
        if dist != 0 {
                t.Errorf("expected dist 0 for nil candidates, got %d", dist)
        }
}

func TestUniqueStrings(t *testing.T) {
        got := uniqueStrings([]string{"a", "b", "a", "c", "b", "a"})
        want := []string{"a", "b", "c"}
        if len(got) != len(want) {
                t.Fatalf("uniqueStrings length: got %d, want %d", len(got), len(want))
        }
        for i := range want {
                if got[i] != want[i] {
                        t.Errorf("uniqueStrings[%d] = %q, want %q", i, got[i], want[i])
                }
        }
}

func TestFuzzySuggestion_UndefinedIdent(t *testing.T) {
        // "pritn" should suggest "print"
        _, errs := checkSource(t, "fn bad()\n    pritn(\"hello\")\n")
        if len(errs) == 0 {
                t.Fatal("expected error for undefined identifier 'pritn'")
        }
        found := false
        for _, e := range errs {
                if strings.Contains(e, "pritn") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected 'pritn' in error, got: %v", errs)
        }
}

func TestFuzzySuggestion_ModuleSymbol(t *testing.T) {
        // "platfrom" should trigger an error about unknown module symbol
        _, errs := checkSource(t, "fn bad()\n    let x = sys.platfrom\n    return x\n")
        if len(errs) == 0 {
                t.Fatal("expected error for unknown module symbol 'platfrom'")
        }
        found := false
        for _, e := range errs {
                if strings.Contains(e, "platfrom") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected 'platfrom' in error, got: %v", errs)
        }
}

func TestMatchDuplicate_Int(t *testing.T) {
        _, errs := checkSource(t, `fn main()
    match 1
        case 1
            print("a")
        case 2
            print("b")
        case 1
            print("c")
`)
        found := false
        for _, e := range errs {
                if strings.Contains(e, "duplicate match case") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected duplicate match case error, got: %v", errs)
        }
}

func TestMatchDuplicate_String(t *testing.T) {
        _, errs := checkSource(t, `fn main()
    let x = "hello"
    match x
        case "hello"
            print("a")
        case "world"
            print("b")
        case "hello"
            print("c")
`)
        found := false
        for _, e := range errs {
                if strings.Contains(e, "duplicate match case") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected duplicate match case error, got: %v", errs)
        }
}

func TestMatchDuplicate_Bool(t *testing.T) {
        _, errs := checkSource(t, `fn main()
    match true
        case true
            print("a")
        case false
            print("b")
        case true
            print("c")
`)
        found := false
        for _, e := range errs {
                if strings.Contains(e, "duplicate match case") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected duplicate match case error, got: %v", errs)
        }
}

func TestMatchNoDuplicate_DistinctValues(t *testing.T) {
        _, errs := checkSource(t, `fn main()
    match 42
        case 1
            print("a")
        case 2
            print("b")
        case 3
            print("c")
        default
            print("d")
`)
        for _, e := range errs {
                if strings.Contains(e, "duplicate match case") {
                        t.Errorf("unexpected duplicate error: %s", e)
                }
        }
}

func TestMatchNoDuplicate_CrossType(t *testing.T) {
        // The integer 1 and the string "1" should NOT be considered duplicates.
        _, errs := checkSource(t, `fn main()
    let x = 1
    match x
        case 1
            print("int")
        case "1"
            print("str")
`)
        for _, e := range errs {
                if strings.Contains(e, "duplicate match case") {
                        t.Errorf("cross-type values should not be duplicates: %s", e)
                }
        }
}

func TestMatchDuplicate_Nil(t *testing.T) {
        _, errs := checkSource(t, `fn main()
    match nil
        case nil
            print("a")
        case nil
            print("b")
`)
        found := false
        for _, e := range errs {
                if strings.Contains(e, "duplicate match case") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected duplicate match case error for nil, got: %v", errs)
        }
}

func TestMatchDuplicate_NegInt(t *testing.T) {
        // -1 appearing twice should be detected as duplicate.
        _, errs := checkSource(t, `fn main()
    match 0
        case -1
            print("a")
        case 0
            print("b")
        case -1
            print("c")
`)
        found := false
        for _, e := range errs {
                if strings.Contains(e, "duplicate match case") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected duplicate match case error for -1, got: %v", errs)
        }
}

func TestPubStructExported(t *testing.T) {
        // pub struct should be registered in the global scope.
        src := `pub struct Point
    x int
    y int

pub fn make_point(a, b)
    return Point{x: a, y: b}

fn main()
    let p = make_point(1, 2)
    print(p.x)
`
        prog, errs := checkSource(t, src)
        if len(errs) > 0 {
                t.Errorf("unexpected errors: %v", errs)
        }
        // Verify the struct is registered as a function (method) target.
        if _, ok := prog.Functions["make_point"]; !ok {
                t.Fatal("function 'make_point' not found")
        }
}

// ---------------------------------------------------------------------------
// Multi-file import tests
// ---------------------------------------------------------------------------

func TestMultiFile_LocalImport(t *testing.T) {
        files := map[string]string{
                "main.yilt": `use utils

pub fn main()
    let r = utils.add(3, 4)
    print(r)
`,
                "utils.yilt": `pub fn add(a int, b int) int
    return a + b
`,
        }
        _, errs := checkMultiSource(t, files)
        if len(errs) > 0 {
                t.Errorf("multi-file import failed: %v", errs)
        }
}

func TestMultiFile_SelectiveImport(t *testing.T) {
        files := map[string]string{
                "main.yilt": `from utils use add, greet

pub fn main()
    let r = add(1, 2)
    let s = greet("Alice")
    print(r)
    print(s)
`,
                "utils.yilt": `pub fn add(a int, b int) int
    return a + b

pub fn greet(name str) str
    return "Hi " + name
`,
        }
        _, errs := checkMultiSource(t, files)
        if len(errs) > 0 {
                t.Errorf("selective import failed: %v", errs)
        }
}

func TestMultiFile_UnknownModule(t *testing.T) {
        files := map[string]string{
                "main.yilt": `use nonexistent

pub fn main()
    print(0)
`,
        }
        _, errs := checkMultiSource(t, files)
        if len(errs) == 0 {
                t.Error("expected error for unknown local module")
        }
}

func TestMultiFile_UnknownSymbol(t *testing.T) {
        files := map[string]string{
                "main.yilt": `from utils use missing_fn

pub fn main()
    print(0)
`,
                "utils.yilt": `pub fn add(a int, b int) int
    return a + b
`,
        }
        _, errs := checkMultiSource(t, files)
        if len(errs) == 0 {
                t.Error("expected error for unknown symbol in selective import")
        }
}

func TestMultiFile_PubStructImport(t *testing.T) {
        files := map[string]string{
                "main.yilt": `use shapes

pub fn main()
    let p = shapes.Point{x: 1, y: 2}
    print(p.x)
`,
                "shapes.yilt": `pub struct Point
    x int
    y int
`,
        }
        _, errs := checkMultiSource(t, files)
        if len(errs) > 0 {
                t.Errorf("pub struct import failed: %v", errs)
        }
}

func TestMultiFile_TransitiveImport(t *testing.T) {
        files := map[string]string{
                "main.yilt": `use utils

pub fn main()
    let r = utils.double(5)
    print(r)
`,
                "utils.yilt": `use math

pub fn double(x int) int
    return math.mul(x, 2)
`,
                "math.yilt": `pub fn mul(a int, b int) int
    return a * b
`,
        }
        _, errs := checkMultiSource(t, files)
        if len(errs) > 0 {
                t.Errorf("transitive import failed: %v", errs)
        }
}
