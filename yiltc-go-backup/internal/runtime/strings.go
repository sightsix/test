package runtime

// ---------------------------------------------------------------------------
// String Representation
//
// Yilt strings are heap-allocated, immutable, and reference-counted
// implicitly by arena lifetime.  Each string is represented by a header
// followed by the character data.
//
// # StrHeader Layout (32 bytes, 8-byte aligned)
//
//  typedef struct y_str {
//      uint64_t len;    // +0: byte length of the string data
//      uint64_t hash;   // +8: cached FNV-1a hash (computed on creation)
//      uint64_t cap;    // +16: allocated capacity (>= len, for substrings)
//      // data follows immediately at +24
//  } y_str;
//
// The data bytes are stored inline after the header, so a pointer to a
// StrHeader also serves as the start of the full allocation.
//
// Total allocation size = 24 + len + padding_to_8_byte_boundary.
// ---------------------------------------------------------------------------

const (
    // StrHeaderSize is the size of the string header in bytes.
    StrHeaderSize = 24

    // StrOffLen is the offset of the length field within StrHeader.
    StrOffLen = 0

    // StrOffHash is the offset of the cached hash field.
    StrOffHash = 8

    // StrOffCap is the offset of the capacity field.
    StrOffCap = 16

    // StrOffData is the offset where string data begins (right after header).
    StrOffData = 24

    // StrMaxInlineLen is the maximum length that can be stored inline.
    // Strings longer than this use the arena allocator.
    StrMaxInlineLen = 22 // 56 - StrHeaderSize doesn't apply; this is for inline tagged values
)

// ---------------------------------------------------------------------------
// Hash function: FNV-1a (64-bit)
//
// The Yilt runtime uses FNV-1a for string hashing and table key hashing.
//
//  FNV offset basis: 14695981039346656037
//  FNV prime:        1099511628211
//
// Algorithm:
//   hash = FNV_offset_basis
//   for each byte b in input:
//     hash ^= b
//     hash *= FNV_prime
//   return hash
//
// Properties:
//   - Fast: one multiply + XOR per byte.
//   - Good distribution for typical string data.
//   - Deterministic across all platforms (no endianness issues for byte data).
// ---------------------------------------------------------------------------

const (
    // FNV1aOffsetBasis is the initial hash value for FNV-1a.
    FNV1aOffsetBasis uint64 = 14695981039346656037

    // FNV1aPrime is the FNV-1a prime multiplier.
    FNV1aPrime uint64 = 1099511628211
)

// FNV1a computes the FNV-1a hash of a byte slice.
func FNV1a(data []byte) uint64 {
    h := FNV1aOffsetBasis
    for _, b := range data {
        h ^= uint64(b)
        h *= FNV1aPrime
    }
    return h
}

// FNV1aString computes the FNV-1a hash of a Go string.
func FNV1aString(s string) uint64 {
    h := FNV1aOffsetBasis
    for i := 0; i < len(s); i++ {
        h ^= uint64(s[i])
        h *= FNV1aPrime
    }
    return h
}

// FNV1aUint64 computes the FNV-1a hash of a uint64 value treated as
// 8 bytes in little-endian order.
func FNV1aUint64(v uint64) uint64 {
    h := FNV1aOffsetBasis
    for i := 0; i < 8; i++ {
        h ^= uint64(uint8(v >> (uint(i) * 8)))
        h *= FNV1aPrime
    }
    return h
}

// ---------------------------------------------------------------------------
// Tagged-value hash (for table keys)
//
// The hash of a tagged value combines the tag byte with a hash of the payload.
// This avoids collisions between values of different types that happen to have
// the same payload bits.
//
//  hash = FNV1a(tag_byte) ^ payload_hash
//
// For integers: payload_hash = FNV1a of the 8-byte little-endian integer.
// For booleans: payload_hash = 0 or 1.
// For strings: payload_hash = the cached hash from the StrHeader.
// For floats: payload_hash = FNV1a of the 8-byte IEEE 754 representation.
// For tables: payload_hash = pointer value (identity-based).
// For errors: payload_hash = FNV1a of the error message bytes.
// ---------------------------------------------------------------------------

// HashTagged computes a hash for a tagged value suitable for use as a
// hash table key.  The tag byte is folded into the hash to prevent
// collisions across types.
func HashTagged(v uint64) uint64 {
    tag := uint64(GetTag(v))
    payload := v & ValueMask

    var payloadHash uint64
    switch GetTag(v) {
    case TagVoid:
        payloadHash = 0
    case TagInt:
        payloadHash = FNV1aUint64(payload)
    case TagBool:
        if payload != 0 {
            payloadHash = 1
        }
    case TagFP:
        // The runtime dereferences the boxed float and hashes the IEEE 754 bits.
        payloadHash = FNV1aUint64(payload)
    case TagStr:
        // The runtime reads hash from the StrHeader.
        // This Go helper can't dereference native memory, so approximate.
        payloadHash = FNV1aUint64(payload)
    case TagTable:
        // Identity-based: hash the pointer value.
        payloadHash = payload
    case TagErr:
        payloadHash = FNV1aUint64(payload)
    default:
        payloadHash = payload
    }

    // Combine tag and payload hash.
    h := FNV1aOffsetBasis
    h ^= tag
    h *= FNV1aPrime
    h ^= payloadHash
    h *= FNV1aPrime
    return h
}

// ---------------------------------------------------------------------------
// String operations (runtime function reference implementations)
//
// These Go functions document the semantics of each runtime string
// function.  The actual machine code emitted by backends implements
// these operations directly using the memory layouts above.
//
// # y_str_new(data: *u8, len: u64, arena: *Arena) -> tagged_str
//
// Allocates a StrHeader + data from arena, copies the bytes, computes
// and caches the FNV-1a hash, returns a tagged string value.
//
// # y_str_concat(a: tagged_str, b: tagged_str, arena: *Arena) -> tagged_str
//
// Creates a new string that is the concatenation of a and b.
// Allocates from arena.  Returns tagged string.
//
// # y_substr(s: tagged_str, start: i64, end: i64, arena: *Arena) -> tagged_str
//
// Extracts the substring s[start:end].  Panics if indices are out of range.
// Negative indices count from the end.
//
// # y_trim(s: tagged_str, arena: *Arena) -> tagged_str
//
// Removes leading and trailing ASCII whitespace.
//
// # y_lower(s: tagged_str, arena: *Arena) -> tagged_str
//
// Converts all ASCII letters to lowercase.
//
// # y_upper(s: tagged_str, arena: *Arena) -> tagged_str
//
// Converts all ASCII letters to uppercase.
//
// # y_contains(s: tagged_str, substr: tagged_str) -> tagged_bool
//
// Returns true if s contains substr.
//
// # y_starts_with(s: tagged_str, prefix: tagged_str) -> tagged_bool
//
// Returns true if s starts with prefix.
//
// # y_ends_with(s: tagged_str, suffix: tagged_str) -> tagged_bool
//
// Returns true if s ends with suffix.
//
// # y_find(s: tagged_str, substr: tagged_str) -> tagged_int
//
// Returns the byte index of the first occurrence of substr in s, or -1.
// ---------------------------------------------------------------------------

// StrAllocSize returns the total number of bytes needed to allocate a
// string of the given length (header + data + padding).
func StrAllocSize(len uint64) uint64 {
    dataLen := len
    total := StrHeaderSize + dataLen
    // Align to 8 bytes.
    return AlignUp(total, 8)
}

// ---------------------------------------------------------------------------
// String equality (used by y_val_eq for string operands)
//
// Two strings are equal if they have the same length and the same bytes.
// The cached hash is used as a fast-path rejection: if hashes differ,
// the strings cannot be equal.
//
// Implementation:
//   1. If a == b (same pointer), return true.
//   2. If a->len != b->len, return false.
//   3. If a->hash != b->hash, return false (probabilistic shortcut).
//   4. Compare bytes with memcmp(a->data, b->data, a->len).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ASCII whitespace characters recognized by y_trim
// ---------------------------------------------------------------------------

const asciiSpace = " \t\n\r\f\v"

// IsASCIISpace returns true if b is an ASCII whitespace character.
func IsASCIISpace(b byte) bool {
    return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

// ---------------------------------------------------------------------------
// String allocation sizes for common operations
//
// These are used by the compiler to estimate stack / arena requirements.
// ---------------------------------------------------------------------------

// StrConcatAllocSize returns the arena allocation needed for concatenating
// two strings of the given lengths.
func StrConcatAllocSize(lenA, lenB uint64) uint64 {
    return StrAllocSize(lenA + lenB)
}

// StrSubstrAllocSize returns the arena allocation needed for a substring
// of the given length.
func StrSubstrAllocSize(length uint64) uint64 {
    return StrAllocSize(length)
}
