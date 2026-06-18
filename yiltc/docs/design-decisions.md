# Yilt Design Decisions

This file records language design decisions made during development.
Each entry includes the date, the decision, the rationale, and the status.

---

## D001: Remove `const` keyword (2026-06-18)

**Status:** APPROVED — implementation in progress

**Decision:** Remove `const` as a keyword from Yilt. Top-level bindings
use `let` (immutable by default) and `let mut` (mutable). There is no
separate `const` declaration.

**Rationale:** Yilt's core design principle is "everything is immutable
by default; `mut` opts into mutability." This makes `const` redundant:
- `let x = 42` at top level is already immutable — it IS a constant.
- `let mut x = 42` at top level is mutable.
- A separate `const` keyword adds cognitive load without adding power.

The original spec included `const` for C/Rust familiarity, but it
contradicts Yilt's simpler model. Removing it keeps the keyword count
down (30 keywords instead of 31) and reinforces the "immutability by
default" principle.

**Impact:**
- Lexer: `const` is no longer a keyword (becomes a regular identifier).
- Parser: `parseTopConst` is removed; top-level `let` already handles
  constant bindings via `ConstDecl` internally.
- Checker: `registerConst` still works (it's called for top-level `let`
  too), but the `TConst` token path is removed.
- Tests: `testsuite/advanced/const_decl.yilt` updated to use `let`.
- Self-host: Stage 1/2 lexer/parser updated to remove `TConst`.

**Migration:** Any existing `const x = 42` becomes `let x = 42`.

---

## D002: `but` instead of `else if` (2026-06-18)

**Status:** KEEP (for now)

**Decision:** Yilt uses `but` for else-if chains instead of `else if`.

**Rationale:** `but` is a single token, simplifying the parser (no
two-keyword sequence to handle). It also contributes to the 2-char
prefix uniqueness property: `bu` is unique among all keyword prefixes.

**Future consideration:** If users find `but` unfamiliar, it can be
changed to `else if` without major disruption. The parser already
handles both forms internally (the `but` branch and a hypothetical
`else if` branch would use the same logic). Decide before public release.

---

## D003: No-arrow rule for return types (2026-06-18)

**Status:** ENFORCED

**Decision:** Function return types use a bare type after `)`:
`fn foo() int`, NOT `fn foo() -> int`.

**Rationale:** Keeps function-signature parsing consistent with
parameter syntax (`name type`, not `name: type` or `name -> type`).
Reduces the number of operators the parser must handle in signature
position. Tuple returns use bare parens: `fn foo() (int, str)`.

---

## D004: 2-char prefix uniqueness for keywords (2026-06-18)

**Status:** OBSERVED (not yet utilized)

**Observation:** 29 of 30 Yilt keyword 2-char prefixes are unique.
The only collision is `co` → `continue`/`const`. With `const` removed
(D001), the remaining collision is `continue` alone (no conflict).

**Future optimization:** The lexer could use a 2-char lookup table for
O(1) keyword recognition instead of the current string-comparison chain.
This is a future optimization — not needed for correctness, but it
would make the lexer faster and simpler. Defer until the compiler is
more mature.
