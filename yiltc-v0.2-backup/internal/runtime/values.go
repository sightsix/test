// Package runtime defines the embedded runtime for Yilt compiled programs.
//
// The runtime is not a separate library that gets linked — it is data
// structures, symbol tables, and code templates that the Yilt compiler's
// code-generation backends reference when emitting machine code.  Every Yilt
// binary includes the runtime inlined into the final executable.
//
// # Tagged Value Encoding
//
// All Yilt values are represented as 64-bit words with the following layout:
//
//      Bits 63-56: Tag   (8 bits) — identifies the type
//      Bits  55-0: Value (56 bits) — payload
//
//      Integers are stored directly in the 56-bit payload (signed, two's complement).
//      Booleans use 0/1 in the payload.
//      Floats, strings, tables, and errors store a pointer to a heap-allocated object
//      in the payload (truncated to 56 bits on 64-bit platforms; see ValueMask).
//
// # Calling Convention
//
// Runtime functions use the platform's System V or Windows calling convention.
// All Yilt values (arguments and return values) are passed as a single u64
// (the tagged value).  Pointers to arena or table structures are passed as
// raw pointers (uintptr-sized).
package runtime

// ---------------------------------------------------------------------------
// Tag constants
//
// These occupy bits 56-63 of a tagged 64-bit value.  Values 0x06-0x0D are
// reserved for future types (function references, handles, etc.).
// ---------------------------------------------------------------------------

const (
        TagVoid  uint8 = 0x0 // void / unit — no meaningful payload
        TagInt   uint8 = 0x1 // signed 56-bit integer
        TagBool  uint8 = 0x2 // boolean (payload is 0 or 1)
        TagFP    uint8 = 0x3 // float — payload is pointer to boxed f64
        TagStr   uint8 = 0x4 // string — payload is pointer to StrHeader
        TagTable uint8 = 0x5 // table — payload is pointer to TableHeader
        // 0x06–0x0D reserved
        TagErr  uint8 = 0xE // error — payload is pointer to error string
        TagNil  uint8 = 0x9 // nil — payload is 0 (also represented as TagVoid=0 for backward compat)
)

// TagName returns a human-readable name for a tag byte.
func TagName(tag uint8) string {
        switch tag {
        case TagVoid:
                return "void"
        case TagInt:
                return "int"
        case TagBool:
                return "bool"
        case TagFP:
                return "fp"
        case TagStr:
                return "str"
        case TagTable:
                return "table"
        case TagErr:
                return "error"
        case TagNil:
                return "nil"
        default:
                return "unknown"
        }
}

// ---------------------------------------------------------------------------
// Bitmask constants for tagged-value manipulation
//
// The x86-64 code generator inlines these as compile-time constants.
// ---------------------------------------------------------------------------

const (
        // TagShift is the number of bits the tag is shifted left.
        TagShift = 56

        // TagMask covers the tag byte (bits 56-63).
        TagMask uint64 = 0xFF << TagShift

        // ValueMask covers the payload (bits 0-55).
        ValueMask uint64 = 0x00FFFFFFFFFFFFFF

        // MaxIntPayload is the largest positive integer representable in the
        // 56-bit signed payload.
        MaxIntPayload = 1<<55 - 1

        // MinIntPayload is the most negative integer representable in the
        // 56-bit signed payload.
        MinIntPayload = -(1 << 55)
)

// ---------------------------------------------------------------------------
// Sentinel values
// ---------------------------------------------------------------------------

// NilValue is the tagged representation of nil / void.
const NilValue uint64 = 0 // tag=TagVoid, payload=0

// TrueValue is the tagged representation of true.
const TrueValue uint64 = (1 << 0) | (uint64(TagBool) << TagShift)

// FalseValue is the tagged representation of false.
const FalseValue uint64 = (uint64(TagBool) << TagShift)

// ---------------------------------------------------------------------------
// Tagging / Untagging helpers (used at compile time and for testing)
//
// Backends emit these as inline bit-manipulation instructions.  The Go
// functions below are provided so that the compiler can unit-test the
// encoding and so that the linker can compute relocations.
// ---------------------------------------------------------------------------

// MakeTagged creates a tagged value from a tag byte and a raw 56-bit payload.
func MakeTagged(tag uint8, payload uint64) uint64 {
        return (payload & ValueMask) | (uint64(tag) << TagShift)
}

// TagInt64 encodes a Go int64 as a tagged Yilt integer value.
// The value is truncated to 56 bits.  Returns NilValue on overflow.
func TagInt64(v int64) uint64 {
        if v < MinIntPayload || v > MaxIntPayload {
                return NilValue // overflow sentinel
        }
        return MakeTagged(TagInt, uint64(v))
}

// UntagInt extracts the signed integer from a tagged integer value.
func UntagInt(v uint64) int64 {
        return int64(v & ValueMask)
}

// MakeTaggedBool encodes a Go bool as a tagged boolean value.
func MakeTaggedBool(b bool) uint64 {
        if b {
                return TrueValue
        }
        return FalseValue
}

// UntagBool extracts the boolean from a tagged boolean value.
func UntagBool(v uint64) bool {
        return (v & ValueMask) != 0
}

// MakeTaggedFP encodes a pointer to a boxed float64 as a tagged float value.
// The pointer is truncated to 56 bits (see ValueMask).
func MakeTaggedFP(ptr uintptr) uint64 {
        return MakeTagged(TagFP, uint64(ptr))
}

// UntagFP extracts the pointer to the boxed float64.
func UntagFP(v uint64) uintptr {
        return uintptr(v & ValueMask)
}

// MakeTaggedStr encodes a pointer to a StrHeader as a tagged string value.
func MakeTaggedStr(ptr uintptr) uint64 {
        return MakeTagged(TagStr, uint64(ptr))
}

// UntagStr extracts the pointer to the StrHeader.
func UntagStr(v uint64) uintptr {
        return uintptr(v & ValueMask)
}

// MakeTaggedTable encodes a pointer to a TableHeader as a tagged table value.
func MakeTaggedTable(ptr uintptr) uint64 {
        return MakeTagged(TagTable, uint64(ptr))
}

// UntagTable extracts the pointer to the TableHeader.
func UntagTable(v uint64) uintptr {
        return uintptr(v & ValueMask)
}

// MakeTaggedErr encodes a pointer to an error string object as a tagged error value.
func MakeTaggedErr(ptr uintptr) uint64 {
        return MakeTagged(TagErr, uint64(ptr))
}

// UntagErr extracts the pointer from a tagged error value.
func UntagErr(v uint64) uintptr {
        return uintptr(v & ValueMask)
}

// GetTag extracts the tag byte (bits 56-63) from any tagged value.
func GetTag(v uint64) uint8 {
        return uint8(v >> TagShift)
}

// IsTag returns true if v's tag matches the expected tag.
func IsTag(v uint64, tag uint8) bool {
        return GetTag(v) == tag
}

// ---------------------------------------------------------------------------
// Type checking predicates
//
// Backends emit these as inline comparisons against the tag byte.
// The Go implementations are provided for the compiler's own use when
// performing constant folding and type inference.
// ---------------------------------------------------------------------------

// IsInt returns true if the tagged value carries an integer.
func IsInt(v uint64) bool { return GetTag(v) == TagInt }

// IsBool returns true if the tagged value carries a boolean.
func IsBool(v uint64) bool { return GetTag(v) == TagBool }

// IsFP returns true if the tagged value carries a boxed float.
func IsFP(v uint64) bool { return GetTag(v) == TagFP }

// IsStr returns true if the tagged value carries a string.
func IsStr(v uint64) bool { return GetTag(v) == TagStr }

// IsTable returns true if the tagged value carries a table.
func IsTable(v uint64) bool { return GetTag(v) == TagTable }

// IsErr returns true if the tagged value carries an error.
func IsErr(v uint64) bool { return GetTag(v) == TagErr }

// IsVoid returns true if the tagged value is nil/void.
func IsVoid(v uint64) bool { return GetTag(v) == TagVoid }

// IsNil returns true if the tagged value is the nil sentinel.
func IsNil(v uint64) bool { return v == NilValue }

// ---------------------------------------------------------------------------
// Promotion / coercion rules
//
// When a binary operator receives operands of different types, Yilt applies
// automatic promotion to find a common type:
//
//      int + int   -> int
//      int + fp    -> fp    (int is converted to float)
//      fp  + fp    -> fp
//      int + str   -> str   (int is formatted then concatenated)
//      str + str   -> str
//      str + <any> -> str   (right side formatted then concatenated)
// ---------------------------------------------------------------------------

// PromoteResult returns the tag of the result type when two tagged values are
// combined by a binary operator.  If promotion is impossible it returns TagErr.
func PromoteResult(leftTag, rightTag uint8) uint8 {
        if leftTag == rightTag {
                return leftTag // same type — no promotion needed
        }
        // int <-> fp: promote to fp
        if (leftTag == TagInt && rightTag == TagFP) ||
                (leftTag == TagFP && rightTag == TagInt) {
                return TagFP
        }
        // anything + str: promote to str (concatenation with formatting)
        if leftTag == TagStr || rightTag == TagStr {
                return TagStr
        }
        // int + bool: treat bool as int (0 or 1)
        if (leftTag == TagInt && rightTag == TagBool) ||
                (leftTag == TagBool && rightTag == TagInt) {
                return TagInt
        }
        return TagErr // incompatible types
}

// CanPromoteTo returns true if a value with srcTag can be promoted to dstTag.
func CanPromoteTo(srcTag, dstTag uint8) bool {
        if srcTag == dstTag {
                return true
        }
        // int -> fp
        if srcTag == TagInt && dstTag == TagFP {
                return true
        }
        // bool -> int
        if srcTag == TagBool && dstTag == TagInt {
                return true
        }
        // <any> -> str (via formatting)
        if dstTag == TagStr {
                return true
        }
        return false
}

// ---------------------------------------------------------------------------
// Boxed float representation
//
// Float values are heap-allocated to keep the tagged word fixed at 64 bits.
// Layout (16 bytes):
//
//      +0  f64 value (8 bytes, IEEE 754)
//      +8  (padding / future use)
// ---------------------------------------------------------------------------

// FPBoxSize is the size in bytes of a boxed float64 allocation.
const FPBoxSize = 16

// FPBoxValueOffset is the offset of the float64 value within the box.
const FPBoxValueOffset = 0

// ---------------------------------------------------------------------------
// Value formatting
//
// The runtime needs to format tagged values for print(), error messages,
// and string concatenation.  The following table describes how each type
// is converted to its string representation.
//
//      void  -> ""
//      int   -> signed decimal, no leading zeros
//      bool  -> "true" or "false"
//      fp    -> shortest decimal that round-trips (sprintf "%g")
//      str   -> raw bytes (no quoting)
//      table -> "[table N]" where N is the entry count
//      err   -> "error: <message>"
//
// Backends emit calls to y_format_val for this.
// ---------------------------------------------------------------------------

// ValueTypeString returns the type name of a tagged value as a short string.
// This is the implementation of y_type_of at the Go level.
func ValueTypeString(v uint64) string {
        return TagName(GetTag(v))
}

// FormatValue returns a string representation of a tagged value.
// This is the reference implementation for y_print and y_str_concat promotion.
func FormatValue(v uint64) string {
        switch GetTag(v) {
        case TagVoid:
                return ""
        case TagInt:
                return int64ToStr(UntagInt(v))
        case TagBool:
                if UntagBool(v) {
                        return "true"
                }
                return "false"
        case TagFP:
                // The actual runtime dereferences the boxed pointer.
                // This Go helper is for compiler-side use only.
                return "<fp>"
        case TagStr:
                // The actual runtime dereferences the string header.
                return "<str>"
        case TagTable:
                return "<table>"
        case TagErr:
                return "<error>"
        default:
                return "<unknown>"
        }
}

// int64ToStr converts a signed 56-bit integer to its decimal string form.
// This is used by the runtime's print and formatting code paths.
func int64ToStr(v int64) string {
        if v == 0 {
                return "0"
        }
        neg := false
        if v < 0 {
                neg = true
                v = -v
        }
        buf := make([]byte, 0, 20)
        for v > 0 {
                buf = append(buf, byte('0'+v%10))
                v /= 10
        }
        if neg {
                buf = append(buf, '-')
        }
        // reverse
        for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
                buf[i], buf[j] = buf[j], buf[i]
        }
        return string(buf)
}

// ---------------------------------------------------------------------------
// Equality comparison (y_val_eq)
//
// Two tagged values are equal if and only if:
//   - They have the same tag AND
//   - Their payloads compare equal according to the type's rules.
//
// For integers and booleans this is a direct bitwise comparison.
// For floats, IEEE 754 equality (after unboxing).
// For strings, bytewise comparison (length + hash shortcut first).
// For tables, pointer identity (not structural equality).
// For errors, string comparison of the message.
// For void/nil, both must be void/nil.
// ---------------------------------------------------------------------------

// ValuesEqual is the reference implementation for y_val_eq.
// It returns true if the two tagged values are considered equal.
// Pointers (for FP, Str, Table, Err) should be dereferenced by the
// actual runtime; this version does shallow pointer comparison only.
func ValuesEqual(a, b uint64) bool {
        tagA := GetTag(a)
        tagB := GetTag(b)
        if tagA != tagB {
                return false
        }
        // Void/nil: both zero
        if tagA == TagVoid {
                return true
        }
        // Int, Bool: bitwise payload comparison
        if tagA == TagInt || tagA == TagBool {
                return (a & ValueMask) == (b & ValueMask)
        }
        // FP, Str, Table, Err: pointer identity (shallow)
        // The real runtime dereferences FP and compares strings by content.
        return (a & ValueMask) == (b & ValueMask)
}

// ---------------------------------------------------------------------------
// Copy semantics (y_copy)
//
// Yilt uses value semantics for ints, bools, and floats (copy the payload).
// Strings and tables use reference semantics with arena-based lifetime:
// y_copy returns the same tagged value (no deep copy).
// ---------------------------------------------------------------------------

// CopyValue performs a shallow copy of a tagged value.
// For immediate types (int, bool) this copies the word.
// For heap types (str, table, fp, err) this returns the same pointer.
func CopyValue(v uint64) uint64 {
        return v // tagged values are immutable words; copy = duplicate the word
}
