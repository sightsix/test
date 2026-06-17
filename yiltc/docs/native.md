# Yilt Native Runtime ABI

This page documents the internal runtime functions that compiler-generated code calls.
They are not the same thing as the public Yilt standard library surface, but they are
part of the implementation contract.

## Model

Yilt uses a tagged 64-bit value representation and a region-based arena allocator.
Generated code and the runtime agree on:

- tagged integers, booleans, floats, strings, tables, and errors
- arena lifetime semantics
- C ABI style calls for internal helpers

The runtime ABI is intentionally small and direct so the compiler can lower without
building a large middle-end.

## Memory and Arenas

- `y_arena_push(parent)` - create a child arena region.
- `y_arena_pop(arena)` - release an arena region and everything allocated in it.
- `y_alloc(arena, size)` - allocate raw bytes from an arena.
- `y_free(arena, value)` - region-aware compatibility release hook.

## Value Helpers

- `y_str_new(arena, s)` - copy a C string into arena-backed Yilt storage.
- `y_fp_new(arena, d)` - box a floating-point value.
- `y_copy(arena, value)` - shallow copy a tagged Yilt value.
- `y_promote(arena, value, kind)` - coerce or copy a value into the requested kind.
- `y_val_eq(arena, left, left_kind, right, right_kind)` - value equality helper.
- `y_type_of(arena, value, kind)` - return the runtime type name.
- `y_str_concat(arena, left, left_kind, right, right_kind)` - concatenate two values as strings.

## Arithmetic Helpers

- `y_abs(arena, x)` - absolute integer value.
- `y_neg(arena, x)` - arithmetic negation.
- `y_min(arena, x, y)` - smaller integer.
- `y_max(arena, x, y)` - larger integer.
- `y_sqrt(arena, x)` - square root.
- `y_floor(arena, x)` - floor to integer.
- `y_ceil(arena, x)` - ceil to integer.
- `y_round(arena, x)` - round to integer.
- `y_sign(arena, x)` - sign of a floating-point value.
- `y_clamp(arena, x, lo, hi)` - clamp integer value.

## Tables

- `y_table_new(arena)` - create a table.
- `y_tab_set(arena, tab, key, key_type, value, value_type)` - store a table entry.
- `y_tab_get(arena, tab, key, key_type)` - fetch a table value.
- `y_tab_has(arena, tab, key, key_type)` - test table membership.
- `y_tab_get_val_type(arena, tab, key, key_type)` - get the stored value kind.
- `y_tab_iter_valid(arena, tab, index)` - iterator validity check.
- `y_tab_iter_key(arena, tab, index)` - iterator key accessor.
- `y_tab_iter_val(arena, tab, index)` - iterator value accessor.
- `y_tab_iter_next(arena, tab, index)` - advance a table iterator.

## Core Runtime Functions

- `y_print(arena, value, kind)` - print a value.
- `y_input(arena, prompt)` - read a line from stdin.
- `y_len(arena, value)` - compute length.
- `y_panic(arena, msg)` - abort execution with a message.
- `y_assert(arena, cond, msg)` - abort if condition is false.
- `y_error(arena, msg)` - construct an error value.
- `y_is_error(value)` - check whether a value is an error.

## String Helpers

- `y_trim(arena, value)` - trim whitespace.
- `y_lower(arena, value)` - lowercase a string.
- `y_upper(arena, value)` - uppercase a string.
- `y_substr(arena, value, start, len)` - slice a string.
- `y_contains(arena, value, sub)` - substring membership test.
- `y_starts_with(arena, value, prefix)` - prefix test.
- `y_ends_with(arena, value, suffix)` - suffix test.
- `y_find(arena, value, sub)` - substring search index.

## System Helpers

- `y_sys_args(arena)` - argument table.
- `y_sys_cwd(arena)` - current working directory.
- `y_sys_platform(arena)` - platform string.
- `y_sys_env(arena, name)` - environment lookup.
- `y_sys_exit(arena, code)` - terminate the process.

## Filesystem Helpers

- `y_fs_exists(arena, path)` - file existence check.
- `y_fs_read_text(arena, path)` - read a whole file.
- `y_fs_write_text(arena, name, content)` - write a file.
- `y_fs_append_text(arena, name, content)` - append to a file.
- `y_fs_read_lines(arena, name)` - read a file as lines.
- `y_fs_remove(arena, path)` - remove a path.
- `y_fs_rename(arena, from, to)` - rename or move a path.
- `y_fs_copy(arena, from, to)` - copy a file.
- `y_fs_mkdir(arena, path)` - create a directory.
- `y_fs_rmdir(arena, path)` - remove a directory.
- `y_fs_read_dir(arena, path)` - list directory entries.

## Path Helpers

- `y_path_normalize(arena, path)` - clean separators and dot segments.
- `y_path_resolve(arena, path)` - resolve against the current directory.
- `y_path_resolve2(arena, left, right)` - resolve a path pair.
- `y_path_relative(arena, from, to)` - compute a relative path.
- `y_path_join(arena, left, right)` - join two path segments.
- `y_path_dirname(arena, path)` - directory name.
- `y_path_parent(arena, path)` - alias for dirname.
- `y_path_basename(arena, path)` - final path segment.
- `y_path_stem(arena, path)` - basename without extension.
- `y_path_extname(arena, path)` - extension suffix.
- `y_path_is_abs(arena, path)` - absolute-path check.

## JSON Helpers

- `y_json_encode(arena, value, kind)` - encode a Yilt value as JSON text.
- `y_json_decode(arena, text)` - decode JSON text into a Yilt value.

## Concurrency Helpers

- `y_spawn(arena, fn)` - start a function in a fresh child arena.
- `y_await(arena, handle)` - wait for a spawned task to finish.

## Entry Points

- `yilt_run(arena)` - compiler/runtime entry hook for the generated program.
- `yilt_main_abi(argc, argv)` - host-facing entry point used by the executable.

## Practical Notes

- These helpers are internal ABI details, not source-level language features.
- The public Yilt surface should prefer normal functions and modules where possible.
- The implementation stays small by keeping these helpers direct and predictable.
