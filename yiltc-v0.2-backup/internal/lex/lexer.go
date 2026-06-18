package lex

import (
        "fmt"
        "os"
        "strings"
        "unicode/utf8"

        "github.com/yilt/yiltc/internal/ast"
)

// Token represents a single lexical token with its source position.
type Token struct {
        Kind   ast.Token
        Value  string
        Line   int
        Col    int
        Offset int
}

// IndentToken is emitted to signal indentation level changes.
type IndentToken struct {
        Level int // indentation level (in 4-space units)
        Line  int
}

// Lexer converts Yilt source text into tokens.
type Lexer struct {
        src     []byte
        pos     int
        line    int
        col     int
        offset  int
        file    string
        tokens  []Token
        indents []IndentToken

        // For tracking indentation
        lineStart    bool
        indentStack  []int
        pendingDedents int

        diag diagHandler
}

type diagHandler interface {
        Error(file string, line, col, offset int, msg string)
}

// New creates a new lexer for the given source.
func New(file string, src []byte) *Lexer {
        // Ensure source ends with a newline so the lexer properly
        // emits dedent tokens for the last indented block.
        if len(src) > 0 && src[len(src)-1] != '\n' {
                src = append(src, '\n')
        }
        return &Lexer{
                src:   src,
                file:  file,
                line:  1,
                col:   1,
                diag:  &defaultDiag{},
        }
}

// SetDiag sets a custom diagnostic handler.
func (l *Lexer) SetDiag(h diagHandler) { l.diag = h }

type defaultDiag struct{}

func (d *defaultDiag) Error(file string, line, col, offset int, msg string) {
        fmt.Fprintf(os.Stderr, "%s:%d:%d: error: %s\n", file, line, col, msg)
}

// Tokenize scans the entire source and returns tokens and indent tokens.
func (l *Lexer) Tokenize() ([]Token, []IndentToken) {
        l.indentStack = []int{0} // base level is 0
        l.lineStart = true

        for l.pos < len(l.src) {
                l.skipWhitespaceAndNewlines()

                if l.pos >= len(l.src) {
                        break
                }

                // Handle indentation at line start
                if l.lineStart {
                        l.lineStart = false
                        l.handleIndent()
                        // After handling indent, we might have dedent tokens to process
                        // Continue to lex the actual token on this line
                        if l.pos >= len(l.src) {
                                break
                        }
                }

                offAtLexStart := l.pos
                l.lexToken()
                // After lexing a token, advance l.col by the number of bytes
                // the lexer just consumed (l.pos - offAtLexStart).  This
                // keeps l.col in sync with l.pos so the NEXT token gets the
                // correct starting column.
                //
                // Previously l.col was only updated for whitespace and
                // newlines, which caused every token after the first one on
                // a line to be reported with the wrong column — and that
                // made every multi-token diagnostic point at the wrong place.
                if l.pos > offAtLexStart {
                        l.col += l.pos - offAtLexStart
                }
        }

        // Emit remaining dedents
        for len(l.indentStack) > 1 {
                l.indentStack = l.indentStack[:len(l.indentStack)-1]
                l.indents = append(l.indents, IndentToken{Level: l.indentStack[len(l.indentStack)-1], Line: l.line})
        }

        // Emit EOF
        l.tokens = append(l.tokens, Token{
                Kind:  ast.TEOF,
                Line:  l.line,
                Col:   l.col,
                Offset: l.offset,
        })
        l.indents = append(l.indents, IndentToken{Level: -1, Line: l.line})

        return l.tokens, l.indents
}

func (l *Lexer) handleIndent() {
        savedPos := l.pos
        savedCol := l.col

        // Count indentation
        indent := 0
        for l.pos < len(l.src) && l.src[l.pos] == ' ' {
                l.pos++
                l.col++
                l.offset = l.pos
                indent++
        }

        // Skip if line is blank or comment-only
        if l.pos >= len(l.src) || l.src[l.pos] == '\n' || l.src[l.pos] == '\r' ||
                (l.src[l.pos] == '/' && l.pos+1 < len(l.src) && (l.src[l.pos+1] == '/' || l.src[l.pos+1] == '*')) {
                l.pos = savedPos
                l.col = savedCol
                l.offset = savedPos
                l.lineStart = true
                return
        }

        // Validate 4-space indentation
        if indent%4 != 0 {
                l.diag.Error(l.file, l.line, savedCol, savedPos,
                        fmt.Sprintf("indentation must be a multiple of 4 spaces, found %d spaces", indent))
        }

        level := indent / 4
        currentLevel := l.indentStack[len(l.indentStack)-1]

        if level > currentLevel {
                l.indentStack = append(l.indentStack, level)
                l.indents = append(l.indents, IndentToken{Level: level, Line: l.line})
        } else if level < currentLevel {
                // Emit dedents until we match
                for len(l.indentStack) > 1 && l.indentStack[len(l.indentStack)-1] > level {
                        l.indentStack = l.indentStack[:len(l.indentStack)-1]
                        lvl := l.indentStack[len(l.indentStack)-1]
                        l.indents = append(l.indents, IndentToken{Level: lvl, Line: l.line})
                }
                if l.indentStack[len(l.indentStack)-1] != level {
                        l.diag.Error(l.file, l.line, savedCol, savedPos,
                                fmt.Sprintf("unexpected dedent to level %d, expected %d", level, l.indentStack[len(l.indentStack)-1]))
                }
        }
        // If level == currentLevel, no indent token needed
}

func (l *Lexer) skipWhitespaceAndNewlines() {
        for l.pos < len(l.src) {
                ch := l.src[l.pos]
                if ch == '\n' {
                        l.pos++
                        l.offset = l.pos
                        l.line++
                        l.col = 1
                        l.lineStart = true
                        // Skip blank lines quickly — only skip newlines and
                        // whitespace-only content, but do NOT consume leading
                        // spaces of the next non-blank line (those are indentation).
                        for l.pos < len(l.src) {
                                c := l.src[l.pos]
                                if c == '\n' {
                                        l.pos++
                                        l.offset = l.pos
                                        l.line++
                                        l.col = 1
                                } else if c == '\r' {
                                        l.pos++
                                } else if c == ' ' || c == '\t' {
                                        // Might be indentation on a non-blank line — stop
                                        // so handleIndent() can count these spaces.
                                        // But first check if the REST of this line is blank.
                                        restBlank := true
                                        scanPos := l.pos
                                        for scanPos < len(l.src) && l.src[scanPos] != '\n' {
                                                if l.src[scanPos] != ' ' && l.src[scanPos] != '\t' && l.src[scanPos] != '\r' {
                                                        restBlank = false
                                                        break
                                                }
                                                scanPos++
                                        }
                                        if restBlank {
                                                l.pos++
                                        } else {
                                                break
                                        }
                                } else {
                                        break
                                }
                        }
                } else if ch == ' ' || ch == '\t' || ch == '\r' {
                        // Don't skip leading whitespace when at the start of a line —
                        // handleIndent() needs to count those spaces as indentation.
                        // BUT: check if the line is a comment-only line (spaces + // or /*),
                        // in which case we should skip the whole line to avoid emitting
                        // errors for the leading spaces.
                        if l.lineStart {
                                if l.isCommentOnlyLine() {
                                        l.skipRestOfLine()
                                        continue
                                }
                                break
                        }
                        l.pos++
                        l.col++
                        l.offset = l.pos
                } else if ch == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
                        // Line comment
                        l.pos += 2
                        for l.pos < len(l.src) && l.src[l.pos] != '\n' {
                                l.pos++
                        }
                } else if ch == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*' {
                        // Block comment (possibly nested)
                        l.skipBlockComment(l.line, l.col, l.offset)
                } else {
                        break
                }
        }
}

// skipBlockComment consumes a /* ... */ block comment, supporting nesting.
// It tracks line numbers for multi-line comments.
func (l *Lexer) skipBlockComment(line, col, off int) {
        l.pos += 2 // skip /*
        l.col += 2
        depth := 1
        for l.pos < len(l.src) && depth > 0 {
                ch := l.src[l.pos]
                if ch == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*' {
                        depth++
                        l.pos += 2
                        l.col += 2
                } else if ch == '*' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
                        depth--
                        l.pos += 2
                        l.col += 2
                } else if ch == '\n' {
                        l.pos++
                        l.offset = l.pos
                        l.line++
                        l.col = 1
                        l.lineStart = true
                } else if ch == '\r' {
                        l.pos++
                } else {
                        l.pos++
                        l.col++
                }
        }
        if depth > 0 {
                l.diag.Error(l.file, line, col, off, "unterminated block comment")
        }
}

func (l *Lexer) lexToken() {
        ch := l.src[l.pos]
        line := l.line
        col := l.col
        off := l.offset

        switch {
        case ch == '"':
                l.lexString(line, col, off)
        case ch == '\'':
                l.lexCharLit(line, col, off)
        case ch >= '0' && ch <= '9':
                l.lexNumber(line, col, off)
        case isIdentStart(ch):
                l.lexIdent(line, col, off)
        case ch == '(':
                l.emit(ast.TLParen, "(", line, col, off)
                l.pos++
        case ch == ')':
                l.emit(ast.TRParen, ")", line, col, off)
                l.pos++
        case ch == '{':
                l.emit(ast.TLBrace, "{", line, col, off)
                l.pos++
        case ch == '}':
                l.emit(ast.TRBrace, "}", line, col, off)
                l.pos++
        case ch == '[':
                l.emit(ast.TLBrack, "[", line, col, off)
                l.pos++
        case ch == ']':
                l.emit(ast.TRBrack, "]", line, col, off)
                l.pos++
        case ch == ',':
                l.emit(ast.TComma, ",", line, col, off)
                l.pos++
        case ch == '.':
                if l.pos+2 < len(l.src) && l.src[l.pos+1] == '.' && l.src[l.pos+2] == '.' {
                        l.emit(ast.TDotDotDot, "...", line, col, off)
                        l.pos += 3
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '.' {
                        l.emit(ast.TDotDot, "..", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TPeriod, ".", line, col, off)
                        l.pos++
                }
        case ch == ':':
                l.emit(ast.TColon, ":", line, col, off)
                l.pos++
        case ch == '+':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '+' {
                        l.emit(ast.TPlusPlus, "++", line, col, off)
                        l.pos += 2
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TPlusEq, "+=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TPlus, "+", line, col, off)
                        l.pos++
                }
        case ch == '*':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TStarEq, "*=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TStar, "*", line, col, off)
                        l.pos++
                }
        case ch == '/':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TSlashEq, "/=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TSlash, "/", line, col, off)
                        l.pos++
                }
        case ch == '%':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TPercentEq, "%=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TPercent, "%", line, col, off)
                        l.pos++
                }
        case ch == '&':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '&' {
                        l.diag.Error(l.file, line, col, off, "unexpected '&&', did you mean 'and'?")
                        l.pos += 2 // skip both '&' characters
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TAmpEq, "&=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TAmp, "&", line, col, off)
                        l.pos++
                }
        case ch == '|':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '|' {
                        l.diag.Error(l.file, line, col, off, "unexpected '|', did you mean 'or'?")
                        l.pos += 2 // skip both '|' characters
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TPipeEq, "|=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TPipe, "|", line, col, off)
                        l.pos++
                }
        case ch == '^':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TCaretEq, "^=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TCaret, "^", line, col, off)
                        l.pos++
                }
        case ch == '~':
                l.emit(ast.TTilde, "~", line, col, off)
                l.pos++
        case ch == '?':
                l.emit(ast.TQuestion, "?", line, col, off)
                l.pos++
        case ch == '-':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '-' {
                        l.emit(ast.TMinusMinus, "--", line, col, off)
                        l.pos += 2
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
                        l.emit(ast.TArrow, "->", line, col, off)
                        l.pos += 2
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TMinusEq, "-=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TMinus, "-", line, col, off)
                        l.pos++
                }
        case ch == '<':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '<' {
                        if l.pos+2 < len(l.src) && l.src[l.pos+2] == '=' {
                                l.emit(ast.TShlEq, "<<=", line, col, off)
                                l.pos += 3
                        } else {
                                l.emit(ast.TLShift, "<<", line, col, off)
                                l.pos += 2
                        }
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TLe, "<=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TLt, "<", line, col, off)
                        l.pos++
                }
        case ch == '>':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
                        if l.pos+2 < len(l.src) && l.src[l.pos+2] == '=' {
                                l.emit(ast.TShrEq, ">>=", line, col, off)
                                l.pos += 3
                        } else {
                                l.emit(ast.TRShift, ">>", line, col, off)
                                l.pos += 2
                        }
                } else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TGe, ">=", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TGt, ">", line, col, off)
                        l.pos++
                }
        case ch == '=':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TEq, "==", line, col, off)
                        l.pos += 2
                } else {
                        l.emit(ast.TAssign, "=", line, col, off)
                        l.pos++
                }
        case ch == '!':
                if l.pos+1 < len(l.src) && l.src[l.pos+1] == '=' {
                        l.emit(ast.TNeq, "!=", line, col, off)
                        l.pos += 2
                } else {
                        l.diag.Error(l.file, line, col, off, "unexpected '!', did you mean 'not' or '!='?")
                        l.pos++
                }
        default:
                r, _ := utf8.DecodeRune(l.src[l.pos:])
                l.diag.Error(l.file, line, col, off,
                        fmt.Sprintf("unexpected character '%U'", r))
                l.pos++
        }
}

func (l *Lexer) lexString(line, col, off int) {
        l.pos++ // skip opening quote
        var b strings.Builder

        for l.pos < len(l.src) && l.src[l.pos] != '"' {
                ch := l.src[l.pos]
                if ch == '\n' {
                        l.diag.Error(l.file, line, col, off, "unterminated string literal")
                        return
                }
                if ch == '\\' {
                        v, ok := l.lexEscape(line, col, off)
                        if !ok {
                                return
                        }
                        b.WriteByte(v)
                } else {
                        b.WriteByte(ch)
                }
                l.pos++
        }

        if l.pos >= len(l.src) {
                l.diag.Error(l.file, line, col, off, "unterminated string literal")
                return
        }
        l.pos++ // skip closing quote

        l.tokens = append(l.tokens, Token{
                Kind:   ast.TStringLit,
                Value:  b.String(),
                Line:   line,
                Col:    col,
                Offset: off,
        })
}

func (l *Lexer) lexNumber(line, col, off int) {
        start := l.pos
        isFloat := false

        if l.src[l.pos] == '0' && l.pos+1 < len(l.src) {
                next := l.src[l.pos+1]
                switch {
                case next == 'x' || next == 'X':
                        // Hex literal: 0x[0-9a-fA-F_]+
                        l.pos += 2
                        for l.pos < len(l.src) {
                                ch := l.src[l.pos]
                                if isHexDigit(ch) {
                                        l.pos++
                                } else if ch == '_' {
                                        l.pos++
                                } else {
                                        break
                                }
                        }
                        l.tokens = append(l.tokens, Token{
                                Kind:   ast.TIntLit,
                                Value:  string(l.src[start:l.pos]),
                                Line:   line,
                                Col:    col,
                                Offset: off,
                        })
                        return
                case next == 'b' || next == 'B':
                        // Binary literal: 0b[01_]+
                        l.pos += 2
                        for l.pos < len(l.src) {
                                ch := l.src[l.pos]
                                if ch == '0' || ch == '1' {
                                        l.pos++
                                } else if ch == '_' {
                                        l.pos++
                                } else {
                                        break
                                }
                        }
                        l.tokens = append(l.tokens, Token{
                                Kind:   ast.TIntLit,
                                Value:  string(l.src[start:l.pos]),
                                Line:   line,
                                Col:    col,
                                Offset: off,
                        })
                        return
                case next == 'o' || next == 'O':
                        // Octal literal: 0o[0-7_]+
                        l.pos += 2
                        for l.pos < len(l.src) {
                                ch := l.src[l.pos]
                                if ch >= '0' && ch <= '7' {
                                        l.pos++
                                } else if ch == '_' {
                                        l.pos++
                                } else {
                                        break
                                }
                        }
                        l.tokens = append(l.tokens, Token{
                                Kind:   ast.TIntLit,
                                Value:  string(l.src[start:l.pos]),
                                Line:   line,
                                Col:    col,
                                Offset: off,
                        })
                        return
                case next >= '0' && next <= '7':
                        // C-style octal: 0[0-7_]+
                        l.pos++ // skip leading 0
                        for l.pos < len(l.src) {
                                ch := l.src[l.pos]
                                if ch >= '0' && ch <= '7' {
                                        l.pos++
                                } else if ch == '_' {
                                        l.pos++
                                } else {
                                        break
                                }
                        }
                        l.tokens = append(l.tokens, Token{
                                Kind:   ast.TIntLit,
                                Value:  string(l.src[start:l.pos]),
                                Line:   line,
                                Col:    col,
                                Offset: off,
                        })
                        return
                }
        }

        for l.pos < len(l.src) {
                ch := l.src[l.pos]
                if ch >= '0' && ch <= '9' {
                        l.pos++
                } else if ch == '_' {
                        l.pos++ // skip underscore separator
                } else if ch == '.' && !isFloat {
                        // Check it's not .. or ...
                        if l.pos+1 < len(l.src) && (l.src[l.pos+1] == '.' || l.src[l.pos+1] >= 'a' && l.src[l.pos+1] <= 'z') {
                                break // it's a method call or range, not float
                        }
                        isFloat = true
                        l.pos++
                } else {
                        break
                }
        }

        kind := ast.TIntLit
        if isFloat {
                kind = ast.TFloatLit
        }

        l.tokens = append(l.tokens, Token{
                Kind:   kind,
                Value:  string(l.src[start:l.pos]),
                Line:   line,
                Col:    col,
                Offset: off,
        })
}

var keywords = map[string]ast.Token{
        "let":     ast.TLet,
        "mut":     ast.TMut,
        "fn":      ast.TFn,
        "extern":  ast.TExtern,
        "pub":     ast.TPub,
        "use":     ast.TUse,
        "from":    ast.TFrom,
        "for":     ast.TFor,
        "in":      ast.TIn,
        "if":      ast.TIf,
        "but":     ast.TBut,
        "else":    ast.TElse,
        "while":   ast.TWhile,
        "match":   ast.TMatch,
        "case":    ast.TCase,
        "default": ast.TDefault,
        "return":  ast.TReturn,
        "break":   ast.TBreak,
        "continue": ast.TContinue,
        "spawn":    ast.TSpawn,
        "await":    ast.TAwait,
        "and":      ast.TAnd,
        "or":      ast.TOr,
        "not":     ast.TNot,
        "true":    ast.TTrue,
        "false":   ast.TFalse,
        "nil":     ast.TNil,
        "assert":  ast.TAssert,
        "struct":  ast.TStruct,
        "enum":    ast.TEnum,
}

func (l *Lexer) lexIdent(line, col, off int) {
        start := l.pos
        for l.pos < len(l.src) && isIdentContinue(l.src[l.pos]) {
                l.pos++
        }
        word := string(l.src[start:l.pos])

        // f-string: when the identifier is exactly "f" and immediately
        // followed by a double-quote, lex as an interpolated string.
        if word == "f" && l.pos < len(l.src) && l.src[l.pos] == '"' {
                l.lexInterpString(line, col, off)
                return
        }

        if tok, ok := keywords[word]; ok {
                l.tokens = append(l.tokens, Token{
                        Kind:   tok,
                        Value:  word,
                        Line:   line,
                        Col:    col,
                        Offset: off,
                })
        } else {
                l.tokens = append(l.tokens, Token{
                        Kind:   ast.TIdent,
                        Value:  word,
                        Line:   line,
                        Col:    col,
                        Offset: off,
                })
        }
}

// lexInterpString lexes an f-string literal: f"...{expr}...".
// It collects the raw content (with {expr} segments intact) and emits a
// single TInterpStr token.  Brace nesting is tracked so that expressions
// may contain nested braces (e.g. table literals).
func (l *Lexer) lexInterpString(line, col, off int) {
        l.pos++ // skip the opening "
        var b strings.Builder
        depth := 0

        for l.pos < len(l.src) {
                ch := l.src[l.pos]
                if ch == '\\' && depth == 0 {
                        // Escape sequence inside a literal part.
                        if l.pos+1 < len(l.src) {
                                next := l.src[l.pos+1]
                                if next == '{' {
                                        b.WriteByte('{')
                                        l.pos += 2
                                        continue
                                }
                                if next == '}' {
                                        b.WriteByte('}')
                                        l.pos += 2
                                        continue
                                }
                        }
                        // Delegate to the shared escape handler for all other escapes
                        // (\n, \t, \\, \", \0, \xNN, \', \a, \b, \f, \v).
                        // lexEscape handles its own position advancement and error reporting.
                        savedPos := l.pos
                        v, ok := l.lexEscape(line, col, off)
                        if ok {
                                b.WriteByte(v)
                        } else {
                                // lexEscape reported an error and returned 0; write nothing
                        }
                        // lexEscape advances past the escape; but if it failed at the
                        // opening backslash, we need to make progress ourselves.
                        if l.pos == savedPos {
                                l.pos++
                        }
                } else if ch == '{' {
                        depth++
                        b.WriteByte('{')
                } else if ch == '}' {
                        if depth > 0 {
                                depth--
                                b.WriteByte('}')
                        } else {
                                l.diag.Error(l.file, l.line, l.col, l.offset,
                                        "unexpected '}' in f-string (no matching '{')")
                                b.WriteByte('}')
                        }
                } else if ch == '"' && depth == 0 {
                        // Closing quote.
                        l.pos++
                        l.tokens = append(l.tokens, Token{
                                Kind:   ast.TInterpStr,
                                Value:  b.String(),
                                Line:   line,
                                Col:    col,
                                Offset: off,
                        })
                        return
                } else if ch == '\n' {
                        l.diag.Error(l.file, line, col, off, "unterminated f-string literal")
                        return
                } else {
                        b.WriteByte(ch)
                }
                l.pos++
        }

        l.diag.Error(l.file, line, col, off, "unterminated f-string literal")
}

func (l *Lexer) emit(kind ast.Token, value string, line, col, off int) {
        l.tokens = append(l.tokens, Token{
                Kind:   kind,
                Value:  value,
                Line:   line,
                Col:    col,
                Offset: off,
        })
}

func isIdentStart(ch byte) bool {
        return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentContinue(ch byte) bool {
        return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

func isHexDigit(ch byte) bool {
        return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func hexVal(ch byte) int {
        switch {
        case ch >= '0' && ch <= '9':
                return int(ch - '0')
        case ch >= 'a' && ch <= 'f':
                return int(ch - 'a' + 10)
        case ch >= 'A' && ch <= 'F':
                return int(ch - 'A' + 10)
        default:
                return 0
        }
}

// lexEscape processes a backslash escape sequence in a string or character literal.
// Returns the byte value and true on success, or 0 and false on error.
// After a successful call, l.pos points to the last character of the escape.
func (l *Lexer) lexEscape(line, col, off int) (byte, bool) {
        l.pos++ // skip backslash
        if l.pos >= len(l.src) {
                l.diag.Error(l.file, line, col, off, "unterminated escape in string")
                return 0, false
        }
        esc := l.src[l.pos]
        switch esc {
        case 'n':
                return '\n', true
        case 't':
                return '\t', true
        case 'r':
                return '\r', true
        case '\\':
                return '\\', true
        case '"':
                return '"', true
        case '\'':
                return '\'', true
        case '0':
                return 0, true
        case 'a':
                return '\a', true // BEL (0x07)
        case 'b':
                return '\b', true // BS (0x08)
        case 'f':
                return '\f', true // FF (0x0C)
        case 'v':
                return '\v', true // VT (0x0B)
        case 'x':
                // Hex escape: \xNN (1-2 hex digits)
                l.pos++ // skip 'x'
                if l.pos >= len(l.src) || !isHexDigit(l.src[l.pos]) {
                        l.diag.Error(l.file, line, col, off, "invalid hex escape")
                        return 0, false
                }
                val := hexVal(l.src[l.pos])
                if l.pos+1 < len(l.src) && isHexDigit(l.src[l.pos+1]) {
                        val = val*16 + hexVal(l.src[l.pos+1])
                        l.pos++ // advance to second hex digit
                }
                // l.pos is at the last hex digit, consistent with other escapes
                return byte(val), true
        default:
                l.diag.Error(l.file, l.line, l.col, l.offset,
                        fmt.Sprintf("unknown escape sequence '\\%c'", esc))
                return esc, true
        }
}

// lexCharLit lexes a character literal: 'c' or '\n' etc.
// Emits a TCharLit token with the integer value stored in Value as a decimal string.
func (l *Lexer) lexCharLit(line, col, off int) {
        l.pos++ // skip opening '

        var val byte
        if l.pos >= len(l.src) {
                l.diag.Error(l.file, line, col, off, "unterminated character literal")
                return
        }

        ch := l.src[l.pos]
        if ch == '\\' {
                v, ok := l.lexEscape(line, col, off)
                if !ok {
                        return
                }
                val = v
        } else if ch == '\n' {
                l.diag.Error(l.file, line, col, off, "unterminated character literal")
                return
        } else if ch == '\'' {
                l.diag.Error(l.file, line, col, off, "empty character literal")
                return
        } else {
                val = ch
        }
        l.pos++

        if l.pos >= len(l.src) || l.src[l.pos] != '\'' {
                l.diag.Error(l.file, line, col, off, "unterminated character literal")
                return
        }
        l.pos++ // skip closing '

        l.tokens = append(l.tokens, Token{
                Kind:   ast.TCharLit,
                Value:  fmt.Sprintf("%d", val),
                Line:   line,
                Col:    col,
                Offset: off,
        })
}

// isCommentOnlyLine checks if the current line (starting at l.pos, which is at
// the first space of a lineStart) consists only of whitespace followed by a
// line or block comment. It does NOT consume any characters.
func (l *Lexer) isCommentOnlyLine() bool {
    scanPos := l.pos
    for scanPos < len(l.src) && (l.src[scanPos] == ' ' || l.src[scanPos] == '\t') {
        scanPos++
    }
    if scanPos >= len(l.src) || l.src[scanPos] == '\n' || l.src[scanPos] == '\r' {
        return true // blank line
    }
    if l.src[scanPos] == '/' && scanPos+1 < len(l.src) {
        return l.src[scanPos+1] == '/' || l.src[scanPos+1] == '*'
    }
    return false
}

// skipRestOfLine consumes characters from l.pos to the end of the line
// (including the newline), updating line/col tracking.
func (l *Lexer) skipRestOfLine() {
    for l.pos < len(l.src) && l.src[l.pos] != '\n' {
        l.pos++
    }
    if l.pos < len(l.src) && l.src[l.pos] == '\n' {
        l.pos++
        l.offset = l.pos
        l.line++
        l.col = 1
        l.lineStart = true
    }
}

// IsStdModule returns true if the name is a built-in standard module.
func IsStdModule(name string) bool {
        switch name {
        case "sys", "fs", "path", "json", "math":
                return true
        default:
                return false
        }
}
