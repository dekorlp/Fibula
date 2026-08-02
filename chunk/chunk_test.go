package chunk

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"strconv"
	"testing"

	"github.com/dekorlp/fibula/tuning"
)

// smallParams keep the tests fast while preserving every property that
// matters. The bounds stand in the same relation to each other as the real
// ones (min = expected/2, max = expected*2, E38), only three orders of
// magnitude smaller.
func smallParams() Params {
	return Params{MinSize: 512, MaxSize: 2048, MaskBits: 9}
}

// deterministicBytes produces reproducible pseudo-random content. Real assets
// are not random, but random content is the harder case for a chunker: it has
// no structure to resynchronize on.
func deterministicBytes(seed int64, n int) []byte {
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	return b
}

func split(t *testing.T, data []byte, p Params) [][]byte {
	t.Helper()

	s, err := NewSplitter(bytes.NewReader(data), p)
	if err != nil {
		t.Fatalf("NewSplitter: %v", err)
	}

	var chunks [][]byte
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			return chunks
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		chunks = append(chunks, append([]byte(nil), c...))
	}
}

// TestRoundTripIsByteIdentical is the property without which nothing else
// matters: reassembling the chunks must reproduce the input exactly.
func TestRoundTripIsByteIdentical(t *testing.T) {
	sizes := []int{0, 1, 100, 511, 512, 513, 2047, 2048, 2049, 100_000}

	for _, size := range sizes {
		t.Run(strconv.Itoa(size)+"-bytes", func(t *testing.T) {
			data := deterministicBytes(1, size)

			var rebuilt []byte
			for _, c := range split(t, data, smallParams()) {
				rebuilt = append(rebuilt, c...)
			}

			if !bytes.Equal(rebuilt, data) {
				t.Errorf("reassembled %d bytes, want %d identical", len(rebuilt), len(data))
			}
		})
	}
}

func TestChunkSizesRespectTheBounds(t *testing.T) {
	p := smallParams()
	data := deterministicBytes(2, 200_000)

	chunks := split(t, data, p)
	if len(chunks) < 10 {
		t.Fatalf("got %d chunks, want enough to be meaningful", len(chunks))
	}

	for i, c := range chunks {
		last := i == len(chunks)-1
		switch {
		case len(c) > p.MaxSize:
			t.Errorf("chunk %d has %d bytes, above the maximum %d", i, len(c), p.MaxSize)
		case len(c) < p.MinSize && !last:
			t.Errorf("chunk %d has %d bytes, below the minimum %d", i, len(c), p.MinSize)
		}
	}
}

// TestEmptyStreamProducesNoChunks pins the edge case from E38: an empty file
// has no chunks at all, and its file object is still well defined.
func TestEmptyStreamProducesNoChunks(t *testing.T) {
	if chunks := split(t, nil, smallParams()); len(chunks) != 0 {
		t.Errorf("got %d chunks for an empty stream, want none", len(chunks))
	}
}

// TestInsertionAtTheStartShiftsBoundariesOnlyLocally is the property that
// justifies content-defined chunking at all. With fixed blocking, inserting a
// single byte at the front would shift every boundary in the file and dedup
// would collapse to nothing.
func TestInsertionAtTheStartShiftsBoundariesOnlyLocally(t *testing.T) {
	p := smallParams()
	original := deterministicBytes(3, 200_000)
	edited := append([]byte("x"), original...)

	before := split(t, original, p)
	after := split(t, edited, p)

	shared := sharedChunks(before, after)
	ratio := float64(shared) / float64(len(before))

	if ratio < 0.9 {
		t.Errorf("only %d of %d chunks survived an insertion at the front (%.0f%%), want most of them",
			shared, len(before), ratio*100)
	}
}

// TestDeletionInTheMiddleShiftsBoundariesOnlyLocally is the same property from
// the other direction.
func TestDeletionInTheMiddleShiftsBoundariesOnlyLocally(t *testing.T) {
	p := smallParams()
	original := deterministicBytes(4, 200_000)

	edited := make([]byte, 0, len(original)-1000)
	edited = append(edited, original[:100_000]...)
	edited = append(edited, original[101_000:]...)

	before := split(t, original, p)
	after := split(t, edited, p)

	shared := sharedChunks(before, after)
	if ratio := float64(shared) / float64(len(before)); ratio < 0.85 {
		t.Errorf("only %d of %d chunks survived a deletion in the middle (%.0f%%)",
			shared, len(before), ratio*100)
	}
}

// TestLongRunOfIdenticalBytesDedupesPerfectly covers the case the hard bounds
// exist for: uncompressed textures and audio contain long constant runs, where
// the rolling hash produces either no boundary at all or one at every
// opportunity.
//
// Both outcomes occur here, and both are fine. For a non-zero fill byte the
// hash never matches and the chunk is cut at the maximum. For 0x00 the hash
// sits on its fixed point - rotating zero and cancelling table[0] against
// itself leaves zero - so every position past the minimum matches and the cut
// lands at the minimum. What matters in either case is the property below: the
// chunks come out identical, so a gigabyte of padding costs one chunk in the
// store.
func TestLongRunOfIdenticalBytesDedupesPerfectly(t *testing.T) {
	p := smallParams()

	tests := []struct {
		name     string
		fill     byte
		wantSize int
	}{
		{"zero fill sits on the hash fixed point", 0x00, p.MinSize},
		{"non-zero fill never matches the mask", 0xff, p.MaxSize},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := split(t, bytes.Repeat([]byte{tc.fill}, 20*p.MaxSize), p)

			for i, c := range chunks[:len(chunks)-1] {
				if len(c) != tc.wantSize {
					t.Fatalf("chunk %d has %d bytes, want %d", i, len(c), tc.wantSize)
				}
				if !bytes.Equal(c, chunks[0]) {
					t.Fatalf("chunk %d differs from the first, a constant run must dedup to one chunk", i)
				}
			}
		})
	}
}

// TestSizeDistributionMatchesTheParameters checks E38 against reality rather
// than against arithmetic: the expected size is min + 2^maskBits, and the hard
// maximum should truncate only the tail of the distribution.
func TestSizeDistributionMatchesTheParameters(t *testing.T) {
	p := smallParams()
	chunks := split(t, deterministicBytes(42, 2_000_000), p)

	var total, atMax int
	for _, c := range chunks {
		total += len(c)
		if len(c) == p.MaxSize {
			atMax++
		}
	}

	average := total / len(chunks)
	expected := p.MinSize + 1<<p.MaskBits
	if average < expected*4/5 || average > expected*6/5 {
		t.Errorf("average chunk size %d, want within 20%% of the expected %d", average, expected)
	}

	// At max = min + 3*2^maskBits the tail beyond the bound is e^-3, about 5%.
	if share := float64(atMax) / float64(len(chunks)); share > 0.12 {
		t.Errorf("%.0f%% of chunks were cut at the maximum, want roughly 5%%", share*100)
	}
}

// TestSkippedPrefixDoesNotChangeBoundaries proves the optimization in
// boundary(): starting the rolling hash MinSize-BuzhashWindow bytes into the
// chunk must produce exactly the boundaries a naive scan over every byte
// produces. If this ever fails, the skip is wrong, not the reference.
func TestSkippedPrefixDoesNotChangeBoundaries(t *testing.T) {
	p := smallParams()
	data := deterministicBytes(5, 300_000)

	optimized := split(t, data, p)
	reference := splitNaively(data, p)

	if len(optimized) != len(reference) {
		t.Fatalf("optimized scan produced %d chunks, naive scan %d", len(optimized), len(reference))
	}
	for i := range reference {
		if !bytes.Equal(optimized[i], reference[i]) {
			t.Fatalf("chunk %d differs: optimized %d bytes, naive %d bytes",
				i, len(optimized[i]), len(reference[i]))
		}
	}
}

// splitNaively is the reference implementation: it rolls the hash over every
// byte of every chunk, with no prefix skipped.
func splitNaively(data []byte, p Params) [][]byte {
	var (
		chunks [][]byte
		rh     rollingHash
		start  int
	)

	for start < len(data) {
		rh.reset()
		end := len(data)

		for i := start; i < len(data) && i-start < p.MaxSize; i++ {
			if rh.roll(data[i])&p.mask() == 0 && i+1-start >= p.MinSize {
				end = i + 1
				break
			}
			if i-start+1 == p.MaxSize {
				end = i + 1
				break
			}
		}

		chunks = append(chunks, data[start:end])
		start = end
	}
	return chunks
}

func TestInvalidParamsAreRejected(t *testing.T) {
	tests := []struct {
		name   string
		params Params
	}{
		{"zero minimum", Params{MinSize: 0, MaxSize: 100, MaskBits: 4}},
		{"negative minimum", Params{MinSize: -1, MaxSize: 100, MaskBits: 4}},
		{"maximum below minimum", Params{MinSize: 100, MaxSize: 50, MaskBits: 4}},
		{"zero mask bits", Params{MinSize: 10, MaxSize: 100, MaskBits: 0}},
		{"mask bits too wide", Params{MinSize: 10, MaxSize: 100, MaskBits: 64}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSplitter(bytes.NewReader(nil), tc.params); !errors.Is(err, ErrInvalidParams) {
				t.Errorf("err = %v, want ErrInvalidParams", err)
			}
		})
	}
}

func sharedChunks(a, b [][]byte) int {
	index := make(map[string]int, len(b))
	for _, c := range b {
		index[string(c)]++
	}

	var shared int
	for _, c := range a {
		if index[string(c)] > 0 {
			index[string(c)]--
			shared++
		}
	}
	return shared
}

// TestDefaultParamsMatchTheCatalogue keeps the chunker honest about where its
// numbers come from: nothing is allowed to grow a magic number of its own.
func TestDefaultParamsMatchTheCatalogue(t *testing.T) {
	p := DefaultParams()

	if p.MinSize != tuning.ChunkMinSize || p.MaxSize != tuning.ChunkMaxSize || p.MaskBits != tuning.ChunkMaskBits {
		t.Errorf("DefaultParams = %+v, want the values from package tuning", p)
	}

	// E38: the expected size is min + 2^maskBits, and that is what the
	// catalogue advertises as the average.
	if expected := p.MinSize + 1<<p.MaskBits; expected != tuning.ChunkAvgSize {
		t.Errorf("expected chunk size %d, catalogue says %d", expected, tuning.ChunkAvgSize)
	}

	m := ManifestParams()
	if expected := m.MinSize + 1<<m.MaskBits; expected != tuning.ManifestChunkAvgSize {
		t.Errorf("expected manifest chunk size %d, catalogue says %d", expected, tuning.ManifestChunkAvgSize)
	}
}
