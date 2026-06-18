package x86_64

import "fmt"

func TestMovRMRCEncoding() {
    a := NewAsm()
    a.MovRM64(RAX, 0x0100000000000005)
    a.MovRM64(RCX, 0x0100000000000000)
    a.OrRR(RAX, RCX)
    b := a.Bytes()
    for i := 0; i < len(b); i++ {
        fmt.Printf("%02x ", b[i])
    }
    fmt.Println()
}
