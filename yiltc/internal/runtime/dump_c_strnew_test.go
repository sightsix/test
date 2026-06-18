package runtime_test

import (
    "fmt"
    "testing"
    "github.com/yilt/yiltc/internal/runtime"
)

func TestDumpCStrNew(t *testing.T) {
    syms := runtime.GetAllSymbolNames()
    for _, s := range syms {
        off := runtime.GetFunctionOffset(s)
        sz := runtime.GetFunctionSize(s)
        pgCode := runtime.PuregenGetFunctionCode(s)
        _ = off
        _ = sz
        _ = pgCode
        if s == "y_str_new" {
            fmt.Printf("C y_str_new: off=%d sz=%d puregen=%v\n", off, sz, pgCode != nil)
        }
    }
}
