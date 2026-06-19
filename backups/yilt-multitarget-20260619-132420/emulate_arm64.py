#!/usr/bin/env python3
"""Minimal AArch64 emulator for verifying Yilt codegen output.

Implements just enough instructions to execute simple Yilt programs:
- Integer arithmetic (ADD, SUB, MUL, SDIV, MSUB, NEG)
- Logical (AND, ORR, EOR, MVN)
- Compare and conditional branches (CMP, Bcond, CSET)
- Loads and stores (LDR, STR, LDRB, STRB, LDP, STP)
- Branches (B, BL, RET)
- Immediate loads (MOVZ, MOVK, ADR)
- Syscalls (SVC) for write(64) and exit(93)

Supports:
- 31 general-purpose registers (X0-X30), SP (X31), XZR (X31 alias)
- 4GB of virtual memory (sparse dict)
- Little-endian memory access
"""

import struct
import sys


class AArch64Emulator:
    def __init__(self):
        self.regs = [0] * 32  # X0-X30, X31=SP
        self.memory = {}  # dict of byte_addr -> byte_value
        self.pc = 0
        self.halted = False
        self.exit_code = 0
        self.output = b""

    def mask64(self, v):
        return v & 0xFFFFFFFFFFFFFFFF

    def sign64(self, v):
        v &= 0xFFFFFFFFFFFFFFFF
        if v & 0x8000000000000000:
            return v - 0x10000000000000000
        return v

    def get_reg(self, n):
        if n == 31:
            return 0  # XZR
        return self.regs[n]

    def set_reg(self, n, v):
        if n == 31:
            return  # XZR is always 0
        self.regs[n] = self.mask64(v)

    def get_sp(self):
        return self.regs[31]  # SP uses X31 slot

    def set_sp(self, v):
        self.regs[31] = self.mask64(v)

    def read_mem(self, addr, size):
        addr = self.mask64(addr)
        result = 0
        for i in range(size):
            result |= self.memory.get(addr + i, 0) << (i * 8)
        return result

    def write_mem(self, addr, size, value):
        addr = self.mask64(addr)
        value = self.mask64(value)
        for i in range(size):
            self.memory[addr + i] = (value >> (i * 8)) & 0xFF

    def load_binary(self, path, base_addr=0x400000):
        """Load an ELF binary into memory."""
        with open(path, "rb") as f:
            data = f.read()

        # Parse ELF header
        e_entry = struct.unpack("<Q", data[24:32])[0]
        e_phoff = struct.unpack("<Q", data[32:40])[0]
        e_phentsize = struct.unpack("<H", data[54:56])[0]
        e_phnum = struct.unpack("<H", data[56:58])[0]

        # Parse program headers (look for PT_LOAD=1)
        for i in range(e_phnum):
            ph_off = e_phoff + i * e_phentsize
            p_type = struct.unpack("<I", data[ph_off:ph_off + 4])[0]
            p_flags = struct.unpack("<I", data[ph_off + 4:ph_off + 8])[0]
            p_offset = struct.unpack("<Q", data[ph_off + 8:ph_off + 16])[0]
            p_vaddr = struct.unpack("<Q", data[ph_off + 16:ph_off + 24])[0]
            p_filesz = struct.unpack("<Q", data[ph_off + 32:ph_off + 40])[0]
            p_memsz = struct.unpack("<Q", data[ph_off + 40:ph_off + 48])[0]

            if p_type == 1:  # PT_LOAD
                segment = data[p_offset:p_offset + p_filesz]
                for j, b in enumerate(segment):
                    self.memory[p_vaddr + j] = b
                # BSS section (zero-fill)
                for j in range(p_filesz, p_memsz):
                    self.memory[p_vaddr + j] = 0

        # Initialize SP to a high address
        self.set_sp(0x7FFFF0000000)
        # Initialize X29 (FP) to 0
        self.set_reg(29, 0)
        # Set entry point
        self.pc = e_entry
        return e_entry

    def step(self):
        """Execute one instruction. Returns False if halted."""
        if self.halted:
            return False

        instr = self.read_mem(self.pc, 4)
        next_pc = self.pc + 4

        # Decode and execute
        if instr == 0xD65F03C0:  # RET
            next_pc = self.get_reg(30)  # Return to LR
        elif instr == 0xD4000001:  # SVC #0
            self.handle_syscall()
        elif (instr & 0xFC000000) == 0x94000000:  # BL
            imm26 = instr & 0x3FFFFFF
            if imm26 & 0x2000000:
                imm26 -= 0x4000000
            self.set_reg(30, self.pc + 4)
            next_pc = self.pc + imm26 * 4
        elif (instr & 0xFC000000) == 0x14000000:  # B
            imm26 = instr & 0x3FFFFFF
            if imm26 & 0x2000000:
                imm26 -= 0x4000000
            next_pc = self.pc + imm26 * 4
        elif (instr & 0xFF000010) == 0x54000000:  # Bcond
            imm19 = (instr >> 5) & 0x7FFFF
            if imm19 & 0x40000:
                imm19 -= 0x80000
            cond = instr & 0xF
            if self.check_cond(cond):
                next_pc = self.pc + imm19 * 4
        elif (instr & 0xFFE00000) == 0xD2800000:  # MOVZ (64-bit)
            hw = (instr >> 21) & 0x3
            imm16 = (instr >> 5) & 0xFFFF
            rd = instr & 0x1F
            self.set_reg(rd, imm16 << (hw * 16))
        elif (instr & 0xFFE00000) == 0xF2800000:  # MOVK (64-bit)
            hw = (instr >> 21) & 0x3
            imm16 = (instr >> 5) & 0xFFFF
            rd = instr & 0x1F
            cur = self.get_reg(rd)
            mask = 0xFFFF << (hw * 16)
            self.set_reg(rd, (cur & ~mask) | (imm16 << (hw * 16)))
        elif (instr & 0xFF200000) == 0xAA000000:  # ORR (register)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            self.set_reg(rd, self.get_reg(rn) | self.get_reg(rm))
        elif (instr & 0xFF200000) == 0x8B000000:  # ADD (register)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            self.set_reg(rd, self.get_reg(rn) + self.get_reg(rm))
        elif (instr & 0xFF200000) == 0xCB000000:  # SUB (register)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            result = self.sign64(self.get_reg(rn)) - self.sign64(self.get_reg(rm))
            self.set_reg(rd, result)
            self.flags = self.compute_sub_flags(self.get_reg(rn), self.get_reg(rm), result)
        elif (instr & 0xFF200000) == 0x8A000000:  # AND (register)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            self.set_reg(rd, self.get_reg(rn) & self.get_reg(rm))
        elif (instr & 0xFF200000) == 0xAA200000:  # ORN (register) - MVN is ORN Xd, XZR, Xm
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            self.set_reg(rd, ~(self.get_reg(rm)))
        elif (instr & 0xFF200000) == 0xCA000000:  # EOR (register)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            self.set_reg(rd, self.get_reg(rn) ^ self.get_reg(rm))
        elif (instr & 0xFF208000) == 0x9B000000:  # MADD/MUL
            rm = (instr >> 16) & 0x1F
            ra = (instr >> 10) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            if ra == 31:
                self.set_reg(rd, self.sign64(self.get_reg(rn)) * self.sign64(self.get_reg(rm)))
            else:
                self.set_reg(rd, self.sign64(self.get_reg(ra)) + self.sign64(self.get_reg(rn)) * self.sign64(self.get_reg(rm)))
        elif (instr & 0xFF208000) == 0x9B008000:  # MSUB
            rm = (instr >> 16) & 0x1F
            ra = (instr >> 10) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            self.set_reg(rd, self.sign64(self.get_reg(ra)) - self.sign64(self.get_reg(rn)) * self.sign64(self.get_reg(rm)))
        elif (instr & 0xFFE0FC00) == 0x9AC00C00:  # SDIV (64-bit)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            divisor = self.sign64(self.get_reg(rm))
            if divisor == 0:
                result = 0
            else:
                dividend = self.sign64(self.get_reg(rn))
                result = int(dividend / divisor) if (dividend < 0) != (divisor < 0) else dividend // divisor
                # AArch64 SDIV truncates toward zero
                result = int(dividend / divisor)
                if result < 0:
                    result = -(-dividend // divisor) if dividend < 0 else -(dividend // -divisor)
                # Simpler: use Python's int division with truncation
                q = abs(dividend) // abs(divisor)
                if (dividend < 0) != (divisor < 0):
                    q = -q
                result = q
            self.set_reg(rd, result)
        elif (instr & 0xFF800000) == 0x91000000:  # ADD imm (64-bit)
            imm12 = (instr >> 10) & 0xFFF
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            val = self.get_reg(rn) if rn != 31 else self.get_sp()
            if rd == 31:
                self.set_sp(val + imm12)
            else:
                self.set_reg(rd, val + imm12)
        elif (instr & 0xFF800000) == 0xD1000000:  # SUB imm (64-bit)
            imm12 = (instr >> 10) & 0xFFF
            rn = (instr >> 5) & 0x1F
            rd = instr & 0x1F
            val = self.get_reg(rn) if rn != 31 else self.get_sp()
            if rd == 31:
                self.set_sp(val - imm12)
            else:
                self.set_reg(rd, val - imm12)
            # Update flags for CMP (SUBS XZR)
            if rd == 31:
                self.flags = self.compute_sub_flags(val, imm12, val - imm12)
        elif (instr & 0xFF80001F) == 0xF100001F:  # CMP imm (SUBS XZR, Xn, #imm5)
            # Actually this is a 12-bit imm, not 5-bit. Let me check.
            # CMP imm = SUBS XZR, Xn, #imm12 = 0xF1000000 | (imm12 << 10) | (Xn << 5) | 31
            imm12 = (instr >> 10) & 0xFFF
            rn = (instr >> 5) & 0x1F
            a = self.sign64(self.get_reg(rn))
            result = a - imm12
            self.flags = self.compute_sub_flags(a, imm12, result)
        elif (instr & 0xFFE0FC1F) == 0xEB00001F:  # CMP register
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            a = self.sign64(self.get_reg(rn))
            b = self.sign64(self.get_reg(rm))
            result = a - b
            self.flags = self.compute_sub_flags(a, b, result)
        elif (instr & 0xFFC00000) == 0xF9000000:  # STR (unsigned offset, 64-bit)
            imm12 = (instr >> 10) & 0xFFF
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            self.write_mem(base + imm12 * 8, 8, self.get_reg(rt))
        elif (instr & 0xFFC00000) == 0xF9400000:  # LDR (unsigned offset, 64-bit)
            imm12 = (instr >> 10) & 0xFFF
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            self.set_reg(rt, self.read_mem(base + imm12 * 8, 8))
        elif (instr & 0xFFC00000) == 0x39000000:  # STRB (unsigned offset)
            imm12 = (instr >> 10) & 0xFFF
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            self.write_mem(base + imm12, 1, self.get_reg(rt) & 0xFF)
        elif (instr & 0xFFE00C00) == 0x38200800:  # STRB (register)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            self.write_mem(base + self.get_reg(rm), 1, self.get_reg(rt) & 0xFF)
        elif (instr & 0xFFE00C00) == 0x38600800:  # LDRB (register)
            rm = (instr >> 16) & 0x1F
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            self.set_reg(rt, self.read_mem(base + self.get_reg(rm), 1))
        elif (instr & 0xFFE00C00) == 0xF8000C00:  # STR (pre-index, 64-bit)
            imm9 = (instr >> 12) & 0x1FF
            if imm9 & 0x100:
                imm9 -= 0x200
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            new_addr = base + imm9
            if rn == 31:
                self.set_sp(new_addr)
            else:
                self.set_reg(rn, new_addr)
            self.write_mem(new_addr, 8, self.get_reg(rt))
        elif (instr & 0xFFE00C00) == 0xF8400400:  # LDR (post-index, 64-bit)
            imm9 = (instr >> 12) & 0x1FF
            if imm9 & 0x100:
                imm9 -= 0x200
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            self.set_reg(rt, self.read_mem(base, 8))
            new_addr = base + imm9
            if rn == 31:
                self.set_sp(new_addr)
            else:
                self.set_reg(rn, new_addr)
        elif (instr & 0xFFC00000) == 0xA9800000:  # STP pre-index (64-bit)
            imm7 = (instr >> 15) & 0x7F
            if imm7 & 0x40:
                imm7 -= 0x80
            rt2 = (instr >> 10) & 0x1F
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            new_addr = base + imm7 * 8
            if rn == 31:
                self.set_sp(new_addr)
            else:
                self.set_reg(rn, new_addr)
            self.write_mem(new_addr, 8, self.get_reg(rt))
            self.write_mem(new_addr + 8, 8, self.get_reg(rt2))
        elif (instr & 0xFFC00000) == 0xA8C00000:  # LDP post-index (64-bit)
            imm7 = (instr >> 15) & 0x7F
            if imm7 & 0x40:
                imm7 -= 0x80
            rt2 = (instr >> 10) & 0x1F
            rn = (instr >> 5) & 0x1F
            rt = instr & 0x1F
            base = self.get_reg(rn) if rn != 31 else self.get_sp()
            self.set_reg(rt, self.read_mem(base, 8))
            self.set_reg(rt2, self.read_mem(base + 8, 8))
            new_addr = base + imm7 * 8
            if rn == 31:
                self.set_sp(new_addr)
            else:
                self.set_reg(rn, new_addr)
        elif (instr & 0xFFFF0FE0) == 0x9A9F07E0:  # CSET
            inv_cond = (instr >> 12) & 0xF
            cond = inv_cond ^ 1
            rd = instr & 0x1F
            self.set_reg(rd, 1 if self.check_cond(cond) else 0)
        elif (instr & 0x9F000000) == 0x10000000:  # ADR
            immlo = (instr >> 29) & 0x3
            immhi = (instr >> 5) & 0x7FFFF
            off = (immhi << 2) | immlo
            if off & 0x100000:
                off -= 0x200000
            rd = instr & 0x1F
            self.set_reg(rd, self.pc + off)
        else:
            raise Exception(f"Unknown instruction 0x{instr:08x} at PC 0x{self.pc:x}")

        self.pc = self.mask64(next_pc)
        return not self.halted

    def compute_sub_flags(self, a, b, result):
        a_s = self.sign64(a)
        b_s = self.sign64(b)
        r_s = self.sign64(result)
        return {
            'Z': (r_s & 0xFFFFFFFFFFFFFFFF) == 0,
            'N': (result & 0x8000000000000000) != 0,
            'C': a_s >= b_s,  # borrow logic (simplified)
            'V': ((a_s < 0) != (b_s < 0)) and ((a_s < 0) != (r_s < 0)),
        }

    def check_cond(self, cond):
        if not hasattr(self, 'flags'):
            self.flags = {'Z': False, 'N': False, 'C': False, 'V': False}
        f = self.flags
        if cond == 0: return f['Z']  # EQ
        if cond == 1: return not f['Z']  # NE
        if cond == 2: return f['C']  # CS
        if cond == 3: return not f['C']  # CC
        if cond == 4: return f['N']  # MI
        if cond == 5: return not f['N']  # PL
        if cond == 6: return f['V']  # VS
        if cond == 7: return not f['V']  # VC
        if cond == 8: return f['C'] and not f['Z']  # HI
        if cond == 9: return not f['C'] or f['Z']  # LS
        if cond == 10: return f['N'] == f['V']  # GE
        if cond == 11: return f['N'] != f['V']  # LT
        if cond == 12: return not f['Z'] and (f['N'] == f['V'])  # GT
        if cond == 13: return f['Z'] or (f['N'] != f['V'])  # LE
        return False

    def handle_syscall(self):
        num = self.get_reg(8)
        if num == 64:  # write(fd, buf, count)
            fd = self.sign64(self.get_reg(0))
            buf = self.get_reg(1)
            count = self.sign64(self.get_reg(2))
            data = bytes(self.read_mem(buf + i, 1) for i in range(count))
            if fd == 1 or fd == 2:
                self.output += data
                sys.stdout.buffer.write(data)
                sys.stdout.buffer.flush()
            self.set_reg(0, count)
        elif num == 93:  # exit(code)
            self.exit_code = self.sign64(self.get_reg(0))
            self.halted = True
        else:
            raise Exception(f"Unknown syscall {num}")

    def run(self, max_steps=1000000):
        steps = 0
        while not self.halted and steps < max_steps:
            self.step()
            steps += 1
        if not self.halted:
            print(f"\n[Emulator] Max steps ({max_steps}) reached")
        return self.exit_code


def main():
    if len(sys.argv) < 2:
        print("Usage: emulate_arm64.py <binary>")
        sys.exit(1)

    emu = AArch64Emulator()
    emu.load_binary(sys.argv[1])
    print(f"[Emulator] Entry point: 0x{emu.pc:x}")
    print(f"[Emulator] Running...")
    print("---")
    exit_code = emu.run()
    print("---")
    print(f"[Emulator] Exited with code: {exit_code}")


if __name__ == "__main__":
    main()
