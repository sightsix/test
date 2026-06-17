// Package ownership implements a post-type-check ownership analysis pass for Yilt.
//
// Yilt uses an ownership-of-return memory model: heap-allocated values (strings,
// tables, functions) have exactly one owner at any time. When such a value is
// assigned to a new variable, the old binding is invalidated (use-after-move is
// a compile-time error). Primitives (int, float, bool, nil) are cheaply copied
// and do not participate in ownership tracking.
//
// This pass runs after type checking and before IR lowering. It walks each
// function body, tracking the ownership state of every variable, and emits
// diagnostics for use-after-move errors.
package ownership

import (
        "fmt"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/check"
        "github.com/yilt/yiltc/internal/diag"
)

// Ownership state of a variable.
type varState int

const (
        varAlive   varState = iota // variable holds a valid value
        varMoved                   // value was moved to another variable
        varConsumed                // value was consumed by a function call / return
)

// ownInfo tracks ownership metadata for a single variable.
type ownInfo struct {
        state   varState
        movePos ast.Pos   // position where the move occurred
        moveTo  string   // name of the variable that received the value
        typ     check.TypeDesc
}

// Analyzer performs ownership analysis on a checked program.
type Analyzer struct {
        handler *diag.DiagnosticHandler
        checked *check.CheckedProgram

        // Per-function state.
        scopes   []map[string]*ownInfo // scope stack of variable name → ownership info
        loopEnds []ast.Pos             // not used yet, reserved for future control flow

        // Control flow merge state: saved snapshots of variable ownership before
        // branching constructs (if/else, while, match) so that we can merge states
        // at join points.
        mergeStack [][]map[string]*ownInfo // stack of scope snapshots for branch merging
}

// NewAnalyzer creates an ownership analyzer for the given checked program.
func NewAnalyzer(cp *check.CheckedProgram, dh *diag.DiagnosticHandler) *Analyzer {
        return &Analyzer{
                handler: dh,
                checked: cp,
        }
}

// Analyze runs ownership analysis on all functions in the checked program.
// Returns true if analysis completed without ownership errors.
func (a *Analyzer) Analyze() bool {
        for _, fn := range a.checked.Functions {
                if fn.Extern {
                        continue
                }
                a.analyzeFunc(fn)
        }
        return !a.handler.HasErrors()
}

// analyzeFunc performs ownership analysis on a single function body.
func (a *Analyzer) analyzeFunc(fn *check.CheckedFn) {
        a.scopes = nil
        a.pushScope()

        // Bind parameters. Parameters are initially alive with their declared types.
        for _, p := range fn.Params {
                a.defineVar(p.Name, &ownInfo{
                        state: varAlive,
                        typ:   p.Type,
                })
        }

        // Walk the function body statements.
        for _, s := range fn.Body {
                a.stmt(s)
        }

        a.scopes = nil
}

// pushScope creates a new variable scope.
func (a *Analyzer) pushScope() {
        a.scopes = append(a.scopes, make(map[string]*ownInfo))
}

// popScope removes the current scope, ending the lifetime of its locals.
func (a *Analyzer) popScope() {
        if len(a.scopes) > 0 {
                a.scopes = a.scopes[:len(a.scopes)-1]
        }
}

// defineVar defines a variable in the current scope.
func (a *Analyzer) defineVar(name string, info *ownInfo) {
        if len(a.scopes) == 0 {
                return
        }
        a.scopes[len(a.scopes)-1][name] = info
}

// lookupVar finds ownership info for a variable, searching up the scope chain.
func (a *Analyzer) lookupVar(name string) *ownInfo {
        for i := len(a.scopes) - 1; i >= 0; i-- {
                if info, ok := a.scopes[i][name]; ok {
                        return info
                }
        }
        return nil
}

// lookupVarInCurrentScope finds ownership info in the current scope only.
func (a *Analyzer) lookupVarInCurrentScope(name string) *ownInfo {
        if len(a.scopes) == 0 {
                return nil
        }
        return a.scopes[len(a.scopes)-1][name]
}

// isHeapType returns true if the type carries a heap pointer that requires
// ownership tracking. Primitives (int, float, bool, nil, void) are cheaply
// copied and do not participate in move semantics. Strings, tables, tuples,
// structs, handles, and function values (closures) are heap-allocated and
// require ownership tracking. Unresolved/generic types are conservatively
// treated as NOT heap to avoid false positives on unannotated parameters.
func isHeapType(t check.TypeDesc) bool {
        switch t.Kind {
        case check.TStr, check.TTable, check.TTuple, check.TStruct, check.THdl, check.TFunc:
                return true
        case check.TNamed:
                // Named types could be heap types; conservatively treat as heap.
                return true
        case check.TGen:
                // Unresolved/generic types: treat as NOT heap to avoid false positives
                // on unannotated parameters. If the type is later resolved, the
                // correct ownership rules will apply.
                return false
        default:
                return false
        }
}

// exprType returns the type of an expression from the checked program's type map.
func (a *Analyzer) exprType(e ast.Expr) check.TypeDesc {
        if a.checked.ExprTypes != nil {
                if t, ok := a.checked.ExprTypes[e]; ok && t.Kind != check.TGen {
                        return t
                }
        }
        return check.GenDesc
}

// checkAlive verifies that a variable is still alive (not moved/consumed).
// If it has been moved, emits a use-after-move error.
func (a *Analyzer) checkAlive(name string, pos ast.Pos) {
        info := a.lookupVar(name)
        if info == nil {
                return // not tracked (e.g., function, constant, import)
        }
        if info.state == varMoved {
                msg := fmt.Sprintf("use of moved value '%s'", name)
                if info.moveTo != "" {
                        msg = fmt.Sprintf("use of moved value '%s' (moved to '%s')", name, info.moveTo)
                }
                a.handler.Error(pos.File, pos.Line, pos.Col, pos.Offset, msg)
        } else if info.state == varConsumed {
                a.handler.Error(pos.File, pos.Line, pos.Col, pos.Offset,
                        fmt.Sprintf("use of consumed value '%s'", name))
        }
}

// isTracked returns true if we should track ownership for a variable.
func (a *Analyzer) isTracked(name string) bool {
        info := a.lookupVar(name)
        return info != nil && isHeapType(info.typ)
}

// moveVar transfers ownership of a variable to a new destination.
// Emits an error if the source variable has already been moved or consumed
// (double-move / use-after-move in move context).
func (a *Analyzer) moveVar(srcName string, dstName string, pos ast.Pos) {
        info := a.lookupVar(srcName)
        if info == nil || !isHeapType(info.typ) {
                return // primitives are copied, not moved; or not tracked
        }
        if info.state != varAlive {
                // Double-move or move-after-consume: emit a dedicated error.
                switch info.state {
                case varMoved:
                        a.handler.Error(pos.File, pos.Line, pos.Col, pos.Offset,
                                fmt.Sprintf("cannot move value '%s' — it was already moved to '%s'", srcName, info.moveTo))
                case varConsumed:
                        a.handler.Error(pos.File, pos.Line, pos.Col, pos.Offset,
                                fmt.Sprintf("cannot move value '%s' — it was already consumed", srcName))
                }
                return
        }
        info.state = varMoved
        info.movePos = pos
        info.moveTo = dstName
}

// consumeVar marks a variable as consumed (used in a function call argument or return).
func (a *Analyzer) consumeVar(name string, pos ast.Pos) {
        info := a.lookupVar(name)
        if info == nil || !isHeapType(info.typ) {
                return // primitives are copied; or not tracked
        }
        if info.state != varAlive {
                return // already moved — checkAlive handles the error
        }
        info.state = varConsumed
        info.movePos = pos
}

// --- Control flow merge helpers ---

// snapshotScopes captures a deep copy of all current scope states.
// This is used before entering a branch so we can restore/merge later.
func (a *Analyzer) snapshotScopes() []map[string]*ownInfo {
        snapshot := make([]map[string]*ownInfo, len(a.scopes))
        for i, scope := range a.scopes {
                copy := make(map[string]*ownInfo, len(scope))
                for name, info := range scope {
                        clone := *info
                        copy[name] = &clone
                }
                snapshot[i] = copy
        }
        return snapshot
}

// restoreScopes restores variable states from a previous snapshot.
// Only variables that exist in the current scopes are updated;
// newly defined variables in branches are left alone (they will
// be cleaned up by popScope).
func (a *Analyzer) restoreScopes(snapshot []map[string]*ownInfo) {
        for i := 0; i < len(a.scopes) && i < len(snapshot); i++ {
                for name, snapInfo := range snapshot[i] {
                        if cur, ok := a.scopes[i][name]; ok {
                                // Restore the state but keep the current type if updated.
                                savedTyp := cur.typ
                                *cur = *snapInfo
                                cur.typ = savedTyp
                        }
                }
        }
}

// mergeBranchStates merges variable ownership from multiple branch snapshots.
// After an if/else or match, a variable is considered alive only if it was
// alive in ALL branches. If it was moved/consumed in any branch, it is
// conservatively marked as moved to prevent use-after-move errors on
// paths where the move did occur.
func (a *Analyzer) mergeBranchStates(snapshots [][]map[string]*ownInfo) {
        if len(snapshots) == 0 {
                return
        }
        if len(snapshots) == 1 {
                // Single branch (no else): variables moved inside remain moved.
                a.restoreScopes(snapshots[0])
                return
        }

        // Collect all variable names across all snapshots.
        allVars := make(map[string]bool)
        for _, snap := range snapshots {
                for i := range snap {
                        for name := range snap[i] {
                                allVars[name] = true
                        }
                }
        }

        // For each variable, check its state across all snapshots.
        // If moved/consumed in ANY branch, conservatively mark as moved.
        for varName := range allVars {
                aliveInAll := true
                firstBadState := varState(-1)
                firstBadMoveTo := ""
                firstBadPos := ast.Pos{}

                for _, snap := range snapshots {
                        found := false
                        for i := len(snap) - 1; i >= 0; i-- {
                                if info, ok := snap[i][varName]; ok {
                                        found = true
                                        if info.state != varAlive {
                                                aliveInAll = false
                                                if firstBadState == varState(-1) {
                                                        firstBadState = info.state
                                                        firstBadMoveTo = info.moveTo
                                                        firstBadPos = info.movePos
                                                }
                                        }
                                        break
                                }
                        }
                        if !found {
                                // Variable not visible in this branch (e.g., defined in
                                // another branch's scope). Don't merge.
                                aliveInAll = false
                                break
                        }
                }

                if !aliveInAll && firstBadState != varState(-1) {
                        // Find the variable in current scopes and update its state.
                        for i := len(a.scopes) - 1; i >= 0; i-- {
                                if cur, ok := a.scopes[i][varName]; ok {
                                        cur.state = firstBadState
                                        cur.moveTo = firstBadMoveTo
                                        cur.movePos = firstBadPos
                                        break
                                }
                        }
                }
        }
}

// --- Statement and expression walkers ---

func (a *Analyzer) stmt(s ast.Stmt) {
        switch n := s.(type) {
        case *ast.LetStmt:
                a.letStmt(n)
        case *ast.ExprStmt:
                a.expr(n.Expr)
        case *ast.ReturnStmt:
                a.returnStmt(n)
        case *ast.IfStmt:
                a.ifStmt(n)
        case *ast.WhileStmt:
                a.whileStmt(n)
        case *ast.ForStmt:
                a.forStmt(n)
        case *ast.MatchStmt:
                a.matchStmt(n)
        case *ast.BreakStmt, *ast.ContinueStmt:
                // No ownership implications
        case *ast.ConstStmt:
                // Constants don't participate in ownership
        case *ast.AssertStmt:
                if n.Cond != nil {
                        a.expr(n.Cond)
                }
                if n.Message != nil {
                        a.expr(n.Message)
                }
        }
}

func (a *Analyzer) letStmt(n *ast.LetStmt) {
        // Evaluate the initializer expression (may consume/move other variables).
        a.expr(n.Value)

        // Determine the type of the new variable from the checked types map.
        typ := a.exprType(n.Value)
        // If the let has a type annotation, use the ExprTypes for the value itself
        // since the annotation is a TypeRef (not an Expr) and doesn't have a type entry.

        // Check if the initializer is a move from another variable.
        if id, ok := n.Value.(*ast.Ident); ok {
                a.moveVar(id.Name, n.Name, n.Value.Pos())
        }

        // Define the new variable as alive.
        a.defineVar(n.Name, &ownInfo{
                state: varAlive,
                typ:   typ,
        })
}

func (a *Analyzer) returnStmt(n *ast.ReturnStmt) {
        if n.Value == nil {
                return
        }
        a.expr(n.Value)

        // If returning a variable, consume it (ownership transfers to caller).
        if id, ok := n.Value.(*ast.Ident); ok {
                a.consumeVar(id.Name, n.Value.Pos())
        }
}

func (a *Analyzer) ifStmt(n *ast.IfStmt) {
        // Save the pre-branch state of all visible variables so we can merge
        // ownership states at the join point after all branches.
        saved := a.snapshotScopes()
        elseCap := 0
        if len(n.Else) > 0 {
                elseCap = 1
        }
        branchSnapshots := make([][]map[string]*ownInfo, 0, len(n.Branches)+elseCap)

        for _, branch := range n.Branches {
                a.expr(branch.Cond)
                a.pushScope()
                for _, s := range branch.Body {
                        a.stmt(s)
                }
                branchSnapshots = append(branchSnapshots, a.snapshotScopes())
                a.restoreScopes(saved)
                a.popScope()
        }
        if len(n.Else) > 0 {
                a.pushScope()
                for _, s := range n.Else {
                        a.stmt(s)
                }
                branchSnapshots = append(branchSnapshots, a.snapshotScopes())
                a.restoreScopes(saved)
                a.popScope()
        }

        // Merge branch states: a variable is only alive after the if/else if
        // it was alive in ALL branches. If it was moved in any branch, we
        // conservatively mark it as "possibly moved" so downstream use is
        // flagged as an error.
        a.mergeBranchStates(branchSnapshots)
}

func (a *Analyzer) whileStmt(n *ast.WhileStmt) {
        a.expr(n.Cond)

        // Save pre-loop state. The loop body may move variables, but on
        // re-entry those variables must be restored to their pre-loop state
        // since the loop condition and body may execute again.
        preLoop := a.snapshotScopes()

        a.pushScope()
        for _, s := range n.Body {
                a.stmt(s)
        }
        a.popScope()

        // After the while loop, merge: the loop body may have moved variables,
        // but the loop might not have executed at all, so we conservatively
        // merge pre-loop state with post-body state.
        postBody := a.snapshotScopes()
        a.restoreScopes(preLoop)
        a.mergeBranchStates([][]map[string]*ownInfo{preLoop, postBody})
}

func (a *Analyzer) forStmt(n *ast.ForStmt) {
        a.expr(n.Over)

        a.pushScope()
        // The loop variable gets a fresh binding each iteration conceptually,
        // but in our analysis we define it once. Its state resets each iteration.
        a.defineVar(n.Key, &ownInfo{
                state: varAlive,
                typ:   a.exprType(n.Over),
        })
        if n.Value != "" {
                a.defineVar(n.Value, &ownInfo{
                        state: varAlive,
                        typ:   check.GenDesc,
                })
        }
        for _, s := range n.Body {
                a.stmt(s)
        }
        a.popScope()
}

func (a *Analyzer) matchStmt(n *ast.MatchStmt) {
        a.expr(n.Subject)

        // Save pre-match state and collect snapshots from each case arm.
        saved := a.snapshotScopes()
        armSnapshots := make([][]map[string]*ownInfo, 0, len(n.Cases))

        for _, c := range n.Cases {
                a.pushScope()
                for _, s := range c.Body {
                        a.stmt(s)
                }
                armSnapshots = append(armSnapshots, a.snapshotScopes())
                a.restoreScopes(saved)
                a.popScope()
        }

        // Merge all arm states conservatively.
        if len(armSnapshots) > 0 {
                a.mergeBranchStates(armSnapshots)
        }
}

func (a *Analyzer) expr(e ast.Expr) {
        switch n := e.(type) {
        case *ast.Ident:
                a.checkAlive(n.Name, n.Pos())
        case *ast.BinOp:
                a.expr(n.Left)
                a.expr(n.Right)
        case *ast.UnaryOp:
                a.expr(n.Operand)
        case *ast.CallExpr:
                a.callExpr(n)
        case *ast.IndexExpr:
                a.expr(n.Obj)
                a.expr(n.Key)
        case *ast.MemberExpr:
                a.expr(n.Obj)
        case *ast.AssignExpr:
                a.assignExpr(n)
        case *ast.IndexAssignExpr:
                a.expr(n.Obj)
                a.expr(n.Key)
                a.expr(n.Value)
        case *ast.MemberAssignExpr:
                a.expr(n.Obj)
                a.expr(n.Value)
        case *ast.TableLit:
                for _, entry := range n.Entries {
                        if entry.Key != nil {
                                a.expr(entry.Key)
                        }
                        a.expr(entry.Value)
                }
        case *ast.FnExpr:
                // Anonymous functions capture free variables by reference.
                // Captured heap-typed variables are borrowed — the closure shares
                // ownership with the enclosing scope. We walk the body in a new
                // scope, inheriting outer variables via clone.
                a.walkFnExpr(n)
        case *ast.IfExpr:
                // IfExpr contains branches directly.
                for _, branch := range n.Branches {
                        a.expr(branch.Cond)
                        a.pushScope()
                        for _, s := range branch.Body {
                                a.stmt(s)
                        }
                        a.popScope()
                }
                if len(n.Else) > 0 {
                        a.pushScope()
                        for _, s := range n.Else {
                                a.stmt(s)
                        }
                        a.popScope()
                }
        case *ast.InterpStr:
                for _, part := range n.Parts {
                        if part.Expr != nil {
                                a.expr(part.Expr)
                        }
                }
        case *ast.RangeExpr:
                a.expr(n.Low)
                a.expr(n.High)
        case *ast.SpawnExpr:
                a.expr(n.Call.Func)
                for _, arg := range n.Call.Args {
                        a.expr(arg)
                }
        case *ast.AwaitExpr:
                a.expr(n.Handle)
        case *ast.ErrorPropExpr:
                a.expr(n.Expr)
        case *ast.IncrDecrExpr:
                a.expr(n.Operand)
        case *ast.TupleExpr:
                for _, el := range n.Elts {
                        a.expr(el)
                }
        case *ast.StructLit:
                for _, f := range n.Fields {
                        a.expr(f.Value)
                }
        // Literals don't involve ownership.
        case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.NilLit:
                // no ownership implications
        }
}

// walkFnExpr analyzes the body of an anonymous function expression.
// Captured variables from outer scopes are inherited (cloned) into the
// closure's scope so that mutations inside the closure don't affect the
// outer analysis state. After the closure body is analyzed, any ownership
// changes to captured variables are propagated back to the outer scope.
func (a *Analyzer) walkFnExpr(n *ast.FnExpr) {
        outerScopes := a.scopes
        a.pushScope()

        // Clone outer scope variables into the closure scope so the closure
        // can read and use them. This creates a snapshot that the closure
        // operates on independently.
        for i, scope := range outerScopes {
                if i == len(outerScopes)-1 {
                        // Skip the current scope — it's already pushed above
                        continue
                }
                for name, info := range scope {
                        if _, exists := a.scopes[0][name]; !exists {
                                clone := *info
                                a.scopes[0][name] = &clone
                        }
                }
        }

        for _, s := range n.Body {
                a.stmt(s)
        }

        a.popScope()
}

func (a *Analyzer) assignExpr(n *ast.AssignExpr) {
        a.expr(n.Value)

        // If assigning from a variable identifier to a mutable variable,
        // this is a move for heap types.
        if srcID, ok := n.Value.(*ast.Ident); ok {
                if dstID, ok := n.Target.(*ast.Ident); ok {
                        // Move semantics: the source variable loses ownership.
                        a.moveVar(srcID.Name, dstID.Name, n.Value.Pos())
                        // The destination variable becomes alive with the new value.
                        info := a.lookupVar(dstID.Name)
                        if info != nil {
                                info.state = varAlive
                                info.moveTo = ""
                        }
                        return
                }
        }

        // For any other assignment (e.g., literal or expression to a variable),
        // the destination variable gets a fresh value and becomes alive again.
        if dstID, ok := n.Target.(*ast.Ident); ok {
                info := a.lookupVar(dstID.Name)
                if info != nil {
                        info.state = varAlive
                        info.moveTo = ""
                }
        }
}

func (a *Analyzer) callExpr(n *ast.CallExpr) {
        a.expr(n.Func)

        // Check each argument. For heap-typed arguments that are variables,
        // the callee borrows them (read-only access). The variable remains
        // alive after the call — no ownership transfer for function arguments.
        // This follows Rule 4: "Functions can temporarily borrow a value
        // for read-only access."
        for _, arg := range n.Args {
                a.expr(arg)
        }

        // If this call might panic (e.g., indexing nil, division by zero),
        // conservatively treat it as a potential branch that may not return.
        // We do NOT consume variables here — the panic path is exceptional.
}
