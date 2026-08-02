package hash

import (
	"encoding/hex"
	"fmt"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
)

// ParseChunkID parses the canonical rendering of a ChunkID.
func ParseChunkID(s string) (ChunkID, error) {
	d, err := parse(s)
	return ChunkID{d}, err
}

// ParseFileID parses the canonical rendering of a FileID.
func ParseFileID(s string) (FileID, error) {
	d, err := parse(s)
	return FileID{d}, err
}

// ParseManifestID parses the canonical rendering of a ManifestID.
func ParseManifestID(s string) (ManifestID, error) {
	d, err := parse(s)
	return ManifestID{d}, err
}

// ParseVersionID parses the canonical rendering of a VersionID.
func ParseVersionID(s string) (VersionID, error) {
	d, err := parse(s)
	return VersionID{d}, err
}

// ParseGraphID parses the canonical rendering of a GraphID.
func ParseGraphID(s string) (GraphID, error) {
	d, err := parse(s)
	return GraphID{d}, err
}

// ParseSignatureID parses the canonical rendering of a SignatureID.
func ParseSignatureID(s string) (SignatureID, error) {
	d, err := parse(s)
	return SignatureID{d}, err
}

// parse decodes the canonical rendering of any object ID. Uppercase hex is
// rejected rather than accepted and folded: the canonical rendering is
// lowercase (E34), and silently accepting the other spelling would let a
// non-canonical object round-trip unnoticed.
func parse(s string) (digest, error) {
	var d digest

	if len(s) != format.HashHexLen {
		return d, fmt.Errorf("%w: %d characters, want %d", errs.ErrMalformedID, len(s), format.HashHexLen)
	}
	for i := 0; i < len(s); i++ {
		if !isLowerHex(s[i]) {
			return d, fmt.Errorf("%w: %q is not lowercase hex", errs.ErrMalformedID, s)
		}
	}
	if _, err := hex.Decode(d[:], []byte(s)); err != nil {
		return d, fmt.Errorf("%w: %w", errs.ErrMalformedID, err)
	}
	return d, nil
}

func isLowerHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}
