#!/usr/bin/env python3
"""AArch64 disassembler for verifying Yilt codegen output."""

import struct
import sys

def decode_arm64(instr):
    """Decode a 32-bit AArch64 instruction. Returns a string description."""
    if instr == 0xD65F03C0:
        return "RET"
    if instr == 0xD4000001:
        return "SVC #0"

    # STP pre-index (64-bit): 0xA9800000 | (imm7 << 15) | (Rt2 << 10) | (Rn << 5) | Rt
    if (instr & 0xFFC00000) == 0xA9800000:
        imm7 = (instr >> 15) & 0x7F
        if imm7 & 0x40:
            imm7 -= 128
        rt2 = (instr >> 10) & 0x1F
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        off = imm7 * 8
        return f"STP X{rt}, X{rt2}, [X{rn}, #{off}]!"

    # LDP post-index (64-bit): 0xA8C00000 | (imm7 << 15) | (Rt2 << 10) | (Rn << 5) | Rt
    if (instr & 0xFFC00000) == 0xA8C00000:
        imm7 = (instr >> 15) & 0x7F
        rt2 = (instr >> 10) & 0x1F
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        off = imm7 * 8
        return f"LDP X{rt}, X{rt2}, [X{rn}], #{off}"

    # ADD imm (64-bit): 0x91000000 | (imm12 << 10) | (Rn << 5) | Rd
    if (instr & 0xFF800000) == 0x91000000:
        imm12 = (instr >> 10) & 0xFFF
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        if rn == 31 and rd == 29 and imm12 == 0:
            return "MOV X29, SP"
        if rn == 29 and rd == 31 and imm12 == 0:
            return "MOV SP, X29"
        rn_s = "SP" if rn == 31 else f"X{rn}"
        rd_s = "SP" if rd == 31 else f"X{rd}"
        return f"ADD {rd_s}, {rn_s}, #{imm12}"

    # SUB imm (64-bit): 0xD1000000 | (imm12 << 10) | (Rn << 5) | Rd
    if (instr & 0xFF800000) == 0xD1000000:
        imm12 = (instr >> 10) & 0xFFF
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        rn_s = "SP" if rn == 31 else f"X{rn}"
        rd_s = "SP" if rd == 31 else f"X{rd}"
        return f"SUB {rd_s}, {rn_s}, #{imm12}"

    # MOVZ (64-bit): 0xD2800000 | (hw << 21) | (imm16 << 5) | Rd
    if (instr & 0xFFE00000) == 0xD2800000:
        hw = (instr >> 21) & 0x3
        imm16 = (instr >> 5) & 0xFFFF
        rd = instr & 0x1F
        shift = hw * 16
        if shift == 0:
            return f"MOVZ X{rd}, #{imm16}  (= {imm16})"
        return f"MOVZ X{rd}, #{imm16}, LSL #{shift}"

    # MOVK (64-bit): 0xF2800000 | (hw << 21) | (imm16 << 5) | Rd
    if (instr & 0xFFE00000) == 0xF2800000:
        hw = (instr >> 21) & 0x3
        imm16 = (instr >> 5) & 0xFFFF
        rd = instr & 0x1F
        shift = hw * 16
        if shift == 0:
            return f"MOVK X{rd}, #{imm16}"
        return f"MOVK X{rd}, #{imm16}, LSL #{shift}"

    # ORR (register, 64-bit): 0xAA000000 | (Rm << 16) | (Rn << 5) | Rd
    if (instr & 0xFF200000) == 0xAA000000:
        rm = (instr >> 16) & 0x1F
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        if rn == 31:
            return f"MOV X{rd}, X{rm}"
        return f"ORR X{rd}, X{rn}, X{rm}"

    # ADD (register, 64-bit): 0x8B000000 | (Rm << 16) | (Rn << 5) | Rd
    if (instr & 0xFF200000) == 0x8B000000:
        rm = (instr >> 16) & 0x1F
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        rn_s = "SP" if rn == 31 else f"X{rn}"
        rd_s = "SP" if rd == 31 else f"X{rd}"
        return f"ADD {rd_s}, {rn_s}, X{rm}"

    # SUB (register, 64-bit): 0xCB000000
    if (instr & 0xFF200000) == 0xCB000000:
        rm = (instr >> 16) & 0x1F
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        if rn == 31:
            return f"NEG X{rd}, X{rm}"
        rn_s = "SP" if rn == 31 else f"X{rn}"
        rd_s = "SP" if rd == 31 else f"X{rd}"
        return f"SUB {rd_s}, {rn_s}, X{rm}"

    # MUL (via MADD with XZR): 0x9B000000 with Ra=31
    if (instr & 0xFF208000) == 0x9B000000:
        rm = (instr >> 16) & 0x1F
        ra = (instr >> 10) & 0x1F
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        if ra == 31:
            return f"MUL X{rd}, X{rn}, X{rm}"
        return f"MADD X{rd}, X{rn}, X{rm}, X{ra}"

    # MSUB: 0x9B008000
    if (instr & 0xFF208000) == 0x9B008000:
        rm = (instr >> 16) & 0x1F
        ra = (instr >> 10) & 0x1F
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        return f"MSUB X{rd}, X{rn}, X{rm}, X{ra}"

    # SDIV: 0x9AC00C00
    if (instr & 0xFFFFFC00) == 0x9AC00C00:
        rm = (instr >> 16) & 0x1F
        rn = (instr >> 5) & 0x1F
        rd = instr & 0x1F
        return f"SDIV X{rd}, X{rn}, X{rm}"

    # CMP (register) = SUBS XZR: 0xEB00001F
    if (instr & 0xFFE0FC1F) == 0xEB00001F or ((instr & 0xFF20001F) == 0xEB00001F and (instr & 0x0000FE00) == 0):
        rm = (instr >> 16) & 0x1F
        rn = (instr >> 5) & 0x1F
        return f"CMP X{rn}, X{rm}"

    # CMP imm (SUBS XZR, Xn, #imm5): 0xF1000000 | (imm5 << 10) | (Xn << 5) | 31
    if (instr & 0xFF80001F) == 0xF100001F:
        imm5 = (instr >> 10) & 0x1F
        rn = (instr >> 5) & 0x1F
        return f"CMP X{rn}, #{imm5}"

    # STR (unsigned offset, 64-bit): 0xF9000000 | (imm12 << 10) | (Rn << 5) | Rt
    if (instr & 0xFFC00000) == 0xF9000000:
        imm12 = (instr >> 10) & 0xFFF
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        off = imm12 * 8
        rn_s = "SP" if rn == 31 else f"X{rn}"
        return f"STR X{rt}, [{rn_s}, #{off}]"

    # LDR (unsigned offset, 64-bit): 0xF9400000
    if (instr & 0xFFC00000) == 0xF9400000:
        imm12 = (instr >> 10) & 0xFFF
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        off = imm12 * 8
        rn_s = "SP" if rn == 31 else f"X{rn}"
        return f"LDR X{rt}, [{rn_s}, #{off}]"

    # STRB (unsigned offset): 0x39000000
    if (instr & 0xFFC00000) == 0x39000000:
        imm12 = (instr >> 10) & 0xFFF
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        return f"STRB W{rt}, [X{rn}, #{imm12}]"

    # STRB (register): 0x38200800
    if (instr & 0xFFE00C00) == 0x38200800:
        rm = (instr >> 16) & 0x1F
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        return f"STRB W{rt}, [X{rn}, X{rm}]"

    # LDRB (register): 0x38600800
    if (instr & 0xFFE00C00) == 0x38600800:
        rm = (instr >> 16) & 0x1F
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        return f"LDRB W{rt}, [X{rn}, X{rm}]"

    # STR (pre-index, 64-bit): 0xF8000C00 | (imm9 << 12) | (Rn << 5) | Rt
    if (instr & 0xFFE00C00) == 0xF8000C00:
        imm9 = (instr >> 12) & 0x1FF
        if imm9 & 0x100:
            imm9 -= 512
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        return f"STR X{rt}, [X{rn}, #{imm9}]!"

    # LDR (post-index, 64-bit): 0xF8400400
    if (instr & 0xFFE00C00) == 0xF8400400:
        imm9 = (instr >> 12) & 0x1FF
        if imm9 & 0x100:
            imm9 -= 512
        rn = (instr >> 5) & 0x1F
        rt = instr & 0x1F
        return f"LDR X{rt}, [X{rn}], #{imm9}"

    # BL: 0x94000000 | imm26
    if (instr & 0xFC000000) == 0x94000000:
        imm26 = instr & 0x3FFFFFF
        if imm26 & 0x2000000:
            imm26 -= 0x4000000
        off = imm26 * 4
        return f"BL #{off}"

    # B: 0x14000000 | imm26
    if (instr & 0xFC000000) == 0x14000000:
        imm26 = instr & 0x3FFFFFF
        if imm26 & 0x2000000:
            imm26 -= 0x4000000
        off = imm26 * 4
        return f"B #{off}"

    # Bcond: 0x54000000 | (imm19 << 5) | cond
    if (instr & 0xFF000010) == 0x54000000:
        imm19 = (instr >> 5) & 0x7FFFF
        if imm19 & 0x40000:
            imm19 -= 0x80000
        cond = instr & 0xF
        cond_names = ["EQ","NE","CS","CC","MI","PL","VS","VC","HI","LS","GE","LT","GT","LE"]
        off = imm19 * 4
        return f"B.{cond_names[cond]} #{off}"

    # CSET: 0x9A1F0400 | (inv_cond << 12) | Rd
    if (instr & 0xFFFFFC1F) == 0x9A1F0400:
        inv_cond = (instr >> 12) & 0xF
        cond = inv_cond ^ 1
        rd = instr & 0x1F
        cond_names = ["EQ","NE","CS","CC","MI","PL","VS","VC","HI","LS","GE","LT","GT","LE"]
        return f"CSET X{rd}, {cond_names[cond]}"

    # ADR: 0x10000000 | (immlo << 29) | (immhi << 5) | Rd
    if (instr & 0x9F000000) == 0x10000000:
        immlo = (instr >> 29) & 0x3
        immhi = (instr >> 5) & 0x7FFFF
        off = (immhi << 2) | immlo
        if off & 0x100000:
            off -= 0x200000
        rd = instr & 0x1F
        return f"ADR X{rd}, #{off}"

    return f".word 0x{instr:08x}"


def main():
    if len(sys.argv) < 2:
        print("Usage: disasm_arm64.py <binary> [start_offset] [end_offset]")
        sys.exit(1)

    path = sys.argv[1]
    start = int(sys.argv[2], 0) if len(sys.argv) > 2 else 120  # default: skip ELF header
    end = int(sys.argv[3], 0) if len(sys.argv) > 3 else 0

    with open(path, "rb") as f:
        data = f.read()

    if end == 0:
        end = len(data)

    print(f"Disassembling {path} from offset {start} to {end}")
    print(f"{'Offset':>8}  {'Bytes':<12} Instruction")
    print("-" * 60)

    off = start
    while off + 4 <= end:
        instr = struct.unpack("<I", data[off:off+4])[0]
        bytes_str = " ".join(f"{b:02x}" for b in data[off:off+4])
        desc = decode_arm64(instr)
        print(f"{off:>8}  {bytes_str:<12} {desc}")
        off += 4


if __name__ == "__main__":
    main()
