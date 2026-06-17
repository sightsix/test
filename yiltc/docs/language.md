# Yilt Language Reference

## Overview
Yilt is a small, indentation-based language with its own built-in IR, codegen, and linker. The surface area is intentionally strict: a compact syntax, explicit imports, explicit mutation, and a narrow set of core types.

## Blocks
- Indentation defines scope.
- Newlines matter.
- A block begins after a header line and an increased indentation level.

## Statements

### `let`
Creates an immutable binding by default.

```yilt
let name = "yilt"
let count int = 3
let mut
Creates a mutable binding.
let mut total = 0
fn
Defines a function.
fn add(a int, b int) -> int
  return a + b
Notes: - Functions are top-level only. - Nested functions are
rejected. - Return type is optional (use -> ret or just
name the type after ); omit for no return value). -
Parameter names are required. - Parameter types may be omitted and will
be inferred.
extern fn
Declares a foreign function with no Yilt body.
pub extern fn rand() int
pub extern fn srand(seed int)
Notes: - extern declarations are top-level only. - They
are used for foreign bindings in ffi/*.yilt. - The compiler
records the signature and emits a call to a host symbol.
pub
Marks a top-level declaration as exported.
use
Imports a module.
use fs
use path as p
use "ffi:libc" as libc
Notes: - use is top-level only. - Standard modules
(sys, fs, path,
json) are built into the compiler. - Local files are
resolved first. - ffi: resolves to
ffi/<name>.yilt. - use name for symbol
imports one symbol directly into the current scope.
from ... use ...
Imports selected symbols.
from path use join, resolve as abs_path
from "ffi:libc" use rand as libc_rand, srand
if / but /
else
Conditional control flow.
if x == 1
  print("one")
but x == 2
  print("two")
else
  print("other")
while
Standard looping construct.
for
Iterates over a table-like value.
for key in tab
  print(key)

for key, value in tab
  print(key)
  print(value)
Notes: - One identifier gives you the key/index. - Two identifiers
give you key/index and value.
match
Strict value dispatch.
match x
  case 1
    print("one")
  case 2
    print("two")
  default
    print("other")
return
Returns from a function.
break / continue
Loop control.
spawn / await
Concurrency primitives.
let handle = spawn work(arg)
await handle
?
Error propagation.
let text = fs.read_text("a.txt")?
Expressions

Literals: integers, floats, strings, true,
false
Tables: { ... }
Indexing: tab[key]
Assignment: name = expr
Table update: tab[key] = value
Function call: name(arg1, arg2)
Module access: mod.name(...) or
mod["name"](...)

Operators
From high to low precedence: - unary -,
not, ~ - *, /,
% - +, - - <<,
>> - <, <=,
>, >= - ==, !=
- & - ^ - and -
or
String concatenation uses +.
Types
Current source-visible types: - int - uint
- fp - bool - str -
table - gen
Notes: - Numeric types are 64-bit unless represented as
fp. - gen is a generic placeholder type used
in compiler internals; it is not a standalone value type in source code.
- void exists internally but is not a source keyword. -
arena is used in internals and runtime contracts.
Imports and Visibility

pub exports a symbol from its module.
use name imports the module under its basename.
use name as alias imports it under a different module
name.
from module use symbol imports named symbols.
use and from both resolve standard modules
and local files.

Errors

Errors use a tagged value model.
? returns early on an error value.
Diagnostics include file, line, column, source context, and helpful
notes.

Match Semantics

match compares one subject against each
case.
Cases are checked top to bottom.
default is optional.
Empty match blocks are rejected.
String equality is by content.

FFI

Foreign binding files live under ffi/.
They use normal Yilt syntax with pub extern fn.
They are linked through Yilt’s built-in linker.
This is file-backed FFI, not runtime dlopen
plugins.

