package hash

import "github.com/zeebo/blake3"

// DeriveBytes returns n bytes of BLAKE3 extendable output in derive_key mode
// for the given context, over empty key material.
//
// It exists so that a deterministic table can be specified as one line of
// prose instead of being checked in as unreviewable literals — the buzhash
// table of the chunker is generated this way (E39). Keeping it here also keeps
// BLAKE3 confined to a single package.
func DeriveBytes(context string, n int) []byte {
	out := make([]byte, n)
	h := blake3.NewDeriveKey(context)
	h.Digest().Read(out) //nolint:errcheck // blake3's XOF read never fails
	return out
}
