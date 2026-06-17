# Task 2d: Add `_start` Entry Point to C Runtime

## Summary

Added a proper `_start` ELF entry point to `cruntime/runtime.c` using GCC's `naked` function attribute with inline x86-64 assembly. Verified that `yilt_print` and `yilt_println` were already fully implemented. Compilation passes cleanly with zero errors and zero warnings (even with `-Werror`).

## Changes Made

### `cruntime/runtime.c` — replaced `_start` function (lines 3205-3238)

**Old code** (broken):
```c
void _start(void) {
    extern uint64_t yilt_main(void);
    yilt_main();
    raw_exit(0);
}
Problems with the old code: - NOT a naked function — GCC generates C
prologue/epilogue (push rbp; mov rbp,rsp; ...), which
breaks the kernel’s stack layout expectations - NOT marked
__attribute__((used)) — linker could discard it as unused -
Called yilt_main instead of main
(codegen/linker emits main) - raw_exit(0)
discards main’s return value
New code (correct):
__attribute__((naked, used, section(".text")))
void _start(void) {
    __asm__ volatile (
        "xor %%rbp, %%rbp\n\t"       /* clear frame pointer */
        "and $-16, %%rsp\n\t"        /* align stack to 16 bytes */
        "call main\n\t"              /* call Yilt's main() */
        "movl %%eax, %%edi\n\t"      /* exit code = main's return value */
        "movl $60, %%eax\n\t"        /* syscall number 60 = sys_exit */
        "syscall\n\t"                /* invoke kernel */
        ::: "memory"
    );
}
Design Decisions

__attribute__((naked)) — No C
prologue/epilogue. The kernel jumps directly to raw instructions. This
is essential for correct stack layout.
__attribute__((used)) — Prevents
the linker from discarding _start as an unreferenced
symbol.
__attribute__((section(".text"))) —
Explicitly places _start in the .text section
so it’s included in the ELF’s loadable code segment.
and $-16, %rsp — Defensive 16-byte
stack alignment before the CALL. The kernel normally provides 16-byte
alignment, but this handles edge cases (e.g., if linker adds alignment
padding).
call main — Calls the Yilt-compiled
main() function (the linker in main.go uses
"main" as the entry symbol, confirming codegen emits
main).
movl %eax, %edi — Uses main’s
return value (low 32 bits of RAX) as the exit code. The sys_exit syscall
takes the exit code in EDI/RDI.
movl $60, %eax /
syscall — Uses sys_exit (syscall 60),
NOT exit_group (231). For _start (the primary
thread), sys_exit is the correct choice.
::: "memory" — Tells the compiler
the assembly clobbers memory, preventing incorrect reordering.

Disassembly Verification
0000000000008ea8 <_start>:
    8ea8:   48 31 ed                xor    %rbp,%rbp
    8eab:   48 83 e4 f0             and    $0xfffffffffffffff0,%rsp
    8eaf:   e8 00 00 00 00          call   8eb4 <_start+0xc>
    8eb4:   89 c7                   mov    %eax,%edi
    8eab:   b8 3c 00 00 00          mov    $0x3c,%eax
    8ebb:   0f 05                   syscall
Clean: no C prologue, no epilogue, direct call main with
relocation, syscall exit.
yilt_print / yilt_println
Status
Both functions were already fully implemented (lines
1725-1750):

yilt_print(val) — Converts the
tagged value to a string via tagged_to_string() (handles
all 10 tag types), then writes the string data to stdout (fd=1) using
raw_write().
yilt_println(val) — Same as
yilt_print but appends a "\n" to stdout
afterward.

No changes needed.
Compilation Result
gcc -c -nostdlib -ffreestanding -fno-stack-protector -fno-pic -Wall -Wextra -Werror -o cruntime/runtime.o cruntime/runtime.c
Result: 0 errors, 0 warnings.
Notes
