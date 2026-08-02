// Package path normalizes and validates the paths that appear in manifests and
// dependency graphs, so that the same working tree produces the same object on
// every platform.
//
// It deliberately shadows the standard library package of the same name: these
// are Fibula paths, not filesystem paths, and the distinction is worth the
// import alias in the rare file that needs both.
//
// Spec: object model E8.
package path
