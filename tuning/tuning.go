// Package tuning holds the tuning parameters of the Fibula implementation.
//
// In contrast to package format, nothing here is part of the format. Object
// identity depends on content, not on where a stream was cut (E3), so a
// deviation in these values costs dedup rate rather than correctness. They are
// centralized anyway, so that a change is a single deliberate edit instead of
// a magic number drifting apart across packages.
//
// Spec: object model E3, E6; chunking parameters E36 to E40; CLAUDE.md pinned
// point 2 and section 8.
package tuning

// Binary size units, unexported so that the catalogue stays the only place
// that spells out a byte count.
const (
	kiB = 1 << 10
	miB = 1 << 20
)

// Chunk size bounds for asset content (CLAUDE.md pinned point 2: target size
// 1 to 4 MB).
//
// ChunkAvgSize is the size the parameters are chosen to produce; it is not
// itself fed to the chunker. The value the chunker uses is ChunkMaskBits, and
// per E38 the expected size is min + 2^maskBits rather than 2^maskBits — the
// minimum suppresses every boundary test below it. 1 MiB + 2^20 = 2 MiB.
//
// The maximum truncates the tail of the distribution, so that a long run of
// identical bytes — common in uncompressed textures and audio — cannot produce
// one gigantic chunk.
const (
	ChunkMinSize  = 1 * miB
	ChunkAvgSize  = 2 * miB
	ChunkMaxSize  = 4 * miB
	ChunkMaskBits = 20
)

// Chunk size bounds for manifests (E6). The manifest is chunked by the same
// machinery but aims at roughly 64 KB, because it is sorted and line based: a
// single changed line then hits exactly one chunk, which turns a 7 MB transfer
// on a large project into a 64 KB one. 32 KiB + 2^15 = 64 KiB (E38).
const (
	ManifestChunkMinSize  = 32 * kiB
	ManifestChunkAvgSize  = 64 * kiB
	ManifestChunkMaxSize  = 128 * kiB
	ManifestChunkMaskBits = 15
)

// Buzhash parameters (E37, E39).
const (
	// BuzhashWindow is the number of bytes a boundary decision depends on.
	// It equals the hash width in bits divided by 8, which puts the bytes of
	// the window at 64 distinct rotations and makes the byte leaving the
	// window a plain XOR — it has been rotated exactly 64 times, the identity.
	BuzhashWindow = 64

	// BuzhashTableContext is the BLAKE3 derive_key context the 256-entry
	// buzhash table is generated from (E39). It lives here rather than in
	// package format on purpose: changing it changes chunk boundaries and
	// therefore dedup rate, not object identity.
	BuzhashTableContext = "fibula buzhash table v1"
)
