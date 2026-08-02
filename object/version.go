package object

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
	"github.com/dekorlp/fibula/hash"
)

// messageMarker introduces the message block, which runs to the end of the
// object. Cutting on the marker rather than scanning lines is what allows the
// message to contain blank lines while the rest of the object may not (E33):
// no header line can produce this byte sequence, because every one of them has
// a key and a tab in front of its value.
const messageMarker = "\n" + "message" + "\n"

// Version is one state of the project, deliberate or automatic (E11, E12).
//
// Both kinds are the same type, distinguished only by which fields are set: a
// deliberate version has a message and no expiry, an auto snapshot has an
// expiry and no message. That is what makes restore, diff and checkout work
// identically for both, and what makes promotion - keeping yesterday's 14:20
// state - a natural workflow rather than a special case.
type Version struct {
	// Manifest is the state this version points at.
	Manifest hash.ManifestID

	// Parents is a list, not a single pointer. Phase 1 produces linear
	// history and fast-forward only; the list costs nothing today and keeps
	// branching open, and the data model is the expensive thing to change
	// later (E11). The order is meaningful and is never sorted.
	Parents []hash.VersionID

	Author string
	Time   time.Time

	// Expiry is set on auto snapshots only and drives the thinning schedule
	// (E14). The zero value means the version never expires.
	Expiry time.Time

	// Graph optionally references the dependency graph valid for this state
	// (E21). The zero value means no graph was recorded.
	Graph hash.GraphID

	// Message is mandatory for deliberate versions and absent on auto
	// snapshots. It may span several lines and is the only place in the
	// format where a blank line is allowed.
	Message string
}

// Marshal renders the version in its canonical form. Optional fields are
// omitted entirely rather than written empty (E33).
func (v Version) Marshal() ([]byte, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}

	e := newEncoder(format.HeaderVersion)
	e.line("manifest", v.Manifest.String())
	for _, p := range v.Parents {
		e.line("parent", p.String())
	}
	e.line("author", v.Author)
	e.line("time", formatTime(v.Time))
	if !v.Expiry.IsZero() {
		e.line("expiry", formatTime(v.Expiry))
	}
	if !v.Graph.IsZero() {
		e.line("graph", v.Graph.String())
	}
	if v.Message != "" {
		e.line("message")
		for _, line := range strings.Split(v.Message, format.LineTerminator) {
			e.line(line)
		}
	}
	return e.bytes(), nil
}

// UnmarshalVersion parses a canonical version object.
func UnmarshalVersion(data []byte) (Version, error) {
	head, message, err := cutMessage(data)
	if err != nil {
		return Version{}, err
	}

	d, err := newDecoder(head, format.HeaderVersion)
	if err != nil {
		return Version{}, err
	}

	v := Version{Message: message}
	if err := v.parseHead(d); err != nil {
		return Version{}, err
	}
	if err := d.expectEnd(); err != nil {
		return Version{}, err
	}
	return v, v.validate()
}

// parseHead reads the fixed field order of E33: manifest, parents, author,
// time, then the optional expiry and graph.
func (v *Version) parseHead(d *decoder) error {
	manifestField, err := d.keyed("manifest", 1)
	if err != nil {
		return err
	}
	if v.Manifest, err = hash.ParseManifestID(manifestField[0]); err != nil {
		return fmt.Errorf("manifest id: %w", err)
	}

	for d.peek() == "parent" {
		parentField, err := d.keyed("parent", 1)
		if err != nil {
			return err
		}
		parent, err := hash.ParseVersionID(parentField[0])
		if err != nil {
			return fmt.Errorf("parent id: %w", err)
		}
		v.Parents = append(v.Parents, parent)
	}

	authorField, err := d.keyed("author", 1)
	if err != nil {
		return err
	}
	v.Author = authorField[0]

	timeField, err := d.keyed("time", 1)
	if err != nil {
		return err
	}
	if v.Time, err = parseTime(timeField[0]); err != nil {
		return fmt.Errorf("time: %w", err)
	}

	return v.parseOptional(d)
}

func (v *Version) parseOptional(d *decoder) error {
	if d.peek() == "expiry" {
		field, err := d.keyed("expiry", 1)
		if err != nil {
			return err
		}
		if v.Expiry, err = parseTime(field[0]); err != nil {
			return fmt.Errorf("expiry: %w", err)
		}
	}

	if d.peek() == "graph" {
		field, err := d.keyed("graph", 1)
		if err != nil {
			return err
		}
		if v.Graph, err = hash.ParseGraphID(field[0]); err != nil {
			return fmt.Errorf("graph id: %w", err)
		}
	}
	return nil
}

// cutMessage splits the object at the message marker and validates the block.
// The returned head still ends with LF, so it parses like any other object.
func cutMessage(data []byte) (head []byte, message string, err error) {
	idx := bytes.Index(data, []byte(messageMarker))
	if idx < 0 {
		return data, "", nil
	}

	head = data[:idx+1]
	block := data[idx+len(messageMarker):]

	if err := validateMessageBlock(block); err != nil {
		return nil, "", err
	}
	return head, strings.TrimSuffix(string(block), format.LineTerminator), nil
}

// validateMessageBlock applies the rules that still hold inside the message:
// LF endings, no CR, no trailing whitespace. Blank lines are allowed in the
// middle, which is the one exception E33 grants, but not at the edges - a
// leading or trailing blank line is a second spelling of the same message.
func validateMessageBlock(block []byte) error {
	switch {
	case len(block) == 0:
		return fmt.Errorf("%w: message block is empty", errs.ErrMalformedObject)
	case bytes.ContainsRune(block, '\r'):
		return fmt.Errorf("%w: message contains a carriage return", errs.ErrMalformedObject)
	case block[len(block)-1] != '\n':
		return fmt.Errorf("%w: message block is not terminated", errs.ErrMalformedObject)
	}

	lines := strings.Split(strings.TrimSuffix(string(block), format.LineTerminator), format.LineTerminator)
	for i, l := range lines {
		if strings.TrimRight(l, " \t") != l {
			return fmt.Errorf("%w: message line %d has trailing whitespace", errs.ErrMalformedObject, i+1)
		}
	}
	if lines[0] == "" || lines[len(lines)-1] == "" {
		return fmt.Errorf("%w: message starts or ends with a blank line", errs.ErrMalformedObject)
	}
	return nil
}

// ID returns the VersionID, BLAKE3 over the canonical serialization (E11).
func (v Version) ID() (hash.VersionID, error) {
	data, err := v.Marshal()
	if err != nil {
		return hash.VersionID{}, err
	}
	return hash.Version(data), nil
}

func (v Version) validate() error {
	if v.Manifest.IsZero() {
		return fmt.Errorf("%w: version has no manifest", errs.ErrInconsistentObject)
	}
	if err := checkText("author", v.Author); err != nil {
		return err
	}
	if v.Time.IsZero() {
		return fmt.Errorf("%w: version has no timestamp", errs.ErrInconsistentObject)
	}

	seen := make(map[hash.VersionID]struct{}, len(v.Parents))
	for _, p := range v.Parents {
		if p.IsZero() {
			return fmt.Errorf("%w: version has an empty parent id", errs.ErrInconsistentObject)
		}
		if _, dup := seen[p]; dup {
			return fmt.Errorf("%w: parent %s is listed twice", errs.ErrInconsistentObject, p)
		}
		seen[p] = struct{}{}
	}

	if v.Message != "" {
		return validateMessageBlock([]byte(v.Message + format.LineTerminator))
	}
	return nil
}
