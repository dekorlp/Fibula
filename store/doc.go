// Package store defines the storage abstraction: an immutable,
// content-addressed object store and a mutable ref store with
// compare-and-swap, both independent of any concrete backend.
//
// Spec: object model E24 to E29.
package store
