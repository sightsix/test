package ast

// Pos represents a source position with optional span length for
// multi-character source highlighting in diagnostics.
type Pos struct {
        File    string
        Line    int
        Col     int
        Offset  int
        SpanLen int // length of the span; 0 means 1 (single character)
}

func (p Pos) IsValid() bool { return p.Line > 0 }

// Node is the base interface for all AST nodes.
type Node interface {
        Pos() Pos
}

// ========== Types ==========

// Kind represents a Yilt type.
type Kind int

const (
        KindVoid   Kind = iota
        KindInt
        KindUint
        KindFp
        KindBool
        KindStr
        KindTable
        KindGen    // generic/unresolved
        KindNamed  // user-named type alias
        KindTuple  // tuple of types
        KindStruct  // struct (nominal record type)
        KindEnum    // enum (sum type with variants)
)

func (k Kind) String() string {
        switch k {
        case KindVoid:
                return "void"
        case KindInt:
                return "int"
        case KindUint:
                return "uint"
        case KindFp:
                return "fp"
        case KindBool:
                return "bool"
        case KindStr:
                return "str"
        case KindTable:
                return "table"
        case KindGen:
                return "gen"
        case KindNamed:
                return "named"
        case KindTuple:
                return "tuple"
        case KindStruct:
                return "struct"
        default:
                return "unknown"
        }
}

// TypeRef is a reference to a type, either a built-in kind or a named type.
type TypeRef struct {
        Kind    Kind
        Name    string // for KindNamed
        Span     Pos
}

// ========== Expressions ==========

type Expr interface {
        exprNode()
        Node
}

type (
        // IntLit is an integer literal.
        IntLit struct {
                Value int64
                Span  Pos
        }
        // FloatLit is a float literal.
        FloatLit struct {
                Value float64
                Span  Pos
        }
        // StringLit is a string literal.
        StringLit struct {
                Value string
                Span  Pos
        }
        // BoolLit is a boolean literal.
        BoolLit struct {
                Value bool
                Span  Pos
        }
        // NilLit represents the absence of a value.
        NilLit struct {
                Span Pos
        }
        // Ident is an identifier reference.
        Ident struct {
                Name string
                Span  Pos
        }
        // BinOp is a binary operation.
        BinOp struct {
                Op    Token
                Left  Expr
                Right Expr
                Span  Pos
        }
        // RangeExpr is a range expression: low..high (e.g., 0..10).
        RangeExpr struct {
                Low   Expr
                High  Expr
                Span  Pos
        }
        // UnaryOp is a unary operation.
        UnaryOp struct {
                Op      Token
                Operand Expr
                Span    Pos
        }
        // CallExpr is a function call.
        CallExpr struct {
                Func     Expr
                TypeArgs []TypeRef // explicit type arguments, e.g. identity[int](x)
                Args     []Expr
                Spread   *Expr    // non-nil if last arg uses ...expr spread syntax
                Span     Pos
        }
        // TypeArgIdent is an identifier with explicit type arguments: SomeType[int].
        // Used as an intermediate form during parsing; the checker resolves it
        // in the context of member access (e.g., Option[int].Some(x)) or calls.
        TypeArgIdent struct {
                Name     string
                TypeArgs []TypeRef
                Span     Pos
        }
        // IndexExpr is indexing: tab[key].
        IndexExpr struct {
                Obj  Expr
                Key  Expr
                Span Pos
        }
        // MemberExpr is member access: mod.name.
        MemberExpr struct {
                Obj   Expr
                Field string
                Span  Pos
        }
        // TableLit is a table literal: { k: v, ... }.
        TableLit struct {
                Entries []TableEntry
                Span    Pos
        }
        // TableEntry is a single entry in a table literal.
        TableEntry struct {
                Key   Expr
                Value Expr
                Span   Pos
        }
        // AssignExpr is assignment: name = expr.
        AssignExpr struct {
                Target Expr
                Value  Expr
                Span   Pos
        }
        // IndexAssignExpr is index assignment: tab[key] = value.
        IndexAssignExpr struct {
                Obj   Expr
                Key   Expr
                Value Expr
                Span  Pos
        }
        // MemberAssignExpr is member assignment: obj.field = value.
        MemberAssignExpr struct {
                Obj   Expr
                Field string
                Value Expr
                Span  Pos
        }
        // ErrorPropExpr is the ? error propagation operator.
        ErrorPropExpr struct {
                Expr Expr
                Span Pos
        }
        // SpawnExpr is the spawn expression.
        SpawnExpr struct {
                Call *CallExpr
                Span Pos
        }
        // AwaitExpr is the await expression.
        AwaitExpr struct {
                Handle Expr
                Span   Pos
        }
        // FnExpr is a function expression (anonymous or named local function).
        FnExpr struct {
                Name       string    // empty for anonymous; set for local named functions
                Params     []Param
                ReturnType *TypeRef  // nil = infer from body
                RetTypes   []TypeRef // multi-return
                Body       []Stmt
                Span       Pos
        }
        // IfExpr is an if-expression that yields a value.
        IfExpr struct {
                Branches []IfBranch
                Else     []Stmt
                Span     Pos
        }
        // MatchExpr is a match-expression that yields a value.
        MatchExpr struct {
                Subject Expr
                Cases   []MatchCase
                Default []Stmt
                Span    Pos
        }
        // InterpStr is an f-string interpolation: f"hello {name}, age {age + 1}".
        InterpStr struct {
                Parts []InterpStrPart
                Span  Pos
        }
        // InterpStrPart is one segment of an f-string: either a literal string
        // fragment or an interpolated expression.  Exactly one of Lit or Expr
        // is set.
        InterpStrPart struct {
                Lit  string // non-empty for literal parts
                Expr Expr   // non-nil for expression parts
        }
        // IncrDecrExpr is an increment/decrement expression: ++x, --x, x++, x--.
        IncrDecrExpr struct {
                Op      Token // TPlusPlus or TMinusMinus
                Operand Expr   // must be an Ident
                Prefix  bool   // true for ++x/--x, false for x++/x--
                Span    Pos
        }
        // TupleExpr is a tuple of expressions: (1, "hello").
        TupleExpr struct {
                Elts []Expr
                Span Pos
        }
        // StructLit is a struct literal: Point{x: 1, y: 2} or Pair[int]{first: 1, second: 2}.
        // When TypeExpr is set, the struct type is resolved through that expression
        // (e.g. shapes.Point{...} where TypeExpr is a MemberExpr).
        StructLit struct {
                Name      string   // struct type name (must be a registered struct)
                TypeExpr  Expr     // optional: qualified type expression (e.g. shapes.Point)
                TypeArgs  []TypeRef // explicit type arguments for generic structs
                Fields    []StructFieldInit
                Span     Pos
        }
        // StructFieldInit is a single field initialization in a struct literal.
        StructFieldInit struct {
                Name  string
                Value Expr
                Span  Pos
        }
        // EnumLit is an enum variant literal: Color.Red or Result.Ok(42) or Option[int].Some(42).
        // EnumName is the enum type name (e.g., "Color", "Result").
        // VariantName is the variant name (e.g., "Red", "Ok").
        // Payload is the optional payload expression (nil for simple variants).
        // TypeArgs are explicit type arguments for generic enums.
        EnumLit struct {
                EnumName    string
                VariantName string
                TypeArgs    []TypeRef // explicit type arguments for generic enums
                Payload     Expr      // nil for simple variants
                Span        Pos
        }
        // EnumMatchPattern is a pattern in a match case for enum variant matching.
        // Used as the Value in a MatchCase when matching enum variants.
        // EnumName and VariantName identify the variant.
        // BindVar is the optional variable name to bind the payload (e.g., "v" in `case Result.Ok(v)`).
        EnumMatchPattern struct {
                EnumName    string
                VariantName string
                BindVar     string // empty if no destructuring
                Span        Pos
        }
)

func (*IntLit) exprNode()      {}
func (*FloatLit) exprNode()    {}
func (*StringLit) exprNode()   {}
func (*BoolLit) exprNode()     {}
func (*NilLit) exprNode()      {}
func (*Ident) exprNode()       {}
func (*BinOp) exprNode()       {}
func (*RangeExpr) exprNode()    {}
func (*UnaryOp) exprNode()     {}
func (*CallExpr) exprNode()    {}
func (*IndexExpr) exprNode()   {}
func (*MemberExpr) exprNode()  {}
func (*TableLit) exprNode()    {}
func (*AssignExpr) exprNode()  {}
func (*IndexAssignExpr) exprNode()    {}
func (*MemberAssignExpr) exprNode()  {}
func (*ErrorPropExpr) exprNode() {}
func (*SpawnExpr) exprNode()   {}
func (*AwaitExpr) exprNode()   {}
func (*FnExpr) exprNode()      {}
func (*IfExpr) exprNode()      {}
func (*MatchExpr) exprNode()   {}
func (*InterpStr) exprNode()     {}
func (*IncrDecrExpr) exprNode() {}
func (*TupleExpr) exprNode()    {}
func (*StructLit) exprNode()    {}
func (*EnumLit) exprNode()      {}
func (*EnumMatchPattern) exprNode() {}
func (*TypeArgIdent) exprNode()     {}

func (n *IntLit) Pos() Pos     { return n.Span }
func (n *FloatLit) Pos() Pos   { return n.Span }
func (n *StringLit) Pos() Pos  { return n.Span }
func (n *BoolLit) Pos() Pos    { return n.Span }
func (n *NilLit) Pos() Pos     { return n.Span }
func (n *Ident) Pos() Pos      { return n.Span }
func (n *BinOp) Pos() Pos      { return n.Span }
func (n *RangeExpr) Pos() Pos   { return n.Span }
func (n *UnaryOp) Pos() Pos    { return n.Span }
func (n *CallExpr) Pos() Pos   { return n.Span }
func (n *IndexExpr) Pos() Pos  { return n.Span }
func (n *MemberExpr) Pos() Pos { return n.Span }
func (n *TableLit) Pos() Pos   { return n.Span }
func (n *AssignExpr) Pos() Pos { return n.Span }
func (n *IndexAssignExpr) Pos() Pos    { return n.Span }
func (n *MemberAssignExpr) Pos() Pos  { return n.Span }
func (n *ErrorPropExpr) Pos() Pos  { return n.Span }
func (n *SpawnExpr) Pos() Pos  { return n.Span }
func (n *AwaitExpr) Pos() Pos { return n.Span }
func (n *FnExpr) Pos() Pos    { return n.Span }
func (n *IfExpr) Pos() Pos    { return n.Span }
func (n *MatchExpr) Pos() Pos { return n.Span }
func (n *InterpStr) Pos() Pos     { return n.Span }
func (n *IncrDecrExpr) Pos() Pos { return n.Span }
func (n *TupleExpr) Pos() Pos    { return n.Span }
func (n *StructLit) Pos() Pos    { return n.Span }
func (n *EnumLit) Pos() Pos      { return n.Span }
func (n *EnumMatchPattern) Pos() Pos { return n.Span }
func (n *TypeArgIdent) Pos() Pos    { return n.Span }

// ========== Statements ==========

type Stmt interface {
        stmtNode()
        Node
}

type (
        // ConstStmt is a const declaration (can appear at top-level or in function bodies).
        ConstStmt struct {
                Name  string
                Value Expr
                Span  Pos
        }
        // LetStmt is a let/mut binding.
        LetStmt struct {
                Name     string
                Mutable  bool
                Type     *TypeRef // may be nil (inferred)
                Value    Expr
                Span      Pos
        }
        // ExprStmt is an expression used as a statement.
        ExprStmt struct {
                Expr Expr
                Span  Pos
        }
        // ReturnStmt returns from a function.
        ReturnStmt struct {
                Value Expr // may be nil
                Span   Pos
        }
        // IfStmt is an if/but/else chain.
        IfStmt struct {
                Branches []IfBranch
                Else     []Stmt
                Span      Pos
        }
        // IfBranch is one branch of an if chain.
        IfBranch struct {
                Cond Expr
                Body []Stmt
                Span  Pos
        }
        // WhileStmt is a while loop.
        WhileStmt struct {
                Cond Expr
                Body []Stmt
                Span  Pos
        }
        // ForStmt iterates over a table.
        ForStmt struct {
                Key   string
                Value string // may be empty
                Over  Expr
                Body  []Stmt
                Span   Pos
        }
        // MatchStmt dispatches on a value.
        MatchStmt struct {
                Subject Expr
                Cases   []MatchCase
                Default []Stmt // may be nil
                Span     Pos
        }
        // MatchCase is one arm of a match.
        MatchCase struct {
                Value Expr
                Body  []Stmt
                Span   Pos
        }
        // AssertStmt is an assertion: assert condition [, "message"]
        AssertStmt struct {
                Cond    Expr
                Message Expr // may be nil
                Span    Pos
        }
        // BreakStmt exits a loop.
        BreakStmt struct {
                Span Pos
        }
        // ContinueStmt continues a loop.
        ContinueStmt struct {
                Span Pos
        }
        // TupleDestructStmt destructures a tuple: let (a, b) = expr.
        TupleDestructStmt struct {
                Names   []string
                Mutable bool
                Value   Expr
                Span     Pos
        }
)

func (*ConstStmt) stmtNode()    {}
func (*LetStmt) stmtNode()     {}
func (*ExprStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()  {}
func (*IfStmt) stmtNode()      {}
func (*WhileStmt) stmtNode()   {}
func (*ForStmt) stmtNode()     {}
func (*AssertStmt) stmtNode()  {}
func (*MatchStmt) stmtNode()   {}
func (*BreakStmt) stmtNode()   {}
func (*ContinueStmt) stmtNode()     {}
func (*TupleDestructStmt) stmtNode() {}

func (n *ConstStmt) Pos() Pos    { return n.Span }
func (n *LetStmt) Pos() Pos     { return n.Span }
func (n *ExprStmt) Pos() Pos    { return n.Span }
func (n *ReturnStmt) Pos() Pos  { return n.Span }
func (n *IfStmt) Pos() Pos      { return n.Span }
func (n *WhileStmt) Pos() Pos   { return n.Span }
func (n *ForStmt) Pos() Pos     { return n.Span }
func (n *AssertStmt) Pos() Pos  { return n.Span }
func (n *MatchStmt) Pos() Pos   { return n.Span }
func (n *BreakStmt) Pos() Pos   { return n.Span }
func (n *ContinueStmt) Pos() Pos         { return n.Span }
func (n *TupleDestructStmt) Pos() Pos     { return n.Span }

// ========== Top-Level Declarations ==========

type Decl interface {
        declNode()
        Node
}

type (
        // FnDecl is a function declaration.
        FnDecl struct {
                Name       string
                TypeParams []string  // generic type parameters, e.g. [T, U]
                Params     []Param
                ReturnType *TypeRef  // nil = void
                RetTypes   []TypeRef // multi-return: e.g. fn foo() (int, str)  [bare, no arrow]
                Body       []Stmt
                Public     bool
                Extern     bool
                Span        Pos
        }
        // Param is a function parameter.
        Param struct {
                Name     string
                Type     TypeRef
                Mutable  bool
                Variadic bool   // true if declared as 'name ...Type'
                Default  Expr   // default value expression, or nil
                Span      Pos
        }
        // UseDecl is an import declaration.
        UseDecl struct {
                Module  string
                Alias   string // empty = use basename
                From    string // for "from X use Y"
                Symbols []UseSymbol
                Span     Pos
        }
        // UseSymbol is a symbol import from "from ... use ...".
        UseSymbol struct {
                Name  string
                Alias string
                Span   Pos
        }
        // FFIUseDecl imports from an ffi: module.
        FFIUseDecl struct {
                Module  string
                Alias   string
                From    string
                Symbols []UseSymbol
                Span     Pos
        }
        // ConstDecl is a top-level const declaration.
        ConstDecl struct {
                Name    string
                Value   Expr
                Span    Pos
                Mutable bool // true for `let mut x = {}` at top level — allows module-level mutable state
        }
        // StructDecl is a struct type declaration.
        StructDecl struct {
                Name       string
                TypeParams []string  // generic type parameters, e.g. [T]
                Fields     []StructField
                Public     bool
                Span       Pos
        }
        // StructField is a field in a struct declaration.
        StructField struct {
                Name     string
                Type     TypeRef
                Mutable bool
                Span     Pos
        }
        // EnumDecl is an enum type declaration.
        EnumDecl struct {
                Name       string
                TypeParams []string  // generic type parameters, e.g. [T]
                Variants   []EnumVariant
                Public     bool
                Span       Pos
        }
        // EnumVariant is a single variant in an enum declaration.
        // Name is the variant name (e.g., "Ok", "Err").
        // Payload is the optional payload type (nil for simple variants).
        EnumVariant struct {
                Name    string
                Payload *TypeRef // nil for simple variants like Red, Green
                Span    Pos
        }
)

func (*FnDecl) declNode()     {}
func (*UseDecl) declNode()    {}
func (*FFIUseDecl) declNode() {}
func (*ConstDecl) declNode()  {}
func (*StructDecl) declNode() {}
func (*EnumDecl) declNode()   {}

func (n *FnDecl) Pos() Pos      { return n.Span }
func (n *UseDecl) Pos() Pos     { return n.Span }
func (n *FFIUseDecl) Pos() Pos { return n.Span }
func (n *ConstDecl) Pos() Pos   { return n.Span }
func (n *StructDecl) Pos() Pos   { return n.Span }
func (n *EnumDecl) Pos() Pos   { return n.Span }

// ========== File ==========

// File represents a parsed Yilt source file.
type File struct {
        Path string
        Decls []Decl
}

// ========== Program ==========

// Program is the full compilation unit.
type Program struct {
        Files []*File
        Root  string // root directory
}

// ========== Token enum ==========

// Token represents a lexical token kind.
type Token int

const (
        TEOF Token = iota
        TIdent
        TIntLit
        TFloatLit
 TStringLit
        TNil

        // Keywords
        TLet
        TMut
        TFn
        TExtern
 TPub
        TUse
        TFrom
        TFor
        TIn
        TIf
        TBut
        TElse
        TWhile
        TMatch
        TCase
        TDefault
        TReturn
        TBreak
        TContinue
        TSpawn
        TAwait
        TConst
        TAssert
        TStruct
        TEnum

        // Operators
        TPlus
        TMinus
        TStar
        TSlash
        TPercent
        TAmp
        TPipe
        TCaret
 TTilde
 TLShift
        TRShift
        TEq
        TNeq
        TLt
        TLe
        TGt
        TGe
        TAssign
        TPlusEq
        TMinusEq
        TStarEq
        TSlashEq
        TPercentEq
        TAmpEq
        TPipeEq
        TCaretEq
        TShlEq
        TShrEq
        TQuestion

        // Delimiters
        TLParen
        TRParen
        TLBrace
        TRBrace
        TLBrack
        TRBrack
        TComma
        TPeriod
        TColon
        TDotDot
        TDotDotDot
        TArrow

        // Increment/decrement
        TPlusPlus
        TMinusMinus

        // Literals
        TTrue
        TFalse
        TAnd
        TOr
        TNot

        // Interpolated string (f"...")
        TInterpStr

        // Character literal 'c'
        TCharLit
)

func (t Token) String() string {
        switch t {
        case TEOF:
                return "EOF"
        case TIdent:
                return "identifier"
        case TIntLit:
                return "integer literal"
        case TFloatLit:
                return "float literal"
        case TStringLit:
                return "string literal"
        case TNil:
                return "nil"
        case TLet:
                return "let"
        case TMut:
                return "mut"
        case TFn:
                return "fn"
        case TExtern:
                return "extern"
        case TPub:
                return "pub"
        case TUse:
                return "use"
        case TFrom:
                return "from"
        case TFor:
                return "for"
        case TIn:
                return "in"
        case TIf:
                return "if"
        case TBut:
                return "but"
        case TElse:
                return "else"
        case TWhile:
                return "while"
        case TMatch:
                return "match"
        case TCase:
                return "case"
        case TDefault:
                return "default"
        case TReturn:
                return "return"
        case TBreak:
                return "break"
        case TContinue:
                return "continue"
        case TSpawn:
                return "spawn"
        case TAwait:
                return "await"
        case TConst:
                return "const"
        case TAssert:
                return "assert"
        case TStruct:
                return "struct"
        case TEnum:
                return "enum"
        case TPlus:
                return "+"
        case TMinus:
                return "-"
        case TStar:
                return "*"
        case TSlash:
                return "/"
        case TPercent:
                return "%"
        case TAmp:
                return "&"
        case TPipe:
                return "|"
        case TCaret:
                return "^"
        case TTilde:
                return "~"
        case TLShift:
                return "<<"
        case TRShift:
                return ">>"
        case TEq:
                return "=="
        case TNeq:
                return "!="
        case TLt:
                return "<"
        case TLe:
                return "<="
        case TGt:
                return ">"
        case TGe:
                return ">="
        case TAssign:
                return "="
        case TPlusEq:
                return "+="
        case TMinusEq:
                return "-="
        case TStarEq:
                return "*="
        case TSlashEq:
                return "/="
        case TPercentEq:
                return "%="
        case TAmpEq:
                return "&="
        case TPipeEq:
                return "|="
        case TCaretEq:
                return "^="
        case TShlEq:
                return "<<="
        case TShrEq:
                return ">>="
        case TQuestion:
                return "?"
        case TLParen:
                return "("
        case TRParen:
                return ")"
        case TLBrace:
                return "{"
        case TRBrace:
                return "}"
        case TLBrack:
                return "["
        case TRBrack:
                return "]"
        case TComma:
                return ","
        case TPeriod:
                return "."
        case TColon:
                return ":"
        case TDotDot:
                return ".."
        case TDotDotDot:
                return "..."
        case TArrow:
                return "->"
        case TPlusPlus:
                return "++"
        case TMinusMinus:
                return "--"
        case TTrue:
                return "true"
        case TFalse:
                return "false"
        case TAnd:
                return "and"
        case TOr:
                return "or"
        case TNot:
                return "not"
        case TInterpStr:
                return "interpolated string"
        case TCharLit:
                return "character literal"
        default:
                return "unknown"
        }
}
