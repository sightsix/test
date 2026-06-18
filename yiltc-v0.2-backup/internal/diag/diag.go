package diag

import (
        "fmt"
        "os"
        "sort"
        "strings"
)

// Severity of a diagnostic message.
type Severity int

const (
        SeverityError Severity = iota
        SeverityWarning
        SeverityNote
        SeverityHelp
)

func (s Severity) String() string {
        switch s {
        case SeverityError:
                return "error"
        case SeverityWarning:
                return "warning"
        case SeverityNote:
                return "note"
        case SeverityHelp:
                return "help"
        default:
                return "unknown"
        }
}

// Color codes for terminal output.
const (
        colorReset  = "\033[0m"
        colorRed    = "\033[31m"
        colorYellow = "\033[33m"
        colorBlue   = "\033[34m"
        colorCyan   = "\033[36m"
        colorBold   = "\033[1m"
        colorDim    = "\033[2m"
)

// Source represents file source content for diagnostics.
type Source struct {
        Path     string
        Content  []byte
        LineOff  []int // offset of each line start
}

// NewSource creates a Source from a file path and content.
func NewSource(path string, content []byte) *Source {
        s := &Source{Path: path, Content: content}
        s.computeLineOffsets()
        return s
}

func (s *Source) computeLineOffsets() {
        s.LineOff = []int{0}
        for i, b := range s.Content {
                if b == '\n' {
                        s.LineOff = append(s.LineOff, i+1)
                }
        }
}

// Line returns the 1-indexed line number for a byte offset.
func (s *Source) Line(off int) int {
        lo, hi := 0, len(s.LineOff)-1
        for lo <= hi {
                mid := (lo + hi) / 2
                if s.LineOff[mid] <= off {
                        lo = mid + 1
                } else {
                        hi = mid - 1
                }
        }
        return lo
}

// Col returns the 1-indexed column for a byte offset within its line.
func (s *Source) Col(off int) int {
        line := s.Line(off) - 1
        if line < 0 || line >= len(s.LineOff) {
                return 1
        }
        return off - s.LineOff[line] + 1
}

// LineText returns the text of a 1-indexed line.
func (s *Source) LineText(line int) string {
        if line < 1 || line > len(s.LineOff) {
                return ""
        }
        start := s.LineOff[line-1]
        end := len(s.Content)
        if line < len(s.LineOff) {
                end = s.LineOff[line]
        }
        return string(s.Content[start:end])
}

// Diagnostic is a single diagnostic message.
type Diagnostic struct {
        Severity Severity
        Pos      struct {
                File    string
                Line    int
                Col     int
                Offset  int
                SpanLen int // length of the span; 0 means 1 (single character)
        }
        Code    string // optional error code, e.g. "E0001", "W0001"
        Message string
        Notes   []Diagnostic
        Sources map[string]*Source
}

// spanLen returns the effective span length (defaults to 1).
func (d *Diagnostic) spanLen() int {
        if d.Pos.SpanLen > 1 {
                return d.Pos.SpanLen
        }
        return 1
}

// DiagnosticHandler collects and renders diagnostics.
type DiagnosticHandler struct {
        diags    []Diagnostic
        sources  map[string]*Source
        color    bool
        errCount int
        wrnCount int
}

// NewHandler creates a new diagnostic handler.
func NewHandler(color bool) *DiagnosticHandler {
        return &DiagnosticHandler{
                sources: make(map[string]*Source),
                color:   color,
        }
}

// AddSource registers a source file.
func (h *DiagnosticHandler) AddSource(path string, content []byte) {
        h.sources[path] = NewSource(path, content)
}

// Error reports an error diagnostic.
func (h *DiagnosticHandler) Error(file string, line, col, offset int, msg string) {
        d := Diagnostic{
                Severity: SeverityError,
                Message:  msg,
                Sources:  h.sources,
        }
        d.Pos.File = file
        d.Pos.Line = line
        d.Pos.Col = col
        d.Pos.Offset = offset
        h.diags = append(h.diags, d)
        h.errCount++
}

// ErrorSpan reports an error diagnostic with an explicit span length.
func (h *DiagnosticHandler) ErrorSpan(file string, line, col, offset, spanLen int, msg string) {
        d := Diagnostic{
                Severity: SeverityError,
                Message:  msg,
                Sources:  h.sources,
        }
        d.Pos.File = file
        d.Pos.Line = line
        d.Pos.Col = col
        d.Pos.Offset = offset
        d.Pos.SpanLen = spanLen
        h.diags = append(h.diags, d)
        h.errCount++
}

// ErrorWithCode reports an error diagnostic with an error code.
func (h *DiagnosticHandler) ErrorWithCode(file string, line, col, offset int, code string, msg string) {
        d := Diagnostic{
                Severity: SeverityError,
                Message:  msg,
                Code:     code,
                Sources:  h.sources,
        }
        d.Pos.File = file
        d.Pos.Line = line
        d.Pos.Col = col
        d.Pos.Offset = offset
        h.diags = append(h.diags, d)
        h.errCount++
}

// Errorf reports an error with formatted message.
func (h *DiagnosticHandler) Errorf(file string, line, col, offset int, format string, args ...interface{}) {
        h.Error(file, line, col, offset, fmt.Sprintf(format, args...))
}

// ErrorfSpan reports an error with formatted message and explicit span length.
func (h *DiagnosticHandler) ErrorfSpan(file string, line, col, offset, spanLen int, format string, args ...interface{}) {
        h.ErrorSpan(file, line, col, offset, spanLen, fmt.Sprintf(format, args...))
}

// Warn reports a warning diagnostic.
func (h *DiagnosticHandler) Warn(file string, line, col, offset int, msg string) {
        d := Diagnostic{
                Severity: SeverityWarning,
                Message:  msg,
                Sources:  h.sources,
        }
        d.Pos.File = file
        d.Pos.Line = line
        d.Pos.Col = col
        d.Pos.Offset = offset
        h.diags = append(h.diags, d)
        h.wrnCount++
}

// WarnSpan reports a warning diagnostic with an explicit span length.
func (h *DiagnosticHandler) WarnSpan(file string, line, col, offset, spanLen int, msg string) {
        d := Diagnostic{
                Severity: SeverityWarning,
                Message:  msg,
                Sources:  h.sources,
        }
        d.Pos.File = file
        d.Pos.Line = line
        d.Pos.Col = col
        d.Pos.Offset = offset
        d.Pos.SpanLen = spanLen
        h.diags = append(h.diags, d)
        h.wrnCount++
}

// WarnWithCode reports a warning diagnostic with an error code.
func (h *DiagnosticHandler) WarnWithCode(file string, line, col, offset int, code string, msg string) {
        d := Diagnostic{
                Severity: SeverityWarning,
                Message:  msg,
                Code:     code,
                Sources:  h.sources,
        }
        d.Pos.File = file
        d.Pos.Line = line
        d.Pos.Col = col
        d.Pos.Offset = offset
        h.diags = append(h.diags, d)
        h.wrnCount++
}

// Warnf reports a warning with formatted message.
func (h *DiagnosticHandler) Warnf(file string, line, col, offset int, format string, args ...interface{}) {
        h.Warn(file, line, col, offset, fmt.Sprintf(format, args...))
}

// WarnfSpan reports a warning with formatted message and explicit span length.
func (h *DiagnosticHandler) WarnfSpan(file string, line, col, offset, spanLen int, format string, args ...interface{}) {
        h.WarnSpan(file, line, col, offset, spanLen, fmt.Sprintf(format, args...))
}

// Note attaches a note to the last diagnostic.
func (h *DiagnosticHandler) Note(file string, line, col, offset int, msg string) {
        if len(h.diags) == 0 {
                return
        }
        n := Diagnostic{
                Severity: SeverityNote,
                Message:  msg,
                Sources:  h.sources,
        }
        n.Pos.File = file
        n.Pos.Line = line
        n.Pos.Col = col
        n.Pos.Offset = offset
        h.diags[len(h.diags)-1].Notes = append(h.diags[len(h.diags)-1].Notes, n)
}

// Help attaches a help note to the last diagnostic.
func (h *DiagnosticHandler) Help(msg string) {
        if len(h.diags) == 0 {
                return
        }
        n := Diagnostic{
                Severity: SeverityHelp,
                Message:  msg,
        }
        h.diags[len(h.diags)-1].Notes = append(h.diags[len(h.diags)-1].Notes, n)
}

// Suggest attaches a suggestion note (help with special rendering) to the last diagnostic.
func (h *DiagnosticHandler) Suggest(file string, line, col, offset int, msg string) {
        if len(h.diags) == 0 {
                return
        }
        n := Diagnostic{
                Severity: SeverityHelp,
                Message:  msg,
        }
        n.Pos.File = file
        n.Pos.Line = line
        n.Pos.Col = col
        n.Pos.Offset = offset
        h.diags[len(h.diags)-1].Notes = append(h.diags[len(h.diags)-1].Notes, n)
}

// HasErrors returns true if any errors were reported.
func (h *DiagnosticHandler) HasErrors() bool { return h.errCount > 0 }

// ErrorCount returns the number of errors.
func (h *DiagnosticHandler) ErrorCount() int { return h.errCount }

// WarningCount returns the number of warnings.
func (h *DiagnosticHandler) WarningCount() int { return h.wrnCount }

// sortedDiags returns diagnostics sorted with all errors first, then warnings.
func (h *DiagnosticHandler) sortedDiags() []Diagnostic {
        sorted := make([]Diagnostic, len(h.diags))
        copy(sorted, h.diags)
        sort.SliceStable(sorted, func(i, j int) bool {
                // Errors (severity 0) come before warnings (severity 1), notes/help (severity 2,3)
                return sorted[i].Severity < sorted[j].Severity
        })
        return sorted
}

// Render prints all diagnostics to stderr, with errors first, then warnings.
func (h *DiagnosticHandler) Render() {
        for _, d := range h.sortedDiags() {
                h.renderOne(d)
        }
}

// RenderWarnings prints only warning diagnostics to stderr.
func (h *DiagnosticHandler) RenderWarnings() {
        for _, d := range h.diags {
                if d.Severity == SeverityWarning {
                        h.renderOne(d)
                }
        }
}

func (h *DiagnosticHandler) renderOne(d Diagnostic) {
        var b strings.Builder

        // Main message
        label := h.sevLabel(d.Severity)
        loc := h.formatLoc(d.Pos)
        if loc != "" {
                if d.Code != "" {
                        b.WriteString(fmt.Sprintf("%s[%s]: %s\n", label, d.Code, d.Message))
                } else {
                        b.WriteString(fmt.Sprintf("%s%s: %s\n", label, loc, d.Message))
                }
        } else {
                if d.Code != "" {
                        b.WriteString(fmt.Sprintf("%s[%s]: %s\n", label, d.Code, d.Message))
                } else {
                        b.WriteString(fmt.Sprintf("%s%s\n", label, d.Message))
                }
        }

        // Source context with multi-line support
        if d.Pos.Line > 0 {
                src := h.sources[d.Pos.File]
                if src != nil {
                        h.renderSourceContext(&b, src, d.Pos.Line, d.Pos.Col, d.Severity, d.spanLen(), "", 0)
                }
        }

        // Notes
        for _, n := range d.Notes {
                if n.Severity == SeverityNote {
                        nlabel := h.sevLabel(SeverityNote)
                        nloc := h.formatLoc(n.Pos)
                        if nloc != "" {
                                b.WriteString(fmt.Sprintf("  %s%s: %s\n", nlabel, nloc, n.Message))
                        } else {
                                b.WriteString(fmt.Sprintf("  %s%s\n", nlabel, n.Message))
                        }
                        // Note source context
                        if n.Pos.Line > 0 {
                                src := h.sources[n.Pos.File]
                                if src != nil {
                                        nSpanLen := 1
                                        if n.Pos.SpanLen > 1 {
                                                nSpanLen = n.Pos.SpanLen
                                        }
                                        h.renderSourceContext(&b, src, n.Pos.Line, n.Pos.Col, n.Severity, nSpanLen, "", 3)
                                }
                        }
                } else if n.Severity == SeverityHelp {
                        hlabel := h.helpLabel()
                        if n.Pos.File != "" || n.Pos.Line > 0 {
                                // Suggestion with source location - show source context
                                b.WriteString(fmt.Sprintf("  %s%s\n", hlabel, n.Message))
                                if n.Pos.Line > 0 {
                                        src := h.sources[n.Pos.File]
                                        if src != nil {
                                                nSpanLen := 1
                                                if n.Pos.SpanLen > 1 {
                                                        nSpanLen = n.Pos.SpanLen
                                                }
                                                h.renderSourceContext(&b, src, n.Pos.Line, n.Pos.Col, SeverityHelp, nSpanLen, "", 3)
                                        }
                                }
                        } else {
                                // Plain help without location
                                b.WriteString(fmt.Sprintf("  %s%s\n", hlabel, n.Message))
                        }
                }
        }

        fmt.Fprint(os.Stderr, b.String())
}

// renderSourceContext renders the source lines around the diagnostic position.
// indent is the number of leading spaces for the gutter (0 for main, 3 for notes).
func (h *DiagnosticHandler) renderSourceContext(b *strings.Builder, src *Source, line, col int, sev Severity, spanLen int, msg string, indent int) {
        totalLines := len(src.LineOff)

        // Determine range: 1 line before and 1 line after
        startLine := line - 1
        endLine := line + 1
        if startLine < 1 {
                startLine = 1
        }
        if endLine > totalLines {
                endLine = totalLines
        }

        // Find max line number width for gutter alignment
        maxLineNum := endLine
        gutterWidth := len(fmt.Sprintf("%d", maxLineNum))
        indentStr := strings.Repeat(" ", indent)

        // Gutter color helpers — no-op when color is disabled.
        dimOn := colorDim
        dimOff := colorReset
        if !h.color {
                dimOn = ""
                dimOff = ""
        }

        // Print lines before error
        for ln := startLine; ln < line; ln++ {
                lineText := src.LineText(ln)
                if lineText == "" {
                        continue
                }
                lnStr := fmt.Sprintf("%*d", gutterWidth, ln)
                b.WriteString(fmt.Sprintf("%s%s%s |%s %s\n", indentStr, dimOn, lnStr, dimOff, strings.TrimRight(lineText, "\r\n")))
        }

        // Print error line with underline
        lineText := src.LineText(line)
        if lineText != "" {
                lnStr := fmt.Sprintf("%*d", gutterWidth, line)
                b.WriteString(fmt.Sprintf("%s%s%s |%s %s\n", indentStr, dimOn, lnStr, dimOff, strings.TrimRight(lineText, "\r\n")))

                // Underline
                ul := h.underline(sev, spanLen)
                padding := col - 1
                if padding < 0 {
                        padding = 0
                }
                b.WriteString(fmt.Sprintf("%s%s |%s %s%s", indentStr, dimOn, dimOff,
                        strings.Repeat(" ", padding), ul))
                if msg != "" {
                        b.WriteString(" " + msg)
                }
                b.WriteString("\n")
        }

        // Print lines after error
        for ln := line + 1; ln <= endLine; ln++ {
                lineText := src.LineText(ln)
                if lineText == "" {
                        continue
                }
                lnStr := fmt.Sprintf("%*d", gutterWidth, ln)
                b.WriteString(fmt.Sprintf("%s%s%s |%s %s\n", indentStr, dimOn, lnStr, dimOff, strings.TrimRight(lineText, "\r\n")))
        }
}

func (h *DiagnosticHandler) sevLabel(s Severity) string {
        if !h.color {
                return fmt.Sprintf("[%s] ", s)
        }
        switch s {
        case SeverityError:
                return fmt.Sprintf("%s%s%s ", colorBold+colorRed, "error", colorReset)
        case SeverityWarning:
                return fmt.Sprintf("%s%s%s ", colorBold+colorYellow, "warning", colorReset)
        case SeverityNote:
                return fmt.Sprintf("%s%s%s ", colorBlue, "note", colorReset)
        case SeverityHelp:
                return fmt.Sprintf("%s%s%s ", colorCyan, "help", colorReset)
        default:
                return fmt.Sprintf("[%s] ", s)
        }
}

// helpLabel returns the label string for help/suggestion notes.
// Uses "= help:" format (like rustc/gcc) to avoid the "[h" terminal
// escape issue where some terminals interpret "[h" as a CSI sequence
// after printing underline characters.
func (h *DiagnosticHandler) helpLabel() string {
        if !h.color {
                return "= help: "
        }
        return fmt.Sprintf("%s= help:%s ", colorCyan, colorReset)
}

func (h *DiagnosticHandler) underline(s Severity, spanLen int) string {
        char := "^"
        if s == SeverityWarning {
                char = "~"
        }
        if spanLen < 1 {
                spanLen = 1
        }
        repeated := strings.Repeat(char, spanLen)
        if !h.color {
                return repeated
        }
        switch s {
        case SeverityError:
                return fmt.Sprintf("%s%s%s", colorRed, repeated, colorReset)
        case SeverityWarning:
                return fmt.Sprintf("%s%s%s", colorYellow, repeated, colorReset)
        default:
                return repeated
        }
}

func (h *DiagnosticHandler) formatLoc(pos struct {
        File    string
        Line    int
        Col     int
        Offset  int
        SpanLen int
}) string {
        if pos.File == "" && pos.Line == 0 {
                return ""
        }
        if pos.File == "" {
                return fmt.Sprintf(":%d:%d", pos.Line, pos.Col)
        }
        return fmt.Sprintf("%s:%d:%d", pos.File, pos.Line, pos.Col)
}

// ErrorMessages returns all error messages as strings.
func (h *DiagnosticHandler) ErrorMessages() []string {
        msgs := make([]string, 0, len(h.diags))
        for _, d := range h.diags {
                if d.Severity == SeverityError {
                        loc := h.formatLoc(d.Pos)
                        if loc != "" {
                                msgs = append(msgs, loc+": "+d.Message)
                        } else {
                                msgs = append(msgs, d.Message)
                        }
                }
        }
        return msgs
}

// RenderSummary prints a compact summary of all diagnostics.
func (h *DiagnosticHandler) RenderSummary() {
        if h.errCount == 0 && h.wrnCount == 0 {
                return
        }

        var b strings.Builder
        b.WriteString("\n")

        if h.errCount > 0 {
                if h.color {
                        b.WriteString(fmt.Sprintf("%s%serror%s: aborting due to %d error%s\n",
                                colorBold, colorRed, colorReset, h.errCount, plural(h.errCount)))
                } else {
                        b.WriteString(fmt.Sprintf("error: aborting due to %d error%s\n",
                                h.errCount, plural(h.errCount)))
                }
                // List error codes
                for _, d := range h.diags {
                        if d.Severity == SeverityError {
                                if d.Code != "" {
                                        b.WriteString(fmt.Sprintf("  %s  %s\n", d.Code, d.Message))
                                }
                        }
                }
        }

        if h.wrnCount > 0 {
                if h.errCount > 0 {
                        b.WriteString(fmt.Sprintf("\ncompilation finished with %d error%s, %d warning%s\n",
                                h.errCount, plural(h.errCount), h.wrnCount, plural(h.wrnCount)))
                } else {
                        b.WriteString(fmt.Sprintf("compilation finished with %d warning%s\n",
                                h.wrnCount, plural(h.wrnCount)))
                }
                // List warning codes
                for _, d := range h.diags {
                        if d.Severity == SeverityWarning {
                                if d.Code != "" {
                                        b.WriteString(fmt.Sprintf("  %s  %s\n", d.Code, d.Message))
                                }
                        }
                }
        }

        fmt.Fprint(os.Stderr, b.String())
}

// plural returns "s" if n != 1, empty string otherwise.
func plural(n int) string {
        if n != 1 {
                return "s"
        }
        return ""
}

// FatalIfErrors renders diagnostics and exits if there are errors.
func (h *DiagnosticHandler) FatalIfErrors() {
        if h.HasErrors() {
                h.Render()
                h.RenderSummary()
                os.Exit(1)
        }
}
