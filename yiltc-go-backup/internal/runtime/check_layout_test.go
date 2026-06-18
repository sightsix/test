package runtime

import (
    "encoding/binary"
    "testing"

    elf64 "github.com/yilt/yiltc/internal/link/elf64"
    "github.com/yilt/yiltc/internal/link/types"
)

func TestPuregenRelocTargets(t *testing.T) {
    lnk := elf64.New("x86_64", "linux", false, 0x401000)

    tabSetCode := []byte{0x53, 0x41, 0x54, 0xE8, 0x00, 0x00, 0x00, 0x00, 0xC3}
    lnk.AddCode("y_tab_set", tabSetCode)

    hashCode := []byte{0x53, 0x41, 0x54, 0x41, 0x55, 0xC3}
    lnk.AddCode("pure_hash_tagged", hashCode)

    lnk.AddRelocation("y_tab_set", 3, "pure_hash_tagged", types.RelocPC32, -4)

    elfBinary, err := lnk.Link()
    if err != nil {
        t.Fatalf("link failed: %v", err)
    }

    bo := binary.LittleEndian

    phoff := bo.Uint64(elfBinary[0x20:0x28])
    phentsize := bo.Uint16(elfBinary[0x36:0x38])
    phnum := bo.Uint16(elfBinary[0x38:0x3a])

    for i := uint16(0); i < phnum; i++ {
        off := phoff + uint64(i)*uint64(phentsize)
        pType := bo.Uint32(elfBinary[off : off+4])
        pOffset := bo.Uint64(elfBinary[off+8 : off+16])
        pVaddr := bo.Uint64(elfBinary[off+16 : off+24])
        pFilesz := bo.Uint64(elfBinary[off+32 : off+40])

        if pType == 1 && pFilesz > 0 {
            textData := elfBinary[pOffset : pOffset+pFilesz]
            for j := 0; j < len(textData)-4; j++ {
                if textData[j] == 0xE8 {
                    disp := int32(bo.Uint32(textData[j+1 : j+5]))
                    target := pVaddr + uint64(j) + 4 + uint64(disp)
                    t.Logf("  E8 at vaddr 0x%x: disp=%d target=0x%x", pVaddr+uint64(j), disp, target)
                }
            }
        }
    }
}
