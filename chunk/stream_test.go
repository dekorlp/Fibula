package chunk

import (
	"errors"
	"io"
	"testing"
)

// TestMemoryCeiling asserts what CLAUDE.md demands of a 4 GB asset on an 8 GB
// machine: the splitter holds at most MaxSize bytes, whatever the stream
// length. The reader here never materializes the stream, so a failure would be
// the splitter's buffer growing.
//
// The stream is 512 MiB rather than something genuinely larger than RAM.
// Streaming past RAM under the race detector costs minutes of CI time and
// proves nothing extra: the ceiling is structural, the buffer is allocated
// once at its capacity and never grown, and 128 chunks exercise every refill
// path. A run against a real multi-GB asset belongs to the end-to-end suite,
// not here.
func TestMemoryCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 512 MiB through the splitter")
	}

	p := DefaultParams()
	const streamSize = int64(512) * 1024 * 1024 // never allocated

	s, err := NewSplitter(&constantReader{remaining: streamSize}, p)
	if err != nil {
		t.Fatalf("NewSplitter: %v", err)
	}

	var total int64
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		total += int64(len(c))

		if cap(s.buf) > p.MaxSize {
			t.Fatalf("buffer grew to %d bytes, ceiling is %d", cap(s.buf), p.MaxSize)
		}
	}

	if total != streamSize {
		t.Errorf("streamed %d bytes, want %d", total, streamSize)
	}
}

// constantReader yields a fixed number of bytes without ever holding them. It
// copies from a small prepared block rather than writing byte by byte, so that
// the test measures the splitter rather than the test's own reader.
type constantReader struct {
	remaining int64
	block     []byte
}

func (c *constantReader) Read(p []byte) (int, error) {
	if c.remaining == 0 {
		return 0, io.EOF
	}
	if c.block == nil {
		c.block = deterministicBytes(11, 64*1024)
	}

	n := int64(len(p))
	if n > c.remaining {
		n = c.remaining
	}

	for written := int64(0); written < n; {
		written += int64(copy(p[written:n], c.block))
	}
	c.remaining -= n
	return int(n), nil
}

func TestReadErrorsAreReported(t *testing.T) {
	wantErr := errors.New("disk fell over")

	s, err := NewSplitter(&failingReader{err: wantErr}, smallParams())
	if err != nil {
		t.Fatalf("NewSplitter: %v", err)
	}

	if _, err := s.Next(); !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want it to wrap %v", err, wantErr)
	}
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }
