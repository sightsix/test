package runtime

// ---------------------------------------------------------------------------
// Runtime Symbol Table and Initialization
//
// This file defines the complete set of runtime symbols emitted into every
// Yilt executable.  Each symbol has a name (mangled with y_ prefix), a
// calling signature describing its arguments and return types, and a
// category for documentation and code-generation purposes.
//
// IMPORTANT: The Yilt compiler is fully self-contained with ZERO external
// dependencies.  There is no C runtime, no libc, no external linker.  The
// compiler emits every byte of the final binary including all runtime
// functions.  I/O uses raw syscalls (read/write), memory management uses
// arena-based bump allocation (no malloc/free), string formatting is
// implemented in-house (no printf/sprintf), and math uses software
// implementations or hardware instructions (no libm).
//
// Backends consume these symbols in three ways:
//  1. Emitting CALL instructions to compiler-generated native functions.
//  2. Inlining the function body for performance-critical operations
//     (e.g. tag/untag, arena bump-alloc).
//  3. Referencing global data symbols (e.g. y_root_arena).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Signature types
//
// The signature uses single-character type codes:
//   V = void
//   i = int (tagged)
//   b = bool (tagged)
//   f = float (tagged, boxed pointer in payload)
//   s = string (tagged, pointer in payload)
//   t = table (tagged, pointer in payload)
//   e = error (tagged, pointer in payload)
//   p = raw pointer (*void, arena pointer, etc.)
//   u = uint64 (raw, untagged)
//   . = self/this (receiver — same as the type of the method's subject)
//
// Arguments are listed in parentheses, return type follows ->.
// Example: "y_print(i)" = takes one tagged int (really any tagged value),
//          returns void.
// ---------------------------------------------------------------------------

// RuntimeSymbol describes a single runtime function or global variable.
type RuntimeSymbol struct {
        // Name is the linker-visible symbol name (e.g. "y_print").
        Name string

        // Signature describes the calling convention in compact notation.
        // Arguments come first in parens, then -> return type.
        // Empty parens means no arguments.
        Signature string

        // Category groups the symbol for documentation and code generation.
        Category string

        // Description is a human-readable summary of what the function does.
        Description string

        // Inlineable is true if the backend should consider inlining this
        // function instead of emitting a call.
        Inlineable bool

        // Variadic is true if the function accepts a variable number of
        // arguments (e.g. y_print).
        Variadic bool
}

// String returns the symbol name.
func (s RuntimeSymbol) String() string { return s.Name }

// ---------------------------------------------------------------------------
// Complete runtime symbol table
//
// This is the authoritative list of ALL runtime symbols.  The compiler emits
// the machine code for every one of these symbols directly into the final
// binary.  No external linker or host-provided implementation is needed;
// the compiler is fully self-contained.
// ---------------------------------------------------------------------------

// Symbols is the complete table of runtime symbols, organized by category.
var Symbols []RuntimeSymbol

func init() {
        Symbols = make([]RuntimeSymbol, 0, 100)

        // ================================================================
        // Entry point
        // ================================================================
        add(RuntimeSymbol{
                Name:        "yilt_run",
                Signature:   "()->i",
                Category:    CatEntry,
                Description: "Program entry point. Initializes the runtime (arena, globals) then calls yilt_main_abi. Returns the process exit code.",
        })
        add(RuntimeSymbol{
                Name:        "yilt_main_abi",
                Signature:   "()->i",
                Category:    CatEntry,
                Description: "User's main function entry point. The compiler emits the user's main() body here. Returns exit code.",
        })

        // ================================================================
        // Memory management
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_arena_push",
                Signature:   "(p)->p",
                Category:    CatMemory,
                Description: "Push a new child arena. Returns pointer to the new arena. Allocated from parent.",
                Inlineable:  false,
        })
        add(RuntimeSymbol{
                Name:        "y_arena_pop",
                Signature:   "(p)->V",
                Category:    CatMemory,
                Description: "Pop (destroy) an arena and all its children. Releases all memory allocated within.",
                Inlineable:  false,
        })
        add(RuntimeSymbol{
                Name:        "y_alloc",
                Signature:   "(pu)->p",
                Category:    CatMemory,
                Description: "Allocate size bytes from arena with alignment. Returns pointer to allocated memory.",
                Inlineable:  true,
        })
        add(RuntimeSymbol{
                Name:        "y_free",
                Signature:   "(pp)->V",
                Category:    CatMemory,
                Description: "No-op in arena mode. Provided for compatibility. Does not release memory.",
                Inlineable:  true,
        })

        // ================================================================
        // Value construction and manipulation
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_str_new",
                Signature:   "(pup)->s",
                Category:    CatValues,
                Description: "Create a new string from raw bytes (data pointer, length, arena). Computes and caches FNV-1a hash.",
        })
        add(RuntimeSymbol{
                Name:        "y_fp_new",
                Signature:   "(fp)->f",
                Category:    CatValues,
                Description: "Box a float64 value into a heap allocation (arena). Returns tagged float.",
        })
        add(RuntimeSymbol{
                Name:        "y_fp_add",
                Signature:   "(ff)->f",
                Category:    CatValues,
                Description: "Add two boxed float64 values. Returns a new tagged float.",
        })
        add(RuntimeSymbol{
                Name:        "y_fp_sub",
                Signature:   "(ff)->f",
                Category:    CatValues,
                Description: "Subtract two boxed float64 values. Returns a new tagged float.",
        })
        add(RuntimeSymbol{
                Name:        "y_fp_mul",
                Signature:   "(ff)->f",
                Category:    CatValues,
                Description: "Multiply two boxed float64 values. Returns a new tagged float.",
        })
        add(RuntimeSymbol{
                Name:        "y_fp_div",
                Signature:   "(ff)->f",
                Category:    CatValues,
                Description: "Divide two boxed float64 values. Returns a new tagged float.",
        })
        add(RuntimeSymbol{
                Name:        "y_fp_neg",
                Signature:   "(f)->f",
                Category:    CatValues,
                Description: "Negate a boxed float64 value. Returns a new tagged float.",
        })
        add(RuntimeSymbol{
                Name:        "y_copy",
                Signature:   "(.)->.",
                Category:    CatValues,
                Description: "Shallow copy a tagged value. For ints/bools this duplicates the word; for heap types returns the same pointer.",
                Inlineable:  true,
        })
        add(RuntimeSymbol{
                Name:        "y_promote",
                Signature:   "(..)->.",
                Category:    CatValues,
                Description: "Promote two tagged values to a common type for binary operations. Returns the promoted left value.",
        })
        add(RuntimeSymbol{
                Name:        "y_val_eq",
                Signature:   "(..)->b",
                Category:    CatValues,
                Description: "Deep equality comparison of two tagged values. Ints/bools by value, floats by IEEE 754, strings by bytes, tables by pointer identity.",
        })
        add(RuntimeSymbol{
                Name:        "y_enum_eq",
                Signature:   "(..)->b",
                Category:    CatValues,
                Description: "Structural equality for enum values (table-backed). Compares _v (variant index) and _p (payload) fields.",
        })
        add(RuntimeSymbol{
                Name:        "y_type_of",
                Signature:   "(.)->s",
                Category:    CatValues,
                Description: "Returns the type name of a tagged value as a string: 'int', 'bool', 'fp', 'str', 'table', 'error', 'void'.",
        })
        add(RuntimeSymbol{
                Name:        "y_str_concat",
                Signature:   "(ss)->s",
                Category:    CatValues,
                Description: "Concatenate two strings, allocating the result from the current arena.",
        })

        // ================================================================
        // Math
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_abs",
                Signature:   "(.)->.",
                Category:    CatMath,
                Description: "Absolute value. For ints returns |x|, for floats returns fabs(x).",
        })
        add(RuntimeSymbol{
                Name:        "y_neg",
                Signature:   "(.)->.",
                Category:    CatMath,
                Description: "Negation. For ints returns -x, for floats returns -x.",
                Inlineable:  true,
        })
        add(RuntimeSymbol{
                Name:        "y_min",
                Signature:   "(..)->.",
                Category:    CatMath,
                Description: "Minimum of two values. Both operands must be the same numeric type.",
        })
        add(RuntimeSymbol{
                Name:        "y_max",
                Signature:   "(..)->.",
                Category:    CatMath,
                Description: "Maximum of two values. Both operands must be the same numeric type.",
        })
        add(RuntimeSymbol{
                Name:        "y_sqrt",
                Signature:   "(.)->f",
                Category:    CatMath,
                Description: "Square root. Promotes int to float if needed.",
        })
        add(RuntimeSymbol{
                Name:        "y_floor",
                Signature:   "(.)->f",
                Category:    CatMath,
                Description: "Floor to the nearest integer value. Returns float.",
        })
        add(RuntimeSymbol{
                Name:        "y_ceil",
                Signature:   "(.)->f",
                Category:    CatMath,
                Description: "Ceiling to the nearest integer value. Returns float.",
        })
        add(RuntimeSymbol{
                Name:        "y_round",
                Signature:   "(.)->f",
                Category:    CatMath,
                Description: "Round to the nearest integer value (half away from zero). Returns float.",
        })
        add(RuntimeSymbol{
                Name:        "y_sign",
                Signature:   "(.)->i",
                Category:    CatMath,
                Description: "Sign function. Returns -1, 0, or 1 (as tagged int).",
        })
        add(RuntimeSymbol{
                Name:        "y_clamp",
                Signature:   "(...)->.",
                Category:    CatMath,
                Description: "Clamp value to [min, max] range. All three args must be the same numeric type.",
        })

        // ================================================================
        // Tables
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_table_new",
                Signature:   "(p)->t",
                Category:    CatTables,
                Description: "Create a new empty hash table with default capacity (16). Allocates from arena.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_set",
                Signature:   "(t..p)->V",
                Category:    CatTables,
                Description: "Insert or update key -> value in table. Triggers rehashing if load factor exceeded.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_get",
                Signature:   "(t.)->.",
                Category:    CatTables,
                Description: "Look up key in table. Returns the associated value, or NilValue if not found.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_has",
                Signature:   "(t.)->b",
                Category:    CatTables,
                Description: "Returns true if table contains the given key.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_del",
                Signature:   "(t.)->b",
                Category:    CatTables,
                Description: "Remove key from table. Returns true if key was present. Marks slot as tombstone.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_len",
                Signature:   "(t)->i",
                Category:    CatTables,
                Description: "Returns the number of occupied entries in the table.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_get_val_type",
                Signature:   "(t.)->i",
                Category:    CatTables,
                Description: "Returns the tag byte of the value at key, or -1 if key not present.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_iter_valid",
                Signature:   "(ti)->b",
                Category:    CatTables,
                Description: "Returns true if the iterator index points to a valid occupied entry. Advances past empty/tombstone slots.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_iter_key",
                Signature:   "(ti)->.",
                Category:    CatTables,
                Description: "Returns the key of the entry at the given iterator index.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_iter_val",
                Signature:   "(ti)->.",
                Category:    CatTables,
                Description: "Returns the value of the entry at the given iterator index.",
        })
        add(RuntimeSymbol{
                Name:        "y_tab_iter_next",
                Signature:   "(ti)->i",
                Category:    CatTables,
                Description: "Advance iterator to the next occupied entry. Returns the new index, or -1 if exhausted.",
        })

        // ================================================================
        // High-level iterator API (used by compiler backends for for-in loops)
        // ================================================================
        add(RuntimeSymbol{
                Name:        "runtime_iter_new",
                Signature:   "(t)->i",
                Category:    CatTables,
                Description: "Create a new table iterator. Stores the table internally and returns the initial index (-1).",
        })
        add(RuntimeSymbol{
                Name:        "runtime_iter_next",
                Signature:   "(i)->i",
                Category:    CatTables,
                Description: "Advance the iterator. Returns 1 if more entries exist, 0 if done. Stores key/value in globals accessible via runtime_iter_get_key/get_val/get_next.",
        })
        add(RuntimeSymbol{
                Name:        "runtime_iter_get_key",
                Signature:   "()->.",
                Category:    CatTables,
                Description: "Returns the key from the most recent runtime_iter_next call.",
        })
        add(RuntimeSymbol{
                Name:        "runtime_iter_get_val",
                Signature:   "()->.",
                Category:    CatTables,
                Description: "Returns the value from the most recent runtime_iter_next call.",
        })
        add(RuntimeSymbol{
                Name:        "runtime_iter_get_next",
                Signature:   "()->i",
                Category:    CatTables,
                Description: "Returns the advanced iterator index from the most recent runtime_iter_next call.",
        })

        // ================================================================
        // Core functions
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_print",
                Signature:   "(.)->V",
                Category:    CatCore,
                Description: "Print a tagged value to stdout (without trailing newline). Formats according to type.",
                Variadic:    false, // takes exactly one arg; overloaded in source as print(a, b, c...)
        })
        add(RuntimeSymbol{
                Name:        "y_println",
                Signature:   "(.)->V",
                Category:    CatCore,
                Description: "Print a tagged value to stdout followed by a newline.",
        })
        add(RuntimeSymbol{
                Name:        "y_input",
                Signature:   "(s)->s",
                Category:    CatCore,
                Description: "Read a line of text from stdin. The argument is the prompt string.",
        })
        add(RuntimeSymbol{
                Name:        "y_len",
                Signature:   "(.)->i",
                Category:    CatCore,
                Description: "Returns the length of a value: string byte count, table entry count, or 0 for other types.",
        })
        add(RuntimeSymbol{
                Name:        "y_panic",
                Signature:   "(s)->V",
                Category:    CatCore,
                Description: "Abort the program with an error message. Prints message to stderr and calls exit(1).",
        })
        add(RuntimeSymbol{
                Name:        "y_assert",
                Signature:   "(bs)->V",
                Category:    CatCore,
                Description: "If condition is false, panic with the given message string.",
        })
        add(RuntimeSymbol{
                Name:        "y_error",
                Signature:   "(s)->e",
                Category:    CatCore,
                Description: "Create an error value from a message string.",
        })
        add(RuntimeSymbol{
                Name:        "y_is_error",
                Signature:   "(.)->b",
                Category:    CatCore,
                Description: "Returns true if the tagged value is an error.",
                Inlineable:  true,
        })

        // ================================================================
        // String operations
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_trim",
                Signature:   "(s)->s",
                Category:    CatStrings,
                Description: "Remove leading and trailing ASCII whitespace from a string.",
        })
        add(RuntimeSymbol{
                Name:        "y_lower",
                Signature:   "(s)->s",
                Category:    CatStrings,
                Description: "Convert all ASCII letters to lowercase.",
        })
        add(RuntimeSymbol{
                Name:        "y_upper",
                Signature:   "(s)->s",
                Category:    CatStrings,
                Description: "Convert all ASCII letters to uppercase.",
        })
        add(RuntimeSymbol{
                Name:        "y_substr",
                Signature:   "(sii)->s",
                Category:    CatStrings,
                Description: "Extract substring s[start:end]. Negative indices count from end. Panics on out-of-range.",
        })
        add(RuntimeSymbol{
                Name:        "y_contains",
                Signature:   "(ss)->b",
                Category:    CatStrings,
                Description: "Returns true if the string contains the given substring.",
        })
        add(RuntimeSymbol{
                Name:        "y_starts_with",
                Signature:   "(ss)->b",
                Category:    CatStrings,
                Description: "Returns true if the string starts with the given prefix.",
        })
        add(RuntimeSymbol{
                Name:        "y_ends_with",
                Signature:   "(ss)->b",
                Category:    CatStrings,
                Description: "Returns true if the string ends with the given suffix.",
        })
        add(RuntimeSymbol{
                Name:        "y_find",
                Signature:   "(ss)->i",
                Category:    CatStrings,
                Description: "Returns the byte index of the first occurrence of substr in s, or -1 if not found.",
        })
        add(RuntimeSymbol{
                Name:        "y_str_split",
                Signature:   "(ss)->t",
                Category:    CatStrings,
                Description: "Split string by separator, returning a table of {index: substring} entries.",
        })
        add(RuntimeSymbol{
                Name:        "y_str_replace",
                Signature:   "(sss)->s",
                Category:    CatStrings,
                Description: "Replace all occurrences of 'old' with 'new' in string.",
        })
        add(RuntimeSymbol{
                Name:        "y_str_bytes",
                Signature:   "(s)->t",
                Category:    CatStrings,
                Description: "Convert string to a table mapping byte indices to integer byte values.",
        })
        add(RuntimeSymbol{
                Name:        "y_str_to_int",
                Signature:   "(s)->i",
                Category:    CatStrings,
                Description: "Parse string as a base-10 integer. Returns 0 and sets error on failure.",
        })
        add(RuntimeSymbol{
                Name:        "y_to_str",
                Signature:   "(a)->s",
                Category:    CatStrings,
                Description: "Convert any tagged value to its string representation.",
        })
        add(RuntimeSymbol{
                Name:        "y_to_int",
                Signature:   "(a)->i",
                Category:    CatStrings,
                Description: "Convert any tagged value to integer.",
        })
        add(RuntimeSymbol{
                Name:        "y_to_fp",
                Signature:   "(a)->f",
                Category:    CatStrings,
                Description: "Convert any tagged value to float.",
        })
        add(RuntimeSymbol{
                Name:        "y_str_to_float",
                Signature:   "(s)->f",
                Category:    CatStrings,
                Description: "Parse string as a float64. Returns 0.0 and sets error on failure.",
        })
        add(RuntimeSymbol{
                Name:        "y_str_repeat",
                Signature:   "(si)->s",
                Category:    CatStrings,
                Description: "Repeat the string n times. Returns empty string if n <= 0.",
        })

        // ================================================================
        // System
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_sys_args",
                Signature:   "()->t",
                Category:    CatSys,
                Description: "Returns command-line arguments as a table: {0: prog, 1: arg1, ...}.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_argc",
                Signature:   "()->i",
                Category:    CatSys,
                Description: "Returns the number of command-line arguments.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_cwd",
                Signature:   "()->s",
                Category:    CatSys,
                Description: "Returns the current working directory as a string.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_platform",
                Signature:   "()->s",
                Category:    CatSys,
                Description: "Returns the platform string: 'linux', 'darwin', 'windows', 'wasm', or 'unknown'.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_env",
                Signature:   "()->t",
                Category:    CatSys,
                Description: "Returns all environment variables as a table: {name: value, ...}.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_getenv",
                Signature:   "(s)->s",
                Category:    CatSys,
                Description: "Get the value of an environment variable, or empty string if not set.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_exit",
                Signature:   "(i)->V",
                Category:    CatSys,
                Description: "Exit the program with the given status code.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_write",
                Signature:   "(i,s,i)->i",
                Category:    CatSys,
                Description: "Write bytes from a string to a file descriptor. Returns bytes written.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_open",
                Signature:   "(s,i,i)->i",
                Category:    CatSys,
                Description: "Open a file. Returns file descriptor or negative error.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_close",
                Signature:   "(i)->i",
                Category:    CatSys,
                Description: "Close a file descriptor. Returns 0 on success.",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_clock",
                Signature:   "()->f",
                Category:    CatSys,
                Description: "Returns the wall-clock time in seconds since program start (monotonic if available).",
        })
        add(RuntimeSymbol{
                Name:        "y_sys_sleep",
                Signature:   "(f)->V",
                Category:    CatSys,
                Description: "Sleep for the given number of seconds (float).",
        })

        // ================================================================
        // Filesystem
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_fs_exists",
                Signature:   "(s)->b",
                Category:    CatFS,
                Description: "Returns true if the path exists (file or directory).",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_read_text",
                Signature:   "(s)->s",
                Category:    CatFS,
                Description: "Read the entire file as a UTF-8 string. Panics on error.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_write_text",
                Signature:   "(ss)->V",
                Category:    CatFS,
                Description: "Write string to file, creating or truncating it. Panics on error.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_append_text",
                Signature:   "(ss)->V",
                Category:    CatFS,
                Description: "Append string to file, creating it if necessary. Panics on error.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_read_lines",
                Signature:   "(s)->t",
                Category:    CatFS,
                Description: "Read file lines into a table: {0: line1, 1: line2, ...}.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_remove",
                Signature:   "(s)->b",
                Category:    CatFS,
                Description: "Remove a file. Returns true on success.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_rename",
                Signature:   "(ss)->b",
                Category:    CatFS,
                Description: "Rename or move a file. Returns true on success.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_copy",
                Signature:   "(ss)->b",
                Category:    CatFS,
                Description: "Copy a file. Returns true on success.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_mkdir",
                Signature:   "(s)->b",
                Category:    CatFS,
                Description: "Create a directory (including parents). Returns true on success.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_rmdir",
                Signature:   "(s)->b",
                Category:    CatFS,
                Description: "Remove an empty directory. Returns true on success.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_read_dir",
                Signature:   "(s)->t",
                Category:    CatFS,
                Description: "List directory entries as a table: {0: name1, 1: name2, ...}.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_is_file",
                Signature:   "(s)->b",
                Category:    CatFS,
                Description: "Returns true if the path exists and is a regular file.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_is_dir",
                Signature:   "(s)->b",
                Category:    CatFS,
                Description: "Returns true if the path exists and is a directory.",
        })
        add(RuntimeSymbol{
                Name:        "y_fs_file_size",
                Signature:   "(s)->i",
                Category:    CatFS,
                Description: "Returns the size of a file in bytes, or -1 if the file doesn't exist.",
        })

        // ================================================================
        // Path
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_path_normalize",
                Signature:   "(s)->s",
                Category:    CatPath,
                Description: "Normalize a path: resolve '.', '..', redundant separators.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_resolve",
                Signature:   "(s)->s",
                Category:    CatPath,
                Description: "Resolve a path to an absolute path (relative to cwd).",
        })
        add(RuntimeSymbol{
                Name:        "y_path_resolve2",
                Signature:   "(ss)->s",
                Category:    CatPath,
                Description: "Resolve a path relative to a base directory.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_relative",
                Signature:   "(ss)->s",
                Category:    CatPath,
                Description: "Compute the relative path from base to target.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_join",
                Signature:   "(..)->s",
                Category:    CatPath,
                Description: "Join path components with the platform separator. Accepts two or more arguments.",
                Variadic:    true,
        })
        add(RuntimeSymbol{
                Name:        "y_path_dirname",
                Signature:   "(s)->s",
                Category:    CatPath,
                Description: "Return the directory part of a path (everything before the last separator).",
        })
        add(RuntimeSymbol{
                Name:        "y_path_parent",
                Signature:   "(s)->s",
                Category:    CatPath,
                Description: "Return the parent directory of a path. Same as dirname for most cases.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_basename",
                Signature:   "(s)->s",
                Category:    CatPath,
                Description: "Return the final component of a path.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_stem",
                Signature:   "(s)->s",
                Category:    CatPath,
                Description: "Return the filename without extension: 'file.tar.gz' -> 'file.tar'.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_extname",
                Signature:   "(s)->s",
                Category:    CatPath,
                Description: "Return the file extension including the dot: 'file.txt' -> '.txt'.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_is_abs",
                Signature:   "(s)->b",
                Category:    CatPath,
                Description: "Returns true if the path is absolute.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_sep",
                Signature:   "()->s",
                Category:    CatPath,
                Description: "Returns the platform path separator: '/' on Unix, '\\' on Windows.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_sep_posix",
                Signature:   "()->s",
                Category:    CatPath,
                Description: "Returns the POSIX path separator: '/'.",
        })
        add(RuntimeSymbol{
                Name:        "y_path_sep_win",
                Signature:   "()->s",
                Category:    CatPath,
                Description: "Returns the Windows path separator: '\\' (backslash).",
        })

        // ================================================================
        // JSON
        // ================================================================
        add(RuntimeSymbol{
                Name:        "y_json_encode",
                Signature:   "(.)->s",
                Category:    CatJSON,
                Description: "Encode a tagged value as a JSON string. Supports int, float, bool, string, table, nil.",
        })
        add(RuntimeSymbol{
                Name:        "y_json_decode",
                Signature:   "(s)->.",
                Category:    CatJSON,
                Description: "Decode a JSON string into a tagged value. Returns error tag on parse failure.",
        })

        // Closure support
        add(RuntimeSymbol{
                Name:        "y_closure_new",
                Signature:   "(i)->.",
                Category:    CatCore,
                Description: "Allocate a closure struct for n_captures captured variables. Returns raw pointer.",
        })
        add(RuntimeSymbol{
                Name:        "y_closure_set",
                Signature:   "(..i,)->",
                Category:    CatCore,
                Description: "Store a tagged value into a closure struct at the given index.",
        })
        add(RuntimeSymbol{
                Name:        "y_closure_get",
                Signature:   "(.i)->.",
                Category:    CatCore,
                Description: "Load a tagged value from a closure struct at the given index.",
        })
        add(RuntimeSymbol{
                Name:        "y_closure_trampoline",
                Signature:   "(...)->.",
                Category:    CatCore,
                Description: "Indirect closure call: strips tag, loads fn_ptr from closure[0], tail-jumps to it.",
        })

}

// add appends a symbol to the table.  It is called from init().
func add(s RuntimeSymbol) {
        Symbols = append(Symbols, s)
}

// ---------------------------------------------------------------------------
// Symbol lookup
// ---------------------------------------------------------------------------

// Lookup finds a runtime symbol by name.  Returns the symbol and true if
// found, or a zero-value and false otherwise.
func Lookup(name string) (RuntimeSymbol, bool) {
        for _, s := range Symbols {
                if s.Name == name {
                        return s, true
                }
        }
        return RuntimeSymbol{}, false
}

// MustLookup finds a runtime symbol by name.  Panics if not found.
// This is used during compilation when the symbol is expected to exist.
func MustLookup(name string) RuntimeSymbol {
        s, ok := Lookup(name)
        if !ok {
                panic("runtime: symbol not found: " + name)
        }
        return s
}

// LookupByCategory returns all symbols in the given category.
func LookupByCategory(category string) []RuntimeSymbol {
        var result []RuntimeSymbol
        for _, s := range Symbols {
                if s.Category == category {
                        result = append(result, s)
                }
        }
        return result
}

// ---------------------------------------------------------------------------
// Runtime initialization
//
// The RuntimeInit structure describes the sequence of operations the
// generated yilt_run function must perform before calling the user's
// main function.
//
// The backends emit these operations as machine code in the preamble
// of yilt_run.
// ---------------------------------------------------------------------------

// RuntimeInit describes the runtime initialization requirements.
type RuntimeInit struct {
        // RootArenaSize is the size of the root arena struct allocation.
        RootArenaSize int

        // StackSize is the initial stack size for the main thread (if the
        // backend manages stacks directly, e.g. WASM).
        StackSize int

        // NeedsArgv is true if the entry point must capture argc/argv from
        // the host ABI and store them for y_sys_args.
        NeedsArgv bool

        // PreallocatedGlobals is the number of global variable slots to
        // zero-initialize in the data segment.
        PreallocatedGlobals int

        // InitFunctions lists runtime functions that must be called during
        // startup, in order.
        InitFunctions []string
}

// DefaultRuntimeInit returns the standard initialization configuration.
func DefaultRuntimeInit() *RuntimeInit {
        return &RuntimeInit{
                RootArenaSize:       ArenaSize,
                StackSize:           0, // use host stack
                NeedsArgv:           true,
                PreallocatedGlobals: 64,
                InitFunctions: []string{
                        "init_root_arena",
                        "init_global_strings",
                        "capture_argv",
                },
        }
}

// ---------------------------------------------------------------------------
// Architecture-specific runtime configuration
//
// Each backend needs to know which runtime functions to emit as native
// machine code.  The Yilt compiler is fully self-contained: NO libc, NO
// external linker, NO C runtime.  The compiler emits every function as
// native machine code using raw syscalls and inline implementations.
//
// ALL runtime functions are emitted by the compiler.  The distinction is:
//  - Inlineable: the backend may choose to inline the body at each call
//    site rather than emitting a separate function (e.g. tag/untag,
//    arena bump-alloc).  These are typically tiny, hot-path operations.
//  - Callable: the compiler emits a standalone function body that other
//    code calls via a normal CALL instruction.  These include everything
//    from print (syscall-based I/O) to hash tables to JSON parsing.
//
// How common operations are implemented without libc:
//  - I/O:              raw syscalls (SYS_write, SYS_read, SYS_exit)
//  - Memory:           arena-based bump allocation (no malloc/free)
//  - String formatting: custom printf implementation (no sprintf)
//  - Math:             software implementations or hardware FP instructions
//  - Filesystem:       raw syscalls (SYS_openat, SYS_read, SYS_write, etc.)
//  - Time/sleep:       raw syscalls (SYS_clock_gettime, SYS_nanosleep)
//  - Process exit:     raw syscall (SYS_exit_group)
// ---------------------------------------------------------------------------

// ArchRuntimeConfig describes the runtime configuration for a target
// architecture.
type ArchRuntimeConfig struct {
        // Arch is the target architecture name ("x86_64", "aarch64", "rv64", "wasm").
        Arch string

        // ArenaRegister is the name of the register used for the arena pointer.
        // Empty string if the platform uses a global variable instead.
        ArenaRegister string

        // PointerSize is the size of a pointer in bytes (4 or 8).
        PointerSize int

        // EmitAllFunctions lists every runtime function the compiler must
        // emit as native machine code for this architecture.  The compiler
        // generates the full function body for each entry; nothing is
        // linked from an external library or C runtime.
        //
        // Functions that are also marked Inlineable in the symbol table may
        // optionally be inlined at call sites for performance, but the
        // compiler still emits a standalone version here.
        EmitAllFunctions []string

        // DataSymbols lists global data symbols emitted in the data segment.
        DataSymbols []string
}

// ArchConfigs returns the runtime configuration for each supported
// architecture.  Every function listed in EmitAllFunctions is emitted as
// native machine code by the compiler.  No libc, no external linker,
// no C runtime — the compiler is fully self-contained.
func ArchConfigs() map[string]*ArchRuntimeConfig {
        return map[string]*ArchRuntimeConfig{
                "x86_64": {
                        Arch:          "x86_64",
                        ArenaRegister: ArenaRegisterX86_64,
                        PointerSize:   8,
                        EmitAllFunctions: []string{
                                // Entry point
                                "yilt_run", "yilt_main_abi",
                                // Memory management — arena-based, no malloc/free
                                "y_arena_push", "y_arena_pop",
                                "y_alloc", "y_free",
                                // Value construction and manipulation
                                "y_str_new", "y_fp_new", "y_copy",
                                "y_promote", "y_val_eq", "y_type_of",
                                "y_str_concat",
                                // Math — software implementations + hardware FP
                                "y_abs", "y_neg", "y_min", "y_max",
                                "y_sqrt", "y_floor", "y_ceil", "y_round",
                                "y_sign", "y_clamp",
                                // Hash tables
                                "y_table_new", "y_tab_set", "y_tab_get",
                                "y_tab_has", "y_tab_del", "y_tab_len",
                                "y_tab_get_val_type",
                                "y_tab_iter_valid", "y_tab_iter_key",
                                "y_tab_iter_val", "y_tab_iter_next",
                                // Core I/O — syscall-based (SYS_write, SYS_read)
                                "y_print", "y_println", "y_input", "y_len",
                                "y_panic", "y_assert", "y_error", "y_is_error",
                                // String operations
                                "y_trim", "y_lower", "y_upper",
                                "y_substr", "y_contains",
                                "y_starts_with", "y_ends_with", "y_find",
                                "y_str_split", "y_str_replace", "y_str_bytes",
                                "y_str_to_int", "y_str_to_float", "y_str_repeat",
                                // System — raw syscalls (SYS_getcwd, SYS_clock_gettime, etc.)
                                "y_sys_args", "y_sys_argc", "y_sys_cwd",
                                "y_sys_platform", "y_sys_env", "y_sys_getenv",
                                "y_sys_exit", "y_sys_clock", "y_sys_sleep",
                                // Filesystem — raw syscalls (SYS_openat, SYS_fstat, etc.)
                                "y_fs_exists", "y_fs_read_text", "y_fs_write_text",
                                "y_fs_append_text", "y_fs_read_lines",
                                "y_fs_remove", "y_fs_rename", "y_fs_copy",
                                "y_fs_mkdir", "y_fs_rmdir", "y_fs_read_dir",
                                "y_fs_is_file", "y_fs_is_dir", "y_fs_file_size",
                                // Path — pure software, no libc path functions
                                "y_path_normalize", "y_path_resolve", "y_path_resolve2",
                                "y_path_relative", "y_path_join",
                                "y_path_dirname", "y_path_parent", "y_path_basename",
                                "y_path_stem", "y_path_extname", "y_path_is_abs",
                                "y_path_sep", "y_path_sep_posix", "y_path_sep_win",
                                // JSON — custom encoder/decoder
                                "y_json_encode", "y_json_decode",
                        },
                        DataSymbols: []string{
                                RootArenaSymbol,
                                "y_argv",
                                "y_argc",
                                "y_platform_string",
                        },
                },
                "aarch64": {
                        Arch:          "aarch64",
                        ArenaRegister: ArenaRegisterAArch64,
                        PointerSize:   8,
                        EmitAllFunctions: []string{
                                // Same function set as x86_64; all emitted as
                                // native AArch64 machine code.  No libc needed.
                                "*x86_64", // marker: inherit function list from x86_64
                        },
                        DataSymbols: []string{
                                RootArenaSymbol,
                                "y_argv",
                                "y_argc",
                                "y_platform_string",
                        },
                },
                "rv64": {
                        Arch:          "rv64",
                        ArenaRegister: ArenaRegisterRV64,
                        PointerSize:   8,
                        EmitAllFunctions: []string{
                                // Same function set as x86_64; all emitted as
                                // native RISC-V machine code.  No libc needed.
                                "*x86_64", // marker: inherit function list from x86_64
                        },
                        DataSymbols: []string{
                                RootArenaSymbol,
                                "y_argv",
                                "y_argc",
                                "y_platform_string",
                        },
                },
                "wasm": {
                        Arch:          "wasm",
                        ArenaRegister: "", // WASM uses a global variable
                        PointerSize:   4,
                        EmitAllFunctions: []string{
                                // WASM subset — all emitted as native WASM bytecode.
                                // I/O uses WASM host imports (not libc).
                                "yilt_run", "yilt_main_abi",
                                "y_arena_push", "y_arena_pop",
                                "y_alloc", "y_free", "y_copy", "y_is_error", "y_neg",
                                "y_str_new", "y_fp_new",
                                "y_print", "y_println",
                                "y_panic",
                        },
                        DataSymbols: []string{
                                RootArenaSymbol,
                                "y_argv",
                                "y_argc",
                        },
                },
        }
}

// ---------------------------------------------------------------------------
// Global runtime state
//
// The following symbols are global data that the runtime initializes at
// program startup and makes available to all runtime functions.
// ---------------------------------------------------------------------------

// GlobalDataSymbols describes the global data symbols with their types
// and initialization semantics.
var GlobalDataSymbols = []struct {
        Name        string
        Size        int   // bytes
        Init        string // "zero", "string", "argv", "argc", "platform"
        Description string
}{
        {
                Name:        RootArenaSymbol,
                Size:        ArenaSize,
                Init:        "zero",
                Description: "Root arena pointer. Initialized by yilt_run to point to the root arena.",
        },
        {
                Name:        "y_argv",
                Size:        8, // pointer
                Init:        "argv",
                Description: "Pointer to the argv array (captured from host ABI at startup).",
        },
        {
                Name:        "y_argc",
                Size:        8, // int64
                Init:        "argc",
                Description: "Argument count (captured from host ABI at startup).",
        },
        {
                Name:        "y_platform_string",
                Size:        16, // inline short string or pointer
                Init:        "platform",
                Description: "Platform identifier string: 'linux', 'darwin', 'windows', 'wasm'.",
        },
        {
                Name:        "y_last_error",
                Size:        8, // tagged value
                Init:        "zero",
                Description: "Last error value. Set by functions that can fail. Read by y_is_error.",
        },
}
