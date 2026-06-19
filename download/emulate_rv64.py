#!/usr/bin/env python3
"""Minimal RV64 (RISC-V 64-bit) emulator for verifying Yilt codegen output.

Implements RV64I + M extension, just enough to execute simple Yilt programs:
- Integer arithmetic (ADD, SUB, MUL, DIV, REM, AND, OR, XOR, SLT, SLTU, SLL, SRL, SRA)
- Immediate variants (ADDI, XORI, ORI, ANDI, SLTI, SLTIU)
- Memory (LD, SD, LBU, SB)
- Branches (BEQ, BNE, BLT, BGE, BLTU, BGEU)
- Jumps (JAL, JALR)
- Upper immediate (LUI, AUIPC)
- System (ECALL for write=64, exit=93)
"""

import struct
import sys


class RV64Emulator:
    def __init__(self):
        self.regs = [0] * 32  # x0-x31
        self.pc = 0
        self.memory = {}
        self.halted = False
        self.exit_code = 0

    def mask64(self, v):
        return v & 0xFFFFFFFFFFFFFFFF

    def sign64(self, v):
        v &= 0xFFFFFFFFFFFFFFFF
        if v & 0x8000000000000000:
            return v - 0x10000000000000000
        return v

    def get_reg(self, n):
        if n == 0:
            return 0  # x0 is always zero
        return self.regs[n]

    def set_reg(self, n, v):
        if n == 0:
            return  # x0 is hardwired to zero
        self.regs[n] = self.mask64(v)

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

    def load_binary(self, path):
        with open(path, "rb") as f:
            data = f.read()

        # Parse ELF header
        e_entry = struct.unpack("<Q", data[24:32])[0]
        e_phoff = struct.unpack("<Q", data[32:40])[0]
        e_phentsize = struct.unpack("<H", data[54:56])[0]
        e_phnum = struct.unpack("<H", data[56:58])[0]

        for i in range(e_phnum):
            ph_off = e_phoff + i * e_phentsize
            p_type = struct.unpack("<I", data[ph_off:ph_off + 4])[0]
            p_offset = struct.unpack("<Q", data[ph_off + 8:ph_off + 16])[0]
            p_vaddr = struct.unpack("<Q", data[ph_off + 16:ph_off + 24])[0]
            p_filesz = struct.unpack("<Q", data[ph_off + 32:ph_off + 40])[0]
            p_memsz = struct.unpack("<Q", data[ph_off + 40:ph_off + 48])[0]

            if p_type == 1:  # PT_LOAD
                segment = data[p_offset:p_offset + p_filesz]
                for j, b in enumerate(segment):
                    self.memory[p_vaddr + j] = b
                for j in range(p_filesz, p_memsz):
                    self.memory[p_vaddr + j] = 0

        # Initialize SP to a high address
        self.set_reg(2, 0x7FFFF0000000)  # sp = x2
        self.pc = e_entry
        return e_entry

    def handle_syscall(self):
        num = self.get_reg(17)  # a7 = x17
        if num == 64:  # write(fd, buf, count)
            fd = self.sign64(self.get_reg(10))  # a0
            buf = self.get_reg(11)  # a1
            count = self.sign64(self.get_reg(12))  # a2
            data = bytes(self.read_mem(buf + i, 1) for i in range(count))
            if fd == 1 or fd == 2:
                sys.stdout.buffer.write(data)
                sys.stdout.buffer.flush()
            self.set_reg(10, count)
        elif num == 93 or num == 94:  # exit / exit_group
            self.exit_code = self.sign64(self.get_reg(10))
            self.halted = True
        else:
            raise Exception(f"Unknown syscall {num}")

    def step(self):
        if self.halted:
            return False

        instr = self.read_mem(self.pc, 4)
        next_pc = self.mask64(self.pc + 4)

        opcode = instr & 0x7F
        rd = (instr >> 7) & 0x1F
        funct3 = (instr >> 12) & 0x7
        rs1 = (instr >> 15) & 0x1F
        rs2 = (instr >> 20) & 0x1F
        funct7 = (instr >> 25) & 0x7F
        imm_i = self.sign64((instr >> 20) & 0xFFF)
        # Sign-extend 12-bit imm_i
        if imm_i & 0x800:
            imm_i -= 0x1000

        if opcode == 0x13:  # OP-IMM (ADDI, SLTI, SLTIU, XORI, ORI, ANDI, SLLI, SRLI, SRAI)
            if funct3 == 0:  # ADDI
                self.set_reg(rd, self.sign64(self.get_reg(rs1)) + imm_i)
            elif funct3 == 1:  # SLLI
                shamt = (instr >> 20) & 0x3F
                self.set_reg(rd, self.sign64(self.get_reg(rs1)) << shamt)
            elif funct3 == 2:  # SLTI (signed)
                self.set_reg(rd, 1 if self.sign64(self.get_reg(rs1)) < imm_i else 0)
            elif funct3 == 3:  # SLTIU (unsigned)
                self.set_reg(rd, 1 if self.get_reg(rs1) < self.mask64(imm_i) else 0)
            elif funct3 == 4:  # XORI
                self.set_reg(rd, self.get_reg(rs1) ^ self.mask64(imm_i))
            elif funct3 == 5:  # SRLI / SRAI
                shamt = (instr >> 20) & 0x3F
                if funct7 == 0x00:  # SRLI
                    self.set_reg(rd, self.get_reg(rs1) >> shamt)
                else:  # SRAI (arithmetic)
                    self.set_reg(rd, self.sign64(self.get_reg(rs1)) >> shamt)
            elif funct3 == 6:  # ORI
                self.set_reg(rd, self.get_reg(rs1) | self.mask64(imm_i))
            elif funct3 == 7:  # ANDI
                self.set_reg(rd, self.get_reg(rs1) & self.mask64(imm_i))
        elif opcode == 0x33:  # OP (R-format)
            a = self.sign64(self.get_reg(rs1))
            b = self.sign64(self.get_reg(rs2))
            au = self.get_reg(rs1)
            bu = self.get_reg(rs2)
            if funct7 == 0x01:  # M extension
                if funct3 == 0:  # MUL
                    self.set_reg(rd, a * b)
                elif funct3 == 4:  # DIV (signed)
                    if b == 0:
                        self.set_reg(rd, -1)
                    else:
                        q = abs(a) // abs(b)
                        if (a < 0) != (b < 0):
                            q = -q
                        self.set_reg(rd, q)
                elif funct3 == 6:  # REM (signed)
                    if b == 0:
                        self.set_reg(rd, a)
                    else:
                        r = abs(a) % abs(b)
                        if a < 0:
                            r = -r
                        self.set_reg(rd, r)
                elif funct3 == 7:  # REMU
                    self.set_reg(rd, au % bu if bu != 0 else au)
                elif funct3 == 5:  # DIVU
                    self.set_reg(rd, au // bu if bu != 0 else 0xFFFFFFFFFFFFFFFF)
                elif funct3 == 1:  # MULH
                    self.set_reg(rd, (a * b) >> 64)
                elif funct3 == 3:  # MULHU
                    self.set_reg(rd, (au * bu) >> 64)
                elif funct3 == 2:  # MULHSU
                    self.set_reg(rd, (a * bu) >> 64)
            else:
                if funct3 == 0:
                    if funct7 == 0x00:  # ADD
                        self.set_reg(rd, a + b)
                    else:  # SUB
                        self.set_reg(rd, a - b)
                elif funct3 == 1:  # SLL
                    self.set_reg(rd, self.sign64(self.get_reg(rs1)) << (bu & 0x3F))
                elif funct3 == 2:  # SLT (signed)
                    self.set_reg(rd, 1 if a < b else 0)
                elif funct3 == 3:  # SLTU (unsigned)
                    self.set_reg(rd, 1 if au < bu else 0)
                elif funct3 == 4:  # XOR
                    self.set_reg(rd, au ^ bu)
                elif funct3 == 5:
                    if funct7 == 0x00:  # SRL
                        self.set_reg(rd, au >> (bu & 0x3F))
                    else:  # SRA
                        self.set_reg(rd, a >> (bu & 0x3F))
                elif funct3 == 6:  # OR
                    self.set_reg(rd, au | bu)
                elif funct3 == 7:  # AND
                    self.set_reg(rd, au & bu)
        elif opcode == 0x03:  # LOAD
            if funct3 == 3:  # LD (64-bit)
                self.set_reg(rd, self.read_mem(self.get_reg(rs1) + imm_i, 8))
            elif funct3 == 0:  # LB (signed byte)
                v = self.read_mem(self.get_reg(rs1) + imm_i, 1)
                if v & 0x80:
                    v -= 0x100
                self.set_reg(rd, v)
            elif funct3 == 4:  # LBU (unsigned byte)
                self.set_reg(rd, self.read_mem(self.get_reg(rs1) + imm_i, 1))
            elif funct3 == 2:  # LW (sign-extended 32-bit)
                v = self.read_mem(self.get_reg(rs1) + imm_i, 4)
                if v & 0x80000000:
                    v -= 0x100000000
                self.set_reg(rd, v)
            elif funct3 == 6:  # LWU (zero-extended 32-bit)
                self.set_reg(rd, self.read_mem(self.get_reg(rs1) + imm_i, 4))
            elif funct3 == 1:  # LH (signed halfword)
                v = self.read_mem(self.get_reg(rs1) + imm_i, 2)
                if v & 0x8000:
                    v -= 0x10000
                self.set_reg(rd, v)
            elif funct3 == 5:  # LHU
                self.set_reg(rd, self.read_mem(self.get_reg(rs1) + imm_i, 2))
        elif opcode == 0x23:  # STORE
            # S-format immediate
            imm_s = ((instr >> 25) & 0x7F) << 5 | ((instr >> 7) & 0x1F)
            if imm_s & 0x800:
                imm_s -= 0x1000
            if funct3 == 3:  # SD
                self.write_mem(self.get_reg(rs1) + imm_s, 8, self.get_reg(rs2))
            elif funct3 == 0:  # SB
                self.write_mem(self.get_reg(rs1) + imm_s, 1, self.get_reg(rs2) & 0xFF)
            elif funct3 == 1:  # SH
                self.write_mem(self.get_reg(rs1) + imm_s, 2, self.get_reg(rs2) & 0xFFFF)
            elif funct3 == 2:  # SW
                self.write_mem(self.get_reg(rs1) + imm_s, 4, self.get_reg(rs2) & 0xFFFFFFFF)
        elif opcode == 0x63:  # BRANCH
            # B-format immediate
            imm_b = ((instr >> 31) & 1) << 12 | ((instr >> 25) & 0x3F) << 5 | ((instr >> 8) & 0xF) << 1 | ((instr >> 7) & 1) << 11
            if imm_b & 0x1000:
                imm_b -= 0x2000
            a = self.sign64(self.get_reg(rs1))
            b = self.sign64(self.get_reg(rs2))
            au = self.get_reg(rs1)
            bu = self.get_reg(rs2)
            taken = False
            if funct3 == 0:  # BEQ
                taken = a == b
            elif funct3 == 1:  # BNE
                taken = a != b
            elif funct3 == 4:  # BLT (signed)
                taken = a < b
            elif funct3 == 5:  # BGE (signed)
                taken = a >= b
            elif funct3 == 6:  # BLTU
                taken = au < bu
            elif funct3 == 7:  # BGEU
                taken = au >= bu
            if taken:
                next_pc = self.mask64(self.pc + imm_b)
        elif opcode == 0x6F:  # JAL
            # J-format immediate
            imm_j = ((instr >> 31) & 1) << 20 | ((instr >> 21) & 0x3FF) << 1 | ((instr >> 20) & 1) << 11 | ((instr >> 12) & 0xFF) << 12
            if imm_j & 0x100000:
                imm_j -= 0x200000
            self.set_reg(rd, self.mask64(self.pc + 4))
            next_pc = self.mask64(self.pc + imm_j)
        elif opcode == 0x67:  # JALR
            target = self.mask64((self.get_reg(rs1) + imm_i) & ~1)
            self.set_reg(rd, self.mask64(self.pc + 4))
            next_pc = target
        elif opcode == 0x37:  # LUI
            self.set_reg(rd, instr & 0xFFFFF000)
        elif opcode == 0x17:  # AUIPC
            self.set_reg(rd, self.mask64(self.pc + (instr & 0xFFFFF000)))
        elif opcode == 0x73:  # SYSTEM (ECALL/EBREAK)
            if instr == 0x73:  # ECALL
                self.handle_syscall()
            elif instr == 0x100073:  # EBREAK
                self.halted = True
        else:
            raise Exception(f"Unknown opcode 0x{opcode:02x} (instr 0x{instr:08x}) at PC 0x{self.pc:x}")

        self.pc = next_pc
        return not self.halted

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
        print("Usage: emulate_rv64.py <binary>")
        sys.exit(1)

    emu = RV64Emulator()
    emu.load_binary(sys.argv[1])
    print(f"[Emulator] Entry point: 0x{emu.pc:x}")
    print(f"[Emulator] Running...")
    print("---")
    exit_code = emu.run()
    print("---")
    print(f"[Emulator] Exited with code: {exit_code}")


if __name__ == "__main__":
    main()
