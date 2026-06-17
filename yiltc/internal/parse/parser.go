package parse

import (
        "fmt"
        "strconv"
        "strings"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/lex"
)

// Parser converts a stream of tokens into an AST.
type Parser struct {
        tokens []lex.Token
        indents []lex.IndentToken
        pos     int
        indentPos int
        indentLevel int // current indentation level (number of active indents)
        file    string
        src     []byte
        errors  []ParseError
        // structNames is populated by prescanStructs to allow struct names
        // as type annotations in function parameters and return types.
        structNames map[string]bool
        // enumNames is populated by prescanStructNames to allow enum names
        // as type annotations and to recognize enum variant literals.
        enumNames map[string]bool
        // genericNames is populated by prescanGenericNames to allow
        // ident[type_args] syntax for generic function calls.
        genericNames map[string]bool
        // typeParamNames is populated by prescanStructNames with the names
        // declared inside `[T, U, ...]` brackets of generic fn/struct/enum
        // declarations.  These are recognised as valid type annotations
        // WITHIN the corresponding declaration's body so the parser can
        // accept signatures like `fn id[T](x T) T`.
        typeParamNames map[string]bool
        // inMatchPattern is set to true while parsing the value expression
        // of a match case.  When true and an enum variant has a single
        // bare-identifier payload, it is parsed as EnumMatchPattern (binding)
        // instead of EnumLit (expression).
        inMatchPattern bool
}

// ParseError is a parsing error with context.
type ParseError struct {
        File   string
        Line   int
        Col    int
        Offset int
        Msg    string
        // Help is an optional suggestion shown under the error as "= help: ...".
        Help string
        // SpanLen is the length of the underline; 0 means 1 (single char).
        SpanLen int
}

func (e ParseError) Error() string {
        return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
}

// New creates a new parser.
func New(file string, tokens []lex.Token, indents []lex.IndentToken, src []byte) *Parser {
        p := &Parser{
                tokens:  tokens,
                indents: indents,
                file:    file,
                src:     src,
        }
        p.prescanStructNames()
        return p
}

// tokDesc returns a human-friendly description of a token for error messages.
// For identifiers and literals, it shows the actual value (e.g., "'foo'", "'42'").
// For keywords and operators, it shows the kind string (e.g., "'fn'", "'+'").
func tokDesc(tok lex.Token) string {
        switch tok.Kind {
        case ast.TIdent:
                if tok.Value != "" {
                        return "'" + tok.Value + "'"
                }
                return "identifier"
        case ast.TIntLit:
                if tok.Value != "" {
                        return "integer literal '" + tok.Value + "'"
                }
                return "integer literal"
        case ast.TFloatLit:
                if tok.Value != "" {
                        return "float literal '" + tok.Value + "'"
                }
                return "float literal"
        case ast.TStringLit:
                if tok.Value != "" {
                        // Truncate long strings in error messages
                        v := tok.Value
                        if len(v) > 20 {
                                v = v[:17] + "..."
                        }
                        return "string literal " + strconv.Quote(v)
                }
                return "string literal"
        default:
                return "'" + tok.Kind.String() + "'"
        }
}

// ParseFile parses the token stream into a File AST.
func (p *Parser) ParseFile() *ast.File {
        file := &ast.File{Path: p.file}

        for !p.atEnd() {
                decl := p.parseTopLevel()
                if decl != nil {
                        file.Decls = append(file.Decls, decl)
                }
        }

        return file
}

func (p *Parser) parseTopLevel() ast.Decl {
        tok := p.peek()
        switch tok.Kind {
        case ast.TConst:
                // Top-level const declaration
                pos := p.posAST()
                p.advance() // const
                name := p.expect(ast.TIdent)
                if name == nil {
                        return nil
                }
                p.expect(ast.TAssign)
                value := p.parseExpr()
                return &ast.ConstDecl{
                        Name:  name.Value,
                        Value: value,
                        Span:  pos,
                }
        case ast.TLet:
                // Top-level let — only const-expr bindings are allowed at top level.
                // Other let bindings are not valid top-level declarations; emit an error.
                stmt := p.parseLet()
                if let, ok := stmt.(*ast.LetStmt); ok {
                        if p.isConstExpr(let.Value) {
                                return &ast.ConstDecl{
                                        Name:  let.Name,
                                        Value: let.Value,
                                        Span:  let.Span,
                                }
                        }
                        p.error(tok, "top-level 'let' requires a constant value; use 'const' for compile-time constants, or wrap in a function")
                        return nil
                }
                return nil
        case ast.TFn, ast.TPub, ast.TStruct, ast.TEnum:
                return p.parseFnOrStructDecl()
        case ast.TUse:
                return p.parseUseDecl()
        case ast.TFrom:
                return p.parseFromDecl()
        default:
                p.error(tok, "expected top-level declaration (fn, const, let, use, from)")
                p.advance() // skip past the unrecognized token to prevent infinite loop
                return nil
        }
}

// ========== Function Declaration ==========

// parseFnOrStructDecl dispatches to parseFnDecl, parseStructDecl, or parseEnumDecl
// based on whether the next token after optional 'pub' is 'fn', 'struct', or 'enum'.
func (p *Parser) parseFnOrStructDecl() ast.Decl {
        pub := false
        if p.peek().Kind == ast.TPub {
                p.advance()
                pub = true
        }
        switch p.peek().Kind {
        case ast.TFn:
                return p.parseFnDecl(pub)
        case ast.TStruct:
                return p.parseStructDecl(pub)
        case ast.TEnum:
                return p.parseEnumDecl(pub)
        default:
                p.error(p.peek(), "expected 'fn', 'struct', or 'enum'")
                return nil
        }
}

// parseTypeParams parses optional type parameters: [T, U]
// Returns nil if the next token is not '['.
func (p *Parser) parseTypeParams() []string {
        if p.peek().Kind != ast.TLBrack {
                return nil
        }
        p.advance() // [

        var params []string
        for p.peek().Kind != ast.TRBrack && !p.atEnd() {
                if len(params) > 0 {
                        p.expect(ast.TComma)
                }
                tok := p.expect(ast.TIdent)
                if tok == nil {
                        break
                }
                params = append(params, tok.Value)
        }
        p.expect(ast.TRBrack)
        return params
}

// parseTypeArgs parses optional explicit type arguments: [int, str]
// Returns nil if the next token is not '['.
func (p *Parser) parseTypeArgs() []ast.TypeRef {
        if p.peek().Kind != ast.TLBrack {
                return nil
        }
        p.advance() // [

        var args []ast.TypeRef
        startpos := p.pos
        for p.peek().Kind != ast.TRBrack && !p.atEnd() {
                if len(args) > 0 {
                        if p.peek().Kind != ast.TComma {
                                break // malformed type args — stop to avoid infinite loop
                        }
                        p.advance() // consume comma
                }
                t := p.parseTypeRef()
                if t != nil {
                        args = append(args, *t)
                }
                // Safety: if parseTypeRef/expect didn't advance, skip a token
                if p.pos <= startpos {
                        p.advance()
                }
                startpos = p.pos
        }
        p.expect(ast.TRBrack)
        return args
}

// parseReturnType parses an optional return type after the closing ')' of
// a function signature.  Enforces the no-arrow rule: `-> int` is rejected
// with a diagnostic suggesting the bare form `int`.  Returns the parsed
// return type (retType, retTypes) — either a single type or a tuple.
//
// Both retType and retTypes may be nil/empty if no return type is present.
func (p *Parser) parseReturnType() (retType *ast.TypeRef, retTypes []ast.TypeRef) {
        if p.peek().Kind == ast.TArrow {
                // Reject `->` and offer the bare-form fix.
                arrowTok := p.peek()
                p.errorDiag(arrowTok, 2,
                        "arrow syntax '->' is not allowed for function return types",
                        "Yilt uses a bare type after ')': write 'fn foo() int' instead of 'fn foo() -> int'")
                p.advance() // consume the arrow so parsing can continue
                // Still parse the (now-expectable) return type so downstream
                // phases have something to work with and we don't cascade.
                if p.peek().Kind == ast.TLParen {
                        retTypes = p.parseTupleType()
                } else if p.peek().Kind == ast.TIdent && p.isTypeName(p.peek().Value) {
                        retType = p.parseTypeRef()
                }
                return retType, retTypes
        }
        if p.peek().Kind == ast.TLParen {
                // Bare tuple return type: fn foo() (int, str)
                retTypes = p.parseTupleType()
                return nil, retTypes
        }
        if p.peek().Kind == ast.TIdent && p.isTypeName(p.peek().Value) {
                retType = p.parseTypeRef()
                return retType, nil
        }
        return nil, nil
}

func (p *Parser) parseFnDecl(pub bool) *ast.FnDecl {
        pos := p.posAST()

        if p.peek().Kind != ast.TFn {
                p.error(p.peek(), "expected 'fn'")
                return nil
        }
        p.advance() // fn

        extern := false
        if p.peek().Kind == ast.TExtern {
                p.advance()
                extern = true
        }

        name := p.expect(ast.TIdent)
        if name == nil {
                return nil
        }

        // Optional type parameters: [T, U]
        typeParams := p.parseTypeParams()

        // Parameters
        p.expect(ast.TLParen)
        params := p.parseParams()
        p.expect(ast.TRParen)

        // Return type (optional) — bare type name only.
        // Yilt enforces a NO-ARROW rule: function return types are written
        // as a bare type after the closing paren, e.g. `fn foo() int`.
        // The arrow form `fn foo() -> int` is rejected with a diagnostic
        // that suggests the bare form.  See parseReturnType for the logic.
        retType, retTypes := p.parseReturnType()

        fn := &ast.FnDecl{
                Name:       name.Value,
                TypeParams: typeParams,
                Params:     params,
                ReturnType: retType,
                RetTypes:   retTypes,
                Public:     pub,
                Extern:     extern,
                Span:       pos,
        }

        if extern {
                // No body for extern functions
                return fn
        }

        // Parse body (indented block)
        body := p.parseBlock()
        fn.Body = body

        return fn
}

// ========== Struct Declaration ==========

// parseStructDecl parses:
//   struct Name
//       field1 Type
//       field2 Type
// Each field can be prefixed with 'mut' for mutable fields.
func (p *Parser) parseStructDecl(pub bool) *ast.StructDecl {
        pos := p.posAST()

        p.advance() // struct

        name := p.expect(ast.TIdent)
        if name == nil {
                return nil
        }

        // Optional type parameters: [T]
        typeParams := p.parseTypeParams()

        // Struct body uses indentation-based blocks (like function bodies)
        if !p.consumeIndent() {
                p.error(p.peek(), "expected indented block after struct declaration")
                return nil
        }

        var fields []ast.StructField
        for !p.atEnd() {
                // Check for dedent — end of block
                if p.isDedent() {
                        p.consumeDedent()
                        break
                }
                fpos := p.posAST()

                mutable := false
                if p.peek().Kind == ast.TMut {
                        p.advance()
                        mutable = true
                }

                fname := p.expect(ast.TIdent)
                if fname == nil {
                        return nil
                }

                ftype := p.parseTypeRef()
                fields = append(fields, ast.StructField{
                        Name:     fname.Value,
                        Type:     *ftype,
                        Mutable: mutable,
                        Span:     fpos,
                })
        }

        p.consumeDedent()

        return &ast.StructDecl{
                Name:       name.Value,
                TypeParams: typeParams,
                Fields:     fields,
                Public:     pub,
                Span:       pos,
        }
}

// parseEnumDecl parses an enum declaration.
//   enum Color
//       Red
//       Green
//       Blue
//   enum Result
//       Ok(value)
//       Err(msg str)
func (p *Parser) parseEnumDecl(pub bool) *ast.EnumDecl {
        pos := p.posAST()

        p.advance() // enum

        name := p.expect(ast.TIdent)
        if name == nil {
                return nil
        }

        // Optional type parameters: [T]
        typeParams := p.parseTypeParams()

        // Enum body uses indentation-based blocks (like struct)
        if !p.consumeIndent() {
                p.error(p.peek(), "expected indented block after enum declaration")
                return nil
        }

        var variants []ast.EnumVariant
        for !p.atEnd() {
                if p.isDedent() {
                        p.consumeDedent()
                        break
                }
                vpos := p.posAST()

                vname := p.expect(ast.TIdent)
                if vname == nil {
                        return nil
                }

                // Check if this variant has a payload: Name(Type)
                var payload *ast.TypeRef
                if p.peek().Kind == ast.TLParen {
                        p.advance() // (
                        payload = p.parseTypeRef()
                        if tok := p.expect(ast.TRParen); tok == nil {
                                return nil
                        }
                }

                variants = append(variants, ast.EnumVariant{
                        Name:    vname.Value,
                        Payload: payload,
                        Span:    vpos,
                })
        }

        p.consumeDedent()

        return &ast.EnumDecl{
                Name:       name.Value,
                TypeParams: typeParams,
                Variants:   variants,
                Public:     pub,
                Span:       pos,
        }
}

func (p *Parser) parseParams() []ast.Param {
        var params []ast.Param
        for p.peek().Kind != ast.TRParen && !p.atEnd() {
                if len(params) > 0 {
                        p.expect(ast.TComma)
                }

                mutable := false
                if p.peek().Kind == ast.TMut {
                        p.advance()
                        mutable = true
                }

                name := p.expect(ast.TIdent)
                if name == nil {
                        break
                }

                // Check for variadic parameter: 'name ...Type'
                variadic := false
                if p.peek().Kind == ast.TDotDotDot {
                        p.advance()
                        variadic = true
                        // After consuming ..., verify we're at the last param position.
                        // If there are more params after this one, it's an error.
                        // We can't easily look ahead here, so we rely on the checker
                        // to enforce "variadic must be last" for multi-param declarations.
                }

                // Only parse type annotation if the next token looks like a type.
                var typ ast.TypeRef
                if p.peek().Kind == ast.TIdent && p.isTypeName(p.peek().Value) {
                        typ = *p.parseTypeRef()
                } else if p.peek().Kind == ast.TLBrack {
                        typ = *p.parseTypeRef()
                } else {
                        typ = ast.TypeRef{Kind: ast.KindGen, Span: p.posFromTok(p.peek())}
                }

                // Parse optional default value: name type = expr
                var defaultVal ast.Expr
                if p.peek().Kind == ast.TAssign {
                        p.advance() // consume '='
                        defaultVal = p.parseExpr()
                }

                params = append(params, ast.Param{
                        Name:     name.Value,
                        Type:     typ,
                        Mutable:  mutable,
                        Variadic: variadic,
                        Default:  defaultVal,
                        Span:      ast.Pos{File: p.file, Line: name.Line, Col: name.Col},
                })
        }
        return params
}

func (p *Parser) parseTypeRef() *ast.TypeRef {
        tok := p.peek()
        switch {
        case tok.Kind == ast.TIdent:
                p.advance()
                name := tok.Value
                kind := ast.KindNamed
                switch name {
                case "int":
                        kind = ast.KindInt
                case "uint":
                        kind = ast.KindUint
                case "fp":
                        kind = ast.KindFp
                case "bool":
                        kind = ast.KindBool
                case "str":
                        kind = ast.KindStr
                case "table":
                        kind = ast.KindTable
                case "gen":
                        kind = ast.KindGen
                case "void":
                        kind = ast.KindVoid
                }
                return &ast.TypeRef{Kind: kind, Name: name, Span: p.posFromTok(tok)}
        case tok.Kind == ast.TLBrack:
                // Array/table type sugar
                p.advance()
                p.expect(ast.TRBrack)
                return &ast.TypeRef{Kind: ast.KindTable, Name: "table", Span: p.posFromTok(tok)}
        default:
                p.error(tok, fmt.Sprintf("expected type, got %s", tokDesc(tok)))
                return &ast.TypeRef{Kind: ast.KindGen, Span: p.posFromTok(tok)}
        }
}

// parseTupleType parses a tuple type: (int, str, bool).
// The opening parenthesis has already been consumed.
func (p *Parser) parseTupleType() []ast.TypeRef {
        p.expect(ast.TLParen)
        var types []ast.TypeRef
        for p.peek().Kind != ast.TRParen && !p.atEnd() {
                if len(types) > 0 {
                        p.expect(ast.TComma)
                        if p.peek().Kind == ast.TRParen {
                                break // trailing comma
                        }
                }
                t := p.parseTypeRef()
                if t != nil {
                        types = append(types, *t)
                }
        }
        p.expect(ast.TRParen)
        return types
}

// ========== Use / From Declarations ==========

func (p *Parser) parseUseDecl() ast.Decl {
        pos := p.posAST()
        p.expect(ast.TUse)

        // Check for FFI import
        if p.peek().Kind == ast.TStringLit {
                module := p.peek()
                p.advance()
                mod := strings.TrimPrefix(module.Value, "ffi:")

                alias := ""
                if p.peek().Kind == ast.TIdent && p.peek().Value == "as" {
                        p.advance()
                        a := p.expect(ast.TIdent)
                        if a != nil {
                                alias = a.Value
                        }
                }

                return &ast.FFIUseDecl{
                        Module: mod,
                        Alias:  alias,
                        Span: pos,
                }
        }

        mod := p.expect(ast.TIdent)
        if mod == nil {
                return nil
        }

        // Consume :: separated path segments (e.g. std::math::linear)
        modulePath := mod.Value
        for p.peek().Kind == ast.TColon && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == ast.TColon {
                p.advance() // skip first :
                p.advance() // skip second :
                seg := p.expect(ast.TIdent)
                if seg == nil {
                        break
                }
                modulePath += "::" + seg.Value
        }

        alias := ""
        if p.peek().Kind == ast.TIdent && p.peek().Value == "as" {
                p.advance()
                a := p.expect(ast.TIdent)
                if a != nil {
                        alias = a.Value
                }
        }

        return &ast.UseDecl{
                Module: modulePath,
                Alias:  alias,
                Span: pos,
        }
}

func (p *Parser) parseFromDecl() ast.Decl {
        pos := p.posAST()
        p.expect(ast.TFrom)

        // Check for FFI import
        if p.peek().Kind == ast.TStringLit {
                module := p.peek()
                p.advance()
                mod := strings.TrimPrefix(module.Value, "ffi:")

                p.expect(ast.TUse)
                symbols := p.parseUseSymbols()

                return &ast.FFIUseDecl{
                        Module:  mod,
                        Symbols: symbols,
                        Span:    pos,
                }
        }

        mod := p.expect(ast.TIdent)
        if mod == nil {
                return nil
        }

        // Consume :: separated path segments (e.g. std::math::linear)
        modulePath := mod.Value
        for p.peek().Kind == ast.TColon && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == ast.TColon {
                p.advance() // skip first :
                p.advance() // skip second :
                seg := p.expect(ast.TIdent)
                if seg == nil {
                        break
                }
                modulePath += "::" + seg.Value
        }

        p.expect(ast.TUse)
        symbols := p.parseUseSymbols()

        return &ast.UseDecl{
                Module:  modulePath,
                From:    modulePath,
                Symbols: symbols,
                Span:    pos,
        }
}

func (p *Parser) parseUseSymbols() []ast.UseSymbol {
        var symbols []ast.UseSymbol
        for {
                name := p.expect(ast.TIdent)
                if name == nil {
                        break
                }
                alias := ""
                if p.peek().Kind == ast.TIdent && p.peek().Value == "as" {
                        p.advance()
                        a := p.expect(ast.TIdent)
                        if a != nil {
                                alias = a.Value
                        }
                }
                symbols = append(symbols, ast.UseSymbol{
                        Name:  name.Value,
                        Alias: alias,
                        Span:   p.posFromTok(*name),
                })
                if p.peek().Kind != ast.TComma {
                        break
                }
                p.advance() // skip comma
        }
        return symbols
}

// ========== Block Parsing ==========

func (p *Parser) parseBlock() []ast.Stmt {
        var stmts []ast.Stmt

        // Expect indent increase
        if !p.consumeIndent() {
                return stmts
        }

        for !p.atEnd() {
                // Check for dedent — end of this block
                if p.isDedent() {
                        p.consumeDedent()
                        break
                }

                stmt := p.parseStmt()
                if stmt != nil {
                        stmts = append(stmts, stmt)
                }
        }

        return stmts
}

// ========== Statement Parsing ==========

func (p *Parser) parseStmt() ast.Stmt {
        tok := p.peek()
        switch tok.Kind {
        case ast.TConst:
                // Local const: const name = expr
                pos := p.posAST()
                p.advance() // const
                name := p.expect(ast.TIdent)
                if name == nil {
                        return nil
                }
                p.expect(ast.TAssign)
                value := p.parseExpr()
                return &ast.ConstStmt{
                        Name:  name.Value,
                        Value: value,
                        Span:  pos,
                }
        case ast.TAssert:
                return p.parseAssert()
        case ast.TLet:
                return p.parseLet()
        case ast.TIf:
                return p.parseIf()
        case ast.TWhile:
                return p.parseWhile()
        case ast.TFor:
                return p.parseFor()
        case ast.TMatch:
                return p.parseMatch()
        case ast.TReturn:
                return p.parseReturn()
        case ast.TBreak:
                p.advance()
                return &ast.BreakStmt{Span: p.posFromTok(tok)}
        case ast.TContinue:
                p.advance()
                return &ast.ContinueStmt{Span: p.posFromTok(tok)}
        case ast.TFn:
                // Check if this is a named function (fn name(...)) or anonymous (fn(...))
                if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == ast.TLParen {
                        // Anonymous function at statement position — treat as expression statement
                        return p.parseExprStmt()
                }
                // Named local function: fn name(params) body
                return p.parseLocalFn()
        case ast.TPub:
                // Could be pub fn (local function with pub modifier - ignored in local scope)
                if p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == ast.TFn {
                        return p.parseLocalFn()
                }
                return p.parseExprStmt()
        default:
                return p.parseExprStmt()
        }
}

func (p *Parser) parseLet() ast.Stmt {
        pos := p.posAST()
        p.expect(ast.TLet)

        mutable := false
        if p.peek().Kind == ast.TMut {
                p.advance()
                mutable = true
        }

        // Check for tuple destructuring: let (a, b) = expr
        if p.peek().Kind == ast.TLParen {
                p.advance() // consume (
                var names []string
                for p.peek().Kind != ast.TRParen && !p.atEnd() {
                        if len(names) > 0 {
                                p.expect(ast.TComma)
                                if p.peek().Kind == ast.TRParen {
                                        break // trailing comma
                                }
                        }
                        name := p.expect(ast.TIdent)
                        if name == nil {
                                break
                        }
                        names = append(names, name.Value)
                }
                p.expect(ast.TRParen)
                p.expect(ast.TAssign)
                value := p.parseExpr()
                return &ast.TupleDestructStmt{
                        Names:   names,
                        Mutable: mutable,
                        Value:   value,
                        Span:     pos,
                }
        }

        name := p.expect(ast.TIdent)
        if name == nil {
                return nil
        }

        // Optional type annotation: let name type = value
        var typ *ast.TypeRef
        if p.peek().Kind == ast.TIdent && p.isTypeName(p.peek().Value) {
                typ = p.parseTypeRef()
        }

        p.expect(ast.TAssign)
        value := p.parseExpr()

        return &ast.LetStmt{
                Name:    name.Value,
                Mutable: mutable,
                Type:    typ,
                Value:   value,
                Span:     pos,
        }
}

func (p *Parser) parseIf() ast.Stmt {
        pos := p.posAST()
        branches := []ast.IfBranch{}

        // Parse if
        p.expect(ast.TIf)
        cond := p.parseExpr()
        body := p.parseBlock()
        branches = append(branches, ast.IfBranch{Cond: cond, Body: body, Span: pos})

        // Parse but chains
        for p.peek().Kind == ast.TBut {
                butPos := p.posAST()
                p.advance() // consume but
                cond := p.parseExpr()
                body := p.parseBlock()
                branches = append(branches, ast.IfBranch{Cond: cond, Body: body, Span: butPos})
        }

        // Parse else
        var elseBody []ast.Stmt
        if p.peek().Kind == ast.TElse {
                p.advance()
                elseBody = p.parseBlock()
        }

        return &ast.IfStmt{
                Branches: branches,
                Else:     elseBody,
                Span:      pos,
        }
}

func (p *Parser) parseWhile() ast.Stmt {
        pos := p.posAST()
        p.expect(ast.TWhile)
        cond := p.parseExpr()
        body := p.parseBlock()
        return &ast.WhileStmt{Cond: cond, Body: body, Span: pos}
}

func (p *Parser) parseFor() ast.Stmt {
        pos := p.posAST()
        p.expect(ast.TFor)

        key := p.expect(ast.TIdent)
        if key == nil {
                return nil
        }

        value := ""
        if p.peek().Kind == ast.TComma {
                p.advance()
                v := p.expect(ast.TIdent)
                if v != nil {
                        value = v.Value
                }
        }

        if p.peek().Kind != ast.TIn {
                p.error(p.peek(), fmt.Sprintf("expected 'in'"))
                return nil
        }
        p.advance()
        over := p.parseExpr()
        body := p.parseBlock()

        return &ast.ForStmt{
                Key:   key.Value,
                Value: value,
                Over:  over,
                Body:  body,
                Span:   pos,
        }
}

func (p *Parser) parseMatch() ast.Stmt {
        pos := p.posAST()
        p.expect(ast.TMatch)
        subject := p.parseExpr()

        // Consume the indent for the match block body.
        if !p.consumeIndent() {
                p.error(p.peek(), "expected indented match block")
                return &ast.MatchStmt{Subject: subject, Span: pos}
        }

        var cases []ast.MatchCase
        var defaultBody []ast.Stmt

        // Parse case/default arms directly (they are keywords, not statements).
        for !p.atEnd() {
                if p.isDedent() {
                        p.consumeDedent()
                        break
                }

                if p.peek().Kind == ast.TCase {
                        casePos := p.posAST()
                        p.advance() // case
                        p.inMatchPattern = true
                        value := p.parseExpr()
                        p.inMatchPattern = false
                        body := p.parseBlock()
                        cases = append(cases, ast.MatchCase{Value: value, Body: body, Span: casePos})
                } else if p.peek().Kind == ast.TDefault {
                        p.advance() // default
                        body := p.parseBlock()
                        defaultBody = body
                } else {
                        break
                }
        }

        return &ast.MatchStmt{
                Subject: subject,
                Cases:   cases,
                Default: defaultBody,
                Span:     pos,
        }
}

// parseAssert parses: assert condition [, "message"]
func (p *Parser) parseAssert() ast.Stmt {
        pos := p.posAST()
        p.advance() // assert
        cond := p.parseExpr()
        var msg ast.Expr
        if p.peek().Kind == ast.TComma {
                p.advance() // comma
                msg = p.parseExpr()
        }
        return &ast.AssertStmt{
                Cond:    cond,
                Message: msg,
                Span:    pos,
        }
}

func (p *Parser) parseReturn() ast.Stmt {
        pos := p.posAST()
        p.expect(ast.TReturn)

        var value ast.Expr
        // If next token looks like it could be an expression value, parse it
        if p.peek().Kind != ast.TEOF && !p.isDedent() && !isBlockTerminator(p.peek().Kind) {
                value = p.parseExpr()
                // Check for multi-return: return 1, "hello"
                if p.peek().Kind == ast.TComma {
                        elts := []ast.Expr{value}
                        for p.peek().Kind == ast.TComma {
                                p.advance() // consume comma
                                if p.peek().Kind == ast.TEOF || p.isDedent() || isBlockTerminator(p.peek().Kind) {
                                        break
                                }
                                elts = append(elts, p.parseExpr())
                        }
                        value = &ast.TupleExpr{Elts: elts, Span: pos}
                }
        }

        return &ast.ReturnStmt{Value: value, Span: pos}
}

func (p *Parser) parseExprStmt() ast.Stmt {
        pos := p.posAST()
        expr := p.parseExpr()
        return &ast.ExprStmt{Expr: expr, Span: pos}
}

// ========== Expression Parsing ==========

func (p *Parser) parseExpr() ast.Expr {
        left := p.parseAssign()

        // Range expression: low..high
        if p.peek().Kind == ast.TDotDot {
                pos := p.posFromTok(p.tokens[p.pos])
                p.advance()
                high := p.parseAssign()
                return &ast.RangeExpr{Low: left, High: high, Span: pos}
        }

        return left
}

func (p *Parser) parseAssign() ast.Expr {
        left := p.parseOr()

        if p.peek().Kind == ast.TAssign {
                // Assignment
                p.advance()
                value := p.parseAssign()

                switch l := left.(type) {
                case *ast.Ident:
                        return &ast.AssignExpr{Target: left, Value: value, Span: l.Span}
                case *ast.IndexExpr:
                        return &ast.IndexAssignExpr{Obj: l.Obj, Key: l.Key, Value: value, Span: l.Span}
                case *ast.MemberExpr:
                        return &ast.MemberAssignExpr{Obj: l.Obj, Field: l.Field, Value: value, Span: l.Span}
                default:
                        p.error(p.peek(), "invalid assignment target; only variables, table indices, and member fields can be assigned to")
                        return left
                }
        }

        // Compound assignment: +=  -=  *=  /=  %=  &=  |=  ^=  <<=  >>=
        if baseOp, ok := compoundAssignOp(p.peek().Kind); ok {
                p.advance()
                rhs := p.parseAssign()
                pos := p.posFromTok(p.tokens[p.pos-1])
                value := &ast.BinOp{Op: baseOp, Left: left, Right: rhs, Span: pos}

                switch l := left.(type) {
                case *ast.Ident:
                        return &ast.AssignExpr{Target: left, Value: value, Span: l.Span}
                case *ast.IndexExpr:
                        return &ast.IndexAssignExpr{Obj: l.Obj, Key: l.Key, Value: value, Span: l.Span}
                case *ast.MemberExpr:
                        return &ast.MemberAssignExpr{Obj: l.Obj, Field: l.Field, Value: value, Span: l.Span}
                default:
                        p.error(p.peek(), "invalid compound assignment target; only variables, table indices, and member fields can be assigned to")
                        return left
                }
        }

        return left
}

// compoundAssignOp maps a compound-assignment token (+=, -=, etc.) to the
// corresponding binary operator token.  Returns false for non-compound tokens.
func compoundAssignOp(tok ast.Token) (ast.Token, bool) {
        switch tok {
        case ast.TPlusEq:
                return ast.TPlus, true
        case ast.TMinusEq:
                return ast.TMinus, true
        case ast.TStarEq:
                return ast.TStar, true
        case ast.TSlashEq:
                return ast.TSlash, true
        case ast.TPercentEq:
                return ast.TPercent, true
        case ast.TAmpEq:
                return ast.TAmp, true
        case ast.TPipeEq:
                return ast.TPipe, true
        case ast.TCaretEq:
                return ast.TCaret, true
        case ast.TShlEq:
                return ast.TLShift, true
        case ast.TShrEq:
                return ast.TRShift, true
        default:
                return 0, false
        }
}

func (p *Parser) parseOr() ast.Expr {
        left := p.parseAnd()
        for p.peek().Kind == ast.TOr {
                pos := p.posAST()
                p.advance()
                right := p.parseAnd()
                left = &ast.BinOp{Op: ast.TOr, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseAnd() ast.Expr {
        left := p.parseBitOr()
        for p.peek().Kind == ast.TAnd {
                pos := p.posAST()
                p.advance()
                right := p.parseBitOr()
                left = &ast.BinOp{Op: ast.TAnd, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseBitOr() ast.Expr {
        left := p.parseBitXor()
        for p.peek().Kind == ast.TPipe {
                pos := p.posAST()
                p.advance()
                right := p.parseBitXor()
                left = &ast.BinOp{Op: ast.TPipe, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseBitXor() ast.Expr {
        left := p.parseBitAnd()
        for p.peek().Kind == ast.TCaret {
                pos := p.posAST()
                p.advance()
                right := p.parseBitAnd()
                left = &ast.BinOp{Op: ast.TCaret, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseBitAnd() ast.Expr {
        left := p.parseEquality()
        for p.peek().Kind == ast.TAmp {
                pos := p.posAST()
                p.advance()
                right := p.parseEquality()
                left = &ast.BinOp{Op: ast.TAmp, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseEquality() ast.Expr {
        left := p.parseComparison()
        for p.peek().Kind == ast.TEq || p.peek().Kind == ast.TNeq {
                op := p.peek().Kind
                pos := p.posAST()
                p.advance()
                right := p.parseComparison()
                left = &ast.BinOp{Op: op, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseComparison() ast.Expr {
        left := p.parseShift()
        for p.peek().Kind == ast.TLt || p.peek().Kind == ast.TLe ||
                p.peek().Kind == ast.TGt || p.peek().Kind == ast.TGe {
                op := p.peek().Kind
                pos := p.posAST()
                p.advance()
                right := p.parseShift()
                left = &ast.BinOp{Op: op, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseShift() ast.Expr {
        left := p.parseAdditive()
        for p.peek().Kind == ast.TLShift || p.peek().Kind == ast.TRShift {
                op := p.peek().Kind
                pos := p.posAST()
                p.advance()
                right := p.parseAdditive()
                left = &ast.BinOp{Op: op, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseAdditive() ast.Expr {
        left := p.parseMultiplicative()
        for p.peek().Kind == ast.TPlus || p.peek().Kind == ast.TMinus {
                op := p.peek().Kind
                pos := p.posAST()
                p.advance()
                right := p.parseMultiplicative()
                left = &ast.BinOp{Op: op, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseMultiplicative() ast.Expr {
        left := p.parseUnary()
        for p.peek().Kind == ast.TStar || p.peek().Kind == ast.TSlash || p.peek().Kind == ast.TPercent {
                op := p.peek().Kind
                pos := p.posAST()
                p.advance()
                right := p.parseUnary()
                left = &ast.BinOp{Op: op, Left: left, Right: right, Span: pos}
        }
        return left
}

func (p *Parser) parseUnary() ast.Expr {
        tok := p.peek()
        switch tok.Kind {
        case ast.TPlusPlus:
                pos := p.posAST()
                p.advance()
                operand := p.parseUnary()
                return &ast.IncrDecrExpr{Op: ast.TPlusPlus, Operand: operand, Prefix: true, Span: pos}
        case ast.TMinusMinus:
                pos := p.posAST()
                p.advance()
                operand := p.parseUnary()
                return &ast.IncrDecrExpr{Op: ast.TMinusMinus, Operand: operand, Prefix: true, Span: pos}
        case ast.TMinus:
                pos := p.posAST()
                p.advance()
                operand := p.parseUnary()
                return &ast.UnaryOp{Op: ast.TMinus, Operand: operand, Span: pos}
        case ast.TNot:
                pos := p.posAST()
                p.advance()
                operand := p.parseUnary()
                return &ast.UnaryOp{Op: ast.TNot, Operand: operand, Span: pos}
        case ast.TTilde:
                pos := p.posAST()
                p.advance()
                operand := p.parseUnary()
                return &ast.UnaryOp{Op: ast.TTilde, Operand: operand, Span: pos}
        default:
                return p.parsePostfix()
        }
}

func (p *Parser) parsePostfix() ast.Expr {
        expr := p.parsePrimary()

        for !p.atEnd() {
                switch p.peek().Kind {
                case ast.TPeriod:
                        // Member access: expr.field
                        // Also handles enum variant literals: Color.Red or Result.Ok(42)
                        pos := p.posAST()
                        p.advance()
                        field := p.expect(ast.TIdent)
                        if field == nil {
                                return expr
                        }
                        // Check if this is an enum variant literal: EnumName.VariantName
                        var typeArgs []ast.TypeRef
                        enumName := ""
                        if ident, ok := expr.(*ast.Ident); ok {
                                if p.isEnumName(ident.Name) {
                                        enumName = ident.Name
                                }
                        } else if tai, ok := expr.(*ast.TypeArgIdent); ok {
                                typeArgs = tai.TypeArgs
                                if p.isEnumName(tai.Name) {
                                        enumName = tai.Name
                                }
                        }
                        if enumName != "" {
                                variantName := field.Value
                                // Check for payload: EnumName.VariantName(expr)
                                var payload ast.Expr
                                var bindVar string
                                if p.peek().Kind == ast.TLParen {
                                        p.advance() // (
                                        args, _ := p.parseArgs()
                                        p.expect(ast.TRParen)
                                        if len(args) == 1 {
                                                payload = args[0]
                                                // In match pattern context, a bare identifier
                                                // payload becomes a binding variable.
                                                if p.inMatchPattern {
                                                        if id, ok := payload.(*ast.Ident); ok {
                                                                bindVar = id.Name
                                                                payload = nil
                                                        }
                                                }
                                        } else if len(args) > 1 {
                                                p.error(p.peek(), "enum variant expects at most one payload value")
                                        }
                                }
                                if bindVar != "" {
                                        expr = &ast.EnumMatchPattern{
                                                EnumName:    enumName,
                                                VariantName: variantName,
                                                BindVar:     bindVar,
                                                Span:        pos,
                                        }
                                } else {
                                        expr = &ast.EnumLit{
                                                EnumName:    enumName,
                                                VariantName: variantName,
                                                TypeArgs:    typeArgs,
                                                Payload:     payload,
                                                Span:        pos,
                                        }
                                }
                        } else {
                                memberExpr := &ast.MemberExpr{Obj: expr, Field: field.Value, Span: pos}
                                // Check for struct literal: Module.StructName{field: val, ...}
                                if p.peek().Kind == ast.TLBrace {
                                        expr = p.parseStructLitForMemberExpr(memberExpr)
                                } else {
                                        expr = memberExpr
                                }
                        }
                case ast.TLBrack:
                        // Indexing: expr[key]
                        pos := p.posAST()
                        p.advance()
                        key := p.parseExpr()
                        p.expect(ast.TRBrack)
                        expr = &ast.IndexExpr{Obj: expr, Key: key, Span: pos}
                case ast.TQuestion:
                        // Error propagation: expr?
                        pos := p.posAST()
                        p.advance()
                        expr = &ast.ErrorPropExpr{Expr: expr, Span: pos}
                case ast.TLParen:
                        // Function call: expr(args) or funcName[int](args)
                        pos := p.posAST()
                        p.advance()
                        args, spread := p.parseArgs()
                        p.expect(ast.TRParen)
                        var typeArgs []ast.TypeRef
                        if tai, ok := expr.(*ast.TypeArgIdent); ok {
                                typeArgs = tai.TypeArgs
                                expr = &ast.Ident{Name: tai.Name, Span: tai.Span}
                        }
                        expr = &ast.CallExpr{Func: expr, TypeArgs: typeArgs, Args: args, Spread: spread, Span: pos}
                case ast.TPlusPlus, ast.TMinusMinus:
                        // Postfix increment/decrement: x++, x--
                        op := p.peek().Kind
                        pos := p.posAST()
                        p.advance()
                        expr = &ast.IncrDecrExpr{Op: op, Operand: expr, Prefix: false, Span: pos}
                default:
                        return expr
                }
        }

        return expr
}

func (p *Parser) parseArgs() ([]ast.Expr, *ast.Expr) {
        var args []ast.Expr
        var spread ast.Expr
        for p.peek().Kind != ast.TRParen && !p.atEnd() {
                if len(args) > 0 {
                        p.expect(ast.TComma)
                }
                // Check for spread syntax: ...expr (must be last argument)
                if p.peek().Kind == ast.TDotDotDot {
                        p.advance()
                        e := p.parseExpr()
                        spread = e
                        continue
                }
                args = append(args, p.parseExpr())
        }
        if spread == nil {
                return args, nil
        }
        return args, &spread
}

func (p *Parser) parsePrimary() ast.Expr {
        tok := p.peek()

        switch tok.Kind {
        case ast.TIntLit:
                p.advance()
                val := parseIntegerLiteral(tok.Value, tok, p)
                return &ast.IntLit{Value: val, Span: p.posFromTok(tok)}

        case ast.TCharLit:
                p.advance()
                val, err := strconv.ParseInt(tok.Value, 10, 64)
                if err != nil {
                        p.error(tok, fmt.Sprintf("invalid character literal: %s", tok.Value))
                        val = 0
                }
                return &ast.IntLit{Value: val, Span: p.posFromTok(tok)}

        case ast.TFloatLit:
                p.advance()
                val, err := strconv.ParseFloat(tok.Value, 64)
                if err != nil {
                        p.error(tok, fmt.Sprintf("invalid float literal: %s", tok.Value))
                        val = 0
                }
                return &ast.FloatLit{Value: val, Span: p.posFromTok(tok)}

        case ast.TStringLit:
                p.advance()
                return &ast.StringLit{Value: tok.Value, Span: p.posFromTok(tok)}

        case ast.TInterpStr:
                p.advance()
                return p.parseInterpStr(tok)

        case ast.TTrue:
                p.advance()
                return &ast.BoolLit{Value: true, Span: p.posFromTok(tok)}

        case ast.TFalse:
                p.advance()
                return &ast.BoolLit{Value: false, Span: p.posFromTok(tok)}

        case ast.TNil:
                p.advance()
                return &ast.NilLit{Span: p.posFromTok(tok)}

        case ast.TIdent:
                p.advance()
                // Check for type arguments then struct literal: TypeName[int]{field: val, ...}
                // or type arguments then member access: Option[int].Some(x)
                // IMPORTANT: Only interpret [...] as type args when the identifier
                // is a known type (struct, enum, or type keyword).  For plain
                // variable names like `x`, the `[` must be handled by parsePostfix
                // as an index expression to avoid infinite loops when the bracket
                // content is not valid type syntax.
                if p.peek().Kind == ast.TLBrack && (p.isTypeName(tok.Value) || p.isEnumName(tok.Value) || p.genericNames[tok.Value]) {
                        typeArgs := p.parseTypeArgs()
                        if p.peek().Kind == ast.TLBrace {
                                return p.parseStructLitWithTypeArgs(tok.Value, typeArgs)
                        }
                        // Type args on a plain identifier — attach to ident for later resolution
                        // (e.g., Option[int].Some(x) will be handled in postfix)
                        _ = &ast.Ident{Name: tok.Value, Span: p.posFromTok(tok)}
                        // We store type args as a "decorated" ident — the postfix handler
                        // will see the next '.' and handle it.
                        // For now, return a special node. Actually, let's just return
                        // the ident and handle type args in postfix via a wrapper.
                        return &ast.TypeArgIdent{
                                Name:     tok.Value,
                                TypeArgs: typeArgs,
                                Span:     p.posFromTok(tok),
                        }
                }
                // Check for struct literal: TypeName{field: val, ...}
                if p.peek().Kind == ast.TLBrace {
                        return p.parseStructLit(tok.Value)
                }
                return &ast.Ident{Name: tok.Value, Span: p.posFromTok(tok)}

        case ast.TLParen:
                p.advance()
                expr := p.parseExpr()
                // Check for tuple literal: (a, b, ...)
                if p.peek().Kind == ast.TComma {
                        elems := []ast.Expr{expr}
                        for p.peek().Kind == ast.TComma {
                                p.advance() // skip comma
                                if p.peek().Kind == ast.TRParen {
                                        break // trailing comma
                                }
                                elems = append(elems, p.parseExpr())
                        }
                        p.expect(ast.TRParen)
                        return &ast.TupleExpr{Elts: elems, Span: p.posFromTok(tok)}
                }
                p.expect(ast.TRParen)
                return expr

        case ast.TLBrace:
                return p.parseTableLit()

        case ast.TSpawn:
                return p.parseSpawn()

        case ast.TAwait:
                return p.parseAwait()

        case ast.TFn:
                // Anonymous function expression: fn(params) body
                return p.parseFnExpr()

        case ast.TIf:
                // If-expression: if cond body [but cond body]* [else body]
                return p.parseIfExpr()

        case ast.TMatch:
                // Match-expression: match subject case val body ... [default body]
                return p.parseMatchExpr()

        default:
                p.error(tok, fmt.Sprintf("unexpected token %s", tokDesc(tok)))
                p.advance()
                return &ast.NilLit{Span: p.posFromTok(tok)}
        }
}

func (p *Parser) parseTableLit() ast.Expr {
        pos := p.posAST()
        p.expect(ast.TLBrace)

        var entries []ast.TableEntry
        for p.peek().Kind != ast.TRBrace && !p.atEnd() {
                if len(entries) > 0 {
                        p.expect(ast.TComma)
                        if p.peek().Kind == ast.TRBrace {
                                break // trailing comma
                        }
                }
                key := p.parseExpr()
                p.expect(ast.TColon)
                value := p.parseExpr()
                entries = append(entries, ast.TableEntry{Key: key, Value: value})
        }

        p.expect(ast.TRBrace)
        return &ast.TableLit{Entries: entries, Span: pos}
}

// parseStructLit parses a struct literal: TypeName{field1: val1, field2: val2, ...}
// The type name is already consumed.
func (p *Parser) parseStructLit(typeName string) ast.Expr {
        pos := p.posAST()

        p.expect(ast.TLBrace)

        var fields []ast.StructFieldInit
        for p.peek().Kind != ast.TRBrace && !p.atEnd() {
                fname := p.expect(ast.TIdent)
                if fname == nil {
                        return nil
                }
                p.expect(ast.TColon)
                val := p.parseExpr()
                fields = append(fields, ast.StructFieldInit{
                        Name:  fname.Value,
                        Value: val,
                        Span:  p.posFromTok(*fname),
                })

                if p.peek().Kind == ast.TComma {
                        p.advance()
                }
        }

        p.expect(ast.TRBrace)

        return &ast.StructLit{
                Name:   typeName,
                Fields: fields,
                Span:   pos,
        }
}

// parseStructLitForMemberExpr parses a qualified struct literal: Module.StructName{field: val, ...}
// The member expression (e.g. shapes.Point) is already created.
func (p *Parser) parseStructLitForMemberExpr(member *ast.MemberExpr) ast.Expr {
        pos := member.Span
        typeName := member.Field

        p.expect(ast.TLBrace)

        var fields []ast.StructFieldInit
        for p.peek().Kind != ast.TRBrace && !p.atEnd() {
                fname := p.expect(ast.TIdent)
                if fname == nil {
                        return nil
                }
                p.expect(ast.TColon)
                val := p.parseExpr()
                fields = append(fields, ast.StructFieldInit{
                        Name:  fname.Value,
                        Value: val,
                        Span:  p.posFromTok(*fname),
                })

                if p.peek().Kind == ast.TComma {
                        p.advance()
                }
        }

        p.expect(ast.TRBrace)

        return &ast.StructLit{
                Name:     typeName,
                TypeExpr: member,
                Fields:   fields,
                Span:     pos,
        }
}

// parseStructLitWithTypeArgs parses a generic struct literal: TypeName[int]{field: val, ...}
// The type name and type arguments are already consumed.
func (p *Parser) parseStructLitWithTypeArgs(typeName string, typeArgs []ast.TypeRef) ast.Expr {
        pos := p.posAST()

        p.expect(ast.TLBrace)

        var fields []ast.StructFieldInit
        for p.peek().Kind != ast.TRBrace && !p.atEnd() {
                fname := p.expect(ast.TIdent)
                if fname == nil {
                        return nil
                }
                p.expect(ast.TColon)
                val := p.parseExpr()
                fields = append(fields, ast.StructFieldInit{
                        Name:  fname.Value,
                        Value: val,
                        Span:  p.posFromTok(*fname),
                })

                if p.peek().Kind == ast.TComma {
                        p.advance()
                }
        }

        p.expect(ast.TRBrace)

        return &ast.StructLit{
                Name:     typeName,
                TypeArgs: typeArgs,
                Fields:   fields,
                Span:     pos,
        }
}

func (p *Parser) parseLocalFn() ast.Stmt {
        pos := p.posAST()

        // Skip optional 'pub' (ignored for local functions).
        if p.peek().Kind == ast.TPub {
                p.advance()
        }

        p.expect(ast.TFn)

        name := p.expect(ast.TIdent)
        if name == nil {
                return nil
        }

        // Parameters
        p.expect(ast.TLParen)
        params := p.parseParams()
        p.expect(ast.TRParen)

        // Optional return type — bare type name only (no arrow, see parseReturnType).
        retType, retTypes := p.parseReturnType()

        // Parse body
        body := p.parseBlock()

        return &ast.LetStmt{
                Name: name.Value,
                Value: &ast.FnExpr{
                        Name:       name.Value,
                        Params:     params,
                        ReturnType: retType,
                        RetTypes:   retTypes,
                        Body:       body,
                        Span:       pos,
                },
                Span: pos,
        }
}

func (p *Parser) parseFnExpr() ast.Expr {
        pos := p.posAST()
        p.expect(ast.TFn)

        // Check for optional name (for self-reference)
        var name string
        if p.peek().Kind == ast.TIdent && !p.isTypeName(p.peek().Value) {
                name = p.advance().Value
        }

        // Parameters
        p.expect(ast.TLParen)
        params := p.parseParams()
        p.expect(ast.TRParen)

        // Optional return type — bare type name only (no arrow, see parseReturnType).
        retType, retTypes := p.parseReturnType()

        // Parse body
        body := p.parseBlock()

        return &ast.FnExpr{
                Name:       name,
                Params:     params,
                ReturnType: retType,
                RetTypes:   retTypes,
                Body:       body,
                Span:       pos,
        }
}

func (p *Parser) parseIfExpr() ast.Expr {
        pos := p.posAST()
        branches := []ast.IfBranch{}

        p.expect(ast.TIf)
        cond := p.parseExpr()
        body := p.parseBlock()
        branches = append(branches, ast.IfBranch{Cond: cond, Body: body, Span: pos})

        for p.peek().Kind == ast.TBut {
                butPos := p.posAST()
                p.advance()
                cond := p.parseExpr()
                body := p.parseBlock()
                branches = append(branches, ast.IfBranch{Cond: cond, Body: body, Span: butPos})
        }

        var elseBody []ast.Stmt
        if p.peek().Kind == ast.TElse {
                p.advance()
                elseBody = p.parseBlock()
        }

        return &ast.IfExpr{
                Branches: branches,
                Else:     elseBody,
                Span:     pos,
        }
}

func (p *Parser) parseMatchExpr() ast.Expr {
        pos := p.posAST()
        p.expect(ast.TMatch)
        subject := p.parseExpr()

        // Parse match arms inline (no intermediate parseBlock)
        var cases []ast.MatchCase
        var defaultBody []ast.Stmt

        // Consume indent for the match block
        if !p.consumeIndent() {
                p.error(p.peek(), "expected indented match block")
        }

        for !p.atEnd() {
                if p.isDedent() {
                        p.consumeDedent()
                        break
                }

                if p.peek().Kind == ast.TCase {
                        casePos := p.posAST()
                        p.advance()
                        p.inMatchPattern = true
                        value := p.parseExpr()
                        p.inMatchPattern = false
                        body := p.parseBlock()
                        cases = append(cases, ast.MatchCase{Value: value, Body: body, Span: casePos})
                } else if p.peek().Kind == ast.TDefault {
                        p.advance()
                        body := p.parseBlock()
                        defaultBody = body
                } else {
                        break
                }
        }

        return &ast.MatchExpr{
                Subject: subject,
                Cases:   cases,
                Default: defaultBody,
                Span:    pos,
        }
}

func (p *Parser) parseSpawn() ast.Expr {
        pos := p.posAST()
        p.expect(ast.TSpawn)

        // spawn must be followed by a function call
        if p.peek().Kind != ast.TIdent && p.peek().Kind != ast.TLParen {
                p.error(p.peek(), "expected function call after 'spawn'")
                return &ast.NilLit{Span: pos}
        }

        call := p.parsePostfix()
        if c, ok := call.(*ast.CallExpr); ok {
                return &ast.SpawnExpr{Call: c, Span: pos}
        }

        p.error(p.peek(), "expected function call after 'spawn'")
        return &ast.NilLit{Span: pos}
}

func (p *Parser) parseAwait() ast.Expr {
        pos := p.posAST()
        p.expect(ast.TAwait)

        // await expects a handle expression (identifier, member, or parenthesized expr).
        handle := p.parsePostfix()
        return &ast.AwaitExpr{Handle: handle, Span: pos}
}

// parseInterpStr parses the raw content of a TInterpStr token into an
// InterpStr AST node.  The raw value contains literal text interleaved
// with {expr} segments.  The parser splits these segments and re-tokenizes
// each expression part.
func (p *Parser) parseInterpStr(tok lex.Token) ast.Expr {
        raw := tok.Value
        segments := splitInterpSegments(raw)

        // If there are no expression parts, return a plain StringLit.
        hasExpr := false
        for _, seg := range segments {
                if seg.isExpr {
                        hasExpr = true
                        break
                }
        }
        if !hasExpr {
                return &ast.StringLit{Value: raw, Span: p.posFromTok(tok)}
        }

        node := &ast.InterpStr{Span: p.posFromTok(tok)}
        for _, seg := range segments {
                if seg.isExpr {
                        expr := p.reparseExpr(seg.text, tok)
                        if expr == nil {
                                expr = &ast.NilLit{Span: p.posFromTok(tok)}
                        }
                        node.Parts = append(node.Parts, ast.InterpStrPart{Expr: expr})
                } else {
                        node.Parts = append(node.Parts, ast.InterpStrPart{Lit: seg.text})
                }
                if len(node.Parts) == 0 {
                        break
                }
        }
        return node
}

// reparseExpr tokenizes and parses a single expression string extracted
// from inside an f-string {expr} segment.
func (p *Parser) reparseExpr(exprText string, parentTok lex.Token) ast.Expr {
        subLexer := lex.New(p.file, []byte(exprText))
        tokens, _ := subLexer.Tokenize()
        // Strip the trailing EOF token.
        if len(tokens) > 0 && tokens[len(tokens)-1].Kind == ast.TEOF {
                tokens = tokens[:len(tokens)-1]
        }
        if len(tokens) == 0 {
                return &ast.NilLit{Span: p.posFromTok(parentTok)}
        }
        subParser := New(p.file, tokens, nil, p.src)
        expr := subParser.parseExpr()
        for _, e := range subParser.Errors() {
                p.errors = append(p.errors, e)
        }
        return expr
}

// interpSegment represents one piece of an f-string: either literal text
// or an expression extracted from between { and }.
type interpSegment struct {
        isExpr bool
        text   string
}

// splitInterpSegments splits the raw f-string content into alternating
// literal and expression segments.  Brace nesting is honoured so that
// expressions may contain table literals, blocks, etc.
func splitInterpSegments(raw string) []interpSegment {
        var segs []interpSegment
        var lit strings.Builder
        i := 0

        for i < len(raw) {
                if raw[i] == '{' {
                        // Flush any accumulated literal text.
                        if lit.Len() > 0 {
                                segs = append(segs, interpSegment{isExpr: false, text: lit.String()})
                                lit.Reset()
                        }
                        // Scan for the matching '}' with depth tracking.
                        depth := 1
                        start := i + 1
                        i = start
                        for i < len(raw) && depth > 0 {
                                if raw[i] == '{' {
                                        depth++
                                } else if raw[i] == '}' {
                                        depth--
                                }
                                if depth > 0 {
                                        i++
                                }
                        }
                        exprText := raw[start:i]
                        segs = append(segs, interpSegment{isExpr: true, text: exprText})
                        if i < len(raw) {
                                i++ // skip closing '}'
                        }
                } else {
                        lit.WriteByte(raw[i])
                        i++
                }
        }
        if lit.Len() > 0 {
                segs = append(segs, interpSegment{isExpr: false, text: lit.String()})
        }
        return segs
}

// ========== Token Helpers ==========

// isConstExpr reports whether an expression is a compile-time constant
// (literal, or unary negation of a literal).
func (p *Parser) isConstExpr(e ast.Expr) bool {
        switch e.(type) {
        case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.NilLit:
                return true
        case *ast.UnaryOp:
                return true // -42, not true, etc. — validated later by the checker
        }
        return false
}

func (p *Parser) peek() lex.Token {
        if p.pos < len(p.tokens) {
                return p.tokens[p.pos]
        }
        return lex.Token{Kind: ast.TEOF}
}

func (p *Parser) advance() lex.Token {
        tok := p.peek()
        if p.pos < len(p.tokens) {
                p.pos++
        }
        return tok
}

func (p *Parser) atEnd() bool {
        return p.peek().Kind == ast.TEOF
}

func (p *Parser) expect(kind ast.Token) *lex.Token {
        tok := p.peek()
        if tok.Kind != kind {
                p.error(tok, fmt.Sprintf("expected '%s', got %s", kind, tokDesc(tok)))
                return nil
        }
        p.advance()
        return &tok
}

func (p *Parser) expectIdent(value string) {
        tok := p.peek()
        if tok.Kind != ast.TIdent || tok.Value != value {
                p.error(tok, fmt.Sprintf("expected '%s'", value))
                return
        }
        p.advance()
}

func (p *Parser) posAST() ast.Pos {
        tok := p.peek()
        return ast.Pos{
                File:   p.file,
                Line:   tok.Line,
                Col:    tok.Col,
                Offset: tok.Offset,
        }
}

func (p *Parser) posFromTok(tok lex.Token) ast.Pos {
        spanLen := 0 // 0 means "auto" (single char)
        if len(tok.Value) > 1 {
                spanLen = len(tok.Value)
        }
        return ast.Pos{
                File:    p.file,
                Line:    tok.Line,
                Col:     tok.Col,
                Offset:  tok.Offset,
                SpanLen: spanLen,
        }
}

func (p *Parser) error(tok lex.Token, msg string) {
        p.errors = append(p.errors, ParseError{
                File:   p.file,
                Line:   tok.Line,
                Col:    tok.Col,
                Offset: tok.Offset,
                Msg:    msg,
        })
}

// errorWithHelp emits a parse error with an attached "= help: ..." suggestion.
// The help string is shown beneath the error in the diagnostic render.
func (p *Parser) errorWithHelp(tok lex.Token, msg, help string) {
        p.errors = append(p.errors, ParseError{
                File:   p.file,
                Line:   tok.Line,
                Col:    tok.Col,
                Offset: tok.Offset,
                Msg:    msg,
                Help:   help,
        })
}

// errorWithSpan emits a parse error with an explicit underline span length.
// Use this when the error pertains to a multi-character token (e.g., the
// 2-character `->` arrow) so the underline covers the whole token.
func (p *Parser) errorWithSpan(tok lex.Token, spanLen int, msg string) {
        p.errors = append(p.errors, ParseError{
                File:    p.file,
                Line:    tok.Line,
                Col:     tok.Col,
                Offset:  tok.Offset,
                Msg:     msg,
                SpanLen: spanLen,
        })
}

// errorDiag emits a parse error with both a span length (for the underline)
// AND a help suggestion.  This is the most informative form.
func (p *Parser) errorDiag(tok lex.Token, spanLen int, msg, help string) {
        p.errors = append(p.errors, ParseError{
                File:    p.file,
                Line:    tok.Line,
                Col:     tok.Col,
                Offset:  tok.Offset,
                Msg:     msg,
                Help:    help,
                SpanLen: spanLen,
        })
}


// ========== Indentation Handling ==========

// consumeIndent checks for an indent increase and consumes it.
func (p *Parser) consumeIndent() bool {
        if p.indentPos < len(p.indents) {
                indent := p.indents[p.indentPos]
                // An indent token with a higher level than the current means a new block
                if indent.Level > p.indentLevel {
                        p.indentPos++
                        p.indentLevel = indent.Level
                        return true
                }
        }
        return false
}

// isDedent checks if we're at a dedent position — i.e., the current
// token's indentation level is strictly less than the block's expected level.
// We consume indent events as we encounter them so that the indentPos
// always points to the event for the current (or next) line.
func (p *Parser) isDedent() bool {
        if p.atEnd() {
                return true
        }
        curTok := p.peek()
        // Advance indentPos past any events for lines before the current token.
        for p.indentPos < len(p.indents) {
                indent := p.indents[p.indentPos]
                if indent.Line < curTok.Line {
                        // This indent event is for a line we've already passed.
                        // Consume it silently.
                        p.indentPos++
                        if indent.Level < p.indentLevel {
                                // We missed a dedent — adjust level
                                p.indentLevel = indent.Level
                        }
                } else if indent.Line == curTok.Line {
                        // This indent event is for the current line.
                        if indent.Level < p.indentLevel {
                                return true
                        }
                        return false
                } else {
                        // Future indent event
                        break
                }
        }
        return false
}

// consumeDedent consumes a single dedent token, updating the current
// indent level by exactly one step.  This is critical for correctly
// handling nested blocks: when a dedent from level 3 to level 0 is
// emitted (e.g. at EOF), each enclosing block must pop one level at a time.
func (p *Parser) consumeDedent() {
        if p.indentPos < len(p.indents) {
                indent := p.indents[p.indentPos]
                if indent.Level < p.indentLevel {
                        p.indentPos++
                        p.indentLevel = indent.Level
                }
        }
}

// ========== Helpers ==========

func isBlockStart(kind ast.Token) bool {
        switch kind {
        case ast.TIf, ast.TWhile, ast.TFor, ast.TMatch, ast.TLet, ast.TFn:
                return true
        default:
                return false
        }
}

func isBlockTerminator(kind ast.Token) bool {
        switch kind {
        case ast.TEOF, ast.TElse, ast.TBut, ast.TCase, ast.TDefault:
                return true
        default:
                return false
        }
}

func isValueStart(kind ast.Token) bool {
        switch kind {
        case ast.TIntLit, ast.TFloatLit, ast.TStringLit, ast.TTrue, ast.TFalse:
                return true
        case ast.TIdent, ast.TLParen, ast.TLBrace, ast.TLBrack:
                return true
        default:
                return false
        }
}

// prescanStructNames does a quick linear scan of tokens to find struct and enum
// declarations and collect their names. This allows the parser to recognize struct/enum
// names as valid type annotations in function parameters and return types.
func (p *Parser) prescanStructNames() {
        p.structNames = make(map[string]bool)
        p.enumNames = make(map[string]bool)
        p.genericNames = make(map[string]bool)
        p.typeParamNames = make(map[string]bool)
        for i := 0; i < len(p.tokens)-1; i++ {
                if p.tokens[i].Kind == ast.TStruct && p.tokens[i+1].Kind == ast.TIdent {
                        p.structNames[p.tokens[i+1].Value] = true
                }
                if p.tokens[i].Kind == ast.TEnum && p.tokens[i+1].Kind == ast.TIdent {
                        p.enumNames[p.tokens[i+1].Value] = true
                }
                // Scan for generic function declarations: fn Name[
                // This allows ident[type_args] to be parsed as a generic call.
                if i+2 < len(p.tokens) && p.tokens[i].Kind == ast.TFn && p.tokens[i+1].Kind == ast.TIdent && p.tokens[i+2].Kind == ast.TLBrack {
                        p.genericNames[p.tokens[i+1].Value] = true
                }
        }
        // Second pass: collect type-parameter names declared inside `[T, U, ...]`
        // brackets that appear after `fn Name` (and after `struct Name` / `enum Name`).
        // These names are valid type references WITHIN the corresponding function/
        // struct/enum body.  We collect them globally here so isTypeName() recognises
        // them at parse time; the checker enforces scoping later.
        for i := 0; i < len(p.tokens); i++ {
                // Find 'fn Name' / 'struct Name' / 'enum Name' followed by '['
                if i+2 < len(p.tokens) {
                        tok0 := p.tokens[i].Kind
                        tok1 := p.tokens[i+1].Kind
                        tok2 := p.tokens[i+2].Kind
                        if (tok0 == ast.TFn || tok0 == ast.TStruct || tok0 == ast.TEnum) &&
                                tok1 == ast.TIdent && tok2 == ast.TLBrack {
                                // Walk inside the brackets and collect identifiers.
                                depth := 1
                                j := i + 3
                        bracketLoop:
                                for j < len(p.tokens) && depth > 0 {
                                        switch p.tokens[j].Kind {
                                        case ast.TLBrack:
                                                depth++
                                        case ast.TRBrack:
                                                depth--
                                                if depth == 0 {
                                                        break bracketLoop
                                                }
                                        case ast.TIdent:
                                                // Only treat as type param if it's at bracket-depth 1
                                                // (avoids matching identifiers inside nested brackets).
                                                if depth == 1 {
                                                        p.typeParamNames[p.tokens[j].Value] = true
                                                }
                                        }
                                        j++
                                }
                                i = j // skip past the brackets
                        }
                }
        }
}

func isTypeKeyword(name string) bool {
        switch name {
        case "int", "uint", "fp", "bool", "str", "table", "gen", "void":
                return true
        default:
                return false
        }
}

// isTypeName reports whether name is a valid type annotation in the current parsing context.
// This includes built-in type keywords and any struct/enum names discovered during prescan.
func (p *Parser) isTypeName(name string) bool {
        if isTypeKeyword(name) {
                return true
        }
        if p.structNames != nil && p.structNames[name] {
                return true
        }
        if p.enumNames != nil && p.enumNames[name] {
                return true
        }
        if p.typeParamNames != nil && p.typeParamNames[name] {
                return true
        }
        return false
}

// isEnumName reports whether name is a known enum type name.
func (p *Parser) isEnumName(name string) bool {
        return p.enumNames != nil && p.enumNames[name]
}

// Errors returns all parse errors.
func (p *Parser) Errors() []ParseError {
        return p.errors
}

// parseIntegerLiteral parses an integer literal string (which may have
// 0x, 0b, 0o prefixes or underscore separators) into an int64 value.
func parseIntegerLiteral(s string, tok lex.Token, p *Parser) int64 {
        // Strip underscores for parsing
        clean := strings.ReplaceAll(s, "_", "")

        var base int
        var digits string
        if len(clean) >= 2 && clean[0] == '0' {
                switch clean[1] {
                case 'x', 'X':
                        base = 16
                        digits = clean[2:]
                case 'b', 'B':
                        base = 2
                        digits = clean[2:]
                case 'o', 'O':
                        base = 8
                        digits = clean[2:]
                default:
                        base = 10
                        digits = clean
                }
        } else {
                base = 10
                digits = clean
        }

        val, err := strconv.ParseInt(digits, base, 64)
        if err != nil {
                p.error(tok, fmt.Sprintf("invalid integer literal: %s", s))
                return 0
        }
        return val
}
