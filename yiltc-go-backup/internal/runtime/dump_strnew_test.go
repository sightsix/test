package runtime_test

import (
    "fmt"
    "testing"
    "github.com/yilt/yiltc/internal/runtime"
)

func TestDumpStrNew(t *testing.T) {
    code := runtime.PuregenGetFunctionCode("y_str_new")
    fmt.Printf("y_str_new puregen (%d bytes):\n", len(code))
    for i := 0; i < len(code); i++ {
        fmt.Printf("%02x ", code[i])
        if (i+1)%16 == 0 {
            fmt.Println()
        }
    }
    fmt.Println()
}
