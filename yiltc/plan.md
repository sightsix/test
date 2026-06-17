# Yilt — Master Implementation Plan

> **Date**: 2026-05-07 (updated 2026-05-11)
> **Module**: `github.com/yilt/yiltc`
> **Go**: 1.22
> **Backends**: x86_64, aarch64, rv64, rv32, WASM
> **Philosophy**: Zero-dependency, tagged-value runtime, no GC/no RC/no borrow checking
> **Status**: Fused from yilt + yiltc into single canonical project (2026-05-11)

---

## 0. Yilt Language Syntax Reference

> **This section is the ground truth** for Yilt syntax. All code examples in this
> document conform to these rules. When in doubt, the lexer (`internal/lex/lexer.go`)
> and parser (`internal/parse/parser.go`) are the final authority.

### 0.1 Keywords (29 total)

| Category | Keywords |
|----------|----------|
| Declaration | `let`, `mut`, `fn`, `extern`, `pub`, `use`, `from`, `const` |
| Control flow | `if`, `but`, `else`, `for`, `in`, `while`, `match`, `case`, `default`, `return`, `break`, `continue` |
| Concurrency | `spawn`, `await` |
| Logical | `and`, `or`, `not` |
| Literals | `true`, `false`, `nil` |
| Other | `assert` |

### 0.2 Variable Declarations

```yilt
let x = 1                    // immutable binding
let mut x = 1                // mutable binding
let x int = 1                // with type annotation (space, NO colon)
let mut x int = 1            // mutable with type annotation
let (a, b) = tuple_fn()      // tuple destructuring
NO var keyword. Only let
(immutable) and let mut (mutable). Type annotations use a
space before the type name: let x int = 1, NOT
let x: int = 1.
0.3 Function Declarations
fn main()                             // no return type
fn add(x, y)                          // untyped params
fn add(x int, y int) int              // typed params, bare return type
fn add(x int, y int) -> int           // arrow return type (also valid)
fn multi() -> (int, str)              // tuple return (only with ->)
fn no_ret() void                      // explicit void return
pub fn exported(a int) int            // public function
extern fn c_func(a int)               // FFI declaration (no body)
Parameters: name type, NOT name: type.
Return type: bare name after ) or -> type.
pub comes BEFORE fn. extern comes
AFTER fn.
0.4 Operators








Category
Operators
Notes




Arithmetic
+ - * /
%
Standard


Bitwise
& \| ^ ~
<< >>
& and \| are bitwise, NOT logical


Comparison
== != < <=
> >=
!= for not-equal (NOT <>)


Logical
and or not
Keywords, NOT
&&/\|\|/!


Assignment
= += -= *=
/= %= &= \|=
^= <<= >>=
11 operators


Increment
++ --
Prefix and postfix


Error propagation
?
Postfix: result = may_fail()?


Member/Index
. [] ()
Access, indexing, call



NOT supported: ** (power),
\|> (pipe), ?: (elvis), ??
(null-coalesce), ... (spread), &&,
||, !.
0.5 Control Flow
If/else (indentation-based blocks, no braces):
if x > 10
    print("big")
but x > 5                     // "but" = else-if (NOT "else if")
    print("medium")
else
    print("small")
While loop:
while x > 0
    x = x - 1
For loop (iterator-based, NO range syntax):
for k in items                // iterate keys
for k, v in items             // iterate key-value pairs
No C-style for (init; cond; incr). No
for i in 0..10 range syntax.
Match (single value per case, NO comma-separated
patterns):
match x
    case 1
        print("one")
    case 2
        print("two")
    case "hello"
        print("greeting")
    default
        print("other")
Break/continue:
break                         // exit current loop
continue                      // skip to next iteration
0.6 Composite Types
Tables (hash maps):
{}                            // empty table
{"name": "Alice", "age": 30}  // string keys
{1: "one", 2: "two"}          // int keys
{1: "one", "two": 2}          // mixed key types
Keys are expressions (strings, ints, computed). NOT bare
identifiers.
No array literal syntax. [1, 2, 3] does
NOT exist. [] is used only for type annotation sugar
(let arr int[] = ...) and indexing
(arr[0]).
No struct/enum/record syntax. These are future
features not yet in the parser. UPDATED:
struct and enum are now fully supported
(table-backed sugar). See Section 6.1 (structs) and Section 6.2
(enums).
0.7 Strings and Characters
"hello world"                 // regular string with \n, \t, \\, \", \0, \xNN
f"hello {name}, age {age}"    // interpolated f-string
'c'                           // character literal (integer value)
Comments: // single-line and /* block */
(with nesting support). No raw strings (r"..."), no
triple-quoted strings.
0.8 Imports
use std::math                 // full module import
use std::math as m            // module alias
from std::math use sin        // selective import
from std::math use sin, cos   // multiple symbols
from std::math use sin as s   // selective with alias
NO import keyword. Use use
or from ... use.
0.9 Assertions and Constants
assert x > 0                  // keyword, not a function
assert x > 0, "x must be positive"   // with message

const PI = 3.14               // constant declaration (top-level or local)
0.10 Closures and Nested
Functions
let add = fn(a, b)            // anonymous function
    return a + b

fn counter()
    let count = 0
    return fn()               // nested function (closure capture pending)
        count = count + 1
        return count
0.11 Concurrency
let handle = spawn compute(42)   // spawn a goroutine
let result = await handle        // await result
0.12 Error Propagation
let result = may_fail()?     // propagate error if result is Err tag
0.13 Integer Literals
42              // decimal
0xFF            // hexadecimal
0b1010          // binary
0o777           // octal (0o prefix)
1_000_000       // numeric separators (underscores)
0.14 Built-in Type Keywords
int, uint, fp,
bool, str, table,
gen, void
0.15 Features NOT Yet
Implemented
These are planned but NOT in the lexer/parser and should NOT appear
in code examples:







Feature
Status




var declaration
Not a keyword; use let/let mut


defer statement
Not a keyword; not parsed


type alias declaration
Not a keyword; not parsed


enum declaration
DONE — full pipeline: lexer/parser/checker/codegen
(table-backed)


struct declaration
DONE — full pipeline: lexer/parser/checker/codegen
(table-backed)


trait / impl blocks
Not keywords; not parsed


&& / || operators
Explicitly rejected; use and/or


! (logical not)
Not for logical; use not keyword


else if chain
Use but instead


import keyword
Use use or from ... use


Ternary cond ? a : b
? only for error propagation


Pipe \|>
\| is bitwise OR only


Elvis ?: / null-coalesce ??
Not implemented


Spread ...args
DONE — parsed in call args, validated for variadic
fns


Range 0..10
Not implemented; .. token used for range loops


Power **
Not implemented


Array literal [1,2,3]
Not implemented


Raw string r"..."
Not implemented


<> not-equal
Not implemented; use !=




1. Design Philosophy:
Ownership Without GC
Yilt deliberately rejects garbage collection, reference counting, and
borrow checking. Instead it uses an ownership-of-return
memory model:
1.1 Core Principles

Values are moved, not copied. When a tagged
value is assigned to a new name, the old binding is invalidated
(use-after-move is a compiler error).
Return values transfer ownership to the caller.
Functions return ownership; the caller becomes the sole owner. There is
no shared ownership.
Heap allocations live in arenas. Within a
function, all heap allocations (strings, tables, arrays) are tracked.
When the function returns, the entire arena can be released in O(1). No
per-object free().
Scope-based lifetime. Variables live until the
end of their enclosing block. When a block exits, any owned heap memory
is reclaimed. No dangling pointers because the compiler ensures no
references escape.
Escape analysis determines lifetime. If a heap
allocation escapes a function (returned, stored in a table, captured by
a closure), it is promoted to the caller’s arena. Non-escaping
allocations stay in the function’s local arena.
No cyclic structures needed. Because ownership
is linear, cyclic data structures are naturally prevented. Tables can
reference each other, but the compiler tracks ownership and can detect
potential cycles at compile time.

1.2 Comparison with Other
Models










Feature
Yilt
Rust
Go
C




GC
No
No
Yes
No


RC
No
No
No
No


Borrow check
No
Yes
No
No


Ownership
Implicit
Explicit
None
Manual


Free
Arena reset
Drop trait
GC handles
Manual


Cycles
Blocked
Weak refs
Allowed
Manual


Runtime cost
O(1) arena
O(1) drop
O(gc pause)
O(free)



1.3 Implementation in the
Compiler
The ownership model is enforced through three compiler passes:

Ownership Analysis (new pass, after type
checking): Walks the AST and assigns each value an “ownership slot” —
the scope that owns it. Detects use-after-move, double-move, and escape
violations.
Escape Analysis (integrated into lowering):
Determines whether a heap allocation’s lifetime exceeds its function
scope. Promoted allocations are registered in the caller’s
arena.
Arena Pass (integrated into codegen): Before
each function call, the codegen saves the current arena cursor. After
the call, the arena is restored. This is already partially implemented
via OpArenaPush/OpArenaPop.

1.4 Arena
Allocator Design (Current — Already Implemented)
+----------------------------------------------+
|  Arena Chunk (4 MB mmap)                     |
|  +------------------------------------------+ |
|  | cursor --> alloc alloc alloc ...          | |
|  | 8-byte aligned bump allocator              | |
|  +------------------------------------------+ |
|  When chunk full, mmap another 4 MB            |
|  arena_push() saves cursor position            |
|  arena_pop() restores cursor                   |
+----------------------------------------------+
Key constraint: Individual free() is a no-op. Memory
is reclaimed in bulk via arena_pop(). This is by design — arena
allocation is O(1) with zero fragmentation.

2. Current State Assessment
2.1 What’s Done (Production
Quality)









Component
File(s)
Lines
Status




Lexer
internal/lex/lexer.go, _test.go
~893
Complete — all 29 keywords, f-strings,
hex/bin/octal, escapes, numeric separators, Unicode, nested block
comments


AST
internal/ast/ast.go
~738
Complete — node types for all parsed constructs,
closures, spawn/await, error propagation


Parser
internal/parse/parser.go
~1653
Complete — Pratt parser, indentation-based blocks,
all declarations + expressions, but chains


IR
internal/ir/ir.go
~356
Complete — Unified IR, Builder, VReg/Label
types


IR Optimizations
_future/ir/ (cfold, dce, inline, loop)
~1,905
Ported but not integrated — requires adaptation to
yiltc’s SSA IR format (see _future/)


Checker
internal/check/checker.go, _test.go,
fuzzy.go
~3035+
Substantial — 56+ unit tests, fuzzy “did you mean?”
suggestions, scope management, unused vars (warning with _
prefix convention), match exhaustiveness, const expr validation, cyclic
import detection, break/continue validation, parameter defaults, generic
param validation, return type inference from nested blocks, unreachable
code detection, type annotations. Several gaps remain (see Section
2.2).


Diagnostics
internal/diag/diag.go, _test.go
~635
Complete — Error codes (E0001),
multi-character underlines, multi-line context, summary footer,
warnings, suggestion helper (= help: ...), ANSI-colored
output, non-TTY fallback


x86_64 Codegen
internal/codegen/x86_64/ (~4 files)
~2500+
Complete — Machine code emission, register
allocation, tagged value arithmetic (NoFold for mutable vars), proper
branch truthiness (tag mask), all opcodes dispatched


ELF64 Linker
internal/link/elf64/elf64.go
~1424
Complete — ELF64 with program headers, symbol
resolution, relocation processing, BSS support, SHT_REL parsing,
overflow checks, undefined symbol reporting, rodata relocations


Mach-O64 Linker
internal/link/macho64/macho64.go
present
Stub — macOS output format (not yet
functional)


PE64 Linker
internal/link/pe64/pe64.go
present
Stub — Windows output format (not yet
functional)


WASM Linker
internal/link/wasmobj/wasm.go
present
Stub — WASM object format (not yet functional)


C Runtime
internal/runtime/cruntime/runtime.c
~1849
Complete — arena allocator, syscall wrappers, math,
string/table/array ops, file I/O, path ops, JSON, f-string, cast,
closure stubs, struct stubs


Puregen
internal/runtime/puregen_runtime.go
~4916
Complete — Go runtime for testing, all
registrations, backward-compatible aliases


Runtime glue
internal/runtime/ (8+ files)
~6000+
Complete — values.go, tables.go, stdlib.go,
puregen.go, arena.go, strings.go, gen.go, runtime.go


CLI
cmd/yiltc/main.go
~3178
Complete — Colored phase headers with timing,
-W/-Werror flags, ANSI-aware TTY gating,
success/error footer


Target
internal/target/target.go
present
Complete — Target platform abstraction


Test suite
internal/testsuite/ + 128 .yilt files
~128+
Complete — 128 integration tests across basic/
(14), advanced/ (19), functions/ (14), stdlib/ (12), types/ (11),
negative/ (58) categories



2.2 What’s Partially Done








Component
Status
Gap




f-string interpolation
Full pipeline: lexer/parser/lower/codegen/C runtime/puregen
Works end-to-end


For-loop (iterator for k in expr)
Full pipeline support
Only key-value iteration over tables; range 0..n not
yet


Closures
AST + parsing + lowering exist
Capture semantics not implemented (captured vars not stored in
closure struct); runtime stubs panic


Error propagation (?)
AST + parsing + lowering exist
Runtime behavior depends on error tag support



2.3 What’s NOT Yet
Implemented (Future Features)







Feature
Notes




defer statement
Not a keyword; not in lexer/parser; deferred execution not
supported


enum declarations
DONE — full pipeline: lexer/parser/checker/codegen
(table-backed sugar)


struct declarations
DONE — full pipeline: lexer/parser/checker/codegen
(table-backed sugar)


trait / impl blocks
Not keywords; not in lexer/parser; no interface system


type alias declarations
Not a keyword; not in lexer/parser


Ownership analysis pass
DONE — basic move/use-after-move + control flow
merge (if/else/match/while)


Range syntax (0..n)
DONE — for i in 0..n,
TDotDot token, RangeExpr,
forRangeStmt


Array literal syntax ([1,2,3])
Not in parser; [] used only for type sugar and
indexing


Spread syntax (...args)
DONE — parsed, validated for variadic
functions


Variadic functions
DONE — full pipeline (parser/checker/lowerer,
table-backed)


Power operator (**)
Not in parser


Pipe operator (\|>)
Not in parser; \| is bitwise OR only


Ternary (? :)
Not in parser; ? is only for error propagation


Elvis (?:) / null-coalesce (??)
Not in parser


aarch64/rv64/rv32 backends
Stub files exist, no instruction emission


Standard library modules
Builtins exist in puregen/stdlib.go; no .yilt stdlib
files yet




3. Memory Model — Detailed
Design
3.1 Tagged Value Layout
(64-bit)
+----------------------------------------------------+
| 63    56 | 55                                        0 |
|  TAG     | PAYLOAD (56 bits, sign-extended for int)    |
+----------------------------------------------------+
Tags (single canonical source in
internal/types/types.go):
TagNone  = 0x00  TagInt   = 0x01  TagBool  = 0x02
TagFloat = 0x03  TagStr   = 0x04  TagTable = 0x05
TagNil   = 0x06  TagArray = 0x07  TagErr   = 0x08
TagFn    = 0x09
3.2 Heap Object Layouts
String (TagStr):
+----------+----------------------------------------+
| len: u64 | data: [len bytes of UTF-8, NOT NUL-term] |
+----------+----------------------------------------+
Array (TagArray):
+----------+----------+----------+----------+
| len: u64 | elem[0]  | elem[1]  | ...      |
+----------+----------+----------+----------+
  Each elem is a 64-bit tagged value.
Table (TagTable):
+----------+----------+----------+----------+
| len: u64 | key0     | val0     | ...      |
+----------+----------+----------+----------+
  Linear scan for keys. For large tables, the arena-based approach
  means we rebuild rather than resize in-place.
3.3 Ownership Rules
(Compiler-Enforced)
Rule 1: Linear ownership
  Each value has exactly one owner at any time.
  After `let b = a`, variable `a` is dead.

Rule 2: Move on assignment
  `let b = a`  ->  `a` is invalidated (use-after-move error)
  `let mut b = a` ->  `a` is invalidated

Rule 3: Copy for primitives
  Int, Float, Bool, Nil are implicitly copied (cheap, inline in 64 bits).
  Str, Table, Array, Fn are moved (pointer semantics).

Rule 4: Borrow via temporary
  Functions can temporarily borrow a value for read-only access.
  `foo(x)` does not consume `x` if `x` is a primitive.
  For heap types, the callee gets a borrowed reference that the
  compiler tracks.

Rule 5: Return = ownership transfer
  `return table` -> caller owns the table, callee loses it.
  `return arr[0]` -> caller owns a copy of the element (if primitive)
    or a moved reference (if heap type).

Rule 6: Arena scoping
  Within a function body, all heap allocs go to the function's arena.
  On return, the arena is reset. No leaks possible.
  If a value escapes (returned, stored in a global/table), it is
  promoted to the parent's arena.
3.4 Escape Analysis Rules
A value escapes a function if: 1. It is returned
from the function 2. It is stored in a table or array that outlives the
function 3. It is captured by a closure 4. It is assigned to a global
variable
Escaped values are allocated in the caller’s arena,
not the callee’s. This ensures that: - Short-lived allocations (local
strings, temp tables) are reclaimed at function exit - Long-lived
allocations (returned values, globals) live in the appropriate scope
3.5 No-Free Design Rationale
Traditional garbage collectors add: - Pause times (STW) - Memory
overhead (GC roots, card tables, mark bits) - Implementation complexity
(generational, concurrent, compacting)
Reference counting adds: - Per-object overhead (ref count field) -
Cycle detection (weak references, trial deletion) - Atomic operations
for thread safety - Destructor ordering complexity
Borrow checking adds: - Complex lifetime annotations in the type
system - Fight with the borrow checker on common patterns - Steep
learning curve
Yilt’s arena model adds: - Zero runtime overhead —
just a bump pointer - O(1) deallocation — restore a
saved pointer - No fragmentation — linear allocation -
No annotations — compiler handles everything
The trade-off is that some patterns (graphs, observer patterns)
require creative use of indices instead of raw pointers. This is
acceptable for Yilt’s target domain (systems programming, scripting,
data processing).

4. Phase 1:
Hardening (Make What We Have Work End-to-End)
Goal: Produce a working executable from a simple
Yilt program.
4.1 Fix Runtime
Registration Mismatch — DONE
Problem: The puregen runtime registered functions
with y_ prefix but the x86_64 codegen emitted calls to
yilt_ prefix.
Fix: Standardized on yilt_ prefix
everywhere.

puregen_runtime.go: Renamed all 67
RegisterRuntime calls to yilt_
prefix
puregen_runtime.go: Added 32 new runtime
function registrations
puregen_runtime.go: Kept all 67 old
y_ names as backward-compatible aliases
x86_64.go:
Audited all 78 emitRuntimeCall targets — all use
yilt_
Cross-verified: 90/90
codegen calls match both C runtime exports and puregen
registrations

4.2 Implement
Missing C Runtime Functions — DONE

yilt_fstring
/ yilt_fstring_part / yilt_fstring_expr —
f-string builder
yilt_str_lt
— string less-than comparison
yilt_print —
print tagged value (int, float, str, bool, nil, table, array,
err)
yilt_println
— print + newline
yilt_input —
read line from stdin
yilt_panic —
print error message and exit
yilt_assert
— conditional panic
yilt_cast —
type cast (int to float, float to int, str to int, etc.)
49+ new C runtime
functions implemented total
runtime.c compiles with
gcc -nostdlib -static -Wall -Wextra — zero
warnings

4.3 Tagged Value Fixes — DONE

Fixed
emitCmp to produce tagged booleans
(TAG_BOOL=2) instead of raw 0/1
Fixed OpNot
codegen to produce tagged booleans
Fixed
OpBitNot to preserve tag byte (was inverting entire 64 bits
including tag)
Fixed bitwise ops (And,
Or, Xor, Shl, Shr) to use emitTaggedArith/emitRestoreTag
Fixed Branch codegen to
mask off tag byte before truthiness test
Added NoFold
field to IR instructions for mutable variable protection
Fixed short-circuit eval
(and/or) using Or+Copy+NoFold
approach

4.4 End-to-End Smoke Tests —
PASSING

print.yilt —
compiles and runs, outputs tagged values correctly
variables.yilt — compiles and
runs
arithmetic.yilt — basic arithmetic
operations
while_loop.yilt — while loops with
break/continue
for_loop.yilt — iterator-based for
loops
if_else.yilt
— conditional branching
match.yilt —
pattern matching
short_circuit.yilt —
and/or short-circuit evaluation
bitwise_ops.yilt — bitwise AND, OR, XOR, NOT,
shifts
break_continue.yilt — loop control
flow


5. Phase 2: Ownership
Analysis (Compiler Pass)
5.1 Ownership Pass
Architecture
New package: internal/ownership/
ownership/
+-- ownership.go    -- main pass: walk AST, assign ownership slots
+-- escape.go       -- escape analysis: determine if values outlive scope
+-- ownership_test.go
5.2 Ownership Slot Types
const (
    SlotLocal    = iota // owned by a local variable (freed at block exit)
    SlotParam         // owned by a function parameter (freed at function exit)
    SlotReturn        // owned by the caller (transferred on return)
    SlotGlobal        // owned for program lifetime (never freed)
    SlotBorrowed      // temporarily borrowed (read-only access)
)
5.3 Ownership Flow Analysis
For each variable, track: 1. Definition: Where was
it first assigned? 2. Transfers: Was it moved to
another variable? 3. Borrows: Was it temporarily
referenced? 4. Death: At what point is it no longer
accessible?
5.4 Move Semantics
When the checker detects a move:
let a = {"k": 1, "v": 2}    // a owns the table
let b = a                    // MOVE: a is invalidated
print(a)                     // ERROR: use after move
print(b)                     // OK: b owns the table
The ownership pass emits diagnostics: -
error: use of moved value 'a' -
error: use of moved value 'a' after move to 'b'
5.5 Escape Promotion
When a local heap value escapes:
fn make_table()
    let t = {}           // local arena
    t["key"] = 42        // t is local
    return t             // ESCAPE: promote to caller's arena
The escape analysis pass: 1. Scans the function body for all
return statements 2. Checks which local heap values are
returned 3. Emits OpAlloc calls into the caller’s arena
instead of the callee’s 4. Updates the lowering pass to use the correct
arena for escaped values
5.6 Implicit Copy for
Primitives
Tagged int, float, bool, nil are 64-bit values stored inline. These
are implicitly copied (no ownership transfer needed).
Tagged str, table, array, fn carry heap pointers. These follow
ownership rules (moved, not copied).
5.7 Integration Points

After check (type checking): Ownership
analysis runs on the checked AST.
In lower: The lowering pass uses
ownership information to decide which arena to allocate into.
In codegen: The codegen emits arena
push/pop around function calls.


6. Phase 3: Type System
Completion
6.1 Struct Support —
DONE (Table-Backed Sugar)
Structs are implemented as syntactic sugar over tables.
Point{x: 10, y: 20} creates a table with string keys
"x" and "y". Field access p.x is
lowered to table_get(p, "x"). This approach requires zero
additional codegen or runtime support.

Lexer/parser:
struct keyword, field declarations with mut
per-field
Struct construction:
Point{x: 1, y: 2} — creates tagged-string-key
table
Field access:
p.x —
table_get(p, y_str_new("x", 1))
Mutable fields:
let mut q = Point{x: 1, y: 2}; q.x = 42
Nested structs:
Rect{origin: Point{x: 5, y: 10}, size: Point{x: 100, y: 50}}
Structs in tables:
points["a"] = make_point(1, 2)
Struct-returning
functions:
fn make_point(a, b) → return Point{x: a, y: b}
Missing field
initialization: compiler error (not just warning)
Unknown field error with
“did you mean?” suggestion
Immutable field mutation:
compiler error
Method dispatch:
p.distance(other) — DONE (convention-based)
Struct equality (field-by-field
comparison) — not yet
Dedicated struct layout (contiguous
memory, offset-based access) — future optimization

6.2 Enum Support — DONE
(Table-Backed Sugar)
Enums are implemented as syntactic sugar over tables, consistent with
structs. Color.Red creates a table {"_v": 0}
(variant index as integer). Result.Ok(42) creates a table
{"_v": 0, "_p": 42} (variant index + payload). Match on
enums is optimized to compare the _v field (O(1) per
case).

Lexer/parser:
enum keyword, variant declarations with optional
payloads
Simple enums:
enum Color\n    Red\n    Green\n    Blue
Enum variants with
payloads:
enum Result\n    Ok(int)\n    Err(str)
Pattern matching on enum
variants (optimized _v field comparison)
Enum exhaustiveness
checking (warns on missing variants without default)
IR representation:
table-backed (reuses table opcodes, zero new runtime)
Checker: payload type
validation, unknown variant/type errors
Checker bugs fixed:
checkEnumLit dead code, exhaustiveness logic inversion
Pattern destructuring in
match arms: case Result.Ok(x) binding payload to
variable
Enum equality (field-by-field
comparison)
Dedicated enum layout (contiguous
memory, offset-based access) — future optimization

6.3 Trait /
Interface System (Not Yet Implemented)
These require adding trait/impl to the
lexer and parser first:

Trait declaration:
trait Stringable { fn to_str() str }
Trait implementation:
impl Stringable for MyType { ... }
Dynamic dispatch: call through trait
object
Trait bounds on
generics

6.4 Generics (Not Yet
Implemented)

Generic function declarations:
fn identity(x T) T (syntax TBD)
Monomorphization at compile
time
Generic structs and
enums
Where clauses / trait
bounds

6.5 Multi-Return Values
(Partially Working)
Tuples work via return (1, "hello") and
let (a, b) = tuple_fn() destructuring. Full support
needs:

IR representation: tuple as array of
tagged values
Partial application / ignore:
let (a, _, c) = ...

6.6 Variadic Functions —
DONE (Table-Backed)
Variadic parameters are collected into an int-keyed table at the call
site. The variadic param is typed as table inside the
function body, with integer keys 0, 1, 2, … for each extra argument.

Parser support:
fn sum(nums ...int) int and spread ...expr in
calls
Declaration: variadic
must be last param, cannot have default value
Calling convention: extra
args packed into int-keyed table
Spread syntax:
foo(1, 2, ...args) — validated for variadic functions
only
Checker: variadic arg
type checking, spread validation
Lowerer: packs extra args
/ passes empty table / handles spread


7. Phase 4: Additional
Backend Targets
Design principle: All backends are self-contained.
The compiler generates machine code directly – NO external assembler, NO
external linker, NO gcc, NO clang, NO system toolchain of any kind. This
is the entire point of Yilt.
7.1 aarch64 Backend (Stub)

Register allocation (x0-x7 for args,
x19-x28 callee-saved)
Instruction emission for all
opcodes
Tagged value encoding (same 64-bit
layout)
Runtime call convention
(AAPCS64)
C runtime: aarch64 syscall wrappers
(different register/ABI)

7.2 RISC-V 64-bit Backend
(Stub)

Register allocation (a0-a7 for args,
s0-s11 callee-saved)
Instruction emission
Runtime call convention
(LP64D)
Linker: ELF64 RISC-V
output

7.3 RISC-V 32-bit Backend
(Stub)

32-bit tagged values (modified
layout: 24-bit tag + 32-bit payload)
32-bit register usage
Instruction emission
Runtime call convention
(ILP32D)

7.4 WASM Backend (Stub)

WASM instruction mapping for all
opcodes
Tagged value encoding in WASM linear
memory
Import/export for runtime
functions
WASM module structure (.wasm binary
output)


8. Phase 5: Standard Library
8.1 Built-in Functions
(Currently Available)
The following are registered in puregen_runtime.go and
stdlib.go:
I/O: print, println,
input, panic
String methods: str.len,
str.contains, str.find,
str.replace, str.split, str.trim,
str.upper, str.lower,
str.starts_with, str.ends_with,
str.chars, str.to_int,
str.to_fp
Table methods: table.len,
table.keys, table.values,
table.has, table.remove,
table.clear
Math builtins: abs, min,
max, floor, ceil,
round, sqrt, pow
Type conversion: int(),
fp(), str(), bool() (via
cast)
File I/O: read_file,
write_file, append_file,
file_exists, delete_file
Path operations: path_join,
path_dir, path_base, path_ext
JSON: json_encode,
json_decode
System: sys.platform,
sys.argv, sys.env, sys.cwd,
sys.exit
8.2 Future stdlib Modules
(Planned)
std/
+-- math.yilt          -- additional math: random, hash, crc32
+-- fmt.yilt           -- advanced formatting
+-- os.yilt            -- environment, args, cwd, platform
+-- fs.yilt            -- enhanced file operations
+-- iter.yilt          -- iterator protocol, map/filter/reduce
+-- sort.yilt          -- sorting algorithms
+-- test.yilt          -- testing framework
+-- collections.yilt   -- List, Set, Queue, Stack, HashMap
8.3 Iterator Protocol (Future)
// Any type implementing `next()` can be iterated with `for`
fn next() -> (val, bool)     // (value, has_more)

9. Phase 6: Tooling
9.1 Language Server (LSP)

Go-based LSP server using gopls
patterns
Completions (keywords, functions,
types, variables)
Go to definition
Find all references
Hover (type
information)
Diagnostics (errors, warnings from
checker)
Code formatting (auto-indent,
consistent style)
Semantic highlighting

9.2 Formatter

Indentation rules (4
spaces)
Line length limits
Import grouping/sorting
Trailing whitespace
removal
Blank line
normalization

9.3 Package Manager

Module resolution:
use std::math
Lock file:
yilt.lock
Registry: yiltpkg
(future)

9.4 REPL

Incremental compilation
Expression evaluation
Auto-print last expression
value
Type display for evaluated
expressions

9.5 Debugger

Source-level debug info
(DWARF)
Source maps for WASM
Variable inspection at
breakpoints
Call stack tracing


10. Phase 7: Optimizations
10.1 Already Implemented

Constant folding
(_future/ir/cfold.go) — awaiting IR adaptation
Dead code elimination
(_future/ir/dce.go) — awaiting IR adaptation
Loop optimization
(_future/ir/loop.go) — awaiting IR adaptation
Inlining
(_future/ir/inline.go) — awaiting IR
adaptation

10.2 Future Optimizations

Register allocation
improvement: Replace linear scan with graph
coloring
Tail call
optimization: Convert tail calls to jumps (reduce stack
usage)
Strength reduction:
Replace expensive ops with cheaper equivalents
Common subexpression
elimination: Share computed values
String interning:
Deduplicate string constants at compile time
Inline caching:
Cache polymorphic dispatch results
Bytecode shrinking:
Use smaller instruction encodings where possible


11. Defect Audit — Completed
Fixes
11.1 Tier
0: Must Fix Before Any Real Program Runs (ALL DONE)








#
Item
Status




1
Runtime name mismatch (y_ vs yilt_)
DONE


2
Missing C runtime functions (print, fstring, etc.)
DONE (49+ functions)


3
Missing puregen registrations
DONE (32 registrations)


4
Cross-verify all symbols (codegen, C runtime, puregen)
DONE (90/90)


5
Tag value consistency verification
Verified


6
Unified IR (no dual system)
DONE
