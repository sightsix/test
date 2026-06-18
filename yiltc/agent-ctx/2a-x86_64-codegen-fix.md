# Task 2a: Fix x86_64 Codegen — String Constants, Float Data Symbols, CompileResult

## Agent: main
## Status: COMPLETED — All tests pass

## Changes Made

### 1. `internal/codegen/x86_64/x86_64.go`

#### X86_64 struct — added 2 new fields
- `module *ir.IRModule` — reference to the IR module for accessing the string constant pool
- `dataSymbols map[string]uint64` — tracks data section labels (symbol name → offset) for linker resolution

#### `New()` constructor
- Added `dataSymbols: make(map[string]uint64)` to initialization

#### `Compile()` — return type changed + module/dataSymbols reset
- **Before**: `func (x *X86_64) Compile(module *ir.IRModule) ([]byte, []byte, []Relocation)`
- **After**: `func (x *X86_64) Compile(module *ir.IRModule) CompileResult`
- Sets `x.module = module` and `x.dataSymbols = make(map[string]uint64)` at start
- Returns `CompileResult` struct with Code, Data, Relocations, FuncAddrs, DataSymbols

#### New `CompileResult` type
```go
type CompileResult struct {
    Code        []byte
    Data        []byte
    Relocations []Relocation
    FuncAddrs   map[string]uint64
    DataSymbols map[string]uint64
}
emitConstString()
— complete rewrite (was broken)

Before: Reserved 8 zero bytes in data, MOV to load
those zeros, OR with TagStr → garbage pointer
After:

Looks up actual string from
x.module.Strings[strIdx]
Aligns data section to 8 bytes
Writes [u64 len][u8 data...] (length-prefixed) to data
section
Uses LEA dst, [rip + disp32] (opcode 0x8D) to load the
ADDRESS
Registers data symbol __strdata_{offset} in
x.dataSymbols
ORs with (TagStr << 56) to create tagged string
value


emitConstFloat()
— added data symbol registration

Extracted symbol name to variable symName
Added x.dataSymbols[symName] = dataOffset after
relocation

2.
internal/codegen/x86_64/x86_64_test.go

Added encoding/binary import (needed for string data
verification)
Updated ALL 28 test functions to use CompileResult
instead of 3 return values
Fixed variable shadowing issues (renamed result VRegs
to cmpResult, callResult, endVal,
compileResult as needed)
Enhanced TestConstString:

Verifies data section starts with u64 length = 11 for “hello
world”
Verifies actual string bytes follow the length prefix
Verifies data symbols were registered (non-empty
result.DataSymbols)

TestPrologueEpilogue now uses
result.FuncAddrs instead of
gen.GetFuncAddrs()
TestMultipleFunctions uses
result.FuncAddrs instead of
gen.GetFuncAddrs()

3. NOT Modified (as instructed)

main.go — will be updated separately to use
CompileResult
