// Package client implements the working copy: local state, the status cache,
// snapshots, the check that must pass before any file is deleted, and restore.
//
// A space is not a separate concept from the working directory - it is the
// directory, plus what Fibula remembers about it, plus a storage budget (E15).
// Clearing therefore means exactly one thing: delete asset files, keep
// metadata. The space keeps its identity and history, and restoring is a pure
// download.
//
// The most important boundary in this package is between Status and
// CheckClear. Status trusts the cache because it only reports; CheckClear
// ignores it entirely because it precedes deletion (E17).
//
// Spec: object model E12, E15 to E18; CLAUDE.md invariant 6.
package client
