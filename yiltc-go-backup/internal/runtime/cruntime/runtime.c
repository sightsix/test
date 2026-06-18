// ---------------------------------------------------------------------------
// Yilt C Runtime — x86_64 Linux (no libc)
//
// This file implements the core runtime functions for Yilt compiled programs.
// It is compiled to a position-independent-free object file and the raw .text
// section is embedded into the Go compiler binary.
//
// All I/O uses raw Linux syscalls. Memory allocation uses mmap(MAP_ANONYMOUS).
// No libc, no external dependencies.
// ---------------------------------------------------------------------------

typedef unsigned long      uint64_t;
typedef long              int64_t;
typedef unsigned long      size_t;

#include <math.h>

// ---------------------------------------------------------------------------
// Tagged Value System
//
// All Yilt values are 64-bit words:
//   Bits 63-56: Tag (type identifier)
//   Bits 55-0:  Payload (data or pointer)
//
// Tag constants:
//   0 = void/nil
//   1 = int (signed 56-bit)
//   2 = bool (payload 0=false, 1=true)
//   3 = float (payload = pointer to boxed f64)
//   4 = string (payload = pointer to StrHeader)
//   5 = table (payload = pointer to TableHeader)
//   9 = nil (used in some contexts; same as void=0 for NilValue)
//   14 = error
// ---------------------------------------------------------------------------

#define TAG_SHIFT    56
#define TAG_INT      1
#define TAG_BOOL     2
#define TAG_FP       3
#define TAG_STR      4
#define TAG_TABLE    5
#define TAG_NIL      9
#define TAG_ERR      14

#define TAG_MASK     (0xFFULL << TAG_SHIFT)
#define VALUE_MASK   0x00FFFFFFFFFFFFFFULL

#define MK_TAG(tag, val) (((uint64_t)(tag) << TAG_SHIFT) | ((uint64_t)(val) & VALUE_MASK))
#define GET_TAG(v)       ((int)((v) >> TAG_SHIFT))
#define GET_PTR(v)       ((void*)((v) & VALUE_MASK))
#define GET_INT(v)       ((int64_t)(int64_t)(v << 8) >> 8)  // sign-extend 56-bit

// NilValue = 0 (tag=0, payload=0)
#define NIL_VALUE 0ULL
// TrueValue: tag=2, payload=1
#define TRUE_VALUE MK_TAG(TAG_BOOL, 1)
// FalseValue: tag=2, payload=0
#define FALSE_VALUE MK_TAG(TAG_BOOL, 0)

// ---------------------------------------------------------------------------
// String Header
//
// Layout (24 bytes):
//   +0  refcount  int64_t  (reserved for future use)
//   +8  len       int64_t  (byte length of string data)
//   +16 cap       int64_t  (allocated capacity >= len)
//   +24 data[]             (flexible array member, NOT null-terminated in general)
//
// NOTE: The Go runtime package defines StrHeaderSize = 24, with fields at
// offsets 0, 8, 16 and data at offset 24. We match that layout exactly.
// ---------------------------------------------------------------------------

typedef struct {
    int64_t  refcount;   // +0
    int64_t  len;        // +8
    int64_t  cap;        // +16
    char     data[];     // +24, flexible array member
} StrHeader;

// ---------------------------------------------------------------------------
// Table Header (64 bytes, 8-byte aligned)
//
// Layout:
//   +0  count      uint64
//   +8  capacity   uint64
//   +16 threshold  uint64
//   +24 mask       uint64
//   +32 entries    pointer
//   +40 entry_cap  uint64
//   +48 tombstones uint64
//   +56 padding    uint64
// ---------------------------------------------------------------------------

typedef struct {
    uint64_t count;
    uint64_t capacity;
    uint64_t threshold;
    uint64_t mask;
    void    *entries;
    uint64_t entry_cap;
    uint64_t tombstones;
    uint64_t _pad;
} TableHeader;

// ---------------------------------------------------------------------------
// Table Entry (32 bytes per slot)
//
// Layout:
//   +0  key       uint64  (tagged key value)
//   +8  value     uint64  (tagged value)
//   +16 hash      uint64  (cached key hash)
//   +24 occupied  uint64  (0=empty, 1=occupied, 2=tombstone)
// ---------------------------------------------------------------------------

typedef struct {
    uint64_t key;       // +0
    uint64_t value;     // +8
    uint64_t hash;      // +16
    uint64_t occupied;  // +24
} TableEntry;

#define ENTRY_SIZE     32
#define ENTRY_EMPTY     0
#define ENTRY_OCCUPIED  1
#define ENTRY_TOMBSTONE 2

// ---------------------------------------------------------------------------
// Syscall wrappers (no libc)
// ---------------------------------------------------------------------------

static long syscall1(long nr, long a) {
    long ret;
    __asm__ volatile ("syscall" : "=a"(ret) : "a"(nr), "D"(a) : "rcx", "r11", "memory");
    return ret;
}

static long syscall2(long nr, long a, long b) {
    long ret;
    __asm__ volatile ("syscall" : "=a"(ret) : "a"(nr), "D"(a), "S"(b) : "rcx", "r11", "memory");
    return ret;
}

static long syscall3(long nr, long a, long b, long c) {
    long ret;
    __asm__ volatile ("syscall" : "=a"(ret) : "a"(nr), "D"(a), "S"(b), "d"(c) : "rcx", "r11", "memory");
    return ret;
}

static long syscall6(long nr, long a, long b, long c, long d, long e, long f) {
    long ret;
    register long r10 __asm__("r10") = d;
    register long r8  __asm__("r8")  = e;
    register long r9  __asm__("r9")  = f;
    __asm__ volatile ("syscall"
        : "=a"(ret)
        : "a"(nr), "D"(a), "S"(b), "d"(c), "r"(r10), "r"(r8), "r"(r9)
        : "rcx", "r11", "memory");
    return ret;
}

#define SYS_read   0
#define SYS_write  1
#define SYS_exit   60
#define SYS_mmap   9
#define SYS_munmap 11

#define PROT_READ     0x1
#define PROT_WRITE    0x2
#define MAP_PRIVATE   0x02
#define MAP_ANONYMOUS 0x20

static void *raw_mmap(size_t size) {
    void *p = (void *)syscall6(SYS_mmap,
        0,                // addr
        (long)size,       // length
        PROT_READ | PROT_WRITE,
        MAP_PRIVATE | MAP_ANONYMOUS,
        -1,               // fd
        0);               // offset
    return p;
}

static void raw_write(int fd, const void *buf, size_t len) {
    syscall3(SYS_write, fd, (long)buf, (long)len);
}

static long raw_read(int fd, void *buf, size_t len) {
    return syscall3(SYS_read, fd, (long)buf, (long)len);
}

static void raw_exit(int code) {
    syscall1(SYS_exit, code);
    __builtin_unreachable();
}

// ---------------------------------------------------------------------------
// Utility functions (no libc)
// ---------------------------------------------------------------------------

static void my_memcpy(void *dst, const void *src, size_t n) {
    char *d = (char *)dst;
    const char *s = (const char *)src;
    for (size_t i = 0; i < n; i++)
        d[i] = s[i];
}

static int my_memcmp(const void *a, const void *b, size_t n) {
    const unsigned char *pa = (const unsigned char *)a;
    const unsigned char *pb = (const unsigned char *)b;
    for (size_t i = 0; i < n; i++) {
        if (pa[i] < pb[i]) return -1;
        if (pa[i] > pb[i]) return 1;
    }
    return 0;
}

static int my_isspace(char c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v';
}

static int my_isdigit(char c) {
    return c >= '0' && c <= '9';
}

static char my_tolower(char c) {
    if (c >= 'A' && c <= 'Z') return (char)(c + 32);
    return c;
}

static char my_toupper(char c) {
    if (c >= 'a' && c <= 'z') return (char)(c - 32);
    return c;
}

// ---------------------------------------------------------------------------
// Hash function for tagged values
//
// For strings, hashes the string contents (FNV-1a).
// For ints/bools, hashes the payload.
// For other types, hashes the raw 64-bit value.
// ---------------------------------------------------------------------------

static uint64_t hash_tagged(uint64_t val) {
    int tag = GET_TAG(val);
    if (tag == TAG_STR) {
        StrHeader *h = (StrHeader *)GET_PTR(val);
        if (!h) return 0;
        uint64_t hash = 14695981039346656037ULL;
        uint64_t prime = 1099511628211ULL;
        for (int64_t i = 0; i < h->len; i++) {
            hash ^= (unsigned char)h->data[i];
            hash *= prime;
        }
        return hash;
    }
    if (tag == TAG_INT || tag == TAG_BOOL) {
        // Use payload directly with some mixing
        uint64_t payload = val & VALUE_MASK;
        payload ^= payload >> 33;
        payload *= 0xff51afd7ed558ccdULL;
        payload ^= payload >> 33;
        payload *= 0xc4ceb9fe1a85ec53ULL;
        payload ^= payload >> 33;
        return payload;
    }
    // Default: FNV-1a over the raw 8 bytes
    uint64_t hash = 14695981039346656037ULL;
    uint64_t prime = 1099511628211ULL;
    for (int i = 0; i < 8; i++) {
        hash ^= (val >> (i * 8)) & 0xFF;
        hash *= prime;
    }
    return hash;
}

// ---------------------------------------------------------------------------
// Value equality check (content-based for strings)
// ---------------------------------------------------------------------------

static int values_equal(uint64_t a, uint64_t b) {
    int ta = GET_TAG(a);
    int tb = GET_TAG(b);
    if (ta != tb) return 0;
    if (ta == TAG_STR) {
        StrHeader *ha = (StrHeader *)GET_PTR(a);
        StrHeader *hb = (StrHeader *)GET_PTR(b);
        if (ha == hb) return 1;
        if (!ha || !hb) return 0;
        if (ha->len != hb->len) return 0;
        return my_memcmp(ha->data, hb->data, (size_t)ha->len) == 0;
    }
    return a == b;
}

// ---------------------------------------------------------------------------
// Table helper functions
// ---------------------------------------------------------------------------

static void table_alloc_entries(TableHeader *t) {
    uint64_t cap = t->capacity;
    size_t size = ENTRY_SIZE * (size_t)cap;
    size = (size + 7) & ~(size_t)7;
    void *e = raw_mmap(size);
    if ((long)e < 0 && (long)e > -4096) {
        raw_write(2, "table_alloc_entries: mmap failed\n", 33);
        raw_exit(99);
    }
    // mmap with MAP_ANONYMOUS returns zeroed memory
    t->entries = e;
    t->entry_cap = cap;
}

static void table_rehash(TableHeader *t) {
    void *old_entries = t->entries;
    uint64_t old_cap = t->capacity;

    // Double capacity (capped at 2^28)
    uint64_t new_cap = old_cap * 2;
    if (new_cap > (1ULL << 28)) new_cap = (1ULL << 28);

    t->capacity = new_cap;
    t->mask = new_cap - 1;
    t->threshold = new_cap * 3 / 4;
    t->count = 0;
    t->tombstones = 0;

    // Allocate new entry array
    size_t size = ENTRY_SIZE * (size_t)new_cap;
    size = (size + 7) & ~(size_t)7;
    void *new_entries = raw_mmap(size);
    if ((long)new_entries < 0 && (long)new_entries > -4096) {
        raw_write(2, "table_rehash: mmap failed\n", 27);
        raw_exit(99);
    }
    t->entries = new_entries;
    t->entry_cap = new_cap;

    // Re-insert all occupied entries from old array
    if (old_entries) {
        TableEntry *old_e = (TableEntry *)old_entries;
        for (uint64_t i = 0; i < old_cap; i++) {
            if (old_e[i].occupied == ENTRY_OCCUPIED) {
                // Insert into new table (no recursive rehash)
                uint64_t idx = old_e[i].hash & t->mask;
                TableEntry *new_e = (TableEntry *)t->entries;
                for (;;) {
                    if (new_e[idx].occupied == ENTRY_EMPTY) {
                        new_e[idx].key = old_e[i].key;
                        new_e[idx].value = old_e[i].value;
                        new_e[idx].hash = old_e[i].hash;
                        new_e[idx].occupied = ENTRY_OCCUPIED;
                        t->count++;
                        break;
                    }
                    idx = (idx + 1) & t->mask;
                }
            }
        }
        // Free old entries
        syscall6(SYS_munmap, (long)old_entries, ENTRY_SIZE * (size_t)old_cap, 0, 0, 0, 0);
    }
}

// ---------------------------------------------------------------------------
// Integer-to-string conversion (in-place, returns pointer to start)
// ---------------------------------------------------------------------------

static char *int64_to_str(int64_t val, char *buf_end) {
    char *p = buf_end;
    *--p = '\0';
    if (val == 0) {
        *--p = '0';
        return p;
    }
    int neg = 0;
    if (val < 0) {
        neg = 1;
        // Handle INT64_MIN safely
        if (val == (-9223372036854775807LL - 1)) {
            // "-9223372036854775808"
            const char *min_str = "-9223372036854775808";
            size_t min_len = 20;
            // Copy backwards
            p -= min_len;
            for (size_t i = 0; i < min_len; i++) {
                p[i] = min_str[i];
            }
            return p;
        }
        val = -val;
    }
    while (val > 0) {
        *--p = '0' + (val % 10);
        val /= 10;
    }
    if (neg) {
        *--p = '-';
    }
    return p;
}

// ---------------------------------------------------------------------------
// Double-to-string conversion (for y_print / debugging)
// ---------------------------------------------------------------------------

static size_t double_to_str(double d, char *buf, size_t bufsize) {
    char *end = buf + bufsize;
    char *pos = end;
    *--pos = '\0';

    if (d != d) { // NaN
        pos -= 3; pos[0] = 'n'; pos[1] = 'a'; pos[2] = 'n';
        return (size_t)(end - 1 - pos);
    }
    if (d == 1.0/0.0) { // +inf
        pos -= 4; pos[0] = '+'; pos[1] = 'i'; pos[2] = 'n'; pos[3] = 'f';
        return (size_t)(end - 1 - pos);
    }
    if (d == -1.0/0.0) { // -inf
        pos -= 4; pos[0] = '-'; pos[1] = 'i'; pos[2] = 'n'; pos[3] = 'f';
        return (size_t)(end - 1 - pos);
    }

    int neg = 0;
    if (d < 0.0) { neg = 1; d = -d; }

    uint64_t ipart = (uint64_t)d;
    double fpart = d - (double)ipart;

    if (ipart == 0 && fpart == 0.0) {
        *--pos = '0';
    } else {
        if (ipart == 0) {
            *--pos = '0';
        } else {
            char digits[20];
            int nd = 0;
            while (ipart > 0) {
                digits[nd++] = '0' + (int)(ipart % 10);
                ipart /= 10;
            }
            for (int i = nd - 1; i >= 0; i--)
                *--pos = digits[i];
        }
        // Print up to 6 decimal places
        if (fpart > 0.0) {
            *--pos = '.';
            int ndigits = 0;
            while (fpart > 0.0 && ndigits < 6) {
                fpart *= 10.0;
                int digit = (int)fpart;
                *--pos = '0' + digit;
                fpart -= (double)digit;
                ndigits++;
            }
        }
    }
    if (neg) *--pos = '-';
    return (size_t)(end - 1 - pos);
}

// ---------------------------------------------------------------------------
// String-to-int64 conversion (no libc strtoll)
// ---------------------------------------------------------------------------

static int64_t str_to_int64(const char *s, int64_t len) {
    int64_t i = 0;
    // Skip leading whitespace
    while (i < len && my_isspace(s[i])) i++;
    if (i >= len) return 0;

    int neg = 0;
    if (s[i] == '-') { neg = 1; i++; }
    else if (s[i] == '+') { i++; }

    int64_t result = 0;
    while (i < len && my_isdigit(s[i])) {
        int digit = s[i] - '0';
        // Check for overflow
        if (result > (9223372036854775807LL / 10)) {
            if (neg) return -9223372036854775807LL - 1;
            return 9223372036854775807LL;
        }
        result = result * 10 + digit;
        i++;
    }
    return neg ? -result : result;
}

// ---------------------------------------------------------------------------
// String-to-double conversion (no libc strtod)
// ---------------------------------------------------------------------------

static double str_to_double(const char *s, int64_t len) {
    int64_t i = 0;
    // Skip leading whitespace
    while (i < len && my_isspace(s[i])) i++;
    if (i >= len) return 0.0;

    int neg = 0;
    if (s[i] == '-') { neg = 1; i++; }
    else if (s[i] == '+') { i++; }

    double result = 0.0;
    // Integer part
    while (i < len && my_isdigit(s[i])) {
        result = result * 10.0 + (s[i] - '0');
        i++;
    }
    // Fractional part
    if (i < len && s[i] == '.') {
        i++;
        double frac = 1.0;
        while (i < len && my_isdigit(s[i])) {
            frac *= 0.1;
            result += (s[i] - '0') * frac;
            i++;
        }
    }
    // Scientific notation (basic)
    if (i < len && (s[i] == 'e' || s[i] == 'E')) {
        i++;
        int eneg = 0;
        if (i < len && s[i] == '-') { eneg = 1; i++; }
        else if (i < len && s[i] == '+') { i++; }
        int exp = 0;
        while (i < len && my_isdigit(s[i])) {
            exp = exp * 10 + (s[i] - '0');
            i++;
        }
        double mul = 1.0;
        if (eneg) {
            while (exp > 0) { mul *= 0.1; exp--; }
        } else {
            while (exp > 0) { mul *= 10.0; exp--; }
        }
        result *= mul;
    }

    return neg ? -result : result;
}

// ===========================================================================
// Runtime function implementations
// ===========================================================================

// ---------------------------------------------------------------------------
// y_print(val) — Print a tagged value to stdout (no newline)
//
// Dispatches on the tag:
//   TAG_INT:  decimal representation
//   TAG_BOOL: "true" or "false"
//   TAG_STR:  raw string data
//   TAG_NIL/0: "nil"
//   others:   nothing
// ---------------------------------------------------------------------------

void y_print(uint64_t val) {
    int tag = GET_TAG(val);

    switch (tag) {
    case TAG_INT: {
        char buf[24];
        int64_t ival = GET_INT(val);
        char *s = int64_to_str(ival, buf + sizeof(buf));
        raw_write(1, s, buf + sizeof(buf) - 1 - s);
        break;
    }
    case TAG_BOOL: {
        uint64_t payload = val & VALUE_MASK;
        if (payload != 0) {
            raw_write(1, "true", 4);
        } else {
            raw_write(1, "false", 5);
        }
        break;
    }
    case TAG_STR: {
        StrHeader *h = (StrHeader *)GET_PTR(val);
        if (h) {
            raw_write(1, h->data, (size_t)h->len);
        }
        break;
    }
    case TAG_FP: {
        double *p = (double *)GET_PTR(val);
        double d = p ? p[0] : 0.0;
        char buf[64];
        size_t n = double_to_str(d, buf, sizeof(buf));
        raw_write(1, buf, n);
        break;
    }
    case 0:     // void/nil
    case TAG_NIL:
        raw_write(1, "nil", 3);
        break;
    case TAG_TABLE:
        raw_write(1, "[table]", 7);
        break;
    default:
        // Unknown type: print nothing
        break;
    }
}

// ---------------------------------------------------------------------------
// y_println(val) — Print a tagged value to stdout followed by newline
// ---------------------------------------------------------------------------

void y_println(uint64_t val) {
    y_print(val);
    raw_write(1, "\n", 1);
}

// ---------------------------------------------------------------------------
// y_str_new(data_ptr, len) — Create a new string from raw bytes
//
// Allocates StrHeader + data via mmap, copies the bytes, null-terminates.
// Returns tagged string value.
// ---------------------------------------------------------------------------

uint64_t y_str_new(const void *data_ptr, uint64_t len) {
    size_t total = sizeof(StrHeader) + (size_t)len + 1;
    // Align to 8 bytes
    total = (total + 7) & ~(size_t)7;

    StrHeader *h = (StrHeader *)raw_mmap(total);
    if ((long)h < 0 && (long)h > -4096) {
        // mmap failed — write error and exit
        raw_write(2, "y_str_new: mmap failed\n", 23);
        raw_exit(99);
    }

    h->refcount = 0;
    h->len = (int64_t)len;
    h->cap = (int64_t)len + 1;
    // Copy data
    if (data_ptr && len > 0) {
        my_memcpy(h->data, data_ptr, (size_t)len);
    }
    h->data[len] = '\0';
    return MK_TAG(TAG_STR, (uint64_t)h);
}

// ---------------------------------------------------------------------------
// y_sys_exit(code) — Exit the process
// ---------------------------------------------------------------------------

void y_sys_exit(uint64_t code) {
    raw_exit((int)(code & VALUE_MASK));
}

// ---------------------------------------------------------------------------
// y_table_new(cap_hint) — Create a new empty table
//
// Returns a tagged table value pointing to a TableHeader.
// The entries array is allocated and zeroed.
// ---------------------------------------------------------------------------

uint64_t y_table_new(uint64_t cap_hint) {
    size_t total = sizeof(TableHeader);
    total = (total + 7) & ~(size_t)7;

    TableHeader *t = (TableHeader *)raw_mmap(total);
    if ((long)t < 0 && (long)t > -4096) {
        raw_write(2, "y_table_new: mmap failed\n", 25);
        raw_exit(99);
    }

    // Default capacity
    uint64_t cap = 16;
    if (cap_hint > 16) cap = cap_hint;
    // Round up to power of 2
    cap--;
    cap |= cap >> 1;
    cap |= cap >> 2;
    cap |= cap >> 4;
    cap |= cap >> 8;
    cap |= cap >> 16;
    cap |= cap >> 32;
    cap++;
    if (cap < 16) cap = 16;

    t->count = 0;
    t->capacity = cap;
    t->threshold = cap * 3 / 4;
    t->mask = cap - 1;
    t->entries = 0;
    t->entry_cap = 0;
    t->tombstones = 0;
    t->_pad = 0;

    // Allocate the entries array
    table_alloc_entries(t);

    return MK_TAG(TAG_TABLE, (uint64_t)t);
}

// ---------------------------------------------------------------------------
// y_tab_set(table, key, val) — Insert or update key -> value in the table
//
// Triggers rehashing if the load factor is exceeded.
// Returns NIL_VALUE (void return).
// ---------------------------------------------------------------------------

uint64_t y_tab_set(uint64_t table, uint64_t key, uint64_t val) {
    if (GET_TAG(table) != TAG_TABLE) return NIL_VALUE;
    TableHeader *t = (TableHeader *)GET_PTR(table);
    if (!t) return NIL_VALUE;

    // Ensure entries are allocated
    if (!t->entries) {
        table_alloc_entries(t);
    }

    uint64_t h = hash_tagged(key);
    uint64_t idx = h & t->mask;
    uint64_t first_tombstone = (uint64_t)(-1); // -1 = no tombstone found
    TableEntry *entries = (TableEntry *)t->entries;

    for (uint64_t probe = 0; probe < t->capacity; probe++) {
        if (entries[idx].occupied == ENTRY_EMPTY) {
            // Found an empty slot
            if (first_tombstone != (uint64_t)(-1)) {
                // Reuse the earlier tombstone slot
                entries[first_tombstone].key = key;
                entries[first_tombstone].value = val;
                entries[first_tombstone].hash = h;
                entries[first_tombstone].occupied = ENTRY_OCCUPIED;
                // count stays the same (replacing tombstone doesn't add a new entry)
            } else {
                entries[idx].key = key;
                entries[idx].value = val;
                entries[idx].hash = h;
                entries[idx].occupied = ENTRY_OCCUPIED;
                t->count++;
            }
            // Check if we need to rehash
            if (t->count + t->tombstones >= t->threshold) {
                table_rehash(t);
            }
            return NIL_VALUE;
        }
        if (entries[idx].occupied == ENTRY_TOMBSTONE) {
            if (first_tombstone == (uint64_t)(-1)) {
                first_tombstone = idx;
            }
        } else if (entries[idx].occupied == ENTRY_OCCUPIED) {
            if (entries[idx].hash == h && values_equal(entries[idx].key, key)) {
                // Key already exists — update value
                entries[idx].value = val;
                return NIL_VALUE;
            }
        }
        idx = (idx + 1) & t->mask;
    }

    // Table full (should not happen with proper rehashing)
    raw_write(2, "panic: table full\n", 18);
    raw_exit(1);
    __builtin_unreachable();
    return NIL_VALUE;
}

// ---------------------------------------------------------------------------
// y_tab_get(table, key) — Lookup key in table, returns value or NIL_VALUE
// ---------------------------------------------------------------------------

uint64_t y_tab_get(uint64_t table, uint64_t key) {
    if (GET_TAG(table) != TAG_TABLE) return NIL_VALUE;
    TableHeader *t = (TableHeader *)GET_PTR(table);
    if (!t || !t->entries) return NIL_VALUE;

    uint64_t h = hash_tagged(key);
    uint64_t idx = h & t->mask;
    TableEntry *entries = (TableEntry *)t->entries;

    for (uint64_t probe = 0; probe < t->capacity; probe++) {
        if (entries[idx].occupied == ENTRY_EMPTY) {
            return NIL_VALUE; // Key not found
        }
        if (entries[idx].occupied == ENTRY_OCCUPIED &&
            entries[idx].hash == h &&
            values_equal(entries[idx].key, key)) {
            return entries[idx].value;
        }
        idx = (idx + 1) & t->mask;
    }
    return NIL_VALUE;
}

// ---------------------------------------------------------------------------
// y_tab_has(table, key) — Check if key exists, returns tagged bool
// ---------------------------------------------------------------------------

uint64_t y_tab_has(uint64_t t, uint64_t k) {
    if (GET_TAG(t) != TAG_TABLE) return FALSE_VALUE;
    TableHeader *th = (TableHeader *)GET_PTR(t);
    if (!th || !th->entries) return FALSE_VALUE;

    uint64_t h = hash_tagged(k);
    uint64_t idx = h & th->mask;
    TableEntry *entries = (TableEntry *)th->entries;

    for (uint64_t probe = 0; probe < th->capacity; probe++) {
        if (entries[idx].occupied == ENTRY_EMPTY) {
            return FALSE_VALUE;
        }
        if (entries[idx].occupied == ENTRY_OCCUPIED &&
            entries[idx].hash == h &&
            values_equal(entries[idx].key, k)) {
            return TRUE_VALUE;
        }
        idx = (idx + 1) & th->mask;
    }
    return FALSE_VALUE;
}

// ---------------------------------------------------------------------------
// y_tab_del(table, key) — Delete key from table, returns tagged bool
// ---------------------------------------------------------------------------

uint64_t y_tab_del(uint64_t t, uint64_t k) {
    if (GET_TAG(t) != TAG_TABLE) return FALSE_VALUE;
    TableHeader *th = (TableHeader *)GET_PTR(t);
    if (!th || !th->entries) return FALSE_VALUE;

    uint64_t h = hash_tagged(k);
    uint64_t idx = h & th->mask;
    TableEntry *entries = (TableEntry *)th->entries;

    for (uint64_t probe = 0; probe < th->capacity; probe++) {
        if (entries[idx].occupied == ENTRY_EMPTY) {
            return FALSE_VALUE; // Key not found
        }
        if (entries[idx].occupied == ENTRY_OCCUPIED &&
            entries[idx].hash == h &&
            values_equal(entries[idx].key, k)) {
            entries[idx].occupied = ENTRY_TOMBSTONE;
            entries[idx].key = 0;
            entries[idx].value = 0;
            entries[idx].hash = 0;
            th->count--;
            th->tombstones++;
            return TRUE_VALUE;
        }
        idx = (idx + 1) & th->mask;
    }
    return FALSE_VALUE;
}

// ---------------------------------------------------------------------------
// y_tab_len(table) — Return number of entries as tagged int
// ---------------------------------------------------------------------------

uint64_t y_tab_len(uint64_t t) {
    if (GET_TAG(t) != TAG_TABLE) return MK_TAG(TAG_INT, 0);
    TableHeader *th = (TableHeader *)GET_PTR(t);
    if (!th) return MK_TAG(TAG_INT, 0);
    return MK_TAG(TAG_INT, th->count);
}

// ---------------------------------------------------------------------------
// y_tab_get_val_type(table, key) — Return tag of value, or -1 if not found
// ---------------------------------------------------------------------------

uint64_t y_tab_get_val_type(uint64_t table, uint64_t key) {
    uint64_t val = y_tab_get(table, key);
    if (val == NIL_VALUE && GET_TAG(val) == 0) {
        // Could be nil value with tag 0, or actually not found
        // Check if key exists
        if (GET_TAG(table) != TAG_TABLE) return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));
        TableHeader *t = (TableHeader *)GET_PTR(table);
        if (!t || !t->entries) return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));

        uint64_t h = hash_tagged(key);
        uint64_t idx = h & t->mask;
        TableEntry *entries = (TableEntry *)t->entries;

        for (uint64_t probe = 0; probe < t->capacity; probe++) {
            if (entries[idx].occupied == ENTRY_EMPTY) {
                return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));
            }
            if (entries[idx].occupied == ENTRY_OCCUPIED &&
                entries[idx].hash == h &&
                values_equal(entries[idx].key, key)) {
                return MK_TAG(TAG_INT, (uint64_t)GET_TAG(entries[idx].value));
            }
            idx = (idx + 1) & t->mask;
        }
        return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));
    }
    return MK_TAG(TAG_INT, (uint64_t)GET_TAG(val));
}

// ---------------------------------------------------------------------------
// y_tab_iter_valid(table, iter) — Check if iter points to valid entry
// ---------------------------------------------------------------------------

uint64_t y_tab_iter_valid(uint64_t t, uint64_t i) {
    if (GET_TAG(t) != TAG_TABLE) return FALSE_VALUE;
    TableHeader *th = (TableHeader *)GET_PTR(t);
    if (!th || !th->entries) return FALSE_VALUE;
    int64_t idx = GET_INT(i);
    if (idx < 0 || (uint64_t)idx >= th->capacity) return FALSE_VALUE;
    TableEntry *entries = (TableEntry *)th->entries;
    return entries[idx].occupied == ENTRY_OCCUPIED ? TRUE_VALUE : FALSE_VALUE;
}

// ---------------------------------------------------------------------------
// y_tab_iter_key(table, iter) — Return key at iter position
// ---------------------------------------------------------------------------

uint64_t y_tab_iter_key(uint64_t t, uint64_t i) {
    if (GET_TAG(t) != TAG_TABLE) return NIL_VALUE;
    TableHeader *th = (TableHeader *)GET_PTR(t);
    if (!th || !th->entries) return NIL_VALUE;
    int64_t idx = GET_INT(i);
    if (idx < 0 || (uint64_t)idx >= th->capacity) return NIL_VALUE;
    TableEntry *entries = (TableEntry *)th->entries;
    if (entries[idx].occupied != ENTRY_OCCUPIED) return NIL_VALUE;
    return entries[idx].key;
}

// ---------------------------------------------------------------------------
// y_tab_iter_val(table, iter) — Return value at iter position
// ---------------------------------------------------------------------------

uint64_t y_tab_iter_val(uint64_t t, uint64_t i) {
    if (GET_TAG(t) != TAG_TABLE) return NIL_VALUE;
    TableHeader *th = (TableHeader *)GET_PTR(t);
    if (!th || !th->entries) return NIL_VALUE;
    int64_t idx = GET_INT(i);
    if (idx < 0 || (uint64_t)idx >= th->capacity) return NIL_VALUE;
    TableEntry *entries = (TableEntry *)th->entries;
    if (entries[idx].occupied != ENTRY_OCCUPIED) return NIL_VALUE;
    return entries[idx].value;
}

// ---------------------------------------------------------------------------
// y_tab_iter_next(table, iter) — Advance iterator, return next index or -1
//
// Returns a raw int64_t index (not tagged).  The compiler stores this directly
// into a StackAlloc slot and passes it to y_tab_iter_valid / y_tab_iter_key /
// y_tab_iter_val, all of which use GET_INT() internally.
//
// Returns -1 when no more entries exist.
// ---------------------------------------------------------------------------

int64_t y_tab_iter_next(uint64_t t, uint64_t i) {
    if (GET_TAG(t) != TAG_TABLE) return -1;
    TableHeader *th = (TableHeader *)GET_PTR(t);
    if (!th || !th->entries) return -1;
    int64_t idx = GET_INT(i);
    if (idx < 0) idx = -1;
    idx++;
    while ((uint64_t)idx < th->capacity) {
        TableEntry *entries = (TableEntry *)th->entries;
        if (entries[idx].occupied == ENTRY_OCCUPIED) {
            return idx;
        }
        idx++;
    }
    return -1;
}

// ---------------------------------------------------------------------------
// High-level table iterator API
//
// These functions implement the iterator protocol expected by the compiler
// backends (rv64, rv32, wasm, and future x86_64/aarch64).
//
// Protocol:
//   1. iter = runtime_iter_new(table)   — stores table, returns initial index -1
//   2. loop:
//        has_more = runtime_iter_next(iter)
//        if !has_more → break
//        key   = runtime_iter_get_key()
//        value = runtime_iter_get_val()
//        ... body ...
//        iter = updated by caller (runtime_iter_next advances internally)
//
// On RISC-V, runtime_iter_next also advances the iterator and the new index
// is stored in g_iter_next_idx for the caller to retrieve.
// ---------------------------------------------------------------------------

static uint64_t g_iter_table = 0;
static uint64_t g_iter_result_key = 0;
static uint64_t g_iter_result_val = 0;
static int64_t  g_iter_next_idx = -1;

uint64_t runtime_iter_new(uint64_t table) {
    g_iter_table = table;
    g_iter_next_idx = -1;
    g_iter_result_key = NIL_VALUE;
    g_iter_result_val = NIL_VALUE;
    return (uint64_t)(int64_t)(-1); // initial iterator index
}

uint64_t runtime_iter_next(uint64_t iter_idx) {
    int64_t next = y_tab_iter_next(g_iter_table, iter_idx);
    if (next < 0) {
        g_iter_result_key = NIL_VALUE;
        g_iter_result_val = NIL_VALUE;
        g_iter_next_idx = -1;
        return 0; // done
    }
    g_iter_result_key = y_tab_iter_key(g_iter_table, MK_TAG(TAG_INT, (uint64_t)next));
    g_iter_result_val = y_tab_iter_val(g_iter_table, MK_TAG(TAG_INT, (uint64_t)next));
    g_iter_next_idx = next;
    return 1; // has more
}

uint64_t runtime_iter_get_key(void) {
    return g_iter_result_key;
}

uint64_t runtime_iter_get_val(void) {
    return g_iter_result_val;
}

uint64_t runtime_iter_get_next(void) {
    return (uint64_t)g_iter_next_idx;
}

// ---------------------------------------------------------------------------
// Stub functions for arena/alloc
// ---------------------------------------------------------------------------

uint64_t y_arena_push(uint64_t parent) {
    (void)parent;
    return 0;
}

void y_arena_pop(uint64_t arena) {
    (void)arena;
}

uint64_t y_alloc(uint64_t arena, uint64_t size, uint64_t align) {
    (void)arena;
    (void)align;
    // Minimal bump allocator using mmap — not arena-based, just for now
    if (size == 0) size = 8;
    size = (size + 7) & ~(uint64_t)7;
    void *p = raw_mmap(size);
    if ((long)p < 0 && (long)p > -4096) return 0;
    return (uint64_t)p;
}

void y_free(uint64_t arena, uint64_t ptr) {
    (void)arena;
    (void)ptr;
}

// ---------------------------------------------------------------------------
// Float operations
// ---------------------------------------------------------------------------

uint64_t y_fp_new(uint64_t bits) {
    // Allocate 8 bytes for the boxed double, store the IEEE 754 bits,
    // and return a tagged float value (tag=3, payload=pointer).
    double *p = (double *)raw_mmap(8);
    if ((long)p < 0 && (long)p > -4096) return 0;
    // bits is already the IEEE 754 representation as passed from the compiler
    union { uint64_t u; double d; } conv;
    conv.u = bits;
    p[0] = conv.d;
    return MK_TAG(TAG_FP, (uint64_t)p);
}

// Float arithmetic operations
// These extract the double pointer from the tagged value, perform the operation,
// box the result, and return a new tagged float.

static double y_fp_get(uint64_t val) {
    if (GET_TAG(val) != TAG_FP) return 0.0;
    double *p = (double *)GET_PTR(val);
    return p ? p[0] : 0.0;
}

static uint64_t y_fp_box(double d) {
    double *p = (double *)raw_mmap(8);
    if ((long)p < 0 && (long)p > -4096) return 0;
    p[0] = d;
    return MK_TAG(TAG_FP, (uint64_t)p);
}

uint64_t y_fp_add(uint64_t a, uint64_t b) { return y_fp_box(y_fp_get(a) + y_fp_get(b)); }
uint64_t y_fp_sub(uint64_t a, uint64_t b) { return y_fp_box(y_fp_get(a) - y_fp_get(b)); }
uint64_t y_fp_mul(uint64_t a, uint64_t b) { return y_fp_box(y_fp_get(a) * y_fp_get(b)); }
uint64_t y_fp_div(uint64_t a, uint64_t b) { return y_fp_box(y_fp_get(a) / y_fp_get(b)); }
uint64_t y_fp_neg(uint64_t a) { return y_fp_box(-y_fp_get(a)); }

// ---------------------------------------------------------------------------
// Value operations
// ---------------------------------------------------------------------------

uint64_t y_copy(uint64_t val) {
    return val;
}

uint64_t y_promote(uint64_t left, uint64_t right) {
    (void)right;
    return left;
}

uint64_t y_val_eq(uint64_t a, uint64_t b) {
    return a == b ? TRUE_VALUE : FALSE_VALUE;
}

// y_enum_eq(a, b) — Structural equality for enum values.
// Enums are tables with "_v" (variant index) and optionally "_p" (payload).
// Pointer identity is wrong for enums; this compares _v and _p fields.
uint64_t y_enum_eq(uint64_t a, uint64_t b) {
    // If both are the same pointer, they're trivially equal.
    if (a == b) return TRUE_VALUE;

    // Both must be tables.
    if (GET_TAG(a) != TAG_TABLE || GET_TAG(b) != TAG_TABLE)
        return FALSE_VALUE;

    // Build "_v" key and compare variant indices.
    uint64_t vk = y_str_new((uint64_t)"_v", (uint64_t)2);
    uint64_t av = y_tab_get(a, vk);
    uint64_t bv = y_tab_get(b, vk);

    // Strip tags from the integer variant indices for comparison.
    // The _v values are tagged integers; compare the raw payloads.
    if (GET_TAG(av) == TAG_INT) av = (uint64_t)(int64_t)av;
    if (GET_TAG(bv) == TAG_INT) bv = (uint64_t)(int64_t)bv;
    // Tag bits should be zero after masking, but compare directly
    // in case both are raw ints from table storage.
    if (av != bv) return FALSE_VALUE;

    // Variant indices match.  Compare _p (payload) if present.
    uint64_t pk = y_str_new((uint64_t)"_p", (uint64_t)2);
    uint64_t ap = y_tab_get(a, pk);
    uint64_t bp = y_tab_get(b, pk);

    // If both are nil (no payload), equal.
    // Otherwise, compare payload values (pointer identity is fine for
    // immediate values; for heap values like strings, this means
    // two equal-but-distinct strings would compare unequal — acceptable
    // for now, consistent with the rest of the equality semantics).
    if (ap == bp) return TRUE_VALUE;
    if (GET_TAG(ap) == TAG_INT && GET_TAG(bp) == TAG_INT) {
        // Compare integer payloads (mask off tags).
        uint64_t a_payload = (uint64_t)(int64_t)ap;
        uint64_t b_payload = (uint64_t)(int64_t)bp;
        return a_payload == b_payload ? TRUE_VALUE : FALSE_VALUE;
    }
    return FALSE_VALUE;
}

uint64_t y_type_of(uint64_t val) {
    int tag = GET_TAG(val);
    switch (tag) {
    case 0:     return (uint64_t)"void";
    case TAG_INT:  return (uint64_t)"int";
    case TAG_BOOL: return (uint64_t)"bool";
    case TAG_FP:   return (uint64_t)"fp";
    case TAG_STR:  return (uint64_t)"str";
    case TAG_TABLE: return (uint64_t)"table";
    case TAG_ERR:   return (uint64_t)"error";
    default:        return (uint64_t)"unknown";
    }
}

// ---------------------------------------------------------------------------
// y_str_concat(a, b) — Concatenate two strings
// ---------------------------------------------------------------------------

uint64_t y_str_concat(uint64_t a, uint64_t b) {
    if (GET_TAG(a) != TAG_STR || GET_TAG(b) != TAG_STR) return NIL_VALUE;
    StrHeader *ha = (StrHeader *)GET_PTR(a);
    StrHeader *hb = (StrHeader *)GET_PTR(b);
    if (!ha || !hb) return NIL_VALUE;

    uint64_t new_len = (uint64_t)ha->len + (uint64_t)hb->len;
    size_t total = sizeof(StrHeader) + (size_t)new_len + 1;
    total = (total + 7) & ~(size_t)7;

    StrHeader *h = (StrHeader *)raw_mmap(total);
    if ((long)h < 0 && (long)h > -4096) {
        raw_write(2, "y_str_concat: mmap failed\n", 27);
        raw_exit(99);
    }

    h->refcount = 0;
    h->len = (int64_t)new_len;
    h->cap = (int64_t)new_len + 1;
    my_memcpy(h->data, ha->data, (size_t)ha->len);
    my_memcpy(h->data + ha->len, hb->data, (size_t)hb->len);
    h->data[new_len] = '\0';

    return MK_TAG(TAG_STR, (uint64_t)h);
}

// ---------------------------------------------------------------------------
// y_to_str(val) — Convert any tagged value to its string representation
// ---------------------------------------------------------------------------

uint64_t y_to_str(uint64_t val) {
    uint8_t tag = GET_TAG(val);

    switch (tag) {
    case TAG_INT: {
        int64_t iv = GET_INT(val);
        char buf[64];
        char *end = buf + sizeof(buf);
        char *p = end;
        int neg = 0;
        uint64_t uval;

        if (iv < 0) { neg = 1; uval = (uint64_t)(-iv); }
        else { uval = (uint64_t)iv; }

        if (uval == 0) { *--p = '0'; }
        else {
            while (uval > 0) { *--p = '0' + (char)(uval % 10); uval /= 10; }
        }
        if (neg) *--p = '-';

        size_t len = (size_t)(end - p);
        size_t total = sizeof(StrHeader) + len + 1;
        total = (total + 7) & ~(size_t)7;
        StrHeader *h = (StrHeader *)raw_mmap(total);
        h->refcount = 0;
        h->len = (int64_t)len;
        h->cap = (int64_t)len + 1;
        my_memcpy(h->data, p, len);
        h->data[len] = '\0';
        return MK_TAG(TAG_STR, (uint64_t)h);
    }
    case TAG_BOOL: {
        const char *s = GET_INT(val) ? "true" : "false";
        size_t len = GET_INT(val) ? 4 : 5;
        size_t total = sizeof(StrHeader) + len + 1;
        total = (total + 7) & ~(size_t)7;
        StrHeader *h = (StrHeader *)raw_mmap(total);
        h->refcount = 0;
        h->len = (int64_t)len;
        h->cap = (int64_t)len + 1;
        my_memcpy(h->data, s, len);
        h->data[len] = '\0';
        return MK_TAG(TAG_STR, (uint64_t)h);
    }
    case TAG_STR:
        return val; // already a string
    case TAG_NIL:
    case 0: {
        const char *s = "nil";
        size_t len = 3;
        size_t total = sizeof(StrHeader) + len + 1;
        total = (total + 7) & ~(size_t)7;
        StrHeader *h = (StrHeader *)raw_mmap(total);
        h->refcount = 0;
        h->len = (int64_t)len;
        h->cap = (int64_t)len + 1;
        my_memcpy(h->data, s, len);
        h->data[len] = '\0';
        return MK_TAG(TAG_STR, (uint64_t)h);
    }
    default:
        return NIL_VALUE;
    }
}

// ---------------------------------------------------------------------------
// Math functions
//
// Functions 1–6 use x86_64 SSE4.1 inline assembly.
// Functions 7–14 use C library <math.h>.
// All functions use y_fp_get() / y_fp_box() for tagged-value boxing.
// ---------------------------------------------------------------------------

uint64_t y_abs(uint64_t val) { return val; }
uint64_t y_neg(uint64_t val) { return MK_TAG(TAG_INT, GET_INT(val) ^ VALUE_MASK) + 1; }
uint64_t y_min(uint64_t a, uint64_t b) { return a < b ? a : b; }
uint64_t y_max(uint64_t a, uint64_t b) { return a > b ? a : b; }

// 1. y_sqrt(x) — square root using sqrtsd
uint64_t y_sqrt(uint64_t val) {
    double v = y_fp_get(val);
    double result;
    __asm__("sqrtsd %1, %0" : "=x"(result) : "x"(v));
    return y_fp_box(result);
}

// 2. y_floor(x) — floor using roundsd mode 01 (round toward -inf)
uint64_t y_floor(uint64_t val) {
    double v = y_fp_get(val);
    double result;
    __asm__("roundsd $1, %1, %0" : "=x"(result) : "x"(v));
    return y_fp_box(result);
}

// 3. y_ceil(x) — ceiling using roundsd mode 10 (round toward +inf)
uint64_t y_ceil(uint64_t val) {
    double v = y_fp_get(val);
    double result;
    __asm__("roundsd $2, %1, %0" : "=x"(result) : "x"(v));
    return y_fp_box(result);
}

// 4. y_round(x) — round to nearest using roundsd mode 00 (round to nearest-even)
uint64_t y_round(uint64_t val) {
    double v = y_fp_get(val);
    double result;
    __asm__("roundsd $0, %1, %0" : "=x"(result) : "x"(v));
    return y_fp_box(result);
}

// 5. y_trunc(x) — truncate toward zero using roundsd mode 11
uint64_t y_trunc(uint64_t val) {
    double v = y_fp_get(val);
    double result;
    __asm__("roundsd $3, %1, %0" : "=x"(result) : "x"(v));
    return y_fp_box(result);
}

// 6. y_fract(x) — fractional part: x - floor(x)
uint64_t y_fract(uint64_t val) {
    double v = y_fp_get(val);
    double fl;
    __asm__("roundsd $1, %1, %0" : "=x"(fl) : "x"(v));
    return y_fp_box(v - fl);
}

// 7. y_pow(base, exp) — power function
uint64_t y_pow(uint64_t base, uint64_t exp) {
    return y_fp_box(pow(y_fp_get(base), y_fp_get(exp)));
}

// 8. y_log(x) — natural logarithm
uint64_t y_log(uint64_t val) {
    return y_fp_box(log(y_fp_get(val)));
}

// 9. y_log2(x) — base-2 logarithm
uint64_t y_log2(uint64_t val) {
    return y_fp_box(log2(y_fp_get(val)));
}

// 10. y_log10(x) — base-10 logarithm
uint64_t y_log10(uint64_t val) {
    return y_fp_box(log10(y_fp_get(val)));
}

// 11. y_exp(x) — e^x
uint64_t y_exp(uint64_t val) {
    return y_fp_box(exp(y_fp_get(val)));
}

// 12. y_sin(x) — sine
uint64_t y_sin(uint64_t val) {
    return y_fp_box(sin(y_fp_get(val)));
}

// 13. y_cos(x) — cosine
uint64_t y_cos(uint64_t val) {
    return y_fp_box(cos(y_fp_get(val)));
}

// 14. y_tan(x) — tangent
uint64_t y_tan(uint64_t val) {
    return y_fp_box(tan(y_fp_get(val)));
}

uint64_t y_sign(uint64_t val) {
    int64_t iv = GET_INT(val);
    if (iv > 0) return MK_TAG(TAG_INT, 1);
    if (iv < 0) return MK_TAG(TAG_INT, 0xFFFFFFFFFFFFFF);  // -1 in 56-bit signed
    return MK_TAG(TAG_INT, 0);
}
uint64_t y_clamp(uint64_t val, uint64_t lo, uint64_t hi) {
    if (val < lo) return lo;
    if (val > hi) return hi;
    return val;
}

// ---------------------------------------------------------------------------
// Core functions
// ---------------------------------------------------------------------------

// y_input(prompt) — Print prompt, read a line from stdin, return tagged string
uint64_t y_input(uint64_t prompt) {
    // Print the prompt
    if (GET_TAG(prompt) == TAG_STR) {
        StrHeader *h = (StrHeader *)GET_PTR(prompt);
        if (h) raw_write(1, h->data, (size_t)h->len);
    }

    // Read into buffer
    char buf[4096];
    long n = raw_read(0, buf, sizeof(buf) - 1);
    if (n <= 0) {
        // EOF or error — return empty string
        return y_str_new("", 0);
    }

    // Strip trailing newline/carriage return
    long len = n;
    while (len > 0 && (buf[len - 1] == '\n' || buf[len - 1] == '\r')) {
        len--;
    }

    return y_str_new(buf, (uint64_t)len);
}

// y_len(val) — Return length of string as tagged int
uint64_t y_len(uint64_t val) {
    int tag = GET_TAG(val);
    if (tag == TAG_STR) {
        StrHeader *h = (StrHeader *)GET_PTR(val);
        if (h) return MK_TAG(TAG_INT, (uint64_t)h->len);
        return MK_TAG(TAG_INT, 0);
    }
    return MK_TAG(TAG_INT, 0);
}

// y_panic(msg) — Print message and abort
void y_panic(uint64_t msg) {
    raw_write(2, "panic: ", 7);
    if (GET_TAG(msg) == TAG_STR) {
        StrHeader *h = (StrHeader *)GET_PTR(msg);
        if (h) raw_write(2, h->data, (size_t)h->len);
    }
    raw_write(2, "\n", 1);
    raw_exit(1);
}

// y_assert(cond, msg) — Assert condition, panic if false
void y_assert(uint64_t cond, uint64_t msg) {
    int tag = GET_TAG(cond);
    uint64_t payload = cond & VALUE_MASK;
    int is_true = (tag == TAG_BOOL && payload != 0) || (tag == TAG_INT && payload != 0);
    if (!is_true) {
        y_panic(msg);
    }
}

uint64_t y_error(uint64_t msg) {
    return MK_TAG(TAG_ERR, msg & VALUE_MASK);
}

uint64_t y_is_error(uint64_t val) {
    return GET_TAG(val) == TAG_ERR ? TRUE_VALUE : FALSE_VALUE;
}

// ===========================================================================
// String method implementations
// ===========================================================================

// ---------------------------------------------------------------------------
// y_trim(s) — Strip leading/trailing whitespace from string
// ---------------------------------------------------------------------------

uint64_t y_trim(uint64_t s) {
    if (GET_TAG(s) != TAG_STR) return s;
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return s;
    if (h->len == 0) return s;

    int64_t start = 0;
    while (start < h->len && my_isspace((unsigned char)h->data[start])) {
        start++;
    }
    int64_t end = h->len;
    while (end > start && my_isspace((unsigned char)h->data[end - 1])) {
        end--;
    }
    return y_str_new(h->data + start, (uint64_t)(end - start));
}

// ---------------------------------------------------------------------------
// y_lower(s) — Convert string to lowercase
// ---------------------------------------------------------------------------

uint64_t y_lower(uint64_t s) {
    if (GET_TAG(s) != TAG_STR) return s;
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return s;
    if (h->len == 0) return s;

    size_t total = sizeof(StrHeader) + (size_t)h->len + 1;
    total = (total + 7) & ~(size_t)7;
    StrHeader *out = (StrHeader *)raw_mmap(total);
    if ((long)out < 0 && (long)out > -4096) {
        raw_write(2, "y_lower: mmap failed\n", 21);
        raw_exit(99);
    }
    out->refcount = 0;
    out->len = h->len;
    out->cap = h->len + 1;
    for (int64_t i = 0; i < h->len; i++) {
        out->data[i] = my_tolower((unsigned char)h->data[i]);
    }
    out->data[h->len] = '\0';
    return MK_TAG(TAG_STR, (uint64_t)out);
}

// ---------------------------------------------------------------------------
// y_upper(s) — Convert string to uppercase
// ---------------------------------------------------------------------------

uint64_t y_upper(uint64_t s) {
    if (GET_TAG(s) != TAG_STR) return s;
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return s;
    if (h->len == 0) return s;

    size_t total = sizeof(StrHeader) + (size_t)h->len + 1;
    total = (total + 7) & ~(size_t)7;
    StrHeader *out = (StrHeader *)raw_mmap(total);
    if ((long)out < 0 && (long)out > -4096) {
        raw_write(2, "y_upper: mmap failed\n", 21);
        raw_exit(99);
    }
    out->refcount = 0;
    out->len = h->len;
    out->cap = h->len + 1;
    for (int64_t i = 0; i < h->len; i++) {
        out->data[i] = my_toupper((unsigned char)h->data[i]);
    }
    out->data[h->len] = '\0';
    return MK_TAG(TAG_STR, (uint64_t)out);
}

// ---------------------------------------------------------------------------
// y_substr(s, start, end) — Extract substring from start..end
//
// Both start and end are tagged ints. Returns new string.
// Handles negative indices from the end.
// ---------------------------------------------------------------------------

uint64_t y_substr(uint64_t s, uint64_t start_val, uint64_t end_val) {
    if (GET_TAG(s) != TAG_STR) return NIL_VALUE;
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return NIL_VALUE;

    int64_t slen = h->len;
    int64_t start = GET_INT(start_val);
    int64_t end = GET_INT(end_val);

    // Handle negative indices
    if (start < 0) start = slen + start;
    if (end < 0) end = slen + end;

    // Clamp to valid range
    if (start < 0) start = 0;
    if (end > slen) end = slen;
    if (start >= end) return y_str_new("", 0);

    return y_str_new(h->data + start, (uint64_t)(end - start));
}

// ---------------------------------------------------------------------------
// y_contains(s, sub) — Check if s contains sub, return tagged bool
// ---------------------------------------------------------------------------

uint64_t y_contains(uint64_t s, uint64_t sub) {
    if (GET_TAG(s) != TAG_STR || GET_TAG(sub) != TAG_STR) return FALSE_VALUE;
    StrHeader *hs = (StrHeader *)GET_PTR(s);
    StrHeader *hsub = (StrHeader *)GET_PTR(sub);
    if (!hs || !hsub) return FALSE_VALUE;
    if (hsub->len == 0) return TRUE_VALUE; // empty string is contained in everything
    if (hs->len < hsub->len) return FALSE_VALUE;

    int64_t limit = hs->len - hsub->len;
    for (int64_t i = 0; i <= limit; i++) {
        if (my_memcmp(hs->data + i, hsub->data, (size_t)hsub->len) == 0) {
            return TRUE_VALUE;
        }
    }
    return FALSE_VALUE;
}

// ---------------------------------------------------------------------------
// y_starts_with(s, prefix) — Check if s starts with prefix, return tagged bool
// ---------------------------------------------------------------------------

uint64_t y_starts_with(uint64_t s, uint64_t prefix) {
    if (GET_TAG(s) != TAG_STR || GET_TAG(prefix) != TAG_STR) return FALSE_VALUE;
    StrHeader *hs = (StrHeader *)GET_PTR(s);
    StrHeader *hp = (StrHeader *)GET_PTR(prefix);
    if (!hs || !hp) return FALSE_VALUE;
    if (hp->len > hs->len) return FALSE_VALUE;
    if (hp->len == 0) return TRUE_VALUE;
    return my_memcmp(hs->data, hp->data, (size_t)hp->len) == 0 ? TRUE_VALUE : FALSE_VALUE;
}

// ---------------------------------------------------------------------------
// y_ends_with(s, suffix) — Check if s ends with suffix, return tagged bool
// ---------------------------------------------------------------------------

uint64_t y_ends_with(uint64_t s, uint64_t suffix) {
    if (GET_TAG(s) != TAG_STR || GET_TAG(suffix) != TAG_STR) return FALSE_VALUE;
    StrHeader *hs = (StrHeader *)GET_PTR(s);
    StrHeader *hsuf = (StrHeader *)GET_PTR(suffix);
    if (!hs || !hsuf) return FALSE_VALUE;
    if (hsuf->len > hs->len) return FALSE_VALUE;
    if (hsuf->len == 0) return TRUE_VALUE;
    int64_t offset = hs->len - hsuf->len;
    return my_memcmp(hs->data + offset, hsuf->data, (size_t)hsuf->len) == 0 ? TRUE_VALUE : FALSE_VALUE;
}

// ---------------------------------------------------------------------------
// y_find(s, sub) — Find index of sub in s, return tagged int (-1 if not found)
// ---------------------------------------------------------------------------

uint64_t y_find(uint64_t s, uint64_t sub) {
    if (GET_TAG(s) != TAG_STR || GET_TAG(sub) != TAG_STR) {
        return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));
    }
    StrHeader *hs = (StrHeader *)GET_PTR(s);
    StrHeader *hsub = (StrHeader *)GET_PTR(sub);
    if (!hs || !hsub) return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));
    if (hsub->len == 0) return MK_TAG(TAG_INT, 0); // empty string found at index 0
    if (hs->len < hsub->len) return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));

    int64_t limit = hs->len - hsub->len;
    for (int64_t i = 0; i <= limit; i++) {
        if (my_memcmp(hs->data + i, hsub->data, (size_t)hsub->len) == 0) {
            return MK_TAG(TAG_INT, (uint64_t)i);
        }
    }
    return MK_TAG(TAG_INT, (uint64_t)(int64_t)(-1));
}

// ---------------------------------------------------------------------------
// y_str_split(s, sep) — Split string by separator, return table of strings
//
// The returned table uses integer keys (0, 1, 2, ...) with string values.
// ---------------------------------------------------------------------------

uint64_t y_str_split(uint64_t s, uint64_t sep) {
    if (GET_TAG(s) != TAG_STR || GET_TAG(sep) != TAG_STR) return NIL_VALUE;
    StrHeader *hs = (StrHeader *)GET_PTR(s);
    StrHeader *hsep = (StrHeader *)GET_PTR(sep);
    if (!hs) return NIL_VALUE;

    uint64_t table = y_table_new(16);
    if (!hsep || hsep->len == 0) {
        // Empty separator: return table with original string
        y_tab_set(table, MK_TAG(TAG_INT, 0), s);
        return table;
    }

    int64_t sep_len = hsep->len;
    int64_t idx = 0;
    int64_t part_start = 0;
    int64_t slen = hs->len;

    while (part_start <= slen) {
        // Find next occurrence of separator
        int64_t found = -1;
        for (int64_t i = part_start; i <= slen - sep_len; i++) {
            if (my_memcmp(hs->data + i, hsep->data, (size_t)sep_len) == 0) {
                found = i;
                break;
            }
        }

        if (found == -1) {
            // No more separators — emit remaining as last part
            uint64_t part = y_str_new(hs->data + part_start, (uint64_t)(slen - part_start));
            y_tab_set(table, MK_TAG(TAG_INT, (uint64_t)idx), part);
            break;
        }

        // Emit part from part_start to found
        uint64_t part = y_str_new(hs->data + part_start, (uint64_t)(found - part_start));
        y_tab_set(table, MK_TAG(TAG_INT, (uint64_t)idx), part);
        idx++;
        part_start = found + sep_len;
    }

    return table;
}

// ---------------------------------------------------------------------------
// y_str_replace(s, old, new_) — Replace all occurrences of old with new_
// ---------------------------------------------------------------------------

uint64_t y_str_replace(uint64_t s, uint64_t old, uint64_t new_) {
    if (GET_TAG(s) != TAG_STR) return s;
    if (GET_TAG(old) != TAG_STR || GET_TAG(new_) != TAG_STR) return s;

    StrHeader *hs = (StrHeader *)GET_PTR(s);
    StrHeader *hold = (StrHeader *)GET_PTR(old);
    StrHeader *hnew = (StrHeader *)GET_PTR(new_);
    if (!hs) return s;
    if (!hold || hold->len == 0) return s; // empty pattern: return original
    if (!hnew) {
        // Treat new_ as empty string
        hnew = (StrHeader *)GET_PTR(y_str_new("", 0));
    }

    int64_t slen = hs->len;
    int64_t old_len = hold->len;
    int64_t new_len = hnew->len;

    // First pass: count occurrences and compute result length
    int64_t count = 0;
    for (int64_t i = 0; i <= slen - old_len; ) {
        if (my_memcmp(hs->data + i, hold->data, (size_t)old_len) == 0) {
            count++;
            i += old_len;
        } else {
            i++;
        }
    }

    if (count == 0) return s; // No replacements needed

    int64_t result_len = slen + count * (new_len - old_len);
    if (result_len < 0) result_len = 0;

    // Allocate result
    size_t total = sizeof(StrHeader) + (size_t)result_len + 1;
    total = (total + 7) & ~(size_t)7;
    StrHeader *out = (StrHeader *)raw_mmap(total);
    if ((long)out < 0 && (long)out > -4096) {
        raw_write(2, "y_str_replace: mmap failed\n", 27);
        raw_exit(99);
    }
    out->refcount = 0;
    out->len = result_len;
    out->cap = result_len + 1;

    // Build result string
    int64_t dst = 0;
    int64_t pos = 0;
    while (pos <= slen - old_len) {
        if (my_memcmp(hs->data + pos, hold->data, (size_t)old_len) == 0) {
            my_memcpy(out->data + dst, hnew->data, (size_t)new_len);
            dst += new_len;
            pos += old_len;
        } else {
            out->data[dst++] = hs->data[pos++];
        }
    }
    // Copy tail (remaining characters that are fewer than old_len)
    while (pos < slen) {
        out->data[dst++] = hs->data[pos++];
    }
    out->data[dst] = '\0';

    return MK_TAG(TAG_STR, (uint64_t)out);
}

// ---------------------------------------------------------------------------
// y_str_bytes(s) — Convert string to table of int byte values
// ---------------------------------------------------------------------------

uint64_t y_str_bytes(uint64_t s) {
    if (GET_TAG(s) != TAG_STR) return NIL_VALUE;
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return NIL_VALUE;

    uint64_t table = y_table_new(16);
    for (int64_t i = 0; i < h->len; i++) {
        unsigned char byte = (unsigned char)h->data[i];
        y_tab_set(table, MK_TAG(TAG_INT, (uint64_t)i), MK_TAG(TAG_INT, (uint64_t)byte));
    }
    return table;
}

// ---------------------------------------------------------------------------
// y_str_to_int(s) — Parse string to integer, return tagged int
// ---------------------------------------------------------------------------

uint64_t y_str_to_int(uint64_t s) {
    if (GET_TAG(s) != TAG_STR) return MK_TAG(TAG_INT, 0);
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return MK_TAG(TAG_INT, 0);

    int64_t result = str_to_int64(h->data, h->len);
    return MK_TAG(TAG_INT, (uint64_t)result);
}

// ---------------------------------------------------------------------------
// y_str_to_float(s) — Parse string to float, return tagged float
// ---------------------------------------------------------------------------

uint64_t y_str_to_float(uint64_t s) {
    if (GET_TAG(s) != TAG_STR) return NIL_VALUE;
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return NIL_VALUE;

    double result = str_to_double(h->data, h->len);
    return y_fp_box(result);
}

// ---------------------------------------------------------------------------
// y_str_repeat(s, n) — Repeat string N times
// ---------------------------------------------------------------------------

uint64_t y_str_repeat(uint64_t s, uint64_t n) {
    if (GET_TAG(s) != TAG_STR) return NIL_VALUE;
    StrHeader *h = (StrHeader *)GET_PTR(s);
    if (!h) return NIL_VALUE;

    int64_t count = GET_INT(n);
    if (count <= 0) return y_str_new("", 0);
    if (h->len == 0) return y_str_new("", 0);

    // Check for overflow
    int64_t new_len = h->len * count;
    if (new_len < 0 || (uint64_t)new_len / (uint64_t)h->len != (uint64_t)count) {
        raw_write(2, "panic: str_repeat overflow\n", 27);
        raw_exit(1);
    }

    size_t total = sizeof(StrHeader) + (size_t)new_len + 1;
    total = (total + 7) & ~(size_t)7;
    StrHeader *out = (StrHeader *)raw_mmap(total);
    if ((long)out < 0 && (long)out > -4096) {
        raw_write(2, "y_str_repeat: mmap failed\n", 26);
        raw_exit(99);
    }
    out->refcount = 0;
    out->len = new_len;
    out->cap = new_len + 1;

    int64_t pos = 0;
    for (int64_t i = 0; i < count; i++) {
        my_memcpy(out->data + pos, h->data, (size_t)h->len);
        pos += h->len;
    }
    out->data[new_len] = '\0';

    return MK_TAG(TAG_STR, (uint64_t)out);
}

// ===========================================================================
// System stubs
// ===========================================================================

uint64_t y_sys_args(void) { return 0; }
uint64_t y_sys_argc(void) { return MK_TAG(TAG_INT, 1); }
uint64_t y_sys_cwd(void) { return MK_TAG(TAG_STR, 0); }
uint64_t y_sys_platform(void) { return (uint64_t)"linux"; }
uint64_t y_sys_env(void) { return 0; }
uint64_t y_sys_getenv(uint64_t name) { (void)name; return 0; }
uint64_t y_sys_clock(void) { return 0; }
void y_sys_sleep(uint64_t secs) { (void)secs; }

// ===========================================================================
// Filesystem stubs
// ===========================================================================

uint64_t y_fs_exists(uint64_t path) { (void)path; return FALSE_VALUE; }
uint64_t y_fs_read_text(uint64_t path) { (void)path; return 0; }
void y_fs_write_text(uint64_t path, uint64_t content) { (void)path; (void)content; }
void y_fs_append_text(uint64_t path, uint64_t content) { (void)path; (void)content; }
uint64_t y_fs_read_lines(uint64_t path) { (void)path; return 0; }
uint64_t y_fs_remove(uint64_t path) { (void)path; return FALSE_VALUE; }
uint64_t y_fs_rename(uint64_t old, uint64_t new_) { (void)old; (void)new_; return FALSE_VALUE; }
uint64_t y_fs_copy(uint64_t src, uint64_t dst) { (void)src; (void)dst; return FALSE_VALUE; }
uint64_t y_fs_mkdir(uint64_t path) { (void)path; return FALSE_VALUE; }
uint64_t y_fs_rmdir(uint64_t path) { (void)path; return FALSE_VALUE; }
uint64_t y_fs_read_dir(uint64_t path) { (void)path; return 0; }
uint64_t y_fs_is_file(uint64_t path) { (void)path; return FALSE_VALUE; }
uint64_t y_fs_is_dir(uint64_t path) { (void)path; return FALSE_VALUE; }
uint64_t y_fs_file_size(uint64_t path) { (void)path; return MK_TAG(TAG_INT, (uint64_t)(-1)); }

// ===========================================================================
// Path stubs
// ===========================================================================

uint64_t y_path_normalize(uint64_t p) { return p; }
uint64_t y_path_resolve(uint64_t p) { return p; }
uint64_t y_path_resolve2(uint64_t base, uint64_t p) { (void)base; return p; }
uint64_t y_path_relative(uint64_t base, uint64_t target) { (void)base; (void)target; return 0; }
uint64_t y_path_join(uint64_t a, uint64_t b) { (void)b; return a; }
uint64_t y_path_dirname(uint64_t p) { return p; }
uint64_t y_path_parent(uint64_t p) { return p; }
uint64_t y_path_basename(uint64_t p) { return p; }
uint64_t y_path_stem(uint64_t p) { return p; }
uint64_t y_path_extname(uint64_t p) { (void)p; return 0; }
uint64_t y_path_is_abs(uint64_t p) { (void)p; return FALSE_VALUE; }
uint64_t y_path_sep(void) { return (uint64_t)"/"; }
uint64_t y_path_sep_posix(void) { return (uint64_t)"/"; }
uint64_t y_path_sep_win(void) { return (uint64_t)"\\"; }

// ===========================================================================
// JSON stubs
// ===========================================================================

uint64_t y_json_encode(uint64_t val) { (void)val; return 0; }
uint64_t y_json_decode(uint64_t s) { (void)s; return 0; }
