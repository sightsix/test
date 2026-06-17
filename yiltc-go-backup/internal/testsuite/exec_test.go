package testsuite

import (
        "bytes"
        "os"
        "os/exec"
        "path/filepath"
        "strings"
        "testing"

        "github.com/yilt/yiltc/internal/diag"
        "github.com/yilt/yiltc/internal/lex"
        "github.com/yilt/yiltc/internal/parse"
)

// runResult holds the result of compiling and running a Yilt program.
type runResult struct {
        compiled    bool
        ran         bool
        exitCode    int
        stdout      string
        stderr      string
        compileErr  string
}

// compileAndRun compiles a Yilt source string to a temporary binary,
// runs it, and returns the captured output.
func compileAndRun(t *testing.T, src string) runResult {
        t.Helper()

        // Find the yiltc binary: look in <project_root>/bin/yiltc.
        // We use the same root-finding logic as the file-based tests.
        projectRoot := findTestsuiteRoot()
        yiltcBin := filepath.Join(projectRoot, "..", "bin", "yiltc")

        // Check if binary exists
        if _, err := os.Stat(yiltcBin); err != nil {
                // Fallback: try to build it via go build
                absRoot, _ := filepath.Abs(projectRoot)
                buildRoot := filepath.Join(absRoot, "..")
                goCmd := exec.Command("go", "build", "-o", filepath.Join(buildRoot, "bin", "yiltc"), "./cmd/yiltc")
                goCmd.Dir = buildRoot
                if out, err := goCmd.CombinedOutput(); err != nil {
                        return runResult{compileErr: "build failed: " + string(out) + ": " + err.Error()}
                }
        }

        // Write source to temp file
        tmpDir := t.TempDir()
        srcFile := filepath.Join(tmpDir, "test.yilt")
        if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
                return runResult{compileErr: "write failed: " + err.Error()}
        }

        // Verify the source compiles (lex + parse + check)
        dh := diag.NewHandler(false)
        absSrc, _ := filepath.Abs(srcFile)
        rawSrc, _ := os.ReadFile(srcFile)
        dh.AddSource(absSrc, rawSrc)

        lexer := lex.New(absSrc, rawSrc)
        lexer.SetDiag(dh)
        tokens, indents := lexer.Tokenize()
        if dh.HasErrors() {
                return runResult{compileErr: "lex errors: " + strings.Join(dh.ErrorMessages(), "; ")}
        }

        parser := parse.New(absSrc, tokens, indents, rawSrc)
        parser.ParseFile()
        if dh.HasErrors() {
                return runResult{compileErr: "parse errors: " + strings.Join(dh.ErrorMessages(), "; ")}
        }

        // Compile to binary
        binPath := filepath.Join(tmpDir, "test_out")
        cmd := exec.Command(yiltcBin, srcFile, "-o", binPath)
        var stderr bytes.Buffer
        cmd.Stderr = &stderr
        if err := cmd.Run(); err != nil {
                return runResult{compileErr: "compile: " + stderr.String() + ": " + err.Error()}
        }

        // Run the binary
        cmd = exec.Command(binPath)
        var stdout, stderrOut bytes.Buffer
        cmd.Stdout = &stdout
        cmd.Stderr = &stderrOut
        err := cmd.Run()
        result := runResult{
                compiled: true,
                stdout:    strings.TrimRight(stdout.String(), "\n"),
                stderr:    stderrOut.String(),
        }
        if err != nil {
                result.ran = true
                if exitErr, ok := err.(*exec.ExitError); ok {
                        result.exitCode = exitErr.ExitCode()
                }
        } else {
                result.ran = true
        }
        return result
}

// TestClosureBasicExec verifies that basic non-capturing closures work correctly
// by compiling and running the program.
func TestClosureBasicExec(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping execution test in short mode")
        }

        src := `fn main()
    let add = fn(x, y)
        return x + y
    print(add(3, 4))
    print(add(0, 0))
    print(add(-1, 1))

    let double = fn(x)
        return x * 2
    print(double(21))

    let always42 = fn(x, y, z)
        return 42
    print(always42(1, 2, 3))
`
        r := compileAndRun(t, src)
        if r.compileErr != "" {
                t.Fatalf("compile error: %s", r.compileErr)
        }
        if !r.ran {
                t.Fatal("program did not run")
        }
        if r.exitCode != 0 {
                t.Fatalf("exit code %d (stderr: %s)", r.exitCode, r.stderr)
        }

        expected := "7004242"
        if r.stdout != expected {
                t.Errorf("wrong output\n  got:  %q\n  want: %q", r.stdout, expected)
        }
}

// TestClosureCaptureExec verifies that capturing closures work correctly.
func TestClosureCaptureExec(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping execution test in short mode")
        }

        src := `fn main()
    fn make_adder(n)
        fn adder(x)
            return x + n
        return adder

    let add5 = make_adder(5)
    print(add5(10))
    print(add5(20))

    let add100 = make_adder(100)
    print(add100(-3))
`
        r := compileAndRun(t, src)
        if r.compileErr != "" {
                t.Fatalf("compile error: %s", r.compileErr)
        }
        if !r.ran {
                t.Fatal("program did not run")
        }
        if r.exitCode != 0 {
                t.Fatalf("exit code %d (stderr: %s)", r.exitCode, r.stderr)
        }

        expected := "152597"
        if r.stdout != expected {
                t.Errorf("wrong output\n  got:  %q\n  want: %q", r.stdout, expected)
        }
}

// TestClosureImmediateCall verifies that closures called immediately work.
func TestClosureImmediateCall(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping execution test in short mode")
        }

        src := `fn main()
    let a = (fn(x)
        return x + 1
    )(10)
    print(a)
    let b = (fn(x, y)
        return x * y
    )(3, 7)
    print(b)
`
        r := compileAndRun(t, src)
        if r.compileErr != "" {
                t.Fatalf("compile error: %s", r.compileErr)
        }
        if !r.ran {
                t.Fatal("program did not run")
        }
        if r.exitCode != 0 {
                t.Fatalf("exit code %d (stderr: %s)", r.exitCode, r.stderr)
        }

        expected := "1121"
        if r.stdout != expected {
                t.Errorf("wrong output\n  got:  %q\n  want: %q", r.stdout, expected)
        }
}

// TestClosureHigherOrderExec verifies that closures passed as arguments work.
func TestClosureHigherOrderExec(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping execution test in short mode")
        }

        src := `fn main()
    fn apply(f, x)
        return f(x)

    fn double(x)
        return x * 2

    fn inc(x)
        return x + 1

    print(apply(double, 5))
    print(apply(inc, 5))
    print(apply(inc, apply(double, 5)))
`
        r := compileAndRun(t, src)
        if r.compileErr != "" {
                t.Fatalf("compile error: %s", r.compileErr)
        }
        if !r.ran {
                t.Fatal("program did not run")
        }
        if r.exitCode != 0 {
                t.Fatalf("exit code %d (stderr: %s)", r.exitCode, r.stderr)
        }

        expected := "10611"
        if r.stdout != expected {
                t.Errorf("wrong output\n  got:  %q\n  want: %q", r.stdout, expected)
        }
}

// TestRangeForExec verifies that for i in 0..N range loops work correctly.
func TestRangeForExec(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping execution test in short mode")
        }

        src := `fn main()
    // Basic range: 0..5 → 0,1,2,3,4
    for i in 0..5
        print(i)
    print("x")

    // Sum 0..10 = 45
    let mut sum = 0
    for i in 0..10
        sum = sum + i
    print(sum)

    // Non-zero start: 3..6 → 3,4,5
    for i in 3..6
        print(i)
    print("x")

    // Empty range: 5..5 → nothing
    for i in 5..5
        print(999)
    print("empty")

    // Single iteration: 7..8 → 7
    for i in 7..8
        print(i)
    print("x")
`
        r := compileAndRun(t, src)
        if r.compileErr != "" {
                t.Fatalf("compile error: %s", r.compileErr)
        }
        if !r.ran {
                t.Fatal("program did not run")
        }
        if r.exitCode != 0 {
                t.Fatalf("exit code %d (stderr: %s)", r.exitCode, r.stderr)
        }

        expected := "01234x45345xempty7x"
        if r.stdout != expected {
                t.Errorf("wrong output\n  got:  %q\n  want: %q", r.stdout, expected)
        }
}

// TestRangeForBreakContinue verifies break/continue in range-for loops.
func TestRangeForBreakContinue(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping execution test in short mode")
        }

        src := `fn main()
    // break at 5 → 0,1,2,3,4
    for i in 0..100
        if i == 5
            break
        print(i)
    print("b")

    // skip 3 → 0,1,2,4,5
    for i in 0..6
        if i == 3
            continue
        print(i)
    print("c")
`
        r := compileAndRun(t, src)
        if r.compileErr != "" {
                t.Fatalf("compile error: %s", r.compileErr)
        }
        if !r.ran {
                t.Fatal("program did not run")
        }
        if r.exitCode != 0 {
                t.Fatalf("exit code %d (stderr: %s)", r.exitCode, r.stderr)
        }

        expected := "01234b01245c"
        if r.stdout != expected {
                t.Errorf("wrong output\n  got:  %q\n  want: %q", r.stdout, expected)
        }
}

// TestRangeForNested verifies nested range-for loops.
func TestRangeForNested(t *testing.T) {
        if testing.Short() {
                t.Skip("skipping execution test in short mode")
        }

        src := `fn main()
    // 2x3 grid: (0,0)(0,1)(1,0)(1,1)(2,0)(2,1)
    for i in 0..3
        for j in 0..2
            print(i)
            print(j)
    print("x")

    // Sum of products
    let mut total = 0
    for i in 0..4
        for j in 0..4
            total = total + i * j
    print(total)
`
        r := compileAndRun(t, src)
        if r.compileErr != "" {
                t.Fatalf("compile error: %s", r.compileErr)
        }
        if !r.ran {
                t.Fatal("program did not run")
        }
        if r.exitCode != 0 {
                t.Fatalf("exit code %d (stderr: %s)", r.exitCode, r.stderr)
        }

        // (0,0)(0,1)(1,0)(1,1)(2,0)(2,1) = "000110112021"
        // Sum i*j for 0..4,0..4 = 0+0+0+0+0+0+1+2+3+0+2+4+6+0+3+6+9 = 36
        expected := "000110112021x36"
        if r.stdout != expected {
                t.Errorf("wrong output\n  got:  %q\n  want: %q", r.stdout, expected)
        }
}
