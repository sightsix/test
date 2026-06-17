package runtime

// ---------------------------------------------------------------------------
// C Runtime Code Embedder
//
// This file embeds the pre-compiled C runtime (runtime.c) as raw machine code
// and provides functions to look up individual runtime functions by name.
//
// The C runtime is compiled to an ELF object file (runtime.o) with:
//   gcc -c -O2 -fno-pic -fno-stack-protector -nostdlib -o runtime.o runtime.c
//
// The .text section is extracted as raw binary (runtime.bin) and embedded here.
// The .rodata.str1.1 and .rodata sections contain string literals and jump
// tables used by the runtime functions.
//
// Build process:
//   1. Compile runtime.c → runtime.o
//   2. Extract sections: objcopy -O binary -j .text runtime.o runtime.bin
//   3. Extract rodata:   objcopy -O binary -j .rodata.str1.1 runtime.o rodata_str.bin
//   4. Extract rodata:   objcopy -O binary -j .rodata runtime.o rodata.bin
//   5. Extract constants: objcopy -O binary -j .rodata.cst8 runtime.o rodata_cst8.bin
//   6. Extract constants: objcopy -O binary -j .rodata.cst16 runtime.o rodata_cst16.bin
//   7. Get symbols:      nm -n runtime.o | grep ' T '
//   8. Get relocs:       objdump -r runtime.o
//
// Then update the symbol table and relocation table below to match.
// ---------------------------------------------------------------------------

import _ "embed"

// ---------------------------------------------------------------------------
// Embedded binary data
// ---------------------------------------------------------------------------

// runtimeText is the raw .text section from the compiled C runtime.
// It contains the machine code for all runtime functions.
//
//go:embed cruntime/runtime.bin
var runtimeText []byte

// runtimeRodataStr is the combined .rodata.str1.1 + .rodata.str1.8 sections
// containing string literals ("true", "false", "nil", error messages, etc.).
// The .rodata.str1.1 content comes first (334 bytes), followed by .rodata.str1.8
// (34 bytes). References to .rodata.str1.8 use addend >= 0x14e.
//
//go:embed cruntime/rodata_str.bin
var runtimeRodataStr []byte

// runtimeRodataCst4 is the .rodata.cst4 section containing 4-byte constants
// ("-inf" and "+inf" string fragments used by float formatting).
//
//go:embed cruntime/rodata_cst4.bin
var runtimeRodataCst4 []byte

// runtimeRodata is the .rodata section containing the y_type_of switch table
// (array of function pointers for type name dispatch).
//
//go:embed cruntime/rodata.bin
var runtimeRodata []byte

// runtimeRodataCst8 is the .rodata.cst8 section containing 8-byte constant
// values (doubles) used by float operations (1.0, 400.0, 10.0).
//
//go:embed cruntime/rodata_cst8.bin
var runtimeRodataCst8 []byte

// runtimeRodataCst16 is the .rodata.cst16 section containing 16-byte constant
// values (x87 extended precision) used by float operations (-0.0).
//
//go:embed cruntime/rodata_cst16.bin
var runtimeRodataCst16 []byte

// ---------------------------------------------------------------------------
// Symbol table
//
// Maps runtime function names to their offset and size within runtimeText.
// The offsets are extracted from `nm -n runtime.o` and sizes are computed
// as the difference between consecutive symbol addresses.
//
// IMPORTANT: If runtime.c is modified and recompiled, these values MUST
// be updated to match the new nm output.
// ---------------------------------------------------------------------------

// runtimeSymbol describes a function's location within the embedded .text.
type runtimeSymbol struct {
        Name   string // linker-visible name (e.g. "y_print")
        Offset uint64 // byte offset within runtimeText
        Size   uint64 // byte count
}

// runtimeSymbolTable lists all functions in the embedded .text section,
// sorted by offset. Generated from: nm -n runtime.o | grep ' [tT] '
var runtimeSymbolTable = []runtimeSymbol{
        {"hash_tagged", 0x0000, 0x0108},
        {"values_equal.part.0", 0x0110, 0x006e},
        {"y_tab_set.part.0", 0x0180, 0x034d},
        {"y_print", 0x04d0, 0x04c1},
        {"y_println", 0x09a0, 0x0020},
        {"y_str_new", 0x09c0, 0x00ae},
        {"y_sys_exit", 0x0a70, 0x000a},
        {"y_table_new", 0x0a80, 0x015f},
        {"y_tab_set", 0x0be0, 0x0023},
        {"y_tab_get", 0x0c10, 0x00fa},
        {"y_tab_has", 0x0d10, 0x0102},
        {"y_tab_del", 0x0e20, 0x013f},
        {"y_tab_len", 0x0f60, 0x0030},
        {"y_tab_get_val_type", 0x0f90, 0x0132},
        {"y_tab_iter_valid", 0x10d0, 0x0054},
        {"y_tab_iter_key", 0x1130, 0x0053},
        {"y_tab_iter_val", 0x1190, 0x0053},
        {"y_tab_iter_next", 0x11f0, 0x0078},
        {"y_arena_push", 0x1270, 0x0003},
        {"y_arena_pop", 0x1280, 0x0001},
        {"y_alloc", 0x1290, 0x0046},
        {"y_free", 0x12e0, 0x0001},
        {"y_fp_new", 0x12f0, 0x005b},
        {"y_fp_add", 0x1350, 0x00b9},
        {"y_fp_sub", 0x1410, 0x00b9},
        {"y_fp_mul", 0x14d0, 0x00b9},
        {"y_fp_div", 0x1590, 0x00b9},
        {"y_fp_neg", 0x1650, 0x008e},
        {"y_copy", 0x16e0, 0x0004},
        {"y_promote", 0x16f0, 0x0004},
        {"y_val_eq", 0x1700, 0x0016},
        {"y_type_of", 0x1720, 0x0086},
        {"y_str_concat", 0x17b0, 0x0156},
        {"y_abs", 0x1910, 0x0004},
        {"y_neg", 0x1920, 0x001b},
        {"y_min", 0x1940, 0x000b},
        {"y_max", 0x1950, 0x000b},
        {"y_sqrt", 0x1960, 0x0003},
        {"y_floor", 0x1970, 0x0003},
        {"y_ceil", 0x1980, 0x0003},
        {"y_round", 0x1990, 0x0003},
        {"y_sign", 0x19a0, 0x0030},
        {"y_clamp", 0x19d0, 0x0012},
        {"y_input", 0x19f0, 0x0100},
        {"y_len", 0x1af0, 0x0037},
        {"y_panic", 0x1b30, 0x0066},
        {"y_assert", 0x1ba0, 0x0027},
        {"y_error", 0x1bd0, 0x001e},
        {"y_is_error", 0x1bf0, 0x001b},
        {"y_trim", 0x1c10, 0x00d8},
        {"y_lower", 0x1cf0, 0x0106},
        {"y_upper", 0x1e00, 0x0116},
        {"y_substr", 0x1f20, 0x00ea},
        {"y_contains", 0x2010, 0x00cb},
        {"y_starts_with", 0x20e0, 0x0093},
        {"y_ends_with", 0x2180, 0x00ab},
        {"y_find", 0x2230, 0x00eb},
        {"y_str_split", 0x2320, 0x01f5},
        {"y_str_replace", 0x2520, 0x0342},
        {"y_str_bytes", 0x2870, 0x00a0},
        {"y_str_to_int", 0x2910, 0x015e},
        {"y_str_to_float", 0x2a70, 0x0346},
        {"y_str_repeat", 0x2dc0, 0x023e},
        {"y_sys_args", 0x3000, 0x0003},
        {"y_sys_argc", 0x3010, 0x000b},
        {"y_sys_cwd", 0x3020, 0x000b},
        {"y_sys_platform", 0x3030, 0x0006},
        {"y_sys_env", 0x3040, 0x0003},
        {"y_sys_getenv", 0x3050, 0x0003},
        {"y_sys_clock", 0x3060, 0x0003},
        {"y_sys_sleep", 0x3070, 0x0001},
        {"y_fs_exists", 0x3080, 0x000b},
        {"y_fs_read_text", 0x3090, 0x0003},
        {"y_fs_write_text", 0x30a0, 0x0001},
        {"y_fs_append_text", 0x30b0, 0x0001},
        {"y_fs_read_lines", 0x30c0, 0x0003},
        {"y_fs_remove", 0x30d0, 0x000b},
        {"y_fs_rename", 0x30e0, 0x000b},
        {"y_fs_copy", 0x30f0, 0x000b},
        {"y_fs_mkdir", 0x3100, 0x000b},
        {"y_fs_rmdir", 0x3110, 0x000b},
        {"y_fs_read_dir", 0x3120, 0x0003},
        {"y_fs_is_file", 0x3130, 0x000b},
        {"y_fs_is_dir", 0x3140, 0x000b},
        {"y_fs_file_size", 0x3150, 0x000b},
        {"y_path_normalize", 0x3160, 0x0004},
        {"y_path_resolve", 0x3170, 0x0004},
        {"y_path_resolve2", 0x3180, 0x0004},
        {"y_path_relative", 0x3190, 0x0003},
        {"y_path_join", 0x31a0, 0x0004},
        {"y_path_dirname", 0x31b0, 0x0004},
        {"y_path_parent", 0x31c0, 0x0004},
        {"y_path_basename", 0x31d0, 0x0004},
        {"y_path_stem", 0x31e0, 0x0004},
        {"y_path_extname", 0x31f0, 0x0003},
        {"y_path_is_abs", 0x3200, 0x000b},
        {"y_path_sep", 0x3210, 0x0006},
        {"y_path_sep_posix", 0x3220, 0x0006},
        {"y_path_sep_win", 0x3230, 0x0006},
        {"y_json_encode", 0x3240, 0x0003},
        {"y_json_decode", 0x3250, 0x0003},
}

// ---------------------------------------------------------------------------
// Relocation types
// ---------------------------------------------------------------------------

// RelocType identifies the type of a relocation entry.
type RelocType int

const (
        // RelocAbs32 is R_X86_64_32: absolute unsigned 32-bit relocation.
        // The linker writes (target_address) as a 4-byte unsigned value at offset.
        RelocAbs32 RelocType = iota

        // RelocAbs32S is R_X86_64_32S: absolute signed 32-bit relocation.
        // The linker writes (target_address) as a 4-byte signed value at offset.
        RelocAbs32S

        // RelocPC32 is R_X86_64_PC32: PC-relative 32-bit relocation.
        // The linker writes (target_address - patch_offset) as a 4-byte signed
        // value at offset. The addend is already baked into the instruction.
        RelocPC32

        // RelocPLT32 is R_X86_64_PLT32: PC-relative 32-bit relocation with
        // implicit -4 addend (from the end of the 4-byte field). The linker
        // writes (target_address - (patch_offset + 4)) as a 4-byte value.
        RelocPLT32
)

// RelocTarget identifies what a relocation references.
type RelocTarget int

const (
        // TargetRodataStr references the .rodata.str1.1 section (string literals).
        TargetRodataStr RelocTarget = iota

        // TargetRodata references the .rodata section (jump tables, etc.).
        TargetRodata

        // TargetText references another function within .text.
        TargetText

        // TargetRodataCst8 references the .rodata.cst8 section (8-byte float constants).
        TargetRodataCst8

        // TargetRodataCst16 references the .rodata.cst16 section (16-byte float constants).
        TargetRodataCst16

        // TargetRodataCst4 references the .rodata.cst4 section (4-byte constants,
        // e.g. "-inf" and "+inf" string fragments used by float formatting).
        TargetRodataCst4
)

// RuntimeReloc describes a single relocation that the linker must apply
// to the embedded runtime code.
type RuntimeReloc struct {
        // Offset is the byte offset within the .text section where the
        // 4-byte relocation target should be written.
        Offset uint64

        // Type is the relocation type (absolute, PC-relative, etc.).
        Type RelocType

        // Target identifies which section/symbol the relocation references.
        Target RelocTarget

        // Addend is added to the target base address. For example, a relocation
        // referencing .rodata.str1.1+0x0b has Addend=0x0b.
        Addend uint64

        // Symbol is the name of the target symbol (for TargetText relocations).
        // For section references (TargetRodataStr, TargetRodata), this is empty.
        Symbol string
}

// ---------------------------------------------------------------------------
// Relocation table
//
// Generated from: objdump -r runtime.o (RELOCATION RECORDS FOR [.text])
//
// IMPORTANT: If runtime.c is modified and recompiled, this table MUST be
// updated to match the new relocations.
// ---------------------------------------------------------------------------
var runtimeRelocations = []RuntimeReloc{
        // y_print: "[table]" at .rodata.str1.1+0x1b
        {0x0471, RelocAbs32, TargetRodataStr, 0x1b, ""},
        // y_print: "true" at .rodata.str1.1+0x00
        {0x0494, RelocAbs32, TargetRodataStr, 0x0, ""},
        // y_print: .rodata.str1.8 ("table_alloc_entries: mmap failed\n") at combined+0x14e
        {0x04b6, RelocAbs32, TargetRodataStr, 0x14e, ""},
        // y_print: references .rodata (hash table at .rodata+0x00)
        {0x04e2, RelocAbs32S, TargetRodata, 0x0, ""},
        // y_print: "false" at .rodata.str1.1+0x3d
        {0x04f6, RelocAbs32, TargetRodataStr, 0x3d, ""},
        // y_print: "nil" at .rodata.str1.1+0x39
        {0x050e, RelocAbs32, TargetRodataStr, 0x39, ""},
        // y_print: "table" at .rodata.str1.1+0x2e
        {0x05e8, RelocAbs32, TargetRodataStr, 0x2e, ""},
        // y_print: "str" at .rodata.str1.1+0x33
        {0x0661, RelocAbs32, TargetRodataStr, 0x33, ""},
        // y_print TAG_FP: .LC8 (1.0) at .rodata.cst8+0x00
        {0x0687, RelocPC32, TargetRodataCst8, 0x0, ""},
        // y_print TAG_FP: .LC10 (-0.0) at .rodata.cst16+0x00
        {0x06a3, RelocPC32, TargetRodataCst16, 0x0, ""},
        // y_print TAG_FP: .LC3 ("-inf") at .rodata.cst4+0x00
        {0x06b3, RelocPC32, TargetRodataCst4, 0x0, ""},
        // y_print TAG_FP: .LC11 (400.0) at .rodata.cst8+0x08
        {0x06d8, RelocPC32, TargetRodataCst8, 0x8, ""},
        // y_print TAG_FP: .LC12 (10.0) at .rodata.cst8+0x10
        {0x0759, RelocPC32, TargetRodataCst8, 0x10, ""},
        // y_print TAG_FP: .LC4 ("+inf") at .rodata.cst4+0x04
        {0x07ba, RelocPC32, TargetRodataCst4, 0x4, ""},
        // y_println: call y_print (PLT32)
        {0x09a5, RelocPLT32, TargetText, 0x0, "y_print"},
        // y_println: "\n" at .rodata.str1.1+0x45
        {0x09af, RelocAbs32, TargetRodataStr, 0x45, ""},
        // y_str_new: error string at .rodata.str1.1+0x47
        {0x0a57, RelocAbs32, TargetRodataStr, 0x47, ""},
        // y_table_new: error string at .rodata.str1.1+0x5f
        {0x0ba6, RelocAbs32, TargetRodataStr, 0x5f, ""},
        // y_table_new: .rodata.str1.8 at combined+0x14e
        {0x0bc8, RelocAbs32, TargetRodataStr, 0x14e, ""},
        // y_tab_get_val_type: .LC18 at .rodata.cst16+0x10
        {0x0f0b, RelocPC32, TargetRodataCst16, 0x10, ""},
        // y_tab_get_val_type: call y_tab_get (PLT32)
        {0x0fa1, RelocPLT32, TargetText, 0x0, "y_tab_get"},
        // y_fp_neg: .LC20 (-0.0) at .rodata.cst16+0x00
        {0x1657, RelocPC32, TargetRodataCst16, 0x0, ""},
        // y_fp_neg: .LC10 (-0.0) at .rodata.cst16+0x00
        {0x16d8, RelocPC32, TargetRodataCst16, 0x0, ""},
        // y_type_of: references .rodata+0x50 (type name pointers)
        {0x172c, RelocAbs32S, TargetRodata, 0x50, ""},
        // y_type_of: "unknown" at .rodata.str1.1+0x9a
        {0x1731, RelocAbs32, TargetRodataStr, 0x9a, ""},
        // y_type_of: "void" at .rodata.str1.1+0x79
        {0x1741, RelocAbs32, TargetRodataStr, 0x79, ""},
        // y_type_of: "int" at .rodata.str1.1+0x7e
        {0x1751, RelocAbs32, TargetRodataStr, 0x7e, ""},
        // y_type_of: "bool" at .rodata.str1.1+0x82
        {0x1761, RelocAbs32, TargetRodataStr, 0x82, ""},
        // y_type_of: "fp" at .rodata.str1.1+0x87
        {0x1771, RelocAbs32, TargetRodataStr, 0x87, ""},
        // y_type_of: "str" at .rodata.str1.1+0x8a
        {0x1781, RelocAbs32, TargetRodataStr, 0x8a, ""},
        // y_type_of: "table" at .rodata.str1.1+0x8e
        {0x1791, RelocAbs32, TargetRodataStr, 0x8e, ""},
        // y_type_of: "error" at .rodata.str1.1+0x94
        {0x17a1, RelocAbs32, TargetRodataStr, 0x94, ""},
        // y_str_concat: .rodata.str1.8 at combined+0x14e
        {0x18ef, RelocAbs32, TargetRodataStr, 0xa2, ""},
        // y_str_concat: call y_str_new (PLT32)
        {0x1a40, RelocPLT32, TargetText, 0x0, "y_str_new"},
        // y_str_concat: error string at .rodata.str1.1+0x47
        {0x1ad9, RelocAbs32, TargetRodataStr, 0x47, ""},
        // y_panic: "panic: " at .rodata.str1.1+0xbd
        {0x1b3a, RelocAbs32, TargetRodataStr, 0xbd, ""},
        // y_panic: "\n" at .rodata.str1.1+0x45
        {0x1b65, RelocAbs32, TargetRodataStr, 0x45, ""},
        // y_assert: call y_panic (PLT32)
        {0x1bc3, RelocPLT32, TargetText, 0x0, "y_panic"},
        // y_lower: .rodata.str1.8 at combined+0x14e
        {0x1ddf, RelocAbs32, TargetRodataStr, 0xc5, ""},
        // y_upper: .rodata.str1.8 at combined+0x14e
        {0x1eff, RelocAbs32, TargetRodataStr, 0xdb, ""},
        // y_starts_with: error string at .rodata.str1.1+0x47
        {0x1ff3, RelocAbs32, TargetRodataStr, 0x47, ""},
        // y_str_split: call y_table_new (PLT32)
        {0x2387, RelocPLT32, TargetText, 0x0, "y_table_new"},
        // y_str_split: call y_str_new (PLT32)
        {0x247f, RelocPLT32, TargetText, 0x0, "y_str_new"},
        // y_str_split: call y_str_new (PLT32)
        {0x24d2, RelocPLT32, TargetText, 0x0, "y_str_new"},
        // y_str_to_float: string at .rodata.str1.1+0xf1
        {0x2829, RelocAbs32, TargetRodataStr, 0xf1, ""},
        // y_str_to_float: error string at .rodata.str1.1+0x47
        {0x284b, RelocAbs32, TargetRodataStr, 0x47, ""},
        // y_str_to_float: call y_table_new (PLT32)
        {0x28c5, RelocPLT32, TargetText, 0x0, "y_table_new"},
        // y_str_to_float: .LC12 (10.0) at .rodata.cst8+0x10
        {0x2af2, RelocPC32, TargetRodataCst8, 0x10, ""},
        // y_str_to_float: .LC8 (1.0) at .rodata.cst8+0x00
        {0x2bc9, RelocPC32, TargetRodataCst8, 0x0, ""},
        // y_str_to_float: .LC12 (10.0) at .rodata.cst8+0x10
        {0x2bd1, RelocPC32, TargetRodataCst8, 0x10, ""},
        // y_str_to_float: .LC10 (-0.0) at .rodata.cst16+0x00
        {0x2c19, RelocPC32, TargetRodataCst16, 0x0, ""},
        // y_str_to_float: .LC20 (-0.0) at .rodata.cst16+0x00
        {0x2c98, RelocPC32, TargetRodataCst16, 0x0, ""},
        // y_str_to_float: .LC8 (1.0) at .rodata.cst8+0x00
        {0x2cd0, RelocPC32, TargetRodataCst8, 0x0, ""},
        // y_str_to_float: .LC34 (0.1) at .rodata.cst8+0x18
        {0x2cd8, RelocPC32, TargetRodataCst8, 0x18, ""},
        // y_str_to_float: .LC8 (1.0) at .rodata.cst8+0x00
        {0x2d51, RelocPC32, TargetRodataCst8, 0x0, ""},
        // y_str_to_float: .LC34 (0.1) at .rodata.cst8+0x18
        {0x2d59, RelocPC32, TargetRodataCst8, 0x18, ""},
        // y_str_repeat: error string at .rodata.str1.1+0x47
        {0x2f7f, RelocAbs32, TargetRodataStr, 0x47, ""},
        // y_str_repeat: error string at .rodata.str1.1+0x47
        {0x2fa1, RelocAbs32, TargetRodataStr, 0x47, ""},
        // y_str_repeat: "panic: str_repeat overflow\n" at .rodata.str1.1+0x10d
        {0x2fc4, RelocAbs32, TargetRodataStr, 0x10d, ""},
        // y_str_repeat: .rodata.str1.8 at combined+0x14e
        {0x2fe7, RelocAbs32, TargetRodataStr, 0x129, ""},
        // y_str_repeat: "panic: " at .rodata.str1.1+0x144
        {0x3031, RelocAbs32, TargetRodataStr, 0x144, ""},
        // y_path_sep_posix: "/" at .rodata.str1.1+0x14a
        {0x3211, RelocAbs32, TargetRodataStr, 0x14a, ""},
        // y_path_sep_win: "/" at .rodata.str1.1+0x14a
        {0x3221, RelocAbs32, TargetRodataStr, 0x14a, ""},
        // y_path_sep_win: "\\" at .rodata.str1.1+0x14c
        {0x3231, RelocAbs32, TargetRodataStr, 0x14c, ""},
}

// ---------------------------------------------------------------------------
// .rodata relocations (within the .rodata section itself)
//
// The .rodata section contains function pointer tables (for y_type_of) that
// reference functions within .text. These are R_X86_64_64 (absolute 64-bit)
// relocations.
// ---------------------------------------------------------------------------

// RuntimeRodataReloc describes a 64-bit absolute relocation within .rodata.
// Each entry maps a .rodata offset to a .text byte offset (the target address).
type RuntimeRodataReloc struct {
        // Offset is the byte offset within the .rodata section where the
        // 8-byte pointer should be written.
        Offset uint64

        // TextOffset is the absolute byte position within the .text section
        // that this rodata entry should point to. The linker resolves this
        // via resolveRuntimeFunc to find the target function section and
        // the offset within that section.
        TextOffset uint64
}

var runtimeRodataRelocations = []RuntimeRodataReloc{
        // Generated from: objdump -r runtime.o (RELOCATION RECORDS FOR [.rodata])
        // All entries are R_X86_64_64 (absolute 64-bit) referencing .text section symbol.
        {0x0000, 0x0508},
        {0x0008, 0x0520},
        {0x0010, 0x05d0},
        {0x0018, 0x0620},
        {0x0020, 0x05f8},
        {0x0028, 0x04f0},
        {0x0030, 0x0504},
        {0x0038, 0x0504},
        {0x0040, 0x0504},
        {0x0048, 0x0508},
        {0x0050, 0x1740},
        {0x0058, 0x1750},
        {0x0060, 0x1760},
        {0x0068, 0x1770},
        {0x0070, 0x1780},
        {0x0078, 0x1790},
        {0x0080, 0x1730},
        {0x0088, 0x1730},
        {0x0090, 0x1730},
        {0x0098, 0x1730},
        {0x00a0, 0x1730},
        {0x00a8, 0x1730},
        {0x00b0, 0x1730},
        {0x00b8, 0x1730},
        {0x00c0, 0x17a0},
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// GetRuntimeText returns the full .text section as raw bytes.
// This is the machine code for all runtime functions concatenated.
func GetRuntimeText() []byte {
        return runtimeText
}

// GetRuntimeRodataStr returns the .rodata.str1.1 section (string literals).
func GetRuntimeRodataStr() []byte {
        return runtimeRodataStr
}

// GetRuntimeRodata returns the .rodata section (jump tables, etc.).
func GetRuntimeRodata() []byte {
        return runtimeRodata
}

// GetRuntimeRodataCst8 returns the .rodata.cst8 section (8-byte float constants
// such as 1.0, 400.0, 10.0 used by float operations).
func GetRuntimeRodataCst8() []byte {
        return runtimeRodataCst8
}

// GetRuntimeRodataCst16 returns the .rodata.cst16 section (16-byte float constants
// such as -0.0 used by float operations).
func GetRuntimeRodataCst16() []byte {
        return runtimeRodataCst16
}

// GetRuntimeRodataCst4 returns the .rodata.cst4 section (4-byte constants
// such as "-inf" and "+inf" used by float formatting).
func GetRuntimeRodataCst4() []byte {
        return runtimeRodataCst4
}

// GetRuntimeCode returns a map from runtime function name to its raw
// machine code bytes. The bytes are sliced from the embedded .text section.
//
// Example:
//
//      code := GetRuntimeCode()
//      printBytes := code["y_print"] // []byte of the y_print function
//
// NOTE: The returned byte slices are NOT independent copies. They share
// the underlying array of the embedded .text section. If you need to
// modify them, make a copy first.
func GetRuntimeCode() map[string][]byte {
        m := make(map[string][]byte, len(runtimeSymbolTable))
        for _, sym := range runtimeSymbolTable {
                end := sym.Offset + sym.Size
                if end > uint64(len(runtimeText)) {
                        end = uint64(len(runtimeText))
                }
                m[sym.Name] = runtimeText[sym.Offset:end]
        }
        return m
}

// GetFunctionOffset returns the offset of the named function within the
// .text section, or -1 if the function is not found.
func GetFunctionOffset(name string) int64 {
        for _, sym := range runtimeSymbolTable {
                if sym.Name == name {
                        return int64(sym.Offset)
                }
        }
        return -1
}

// GetFunctionSize returns the size in bytes of the named function, or 0
// if the function is not found.
func GetFunctionSize(name string) uint64 {
        for _, sym := range runtimeSymbolTable {
                if sym.Name == name {
                        return sym.Size
                }
        }
        return 0
}

// GetAllSymbolNames returns the names of all runtime functions in the
// embedded .text section, in offset order (including local helpers).
func GetAllSymbolNames() []string {
        names := make([]string, len(runtimeSymbolTable))
        for i, sym := range runtimeSymbolTable {
                names[i] = sym.Name
        }
        return names
}

// GetFunctionCode returns the raw machine code bytes for the named function.
// Returns nil if the function is not found.
func GetFunctionCode(name string) []byte {
        for _, sym := range runtimeSymbolTable {
                if sym.Name == name {
                        end := sym.Offset + sym.Size
                        if end > uint64(len(runtimeText)) {
                                end = uint64(len(runtimeText))
                        }
                        return runtimeText[sym.Offset:end]
                }
        }
        return nil
}

// GetRuntimeRelocations returns the list of relocations that must be
// applied to the .text section. The linker uses this to patch absolute
// and relative addresses for string literals and inter-function calls.
func GetRuntimeRelocations() []RuntimeReloc {
        return runtimeRelocations
}

// GetRuntimeRodataRelocations returns the list of 64-bit relocations
// within the .rodata section. These are typically function pointer
// tables used by switch statements (e.g., y_type_of).
func GetRuntimeRodataRelocations() []RuntimeRodataReloc {
        return runtimeRodataRelocations
}

// ---------------------------------------------------------------------------
// Pure-Go Runtime Integration
//
// The following functions provide a unified view that merges pure-Go generated
// runtime functions with the C-compiled runtime. When a function is available
// from the pure-Go generator, it is used instead of the C-compiled version.
// This allows incremental migration away from the C compiler dependency.
// ---------------------------------------------------------------------------

// GetMergedFunctionCode returns the code for a function, preferring the
// pure-Go version if available, falling back to the C-compiled binary.
func GetMergedFunctionCode(name string) []byte {
        if code := PuregenGetFunctionCode(name); code != nil {
                return code
        }
        return GetFunctionCode(name)
}

// GetMergedAllFunctions returns all function names — C-compiled functions
// plus any pure-Go functions that don't have a C equivalent.
// Functions with both versions are listed once (pure-Go preferred).
func GetMergedAllFunctions() []string {
        seen := make(map[string]bool)
        var result []string

        // Add C-compiled functions first (they define the base order)
        for _, name := range GetAllSymbolNames() {
                if !seen[name] {
                        seen[name] = true
                        result = append(result, name)
                }
        }

        // Add puregen-only functions (not already present from C)
        for _, name := range PuregenGetAllFunctions() {
                if !seen[name] {
                        seen[name] = true
                        result = append(result, name)
                }
        }

        return result
}

// ShouldUsePuregen returns true if the given function should use the
// pure-Go generated code instead of the C-compiled binary.
func ShouldUsePuregen(name string) bool {
        return PuregenGetFunctionCode(name) != nil
}

// GetMergedRodataStr returns the rodata string section. If the pure-Go
// generator has produced string data, it is appended to the C-compiled data.
func GetMergedRodataStr() []byte {
        cData := GetRuntimeRodataStr()
        goData := PuregenGetRodataStr()
        if len(goData) == 0 {
                return cData
        }
        merged := make([]byte, 0, len(cData)+len(goData))
        merged = append(merged, cData...)
        merged = append(merged, goData...)
        return merged
}

// GetMergedRelocations returns C-compiled relocations only for functions
// that do NOT have a pure-Go replacement. Relocations for puregen functions
// are handled separately via PuregenRelocationsByFunc() in the linker.
func GetMergedRelocations() []RuntimeReloc {
        // Build set of function names that have puregen replacements
        puregenNames := make(map[string]bool)
        for _, name := range PuregenGetAllFunctions() {
                puregenNames[name] = true
        }

        // Only include C-compiled relocations for functions that are still C-compiled
        var relocs []RuntimeReloc
        for _, r := range runtimeRelocations {
                // Resolve which C function this relocation belongs to
                funcName := resolveRuntimeFuncName(r.Offset)
                if funcName == "" {
                        continue // orphan relocation, skip
                }
                // Skip if the function has a puregen replacement
                if puregenNames[funcName] {
                        continue
                }
                relocs = append(relocs, r)
        }

        return relocs
}

// resolveRuntimeFuncName is like resolveRuntimeFunc but returns only the
// function name (no offset). This is a package-internal helper.
func resolveRuntimeFuncName(blobOffset uint64) string {
        for _, name := range GetAllSymbolNames() {
                start := uint64(GetFunctionOffset(name))
                end := start + GetFunctionSize(name)
                if blobOffset >= start && blobOffset < end {
                        return name
                }
        }
        // Also check exact end boundary
        for _, name := range GetAllSymbolNames() {
                start := uint64(GetFunctionOffset(name))
                if start <= blobOffset && blobOffset <= start+GetFunctionSize(name) {
                        return name
                }
        }
        return ""
}
