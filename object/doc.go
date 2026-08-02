// Package object defines the five Fibula object types and their canonical
// text serialization — the format itself.
//
// Every object is written by exactly one encoder and read by exactly one
// strict parser, so that byte equality is a property of the package rather
// than of the discipline of its callers. The parser rejects non-canonical
// input instead of accepting it leniently: an object's ID is computed over its
// bytes as they stand, so a second accepted spelling would mean two IDs for
// one state.
//
// Spec: object model E32 to E35, and the per-type entries E2, E7, E11, E23,
// E31.
package object
