#!/usr/bin/env python3
"""Minimal WASM module parser and interpreter for verifying Yilt codegen output.

Parses a WASM binary module and executes the _start function.
Implements enough of the WASM MVP spec to run simple Yilt programs:
- i64 arithmetic (ADD, SUB, MUL, DIV_S, REM_S, AND, OR, XOR, SHL, SHR_S)
- i64 comparisons (EQ, NE, LT_S, GT_S, LE_S, GE_S, EQZ)
- Control flow (BLOCK, LOOP, IF, ELSE, END, BR, BR_IF, RETURN)
- Variables (LOCAL_GET, LOCAL_SET, LOCAL_TEE)
- Constants (I64_CONST, I32_CONST)
- Calls (CALL)
- Memory (I64_LOAD, I64_STORE, etc.)
- Select
"""

import struct
import sys


class WasmModule:
    def __init__(self):
        self.types = []  # list of (params, results)
        self.functions = []  # list of type indices
        self.exports = {}  # name -> (kind, index)
        self.memories = []  # list of (min, max)
        self.code = []  # list of (locals, body bytes)
        self.data = []  # list of (offset, bytes)
        self.memory = bytearray(65536)  # 1 page = 64KB
        self.func_names = {}  # index -> name (reverse of exports)


def parse_module(path):
    with open(path, "rb") as f:
        data = f.read()

    mod = WasmModule()
    pos = 0

    # Magic and version
    if data[:4] != b"\x00asm":
        raise Exception("Not a WASM module")
    pos = 4
    version = struct.unpack("<I", data[pos:pos+4])[0]
    pos += 4
    print(f"[Parser] WASM version: {version}")

    while pos < len(data):
        section_id = data[pos]
        pos += 1
        size, pos = read_leb128_u32(data, pos)
        section_end = pos + size
        print(f"[Parser] Section {section_id}, size {size}")

        if section_id == 1:  # Type section
            count, pos = read_leb128_u32(data, pos)
            for _ in range(count):
                form = data[pos]; pos += 1  # 0x60
                param_count, pos = read_leb128_u32(data, pos)
                params = []
                for _ in range(param_count):
                    params.append(data[pos]); pos += 1
                result_count, pos = read_leb128_u32(data, pos)
                results = []
                for _ in range(result_count):
                    results.append(data[pos]); pos += 1
                mod.types.append((params, results))
                print(f"  Type {len(mod.types)-1}: ({params}) -> ({results})")
        elif section_id == 3:  # Function section
            count, pos = read_leb128_u32(data, pos)
            for _ in range(count):
                type_idx, pos = read_leb128_u32(data, pos)
                mod.functions.append(type_idx)
        elif section_id == 5:  # Memory section
            count, pos = read_leb128_u32(data, pos)
            for _ in range(count):
                flags = data[pos]; pos += 1
                min_pages, pos = read_leb128_u32(data, pos)
                max_pages = None
                if flags & 1:
                    max_pages, pos = read_leb128_u32(data, pos)
                mod.memories.append((min_pages, max_pages))
        elif section_id == 7:  # Export section
            count, pos = read_leb128_u32(data, pos)
            for _ in range(count):
                name_len, pos = read_leb128_u32(data, pos)
                name = data[pos:pos+name_len].decode("utf-8")
                pos += name_len
                kind = data[pos]; pos += 1
                idx, pos = read_leb128_u32(data, pos)
                mod.exports[name] = (kind, idx)
                if kind == 0:  # function
                    mod.func_names[idx] = name
                print(f"  Export '{name}': kind={kind}, idx={idx}")
        elif section_id == 10:  # Code section
            count, pos = read_leb128_u32(data, pos)
            for _ in range(count):
                body_size, pos = read_leb128_u32(data, pos)
                body_end = pos + body_size
                local_count, pos = read_leb128_u32(data, pos)
                locals = []
                for _ in range(local_count):
                    n, pos = read_leb128_u32(data, pos)
                    t = data[pos]; pos += 1
                    locals.append((n, t))
                body = data[pos:body_end]
                mod.code.append((locals, body))
                pos = body_end
        elif section_id == 11:  # Data section
            count, pos = read_leb128_u32(data, pos)
            for _ in range(count):
                mem_idx, pos = read_leb128_u32(data, pos)
                # Offset expression: i32.const <offset> end
                op = data[pos]; pos += 1
                if op == 0x41:  # i32.const
                    offset, pos = read_leb128_i32(data, pos)
                    end_op = data[pos]; pos += 1  # 0x0b (end)
                else:
                    raise Exception(f"Unsupported offset expr op: 0x{op:02x}")
                data_size, pos = read_leb128_u32(data, pos)
                data_bytes = data[pos:pos+data_size]
                pos += data_size
                mod.data.append((offset, data_bytes))
                # Initialize memory
                for i, b in enumerate(data_bytes):
                    mod.memory[offset + i] = b
        else:
            # Skip unknown sections
            pos = section_end

        pos = section_end

    return mod


def read_leb128_u32(data, pos):
    result = 0
    shift = 0
    while True:
        b = data[pos]
        pos += 1
        result |= (b & 0x7F) << shift
        if (b & 0x80) == 0:
            break
        shift += 7
    return result, pos


def read_leb128_i32(data, pos):
    result = 0
    shift = 0
    while True:
        b = data[pos]
        pos += 1
        result |= (b & 0x7F) << shift
        if (b & 0x80) == 0:
            # Sign extend
            if b & 0x40:
                result |= -(1 << (shift + 7))
            break
        shift += 7
    return result, pos


def read_leb128_i64(data, pos):
    result = 0
    shift = 0
    while True:
        b = data[pos]
        pos += 1
        result |= (b & 0x7F) << shift
        if (b & 0x80) == 0:
            if b & 0x40:
                result |= -(1 << (shift + 7))
            break
        shift += 7
    return result, pos


def to_signed64(v):
    v &= 0xFFFFFFFFFFFFFFFF
    if v & 0x8000000000000000:
        return v - 0x10000000000000000
    return v


def to_unsigned64(v):
    return v & 0xFFFFFFFFFFFFFFFF


class WasmInterpreter:
    def __init__(self, mod):
        self.mod = mod
        self.stack = []
        self.locals = []

    def push(self, v):
        self.stack.append(v)

    def pop(self):
        return self.stack.pop()

    def call_function(self, idx, args):
        type_idx = self.mod.functions[idx]
        params, results = self.mod.types[type_idx]
        locals_list, body = self.mod.code[idx]

        # Set up locals: params + declared locals
        all_locals = list(args)
        for count, val_type in locals_list:
            for _ in range(count):
                if val_type == 0x7E:  # i64
                    all_locals.append(0)
                else:
                    all_locals.append(0)

        saved_locals = self.locals
        self.locals = all_locals
        saved_stack_len = len(self.stack)

        # Execute body
        result = self.execute(body, 0, len(body))

        # Get return value
        ret_val = None
        if results:
            ret_val = self.pop()

        # Restore state
        self.locals = saved_locals
        # Truncate stack to saved length
        while len(self.stack) > saved_stack_len:
            self.stack.pop()

        return ret_val

    def execute(self, body, start, end):
        """Execute instructions from body[start:end]. Returns None or a label for branching."""
        pos = start
        while pos < end:
            op = body[pos]
            pos += 1

            if op == 0x0B:  # END
                return None
            elif op == 0x0F:  # RETURN
                return "return"
            elif op == 0x42:  # I64_CONST
                val, pos = read_leb128_i64(body, pos)
                self.push(val)
            elif op == 0x41:  # I32_CONST
                val, pos = read_leb128_i32(body, pos)
                self.push(val)
            elif op == 0x20:  # LOCAL_GET
                idx, pos = read_leb128_u32(body, pos)
                self.push(self.locals[idx])
            elif op == 0x21:  # LOCAL_SET
                idx, pos = read_leb128_u32(body, pos)
                self.locals[idx] = self.pop()
            elif op == 0x22:  # LOCAL_TEE
                idx, pos = read_leb128_u32(body, pos)
                self.locals[idx] = self.stack[-1]
            elif op == 0x1A:  # DROP
                self.pop()
            elif op == 0x7C:  # I64_ADD
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(a + b)
            elif op == 0x7D:  # I64_SUB
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(a - b)
            elif op == 0x7E:  # I64_MUL
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(a * b)
            elif op == 0x7F:  # I64_DIV_S
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                if b == 0:
                    raise Exception("Division by zero")
                self.push(int(a / b))
            elif op == 0x81:  # I64_REM_S
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                if b == 0:
                    raise Exception("Modulo by zero")
                # Truncated division remainder
                q = int(a / b)
                self.push(a - q * b)
            elif op == 0x83:  # I64_AND
                b = to_unsigned64(self.pop())
                a = to_unsigned64(self.pop())
                self.push(to_signed64(a & b))
            elif op == 0x84:  # I64_OR
                b = to_unsigned64(self.pop())
                a = to_unsigned64(self.pop())
                self.push(to_signed64(a | b))
            elif op == 0x85:  # I64_XOR
                b = to_unsigned64(self.pop())
                a = to_unsigned64(self.pop())
                self.push(to_signed64(a ^ b))
            elif op == 0x86:  # I64_SHL
                b = to_unsigned64(self.pop()) & 63
                a = to_unsigned64(self.pop())
                self.push(to_signed64(a << b))
            elif op == 0x87:  # I64_SHR_S
                b = to_unsigned64(self.pop()) & 63
                a = to_signed64(self.pop())
                self.push(a >> b)
            elif op == 0x51:  # I64_EQ
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(1 if a == b else 0)
            elif op == 0x52:  # I64_NE
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(1 if a != b else 0)
            elif op == 0x53:  # I64_LT_S
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(1 if a < b else 0)
            elif op == 0x55:  # I64_GT_S
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(1 if a > b else 0)
            elif op == 0x57:  # I64_LE_S
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(1 if a <= b else 0)
            elif op == 0x59:  # I64_GE_S
                b = to_signed64(self.pop())
                a = to_signed64(self.pop())
                self.push(1 if a >= b else 0)
            elif op == 0x50:  # I64_EQZ
                a = to_signed64(self.pop())
                self.push(1 if a == 0 else 0)
            elif op == 0x10:  # CALL
                idx, pos = read_leb128_u32(body, pos)
                type_idx = self.mod.functions[idx]
                params, results = self.mod.types[type_idx]
                args = []
                for _ in range(len(params)):
                    args.insert(0, self.pop())
                ret = self.call_function(idx, args)
                if ret is not None:
                    self.push(ret)
            elif op == 0x29:  # I64_LOAD
                align, pos = read_leb128_u32(body, pos)
                offset, pos = read_leb128_u32(body, pos)
                addr = to_unsigned64(self.pop()) + offset
                val = struct.unpack("<q", bytes(self.mod.memory[addr:addr+8]))[0]
                self.push(val)
            elif op == 0x37:  # I64_STORE
                align, pos = read_leb128_u32(body, pos)
                offset, pos = read_leb128_u32(body, pos)
                val = to_signed64(self.pop())
                addr = to_unsigned64(self.pop()) + offset
                self.mod.memory[addr:addr+8] = struct.pack("<q", val)
            elif op == 0x31:  # I64_LOAD8_U
                align, pos = read_leb128_u32(body, pos)
                offset, pos = read_leb128_u32(body, pos)
                addr = to_unsigned64(self.pop()) + offset
                self.push(self.mod.memory[addr])
            elif op == 0x3C:  # I64_STORE8
                align, pos = read_leb128_u32(body, pos)
                offset, pos = read_leb128_u32(body, pos)
                val = to_signed64(self.pop()) & 0xFF
                addr = to_unsigned64(self.pop()) + offset
                self.mod.memory[addr] = val
            elif op == 0xA7:  # I32_WRAP_I64
                v = to_signed64(self.pop())
                self.push(v & 0xFFFFFFFF)
            elif op == 0x02:  # BLOCK
                block_type = body[pos]; pos += 1
                # Find matching END
                end_pos = find_end(body, pos)
                # Execute block
                self.execute(body, pos, end_pos)
                pos = end_pos + 1
            elif op == 0x03:  # LOOP
                block_type = body[pos]; pos += 1
                end_pos = find_end(body, pos)
                # Loop: execute repeatedly until broken
                while True:
                    result = self.execute(body, pos, end_pos)
                    if result == "return":
                        return "return"
                    if result is not None and isinstance(result, tuple) and result[0] == "br":
                        depth = result[1]
                        if depth == 0:
                            continue  # loop back
                        else:
                            return ("br", depth - 1)
                    break  # no break, exit loop
                pos = end_pos + 1
            elif op == 0x04:  # IF
                block_type = body[pos]; pos += 1
                cond = self.pop()
                end_pos = find_end(body, pos)
                else_pos = find_else(body, pos, end_pos)
                if cond != 0:
                    if else_pos is not None:
                        result = self.execute(body, pos, else_pos)
                    else:
                        result = self.execute(body, pos, end_pos)
                else:
                    result = None
                    if else_pos is not None:
                        result = self.execute(body, else_pos + 1, end_pos)
                if result == "return":
                    return "return"
                if result is not None and isinstance(result, tuple) and result[0] == "br":
                    # Propagate branch out of the if block
                    depth = result[1]
                    if depth > 0:
                        result = ("br", depth - 1)
                    else:
                        result = None  # branch to end of if (fall through)
                    if result is not None:
                        return result
                pos = end_pos + 1
            elif op == 0x05:  # ELSE
                # Shouldn't reach here directly
                pass
            elif op == 0x0C:  # BR
                depth, pos = read_leb128_u32(body, pos)
                return ("br", depth)
            elif op == 0x0D:  # BR_IF
                depth, pos = read_leb128_u32(body, pos)
                cond = self.pop()
                if cond != 0:
                    return ("br", depth)
            elif op == 0x1B:  # SELECT
                c = self.pop()
                b = self.pop()
                a = self.pop()
                self.push(a if c != 0 else b)
            else:
                raise Exception(f"Unknown opcode 0x{op:02x} at pos {pos-1}")

        return None


def find_end(body, start):
    """Find the matching END for a block/loop/if starting at `start`."""
    depth = 1
    pos = start
    while pos < len(body):
        op = body[pos]
        pos += 1
        if op in (0x02, 0x03, 0x04):  # BLOCK, LOOP, IF
            pos += 1  # skip block type
            depth += 1
        elif op == 0x05:  # ELSE
            pass
        elif op == 0x0B:  # END
            depth -= 1
            if depth == 0:
                return pos - 1
        elif op in (0x0C, 0x0D):  # BR, BR_IF
            _, pos = read_leb128_u32(body, pos)
        elif op == 0x10:  # CALL
            _, pos = read_leb128_u32(body, pos)
        elif op in (0x20, 0x21, 0x22, 0x23, 0x24):  # LOCAL/GLOBAL ops
            _, pos = read_leb128_u32(body, pos)
        elif op in (0x28, 0x29, 0x2D, 0x31, 0x36, 0x37, 0x3A, 0x3C):  # MEMORY ops
            _, pos = read_leb128_u32(body, pos)
            _, pos = read_leb128_u32(body, pos)
        elif op == 0x41:  # I32_CONST
            _, pos = read_leb128_i32(body, pos)
        elif op == 0x42:  # I64_CONST
            _, pos = read_leb128_i64(body, pos)
        elif op == 0x0E:  # BR_TABLE
            n, pos = read_leb128_u32(body, pos)
            for _ in range(n + 1):
                _, pos = read_leb128_u32(body, pos)
    raise Exception("No matching END found")


def find_else(body, start, end):
    """Find an ELSE within an IF block, if any."""
    depth = 1
    pos = start
    while pos < end:
        op = body[pos]
        pos += 1
        if op in (0x02, 0x03, 0x04):
            pos += 1
            depth += 1
        elif op == 0x05:  # ELSE
            if depth == 1:
                return pos - 1
        elif op == 0x0B:  # END
            depth -= 1
            if depth == 0:
                return None
        elif op in (0x0C, 0x0D):
            _, pos = read_leb128_u32(body, pos)
        elif op == 0x10:
            _, pos = read_leb128_u32(body, pos)
        elif op in (0x20, 0x21, 0x22, 0x23, 0x24):
            _, pos = read_leb128_u32(body, pos)
        elif op in (0x28, 0x29, 0x2D, 0x31, 0x36, 0x37, 0x3A, 0x3C):
            _, pos = read_leb128_u32(body, pos)
            _, pos = read_leb128_u32(body, pos)
        elif op == 0x41:
            _, pos = read_leb128_i32(body, pos)
        elif op == 0x42:
            _, pos = read_leb128_i64(body, pos)
    return None


def main():
    if len(sys.argv) < 2:
        print("Usage: emulate_wasm.py <module.wasm>")
        sys.exit(1)

    mod = parse_module(sys.argv[1])
    interp = WasmInterpreter(mod)

    if "_start" in mod.exports:
        kind, idx = mod.exports["_start"]
        if kind == 0:  # function
            print(f"[Interpreter] Calling _start (function {idx})")
            result = interp.call_function(idx, [])
            print(f"[Interpreter] Result: {result}")
            sys.exit(result if result is not None else 0)
    print("[Interpreter] No _start export found")


if __name__ == "__main__":
    main()
