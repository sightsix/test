package ir

import "testing"

func TestModuleNewModule(t *testing.T) {
    m := NewModule()
    if m.FuncCount() != 0 {
        t.Errorf("new module should have 0 functions, got %d", m.FuncCount())
    }
}

func TestModuleAddFunc(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("main", VVoid, true, false)

    if f == nil {
        t.Fatal("AddFunc returned nil")
    }
    if f.Name != "main" {
        t.Errorf("expected name 'main', got '%s'", f.Name)
    }
    if f.RetType != VVoid {
        t.Errorf("expected VVoid return type")
    }
    if !f.Public {
        t.Error("expected public function")
    }
    if f.Extern {
        t.Error("expected non-extern function")
    }
    if m.FuncCount() != 1 {
        t.Errorf("expected 1 function, got %d", m.FuncCount())
    }
}

func TestModuleAllocID(t *testing.T) {
    m := NewModule()
    id1 := m.AllocID()
    id2 := m.AllocID()
    if id1 == id2 {
        t.Error("allocIDs should be unique")
    }
    if id2 <= id1 {
        t.Error("allocIDs should be monotonically increasing")
    }
}

func TestModuleLookupFunc(t *testing.T) {
    m := NewModule()
    m.AddFunc("foo", VVoid, true, false)
    m.AddFunc("bar", VRaw, false, true)

    f := m.LookupFunc("foo")
    if f == nil {
        t.Error("expected to find 'foo'")
    }
    f = m.LookupFunc("bar")
    if f == nil {
        t.Error("expected to find 'bar'")
    }
    f = m.LookupFunc("baz")
    if f != nil {
        t.Error("expected not to find 'baz'")
    }
}

func TestModuleAddRuntimeSym(t *testing.T) {
    m := NewModule()
    m.AddRuntimeSym("y_print", VRaw, "print tagged value")
    m.AddRuntimeSym("y_alloc", VRaw, "heap alloc")

    if len(m.RuntimeSyms) != 2 {
        t.Errorf("expected 2 runtime syms, got %d", len(m.RuntimeSyms))
    }
    if m.RuntimeSyms[0].Name != "y_print" {
        t.Errorf("expected 'y_print', got '%s'", m.RuntimeSyms[0].Name)
    }
}

func TestValTypeString(t *testing.T) {
    tests := []struct {
        vt   ValType
        want string
    }{
        {VInt, "i64"},
        {VUint, "u64"},
        {VFp, "f64"},
        {VBool, "bool"},
        {VStr, "str"},
        {VTable, "table"},
        {VVoid, "void"},
        {VRaw, "raw"},
    }

    for _, tc := range tests {
        if got := tc.vt.String(); got != tc.want {
            t.Errorf("ValType(%d).String() = %q, want %q", tc.vt, got, tc.want)
        }
    }
}

func TestNewFunc(t *testing.T) {
    f := NewFunc("test", VRaw, false, false)
    if f.Name != "test" {
        t.Errorf("expected name 'test', got '%s'", f.Name)
    }
    if f.Entry == nil {
        t.Error("expected non-nil entry block")
    }
    if len(f.Blocks) != 1 {
        t.Errorf("expected 1 block, got %d", len(f.Blocks))
    }
}

func TestBuilderConstInt(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    v := b.ConstInt(42, "x")
    if v == nil {
        t.Fatal("ConstInt returned nil")
    }
    if v.Const == nil {
        t.Fatal("expected non-nil Const")
    }
    if v.Const.IntVal != 42 {
        t.Errorf("expected 42, got %d", v.Const.IntVal)
    }
}

func TestBuilderConstFp(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    v := b.ConstFp(3.14, "pi")
    if v.Const == nil || v.Const.FpVal != 3.14 {
        t.Errorf("expected 3.14, got %v", v.Const)
    }
}

func TestBuilderConstStr(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    v := b.ConstStr("hello", "s")
    if v.Const == nil || v.Const.StrVal != "hello" {
        t.Errorf("expected 'hello', got '%v'", v.Const)
    }
}

func TestBuilderConstBool(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    vt := b.ConstBool(true, "t")
    vf := b.ConstBool(false, "f")
    if vt.Const == nil || !vt.Const.BoolVal {
        t.Error("expected true")
    }
    if vf.Const == nil || vf.Const.BoolVal {
        t.Error("expected false")
    }
}

func TestBuilderArithmetic(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    a := b.ConstInt(10, "a")
    c := b.ConstInt(3, "c")

    add := b.Add(a, c, "sum")
    if add == nil {
        t.Fatal("Add returned nil")
    }

    sub := b.Sub(a, c, "diff")
    if sub == nil {
        t.Fatal("Sub returned nil")
    }

    mul := b.Mul(a, c, "prod")
    if mul == nil {
        t.Fatal("Mul returned nil")
    }
}

func TestBuilderComparison(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    a := b.ConstInt(5, "a")
    c := b.ConstInt(10, "c")

    eq := b.Eq(a, c, "eq")
    if eq == nil {
        t.Fatal("Eq returned nil")
    }

    lt := b.Lt(a, c, "lt")
    if lt == nil {
        t.Fatal("Lt returned nil")
    }
}

func TestBuilderCall(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    arg := b.ConstInt(42, "arg")
    ret := b.Call("y_print", []*Value{arg}, VRaw, "result")
    if ret == nil {
        t.Fatal("Call returned nil")
    }
}

func TestBuilderReturn(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    v := b.ConstInt(0, "r")
    b.Return(v)

    if !b.CurBlock().IsTerminated() {
        t.Error("block should be terminated after return")
    }
}

func TestBuilderJump(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VVoid, false, false)
    b := NewBuilder(m, f)

    target := f.AddBlock("target")
    b.Jump(target, nil)

    if !b.CurBlock().IsTerminated() {
        t.Error("block should be terminated after jump")
    }
}

func TestBuilderBranch(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VVoid, false, false)
    b := NewBuilder(m, f)

    cond := b.ConstBool(true, "cond")
    thenBlk := f.AddBlock("then")
    elseBlk := f.AddBlock("else")

    b.Branch(cond, thenBlk, elseBlk, nil, nil)
    if !b.CurBlock().IsTerminated() {
        t.Error("block should be terminated after branch")
    }
}

func TestBuilderAllocStore(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VVoid, false, false)
    b := NewBuilder(m, f)

    addr := b.Alloc(8, "slot")
    val := b.ConstInt(99, "val")
    b.Store(addr, val)

    loaded := b.Load(addr, VRaw, "loaded")
    if loaded == nil {
        t.Fatal("Load returned nil")
    }
}

func TestBuilderTableOps(t *testing.T) {
    m := NewModule()
    f := m.AddFunc("test", VRaw, false, false)
    b := NewBuilder(m, f)

    tab := b.TableNew(16, "tab")
    if tab == nil {
        t.Fatal("TableNew returned nil")
    }

    key := b.ConstInt(1, "k")
    val := b.ConstInt(42, "v")
    b.TableSet(tab, key, val)

    got := b.TableGet(tab, key, "got")
    if got == nil {
        t.Fatal("TableGet returned nil")
    }
}

func TestBlockIsTerminated(t *testing.T) {
    b := NewBlock("test")
    if b.IsTerminated() {
        t.Error("empty block should not be terminated")
    }

    b.AddInstr(&Instr{Op: OpReturn})
    if !b.IsTerminated() {
        t.Error("block with return should be terminated")
    }
}

func TestInstrIsTerminator(t *testing.T) {
    tests := []struct {
        op  Op
        term bool
    }{
        {OpReturn, true},
        {OpJump, true},
        {OpBranch, true},
        {OpAdd, false},
        {OpConst, false},
        {OpCall, false},
    }

    for _, tc := range tests {
        i := &Instr{Op: tc.op}
        if i.IsTerminator() != tc.term {
            t.Errorf("IsTerminator(%s) = %v, want %v", tc.op, i.IsTerminator(), tc.term)
        }
    }
}

func TestConstValString(t *testing.T) {
    tests := []struct {
        cv   ConstVal
        want string
    }{
        {ConstVal{Kind: VInt, IntVal: 42}, "42"},
        {ConstVal{Kind: VUint, UintVal: 100}, "100u"},
        {ConstVal{Kind: VFp, FpVal: 3.14}, "3.14"},
        {ConstVal{Kind: VStr, StrVal: "hi"}, "\"hi\""},
        {ConstVal{Kind: VBool, BoolVal: true}, "true"},
        {ConstVal{Kind: VBool, BoolVal: false}, "false"},
        {ConstVal{Kind: VVoid}, "void"},
    }

    for _, tc := range tests {
        if got := tc.cv.String(); got != tc.want {
            t.Errorf("ConstVal{%v}.String() = %q, want %q", tc.cv, got, tc.want)
        }
    }
}

func TestValueString(t *testing.T) {
    v := &Value{ID: 42, Name: "result"}
    if got := v.String(); got != "%result" {
        t.Errorf("expected '%%result', got %q", got)
    }

    v2 := &Value{ID: 7}
    if got := v2.String(); got != "v7" {
        t.Errorf("expected 'v7', got %q", got)
    }
}

func TestModuleString(t *testing.T) {
    m := NewModule()
    m.AddFunc("main", VVoid, true, false)

    s := m.String()
    if len(s) == 0 {
        t.Error("expected non-empty string")
    }
}
