package testsuite

import (
        "path/filepath"
        "testing"
)

// TestPositivePrograms verifies that all valid Yilt programs in the
// testsuite directories compile successfully through the full
// lex -> parse -> check pipeline.
func TestPositivePrograms(t *testing.T) {
        root := findTestsuiteRoot()

        subdirs := []string{
                "basic",
                "functions",
                "types",
                "advanced",
                "stdlib",
        }

        for _, sub := range subdirs {
                dir := filepath.Join(root, sub)
                files := walkYiltFiles(dir)

                for _, file := range files {
                        name := filepath.Base(file)
                        t.Run(sub+"/"+name, func(t *testing.T) {
                                r := compileFile(t, file)

                                if !r.lexOK {
                                        t.Errorf("LEX FAILED: %d tokens, %d errors", r.tokenCnt, r.errCount)
                                }
                                if !r.parseOK {
                                        t.Errorf("PARSE FAILED: %d declarations", r.declCnt)
                                }
                                if !r.checkOK {
                                        t.Errorf("CHECK FAILED: %d functions, %d errors", r.fnCnt, r.errCount)
                                        for _, e := range r.errMsgs {
                                                t.Logf("  err: %s", e)
                                        }
                                }

                                // Summary info (always useful, even on success)
                                t.Logf("tokens=%d decls=%d fns=%d", r.tokenCnt, r.declCnt, r.fnCnt)
                        })
                }
        }
}

// TestNegativePrograms verifies that invalid Yilt programs in the
// testsuite/negative directory produce at least one error during
// compilation.
func TestNegativePrograms(t *testing.T) {
        root := findTestsuiteRoot()
        negDir := filepath.Join(root, "negative")
        files := walkYiltFiles(negDir)

        for _, file := range files {
                name := filepath.Base(file)
                t.Run(name, func(t *testing.T) {
                        r := compileFile(t, file)

                        if r.checkOK && r.parseOK && r.lexOK {
                                t.Errorf("expected compilation to FAIL, but it succeeded (tokens=%d, decls=%d, fns=%d)",
                                        r.tokenCnt, r.declCnt, r.fnCnt)
                        }

                        t.Logf("errors=%d phase_ok=[lex=%v parse=%v check=%v]",
                                r.errCount, r.lexOK, r.parseOK, r.checkOK)
                })
        }
}
