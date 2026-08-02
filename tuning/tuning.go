// Package tuning holds the tuning parameters of the Fibula implementation.
//
// In contrast to package format, nothing here is part of the format. Object
// identity depends on content, not on where a stream was cut (E3), so a
// deviation in these values costs dedup rate rather than correctness. They are
// centralized anyway, so that a change is a single deliberate edit instead of
// a magic number drifting apart across packages.
//
// Spec: object model E3, E6; CLAUDE.md pinned point 2 and section 8.
package tuning

// Binary size units, unexported so that the catalogue stays the only place
// that spells out a byte count.
const (
	kiB = 1 << 10
	miB = 1 << 20
)

// Chunk size bounds for asset content (CLAUDE.md pinned point 2: target size
// 1 to 4 MB). The average is the target the rolling hash aims at; the minimum
// and maximum are hard bounds the chunker enforces regardless of the hash, so
// that a pathological input cannot produce chunks of a single byte or of
// gigabytes.
const (
	ChunkMinSize = 1 * miB
	ChunkAvgSize = 2 * miB
	ChunkMaxSize = 4 * miB
)

// Chunk size bounds for manifests (E6). The manifest is chunked by the same
// machinery but aims at roughly 64 KB, because it is sorted and line based: a
// single changed line then hits exactly one chunk, which turns a 7 MB transfer
// on a large project into a 64 KB one.
const (
	ManifestChunkMinSize = 32 * kiB
	ManifestChunkAvgSize = 64 * kiB
	ManifestChunkMaxSize = 128 * kiB
)

// The rolling hash parameters - window size and boundary mask, and for Rabin
// the polynomial - are deliberately absent. They are algorithm specific and
// the algorithm itself is still open (refinements/index.md); F-S1-02 picks it
// and records the choice as a refinement addendum. They belong in this file
// once that decision exists, not before.
