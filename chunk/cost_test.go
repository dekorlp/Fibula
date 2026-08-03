package chunk

import (
	"testing"
)

// meanChunk is the average chunk length produced for data under p.
func meanChunk(t *testing.T, data []byte, p Params) int {
	t.Helper()

	chunks := split(t, data, p)
	if len(chunks) == 0 {
		t.Fatal("no chunks produced")
	}
	return len(data) / len(chunks)
}

// newBytes reports how many bytes of after are in chunks that before did not
// already contain. This is what a change actually costs a store, and what a
// user on a slow connection actually waits for.
func newBytes(before, after [][]byte) int {
	have := make(map[string]int, len(before))
	for _, c := range before {
		have[string(c)]++
	}

	var cost int
	for _, c := range after {
		if have[string(c)] > 0 {
			have[string(c)]--
			continue
		}
		cost += len(c)
	}
	return cost
}

// TestAnEditCostsAboutOneChunk pins the law TP-003 measured: a small change
// anywhere in a file costs roughly one mean chunk, because the chunker
// resynchronizes at the next boundary and only the straddling chunk is
// rewritten.
//
// The existing locality tests count how many chunk *boundaries* survive an
// edit, which is the right property but the wrong currency. This measures the
// bytes, which is what a store pays and what decides whether the chunk size
// parameters are set sensibly (E3, E37, E38). A regression that doubled the
// cost while keeping most boundaries intact would pass those tests and fail
// this one.
//
// The bound is deliberately loose. The measured mean is ~1.3x the mean chunk
// size and the worst single offset ~3x, so 4x catches a real regression
// without failing on an unlucky boundary alignment.
func TestAnEditCostsAboutOneChunk(t *testing.T) {
	p := smallParams()
	original := deterministicBytes(21, 400_000)
	before := split(t, original, p)
	mean := meanChunk(t, original, p)

	const trials = 24
	var total, worst int

	for i := range trials {
		// Spread the edits across the file so that no single accidental
		// alignment with a boundary decides the result.
		at := len(original) * (i + 1) / (trials + 2)
		edited := make([]byte, 0, len(original)+16)
		edited = append(edited, original[:at]...)
		edited = append(edited, deterministicBytes(int64(i)+700, 16)...)
		edited = append(edited, original[at:]...)

		cost := newBytes(before, split(t, edited, p))
		total += cost
		if cost > worst {
			worst = cost
		}
	}

	if mean := total / trials; mean > 4*meanChunk(t, original, p) {
		t.Errorf("a 16 byte insertion cost %d bytes on average, want at most %d "+
			"(4x the mean chunk size); the chunker is not resynchronizing",
			mean, 4*meanChunk(t, original, p))
	}
	t.Logf("mean chunk %d B, edit costs %d B on average, %d B worst of %d offsets",
		mean, total/trials, worst, trials)
}

// TestSharedRegionDedupsOnceItExceedsTheChunkSize is the same law seen from the
// other side, and it is the one that decides whether global deduplication (E5)
// delivers anything in practice.
//
// A shared region loses roughly one chunk at each end, where it is cut together
// with surrounding unique data. So dedup is a function of the *ratio* of region
// length to chunk size, not of either alone: a region shorter than a chunk
// deduplicates nothing at all, however often it repeats.
//
// TP-002 hit exactly this on a corpus whose shared runs were shorter than the
// minimum chunk size and deduplicated to zero. The test states the property so
// that it cannot be rediscovered by accident a third time.
func TestSharedRegionDedupsOnceItExceedsTheChunkSize(t *testing.T) {
	p := smallParams()
	mean := meanChunk(t, deterministicBytes(31, 400_000), p)

	tests := []struct {
		name    string
		region  int
		wantMin float64
	}{
		// Shorter than one chunk: nothing can be isolated, so nothing dedups.
		// Asserted as an upper bound, because this is a limitation to know
		// about rather than a guarantee to rely on.
		{"shorter than one chunk", mean / 2, 0},
		{"eight chunks", mean * 8, 0.5},
		{"thirty-two chunks", mean * 32, 0.8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			region := deterministicBytes(41, tc.region)

			a := deterministicBytes(43, 400_000)
			copy(a[50_000:], region)
			b := deterministicBytes(47, 400_000)
			copy(b[310_000:], region)

			chunksA := split(t, a, p)
			index := make(map[string]int, len(chunksA))
			for _, c := range chunksA {
				index[string(c)]++
			}

			var common int
			for _, c := range split(t, b, p) {
				if index[string(c)] > 0 {
					index[string(c)]--
					common += len(c)
				}
			}
			if common > tc.region {
				common = tc.region
			}

			got := float64(common) / float64(tc.region)
			if got < tc.wantMin {
				t.Errorf("a shared region of %d bytes (%.0fx the mean chunk of %d) deduplicated %.0f%%, want at least %.0f%%",
					tc.region, float64(tc.region)/float64(mean), mean, got*100, tc.wantMin*100)
			}
			t.Logf("region %d B (%.0fx mean chunk): %.0f%% deduplicated",
				tc.region, float64(tc.region)/float64(mean), got*100)
		})
	}
}
