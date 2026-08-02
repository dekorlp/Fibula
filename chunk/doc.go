// Package chunk splits streams into content-defined chunks, so that a boundary
// follows from the content around it rather than from an offset in the file,
// and builds the file object that results from one such pass.
//
// Spec: object model E2, E3; chunking parameters E36 to E40; CLAUDE.md pinned
// point 2.
package chunk
