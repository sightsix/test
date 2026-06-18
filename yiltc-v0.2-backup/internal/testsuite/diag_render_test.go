package testsuite

import (
        "io"
        "os"
        "path/filepath"
        "strings"
        "testing"

        "github.com/yilt/yiltc/internal/diag"
        "github.com/yilt/yiltc/internal/lex"
        "github.com/yilt/yiltc/internal/parse"
)

// TestNoArrowDiagnostics verifies that the no-arrow rule produces a clear,
// well-formatted diagnostic with:
//   - the word "arrow" in the message (so users can search for the rule)
//   - a "help:" suggestion that mentions the bare form `fn foo() int`
//   - the underline span length covering exactly the 2 characters of `->`
//   - source context showing the offending line
//
// This test exists because nice diagnostics are first-class in Yilt — the
// user should never have to guess what fix the compiler wants.
func TestNoArrowDiagnostics(t *testing.T) {
        cases := []struct {
                name string
                src  string
                // expectedSubstrings are lowercased substrings that must all appear
                // somewhere in the rendered diagnostic output.
                expectedSubstrings []string
        }{
                {
                        name: "top_level_fn",
                        src:  "fn add(a int, b int) -> int\n    return a + b\n",
                        expectedSubstrings: []string{
                                "arrow",
                                "->",
                                "help",
                                "fn foo() int",
                        },
                },
                {
                        name: "tuple_return",
                        src:  "fn pair() -> (int, str)\n    return (1, \"hi\")\n",
                        expectedSubstrings: []string{
                                "arrow",
                                "help",
                        },
                },
                {
                        name: "closure_expr",
                        src:  "fn main()\n    let f = fn(x int) -> int\n        return x + 1\n",
                        expectedSubstrings: []string{
                                "arrow",
                                "help",
                        },
                },
                {
                        name: "local_named_fn",
                        src:  "fn main()\n    let fn helper(x int) -> int\n        return x * 2\n",
                        expectedSubstrings: []string{
                                "arrow",
                                "help",
                        },
                },
        }

        for _, tc := range cases {
                t.Run(tc.name, func(t *testing.T) {
                        path := "/test/arrow_" + tc.name + ".yilt"
                        src := []byte(tc.src)
                        dh := diag.NewHandler(false) // no color for stable matching
                        dh.AddSource(path, src)

                        lexer := lex.New(path, src)
                        lexer.SetDiag(dh)
                        tokens, indents := lexer.Tokenize()
                        if dh.HasErrors() {
                                t.Fatalf("lex failed: %v", dh.ErrorMessages())
                        }

                        parser := parse.New(path, tokens, indents, src)
                        parser.ParseFile()
                        parseErrs := parser.Errors()
                        if len(parseErrs) == 0 {
                                t.Fatalf("expected at least one parse error, got none")
                        }

                        // Pipe through the diag handler as cmd/yiltc does.
                        for _, pe := range parseErrs {
                                if pe.Help != "" {
                                        if pe.SpanLen > 1 {
                                                dh.ErrorfSpan(pe.File, pe.Line, pe.Col, pe.Offset, pe.SpanLen, "%s", pe.Msg)
                                        } else {
                                                dh.Errorf(pe.File, pe.Line, pe.Col, pe.Offset, "%s", pe.Msg)
                                        }
                                        dh.Help(pe.Help)
                                } else if pe.SpanLen > 1 {
                                        dh.ErrorSpan(pe.File, pe.Line, pe.Col, pe.Offset, pe.SpanLen, pe.Msg)
                                } else {
                                        dh.Error(pe.File, pe.Line, pe.Col, pe.Offset, pe.Msg)
                                }
                        }

                        if !dh.HasErrors() {
                                t.Fatalf("diag handler should have errors after pipe-through")
                        }

                        // Render to a buffer and check substrings.
                        rendered := captureRender(dh)
                        lower := strings.ToLower(rendered)
                        for _, want := range tc.expectedSubstrings {
                                if !strings.Contains(lower, strings.ToLower(want)) {
                                        t.Errorf("missing substring %q in rendered diagnostic:\n%s", want, rendered)
                                }
                        }

                        // Check that at least one error has a help attached.
                        hasHelp := strings.Contains(lower, "= help:")
                        if !hasHelp {
                                t.Errorf("expected a '= help:' line in rendered diagnostic:\n%s", rendered)
                        }

                        // Check that the source line is shown (1 | fn add...).
                        hasSourceLine := strings.Contains(rendered, " | ")
                        if !hasSourceLine {
                                t.Errorf("expected a source context line with ' | ' in:\n%s", rendered)
                        }

                        t.Logf("rendered diagnostic:\n%s", rendered)
                })
        }
}

// TestNoArrowSpanLength verifies that the underline span for the no-arrow
// error covers exactly the 2 characters `->`, not just one.
func TestNoArrowSpanLength(t *testing.T) {
        path := "/test/arrow_span.yilt"
        src := []byte("fn add(a int, b int) -> int\n    return a + b\n")
        dh := diag.NewHandler(false)
        dh.AddSource(path, src)

        lexer := lex.New(path, src)
        lexer.SetDiag(dh)
        tokens, indents := lexer.Tokenize()

        parser := parse.New(path, tokens, indents, src)
        parser.ParseFile()
        parseErrs := parser.Errors()

        // Find the arrow-related error.
        var arrowErr *parse.ParseError
        for i := range parseErrs {
                if strings.Contains(parseErrs[i].Msg, "arrow") {
                        arrowErr = &parseErrs[i]
                        break
                }
        }
        if arrowErr == nil {
                t.Fatalf("no 'arrow' parse error found; got: %v", parseErrs)
        }

        if arrowErr.SpanLen != 2 {
                t.Errorf("arrow error SpanLen = %d, want 2 (the '->' token is 2 chars)", arrowErr.SpanLen)
        }

        // Sanity check: the arrow token's source position should point at '-'.
        srcLine := strings.SplitN(string(src), "\n", 2)[0]
        if arrowErr.Col-1 >= len(srcLine) {
                t.Fatalf("arrow col %d out of range of source line %q", arrowErr.Col, srcLine)
        }
        atCol := string(srcLine[arrowErr.Col-1])
        if atCol != "-" {
                t.Errorf("arrow error col points at %q, want '-'", atCol)
        }
}

// TestNoArrowBareFormWorks verifies that the same source compiles cleanly
// once the arrow is removed.  This is the positive counterpart to the
// no-arrow rejection tests above.
func TestNoArrowBareFormWorks(t *testing.T) {
        cases := []struct {
                name string
                src  string
        }{
                {
                        name: "top_level_fn",
                        src:  "fn add(a int, b int) int\n    return a + b\n",
                },
                {
                        name: "tuple_return",
                        src:  "fn pair() (int, str)\n    return (1, \"hi\")\n",
                },
                {
                        name: "void_return",
                        src:  "fn greet()\n    print(\"hi\")\n",
                },
        }

        for _, tc := range cases {
                t.Run(tc.name, func(t *testing.T) {
                        path := filepath.Join(t.TempDir(), "test.yilt")
                        src := []byte(tc.src)
                        dh := diag.NewHandler(false)
                        dh.AddSource(path, src)

                        lexer := lex.New(path, src)
                        lexer.SetDiag(dh)
                        tokens, indents := lexer.Tokenize()

                        parser := parse.New(path, tokens, indents, src)
                        parser.ParseFile()
                        parseErrs := parser.Errors()
                        if len(parseErrs) != 0 {
                                t.Errorf("expected 0 parse errors for bare-form %q, got %d: %v",
                                        tc.name, len(parseErrs), parseErrs)
                                for _, e := range parseErrs {
                                        t.Logf("  err: %s", e)
                                }
                        }
                })
        }
}

// captureRender captures the stderr output of dh.Render() into a string.
// We do this by swapping os.Stderr to a pipe during the call.
func captureRender(dh *diag.DiagnosticHandler) string {
        // Save stderr, redirect to pipe, restore after.
        // diag.Render writes to os.Stderr directly, so we need to swap it.
        //
        // Implementation note: We use a goroutine to read from the pipe so the
        // write side never blocks.
        r, w, err := osPipe()
        if err != nil {
                // Fallback: just return a stub if pipe creation fails.
                return "<pipe-failure>"
        }
        orig := swapStderr(w)
        done := make(chan string, 1)
        go func() {
                buf := make([]byte, 0, 4096)
                tmp := make([]byte, 4096)
                for {
                        n, err := r.Read(tmp)
                        if n > 0 {
                                buf = append(buf, tmp[:n]...)
                        }
                        if err != nil {
                                break
                        }
                }
                done <- string(buf)
        }()

        dh.Render()
        closeStderr(w)
        swapStderr(orig)
        return <-done
}

// osPipe, swapStderr, and closeStderr are factored out so they can be
// replaced on non-Unix platforms if needed.  On Linux/macOS they map to
// the standard library os.Pipe and direct field assignment.
//
// NOTE: We can't use os.Stderr = ... directly because diag.Render() calls
// fmt.Fprint(os.Stderr, ...) which reads the variable at call time, so
// swapping it does work.  But we still need a real pipe underneath.

// osPipe creates an in-memory pipe pair (reader, writer).
func osPipe() (*os.File, *os.File, error) {
        return os.Pipe()
}

// swapStderr replaces os.Stderr with w and returns the previous value.
// Callers must restore it via another swapStderr call.
func swapStderr(w *os.File) *os.File {
        orig := os.Stderr
        os.Stderr = w
        return orig
}

// closeStderr closes the writer end of the pipe so the reader goroutine
// sees EOF.
func closeStderr(w *os.File) {
        w.Close()
}

// Ensure io is used so the import is not flagged as unused (we use it for
// future io.Copy convenience if needed).
var _ = io.Copy
