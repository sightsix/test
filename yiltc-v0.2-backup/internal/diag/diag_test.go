package diag

import (
        "strings"
        "testing"
)

func TestDiagnosticHandlerError(t *testing.T) {
        h := NewHandler(false)
        h.AddSource("test.yilt", []byte("let x = 42\nlet y = 0\n"))

        h.Error("test.yilt", 1, 5, 8, "expected int, got string")

        if !h.HasErrors() {
                t.Error("expected error to be reported")
        }
        if h.ErrorCount() != 1 {
                t.Errorf("expected 1 error, got %d", h.ErrorCount())
        }
}

func TestDiagnosticHandlerWarning(t *testing.T) {
        h := NewHandler(false)
        h.Warn("test.yilt", 2, 1, 10, "unused variable 'x'")

        if h.HasErrors() {
                t.Error("expected no errors, only warnings")
        }
        if h.WarningCount() != 1 {
                t.Errorf("expected 1 warning, got %d", h.WarningCount())
        }
}

func TestDiagnosticHandlerNote(t *testing.T) {
        h := NewHandler(false)
        h.Error("test.yilt", 1, 1, 0, "main error")
        h.Note("test.yilt", 3, 5, 20, "related definition here")

        if h.ErrorCount() != 1 {
                t.Errorf("expected 1 error, got %d", h.ErrorCount())
        }
}

func TestDiagnosticHandlerHelp(t *testing.T) {
        h := NewHandler(false)
        h.Error("test.yilt", 1, 1, 0, "missing type")
        h.Help("try adding ': int' after the variable name")
}

func TestDiagnosticHandlerRender(t *testing.T) {
        h := NewHandler(false)
        h.AddSource("test.yilt", []byte("let x = 42\n"))
        h.Error("test.yilt", 1, 5, 4, "test error")

        // Render should not panic
        h.Render()
}

func TestDiagnosticHandlerColoredRender(t *testing.T) {
        h := NewHandler(true)
        h.AddSource("test.yilt", []byte("let x = 42\n"))
        h.Error("test.yilt", 1, 5, 4, "colored error")

        h.Render()
}

func TestSourceLineOffset(t *testing.T) {
        s := NewSource("test.yilt", []byte("line1\nline2\nline3\n"))

        if s.Line(0) != 1 {
                t.Errorf("expected line 1, got %d", s.Line(0))
        }
        if s.Line(5) != 1 {
                t.Errorf("expected line 1 for offset 5, got %d", s.Line(5))
        }
        if s.Line(6) != 2 {
                t.Errorf("expected line 2 for offset 6, got %d", s.Line(6))
        }
        if s.Line(12) != 3 {
                t.Errorf("expected line 3 for offset 12, got %d", s.Line(12))
        }
}

func TestSourceCol(t *testing.T) {
        s := NewSource("test.yilt", []byte("hello\nworld\n"))

        if s.Col(0) != 1 {
                t.Errorf("expected col 1, got %d", s.Col(0))
        }
        if s.Col(3) != 4 {
                t.Errorf("expected col 4, got %d", s.Col(3))
        }
        if s.Col(6) != 1 {
                t.Errorf("expected col 1 for start of line 2, got %d", s.Col(6))
        }
}

func TestSourceLineText(t *testing.T) {
        s := NewSource("test.yilt", []byte("hello\nworld\n"))

        text := strings.TrimRight(s.LineText(1), "\r\n")
        if text != "hello" {
                t.Errorf("expected 'hello', got '%s'", text)
        }
        text = strings.TrimRight(s.LineText(2), "\r\n")
        if text != "world" {
                t.Errorf("expected 'world', got '%s'", text)
        }
}
