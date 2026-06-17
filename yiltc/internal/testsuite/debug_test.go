package testsuite

import (
        "path/filepath"
        "testing"
)

// TestDebugHang hangs on the break_continue test — used to debug the issue.
func TestDebugHang(t *testing.T) {
        root := findTestsuiteRoot()
        file := filepath.Join(root, "basic", "break_continue.yilt")
        compileFile(t, file)
}
