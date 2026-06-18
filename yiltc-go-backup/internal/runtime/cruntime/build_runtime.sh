#!/usr/bin/env bash
# build_runtime.sh — compile runtime.c and extract binary sections that gen.go embeds.
#
# This is the "step 1-7" from internal/runtime/gen.go's header comment:
#   1. Compile runtime.c → runtime.o
#   2. Extract .text → runtime.bin
#   3. Extract .rodata.str1.1 → rodata_str.bin  (combined with .rodata.str1.8)
#   4. Extract .rodata → rodata.bin
#   5. Extract .rodata.cst4 → rodata_cst4.bin
#   6. Extract .rodata.cst8 → rodata_cst8.bin
#   7. Extract .rodata.cst16 → rodata_cst16.bin
#
# After this, the existing symbol/reloc tables in gen.go (which the spec
# author already generated via regen.py) should match the .bin contents.
#
# Usage:
#   cd /home/z/my-project/yiltc/internal/runtime/cruntime
#   bash build_runtime.sh
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "[1/7] Compiling runtime.c → runtime.o"
# -Wno-int-conversion: runtime.c uses (uint64_t)"string" pattern (intentional
#   tagged-value punning) which gcc 14+ rejects as error by default.
# -include stdint.h: runtime.c uses uint8_t without including stdint.h directly.
# -fno-pic -fno-stack-protector -nostdlib: required for freestanding runtime.
gcc -c -O2 -fno-pic -fno-stack-protector -nostdlib \
    -Wno-int-conversion -Wno-incompatible-pointer-types \
    -include stdint.h \
    -o runtime.o runtime.c
echo "  size: $(stat -c '%s' runtime.o) bytes"

echo "[2/7] Extracting .text → runtime.bin"
objcopy -O binary -j .text runtime.o runtime.bin
echo "  size: $(stat -c '%s' runtime.bin) bytes"

echo "[3/7] Extracting .rodata.str1.1 → rodata_str.bin (first half)"
objcopy -O binary -j .rodata.str1.1 runtime.o rodata_str_part1.bin
echo "  size: $(stat -c '%s' rodata_str_part1.bin) bytes"

echo "[3b/7] Extracting .rodata.str1.8 → rodata_str.bin (second half)"
# .rodata.str1.8 may not exist; tolerate failure
if objcopy -O binary -j .rodata.str1.8 runtime.o rodata_str_part2.bin 2>/dev/null; then
    echo "  size: $(stat -c '%s' rodata_str_part2.bin) bytes"
    # Combine
    cat rodata_str_part1.bin rodata_str_part2.bin > rodata_str.bin
    rm rodata_str_part1.bin rodata_str_part2.bin
else
    echo "  .rodata.str1.8 not present; using just .rodata.str1.1"
    mv rodata_str_part1.bin rodata_str.bin
fi
echo "  combined rodata_str.bin: $(stat -c '%s' rodata_str.bin) bytes"

echo "[4/7] Extracting .rodata → rodata.bin"
objcopy -O binary -j .rodata runtime.o rodata.bin
echo "  size: $(stat -c '%s' rodata.bin) bytes"

echo "[5/7] Extracting .rodata.cst4 → rodata_cst4.bin"
if objcopy -O binary -j .rodata.cst4 runtime.o rodata_cst4.bin 2>/dev/null; then
    echo "  size: $(stat -c '%s' rodata_cst4.bin) bytes"
else
    echo "  .rodata.cst4 not present; creating empty file"
    : > rodata_cst4.bin
fi

echo "[6/7] Extracting .rodata.cst8 → rodata_cst8.bin"
if objcopy -O binary -j .rodata.cst8 runtime.o rodata_cst8.bin 2>/dev/null; then
    echo "  size: $(stat -c '%s' rodata_cst8.bin) bytes"
else
    echo "  .rodata.cst8 not present; creating empty file"
    : > rodata_cst8.bin
fi

echo "[7/7] Extracting .rodata.cst16 → rodata_cst16.bin"
if objcopy -O binary -j .rodata.cst16 runtime.o rodata_cst16.bin 2>/dev/null; then
    echo "  size: $(stat -c '%s' rodata_cst16.bin) bytes"
else
    echo "  .rodata.cst16 not present; creating empty file"
    : > rodata_cst16.bin
fi

echo
echo "Done. Generated .bin files:"
ls -la *.bin
