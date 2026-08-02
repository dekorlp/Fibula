// Package store defines the storage abstraction: an immutable,
// content-addressed object store and a mutable ref store with
// compare-and-swap, both independent of any concrete backend.
//
// The split is not cosmetic. Every object except refs is immutable, and
// forcing both into one interface would ignore that they need different
// backends and different guarantees. Deletion lives in a third interface that
// only the admin path receives, so that the no-data-loss invariant is anchored
// in the type system.
//
// Spec: object model E13, E24 to E29.
package store
