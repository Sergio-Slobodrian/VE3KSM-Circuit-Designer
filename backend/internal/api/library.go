package api

import (
	"errors"
	"strings"

	"circuit-designer/backend/internal/library"
)

// stubLibrary is a fallback LibraryProvider for tests and the placeholder mode
// the server falls back to when the on-disk library directory is missing. It
// hard-codes the same primitive set the YAML manifests describe so existing
// session/protocol tests keep working without a filesystem.
type stubLibrary struct{}

// NewStubLibrary returns the placeholder library used when no on-disk library
// is available.
func NewStubLibrary() LibraryProvider { return stubLibrary{} }

var stubComponents = []LibraryComponent{
	{Kind: "resistor", RefPrefix: "R", Symbol: "R", Group: "Passive", NodeCount: 2, Description: "Linear resistor"},
	{Kind: "capacitor", RefPrefix: "C", Symbol: "C", Group: "Passive", NodeCount: 2, Description: "Linear capacitor"},
	{Kind: "inductor", RefPrefix: "L", Symbol: "L", Group: "Passive", NodeCount: 2, Description: "Linear inductor"},
	{Kind: "voltage_source", RefPrefix: "V", Symbol: "V", Group: "Sources", NodeCount: 2, Description: "Independent voltage source (DC, SIN, PULSE, ...)"},
	{Kind: "current_source", RefPrefix: "I", Symbol: "I", Group: "Sources", NodeCount: 2, Description: "Independent current source"},
	{Kind: "subcircuit", RefPrefix: "X", Symbol: "X", Group: "Active", NodeCount: 3, Description: "Subcircuit instance (.SUBCKT model)"},
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

func (stubLibrary) Import(filename, body string) (string, []LibraryComponent, []LibraryComponent, error) {
	return "", nil, nil, errors.New("library.import requires the on-disk library loader; this server is running with the stub provider")
}

func (stubLibrary) ImportArchive(filename string, body []byte) (string, []LibraryComponent, []LibraryComponent, []LibraryImportWarning, error) {
	return "", nil, nil, nil, errors.New("library.import_archive requires the on-disk library loader; this server is running with the stub provider")
}

// loadedLibrary adapts a *library.Loader to the LibraryProvider interface used
// by the WebSocket session. The adapter is the production wiring; the stub
// above stays around for tests and degraded-mode startup.
type loadedLibrary struct {
	loader *library.Loader
}

// NewLoadedLibrary wraps a library.Loader as a LibraryProvider. The loader is
// expected to have been Reload()ed before the first request; subsequent
// imports re-load internally.
func NewLoadedLibrary(loader *library.Loader) LibraryProvider {
	return &loadedLibrary{loader: loader}
}

func (l *loadedLibrary) List(filter string) []LibraryComponent {
	snap := l.loader.Snapshot()
	in := snap.Filter(filter)
	out := make([]LibraryComponent, len(in))
	for i, c := range in {
		out[i] = toAPIComponent(c)
	}
	return out
}

func (l *loadedLibrary) Import(filename, body string) (string, []LibraryComponent, []LibraryComponent, error) {
	res, err := l.loader.Import(filename, body)
	if err != nil {
		return "", nil, nil, err
	}
	imported := make([]LibraryComponent, len(res.Imported))
	for i, c := range res.Imported {
		imported[i] = toAPIComponent(c)
	}
	var updated []LibraryComponent
	if len(res.Updated) > 0 {
		updated = make([]LibraryComponent, len(res.Updated))
		for i, c := range res.Updated {
			updated[i] = toAPIComponent(c)
		}
	}
	return res.LibFile, imported, updated, nil
}

func (l *loadedLibrary) ImportArchive(filename string, body []byte) (string, []LibraryComponent, []LibraryComponent, []LibraryImportWarning, error) {
	res, err := l.loader.ImportArchive(filename, body)
	if err != nil {
		return "", nil, nil, nil, err
	}
	imported := make([]LibraryComponent, len(res.Imported))
	for i, c := range res.Imported {
		imported[i] = toAPIComponent(c)
	}
	var updated []LibraryComponent
	if len(res.Updated) > 0 {
		updated = make([]LibraryComponent, len(res.Updated))
		for i, c := range res.Updated {
			updated[i] = toAPIComponent(c)
		}
	}
	var warnings []LibraryImportWarning
	if len(res.Warnings) > 0 {
		warnings = make([]LibraryImportWarning, len(res.Warnings))
		for i, w := range res.Warnings {
			warnings[i] = LibraryImportWarning{File: w.File, Reason: w.Reason}
		}
	}
	return res.LibFile, imported, updated, warnings, nil
}

func toAPIComponent(c library.Component) LibraryComponent {
	out := LibraryComponent{
		Kind:         c.Kind,
		RefPrefix:    c.RefPrefix,
		Symbol:       c.Symbol,
		Description:  c.Description,
		Group:        c.Group,
		NodeCount:    c.NodeCount,
		NodeNames:    append([]string(nil), c.NodeNames...),
		DefaultValue: c.DefaultValue,
		SymbolSVG:    c.SymbolSVG,
		Library:      c.Library,
		ModelName:    c.ModelName,
	}
	if len(c.InspectorFields) > 0 {
		out.InspectorFields = make([]LibraryInspectorField, len(c.InspectorFields))
		for i, f := range c.InspectorFields {
			out.InspectorFields[i] = LibraryInspectorField{
				Name:    f.Name,
				Label:   f.Label,
				Unit:    f.Unit,
				Type:    f.Type,
				Default: f.Default,
			}
		}
	}
	if c.SymbolDef != nil {
		def := &LibrarySymbolDef{
			BBox:        LibrarySymbolRect{W: c.SymbolDef.BBox.W, H: c.SymbolDef.BBox.H},
			Origin:      LibrarySymbolPoint{X: c.SymbolDef.Origin.X, Y: c.SymbolDef.Origin.Y},
			Body:        c.SymbolDef.Body,
			Description: c.SymbolDef.Description,
			ModelFile:   c.SymbolDef.ModelFile,
			SpiceModel:  c.SymbolDef.SpiceModel,
			Source:      c.SymbolDef.Source,
		}
		if len(c.SymbolDef.Pins) > 0 {
			def.Pins = make([]LibrarySymbolPin, len(c.SymbolDef.Pins))
			for i, p := range c.SymbolDef.Pins {
				def.Pins[i] = LibrarySymbolPin{
					Name:      p.Name,
					X:         p.X,
					Y:         p.Y,
					LabelSide: p.LabelSide,
				}
			}
		}
		out.SymbolDef = def
	}
	return out
}
