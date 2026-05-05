// Package library loads YAML component manifests (DESIGN.md §8) and ingests
// user-supplied SPICE .lib files. Each .SUBCKT discovered during ingest is
// turned into a YAML stub the user can refine; the loader then re-walks the
// manifest tree and republishes a Library snapshot to the API.
//
// The package exposes Loader (Snapshot/Reload/Import) and Library (Filter).
// It owns no goroutines and is safe for concurrent use.
package library
