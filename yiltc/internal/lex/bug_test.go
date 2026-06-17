package lex

import (
    "testing"

    "github.com/yilt/yiltc/internal/ast"
)

// Regression tests for the \xNN escape off-by-one fix.
// Previously, \xNN left l.pos past the escape, and callers did another l.pos++,
// swallowing the character immediately after the escape.

func TestHexEscapeString(t *testing.T) {
    // Yilt source: "\xFF!" — should produce 2-byte string: 0xFF, '!'
    src := []byte{'"', '\\', 'x', 'F', 'F', '!', '"'}
    l := New("test.yilt", src)
    tokens, _ := l.Tokenize()
    if len(tokens) < 2 {
        t.Fatalf("expected at least 2 tokens, got %d", len(tokens))
    }
    tok := tokens[0]
    if tok.Kind != ast.TStringLit {
        t.Fatalf("expected TStringLit, got %v", tok.Kind)
    }
    if len(tok.Value) != 2 {
        t.Fatalf("expected 2 bytes, got %d: %q", len(tok.Value), tok.Value)
    }
    if tok.Value[0] != 0xFF {
        t.Errorf("byte[0]: expected 0xFF, got 0x%02X", tok.Value[0])
    }
    if tok.Value[1] != '!' {
        t.Errorf("byte[1]: expected '!', got 0x%02X", tok.Value[1])
    }
}

func TestHexEscapeStringSingleDigit(t *testing.T) {
    // Yilt source: "\x0A" — should produce 1-byte string: 0x0A
    src := []byte{'"', '\\', 'x', '0', 'A', '"'}
    l := New("test.yilt", src)
    tokens, _ := l.Tokenize()
    tok := tokens[0]
    if tok.Kind != ast.TStringLit {
        t.Fatalf("expected TStringLit, got %v", tok.Kind)
    }
    if len(tok.Value) != 1 || tok.Value[0] != 0x0A {
        t.Errorf("expected 1 byte 0x0A, got %d bytes: %q", len(tok.Value), tok.Value)
    }
}

func TestHexEscapeCharLit(t *testing.T) {
    // Yilt source: '\xFF' — should produce a TCharLit with value 255
    src := []byte{'\'', '\\', 'x', 'F', 'F', '\''}
    l := New("test.yilt", src)
    tokens, _ := l.Tokenize()
    if len(tokens) < 2 {
        t.Fatalf("expected at least 2 tokens, got %d: %v", len(tokens), tokens)
    }
    tok := tokens[0]
    if tok.Kind != ast.TCharLit {
        t.Fatalf("expected TCharLit, got %v (value=%q)", tok.Kind, tok.Value)
    }
    if tok.Value != "255" {
        t.Errorf("expected char value 255, got %s", tok.Value)
    }
}

func TestHexEscapeCharLitSingleDigit(t *testing.T) {
    // Yilt source: '\x0A' — should produce a TCharLit with value 10
    src := []byte{'\'', '\\', 'x', '0', 'A', '\''}
    l := New("test.yilt", src)
    tokens, _ := l.Tokenize()
    tok := tokens[0]
    if tok.Kind != ast.TCharLit {
        t.Fatalf("expected TCharLit, got %v (value=%q)", tok.Kind, tok.Value)
    }
    if tok.Value != "10" {
        t.Errorf("expected char value 10, got %s", tok.Value)
    }
}

func TestHexEscapeStringInFString(t *testing.T) {
    // Yilt source: f"\xFF!" — f-string with \xFF escape followed by literal text
    src := []byte{'f', '"', '\\', 'x', 'F', 'F', '!', '"'}
    l := New("test.yilt", src)
    tokens, _ := l.Tokenize()
    tok := tokens[0]
    if tok.Kind != ast.TInterpStr {
        t.Fatalf("expected TInterpStr, got %v", tok.Kind)
    }
    if len(tok.Value) != 2 || tok.Value[0] != 0xFF || tok.Value[1] != '!' {
        t.Errorf("expected 2 bytes {0xFF, '!'}, got %d bytes: %q", len(tok.Value), tok.Value)
    }
}

// Regression test for && rejection.
// Previously, && was silently tokenized as two & tokens (unlike || which produced an error).

func TestAmpersandDoubleRejected(t *testing.T) {
    src := []byte("a && b")
    l := New("test.yilt", src)
    tokens, _ := l.Tokenize()
    ampCount := 0
    for _, tok := range tokens {
        if tok.Kind == ast.TAmp {
            ampCount++
        }
    }
    if ampCount == 2 {
        t.Errorf("&& was silently tokenized as two & tokens; should have produced an error")
    }
    // Should have: a (ident), b (ident), EOF (the && should be consumed with error)
    if len(tokens) < 3 {
        t.Fatalf("expected at least 3 tokens, got %d", len(tokens))
    }
    if tokens[0].Kind != ast.TIdent || tokens[0].Value != "a" {
        t.Errorf("token[0]: expected ident 'a', got %v %q", tokens[0].Kind, tokens[0].Value)
    }
    if tokens[1].Kind != ast.TIdent || tokens[1].Value != "b" {
        t.Errorf("token[1]: expected ident 'b', got %v %q", tokens[1].Kind, tokens[1].Value)
    }
}
