#!/usr/bin/env python3
"""
regen.py — Regenerate the Go symbol/relocation tables in gen.go from runtime.o.

Usage:
    cd internal/runtime/cruntime
    python3 regen.py > ../gen_tables.go.inc

Then paste the output into gen.go (or use the --update flag to do it automatically).

Requires: nm, objdump, readelf (binutils)
"""

import subprocess
import sys
import os
import re
import struct

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
RUNTIME_O = os.path.join(SCRIPT_DIR, "runtime.o")

# Size of .rodata.str1.1 section — appended after it in the combined binary.
# .rodata.str1.8 relocations get this offset added to their addend.
RODATA_STR1_1_SIZE = None  # computed from readelf -S


def run(cmd):
    """Run a command and return stdout."""
    result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"Command failed: {cmd}", file=sys.stderr)
        print(result.stderr, file=sys.stderr)
        sys.exit(1)
    return result.stdout


def parse_nm_symbols(nm_output):
    """Parse 'nm -n runtime.o' output. Returns list of (addr, type_char, name)."""
    symbols = []
    for line in nm_output.strip().splitlines():
        # Format: "00000000000004d0 T y_print"
        parts = line.split()
        if len(parts) >= 3:
            addr = int(parts[0], 16)
            type_char = parts[1]
            name = parts[2]
            symbols.append((addr, type_char, name))
    return symbols


def parse_readelf_symbols(readelf_output):
    """Parse 'readelf -s runtime.o' output. Returns dict of name -> (section_idx, size)."""
    syms = {}
    for line in readelf_output.strip().splitlines():
        # Format: "    18: 00000000000004d0  1217 FUNC    GLOBAL DEFAULT    1 y_print"
        m = re.match(r'\s+\d+:\s+([0-9a-f]+)\s+(\d+)\s+\w+\s+\w+\s+\w+\s+(\d+)\s+(.+)', line)
        if m:
            addr = int(m.group(1), 16)
            size = int(m.group(2))
            section_idx = int(m.group(3))
            name = m.group(4).strip()
            syms[name] = (section_idx, size)
    return syms


def parse_readelf_sections(readelf_output):
    """Parse 'readelf -S runtime.o' output. Returns dict of section_idx -> section_name."""
    sections = {}
    for line in readelf_output.strip().splitlines():
        # Format: "  [ 6] .rodata.str1.1    PROGBITS ..."
        m = re.match(r'\s+\[\s*(\d+)\]\s+(\S+)', line)
        if m:
            idx = int(m.group(1))
            name = m.group(2)
            sections[idx] = name
            # Also look for size on the same or next line
    return sections


def parse_readelf_sections_with_sizes(readelf_output):
    """Parse 'readelf -S runtime.o' output. Returns dict of section_name -> size."""
    sections = {}
    lines = readelf_output.strip().splitlines()
    i = 0
    while i < len(lines):
        line = lines[i]
        m = re.match(r'\s+\[\s*\d+\]\s+(\S+)', line)
        if m:
            name = m.group(1)
            # Size might be on the same line or next line
            combined = line
            if i + 1 < len(lines) and not re.match(r'\s+\[\s*\d+\]', lines[i+1]):
                combined += " " + lines[i+1]
            # Look for size pattern: hex number after the name field
            # The size is typically after address and offset
            size_m = re.search(r'([0-9a-f]{8,})\s+([0-9a-f]{8,})\s+([0-9a-f]{8,})', combined)
            if size_m:
                sections[name] = int(size_m.group(3), 16)
        i += 1
    return sections


def parse_objdump_relocs(objdump_output):
    """Parse 'objdump -r runtime.o' output. Returns dict of section -> list of relocs."""
    sections = {}
    current_section = None
    for line in objdump_output.strip().splitlines():
        if line.startswith("RELOCATION RECORDS FOR"):
            m = re.search(r'\[(.+)\]', line)
            if m:
                current_section = m.group(1)
                if current_section not in sections:
                    sections[current_section] = []
            continue
        if current_section is None:
            continue
        # Skip header line
        if "OFFSET" in line and "TYPE" in line:
            continue
        # Parse relocation line: "0000000000000471 R_X86_64_32       .rodata.str1.1+0x000000000000001b"
        m = re.match(r'\s*([0-9a-f]+)\s+(R_X86_64_\w+)\s+(.+)', line)
        if m:
            offset = int(m.group(1), 16)
            reloc_type = m.group(2)
            value = m.group(3).strip()
            sections[current_section].append((offset, reloc_type, value))
    return sections


def resolve_lc_label(label, lc_map):
    """Resolve a .LC label to (section_name, offset_within_section)."""
    if label in lc_map:
        return lc_map[label]
    return None


def main():
    global RODATA_STR1_1_SIZE

    # 1. Get symbol table from nm
    nm_output = run(f"nm -n {RUNTIME_O}")
    nm_symbols = parse_nm_symbols(nm_output)

    # 2. Get symbol sizes from readelf
    readelf_s_output = run(f"readelf -s {RUNTIME_O}")
    readelf_syms = parse_readelf_symbols(readelf_s_output)

    # 3. Get section info from readelf
    readelf_S_output = run(f"readelf -S {RUNTIME_O}")
    section_map = parse_readelf_sections(readelf_S_output)
    section_sizes = parse_readelf_sections_with_sizes(readelf_S_output)

    # Get size of .rodata.str1.1
    RODATA_STR1_1_SIZE = section_sizes.get('.rodata.str1.1', 0)

    # 4. Build LC label map: label -> (section_name, offset)
    lc_map = {}
    for name, (sec_idx, size) in readelf_syms.items():
        if name.startswith('.LC'):
            sec_name = section_map.get(sec_idx, f"unknown_section_{sec_idx}")
            # Get the address of the label from nm
            label_addr = None
            for addr, typ, n in nm_symbols:
                if n == name and typ == 'r':
                    label_addr = addr
                    break
            lc_map[name] = (sec_name, label_addr if label_addr is not None else 0)

    # 5. Get relocations from objdump
    objdump_output = run(f"objdump -r {RUNTIME_O}")
    reloc_sections = parse_objdump_relocs(objdump_output)

    # ---- Generate symbol table ----
    # Include both T (global) and t (local) symbols so that internal helper
    # functions (hash_tagged, values_equal.part.0, y_tab_set.part.0) are also
    # added as linker code sections. This preserves PC-relative call offsets
    # within the .text blob.
    text_symbols = [(addr, name) for addr, typ, name in nm_symbols if typ in ('T', 't')]

    print("// runtimeSymbolTable lists all functions in the embedded .text section,")
    print("// sorted by offset. Generated from: nm -n runtime.o | grep ' [tT] '")
    print("var runtimeSymbolTable = []runtimeSymbol{")
    text_size = section_sizes.get('.text', 0)
    for i, (addr, name) in enumerate(text_symbols):
        # Get size from readelf if available, otherwise use offset diff
        if name in readelf_syms:
            _, elf_size = readelf_syms[name]
            size = elf_size
        elif i + 1 < len(text_symbols):
            size = text_symbols[i + 1][0] - addr
        else:
            size = text_size - addr
        print(f'\t{{"{name}", 0x{addr:04x}, 0x{size:04x}}},')
    print("}")
    print()

    # ---- Relocation type mapping ----
    reloc_type_map = {
        'R_X86_64_32': 'RelocAbs32',
        'R_X86_64_32S': 'RelocAbs32S',
        'R_X86_64_PC32': 'RelocPC32',
        'R_X86_64_PLT32': 'RelocPLT32',
    }

    # ---- Generate .text relocations ----
    text_relocs = reloc_sections.get('.text', [])
    print("// Generated from: objdump -r runtime.o (RELOCATION RECORDS FOR [.text])")
    print("var runtimeRelocations = []RuntimeReloc{")
    for offset, reloc_type, value in text_relocs:
        go_type = reloc_type_map.get(reloc_type)
        if go_type is None:
            print(f'\t// WARNING: unknown reloc type {reloc_type} at 0x{offset:04x}: {value}', file=sys.stderr)
            continue

        # Parse value: can be:
        #   .rodata.str1.1+0x000000000000001b
        #   .rodata.str1.8
        #   .rodata+0x0000000000000050
        #   .rodata.cst8
        #   .rodata.cst16
        #   .rodata.cst4
        #   y_print-0x0000000000000004
        #   .LC8-0x0000000000000004
        #   .text (section ref)

        addend = 0
        target = ""
        symbol = ""

        # Parse the value field. Format examples:
        #   .rodata.str1.1+0x000000000000001b   (section + positive offset)
        #   .rodata.str1.8                       (section, no offset)
        #   .rodata+0x0000000000000050           (section + positive offset)
        #   .LC8-0x0000000000000004             (LC label + implicit -4 addend)
        #   y_print-0x0000000000000004          (function + implicit -4 addend)
        #   .rodata.cst8                        (section, no offset)

        # Extract positive and negative addends separately
        pos_addend = 0
        neg_addend = 0
        base = value

        # Handle positive addend first (e.g., +0x1b)
        pos_m = re.search(r'\+0x([0-9a-fA-F]+)', value)
        if pos_m:
            pos_addend = int(pos_m.group(1), 16)
            base = value[:pos_m.start()]

        # Handle negative addend (e.g., -0x4) — strip for PC32/PLT32 (implicit)
        neg_m = re.search(r'-0x([0-9a-fA-F]+)', base)
        if neg_m:
            neg_addend = int(neg_m.group(1), 16)
            base = base[:neg_m.start()]

        base = base.strip()

        # For PC32/PLT32, the negative addend (-4) is implicit in the instruction
        # encoding and handled by the linker's relocation formula. Do NOT include
        # it in the Go Addend field. Only LC label offsets and positive addends matter.
        if reloc_type in ('R_X86_64_PC32', 'R_X86_64_PLT32'):
            addend = pos_addend  # ignore neg_addend (implicit -4)
        else:
            # For Abs32/Abs32S, include all addends
            addend = pos_addend - neg_addend

        # Resolve base
        if base.startswith('.LC'):
            # LC label
            resolved = resolve_lc_label(base, lc_map)
            if resolved:
                sec_name, lc_offset = resolved
                addend += lc_offset
                if sec_name == '.rodata.cst8':
                    target = "TargetRodataCst8"
                elif sec_name == '.rodata.cst16':
                    target = "TargetRodataCst16"
                elif sec_name == '.rodata.cst4':
                    target = "TargetRodataCst4"
                else:
                    print(f'\t// WARNING: LC label {base} in unexpected section {sec_name}', file=sys.stderr)
                    target = f"TargetUnknown /* {sec_name} */"
            else:
                print(f'\t// WARNING: unresolved LC label {base}', file=sys.stderr)
                target = f"TargetUnknown /* {base} */"
        elif base == '.rodata.str1.1':
            target = "TargetRodataStr"
        elif base == '.rodata.str1.8':
            target = "TargetRodataStr"
            # Adjust addend: .rodata.str1.8 starts after .rodata.str1.1 in combined binary
            addend += RODATA_STR1_1_SIZE
        elif base == '.rodata':
            target = "TargetRodata"
        elif base == '.rodata.cst8':
            target = "TargetRodataCst8"
        elif base == '.rodata.cst16':
            target = "TargetRodataCst16"
        elif base == '.rodata.cst4':
            target = "TargetRodataCst4"
        elif base == '.text':
            # Section reference to .text itself (used in .eh_frame, skip for .text relocs)
            continue
        elif base.startswith('y_') or base.startswith('hash_tagged') or base.startswith('values_equal') or base.startswith('y_tab_set.part'):
            # Function reference
            target = "TargetText"
            symbol = base
        else:
            print(f'\t// WARNING: unknown relocation target {base} at 0x{offset:04x}', file=sys.stderr)
            continue

        # Format addend
        if addend >= 0:
            addend_str = f"0x{addend:x}"
        else:
            addend_str = f"-0x{-addend:x}"

        symbol_str = f', "{symbol}"' if symbol else ', ""'

        print(f'\t{{0x{offset:04x}, {go_type}, {target}, {addend_str}{symbol_str}}},')

    print("}")
    print()

    # ---- Generate .rodata relocations ----
    rodata_relocs = reloc_sections.get('.rodata', [])
    print("// Generated from: objdump -r runtime.o (RELOCATION RECORDS FOR [.rodata])")
    print("// All entries are R_X86_64_64 (absolute 64-bit) referencing .text section symbol.")
    print("var runtimeRodataRelocations = []RuntimeRodataReloc{")
    for offset, reloc_type, value in rodata_relocs:
        if reloc_type != 'R_X86_64_64':
            print(f'\t// WARNING: unexpected rodata reloc type {reloc_type} at 0x{offset:04x}', file=sys.stderr)
            continue

        # Parse value: .text+0x0000000000000508
        addend = 0
        if '+0x' in value:
            parts = value.split('+0x')
            addend = int(parts[1], 16)
        elif '-0x' in value:
            parts = value.split('-0x')
            addend = -int(parts[1], 16)

        print(f'\t{{0x{offset:04x}, 0x{addend:04x}}},')

    print("}")


if __name__ == '__main__':
    main()
