// Package manifest assembles manifests from file entries and compares two of
// them, on top of the canonical manifest object in package object.
//
// It deliberately does not read a directory: what a manifest *is* - normalized
// paths, bytewise sorting, rejected case collisions - belongs to the core,
// while walking a tree with its symlinks, ignore rules and host separators
// belongs to the client.
//
// Spec: object model E5, E7, E8, E9, E10.
package manifest
