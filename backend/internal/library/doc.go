// Package library loads the YAML component-library files (DESIGN.md §8)
// and ingests user-supplied SPICE .lib files (auto-discovering .SUBCKT
// definitions and emitting palette stubs the user can fill in).
//
// The package exposes a snapshot of the available palette to the API so
// the frontend can render the component picker and the inspector
// schemas.
package library
