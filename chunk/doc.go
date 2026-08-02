// Package chunk splits streams into content-defined chunks, so that a boundary
// follows from the content around it rather than from an offset in the file.
//
// Spec: object model E2, E3; CLAUDE.md pinned point 2.
package chunk
