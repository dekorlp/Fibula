package chunk

import (
	"errors"
	"fmt"
	"io"

	"github.com/dekorlp/fibula/tuning"
)

// ErrInvalidParams reports chunking parameters that cannot produce sensible
// boundaries.
var ErrInvalidParams = errors.New("invalid chunking parameters")

// Params are the bounds a Splitter cuts by (E37, E38).
//
// MaskBits is what the rolling hash is actually tested against; the expected
// chunk size is MinSize + 2^MaskBits, because below the minimum no boundary is
// tested at all. Getting that wrong is the classic way to overshoot the target
// size by exactly the minimum.
type Params struct {
	// MinSize is the hard lower bound. No boundary is tested below it.
	MinSize int

	// MaxSize is the hard upper bound. It truncates the tail of the
	// distribution, so that a long run of identical bytes - common in
	// uncompressed textures and audio - cannot produce one gigantic chunk.
	MaxSize int

	// MaskBits selects how many low bits of the rolling hash must be zero for
	// a boundary.
	MaskBits uint
}

// DefaultParams are the parameters for asset content: 1 MiB / 2 MiB expected /
// 4 MiB (E38).
func DefaultParams() Params {
	return Params{
		MinSize:  tuning.ChunkMinSize,
		MaxSize:  tuning.ChunkMaxSize,
		MaskBits: tuning.ChunkMaskBits,
	}
}

// ManifestParams are the parameters for manifests: 32 KiB / 64 KiB expected /
// 128 KiB (E6, E38).
//
// The manifest goes through the same chunker at a much smaller target,
// because it is sorted and line based: a single changed line then hits exactly
// one chunk, which turns a 7 MB transfer on a large project into a 64 KB one.
func ManifestParams() Params {
	return Params{
		MinSize:  tuning.ManifestChunkMinSize,
		MaxSize:  tuning.ManifestChunkMaxSize,
		MaskBits: tuning.ManifestChunkMaskBits,
	}
}

func (p Params) validate() error {
	switch {
	case p.MinSize <= 0:
		return fmt.Errorf("%w: minimum size %d must be positive", ErrInvalidParams, p.MinSize)
	case p.MaxSize < p.MinSize:
		return fmt.Errorf("%w: maximum size %d is below the minimum %d", ErrInvalidParams, p.MaxSize, p.MinSize)
	case p.MaskBits == 0 || p.MaskBits >= 64:
		return fmt.Errorf("%w: mask bits %d must be between 1 and 63", ErrInvalidParams, p.MaskBits)
	}
	return nil
}

// mask is the value the rolling hash is tested against.
func (p Params) mask() uint64 { return 1<<p.MaskBits - 1 }

// Splitter cuts a stream into content-defined chunks.
//
// It holds at most MaxSize bytes at a time, which is what lets a 4 GB asset
// pass through on an 8 GB machine: the file is a sequence of chunks and is
// genuinely streamed, never buffered whole.
type Splitter struct {
	r      io.Reader
	params Params

	buf     []byte // length is the live window, capacity is the memory ceiling
	emitted int    // bytes returned by the last Next, dropped on the next call
	eof     bool

	rh rollingHash
}

// NewSplitter returns a Splitter over r.
func NewSplitter(r io.Reader, p Params) (*Splitter, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &Splitter{
		r:      r,
		params: p,
		buf:    make([]byte, 0, p.MaxSize),
	}, nil
}

// Next returns the next chunk, or io.EOF when the stream is exhausted.
//
// The returned slice points into the Splitter's buffer and stays valid only
// until the following call to Next. A caller that keeps chunks around must
// copy them - or, better, hand each one to the store before asking for the
// next.
func (s *Splitter) Next() ([]byte, error) {
	s.dropEmitted()

	if err := s.fill(); err != nil {
		return nil, err
	}
	if len(s.buf) == 0 {
		return nil, io.EOF
	}

	s.emitted = s.boundary()
	return s.buf[:s.emitted], nil
}

// dropEmitted removes the previously returned chunk. It happens here rather
// than at the end of Next so that the slice handed to the caller survives
// until they ask for the next one.
func (s *Splitter) dropEmitted() {
	if s.emitted == 0 {
		return
	}
	s.buf = append(s.buf[:0], s.buf[s.emitted:]...)
	s.emitted = 0
}

// fill tops the buffer up to its capacity, which is MaxSize. Using ReadFull
// rather than a bare Read means the buffer is either full or the stream is
// exhausted afterwards - a reader that returns zero bytes without an error
// cannot spin the scan loop.
func (s *Splitter) fill() error {
	if s.eof || len(s.buf) == cap(s.buf) {
		return nil
	}

	n, err := io.ReadFull(s.r, s.buf[len(s.buf):cap(s.buf)])
	s.buf = s.buf[:len(s.buf)+n]

	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		s.eof = true
	case err != nil:
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

// boundary returns the length of the chunk starting at buf[0].
//
// The scan starts MinSize-BuzhashWindow bytes in rather than at zero. Bytes
// before that cannot influence any boundary the scan is allowed to accept:
// the hash covers only the last BuzhashWindow bytes, and no boundary below
// MinSize is tested. Rolling them anyway would double the work for nothing -
// and TestSkippedPrefixDoesNotChangeBoundaries proves the two agree.
func (s *Splitter) boundary() int {
	s.rh.reset()

	start := s.params.MinSize - tuning.BuzhashWindow
	if start < 0 {
		start = 0
	}
	if start >= len(s.buf) {
		return len(s.buf) // a final chunk shorter than the minimum
	}

	mask := s.params.mask()
	for i := start; i < len(s.buf); i++ {
		if s.rh.roll(s.buf[i])&mask == 0 && i+1 >= s.params.MinSize {
			return i + 1
		}
	}
	return len(s.buf) // the hard maximum, or the remainder at end of stream
}
