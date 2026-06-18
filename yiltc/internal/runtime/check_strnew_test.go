package runtime_test

import (
    "fmt"
    "testing"
    "github.com/yilt/yiltc/internal/runtime"
)

func TestCheckStrNew(t *testing.T) {
    code := runtime.PuregenGetFunctionCode("y_str_new")
    if code != nil {
        fmt.Printf("puregen y_str_new: %d bytes\n", len(code))
    } else {
        fmt.Println("y_str_new: NOT puregen (using C)")
    }
    allFuncs := runtime.PuregenGetAllFunctions()
    fmt.Printf("Puregen functions (%d):\n", len(allFuncs))
    for _, f := range allFuncs {
        fmt.Printf("  %s\n", f)
    }
}
