# Yilt Programming Language

A fast, safe, and self-contained systems programming language with zero external dependencies.
Yilt compiles to native machine code with its own linker — no C runtime, no libc, no
external toolchain needed. Arena-based memory, tagged values, and built-in concurrency make
it ideal for systems programming, scripting, and embedded targets.

```bash
# Build from source (requires Go 1.22+)
go build -o yiltc ./cmd/yiltc/

# Compile and run
echo 'fn main() -> int
    print("Hello from Yilt!")
    return 0' > hello.yilt
yiltc hello.yilt && ./hello

# Cross-compile for any target
yiltc -t arm+macos -o myapp input.yilt
yiltc -t wasm -o app.wasm input.yilt
yiltc -t rv32+bare -o firmware input.yilt

Language Tour
Hello World
fn main() -> int
    print("Hello, World!")
    return 0
Yilt uses indentation-based blocks (4 spaces), similar to Python. No
braces or semicolons needed.
Variables & Types
fn demo()
    // Immutable bindings (default)
    let name = "Yilt"
    let version = 1.0
    let ready = true
    let count = 42
    let data = {key: "value", count: 1}

    // Mutable bindings
    let mut score = 0
    score = score + 1
Yilt has eight built-in types: int, uint,
fp (float), bool, str (string),
table (hash map), gen (generic), and
void (internal).
Functions
// Function with return type (arrow syntax)
fn add(a int, b int) -> int
    return a + b

// Return type without arrow (also valid)
fn sub(a int, b int) int
    return a - b

// Parameters without annotations (types inferred from usage)
fn double(x)
    return x * 2

// Void function (no return type needed)
fn greet(name str)
    print(f"Hello, {name}!")

// Recursive functions
fn factorial(n int) -> int
    if n <= 1
        return 1
    return n * factorial(n - 1)

fn main()
    greet("World")
    print(add(3, 4))    // 7
    print(factorial(5)) // 120
    print(double(6))    // 12
Functions are first-class values. Yilt automatically infers return
types when not annotated. Both fn foo() -> int and
fn foo() int are valid return type declarations.
String Interpolation
fn main()
    let name = "Yilt"
    let version = 1.0
    print(f"Welcome to {name} v{version}")
    print(f"2 + 3 = {2 + 3}")       // inline expressions
    print(f"debug: {true}")            // bools
    print(f"chars: {len(name)}")        // function calls
Use f"..." for interpolated strings. Expressions inside
{} are evaluated and auto-coerced to strings.
Control Flow
fn demo(x int)
    // If / but / else
    if x > 0
        print("positive")
    but x < 0
        print("negative")
    else
        print("zero")

    // While loop
    let mut i = 0
    while i < 5
        print(i)
        i = i + 1

    // For-in over tables
    let scores = {alice: 95, bob: 87, carol: 92}
    for name, score in scores
        print(f"{name}: {score}")

    // For-range (numeric)
    for i in 0..5
        print(i)  // prints 0, 1, 2, 3, 4

    // Match (switch)
    match x
        case 1
            print("one")
        case 2
            print("two")
        case 3
            print("three")
        default
            print("other")
Yilt uses but instead of else if, and
supports for i in START..END range iteration.
Tables (Hash Maps)
fn demo()
    // Creation
    let empty = {}
    let person = {name: "Alice", age: 30, active: true}

    // Access
    print(person["name"])  // "Alice"
    print(person.name)     // "Alice" (dot notation)

    // Modify
    let mut data = {count: 0}
    data["count"] = 1

    // Iteration
    for k, v in data
        print(f"{k}: {v}")

    // Methods
    let n = person.len()
    let has = person.has("name")
    print(f"count: {n}")
Tables are Yilt’s primary data structure — hash maps with string or
integer keys. They support dot notation access for known fields.
Table & String Methods
fn demo()
    let nums = {0: "a", 1: "b", 2: "c"}

    // Table methods
    nums.set(3, "d")
    let val = nums.get(0)
    let n = nums.len()
    let has = nums.has(0)
    nums.del(2)

    // String methods
    let s = "  Hello, World  "
    print(s.trim())              // "Hello, World"
    print(s.lower())             // "  hello, world  "
    print(s.upper())             // "  HELLO, WORLD  "
    print(s.substr(2, 5))        // "Hello"
    print(s.contains("Hello"))   // true
    print(s.split(", "))         // ["  Hello", "World  "]
    print("ha".repeat(3))        // "hahaha"
Error Handling
fn divide(a int, b int) -> int
    if b == 0
        error("division by zero")
    return a / b

fn safe_divide(a int, b int) -> int
    let result = divide(a, b)?
    return result

fn main()
    let result = safe_divide(10, 2)
    print(result)  // 5
The ? operator propagates errors upward. Use
error("msg") to create error values, and expr?
to early-return from the current function on error.
Concurrency
fn compute(n int) -> int
    return n * 2

fn main()
    // Spawn tasks (lightweight green threads)
    let handle = spawn compute(42)

    // Await the result
    let result = await handle
    print(result)  // 84
spawn creates a lightweight task and returns a handle.
await waits for the result.
Standard Library
fn main()
    // System
    let args = sys.args
    print(f"platform: {sys.platform}")
    print(f"cwd: {sys.cwd()}")

    // Filesystem
    let content = fs.read_text("data.txt")
    fs.write_text("out.txt", content)
    let exists = fs.exists("data.txt")

    // Path manipulation
    let joined = path.join("src", "main.yilt")
    let base = path.basename(joined)  // "main.yilt"

    // JSON
    let data = json.decode('{"key": "value"}')
    let out = json.encode(data)
The following modules are built in and require no imports:
sys, fs, path, json.
Math and string functions are available as global built-ins.

CLI Reference
Usage: yiltc [options] <input.yilt>

Options:
  -o, -output <path>   Output file path
  -t, -target <target>   Target triple (default: host)
  -O, -optimize <0-2>    Optimization level
  -v, -verbose          Verbose output with per-phase timing
  -j, -jobs <int>        Concurrent compilation jobs
  -run                  Compile and run the binary
  --emit-ir              Emit IR to stdout
  --emit-ast             Emit AST to stdout
  --check                Type-check only (no codegen)
  --emit-obj             Emit object file only
  --version              Print version
  --list-targets         List all supported targets
  -W <flags>             Warning control: all, none, or comma-separated codes
  -Werror               Treat warnings as errors
Target Examples
yiltc -t x86              # x86_64-linux-gnu (default)
yiltc -t x86+musl         # x86_64-linux-musl (static)
yiltc -t x86+windows      # x86_64-windows-msvc
yiltc -t arm              # aarch64-linux-gnu
yiltc -t arm+macos        # aarch64-macos
yiltc -t rv               # rv64-linux-gnu
yiltc -t wasm             # wasm32-unknown-unknown
yiltc -t x86+bare         # x86_64-unknown-none (bare metal)
yiltc -t rv32+bare        # rv32-unknown-none (bare metal)
Compiler Phases (verbose
output)
yiltc -v input.yilt

  yiltc 1.0.0
    input:  input.yilt
    output: input
    target: x86_64-linux-gnu
    jobs:   4
  Lexing        12.3us   47 tokens
  Parsing       8.1us     3 declarations
  Type check    245us     3 functions checked
  IR gen        15.2us     3 IR functions generated
  Optimizing    0.00us     O1
  Codegen       1.23ms     x86_64 + elf64, 3 functions compiled
Compiled input -> input (x86_64-linux-gnu, elf64) in 1.53ms

Type System
Type Annotations
fn add(a int, b int) -> int
    return a + b

fn demo()
    let x int = 42
    let y fp = 3.14
    let z str = "hello"
    let t table = {a: 1}
Type Coercion Rules



From
To
Allowed?




int
fp
Yes (implicit widening)


uint
fp
Yes (implicit widening)


int
str
Yes (in string context)


fp
str
Yes (in string context)


bool
str
Yes (in string context)


str
int
Only with explicit to_int()



Implicit Widening
// int to fp — works automatically
let result: fp = 10 / 3   // result is 3.333...

// Auto-coercion to str in string context
print(f"value: {42}")      // "value: 42"
print(f"pi is {3.14}")   // "pi is 3.14"

Diagnostic Quality
Yilt produces best-in-class error messages with rich source
context:
main.yilt:5:9: error[E0001]: cannot assign 'str' to 'int'
   |
 3 |     let result: int = "hello"
   |                      ^^^^^^^
   |
  help: remove the type annotation or change the value to an int
Key Diagnostic Features

Multi-line source context — shows the line before
and after the error
Multi-character underlines — underlines span the
entire token
Fuzzy suggestions — “did you mean ‘print’?” on
typos
Error codes — [E0001],
[W0001] for machine-readable output
Warning system — unused variables, dead functions,
unreachable code
Colored output — ANSI colors when running in a
terminal, plain text when piped
Error/warning summary footer — “aborting due to N
error(s), M warning(s)”
Help notes — actionable suggestions for fixing
errors

Warnings

Unused variables with fix suggestions
(prefix with '_' to suppress)
Dead functions
(function 'foo' is defined but never used)
Unreachable code after return/break/continue
_-prefixed bindings silently skip unused-variable
checks


Architecture
Source (.yilt)
    |
    v
  Lexer
    |  - Indentation-based tokenization
    |  - String interpolation (f"...")
    |  - Keywords, operators, literals
    |
    v
  Parser
    |  - Recursive descent
    |  - Indentation-sensitive blocks
    |  - Range syntax (0..10)
    |
    v
  Type Checker (concurrent)
    |  - Two-pass: declarations then bodies
    |  - Concurrent function body checking
    |  - Fuzzy "did you mean?" suggestions
    |  - Dead code detection
    |  - Method resolution on all types
    |  - Auto type inference
    |
    v
  IR (SSA-form)
    |  - Tagged 64-bit values
    |  - Block parameters (no phi nodes)
    |  - Table operations
    |  - Arena/SPAWN/AWAIT instructions
    |
    v
  Optimizer (single pass)
    |  - Constant folding
    |  - Dead code elimination
    |
    v
  Code Generator (concurrent)
    |  +-- x86_64   (ELF64/PE64/Mach-O64)
    |  +-- aarch64  (ELF64/PE64/Mach-O64)
    |  +-- rv64     (ELF64)
    |  +-- rv32     (ELF64)
    |  +-- wasm     (WASM)
    |
    v
  Linker (built-in)
    |  +-- ELF64
    |  +-- PE64
    |  +-- Mach-O64
    |  +-- WASM
    |
    v
  Native Binary
  (zero external dependencies)

Memory Model
Yilt uses arena-based allocation exclusively:

No garbage collector — deterministic deallocation
No malloc/free from libc — zero external dependencies
All heap objects (strings, tables, boxed floats) live in arenas
Arena push/pop for scoped lifetime management
Arena pointer kept in a dedicated register (R15 on x86-64, X28 on
AArch64)
64-bit tagged value representation for all runtime values


Supported Platforms








Architecture
Operating System
Format




x86-64
Linux (gnu/musl), Windows (msvc), macOS
ELF64 / PE64 / Mach-O64


AArch64
Linux (gnu), Android, Windows (msvc), macOS
ELF64 / PE64 / Mach-O64


RISC-V 64
Linux (gnu), bare metal
ELF64


RISC-V 32
Bare metal
ELF64


WebAssembly
—
WASM




Quick Reference
Operators








Precedence
Operator
Description




Highest
not, ~, - (unary)
Logical not, bitwise not, negation



*, /, %
Multiply, divide, modulo



+, -
Add, subtract



<<, >>
Shift left, shift right



&
Bitwise AND



^
Bitwise XOR



<, <=, >,
>=
Comparison

