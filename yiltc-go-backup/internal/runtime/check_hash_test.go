package runtime

import "testing"

func TestCheckHashFunc(t *testing.T) {
    for _, name := range GetAllSymbolNames() {
        if name == "pure_hash_tagged" {
            t.Logf("pure_hash_tagged found in C runtime, code size: %d", len(GetFunctionCode(name)))
        }
    }
    code := PuregenGetFunctionCode("pure_hash_tagged")
    if code != nil {
        t.Logf("puregen code size: %d", len(code))
    }
    merged := GetMergedFunctionCode("pure_hash_tagged")
    if merged != nil {
        t.Logf("merged code size: %d", len(merged))
    }
}
