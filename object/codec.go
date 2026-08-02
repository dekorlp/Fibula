package object

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dekorlp/fibula/errs"
	"github.com/dekorlp/fibula/format"
)

// byteOrderMark is rejected on input. UTF-8 needs no byte order mark, and one
// at the start of an object would change its ID while leaving it looking
// identical in every editor (E33).
const byteOrderMark = "\ufeff"

// encoder builds an object's canonical bytes. Every line goes through it, so
// that the framing rules of E33 - tab separator, LF terminator, no trailing
// whitespace - are applied in one place instead of at every call site.
type encoder struct {
	buf bytes.Buffer
}

// newEncoder starts an object with its framing line (E33). The type
// designation is part of the hashed content and is therefore a second domain
// separation alongside the derive_key contexts of E4.
func newEncoder(header string) *encoder {
	e := &encoder{}
	e.line(header)
	return e
}

// line writes one tab-separated, LF-terminated line.
func (e *encoder) line(fields ...string) {
	for i, f := range fields {
		if i > 0 {
			e.buf.WriteString(format.FieldSeparator)
		}
		e.buf.WriteString(f)
	}
	e.buf.WriteString(format.LineTerminator)
}

// bytes returns the encoded object.
func (e *encoder) bytes() []byte { return e.buf.Bytes() }

// decoder walks the lines of an object and enforces the canonical rules while
// doing so.
type decoder struct {
	lines []string
	pos   int
}

// newDecoder validates the framing of data and splits it into lines. It
// deliberately checks the whole object up front rather than lazily: a
// malformed object must be rejected before any of its content is acted on.
func newDecoder(data []byte, header string) (*decoder, error) {
	lines, err := splitCanonicalLines(data)
	if err != nil {
		return nil, err
	}
	if lines[0] != header {
		return nil, fmt.Errorf("%w: framing line is %q, want %q", errs.ErrMalformedObject, lines[0], header)
	}
	return &decoder{lines: lines, pos: 1}, nil
}

// splitCanonicalLines applies the rules from E33 that hold for every object:
// UTF-8 without BOM, LF endings including on the last line, no CR, no blank
// lines and no trailing whitespace.
func splitCanonicalLines(data []byte) ([]string, error) {
	switch {
	case len(data) == 0:
		return nil, fmt.Errorf("%w: empty", errs.ErrMalformedObject)
	case !utf8.Valid(data):
		return nil, fmt.Errorf("%w: not valid UTF-8", errs.ErrMalformedObject)
	case bytes.HasPrefix(data, []byte(byteOrderMark)):
		return nil, fmt.Errorf("%w: starts with a byte order mark", errs.ErrMalformedObject)
	case bytes.ContainsRune(data, '\r'):
		return nil, fmt.Errorf("%w: contains a carriage return, line endings are LF", errs.ErrMalformedObject)
	case data[len(data)-1] != '\n':
		return nil, fmt.Errorf("%w: last line is not terminated", errs.ErrMalformedObject)
	}

	lines := strings.Split(string(data[:len(data)-1]), format.LineTerminator)
	for i, l := range lines {
		if err := checkLine(l, i); err != nil {
			return nil, err
		}
	}
	return lines, nil
}

func checkLine(l string, i int) error {
	if l == "" {
		return fmt.Errorf("%w: line %d is blank", errs.ErrMalformedObject, i+1)
	}
	if strings.HasSuffix(l, " ") || strings.HasSuffix(l, format.FieldSeparator) {
		return fmt.Errorf("%w: line %d has trailing whitespace", errs.ErrMalformedObject, i+1)
	}
	return nil
}

// done reports whether every line has been consumed.
func (d *decoder) done() bool { return d.pos >= len(d.lines) }

// peek returns the first field of the next line without consuming it, so that
// optional fields can be recognized by their key.
func (d *decoder) peek() string {
	if d.done() {
		return ""
	}
	key, _, _ := strings.Cut(d.lines[d.pos], format.FieldSeparator)
	return key
}

// next consumes the next line and splits it into exactly want fields.
func (d *decoder) next(want int) ([]string, error) {
	if d.done() {
		return nil, fmt.Errorf("%w: unexpected end of object", errs.ErrMalformedObject)
	}

	line := d.lines[d.pos]
	fields := strings.Split(line, format.FieldSeparator)
	if len(fields) != want {
		return nil, fmt.Errorf("%w: line %d has %d fields, want %d",
			errs.ErrMalformedObject, d.pos+1, len(fields), want)
	}

	d.pos++
	return fields, nil
}

// keyed consumes the next line, which must start with the given key, and
// returns its remaining fields.
func (d *decoder) keyed(key string, want int) ([]string, error) {
	fields, err := d.next(want + 1)
	if err != nil {
		return nil, err
	}
	if fields[0] != key {
		return nil, fmt.Errorf("%w: line %d starts with %q, want %q",
			errs.ErrMalformedObject, d.pos, fields[0], key)
	}
	return fields[1:], nil
}

// expectEnd reports an error if any line is left over.
func (d *decoder) expectEnd() error {
	if !d.done() {
		return fmt.Errorf("%w: unexpected line %d: %q", errs.ErrMalformedObject, d.pos+1, d.lines[d.pos])
	}
	return nil
}

// formatInt renders a number the way E33 requires: decimal, no leading zeros,
// no sign for positive values.
func formatInt(n int64) string { return strconv.FormatInt(n, 10) }

// parseInt is the strict counterpart of formatInt. It rejects what
// strconv.ParseInt would happily accept - a leading plus, a leading zero,
// surrounding space - because each of those is a second spelling of the same
// number and would give one state two IDs.
func parseInt(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("%w: empty number", errs.ErrMalformedObject)
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("%w: %q has a leading zero", errs.ErrMalformedObject, s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%w: %q is not a decimal number", errs.ErrMalformedObject, s)
		}
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %w", errs.ErrMalformedObject, s, err)
	}
	return n, nil
}

// formatTime renders a timestamp as RFC 3339 in UTC with a literal Z and
// second resolution (E33). Sub-second precision is truncated rather than
// rejected: the format has no room for it, and forcing every caller to
// truncate first would mean one forgotten call site produces an object that
// cannot be written at all.
func formatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(format.TimestampLayout)
}

// parseTime accepts exactly what formatTime produces. Any other RFC 3339
// spelling - a numeric offset, a lowercase z, a fractional second - is a
// second rendering of the same instant and is therefore rejected.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(format.TimestampLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q is not an RFC 3339 UTC timestamp: %w",
			errs.ErrMalformedObject, s, err)
	}
	if got := formatTime(t); got != s {
		return time.Time{}, fmt.Errorf("%w: %q is not the canonical rendering of %q",
			errs.ErrMalformedObject, s, got)
	}
	return t.UTC(), nil
}

// checkText validates a free-text field that shares a line with others: no
// control characters (tab and LF would break the framing), no surrounding
// space, not empty.
func checkText(field, value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: %s is empty", errs.ErrMalformedObject, field)
	case !utf8.ValidString(value):
		return fmt.Errorf("%w: %s is not valid UTF-8", errs.ErrMalformedObject, field)
	case strings.TrimSpace(value) != value:
		return fmt.Errorf("%w: %s has leading or trailing whitespace", errs.ErrMalformedObject, field)
	}

	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("%w: %s contains the control character %#02x",
				errs.ErrMalformedObject, field, c)
		}
	}
	return nil
}
