package chunk

import (
	"encoding/binary"
	"math/bits"

	"github.com/dekorlp/fibula/hash"
	"github.com/dekorlp/fibula/tuning"
)

// tableSize is the number of entries in the buzhash substitution table, one
// per possible byte value.
const tableSize = 256

// table maps each byte value to a 64-bit random word (E39).
//
// It is derived from BLAKE3 rather than checked in as 256 literals, for a
// reason that is worth stating: a table of random-looking constants is
// unreviewable. Nobody can tell a correct one from a corrupted one by reading
// it, and a single mistyped digit would cost dedup rate silently, without
// failing a single test. Derived this way the table is reproducible from one
// line of specification, demonstrably uniform, and checkable by hashing it.
//
// The byte order is pinned explicitly, so that the table is identical on a
// big-endian machine. It is the one place in the chunker where host byte order
// could have leaked into the boundaries.
var table = deriveTable()

func deriveTable() [tableSize]uint64 {
	raw := hash.DeriveBytes(tuning.BuzhashTableContext, tableSize*8)

	var t [tableSize]uint64
	for i := range t {
		t[i] = binary.LittleEndian.Uint64(raw[i*8 : (i+1)*8])
	}
	return t
}

// rollingHash is a buzhash over a fixed-size window (E37).
//
// The window is 64 bytes and the hash is 64 bits wide, which is what makes the
// roll step this cheap: the byte leaving the window has been rotated exactly
// 64 times by the time it leaves, and a 64-bit rotation by 64 is the identity,
// so removing it is a plain XOR rather than a rotation of its own.
type rollingHash struct {
	sum    uint64
	window [tuning.BuzhashWindow]byte
	pos    int
}

// reset clears the hash and its window, which is what happens at the start of
// every chunk: a boundary decision must never depend on bytes from the
// previous chunk, otherwise an edit early in a file could not resynchronize.
func (r *rollingHash) reset() {
	r.sum = 0
	r.window = [tuning.BuzhashWindow]byte{}
	r.pos = 0
}

// roll feeds one byte and returns the hash over the last BuzhashWindow bytes.
func (r *rollingHash) roll(b byte) uint64 {
	out := r.window[r.pos]
	r.window[r.pos] = b
	r.pos = (r.pos + 1) % tuning.BuzhashWindow

	r.sum = bits.RotateLeft64(r.sum, 1) ^ table[out] ^ table[b]
	return r.sum
}
