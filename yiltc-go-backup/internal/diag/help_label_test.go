package diag

import (
    "os"
    "strings"
    "testing"
)

func TestHelpLabelNonColored(t *testing.T) {
    h := NewHandler(false)
    label := h.helpLabel()
    if label != "= help: " {
        t.Errorf("expected '= help: ', got %q", label)
    }
}

func TestHelpLabelColored(t *testing.T) {
    h := NewHandler(true)
    label := h.helpLabel()
    if !strings.Contains(label, "= help:") {
        t.Errorf("expected colored label to contain '= help:', got %q", label)
    }
    if !strings.Contains(label, "\033[36m") {
        t.Errorf("expected cyan color in label, got %q", label)
    }
}

func TestHelpRenderOutputNotCorrupted(t *testing.T) {
    old := os.Stderr
    r, w, _ := os.Pipe()
    os.Stderr = w

    h := NewHandler(false)
    h.AddSource("test.yilt", []byte("let x = 42\n"))
    h.Error("test.yilt", 1, 5, 4, "test error")
    h.Help("try adding ': int'")
    h.Render()

    w.Close()
    os.Stderr = old

    buf := make([]byte, 4096)
    n, _ := r.Read(buf)
    output := string(buf[:n])

    // The output should contain "= help:" not a corrupted version
    if !strings.Contains(output, "= help:") {
        t.Errorf("expected '= help:' in output, got:\n%s", output)
    }
    // The output should also contain "[error]"
    if !strings.Contains(output, "[error]") {
        t.Errorf("expected '[error]' in output, got:\n%s", output)
    }
}

func TestSuggestRenderOutputNotCorrupted(t *testing.T) {
    old := os.Stderr
    r, w, _ := os.Pipe()
    os.Stderr = w

    h := NewHandler(false)
    h.AddSource("test.yilt", []byte("pritn(42)\n"))
    h.Error("test.yilt", 1, 1, 0, "undefined identifier 'pritn'")
    h.Suggest("test.yilt", 1, 1, 0, "did you mean 'print'?")
    h.Render()

    w.Close()
    os.Stderr = old

    buf := make([]byte, 4096)
    n, _ := r.Read(buf)
    output := string(buf[:n])

    if !strings.Contains(output, "= help:") {
        t.Errorf("expected '= help:' in suggest output, got:\n%s", output)
    }
    if !strings.Contains(output, "did you mean") {
        t.Errorf("expected suggestion text in output, got:\n%s", output)
    }
}
