package chunk

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/dekorlp/fibula/hash"
)

func TestBuildFile(t *testing.T) {
	p := smallParams()
	data := deterministicBytes(6, 50_000)

	var sunk [][]byte
	got, err := BuildFile(context.Background(), bytes.NewReader(data), p,
		func(_ context.Context, _ hash.ChunkID, c []byte) error {
			sunk = append(sunk, append([]byte(nil), c...))
			return nil
		})
	if err != nil {
		t.Fatalf("BuildFile: %v", err)
	}

	if got.ID != hash.File(data) {
		t.Errorf("FileID = %s, want the hash of the content %s", got.ID, hash.File(data))
	}
	if got.File.Size != int64(len(data)) {
		t.Errorf("size = %d, want %d", got.File.Size, len(data))
	}
	if len(sunk) != len(got.File.Chunks) {
		t.Errorf("sink saw %d chunks, file object lists %d", len(sunk), len(got.File.Chunks))
	}
	if _, err := got.File.Marshal(); err != nil {
		t.Errorf("the file object built here does not serialize: %v", err)
	}
}

// TestFileIDIsIndependentOfTheChunkingParameters is the done-criterion of
// F-S1-05 and the whole point of E3: two clients with different parameters
// produce the same FileID with different chunk lists. Had identity been
// derived from the chunk list, any later parameter change would have destroyed
// global dedup retroactively.
func TestFileIDIsIndependentOfTheChunkingParameters(t *testing.T) {
	data := deterministicBytes(7, 300_000)

	coarse, err := BuildFile(context.Background(), bytes.NewReader(data),
		Params{MinSize: 4096, MaxSize: 16384, MaskBits: 12}, nil)
	if err != nil {
		t.Fatalf("BuildFile: %v", err)
	}
	fine, err := BuildFile(context.Background(), bytes.NewReader(data),
		Params{MinSize: 512, MaxSize: 2048, MaskBits: 9}, nil)
	if err != nil {
		t.Fatalf("BuildFile: %v", err)
	}

	if coarse.ID != fine.ID {
		t.Errorf("FileID depends on the parameters: %s vs %s", coarse.ID, fine.ID)
	}
	if len(coarse.File.Chunks) == len(fine.File.Chunks) {
		t.Fatalf("both parameter sets produced %d chunks - the test proves nothing",
			len(coarse.File.Chunks))
	}
}

func TestBuildFileHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := BuildFile(ctx, bytes.NewReader(deterministicBytes(8, 100_000)), smallParams(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestBuildFileReportsSinkErrors(t *testing.T) {
	wantErr := errors.New("store unreachable")

	_, err := BuildFile(context.Background(), bytes.NewReader(deterministicBytes(9, 50_000)),
		smallParams(), func(context.Context, hash.ChunkID, []byte) error { return wantErr })

	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want it to wrap %v", err, wantErr)
	}
}
