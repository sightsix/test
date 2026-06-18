package runtime

// ---------------------------------------------------------------------------
// Arena / Region-Based Memory Allocator
//
// Yilt uses arena (region-based) allocation.  All heap objects — strings,
// tables, boxed floats — are allocated from arenas.  When an arena scope
// ends (y_arena_pop), all memory allocated within that scope is reclaimed
// in O(1) by releasing the arena's chunks.
//
// # Memory Layout
//
// Each arena is a fixed-size struct followed by a linked list of chunks:
//
//  Arena struct (on the heap or stack):
//    +0  parent    *Arena  (8 bytes) — parent arena, nil for root
//    +8  chunkSize  int     (8 bytes) — default chunk size (e.g. 4096)
//    +16 chunks    []Chunk (24 bytes, Go slice header) — all chunks owned
//    +40 cur       *Chunk  (8 bytes) — current chunk for bump allocation
//    +48 remaining int     (8 bytes) — bytes left in current chunk
//
//  Chunk struct:
//    +0  data  []byte (24 bytes, Go slice header) — backing memory
//    +24 used  int     (8 bytes) — bytes consumed in this chunk
//
// In the emitted machine code, arenas are plain C structs:
//
//  typedef struct y_arena {
//      struct y_arena* parent;
//      uint64_t        chunk_size;
//      uint8_t*        chunks;      // dynamically grown array of chunk ptrs
//      uint64_t        chunk_count;
//      uint64_t        chunk_cap;
//      uint8_t*        cur_data;    // current bump pointer
//      uint64_t        remaining;
//  } y_arena;
//
//  typedef struct y_chunk {
//      uint8_t* data;
//      uint64_t used;
//      uint64_t capacity;
//  } y_chunk;
//
// # Calling Convention
//
//  y_arena_push(parent: *Arena) -> *Arena   // returns new child
//  y_arena_pop(arena: *Arena)               // releases arena & children
//  y_alloc(arena: *Arena, size: u64, align: u32) -> *void
//  y_free(arena: *Arena, ptr: *void)        // no-op in arena mode
//
// # Alignment
//
// All allocations are aligned to at least 8 bytes.  The arena bump pointer
// is always kept 8-byte aligned.  Callers requesting higher alignment
// (e.g. 16-byte SIMD data) pass the alignment explicitly.
// ---------------------------------------------------------------------------

// DefaultChunkSize is the initial and default size of each arena chunk.
// Chunks grow by doubling up to MaxChunkSize.
const DefaultChunkSize = 4096

// MaxChunkSize caps chunk growth to prevent excessive virtual memory use.
const MaxChunkSize = 4 * 1024 * 1024 // 4 MiB

// MinAllocation is the smallest allocation the arena will service.
// Requests smaller than this are rounded up.
const MinAllocation = 8

// DefaultAlignment is the alignment guarantee for all arena allocations.
const DefaultAlignment = 8

// MaxAlignment is the maximum supported alignment.
const MaxAlignment = 128

// ---------------------------------------------------------------------------
// Arena struct layout offsets (for machine code generation)
//
// These constants tell the backend the byte offset of each field within
// the native arena struct.  Backends use these when emitting load/store
// instructions.
//
// Native layout (64-bit):
//   +0   parent     *y_arena
//   +8   chunk_size  uint64
//   +16  chunks      *y_chunk  (pointer to dynamically allocated array)
//   +24  chunk_count uint64
//   +32  chunk_cap   uint64
//   +40  cur_data    *uint8    (current bump pointer within chunk)
//   +48  remaining   uint64
// TOTAL = 56 bytes
// ---------------------------------------------------------------------------

const (
    // ArenaSize is the total size of the native arena struct in bytes.
    ArenaSize = 56

    // ArenaOffParent is the offset of the parent pointer field.
    ArenaOffParent = 0

    // ArenaOffChunkSize is the offset of the chunk_size field.
    ArenaOffChunkSize = 8

    // ArenaOffChunks is the offset of the chunks array pointer.
    ArenaOffChunks = 16

    // ArenaOffChunkCount is the offset of the chunk_count field.
    ArenaOffChunkCount = 24

    // ArenaOffChunkCap is the offset of the chunk_cap field.
    ArenaOffChunkCap = 32

    // ArenaOffCurData is the offset of the current bump pointer.
    ArenaOffCurData = 40

    // ArenaOffRemaining is the offset of the remaining-bytes field.
    ArenaOffRemaining = 48
)

// ---------------------------------------------------------------------------
// Chunk struct layout offsets
//
// Native layout (64-bit):
//   +0   data     *uint8
//   +8   used     uint64
//   +16  capacity uint64
// TOTAL = 24 bytes
// ---------------------------------------------------------------------------

const (
    // ChunkSize is the total size of the native chunk struct in bytes.
    ChunkStructSize = 24

    // ChunkOffData is the offset of the data pointer field.
    ChunkOffData = 0

    // ChunkOffUsed is the offset of the used-bytes field.
    ChunkOffUsed = 8

    // ChunkOffCapacity is the offset of the capacity field.
    ChunkOffCapacity = 16
)

// ---------------------------------------------------------------------------
// Root arena layout
//
// The Yilt runtime maintains a single global root arena.  Its pointer is
// stored in a global variable accessible to all runtime functions.
//
// In machine code, this is an absolute address in the data segment:
//
//  y_arena* y_root_arena;
// ---------------------------------------------------------------------------

// RootArenaSymbol is the linker-visible symbol name for the global root arena.
const RootArenaSymbol = "y_root_arena"

// ---------------------------------------------------------------------------
// Allocation strategy
//
// y_alloc(arena, size, align):
//  1. Round size up to max(size, MinAllocation), then align.
//  2. If arena->remaining >= aligned_size:
//       ptr = arena->cur_data
//       arena->cur_data += aligned_size
//       arena->remaining -= aligned_size
//       return ptr
//  3. Else (current chunk exhausted):
//       a. Allocate a new chunk with max(next_chunk_size, aligned_size).
//       b. Append chunk to arena->chunks.
//       c. Set arena->cur to new chunk.
//       d. Bump-allocate from the new chunk.
//       e. Return pointer.
//  4. next_chunk_size = min(chunk_size * 2, MaxChunkSize).
//
// Alignment is performed by:
//   padding = (align - (ptr % align)) % align
//   aligned_ptr = ptr + padding
//
// For the common case of 8-byte alignment and 8-byte-aligned chunks,
// no padding is needed.
// ---------------------------------------------------------------------------

// AlignUp rounds n up to the next multiple of align.  align must be a
// power of two.  This is a compile-time helper for backends.
func AlignUp(n, align uint64) uint64 {
    return (n + align - 1) &^ (align - 1)
}

// ---------------------------------------------------------------------------
// Arena push / pop
//
// y_arena_push(parent):
//   1. Allocate a new Arena struct from parent's arena.
//   2. Set child->parent = parent.
//   3. Set child->chunk_size = parent->chunk_size.
//   4. Initialize child->chunks to empty.
//   5. Return child.
//
// y_arena_pop(arena):
//   1. For each chunk in arena->chunks:
//        a. Free chunk->data (mmap-allocated or malloc-free).
//        b. Free chunk struct.
//   2. Free arena->chunks array.
//   3. Free arena struct itself (allocated from parent).
//   4. If arena->parent is not nil, update parent's state.
//
// IMPORTANT: y_arena_pop releases ALL memory in the arena and any child
// arenas that were pushed from this one.  Use a stack-like discipline
// (push before a block, pop after) for correct lifetime management.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Thread-local arena register
//
// On x86-64, the current arena pointer is kept in register R15 for fast
// access.  All runtime allocation functions expect R15 to hold a valid
// arena pointer.
//
// On AArch64, the platform register x28 (or a callee-saved register)
// is used analogously.
//
// On RISC-V, x27 is reserved for the arena base register.
//
// On WASM, the arena pointer is stored in a global variable since WASM
// has no dedicated register file.
// ---------------------------------------------------------------------------

// ArenaRegisterX86_64 is the register used for the arena pointer on x86-64.
const ArenaRegisterX86_64 = "R15"

// ArenaRegisterAArch64 is the register used for the arena pointer on AArch64.
const ArenaRegisterAArch64 = "X28"

// ArenaRegisterRV64 is the register used for the arena pointer on RISC-V 64.
const ArenaRegisterRV64 = "X27"

// ---------------------------------------------------------------------------
// Root arena initialization
//
// At program startup (yilt_run), the runtime:
//   1. Allocates the root arena struct via mmap or malloc.
//   2. Zero-initializes all fields.
//   3. Sets chunk_size = DefaultChunkSize.
//   4. Sets parent = nil.
//   5. Stores the pointer in y_root_arena.
//   6. Sets the arena register to point to the root arena.
//
// The root arena lives for the entire program lifetime.  It is freed
// by the OS at process exit; explicit cleanup is optional.
// ---------------------------------------------------------------------------

// InitSteps describes the sequence of operations the entry point performs
// to set up the runtime.  Backends emit these as the preamble to yilt_run.
var InitSteps = []string{
    "mmap root arena struct (56 bytes)",
    "zero-initialize arena fields",
    "set arena.chunk_size = 4096",
    "set arena.parent = NULL",
    "store arena pointer in y_root_arena",
    "set arena register to root arena pointer",
}
