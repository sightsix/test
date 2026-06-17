# Yilt Self-Host Compiler

> A Yilt-to-Yilt compiler: the Yilt compiler rewritten **in Yilt itself**.

## Status: Planning Phase

This project rewrites the Go-based `yiltc` compiler as a native Yilt program
that can compile Yilt source code to x86-64 ELF binaries. The output of this
compiler should be byte-for-byte compatible with (or functionally equivalent to)
the output of the Go-based `yiltc`.

## Architecture
yilt-selfhost/ ├── src/ │ ├── main.ylt # Entry point, CLI argument
parsing │ ├── lexer.ylt # Tokenizer (characters → tokens) │ ├──
parser.ylt # Parser (tokens → AST) │ ├── checker.ylt # Type checker (AST
→ typed AST) │ ├── ir.ylt # IR generation (typed AST → IR) │ ├──
optimize.ylt # IR optimization passes │ ├── codegen.ylt # x86_64 machine
code emission │ ├── linker.ylt # ELF64 linking │ └── runtime.ylt #
Runtime ABI definitions ├── tests/ │ ├── lexer_test.ylt │ ├──
parser_test.ylt │ └── e2e_test.ylt └── README.md

## Bootstrap Strategy

### Phase 1: Cross-compile (current Go compiler → Yilt source)
- Write the compiler in Yilt syntax
- Compile with the Go-based `yiltc`
- Test that output matches `yiltc` output

### Phase 2: Minimal self-host
- Get the compiler to compile a trivial Yilt program
- The output of the self-host compiler should produce a working binary

### Phase 3: Full self-host
- The self-host compiler compiles itself
- Fixpoint: `yiltc compiles selfhost.ylt → selfhost_binary; selfhost_binary compiles selfhost.ylt → same binary`

## Current Yilt Capabilities & Limitations

### Available now (can use in self-host):
- Basic types: int, float, str, bool, nil, table, void
- Functions: top-level, anonymous, recursion, closures (no capture)
- Control flow: if/but/else, while, for-in, match/case/default, break, continue
- Tables: literal, index/field access, assignment
- String interpolation: f-strings
- Struct declarations (backed by tables)
- Constants: const, local const
- Error propagation: `?` operator
- Assertions: assert
- Stdlib: sys module (args, env, platform), print/println

### NOT yet available (need to work around):
- No arrays (`[1, 2, 3]` syntax) — use tables with int keys
- No enums, traits, interfaces
- No range syntax (`0..n`)
- Closures cannot capture variables (runtime limitation)
- print() of strings shows nil at runtime (C runtime stub)
- Table literal with multiple entries can hang (C runtime bug)
- For-in loop over tables has off-by-one issues

## Design Decisions for Self-Host

1. **Single-file bootstrap**: Start with everything in one file for simplicity
2. **Table-based data structures**: Use tables for AST nodes, symbol tables, etc.
   - AST nodes: `{"kind": "fn_decl", "name": "main", "params": {0: ..., 1: ...}, ...}`
   - Token: `{"kind": "t_int", "value": 42, "line": 1, "col": 1}`
3. **String-based identifiers**: All names stored as strings
4. **Two-pass compilation**: Parse all source, then type-check/codegen
5. **No GC**: Use bump allocation via the arena API when available



