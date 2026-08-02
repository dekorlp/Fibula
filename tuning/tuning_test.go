package tuning

import "testing"

// TestChunkBoundsAreOrdered guards the one property the chunker relies on: the
// hard bounds must bracket the target, otherwise the minimum would suppress
// every boundary the rolling hash finds, or the maximum would cut before the
// average is ever reached.
func TestChunkBoundsAreOrdered(t *testing.T) {
	tests := []struct {
		name          string
		min, avg, max int
	}{
		{"asset chunks", ChunkMinSize, ChunkAvgSize, ChunkMaxSize},
		{"manifest chunks", ManifestChunkMinSize, ManifestChunkAvgSize, ManifestChunkMaxSize},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.min <= 0 {
				t.Errorf("min = %d, want a positive size", tc.min)
			}
			if tc.min >= tc.avg {
				t.Errorf("min = %d, avg = %d, want min < avg", tc.min, tc.avg)
			}
			if tc.avg >= tc.max {
				t.Errorf("avg = %d, max = %d, want avg < max", tc.avg, tc.max)
			}
		})
	}
}

// TestAssetChunkTargetStaysInThePinnedRange checks CLAUDE.md pinned point 2:
// target size 1 to 4 MB. Per E3 a deviation costs dedup rate rather than
// correctness, but it is still a deliberate decision and not a drift.
func TestAssetChunkTargetStaysInThePinnedRange(t *testing.T) {
	const (
		lower = 1 << 20
		upper = 4 << 20
	)

	if ChunkMinSize < lower {
		t.Errorf("ChunkMinSize = %d, want at least %d", ChunkMinSize, lower)
	}
	if ChunkMaxSize > upper {
		t.Errorf("ChunkMaxSize = %d, want at most %d", ChunkMaxSize, upper)
	}
}

// TestManifestChunksAreSmallerThanAssetChunks pins E6: the manifest is chunked
// by the same machinery but at a much smaller target, so that one changed line
// transfers roughly 64 KB instead of the whole manifest.
func TestManifestChunksAreSmallerThanAssetChunks(t *testing.T) {
	if ManifestChunkMaxSize >= ChunkMinSize {
		t.Errorf("manifest max = %d, asset min = %d, want the manifest bounds strictly below the asset bounds",
			ManifestChunkMaxSize, ChunkMinSize)
	}
	if ManifestChunkAvgSize != 64*kiB {
		t.Errorf("ManifestChunkAvgSize = %d, want %d (E6: roughly 64 KB)", ManifestChunkAvgSize, 64*kiB)
	}
}
