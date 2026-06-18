package runtime

// ---------------------------------------------------------------------------
// Standard Library Function Mappings
//
// Yilt provides built-in functions and module-based standard library
// functions.  The compiler resolves source-level names (e.g. "print",
// "sys.args") to runtime symbol names (e.g. "y_print", "y_sys_args").
//
// This file provides the mapping tables that the compiler's name-resolution
// phase uses to look up runtime symbols for each source-level function.
//
// # Module system
//
// Yilt has the following built-in modules:
//   - sys:   system information (args, cwd, platform, env, exit)
//   - fs:    filesystem operations (read, write, mkdir, etc.)
//   - path:  path manipulation (join, dirname, basename, etc.)
//   - json:  JSON encoding/decoding
//
// Module functions are accessed as: module.function, e.g. sys.args
// The compiler strips the module prefix and maps to the corresponding
// y_-prefixed runtime symbol.
// ---------------------------------------------------------------------------

// BuiltinMapping maps source-level built-in function names to runtime
// symbol names.  These functions are available without any import.
var BuiltinMapping = map[string]string{
        // Core
        "print":    "y_print",
        "println":  "y_println",
        "input":    "y_input",
        "len":    "y_len",
        "panic":  "y_panic",
        "assert": "y_assert",
        "error":  "y_error",
        "is_error": "y_is_error",

        // Value operations
        "copy":     "y_copy",
        "promote":  "y_promote",
        "type_of":  "y_type_of",

        // Math
        "abs":   "y_abs",
        "neg":   "y_neg",
        "min":   "y_min",
        "max":   "y_max",
        "sqrt":  "y_sqrt",
        "floor": "y_floor",
        "ceil":  "y_ceil",
        "round": "y_round",
        "sign":  "y_sign",
        "clamp": "y_clamp",
        "pow":   "y_pow",
        "sin":   "y_sin",
        "cos":   "y_cos",
        "tan":   "y_tan",

        // String
        "trim":        "y_trim",
        "lower":       "y_lower",
        "upper":       "y_upper",
        "substr":      "y_substr",
        "contains":    "y_contains",
        "starts_with": "y_starts_with",
        "ends_with":   "y_ends_with",
        "find":        "y_find",
        "str_concat":  "y_str_concat",
        "str_new":     "y_str_new",
        "to_str":      "y_to_str",
        "to_int":      "y_to_int",
        "to_fp":       "y_to_fp",

        // Tables
        "table_new": "y_table_new",
}

// ModuleMapping maps "module.function" to the runtime symbol name.
// The compiler parses dotted identifiers and looks them up here.
var ModuleMapping = map[string]string{
        // ---- sys module ----
        "sys.args":      "y_sys_args",
        "sys.argc":      "y_sys_argc",
        "sys.cwd":       "y_sys_cwd",
        "sys.platform":  "y_sys_platform",
        "sys.env":       "y_sys_env",
        "sys.getenv":    "y_sys_getenv",
        "sys.exit":      "y_sys_exit",
        "sys.write":     "y_sys_write",
        "sys.open":      "y_sys_open",
        "sys.close":     "y_sys_close",
        "sys.clock":     "y_sys_clock",
        "sys.sleep":     "y_sys_sleep",
        "sys.setenv":    "y_sys_setenv",

        // ---- fs module ----
        "fs.exists":       "y_fs_exists",
        "fs.read_text":    "y_fs_read_text",
        "fs.write_text":   "y_fs_write_text",
        "fs.append_text":  "y_fs_append_text",
        "fs.read_lines":   "y_fs_read_lines",
        "fs.remove":       "y_fs_remove",
        "fs.rename":       "y_fs_rename",
        "fs.copy":         "y_fs_copy",
        "fs.mkdir":        "y_fs_mkdir",
        "fs.rmdir":        "y_fs_rmdir",
        "fs.read_dir":     "y_fs_read_dir",
        "fs.is_file":      "y_fs_is_file",
        "fs.is_dir":       "y_fs_is_dir",
        "fs.file_size":    "y_fs_file_size",
        "fs.stat":        "y_fs_stat",

        // ---- path module ----
        "path.normalize":  "y_path_normalize",
        "path.resolve":    "y_path_resolve",
        "path.resolve2":   "y_path_resolve2",
        "path.relative":   "y_path_relative",
        "path.join":       "y_path_join",
        "path.dirname":    "y_path_dirname",
        "path.parent":     "y_path_parent",
        "path.basename":   "y_path_basename",
        "path.stem":       "y_path_stem",
        "path.extname":    "y_path_extname",
        "path.is_abs":     "y_path_is_abs",
        "path.sep":        "y_path_sep",
        "path.sep_posix":  "y_path_sep_posix",
        "path.sep_win":    "y_path_sep_win",

        // ---- json module ----
        "json.encode": "y_json_encode",
        "json.decode": "y_json_decode",
        "json.dump":   "y_json_encode", // alias
        "json.parse":  "y_json_decode", // alias
        "json.stringify": "y_json_encode", // alias
}

// ---------------------------------------------------------------------------
// Module membership
//
// These maps allow the compiler to answer "what module does function X
// belong to?" which is needed for error messages and documentation.
// ---------------------------------------------------------------------------

// ModuleMembers maps module names to the set of function names defined in
// that module.
var ModuleMembers = map[string][]string{
        "sys": {
                "args", "argc", "cwd", "platform", "env", "getenv",
                "exit", "clock", "sleep", "setenv",
        },
        "fs": {
                "exists", "read_text", "write_text", "append_text",
                "read_lines", "remove", "rename", "copy",
                "mkdir", "rmdir", "read_dir", "is_file", "is_dir",
                "file_size", "stat",
        },
        "path": {
                "normalize", "resolve", "resolve2", "relative", "join",
                "dirname", "parent", "basename", "stem", "extname",
                "is_abs", "sep", "sep_posix", "sep_win",
        },
        "json": {
                "encode", "decode", "dump", "parse", "stringify",
        },
}

// ---------------------------------------------------------------------------
// Method-like table operations
//
// Some table operations are accessed as method calls on table values:
//   t.set(key, value)  -> y_tab_set(t, key, value, arena)
//   t.get(key)          -> y_tab_get(t, key)
//   t.has(key)          -> y_tab_has(t, key)
//   t.len()             -> y_tab_len(t)
//   t.del(key)          -> y_tab_del(t, key)
//
// The compiler resolves these by checking if the receiver is a table
// and mapping the method name to the corresponding runtime function.
// ---------------------------------------------------------------------------

// TableMethodMapping maps table method names to runtime symbols.
// The compiler prepends "y_" and dispatches accordingly.
var TableMethodMapping = map[string]string{
        "set":          "y_tab_set",
        "get":          "y_tab_get",
        "has":          "y_tab_has",
        "del":          "y_tab_del",
        "len":          "y_tab_len",
        "get_type":     "y_tab_get_val_type",
        "iter_valid":   "y_tab_iter_valid",
        "iter_key":     "y_tab_iter_key",
        "iter_val":     "y_tab_iter_val",
        "iter_next":    "y_tab_iter_next",
}

// ---------------------------------------------------------------------------
// String method mapping
//
// String methods: s.len(), s.trim(), s.lower(), s.upper(), s.substr(), etc.
// ---------------------------------------------------------------------------

// StringMethodMapping maps string method names to runtime symbols.
var StringMethodMapping = map[string]string{
        "len":         "y_len",
        "trim":        "y_trim",
        "lower":       "y_lower",
        "upper":       "y_upper",
        "substr":      "y_substr",
        "contains":    "y_contains",
        "starts_with": "y_starts_with",
        "ends_with":   "y_ends_with",
        "find":        "y_find",
        "split":       "y_str_split",
        "replace":     "y_str_replace",
        "bytes":       "y_str_bytes",
        "to_int":      "y_str_to_int",
        "to_float":    "y_str_to_float",
        "repeat":      "y_str_repeat",
}

// ---------------------------------------------------------------------------
// ResolveBuiltin resolves a source-level function name to a runtime symbol.
//
// First checks the built-in mapping, then the module mapping (for
// "module.function" syntax).  Returns the runtime symbol name and true
// if found, or "" and false if not recognized.
// ---------------------------------------------------------------------------

// ResolveBuiltin looks up a source-level name and returns the runtime
// symbol name.  Returns ("", false) if the name is not a built-in or
// module function.
func ResolveBuiltin(name string) (string, bool) {
        // Direct built-in lookup.
        if sym, ok := BuiltinMapping[name]; ok {
                return sym, true
        }
        // Module function lookup ("sys.args" etc.).
        if sym, ok := ModuleMapping[name]; ok {
                return sym, true
        }
        return "", false
}

// ResolveModuleFunction resolves "module.function" and returns the
// runtime symbol name.
func ResolveModuleFunction(module, function string) (string, bool) {
        key := module + "." + function
        if sym, ok := ModuleMapping[key]; ok {
                return sym, true
        }
        return "", false
}

// ResolveMethod resolves a method call on a receiver of the given type tag.
// Returns the runtime symbol name and true if the method exists.
func ResolveMethod(typeTag uint8, methodName string) (string, bool) {
        switch typeTag {
        case TagTable:
                if sym, ok := TableMethodMapping[methodName]; ok {
                        return sym, true
                }
        case TagStr:
                if sym, ok := StringMethodMapping[methodName]; ok {
                        return sym, true
                }
        }
        return "", false
}

// IsBuiltin returns true if the given source-level name is a built-in
// function (not a module function).
func IsBuiltin(name string) bool {
        _, ok := BuiltinMapping[name]
        return ok
}

// IsModuleFunction returns true if the name is in the "module.function" form
// and refers to a known module function.
func IsModuleFunction(name string) bool {
        _, ok := ModuleMapping[name]
        return ok
}

// ---------------------------------------------------------------------------
// Runtime function categories
//
// The runtime groups functions into categories for documentation,
// code generation hints, and linking.
// ---------------------------------------------------------------------------

// Category constants identify the functional group of a runtime symbol.
const (
        CatMemory  = "memory"
        CatValues  = "values"
        CatMath    = "math"
        CatTables  = "tables"
        CatCore    = "core"
        CatStrings = "strings"
        CatSys     = "sys"
        CatFS      = "fs"
        CatPath    = "path"
        CatJSON    = "json"
        CatEntry   = "entry"
)

// ModuleCategories maps module names to their category constant.
var ModuleCategories = map[string]string{
        "sys":  CatSys,
        "fs":   CatFS,
        "path": CatPath,
        "json": CatJSON,
}

// AllRuntimeSymbolNames returns a sorted list of all known runtime symbol
// names (y_-prefixed).  This is used by the linker to pre-populate the
// symbol table.
func AllRuntimeSymbolNames() []string {
        seen := make(map[string]bool)
        var names []string

        add := func(s string) {
                if !seen[s] {
                        seen[s] = true
                        names = append(names, s)
                }
        }

        // Built-ins
        for _, sym := range BuiltinMapping {
                add(sym)
        }
        // Explicit additions that may not be in BuiltinMapping yet
        add("y_println")
        // Module functions
        for _, sym := range ModuleMapping {
                add(sym)
        }
        // Table methods
        for _, sym := range TableMethodMapping {
                add(sym)
        }
        // String methods
        for _, sym := range StringMethodMapping {
                add(sym)
        }
        // Extra runtime-internal symbols not exposed at source level
        add("y_arena_push")
        add("y_arena_pop")
        add("y_alloc")
        add("y_free")
        add("y_str_new")
        add("y_str_concat")
        add("y_to_str")
        add("y_to_int")
        add("y_to_fp")
        add("y_fp_new")
        add("y_fp_add")
        add("y_fp_sub")
        add("y_fp_mul")
        add("y_fp_div")
        add("y_fp_neg")
        add("y_val_eq")
        add("y_enum_eq")
        add("y_str_concat")
        add("yilt_run")
        add("yilt_main_abi")
        add("y_table_new")
        add("y_tab_set")
        add("y_tab_get")
        add("y_tab_has")
        add("y_tab_del")
        add("y_tab_len")
        add("y_tab_get_val_type")
        add("y_tab_iter_valid")
        add("y_tab_iter_key")
        add("y_tab_iter_val")
        add("y_tab_iter_next")
        add("y_str_split")
        add("y_str_replace")
        add("y_str_bytes")
        add("y_str_to_int")
        add("y_str_to_float")
        add("y_str_repeat")
        add("y_sys_argc")
        add("y_sys_getenv")
        add("y_sys_setenv")
        add("y_sys_clock")
        add("y_sys_sleep")
        add("y_fs_is_file")
        add("y_fs_is_dir")
        add("y_fs_file_size")
        add("y_path_sep")
        add("y_path_sep_posix")
        add("y_path_sep_win")

        return names
}
