package testsuite

import (
        "os"
        "path/filepath"
        "runtime"
        "strings"
        "testing"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/check"
        "github.com/yilt/yiltc/internal/diag"
        "github.com/yilt/yiltc/internal/lex"
        "github.com/yilt/yiltc/internal/ownership"
        "github.com/yilt/yiltc/internal/parse"
)

type compileResult struct {
        lexOK    bool
        parseOK  bool
        checkOK  bool
        errCount int
        errMsgs  []string
        tokenCnt int
        declCnt  int
        fnCnt    int
}

func compileFile(t *testing.T, path string) compileResult {
        t.Helper()
        src, err := os.ReadFile(path)
        if err != nil {
                t.Fatalf("cannot read %s: %s", path, err)
        }
        dh := diag.NewHandler(false)
        dh.AddSource(path, src)
        absPath, _ := filepath.Abs(path)

        result := compileResult{}

        lexer := lex.New(absPath, src)
        lexer.SetDiag(dh)
        tokens, indents := lexer.Tokenize()
        result.tokenCnt = len(tokens)
        result.lexOK = !dh.HasErrors()
        result.errCount = dh.ErrorCount()
        if dh.HasErrors() {
                result.errMsgs = dh.ErrorMessages()
                return result
        }

        parser := parse.New(absPath, tokens, indents, src)
        file := parser.ParseFile()
        for _, pe := range parser.Errors() {
                if pe.Help != "" {
                        // Use Errorf + Help so the suggestion renders nicely.
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
        result.declCnt = len(file.Decls)
        result.parseOK = !dh.HasErrors()
        result.errCount = dh.ErrorCount()
        if dh.HasErrors() {
                result.errMsgs = dh.ErrorMessages()
                return result
        }

        program := &ast.Program{
                Files: []*ast.File{file},
                Root:  filepath.Dir(absPath),
        }
        checker := check.NewChecker(program, dh)
        checked, _ := checker.Check()
        result.fnCnt = len(checked.Functions)
        result.checkOK = !dh.HasErrors()
        result.errCount = dh.ErrorCount()
        result.errMsgs = dh.ErrorMessages()
        if dh.HasErrors() {
                return result
        }

        // Run ownership analysis pass.
        ownAnalyzer := ownership.NewAnalyzer(checked, dh)
        ownAnalyzer.Analyze()
        result.checkOK = !dh.HasErrors()
        result.errCount = dh.ErrorCount()
        result.errMsgs = dh.ErrorMessages()

        return result
}

func walkYiltFiles(dir string) []string {
        var files []string
        filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
                if err != nil || info.IsDir() || !strings.HasSuffix(path, ".yilt") {
                        return nil
                }
                files = append(files, path)
                return nil
        })
        return files
}

func findTestsuiteRoot() string {
        _, thisFile, _, ok := runtime.Caller(0)
        if !ok {
                return "testsuite"
        }
        dir := filepath.Dir(thisFile)
        return filepath.Join(dir, "..", "..", "testsuite")
}
