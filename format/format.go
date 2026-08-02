// Package format holds the format parameters of the Fibula object model.
//
// Everything declared here is part of the format: object framing, the BLAKE3
// derive_key contexts and the canonical serialization rules. These values are
// never runtime configuration. Changing one of them changes object identity or
// breaks byte equality with every store already written, which makes it a
// format break that needs a refinement addendum before any code moves.
//
// Tuning parameters, which may be changed freely because they cost dedup rate
// rather than correctness, live in package tuning.
//
// Spec: object model E4, E32, E33, E34; CLAUDE.md section 8 (code quality) on
// the strict separation of the two groups.
package format

// ObjectVersion is the format version carried by every framing line and by
// every derive_key context. A future version 2 may change the hash algorithm,
// which is precisely why hashes carry no algorithm prefix (E34).
const ObjectVersion = 1

// ObjectType names one of the six Fibula object kinds. The name appears both
// in the framing line (E33) and in the derive_key context (E4) and is thereby
// the format's domain separation in two independent places.
type ObjectType string

// The six object kinds. Chunks are the only kind stored as raw content bytes;
// the other five are text objects with a framing line.
const (
	TypeChunk     ObjectType = "chunk"
	TypeFile      ObjectType = "file"
	TypeManifest  ObjectType = "manifest"
	TypeVersion   ObjectType = "version"
	TypeGraph     ObjectType = "graph"
	TypeSignature ObjectType = "signature"
)

// BLAKE3 derive_key contexts, one per object type (E4). Without this
// separation a chunk and a file object with identical bytes would get the same
// ID, which for a small file that fits into a single chunk is the normal case
// rather than an edge case.
const (
	ContextChunk     = "fibula chunk v1"
	ContextFile      = "fibula file v1"
	ContextManifest  = "fibula manifest v1"
	ContextVersion   = "fibula version v1"
	ContextGraph     = "fibula graph v1"
	ContextSignature = "fibula signature v1"
)

// Framing lines, the first line of every text object (E33). There is
// deliberately no chunk header: a chunk is the raw content produced by the
// chunker, and prefixing it would make the stored bytes differ from the file
// content they were cut from.
const (
	HeaderFile      = "fibula-file v1"
	HeaderManifest  = "fibula-manifest v1"
	HeaderVersion   = "fibula-version v1"
	HeaderGraph     = "fibula-graph v1"
	HeaderSignature = "fibula-signature v1"
)

// Canonical serialization rules (E33). Two identical working directories must
// produce byte-identical objects on Windows, macOS and Linux alike, which is
// why none of these follow the host platform.
const (
	// FieldSeparator separates the fields within a line. Tab and LF are
	// therefore both forbidden inside paths (E8.6).
	FieldSeparator = "\t"

	// LineTerminator ends every line, including the last one - LF on Windows
	// too, never CRLF.
	LineTerminator = "\n"

	// PathSeparator is the separator inside manifest paths, on every platform
	// (E8.1).
	PathSeparator = "/"

	// TimestampLayout is RFC 3339 in UTC with second resolution and a literal
	// Z, in the layout syntax of package time (E33).
	TimestampLayout = "2006-01-02T15:04:05Z"
)

// Hash rendering (E34): hex, lowercase, no prefix.
const (
	// HashSize is the length of a BLAKE3 object hash in bytes.
	HashSize = 32

	// HashHexLen is the length of the same hash in its hex rendering.
	HashHexLen = 2 * HashSize
)
