# Yilt Self-Host Compiler

> A Yilt-to-Yilt compiler: the Yilt compiler rewritten **in Yilt itself**.

## Status: Stage 0 Complete

Stage 0 is a working expression calculator (lexer + Pratt parser + tree-walking
evaluator) written entirely in Yilt, compiled by the Go-based `yiltc`, and
producing correct results for all 17 test cases.

## Quick Start

```bash
cd /home/z/my-project/yiltc
./bin/yiltc yilt-selfhost/src/stage0/calc.yilt -o /tmp/stage0 --quiet
/tmp/stage0
```

Expected output:
```
=== Yilt Self-Host Stage 0: Expression Calculator ===
1 + 2 => 3
2 + 3 * 4 => 14
(2 + 3) * 4 => 20
2 + 3 * (4 - 1) => 11
10 - 2 - 3 => 5
100 / 7 => 14
100 % 7 => 2
2 < 3 => 1
3 < 2 => 0
1 == 1 => 1
1 != 2 => 1
true and false => 0
true or false => 1
not true => 0
-5 + 10 => 5
2 + 3 == 5 => 1
(1 + 2) * (3 + 4) - 10 => 11
=== Stage 0 complete ===
```

## Architecture

```
yilt-selfhost/
├── README.md                      # this file
└── src/
    └── stage0/
        └── calc.yilt              # Stage 0: expression calculator
```

### Stage 0 (`calc.yilt`) — ~500 lines of Yilt

A complete expression calculator demonstrating:

1. **Lexer** (`lex_all`, `lex_one`, `lex_number`, `lex_ident`)
   - Character classification (`is_digit`, `is_alpha`, `is_alnum`)
   - Tokenises integers, identifiers, keywords, operators, parens
   - Tracks line/column for future diagnostics
   - Returns tokens as a table of `Token` structs (integer-keyed)

2. **Pratt Parser** (`parse_expr` → `parse_or` → `parse_and` → ... → `parse_primary`)
   - Full operator precedence: `or` < `and` < `==`/`!=` < `<`/`<=`/`>`/`>=` < `+`/`-` < `*`/`/`/`%` < unary
   - Left-associative binary operators
   - Right-associative unary operators (`-`, `not`)
   - Parenthesised sub-expressions
   - AST nodes are `Node` structs with a `kind` string discriminator

3. **Tree-Walking Evaluator** (`eval`)
   - Recursively evaluates the AST
   - Handles all operators: `+`, `-`, `*`, `/`, `%`, `==`, `!=`, `<`, `<=`, `>`, `>=`, `and`, `or`, `not`, unary `-`
   - Returns integers (0 = false, 1 = true for boolean ops)

### Data Structures Used

Yilt doesn't yet support recursive struct types (a struct field can't hold
another struct of the same type), so AST child nodes are wrapped in
single-element tables:

```yilt
struct Node
    kind str       # "int", "bool", "unary", "binary", "error"
    value int      # for "int" and "bool"
    op str         # for "unary" and "binary": the operator text
    left table     # for "binary": {0: Node} (wrapped child)
    right table    # for "binary": {0: Node}
    expr table     # for "unary": {0: Node}
    msg str        # for "error"
```

Stage 1 will switch to proper recursive enums once the runtime supports them.

## Bootstrap Strategy

### Stage 0 (COMPLETE) — Expression Calculator
- Lexer + Pratt parser + evaluator for arithmetic/boolean expressions
- Written in Yilt, compiled by Go `yiltc`
- 17/17 test cases pass
- Demonstrates Yilt can express: character classification, tokenisation,
  recursive-descent parsing, AST traversal, recursion, struct/table usage

### Stage 1 (NEXT) — Yilt Subset Lexer
- Extend the lexer to handle full Yilt source: keywords, string literals,
  f-strings, indentation tokens, comments
- Output a token stream that can be consumed by a parser
- ~2000 lines of Yilt

### Stage 2 — Yilt Subset Parser
- Parse the token stream into a full Yilt AST
- Handle: fn declarations, struct/enum declarations, let bindings, if/but/else,
  while, for-in, match, expressions, closures
- ~3000 lines of Yilt

### Stage 3 — Yilt Subset Type Checker
- Type inference, scope management, generic monomorphisation
- ~2000 lines of Yilt

### Stage 4 — Yilt Subset Code Generator
- Emit x86_64 machine code for a subset of Yilt
- Output ELF64 binaries via the existing linker (or a Yilt-written one)
- ~3000 lines of Yilt

### Stage 5 — Self-Compilation (Fixpoint)
- The Stage 4 compiler compiles itself
- Verify: `yiltc compiles selfhost.ylt → binary1; binary1 compiles selfhost.ylt → binary2; binary1 == binary2`

## Runtime Bugs Fixed During Stage 0

Writing the self-host compiler exposed four real runtime bugs that hadn't
been caught by the existing test suite:

1. **String equality (`==` on `str`)** — was using bitwise comparison
   of tagged values (pointer identity), so two separate allocations of
   "int" were never equal.  Fixed: `==` and `!=` on strings now call
   `pure_values_equal` for content-based comparison.

2. **Boolean NOT (`not` on `bool`)** — was using bitwise NOT (`~x`)
   which corrupts the tag bits of tagged bools.  Fixed: `not` on bools
   now uses `XOR x, 1` to flip just the payload bit.

3. **Short-circuit AND/OR** — `and` used `b.Not(left)` (bitwise) to
   check if left was false, which corrupted the tag.  Fixed: branch
   directly on `left` (truthy = evaluate right, falsy = skip).

4. **Conditional branch (`if`/`while` conditions)** — `genCondJump`
   used `TEST cond, cond` which checks if the full 64-bit value is
   non-zero.  But tagged `false` = `0x0200000000000000` is non-zero
   (the tag byte is 0x02)!  Fixed: `TEST cond, 1` checks only the
   payload bit, correctly distinguishing true (1) from false (0).

5. **memcmp (`pure_values_equal` string comparison)** — `emitMemcmp`
   emitted `0xF2 0xA6` (REPNE CMPSB) instead of `0xF3 0xA6` (REPE
   CMPSB).  REPNE stops at the first MATCHING byte, so "abc"=="abd"
   returned true because byte 0 ('a') matched.  Fixed: use REPE which
   stops at the first MISMATCH.

All five bugs are now fixed.  The Go test suite (240/240 tests) still
passes with no regressions.
