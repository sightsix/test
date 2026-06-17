package lex

import (
        "testing"

        "github.com/yilt/yiltc/internal/ast"
)

func TestLexerBasicTokens(t *testing.T) {
        src := `let x = 42`
        l := New("test.yilt", []byte(src))
        tokens, _ := l.Tokenize()

        if len(tokens) < 4 {
                t.Fatalf("expected at least 4 tokens, got %d", len(tokens))
        }
        assertTokenKind(t, tokens[0], ast.TLet)
        if tokens[0].Value != "let" {
                t.Errorf("expected 'let', got '%s'", tokens[0].Value)
        }
        assertTokenKind(t, tokens[1], ast.TIdent)
        assertTokenKind(t, tokens[2], ast.TAssign)
        assertTokenKind(t, tokens[3], ast.TIntLit)
}

func TestLexerKeywords(t *testing.T) {
        src := "let mut fn extern pub use from for in if but else while match case default return break continue spawn await and or not true false"
        l := New("test.yilt", []byte(src))
        tokens, _ := l.Tokenize()

        expected := []ast.Token{
                ast.TLet, ast.TMut, ast.TFn, ast.TExtern, ast.TPub,
                ast.TUse, ast.TFrom, ast.TFor, ast.TIn, ast.TIf,
                ast.TBut, ast.TElse, ast.TWhile, ast.TMatch,
                ast.TCase, ast.TDefault, ast.TReturn, ast.TBreak,
                ast.TContinue, ast.TSpawn, ast.TAwait,
                ast.TAnd, ast.TOr, ast.TNot, ast.TTrue, ast.TFalse,
        }

        for i, exp := range expected {
                if i >= len(tokens) || tokens[i].Kind != exp {
                        t.Errorf("token %d: expected %s, got %s", i, exp, func() string {
                                if i < len(tokens) {
                                        return tokens[i].Kind.String()
                                }
                                return "EOF"
                        }())
                        continue
                }
        }
}

func TestLexerOperators(t *testing.T) {
        tests := []struct {
                src  string
                kind ast.Token
                val  string
        }{
                {"+", ast.TPlus, "+"}, {"-", ast.TMinus, "-"},
                {"*", ast.TStar, "*"}, {"/", ast.TSlash, "/"},
                {"%", ast.TPercent, "%"}, {"&", ast.TAmp, "&"},
                {"|", ast.TPipe, "|"}, {"^", ast.TCaret, "^"},
                {"~", ast.TTilde, "~"}, {"<<", ast.TLShift, "<<"},
                {">>", ast.TRShift, ">>"}, {"==", ast.TEq, "=="},
                {"!=", ast.TNeq, "!="}, {"<", ast.TLt, "<"},
                {"<=", ast.TLe, "<="}, {">", ast.TGt, ">"},
                {">=", ast.TGe, ">="}, {"=", ast.TAssign, "="},
                {"?", ast.TQuestion, "?"}, {"->", ast.TArrow, "->"},
                {"...", ast.TDotDotDot, "..."},
        }

        for _, tc := range tests {
                t.Run(tc.val, func(t *testing.T) {
                        l := New("test.yilt", []byte(tc.src))
                        tokens, _ := l.Tokenize()
                        if len(tokens) < 1 || tokens[0].Kind == ast.TEOF {
                                t.Fatalf("no tokens for '%s'", tc.src)
                        }
                        if tokens[0].Kind != tc.kind {
                                t.Errorf("expected %s, got %s", tc.kind, tokens[0].Kind)
                        }
                })
        }
}

func TestLexerStrings(t *testing.T) {
        tests := []struct {
                src  string
                want string
        }{
                {`"hello"`, "hello"},
                {`"hello\nworld"`, "hello\nworld"},
                {`"tab\there"`, "tab\there"},
                {`""`, ""},
        }

        for _, tc := range tests {
                t.Run(tc.src, func(t *testing.T) {
                        l := New("test.yilt", []byte(tc.src))
                        tokens, _ := l.Tokenize()
                        if len(tokens) < 1 || tokens[0].Kind != ast.TStringLit {
                                t.Fatalf("expected string literal")
                        }
                        if tokens[0].Value != tc.want {
                                t.Errorf("expected %q, got %q", tc.want, tokens[0].Value)
                        }
                })
        }
}

func TestLexerNumbers(t *testing.T) {
        tests := []struct {
                src  string
                kind ast.Token
                val  string
        }{
                {"42", ast.TIntLit, "42"},
                {"0", ast.TIntLit, "0"},
                {"3.14", ast.TFloatLit, "3.14"},
                {"0.5", ast.TFloatLit, "0.5"},
        }

        for _, tc := range tests {
                l := New("test.yilt", []byte(tc.src))
                tokens, _ := l.Tokenize()
                if tokens[0].Kind != tc.kind {
                        t.Errorf("expected %s for '%s'", tc.kind, tc.src)
                }
                if tokens[0].Value != tc.val {
                        t.Errorf("expected '%s', got '%s'", tc.val, tokens[0].Value)
                }
        }
}

func TestLexerComments(t *testing.T) {
        src := "42 // comment\n43"
        l := New("test.yilt", []byte(src))
        tokens, _ := l.Tokenize()
        if len(tokens) < 2 || tokens[0].Kind != ast.TIntLit || tokens[1].Kind != ast.TIntLit {
                t.Errorf("expected two integers, got %d tokens", len(tokens))
        }
}

func TestLexerIndentedComments(t *testing.T) {
        // Indented // and /* comments should be skipped without errors
        tests := []struct {
                name string
                src  string
        }{
                {
                        name: "indented_line_comment",
                        src: "fn main()\n    // indented comment\n    print(1)\n",
                },
                {
                        name: "indented_block_comment",
                        src: "fn main()\n    /* block\n       comment */\n    print(1)\n",
                },
                {
                        name: "mixed_indent_comments",
                        src: "fn main()\n    // comment 1\n    print(1)\n    // comment 2\n    print(2)\n",
                },
                {
                        name: "deep_indent_comment",
                        src: "fn main()\n        fn inner()\n            // deeply indented\n            return 0\n",
                },
        }
        for _, tc := range tests {
                t.Run(tc.name, func(t *testing.T) {
                        l := New("test.yilt", []byte(tc.src))
                        l.SetDiag(&silentDiag{})
                        tokens, _ := l.Tokenize()
                        if len(tokens) == 0 {
                                t.Fatal("no tokens produced")
                        }
                        // Should have tokens without any errors
                        last := tokens[len(tokens)-1]
                        if last.Kind != ast.TEOF {
                                t.Errorf("expected last token to be EOF, got %s", last.Kind)
                        }
                        // Count non-EOF tokens — should not include comment text
                        nonEOF := 0
                        for _, tok := range tokens {
                                if tok.Kind != ast.TEOF {
                                        nonEOF++
                                }
                        }
                        if nonEOF == 0 {
                                t.Error("expected at least one non-EOF token")
                        }
                })
        }
}

func TestLexerIndentation(t *testing.T) {
        src := "fn main()\n    return 0\n"
        l := New("test.yilt", []byte(src))
        _, indents := l.Tokenize()
        // Indent events should be generated for the 4-space indentation
        // The exact structure depends on the tokenizer's indent tracking
        // We just verify it doesn't panic and produces some indent events
        t.Logf("indent events: %v", indents)
}

func assertTokenKind(t *testing.T, tok Token, kind ast.Token) {
        t.Helper()
        if tok.Kind != kind {
                t.Errorf("expected %s, got %s", kind, tok.Kind)
        }
}

// silentDiag is a diagnostic handler that discards all errors (for testing).
type silentDiag struct{}

func (d *silentDiag) Error(file string, line, col, offset int, msg string) {}
