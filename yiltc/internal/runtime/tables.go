package runtime

// ---------------------------------------------------------------------------
// Hash Table Implementation
//
// Yilt tables (hash maps) use open addressing with linear probing.
// Keys and values are both tagged 64-bit values.
//
// # TableHeader Layout (64 bytes, 8-byte aligned)
//
//  typedef struct y_table {
//      uint64_t    count;      // +0:  number of occupied entries
//      uint64_t    capacity;   // +8:  total slot count (power of 2)
//      uint64_t    threshold;  // +16: count at which to grow (capacity * 3/4)
//      uint64_t    mask;       // +24: capacity - 1 (for fast modular indexing)
//      y_entry*    entries;    // +32: pointer to entry array
//      uint64_t    entry_cap;  // +40: allocated entry capacity (>= capacity)
//      uint64_t    tombstones; // +48: number of deleted entries
//      // reserved: +56 (8 bytes padding for alignment)
//  } y_table;
//
// TOTAL = 64 bytes
//
// # Entry Layout (32 bytes per slot)
//
//  typedef struct y_entry {
//      uint64_t    key;        // +0:  tagged key value
//      uint64_t    value;      // +8:  tagged value
//      uint64_t    hash;       // +16: cached key hash (for faster probing)
//      uint64_t    occupied;   // +24: 0 = empty, 1 = occupied, 2 = tombstone
//  } y_entry;
//
// TOTAL = 32 bytes per entry
//
// # Hash Table Invariants
//
//  1. capacity is always a power of 2.
//  2. mask = capacity - 1 (for index = hash & mask).
//  3. threshold = capacity * 3 / 4 (75% load factor).
//  4. When count + tombstones >= threshold, the table rehashes.
//  5. Tombstones are cleared during rehashing.
//  6. Iteration visits all occupied entries (skips empty and tombstone).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Table header layout offsets
// ---------------------------------------------------------------------------

const (
    // TableHeaderSize is the total size of the native table header.
    TableHeaderSize = 64

    // TableOffCount is the offset of the entry count field.
    TableOffCount = 0

    // TableOffCapacity is the offset of the capacity field.
    TableOffCapacity = 8

    // TableOffThreshold is the offset of the growth threshold.
    TableOffThreshold = 16

    // TableOffMask is the offset of the capacity-1 mask.
    TableOffMask = 24

    // TableOffEntries is the offset of the entries array pointer.
    TableOffEntries = 32

    // TableOffEntryCap is the offset of the allocated entry capacity.
    TableOffEntryCap = 40

    // TableOffTombstones is the offset of the tombstone count.
    TableOffTombstones = 48
)

// ---------------------------------------------------------------------------
// Entry layout offsets
// ---------------------------------------------------------------------------

const (
    // EntrySize is the total size of a single table entry.
    EntrySize = 32

    // EntryOffKey is the offset of the tagged key within an entry.
    EntryOffKey = 0

    // EntryOffValue is the offset of the tagged value within an entry.
    EntryOffValue = 8

    // EntryOffHash is the offset of the cached hash within an entry.
    EntryOffHash = 16

    // EntryOffOccupied is the offset of the occupancy flag within an entry.
    EntryOffOccupied = 24
)

// ---------------------------------------------------------------------------
// Occupancy flags
// ---------------------------------------------------------------------------

const (
    // EntryEmpty means the slot has never been used.
    EntryEmpty = 0

    // EntryOccupied means the slot contains a live key-value pair.
    EntryOccupied = 1

    // EntryTombstone means the slot was deleted (tombstone marker).
    EntryTombstone = 2
)

// ---------------------------------------------------------------------------
// Growth strategy
// ---------------------------------------------------------------------------

// InitialTableCapacity is the default capacity for a newly created table.
const InitialTableCapacity = 16

// MaxLoadFactorNum and MaxLoadFactorDen define the load factor as a fraction.
// When count / capacity >= Num/Den, the table grows.
const (
    MaxLoadFactorNum = 3
    MaxLoadFactorDen = 4
)

// GrowFactor is the multiplier applied to capacity when rehashing.
// Capacity doubles on each growth.
const GrowFactor = 2

// MaxTableCapacity caps the maximum table size to prevent pathological
// memory usage.  At this size, the table stops growing and linear probing
// may degrade, but the program will not OOM from a single table.
const MaxTableCapacity = 1 << 28 // ~268 million entries

// ---------------------------------------------------------------------------
// Table operations (runtime function specifications)
//
// # y_table_new(arena: *Arena) -> tagged_table
//
// Creates a new empty table with InitialTableCapacity.
// Allocates TableHeader and entry array from arena.
// Returns a tagged table value.
//
// # y_table_new_cap(arena: *Arena, cap: u64) -> tagged_table
//
// Creates a new table with at least `cap` initial slots.
// Rounds up to the next power of 2.
//
// # y_tab_set(table: tagged_table, key: tagged, value: tagged, arena: *Arena) -> void
//
// Inserts or updates key -> value in the table.
// If the key already exists, its value is replaced in place.
// Triggers rehashing if the load factor is exceeded.
//
// # y_tab_get(table: tagged_table, key: tagged) -> tagged
//
// Returns the value associated with key, or NilValue if not found.
//
// # y_tab_has(table: tagged_table, key: tagged) -> tagged_bool
//
// Returns TrueValue if the table contains key, FalseValue otherwise.
//
// # y_tab_get_val_type(table: tagged_table, key: tagged) -> tagged_int
//
// Returns the tag byte of the value associated with key as a tagged int,
// or -1 (as a tagged int) if the key is not present.
//
// # y_tab_del(table: tagged_table, key: tagged) -> tagged_bool
//
// Removes the key from the table.  Returns TrueValue if the key was
// present, FalseValue otherwise.  Marks the slot as a tombstone.
//
// # y_tab_len(table: tagged_table) -> tagged_int
//
// Returns the number of occupied entries as a tagged integer.
//
// ---------------------------------------------------------------------------
// Table iteration
//
// Tables support forward iteration using an iterator state variable.
// The iterator is a plain integer (index into the entries array).
//
// # y_tab_iter_valid(table: tagged_table, iter: i64) -> tagged_bool
//
// Returns true if `iter` points to a valid (occupied) entry.
// Advances iter to the next occupied entry.
//
// # y_tab_iter_key(table: tagged_table, iter: i64) -> tagged
//
// Returns the key of the entry at position `iter`.
//
// # y_tab_iter_val(table: tagged_table, iter: i64) -> tagged
//
// Returns the value of the entry at position `iter`.
//
// # y_tab_iter_next(table: tagged_table, iter: i64) -> i64
//
// Advances the iterator to the next occupied entry and returns the
// new index.  Returns -1 (or capacity) if no more entries exist.
//
// Typical iteration pattern:
//   mut iter = 0
//   while y_tab_iter_valid(t, iter):
//     let k = y_tab_iter_key(t, iter)
//     let v = y_tab_iter_val(t, iter)
//     iter = y_tab_iter_next(t, iter)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Lookup algorithm (linear probing)
//
// y_tab_get(table, key):
//   h = HashTagged(key)
//   idx = h & table->mask
//   for probe = 0; probe < table->capacity; probe++:
//     entry = &table->entries[idx]
//     if entry->occupied == EntryEmpty:
//       return NilValue  // key not found (stop at first empty)
//     if entry->occupied == EntryOccupied AND entry->hash == h AND ValuesEqual(entry->key, key):
//       return entry->value
//     idx = (idx + 1) & table->mask  // linear probe
//   return NilValue  // table full (should not happen with rehashing)
//
// y_tab_set(table, key, value):
//   h = HashTagged(key)
//   idx = h & table->mask
//   first_tombstone = -1
//   for probe = 0; probe < table->capacity; probe++:
//     entry = &table->entries[idx]
//     if entry->occupied == EntryEmpty:
//       if first_tombstone >= 0:
//         // Reuse tombstone slot
//         entry = &table->entries[first_tombstone]
//       else:
//         table->count++
//       goto insert
//     if entry->occupied == EntryTombstone AND first_tombstone < 0:
//       first_tombstone = idx
//     if entry->occupied == EntryOccupied AND entry->hash == h AND ValuesEqual(entry->key, key):
//       entry->value = value  // update existing
//       return
//     idx = (idx + 1) & table->mask
//   // Table full (should not happen)
//   panic("table full")
// insert:
//   entry->key = key
//   entry->value = value
//   entry->hash = h
//   entry->occupied = EntryOccupied
//   if table->count + table->tombstones >= table->threshold:
//     rehash(table)
//
// Rehashing:
//   old_entries = table->entries
//   old_capacity = table->capacity
//   new_capacity = min(old_capacity * GrowFactor, MaxTableCapacity)
//   allocate new entry array of new_capacity entries
//   zero-initialize all entries
//   table->entries = new array
//   table->capacity = new_capacity
//   table->mask = new_capacity - 1
//   table->threshold = new_capacity * MaxLoadFactorNum / MaxLoadFactorDen
//   table->count = 0
//   table->tombstones = 0
//   for i = 0; i < old_capacity; i++:
//     if old_entries[i].occupied == EntryOccupied:
//       insert old_entries[i] into new table (no recursive rehash)
//   free old entry array
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Allocation sizes
//
// These helpers compute the total allocation needed for table structures,
// used by backends to estimate memory requirements.
// ---------------------------------------------------------------------------

// TableAllocSize returns the bytes needed for the TableHeader itself.
func TableAllocSize() uint64 {
    return uint64(TableHeaderSize)
}

// EntryArrayAllocSize returns the bytes needed for an entry array of
// the given capacity.
func EntryArrayAllocSize(capacity uint64) uint64 {
    return EntrySize * capacity
}

// TableTotalAllocSize returns the total bytes for a table with the given
// capacity (header + entries).
func TableTotalAllocSize(capacity uint64) uint64 {
    return uint64(TableHeaderSize) + EntryArrayAllocSize(capacity)
}

// ---------------------------------------------------------------------------
// Iterator helpers
//
// The table iterator is a simple integer index that scans forward through
// the entries array.  The runtime functions advance the iterator past
// empty slots and tombstones.
// ---------------------------------------------------------------------------

// IterStart is the initial value for a table iterator.
const IterStart int64 = 0

// IterEnd is the sentinel value indicating no more entries.
const IterEnd int64 = -1
