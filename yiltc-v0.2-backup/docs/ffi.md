# Yilt Foreign Bindings

Yilt foreign bindings are the bridge to non-Yilt libraries, especially C ABI libraries.

## Short Version
- `extern` means "declare this function, but do not define it in Yilt."
- The compiler records the signature and emits a call site.
- The real implementation is resolved by Yilt's built-in linker.

## Binding Model
Foreign bindings live in normal Yilt files under `ffi/`.

Typical layout:

```text
ffi/
  libc.yilt
  sqlite.yilt
  zlib.yilt
Example binding file:
pub extern fn rand() int
pub extern fn srand(seed int)
That file is still parsed as Yilt, so it participates in
use, from, as, and
pub.
Import Forms
You can load a foreign module in two common ways:
use "ffi:libc" as libc
or:
from "ffi:libc" use rand as libc_rand, srand
The second form is the most explicit when you want to rename specific
foreign symbols.
What extern Does
extern is a declaration-only function: - no body - no
Yilt implementation - no local code generation for the function body -
symbol is resolved by Yilt’s built-in linker
This keeps the language direct and fast to compile while still
allowing low-level library access.
Current Scope
The current implementation is intentionally small: - file-backed
binding modules - C ABI style symbol calls - imported through the
existing module system
Not implemented yet: - runtime
dlopen/LoadLibrary plugin loading - automatic
header parsing - pointer-heavy marshalling helpers - rich type metadata
for nested C structs
Practical Guidance

Use foreign bindings for narrow, stable APIs.
Keep the binding layer explicit and typed.
Prefer small wrapper functions in ffi/*.yilt over
exposing huge C surfaces directly.
For void returns, omit the return type in Yilt source.

Example:
use "ffi:libc" as libc

libc.srand(1)
print(type_of(libc.rand()))
