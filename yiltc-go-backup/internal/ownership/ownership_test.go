package ownership

import (
        "testing"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/check"
        "github.com/yilt/yiltc/internal/diag"
        "github.com/yilt/yiltc/internal/lex"
        "github.com/yilt/yiltc/internal/parse"
)

// TestBasicOwnership verifies use-after-move detection.
func TestBasicOwnership(t *testing.T) {
        // Build a minimal checked program with a use-after-move.
        src := `
fn main()
        let a = {"k": 1}
        let b = a
        print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move error, got errors: %v", errs)
        }
}

// TestPrimitiveNoMove verifies that primitive assignments are NOT moves.
func TestPrimitiveNoMove(t *testing.T) {
        src := `
fn main()
        let a = 42
        let b = a
        print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        for _, e := range errs {
                if contains(e, "moved") {
                        t.Errorf("primitive should not trigger move, got: %s", e)
                }
        }
}

// TestStringOwnership verifies string move semantics.
func TestStringOwnership(t *testing.T) {
        src := `
fn main()
        let a = "hello"
        let b = a
        print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move error for string, got: %v", errs)
        }
}

// TestDoubleMove verifies that moving an already-moved value emits an error.
func TestDoubleMove(t *testing.T) {
        src := `
fn main()
        let a = {}
        let b = a
        let c = a
        print(b)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "already moved") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected double-move error, got errors: %v", errs)
        }
}

// TestFnValueOwnership verifies that function values participate in move semantics.
func TestFnValueOwnership(t *testing.T) {
        src := `
fn main()
        let a = fn()
                return 42
        let b = a
        print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move error for function value, got errors: %v", errs)
        }
}

// TestFnValueNoMove verifies that calling a function value does NOT move it.
func TestFnValueNoMove(t *testing.T) {
        src := `
fn main()
        let f = fn()
                return 42
        let x = f()
        let y = f()
        print(x)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        for _, e := range errs {
                if contains(e, "moved") {
                        t.Errorf("calling a function value should not move it, got: %s", e)
                }
        }
}

// TestClosureCaptureBorrow verifies that closures borrow captured variables
// (the captured variable is not moved into the closure).
func TestClosureCaptureBorrow(t *testing.T) {
        src := `
fn main()
        let msg = "hello"
        let f = fn()
                return msg
        print(msg)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        // msg should NOT be moved — closures borrow captured variables.
        errs := dh.ErrorMessages()
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "msg") {
                        t.Errorf("closure capture should borrow, not move, got: %s", e)
                }
        }
}

// TestConsumeAfterMove verifies that returning a moved value is caught.
func TestConsumeAfterMove(t *testing.T) {
        src := `
fn main()
        let a = {}
        let b = a
        return a
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        // Should get at least one use-after-move error
        found := false
        for _, e := range errs {
                if contains(e, "moved") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move error for return of moved value, got errors: %v", errs)
        }
}

// TestNoUseAfterAssign verifies re-assigning a mutable var is OK.
func TestNoUseAfterAssign(t *testing.T) {
        src := `
fn main()
        let mut a = "hello"
        let b = a
        a = "world"
        print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        // The "let b = a" moves a. Then "a = world" reassigns it, making a alive again.
        // So print(a) should be OK — no use-after-move error expected.
        errs := dh.ErrorMessages()
        for _, e := range errs {
                if contains(e, "moved") {
                        t.Errorf("reassignment should clear move state, got: %s", e)
                }
        }
}

// typeCheck is a helper that runs the full lex → parse → check pipeline.
func typeCheck(t *testing.T, filename, src string, dh *diag.DiagnosticHandler) *check.CheckedProgram {
        t.Helper()

        absPath := "/fake/" + filename
        rawSrc := []byte(src)
        dh.AddSource(absPath, rawSrc)

        lexer := lex.New(absPath, rawSrc)
        lexer.SetDiag(dh)
        tokens, indents := lexer.Tokenize()
        if dh.HasErrors() {
                t.Logf("lex errors: %v", dh.ErrorMessages())
                return nil
        }

        parser := parse.New(absPath, tokens, indents, rawSrc)
        file := parser.ParseFile()
        for _, pe := range parser.Errors() {
                dh.Error(pe.File, pe.Line, pe.Col, pe.Offset, pe.Msg)
        }
        if dh.HasErrors() {
                t.Logf("parse errors: %v", dh.ErrorMessages())
                return nil
        }

        program := &ast.Program{
                Files: []*ast.File{file},
                Root:  "/fake",
        }
        checker := check.NewChecker(program, dh)
        checked, _ := checker.Check()
        if dh.HasErrors() {
                t.Logf("check errors: %v", dh.ErrorMessages())
                return nil
        }
        return checked
}

// TestIfBranchMoveMerge verifies that a value moved in one if-branch
// is conservatively marked as possibly-moved after the if/else.
func TestIfBranchMoveMerge(t *testing.T) {
        // Move happens in the "if" branch but not the "else" branch.
        // After merge, 'a' should be considered moved.
        src := `
fn main()
    let a = {"k": 1}
    let x = 1
    if x == 1
        let b = a
        print(b)
    else
        print(a)
    print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "a") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move after if/else merge, got errors: %v", errs)
        }
}

// TestIfElseBothMove verifies that if both branches move a variable,
// using it after is still an error.
func TestIfElseBothMove(t *testing.T) {
        src := `
fn main()
    let a = "hello"
    let x = 1
    if x == 1
        let b = a
    else
        let c = a
    print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "a") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move when both branches move, got errors: %v", errs)
        }
}

// TestIfNoElseMoveInBranch verifies that moving in a branch without else
// conservatively marks the variable as moved after the if.
func TestIfNoElseMoveInBranch(t *testing.T) {
        src := `
fn main()
    let a = {}
    let x = 1
    if x == 1
        let b = a
    print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "a") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move after if-only branch with move, got errors: %v", errs)
        }
}

// TestMatchArmMoveMerge verifies that a value moved in one match arm
// is conservatively marked as moved after the match.
func TestMatchArmMoveMerge(t *testing.T) {
        src := `
fn main()
    let a = {"k": 1}
    let x = 1
    match x
        case 1
            let b = a
        case 2
            print(a)
        default
            print(a)
    print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "a") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move after match merge, got errors: %v", errs)
        }
}

// TestWhileLoopNoLeak verifies that a variable moved inside a while loop
// body is restored on re-entry (the loop body runs multiple times).
func TestWhileLoopNoLeak(t *testing.T) {
        // The table is created once. Inside the loop, it's moved to 'b'.
        // On the next iteration, 'a' should still be valid because the
        // loop body scope is popped and the pre-loop state is restored.
        // After the loop, 'a' might be alive (loop may not have executed).
        src := `
fn main()
    let a = {}
    let mut i = 0
    while i < 1
        let b = a
        i = i + 1
    print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        // After the while loop merge (pre-loop vs post-body), 'a' was moved
        // in the body but alive before the loop. The conservative merge should
        // flag the final print(a) as use-after-move.
        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "a") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move after while loop merge, got errors: %v", errs)
        }
}

// TestForLoopVariableFresh verifies that the loop variable in a for-loop
// is fresh each iteration (re-defined as alive).
func TestForLoopVariableFresh(t *testing.T) {
        src := `
fn main()
    let t = {"a": 1, "b": 2}
    for k in t
        let x = k
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        // No errors expected — loop variable is fresh each iteration.
        errs := dh.ErrorMessages()
        for _, e := range errs {
                if contains(e, "moved") {
                        t.Errorf("for-loop variable should be fresh each iteration, got: %s", e)
                }
        }
}

// TestNestedClosureBorrow verifies deep nesting of closures that borrow variables.
func TestNestedClosureBorrow(t *testing.T) {
        src := `
fn main()
    let data = "outer"
    let f = fn()
        let g = fn()
            return data
        return g
    print(data)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        // data should NOT be moved — nested closures borrow.
        errs := dh.ErrorMessages()
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "data") {
                        t.Errorf("nested closure should borrow, not move, got: %s", e)
                }
        }
}

// TestConsumeInBranch verifies that consuming a value via return in one
// branch makes it possibly-consumed after the if/else.
func TestConsumeInBranch(t *testing.T) {
        src := `
fn main()
    let a = "hello"
    let x = 1
    if x == 1
        return a
    else
        print(a)
    print(a)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        // 'a' is consumed (returned) in the if branch but alive in the else branch.
        // After merge, it should be conservatively marked as consumed/moved.
        errs := dh.ErrorMessages()
        found := false
        for _, e := range errs {
                if (contains(e, "moved") || contains(e, "consumed")) && contains(e, "a") {
                        found = true
                        break
                }
        }
        if !found {
                t.Errorf("expected use-after-move/consume after branch with return, got errors: %v", errs)
        }
}

// TestIndexAssignNoMove verifies that table index assignment does not
// move the table variable.
func TestIndexAssignNoMove(t *testing.T) {
        src := `
fn main()
    let mut t = {}
    t["key"] = "value"
    print(t)
`

        dh := diag.NewHandler(false)
        cp := typeCheck(t, "test.yilt", src, dh)
        if cp == nil {
                t.Fatal("type check failed")
        }

        analyzer := NewAnalyzer(cp, dh)
        analyzer.Analyze()

        // Index assignment is a mutation, not a move. The table should remain alive.
        errs := dh.ErrorMessages()
        for _, e := range errs {
                if contains(e, "moved") && contains(e, "t") {
                        t.Errorf("index assignment should not move the table, got: %s", e)
                }
        }
}

func contains(s, substr string) bool {
        return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
        for i := 0; i <= len(s)-len(sub); i++ {
                if s[i:i+len(sub)] == sub {
                        return true
                }
        }
        return false
}
