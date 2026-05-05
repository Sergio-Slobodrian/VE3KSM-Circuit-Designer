package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ImportResult describes what an Import call produced. Imported is the set of
// new palette entries the loader will return on its next Snapshot; LibFile is
// the path the raw .lib body was written to (relative to the loader root).
type ImportResult struct {
	LibFile  string
	Imported []Component
}

// Import ingests a SPICE .lib body. The raw bytes are written to
// <LibDir>/<sanitized-filename>.lib so ngspice can resolve `.LIB` references
// at simulation time, and one YAML stub per discovered .SUBCKT is written to
// <Root>/imported/<basename>__<model>.yaml. Reload is invoked so the next
// Snapshot reflects the new entries.
//
// filename is the user-supplied basename (e.g. "tubes_koren.lib"). It is
// sanitised — only the leaf is kept and dangerous characters are stripped —
// so the caller can safely pass an HTTP form filename through.
//
// body is the full .lib source.
func (l *Loader) Import(filename, body string) (*ImportResult, error) {
	if l.Root == "" {
		return nil, errors.New("library: loader root is empty")
	}
	if l.LibDir == "" {
		return nil, errors.New("library: loader lib dir is empty")
	}
	clean := sanitiseLibFilename(filename)
	if clean == "" {
		return nil, errors.New("library: filename must end in .lib")
	}

	subs, err := ScanSubcircuits(body)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, errors.New("library: no .SUBCKT definitions found")
	}

	// Capture the current snapshot's (kind, model) keys so we can label the
	// import response with what is actually new vs already-known. Bundled
	// manifests for tubes_koren shipped models will mask the stubs that
	// componentFromSubckt is about to fabricate.
	pre := map[string]bool{}
	for _, c := range l.Snapshot().Components {
		pre[paletteKey(c.Kind, c.ModelName)] = true
	}

	stubDir := filepath.Join(l.Root, "imported")
	if err := os.MkdirAll(l.LibDir, 0o755); err != nil {
		return nil, fmt.Errorf("library: mkdir lib dir: %w", err)
	}
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		return nil, fmt.Errorf("library: mkdir imported dir: %w", err)
	}

	libPath := filepath.Join(l.LibDir, clean)
	if err := os.WriteFile(libPath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("library: write %s: %w", libPath, err)
	}

	base := strings.TrimSuffix(clean, ".lib")
	imported := make([]Component, 0, len(subs))
	for _, s := range subs {
		comp := componentFromSubckt(s, clean, base)
		stub := filepath.Join(stubDir, fmt.Sprintf("%s__%s.yaml", base, sanitiseManifestName(s.Name)))
		out, err := yaml.Marshal(&comp)
		if err != nil {
			return nil, fmt.Errorf("library: marshal stub %s: %w", s.Name, err)
		}
		if err := os.WriteFile(stub, out, 0o644); err != nil {
			return nil, fmt.Errorf("library: write %s: %w", stub, err)
		}
		rel, _ := filepath.Rel(l.Root, stub)
		comp.Source = filepath.ToSlash(rel)
		// Only count this entry as "imported" (i.e. user-visible new palette
		// row) when the (kind, model) wasn't already covered by a bundled
		// manifest — otherwise the dedupe step in Reload will mask the stub.
		if !pre[paletteKey(comp.Kind, comp.ModelName)] {
			imported = append(imported, comp)
		}
	}

	if err := l.Reload(); err != nil {
		return nil, fmt.Errorf("library: reload after import: %w", err)
	}

	return &ImportResult{
		LibFile:  clean,
		Imported: imported,
	}, nil
}

// paletteKey is the dedupe key used by both Loader.Reload and Loader.Import.
// Models compare case-insensitively (SPICE identifiers are case-insensitive);
// primitives without a model fall back to kind.
func paletteKey(kind, model string) string {
	if model == "" {
		return kind + "/"
	}
	return kind + "/" + strings.ToLower(model)
}

// componentFromSubckt fabricates a YAML stub for one discovered .SUBCKT. We
// classify the device by pin count (3-pin → triode-like tube, 2-pin → diode,
// 4-pin+ → generic subcircuit) only to pick a default symbol; the user is
// expected to refine the stub if the heuristic guesses wrong.
func componentFromSubckt(s Subckt, libFile, libBase string) Component {
	kind := "subcircuit"
	group := "Imported"
	symbolSVG := defaultSubcircuitSymbol
	prefix := "X"
	desc := fmt.Sprintf("Imported from %s", libFile)

	switch len(s.Pins) {
	case 2:
		// Could be a diode model or a generic two-pin macromodel — keep
		// kind=subcircuit (most user-supplied .lib files are macromodels) but
		// signal it visually as a two-terminal device.
		symbolSVG = defaultTwoTerminalSymbol
	case 3:
		// Triode-shaped — the Koren tube models are the canonical example.
		// Heuristic: pin names spell P/G/K (or A/G/K) → tube symbol.
		if isTubePinPattern(s.Pins) {
			group = "Tubes"
			symbolSVG = defaultTubeSymbol
			desc = fmt.Sprintf("Triode model from %s", libFile)
		}
	}

	insp := []InspectorField{
		{Name: "model", Label: "Model", Unit: "", Type: "text", Default: s.Name},
	}
	for _, p := range s.Params {
		insp = append(insp, InspectorField{
			Name:    strings.ToLower(p.Name),
			Label:   p.Name,
			Type:    "text",
			Default: p.Default,
		})
	}

	return Component{
		Kind:            kind,
		RefPrefix:       prefix,
		Symbol:          prefix,
		Description:     desc,
		Group:           group,
		NodeCount:       len(s.Pins),
		NodeNames:       append([]string(nil), s.Pins...),
		SymbolSVG:       symbolSVG,
		Library:         libFile,
		ModelName:       s.Name,
		InspectorFields: insp,
	}
}

func isTubePinPattern(pins []string) bool {
	if len(pins) != 3 {
		return false
	}
	got := make(map[string]bool, 3)
	for _, p := range pins {
		got[strings.ToUpper(p)] = true
	}
	if got["P"] && got["G"] && got["K"] {
		return true
	}
	if got["A"] && got["G"] && got["K"] {
		return true
	}
	return false
}

// sanitiseLibFilename strips path components and ensures the .lib extension.
// Returns empty string for input that cannot be reduced to a .lib filename.
func sanitiseLibFilename(name string) string {
	leaf := filepath.Base(strings.TrimSpace(name))
	if leaf == "" || leaf == "." || leaf == "/" {
		return ""
	}
	leaf = strings.ReplaceAll(leaf, "..", "")
	if strings.ToLower(filepath.Ext(leaf)) != ".lib" {
		return ""
	}
	cleaned := make([]rune, 0, len(leaf))
	for _, r := range leaf {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			cleaned = append(cleaned, r)
		}
	}
	out := string(cleaned)
	if !strings.HasSuffix(strings.ToLower(out), ".lib") {
		return ""
	}
	if out == ".lib" {
		return ""
	}
	return out
}

// sanitiseManifestName produces a filesystem-safe slug from a SUBCKT name.
func sanitiseManifestName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			out = append(out, r)
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "model"
	}
	return string(out)
}

// Default symbols rendered as raw SVG path bodies. The frontend wraps them in
// an <svg> with an appropriate viewBox per component group.
const (
	defaultSubcircuitSymbol = `<rect x="2" y="2" width="20" height="14" fill="none"/>` +
		`<path d="M5 9 H19" stroke-dasharray="1.5 1.2"/>`
	defaultTwoTerminalSymbol = `<circle cx="11" cy="7" r="5" fill="none"/>` +
		`<path d="M0 7 H6 M16 7 H22"/>`
	defaultTubeSymbol = `<circle cx="11" cy="8" r="6" fill="none"/>` +
		`<path d="M11 2 V0 M7 5 H15 M11 14 V16"/>` +
		`<path d="M5 8 H17" stroke-dasharray="1.5 1.2"/>`
)
