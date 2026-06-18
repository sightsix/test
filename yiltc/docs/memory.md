# Yilt Current Contract

Yilt is a small indentation-based language with its own **built-in IR, codegen, and linker** (zero external dependencies) and a deterministic arena/region runtime.

For a guided overview, start at [docs/index.md](index.md). For internal runtime symbols, see [docs/native.md](native.md).

## What Yilt Feels Like
- Small and strict rather than broad and magical.
- Value-oriented by default.
- Explicit about mutation, imports, and visibility.
- Portable in its generated output across multiple targets.
- Built around a few strong primitives with an integrated standard library.

## Syntax
- Indentation defines blocks.
- Core statements: `let`, `fn`, `use`, `pub`, `if`, `but`, `else`, `while`, `break`, `continue`, `return`.
- `but` is used instead of `else if` for conditional branching, allowing for flat, fast-parsing control flows.
- `use` is top-level only.
- `pub` marks exported module symbols.
- `use name` resolves a local file first. Standard modules (`sys`, `fs`, `path`, `json`) are integrated natively into the compiler.
- `use name as alias` is supported for namespacing and module-style access.
- `use name for symbol` imports a single exported symbol into the current scope.
- `from module use sym1, sym2` imports selected symbols from a module.
- `use "ffi:libname"` resolves a foreign binding module at `ffi/libname.yilt`.
- `from "ffi:libname" use ...` works the same way and is the most direct way to import foreign symbols.
- Foreign modules can declare `pub extern fn ...` symbols that lower to raw C ABI calls.
- `extern` means the function has no Yilt body. The compiler records its signature and emits a call to a symbol provided by the linked host library.
- `match` / `case` / `default` provide structured branching over a single value.
- **Error Propagation**: The `?` operator is used after an expression to propagate errors.

## Types
- Current value types: `int`, `uint`, `fp`, `bool`, `str`, `table`.
- `gen` is a generic placeholder used internally by the compiler; it is not a standalone value type in source code.
- All numeric types are 64-bit by default. Explicit bit-widths are NOT supported.
- `void` is internally supported for functions returning nothing but is not a valid keyword in source code.
- `table` is the canonical composite value type (heterogeneous hash map + array).

## Errors
- Yilt uses a tagged-value system where `0xE` indicates an Error.
- Error values carry a pointer to a descriptive error string.
- The `?` operator automates check and early return.

## Standard Library
Yilt includes several standard modules natively. Access requires namespacing unless imported specifically.
- `sys`: `sys.args()`, `sys.cwd()`, `sys.platform()`, `sys.env(name)`, `sys.exit(code)`
- `fs`: `fs.exists(path)`, `fs.read_text(path)`, `fs.write_text(path, text)`, `fs.append_text(path, text)`, `fs.read_lines(path)`, `fs.remove(path)`, `fs.rename(from, to)`, `fs.copy(from, to)`, `fs.mkdir(path)`, `fs.rmdir(path)`, `fs.read_dir(path)`
- `path`: `path.join(...)`, `path.resolve(...)`, `path.relative(from, to)`, `path.normalize(path)`, `path.dirname(path)`, `path.parent(path)`, `path.basename(path)`, `path.stem(path)`, `path.extname(path)`, `path.is_abs(path)`
- `json`: `json.encode(val)`, `json.decode(str)`, `json.parse(str)`
- `ffi`: foreign binding modules loaded through `ffi:` are regular Yilt files that wrap external symbols.

## Control Flow
- `if` and `but` form the primary conditional chain.
- `match` is a direct multi-branch form for strict value dispatch.
- `case` arms are compared in order.
- `default` is optional and ends the chain.

## Foreign Modules
Foreign bindings are intentionally small and explicit.

- A foreign binding file lives under `ffi/`.
- The file is still parsed as normal Yilt.
- The difference is `pub extern fn` instead of `fn ... { ... }`.
- The function body is omitted because the implementation comes from a host library.
- Foreign imports can be used with either:
  - `use "ffi:libname" as libname`
  - `from "ffi:libname" use symbol as alias`

Example:
```yilt
from "ffi:libc" use rand as libc_rand, srand

srand(1)
print(type_of(libc_rand()))
This project currently treats ffi: modules as
file-backed binding modules, not as runtime-loaded plugins. Yilt’s
built-in linker resolves foreign symbols.
Runtime and Memory

Backend translates Yilt through its own internal SSA IR to native
code.
Memory is managed via a chunk-backed YArena region
system.
free is a compatibility hook for region allocations,
