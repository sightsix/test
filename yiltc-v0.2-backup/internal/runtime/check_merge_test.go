package runtime

import "testing"

func TestCheckMergedFuncs(t *testing.T) {
    allFuncs := GetMergedAllFunctions()
    hashCount := 0
    for _, name := range allFuncs {
        if name == "pure_hash_tagged" {
            hashCount++
            t.Logf("pure_hash_tagged appears in merged list at position %d", hashCount)
        }
    }
    if hashCount > 1 {
        t.Errorf("pure_hash_tagged appears %d times in merged list!", hashCount)
    }
    // Also check C runtime symbol names
    cCount := 0
    for _, name := range GetAllSymbolNames() {
        if name == "pure_hash_tagged" {
            cCount++
        }
    }
    t.Logf("pure_hash_tagged in C runtime: %d times", cCount)
    
    // Check puregen
    pCount := 0
    for _, name := range PuregenGetAllFunctions() {
        if name == "pure_hash_tagged" {
            pCount++
        }
    }
    t.Logf("pure_hash_tagged in puregen: %d times", pCount)
}
