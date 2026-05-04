package api

import (
	"errors"
	"strings"
)

// stubLibrary is the milestone-3 LibraryProvider: a small, hard-coded set of
// primitive components matching the Kind values listed in the data model
// (DESIGN.md §4). Library import is rejected with a clear "deferred" error.
//
// Milestone 9 replaces this with a real loader that reads YAML manifests
// (DESIGN.md §8) and ingests user-supplied .lib files.
type stubLibrary struct{}

// NewStubLibrary returns the placeholder library used by the milestone-3 API.
func NewStubLibrary() LibraryProvider { return stubLibrary{} }

var stubComponents = []LibraryComponent{
	{Kind: "resistor", Symbol: "R", Description: "Linear resistor"},
	{Kind: "capacitor", Symbol: "C", Description: "Linear capacitor"},
	{Kind: "inductor", Symbol: "L", Description: "Linear inductor"},
	{Kind: "voltage_source", Symbol: "V", Description: "Independent voltage source (DC, SIN, PULSE, ...)"},
	{Kind: "current_source", Symbol: "I", Description: "Independent current source"},
	{Kind: "subcircuit", Symbol: "X", Description: "Subcircuit instance (.SUBCKT model)"},
}

func (stubLibrary) List(filter string) []LibraryComponent {
	q := strings.ToLower(strings.TrimSpace(filter))
	if q == "" {
		out := make([]LibraryComponent, len(stubComponents))
		copy(out, stubComponents)
		return out
	}
	out := make([]LibraryComponent, 0, len(stubComponents))
	for _, c := range stubComponents {
		if strings.Contains(strings.ToLower(c.Kind), q) ||
			strings.Contains(strings.ToLower(c.Symbol), q) ||
			strings.Contains(strings.ToLower(c.Description), q) {
			out = append(out, c)
		}
	}
	return out
}

func (stubLibrary) Import(filename, body string) error {
	return errors.New("library.import is not implemented in milestone 3 (deferred to milestone 9)")
}
