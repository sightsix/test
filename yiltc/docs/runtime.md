# Yilt Runtime and Implementation

For the public-facing library surface, see [docs/stdlib.md](stdlib.md). For the internal
ABI used by generated code, see [docs/native.md](native.md).

## Architecture
Yilt has its own built-in IR, codegen, and linker. There are zero external dependencies — no QBE, no gcc.

Pipeline:
1. Lex source into tokens.
2. Parse into an AST.
3. Type check.
4. Lower to internal SSA IR.
5. Optimize.
6. Codegen to target architecture.
7. Link into final binary (ELF64, PE64, Mach-O64, or WASM).

Targets: x86-64, AArch64, RISC-V 64, RISC-V 32, WebAssembly.

## Source Model
- Indentation defines block structure.
- `use` and `from` are top-level constructs.
- `pub` is a visibility marker.
- `extern fn` is a declaration-only foreign symbol.
- `match` lowers to labels and branches, not a pattern-matching VM.
- The compiler has its own integrated codegen and linker; no external toolchain is needed.

## Types and Values
Yilt uses a tagged 64-bit value model.

Tags currently used:
- `TAG_VOID`
- `TAG_INT`
- `TAG_BOOL`
- `TAG_FP`
- `TAG_STR`
- `TAG_TABLE`
- `TAG_ERR`

Implementation details:
- Integers and booleans are tagged values.
- Strings and tables are heap objects stored in arenas.
- Floats are boxed and tagged.
- Errors carry a message string.

## Arenas
- The compiler and runtime use chunk-backed arenas.
- Arena allocations are region-oriented.
- `y_arena_pop` releases the whole region.
- `free` is a compatibility hook, not a general-purpose heap API.

## Runtime Helpers
The runtime exports the underlying implementation helpers used by the compiler-emitted code:

- `y_str_new`
- `y_fp_new`
- `y_val_eq`
- `y_promote`
- `y_copy`
- `y_table_new`
- `y_tab_set`
- `y_tab_get`
- `y_tab_has`
- `y_tab_get_val_type`
- `y_tab_iter_valid`
- `y_tab_iter_key`
- `y_tab_iter_val`
- `y_tab_iter_next`
- `y_print`
- `y_input`
- `y_len`
- `y_panic`
- `y_assert`
- `y_trim`
- `y_lower`
- `y_upper`
- `y_substr`
- `y_contains`
- `y_starts_with`
- `y_ends_with`
- `y_find`
- `y_type_of`

## Native Modules
The compiler injects native stdlib symbols for:
- `sys`
- `fs`
- `path`
- `json`

These are not loaded from source files at compile time. They are part of the compiler/runtime contract.

## FFI
- Foreign bindings are file-backed Yilt modules under `ffi/`.
- `use "ffi:libname"` resolves to `ffi/libname.yilt`.
- `pub extern fn` declares a symbol without a Yilt body.
- Yilt's built-in linker resolves the symbol.
- This design keeps the compiler simple and the ABI explicit.

## Diagnostics
- Diagnostics are rendered with source location, line context, caret/underline, notes, and help text.
- Duplicate names can carry a related location.
- Color is enabled on TTY stderr.

## Current Tradeoffs
- No runtime shared-library loader yet.
- No header auto-parsing for FFI.
- No structured pattern matching beyond value `match`.
- No full generics system or user-defined algebraic data types yet.
