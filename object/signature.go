package object

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
)

// Signature signs a version (E31).
//
// It is its own object rather than a field inside the version, which keeps it
// free of circularity: were the signature part of the version object, signing
// would have to exclude its own field from the hash computation - the detour
// Git has to take. As a separate object it can also be added later and several
// signatures per version are possible.
//
// Phase 1 does not build signing. This type exists so that the format leaves
// room for it, and its shape is provisional until it does.
type Signature struct {
	// Version is the version being signed.
	Version hash.VersionID

	// Key identifies the signing key. Its shape depends on the key handling,
	// which is not decided - provisional.
	Key string

	// Signature is the raw signature, rendered as lowercase hex like every
	// other binary value in the format (E34).
	Signature []byte
}

// Marshal renders the signature object in its canonical form.
func (s Signature) Marshal() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}

	e := newEncoder(format.HeaderSignature)
	e.line("version", s.Version.String())
	e.line("key", s.Key)
	e.line("signature", hex.EncodeToString(s.Signature))
	return e.bytes(), nil
}

// UnmarshalSignature parses a canonical signature object.
func UnmarshalSignature(data []byte) (Signature, error) {
	d, err := newDecoder(data, format.HeaderSignature)
	if err != nil {
		return Signature{}, err
	}

	versionField, err := d.keyed("version", 1)
	if err != nil {
		return Signature{}, err
	}
	version, err := hash.ParseVersionID(versionField[0])
	if err != nil {
		return Signature{}, fmt.Errorf("signed version id: %w", err)
	}

	keyField, err := d.keyed("key", 1)
	if err != nil {
		return Signature{}, err
	}

	sigField, err := d.keyed("signature", 1)
	if err != nil {
		return Signature{}, err
	}
	raw, err := decodeLowerHex(sigField[0])
	if err != nil {
		return Signature{}, err
	}

	if err := d.expectEnd(); err != nil {
		return Signature{}, err
	}

	s := Signature{Version: version, Key: keyField[0], Signature: raw}
	return s, s.validate()
}

// ID returns the ID of the signature object itself, BLAKE3 over its canonical
// serialization.
func (s Signature) ID() (hash.SignatureID, error) {
	data, err := s.Marshal()
	if err != nil {
		return hash.SignatureID{}, err
	}
	return hash.Signature(data), nil
}

func (s Signature) validate() error {
	if s.Version.IsZero() {
		return fmt.Errorf("%w: signature names no version", errs.ErrInconsistentObject)
	}
	if err := checkText("key", s.Key); err != nil {
		return err
	}
	if len(s.Signature) == 0 {
		return fmt.Errorf("%w: signature is empty", errs.ErrInconsistentObject)
	}
	return nil
}

// decodeLowerHex accepts only the canonical rendering: lowercase, even length,
// nothing else. Uppercase hex would be a second spelling of the same bytes.
func decodeLowerHex(s string) ([]byte, error) {
	if s != strings.ToLower(s) {
		return nil, fmt.Errorf("%w: %q is not lowercase hex", errs.ErrMalformedObject, s)
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errs.ErrMalformedObject, err)
	}
	return raw, nil
}
