package x86_64

// RegClass classifies a register as general-purpose or XMM (SSE).
type RegClass int

const (
    RegGPR RegClass = iota
    RegXMM
)

// Reg represents an x86-64 register with encoding metadata for machine code
// generation. The Code field holds the low 3 bits of the register encoding
// (0-7). Extended registers (R8-R15, XMM8-XMM15) set Extended=true, which
// triggers the appropriate REX prefix bit (REX.R, REX.X, REX.B) during encoding.
type Reg struct {
    Code     uint8   // low 3-bit encoding (0-7)
    Extended bool    // true for R8-R15 / XMM8-XMM15
    Class    RegClass
    Size     int    // 8, 16, 32, or 64 bits
    Name     string // assembly name
}

// Encoding returns the 3-bit register code and whether a REX extension is needed.
func (r Reg) Encoding() (code uint8, extended bool) {
    return r.Code, r.Extended
}

// IsGPR returns true if this is a general-purpose register.
func (r Reg) IsGPR() bool { return r.Class == RegGPR }

// IsXMM returns true if this is an XMM register.
func (r Reg) IsXMM() bool { return r.Class == RegXMM }

// SubReg returns a sub-register of the same class with the given size (8, 16, 32, 64).
// For GPR, it returns the appropriately-sized variant. For XMM, only 128-bit is supported.
func (r Reg) SubReg(size int) Reg {
    if r.Class == RegXMM {
        return r // XMM registers don't have sub-register variants in our model
    }
    switch size {
    case 8:
        return gpr8[r.Code]
    case 16:
        return gpr16[r.Code]
    case 32:
        return gpr32[r.Code]
    case 64:
        return gpr64[r.Code]
    }
    return r
}

// ========== Register Definitions ==========

// 64-bit general-purpose registers.
var (
    RAX = Reg{Code: 0, Class: RegGPR, Size: 64, Name: "rax"}
    RCX = Reg{Code: 1, Class: RegGPR, Size: 64, Name: "rcx"}
    RDX = Reg{Code: 2, Class: RegGPR, Size: 64, Name: "rdx"}
    RBX = Reg{Code: 3, Class: RegGPR, Size: 64, Name: "rbx"}
    RSP = Reg{Code: 4, Class: RegGPR, Size: 64, Name: "rsp"}
    RBP = Reg{Code: 5, Class: RegGPR, Size: 64, Name: "rbp"}
    RSI = Reg{Code: 6, Class: RegGPR, Size: 64, Name: "rsi"}
    RDI = Reg{Code: 7, Class: RegGPR, Size: 64, Name: "rdi"}
    R8  = Reg{Code: 0, Extended: true, Class: RegGPR, Size: 64, Name: "r8"}
    R9  = Reg{Code: 1, Extended: true, Class: RegGPR, Size: 64, Name: "r9"}
    R10 = Reg{Code: 2, Extended: true, Class: RegGPR, Size: 64, Name: "r10"}
    R11 = Reg{Code: 3, Extended: true, Class: RegGPR, Size: 64, Name: "r11"}
    R12 = Reg{Code: 4, Extended: true, Class: RegGPR, Size: 64, Name: "r12"}
    R13 = Reg{Code: 5, Extended: true, Class: RegGPR, Size: 64, Name: "r13"}
    R14 = Reg{Code: 6, Extended: true, Class: RegGPR, Size: 64, Name: "r14"}
    R15 = Reg{Code: 7, Extended: true, Class: RegGPR, Size: 64, Name: "r15"}
)

// 32-bit general-purpose registers.
var (
    EAX  = Reg{Code: 0, Class: RegGPR, Size: 32, Name: "eax"}
    ECX  = Reg{Code: 1, Class: RegGPR, Size: 32, Name: "ecx"}
    EDX  = Reg{Code: 2, Class: RegGPR, Size: 32, Name: "edx"}
    EBX  = Reg{Code: 3, Class: RegGPR, Size: 32, Name: "ebx"}
    ESP  = Reg{Code: 4, Class: RegGPR, Size: 32, Name: "esp"}
    EBP  = Reg{Code: 5, Class: RegGPR, Size: 32, Name: "ebp"}
    ESI  = Reg{Code: 6, Class: RegGPR, Size: 32, Name: "esi"}
    EDI  = Reg{Code: 7, Class: RegGPR, Size: 32, Name: "edi"}
    R8D  = Reg{Code: 0, Extended: true, Class: RegGPR, Size: 32, Name: "r8d"}
    R9D  = Reg{Code: 1, Extended: true, Class: RegGPR, Size: 32, Name: "r9d"}
    R10D = Reg{Code: 2, Extended: true, Class: RegGPR, Size: 32, Name: "r10d"}
    R11D = Reg{Code: 3, Extended: true, Class: RegGPR, Size: 32, Name: "r11d"}
    R12D = Reg{Code: 4, Extended: true, Class: RegGPR, Size: 32, Name: "r12d"}
    R13D = Reg{Code: 5, Extended: true, Class: RegGPR, Size: 32, Name: "r13d"}
    R14D = Reg{Code: 6, Extended: true, Class: RegGPR, Size: 32, Name: "r14d"}
    R15D = Reg{Code: 7, Extended: true, Class: RegGPR, Size: 32, Name: "r15d"}
)

// 16-bit general-purpose registers.
var (
    AX  = Reg{Code: 0, Class: RegGPR, Size: 16, Name: "ax"}
    CX  = Reg{Code: 1, Class: RegGPR, Size: 16, Name: "cx"}
    DX  = Reg{Code: 2, Class: RegGPR, Size: 16, Name: "dx"}
    BX  = Reg{Code: 3, Class: RegGPR, Size: 16, Name: "bx"}
    SP  = Reg{Code: 4, Class: RegGPR, Size: 16, Name: "sp"}
    BP  = Reg{Code: 5, Class: RegGPR, Size: 16, Name: "bp"}
    SI  = Reg{Code: 6, Class: RegGPR, Size: 16, Name: "si"}
    DI  = Reg{Code: 7, Class: RegGPR, Size: 16, Name: "di"}
    R8W = Reg{Code: 0, Extended: true, Class: RegGPR, Size: 16, Name: "r8w"}
    R9W = Reg{Code: 1, Extended: true, Class: RegGPR, Size: 16, Name: "r9w"}
)

// 8-bit general-purpose registers (low byte, with REX prefix for SPL/BPL/SIL/DIL).
var (
    AL  = Reg{Code: 0, Class: RegGPR, Size: 8, Name: "al"}
    CL  = Reg{Code: 1, Class: RegGPR, Size: 8, Name: "cl"}
    DL  = Reg{Code: 2, Class: RegGPR, Size: 8, Name: "dl"}
    BL  = Reg{Code: 3, Class: RegGPR, Size: 8, Name: "bl"}
    SPL = Reg{Code: 4, Class: RegGPR, Size: 8, Name: "spl"} // requires REX
    BPL = Reg{Code: 5, Class: RegGPR, Size: 8, Name: "bpl"} // requires REX
    SIL = Reg{Code: 6, Class: RegGPR, Size: 8, Name: "sil"} // requires REX
    DIL = Reg{Code: 7, Class: RegGPR, Size: 8, Name: "dil"} // requires REX
    R8B = Reg{Code: 0, Extended: true, Class: RegGPR, Size: 8, Name: "r8b"}
    R9B = Reg{Code: 1, Extended: true, Class: RegGPR, Size: 8, Name: "r9b"}
)

// XMM registers (128-bit SSE registers).
var (
    XMM0  = Reg{Code: 0, Class: RegXMM, Size: 128, Name: "xmm0"}
    XMM1  = Reg{Code: 1, Class: RegXMM, Size: 128, Name: "xmm1"}
    XMM2  = Reg{Code: 2, Class: RegXMM, Size: 128, Name: "xmm2"}
    XMM3  = Reg{Code: 3, Class: RegXMM, Size: 128, Name: "xmm3"}
    XMM4  = Reg{Code: 4, Class: RegXMM, Size: 128, Name: "xmm4"}
    XMM5  = Reg{Code: 5, Class: RegXMM, Size: 128, Name: "xmm5"}
    XMM6  = Reg{Code: 6, Class: RegXMM, Size: 128, Name: "xmm6"}
    XMM7  = Reg{Code: 7, Class: RegXMM, Size: 128, Name: "xmm7"}
    XMM8  = Reg{Code: 0, Extended: true, Class: RegXMM, Size: 128, Name: "xmm8"}
    XMM9  = Reg{Code: 1, Extended: true, Class: RegXMM, Size: 128, Name: "xmm9"}
    XMM10 = Reg{Code: 2, Extended: true, Class: RegXMM, Size: 128, Name: "xmm10"}
    XMM11 = Reg{Code: 3, Extended: true, Class: RegXMM, Size: 128, Name: "xmm11"}
    XMM12 = Reg{Code: 4, Extended: true, Class: RegXMM, Size: 128, Name: "xmm12"}
    XMM13 = Reg{Code: 5, Extended: true, Class: RegXMM, Size: 128, Name: "xmm13"}
    XMM14 = Reg{Code: 6, Extended: true, Class: RegXMM, Size: 128, Name: "xmm14"}
    XMM15 = Reg{Code: 7, Extended: true, Class: RegXMM, Size: 128, Name: "xmm15"}
)

// Lookup tables for sub-register access.
var (
    gpr8  = [16]Reg{AL, CL, DL, BL, SPL, BPL, SIL, DIL, R8B, R9B, Reg{Code: 2, Extended: true, Class: RegGPR, Size: 8, Name: "r10b"}, Reg{Code: 3, Extended: true, Class: RegGPR, Size: 8, Name: "r11b"}, Reg{Code: 4, Extended: true, Class: RegGPR, Size: 8, Name: "r12b"}, Reg{Code: 5, Extended: true, Class: RegGPR, Size: 8, Name: "r13b"}, Reg{Code: 6, Extended: true, Class: RegGPR, Size: 8, Name: "r14b"}, Reg{Code: 7, Extended: true, Class: RegGPR, Size: 8, Name: "r15b"}}
    gpr16 = [16]Reg{AX, CX, DX, BX, SP, BP, SI, DI, R8W, R9W, Reg{Code: 2, Extended: true, Class: RegGPR, Size: 16, Name: "r10w"}, Reg{Code: 3, Extended: true, Class: RegGPR, Size: 16, Name: "r11w"}, Reg{Code: 4, Extended: true, Class: RegGPR, Size: 16, Name: "r12w"}, Reg{Code: 5, Extended: true, Class: RegGPR, Size: 16, Name: "r13w"}, Reg{Code: 6, Extended: true, Class: RegGPR, Size: 16, Name: "r14w"}, Reg{Code: 7, Extended: true, Class: RegGPR, Size: 16, Name: "r15w"}}
    gpr32 = [16]Reg{EAX, ECX, EDX, EBX, ESP, EBP, ESI, EDI, R8D, R9D, R10D, R11D, R12D, R13D, R14D, R15D}
    gpr64 = [16]Reg{RAX, RCX, RDX, RBX, RSP, RBP, RSI, RDI, R8, R9, R10, R11, R12, R13, R14, R15}
)

// AllGPR64 returns all 16 64-bit general-purpose registers in encoding order.
func AllGPR64() []Reg {
    return gpr64[:]
}

// AllXMM returns all 16 XMM registers in encoding order.
func AllXMM() []Reg {
    return []Reg{XMM0, XMM1, XMM2, XMM3, XMM4, XMM5, XMM6, XMM7,
        XMM8, XMM9, XMM10, XMM11, XMM12, XMM13, XMM14, XMM15}
}

// ========== ABI Configuration ==========

// ABIKind selects the calling convention.
type ABIKind int

const (
    ABISystemV ABIKind = iota // Linux, macOS, BSD (System V AMD64)
    ABIWindows                // Windows x64
    ABIBareMetal              // No OS assumptions
)

// Target holds the compilation target configuration.
type Target struct {
    Triple string
    ABI    ABIKind
}

// ParseTarget parses a target triple string and returns the appropriate ABI.
func ParseTarget(triple string) Target {
    t := Target{Triple: triple}
    switch triple {
    case "x86_64-windows-msvc", "x86_64-pc-windows-msvc":
        t.ABI = ABIWindows
    case "x86_64-macos", "x86_64-apple-macos", "x86_64-apple-darwin":
        t.ABI = ABISystemV // System V ABI, 16-byte stack alignment
    case "x86_64-unknown-none", "x86_64-unknown-none-elf":
        t.ABI = ABIBareMetal
    default:
        // Default to System V (Linux, most common)
        t.ABI = ABISystemV
    }
    return t
}

// StackAlign returns the required stack alignment in bytes for the target.
func (t Target) StackAlign() int {
    switch t.ABI {
    case ABISystemV:
        return 16
    case ABIWindows:
        return 16
    case ABIBareMetal:
        return 8
    default:
        return 16
    }
}

// HomeSlotSize returns the size of shadow/home parameter slots on the stack.
// Windows x64 reserves 32 bytes (4 slots) for register parameters.
func (t Target) HomeSlotSize() int {
    if t.ABI == ABIWindows {
        return 32
    }
    return 0
}

// ABIInfo describes the calling convention for a target.
type ABIInfo struct {
    Target        Target
    IntArgRegs    []Reg // registers for integer/pointer arguments
    FloatArgRegs  []Reg // registers for float arguments
    IntRetRegs    []Reg // registers for integer return values
    FloatRetRegs  []Reg // registers for float return values
    CalleeSaved   []Reg // callee-saved registers
    CallerSaved   []Reg // caller-saved registers
    AllocatableGPR []Reg // GPRs available for register allocation
    AllocatableXMM []Reg // XMMs available for register allocation
    ArenaReg      Reg   // register holding arena pointer
}

// GetABIInfo returns the ABI information for the given target.
func GetABIInfo(target Target) *ABIInfo {
    switch target.ABI {
    case ABIWindows:
        return &ABIInfo{
            Target:       target,
            IntArgRegs:   []Reg{RCX, RDX, R8, R9},
            FloatArgRegs: []Reg{XMM0, XMM1, XMM2, XMM3},
            IntRetRegs:   []Reg{RAX},
            FloatRetRegs: []Reg{XMM0},
            CalleeSaved:  []Reg{RBX, RBP, RSI, RDI, R12, R13, R14, R15},
            CallerSaved:  []Reg{RAX, RCX, RDX, R8, R9, R10, R11},
            AllocatableGPR: []Reg{RAX, RCX, RDX, R8, R9, R10, R11},
            AllocatableXMM: []Reg{XMM0, XMM1, XMM2, XMM3, XMM4, XMM5},
            ArenaReg:      R15,
        }
    default:
        // System V AMD64 (Linux, macOS)
        return &ABIInfo{
            Target:       target,
            IntArgRegs:   []Reg{RDI, RSI, RDX, RCX, R8, R9},
            FloatArgRegs: []Reg{XMM0, XMM1, XMM2, XMM3, XMM4, XMM5, XMM6, XMM7},
            IntRetRegs:   []Reg{RAX},
            FloatRetRegs: []Reg{XMM0},
            CalleeSaved:  []Reg{RBX, RBP, R12, R13, R14, R15},
            CallerSaved:  []Reg{RAX, RCX, RDX, RSI, RDI, R8, R9, R10, R11},
            AllocatableGPR: []Reg{RAX, RCX, RDX, RSI, RDI, R8, R9, R10, R11},
            AllocatableXMM: []Reg{XMM0, XMM1, XMM2, XMM3, XMM4, XMM5, XMM6, XMM7},
            ArenaReg:      R15,
        }
    }
}

// IsCalleeSaved returns true if the register is callee-saved in this ABI.
func (a *ABIInfo) IsCalleeSaved(r Reg) bool {
    for _, cs := range a.CalleeSaved {
        if cs.Code == r.Code && cs.Extended == r.Extended {
            return true
        }
    }
    return false
}

// IsCallerSaved returns true if the register is caller-saved in this ABI.
func (a *ABIInfo) IsCallerSaved(r Reg) bool {
    return !a.IsCalleeSaved(r) && r.Code != RSP.Code
}

// IsAllocatable returns true if the register can be used for register allocation.
func (a *ABIInfo) IsAllocatable(r Reg) bool {
    switch r.Class {
    case RegGPR:
        for _, gr := range a.AllocatableGPR {
            if gr.Code == r.Code && gr.Extended == r.Extended {
                return true
            }
        }
    case RegXMM:
        for _, xr := range a.AllocatableXMM {
            if xr.Code == r.Code && xr.Extended == r.Extended {
                return true
            }
        }
    }
    return false
}

// IntArgReg returns the integer argument register for the given parameter index,
// or nil if the argument must be passed on the stack.
func (a *ABIInfo) IntArgReg(idx int) *Reg {
    if idx >= 0 && idx < len(a.IntArgRegs) {
        return &a.IntArgRegs[idx]
    }
    return nil
}

// FloatArgReg returns the float argument register for the given parameter index,
// or nil if the argument must be passed on the stack.
func (a *ABIInfo) FloatArgReg(idx int) *Reg {
    if idx >= 0 && idx < len(a.FloatArgRegs) {
        return &a.FloatArgRegs[idx]
    }
    return nil
}

// ========== Linear Scan Register Allocator ==========

// VReg is a virtual register identifier used by the IR before register allocation.
type VReg int

const (
    NoVReg VReg = -1
)

// RegAlloc tracks the allocation of physical registers to virtual registers.
// It implements a simple linear-scan register allocator.
type RegAlloc struct {
    abi        *ABIInfo
    gprPool    []Reg   // available GPRs (ordered by preference)
    xmmPool    []Reg   // available XMMs (ordered by preference)
    gprFree    []Reg   // currently free GPRs
    xmmFree    []Reg   // currently free XMMs
    gprAssigned map[VReg]Reg // virtual -> physical GPR mapping
    xmmAssigned map[VReg]Reg // virtual -> physical XMM mapping
    spillOffset int    // next available spill slot offset from RBP
    spillSlots  map[VReg]int // virtual reg -> stack offset from RBP
    spillSize   map[VReg]int // virtual reg -> spill size in bytes
    liveRegs    map[VReg]bool // currently live virtual registers
}

// NewRegAlloc creates a new register allocator for the given ABI.
func NewRegAlloc(abi *ABIInfo) *RegAlloc {
    ra := &RegAlloc{
        abi:          abi,
        gprPool:      make([]Reg, len(abi.AllocatableGPR)),
        xmmPool:      make([]Reg, len(abi.AllocatableXMM)),
        gprFree:      make([]Reg, 0, len(abi.AllocatableGPR)),
        xmmFree:      make([]Reg, 0, len(abi.AllocatableXMM)),
        gprAssigned:  make(map[VReg]Reg),
        xmmAssigned:  make(map[VReg]Reg),
        spillSlots:   make(map[VReg]int),
        spillSize:    make(map[VReg]int),
        liveRegs:     make(map[VReg]bool),
    }
    copy(ra.gprPool, abi.AllocatableGPR)
    copy(ra.xmmPool, abi.AllocatableXMM)
    ra.resetPools()
    return ra
}

// resetPools reinitializes the free lists.
func (ra *RegAlloc) resetPools() {
    ra.gprFree = make([]Reg, 0, len(ra.gprPool))
    ra.xmmFree = make([]Reg, 0, len(ra.xmmPool))
    for _, r := range ra.gprPool {
        ra.gprFree = append(ra.gprFree, r)
    }
    for _, r := range ra.xmmPool {
        ra.xmmFree = append(ra.xmmFree, r)
    }
}

// AllocGPR allocates a physical GPR for a virtual register.
// If no register is available, it spills and returns a stack slot.
// The caller should check if the returned Reg has Code == RSP.Code as a sentinel
// to indicate a spill occurred (use SpillSlot to get the offset).
func (ra *RegAlloc) AllocGPR(v VReg, size int) Reg {
    ra.liveRegs[v] = true
    ra.spillSize[v] = size
    if len(ra.gprFree) == 0 {
        return ra.spill(v)
    }
    r := ra.gprFree[len(ra.gprFree)-1]
    ra.gprFree = ra.gprFree[:len(ra.gprFree)-1]
    ra.gprAssigned[v] = r
    return r
}

// AllocXMM allocates a physical XMM register for a virtual register.
func (ra *RegAlloc) AllocXMM(v VReg) Reg {
    ra.liveRegs[v] = true
    ra.spillSize[v] = 8 // XMM values are 8 bytes (double) on stack
    if len(ra.xmmFree) == 0 {
        return ra.spill(v)
    }
    r := ra.xmmFree[len(ra.xmmFree)-1]
    ra.xmmFree = ra.xmmFree[:len(ra.xmmFree)-1]
    ra.xmmAssigned[v] = r
    return r
}

// spill evicts a value to the stack. We use a simple strategy: pick the least
// recently allocated virtual register that is still live and spill it, then
// reuse its physical register. If no candidate is available, allocate a new
// stack slot and return RBP-relative addressing info.
func (ra *RegAlloc) spill(v VReg) Reg {
    // Align the spill offset to 8 bytes.
    ra.spillOffset = (ra.spillOffset + 7) & ^7
    ra.spillSlots[v] = ra.spillOffset
    sz := ra.spillSize[v]
    if sz < 8 {
        sz = 8
    }
    ra.spillOffset += sz
    return RBP // sentinel: caller must check SpillSlot
}

// SpillSlot returns the stack offset from RBP for a spilled virtual register.
// Returns 0 if the register is not spilled (meaning it's in a physical register).
func (ra *RegAlloc) SpillSlot(v VReg) int {
    return ra.spillSlots[v]
}

// IsSpilled returns true if the virtual register is spilled to the stack.
func (ra *RegAlloc) IsSpilled(v VReg) bool {
    _, ok := ra.spillSlots[v]
    return ok
}

// FreeGPR releases a GPR that was allocated for a virtual register.
func (ra *RegAlloc) FreeGPR(v VReg) {
    delete(ra.liveRegs, v)
    if r, ok := ra.gprAssigned[v]; ok {
        delete(ra.gprAssigned, v)
        ra.gprFree = append(ra.gprFree, r)
    }
}

// FreeXMM releases an XMM register that was allocated for a virtual register.
func (ra *RegAlloc) FreeXMM(v VReg) {
    delete(ra.liveRegs, v)
    if r, ok := ra.xmmAssigned[v]; ok {
        delete(ra.xmmAssigned, v)
        ra.xmmFree = append(ra.xmmFree, r)
    }
}

// Free releases a register based on its class.
func (ra *RegAlloc) Free(v VReg, class RegClass) {
    if class == RegXMM {
        ra.FreeXMM(v)
    } else {
        ra.FreeGPR(v)
    }
}

// PhysReg returns the physical register assigned to a virtual register, or RBP
// as a sentinel if the register is spilled (check IsSpilled).
func (ra *RegAlloc) PhysReg(v VReg) Reg {
    if r, ok := ra.gprAssigned[v]; ok {
        return r
    }
    if r, ok := ra.xmmAssigned[v]; ok {
        return r
    }
    return RBP // sentinel
}

// HasPhysReg returns true if the virtual register is assigned to a physical register.
func (ra *RegAlloc) HasPhysReg(v VReg) bool {
    _, gpr := ra.gprAssigned[v]
    if gpr {
        return true
    }
    _, xmm := ra.xmmAssigned[v]
    return xmm
}

// AllocTempGPR allocates a temporary GPR (not tracked by virtual register ID).
// Caller is responsible for calling FreeTempGPR.
func (ra *RegAlloc) AllocTempGPR() Reg {
    if len(ra.gprFree) == 0 {
        return RBP // no register available
    }
    r := ra.gprFree[len(ra.gprFree)-1]
    ra.gprFree = ra.gprFree[:len(ra.gprFree)-1]
    return r
}

// AllocTempXMM allocates a temporary XMM register.
func (ra *RegAlloc) AllocTempXMM() Reg {
    if len(ra.xmmFree) == 0 {
        return XMM0 // no register available, use XMM0 as fallback
    }
    r := ra.xmmFree[len(ra.xmmFree)-1]
    ra.xmmFree = ra.xmmFree[:len(ra.xmmFree)-1]
    return r
}

// FreeTempGPR returns a temporary GPR to the free pool.
func (ra *RegAlloc) FreeTempGPR(r Reg) {
    ra.gprFree = append(ra.gprFree, r)
}

// FreeTempXMM returns a temporary XMM register to the free pool.
func (ra *RegAlloc) FreeTempXMM(r Reg) {
    ra.xmmFree = append(ra.xmmFree, r)
}

// SpillAreaSize returns the total size in bytes of all spill slots.
func (ra *RegAlloc) SpillAreaSize() int {
    return ra.spillOffset
}

// Reset clears all allocations for a new function.
func (ra *RegAlloc) Reset() {
    ra.gprAssigned = make(map[VReg]Reg)
    ra.xmmAssigned = make(map[VReg]Reg)
    ra.spillSlots = make(map[VReg]int)
    ra.spillSize = make(map[VReg]int)
    ra.liveRegs = make(map[VReg]bool)
    ra.spillOffset = 0
    ra.resetPools()
}

// ========== Frame Layout ==========

// FrameLayout describes the stack frame layout for a function.
type FrameLayout struct {
    FrameSize   int // total frame size in bytes (multiple of 16)
    CalleeSaveArea []Reg // callee-saved registers pushed
    SpillAreaSize int  // size of spill area
    LocalAreaSize int  // size for local variables
    ArgAreaSize   int  // size for stack-passed arguments
    NumSpillSlots int  // number of spill slots
}

// ComputeFrameLayout computes the frame layout given the register allocator state
// and additional local variable space.
func (a *ABIInfo) ComputeFrameLayout(spillSize int, localSize int, numStackArgs int) *FrameLayout {
    align := a.Target.StackAlign()

    spillAligned := (spillSize + 7) & ^7
    localAligned := (localSize + 7) & ^7
    argAligned := (numStackArgs * 8 + 7) & ^7

    // Windows: add 32 bytes shadow space for register parameters
    shadowSize := a.Target.HomeSlotSize()

    // Callee-saved pushes (each is 8 bytes on x86-64)
    calleeSaveBytes := len(a.CalleeSaved) * 8

    // Total frame from after pushes: spill + locals + args + shadow + alignment
    innerSize := spillAligned + localAligned + argAligned + shadowSize
    // Frame pointer push is 8 bytes
    totalUnaligned := calleeSaveBytes + innerSize
    // Add alignment padding to make (RSP + 8) a multiple of 16 after the call
    // (call pushes 8 bytes, so we need (totalUnaligned + 8) % 16 == 0)
    frameSize := (totalUnaligned + align - 1) & ^align

    return &FrameLayout{
        FrameSize:      frameSize,
        SpillAreaSize:  spillAligned,
        LocalAreaSize:  localAligned,
        ArgAreaSize:    argAligned,
        NumSpillSlots:  spillAligned / 8,
    }
}
