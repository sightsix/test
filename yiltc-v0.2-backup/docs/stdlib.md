# Yilt Standard Library

This page documents the public Yilt library surface. For internal runtime symbols used by
generated code, see [docs/native.md](native.md).

## Overview
Yilt's standard library is built into the compiler/runtime. The implementation is small and direct, but the surface is meant to be practical enough for real programs.

## Core Functions
These are available as regular functions unless imported under a module alias.

- `print(value)` - print a value to stdout.
- `input(prompt)` - read a line from stdin.
- `len(value)` - length of a string or table.
- `type_of(value)` - return the runtime type name.
- `panic(message)` - abort with an error message.
- `assert(cond, message)` - abort if `cond` is false.
- `copy(value)` - shallow copy of strings, tables, and floats.
- `trim(value)` - trim leading and trailing whitespace from a string.
- `lower(value)` - lowercase a string.
- `upper(value)` - uppercase a string.
- `substr(value, start, len)` - slice a string.
- `contains(value, sub)` - substring test.
- `starts_with(value, prefix)` - prefix test.
- `ends_with(value, suffix)` - suffix test.
- `find(value, sub)` - index of a substring, or `-1`.
- `has(tab, key)` - table membership test.
- `min(a, b)` - smaller integer.
- `max(a, b)` - larger integer.
- `abs(x)` - absolute integer value.
- `sqrt(x)` - square root.
- `floor(x)` - floor to integer.
- `ceil(x)` - ceil to integer.
- `round(x)` - round to integer.
- `sign(x)` - sign of a floating-point value.
- `clamp(x, lo, hi)` - clamp integer value.
- `free(value)` - region-aware free hook.

## `sys`
- `sys.args()` - argument table.
- `sys.cwd()` - current working directory.
- `sys.platform()` - `"windows"` or `"unix"`.
- `sys.env(name)` - environment lookup, returns string or nil.
- `sys.exit(code)` - exit the process.

## `fs`
- `fs.exists(path)` - file existence check.
- `fs.read_text(path)` - read whole file as text.
- `fs.write_text(path, text)` - overwrite file, returns bool.
- `fs.append_text(path, text)` - append file, returns bool.
- `fs.read_lines(path)` - read lines into a table.
- `fs.remove(path)` - remove file or directory.
- `fs.rename(from, to)` - rename or move.
- `fs.copy(from, to)` - copy file contents.
- `fs.mkdir(path)` - create a directory.
- `fs.rmdir(path)` - remove a directory.
- `fs.read_dir(path)` - list directory entries.

## `path`
- `path.join(...)` - join path segments.
- `path.resolve(...)` - resolve against the current directory.
- `path.relative(from, to)` - compute a relative path.
- `path.normalize(path)` - clean separators and dot segments.
- `path.dirname(path)` - parent directory.
- `path.parent(path)` - alias for `dirname`.
- `path.basename(path)` - final path segment.
- `path.stem(path)` - basename without extension.
- `path.extname(path)` - file extension.
- `path.is_abs(path)` - absolute-path check.

## `json`
- `json.encode(value)` - encode a value to JSON text.
- `json.decode(text)` - decode JSON text.
- `json.parse(text)` - parse JSON text (alias for `json.decode`).

## Practical Notes
- String values are immutable.
- Tables are the main composite type.
- `fs.write_text` and `fs.append_text` return booleans.
- `sys.env` returns an error value for missing variables; check with `is_error`.
- `fs.read_dir` returns a table of entries.
