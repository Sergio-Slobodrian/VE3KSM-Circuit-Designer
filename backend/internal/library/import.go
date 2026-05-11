package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ImportResult describes what an Import call produced. Imported is the set of
// new palette entries the loader will return on its next Snapshot; Updated is
// the set of pre-existing entries whose symbol geometry was enriched in place
// by an .asy ingest. LibFile is the path the raw body was persisted to
// (relative to LibDir for .lib, or empty for .asy since .asy never feeds
// ngspice).
type ImportResult struct {
	LibFile  string
	Imported []Component
	Updated  []Component
}

// Import ingests either a SPICE .lib body or an LTspice .asy symbol body. The
// dispatch is content-based via looksLikeAsy — the first non-comment line
// of an .asy file always starts with "Version 4", whereas .lib files start
// with `*` comments or `.subckt` headers.
//
// .lib path: raw bytes are written to <LibDir>/<sanitized-filename>.lib so
// ngspice can resolve `.LIB` references at simulation time, and one YAML stub
// per discovered .SUBCKT is written to <Root>/imported/<basename>__<model>.yaml.
//
// .asy path: the .asy is parsed via ParseAsy and the resulting SymbolDef is
// merged onto every imported stub whose Library matches the .asy's ModelFile
// SYMATTR. The .asy itself is not persisted in phase 1 — phase 3's archive
// importer keeps the full .asy alongside the .lib so re-importing one or the
// other re-applies the merge from a single source of truth.
//
// filename is the user-supplied basename. Sanitised — only the leaf is kept
// and dangerous characters are stripped — so the caller can safely pass an
// HTTP form filename through.
//
// body is the full file source.
func (l *Loader) Import(filename, body string) (*ImportResult, error) {
	if l.Root == "" {
		return nil, errors.New("library: loader root is empty")
	}
	if l.LibDir == "" {
		return nil, errors.New("library: loader lib dir is empty")
	}
	res, err := l.importOne(filename, body)
	if err != nil {
		return nil, err
	}
	if err := l.Reload(); err != nil {
		return nil, fmt.Errorf("library: reload after import: %w", err)
	}
	return res, nil
}

// importOne is the no-reload core shared by Import (which adds a single
// Reload after one file) and ImportArchive (which batches many files and
// reloads once at the end). All on-disk effects happen here.
func (l *Loader) importOne(filename, body string) (*ImportResult, error) {
	if looksLikeAsy(body) {
		return l.importAsyCore(filename, body)
	}
	return l.importLibCore(filename, body)
}

func (l *Loader) importLibCore(filename, body string) (*ImportResult, error) {
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

	return &ImportResult{
		LibFile:  clean,
		Imported: imported,
	}, nil
}

// importAsyCore parses an .asy body and attaches the resulting SymbolDef to
// every previously-imported stub whose Library matches the .asy's
// SYMATTR ModelFile. No reload here — callers (Import, ImportArchive) handle
// reload at the appropriate granularity.
func (l *Loader) importAsyCore(filename, body string) (*ImportResult, error) {
	clean := sanitiseAsyFilename(filename)
	if clean == "" {
		return nil, errors.New("library: filename must end in .asy")
	}
	sym, err := ParseAsy(body)
	if err != nil {
		return nil, err
	}
	if sym.ModelFile == "" {
		return nil, errors.New("library: .asy missing SYMATTR ModelFile — cannot match to a .lib")
	}
	updated, err := l.attachSymbolToImportedStubs(sym)
	if err != nil {
		return nil, err
	}
	return &ImportResult{
		LibFile:  clean,
		Imported: nil,
		Updated:  updated,
	}, nil
}

// attachSymbolToImportedStubs walks <Root>/imported/*.yaml, decodes each stub,
// and writes back any whose Library matches sym.ModelFile (case-insensitive,
// matching SPICE identifier rules). Suitable for single-file Loader.Import
// calls; archive ingest uses attachSymbolUsingIndex to avoid the O(N²) walk.
//
// The full SymbolDef is attached and the legacy SymbolSVG flat string is
// cleared so the frontend takes the structured path. Returns the slice of
// updated components for the import banner.
func (l *Loader) attachSymbolToImportedStubs(sym *SymbolDef) ([]Component, error) {
	idx, err := l.buildStubIndex()
	if err != nil {
		return nil, err
	}
	return l.attachSymbolUsingIndex(sym, "", idx)
}

// stubRecord is one (path, decoded-Component) pair held by the in-memory
// index. The index is built once at the start of an archive import so the
// .asy merge step doesn't re-walk imported/ for every symbol file.
type stubRecord struct {
	path string
	comp Component
}

// stubIndex is the in-memory map used by archive ingest: lower-case .lib
// basename → list of stubs that came from that .lib. Multiple .asys hitting
// the same .lib reuse the same record entries so writes are idempotent
// (last-writer-wins on SymbolDef, which doesn't matter — for .libs that
// share an .asy via Würth's one-symbol-per-pack pattern, all .asys of the
// same family produce the same SymbolDef anyway).
type stubIndex map[string][]*stubRecord

// buildStubIndex reads <Root>/imported/*.yaml into memory once. Cheap
// (~2000 small files for the Würth passive pack ≈ a single dir scan + N
// reads), and turns the .asy merge from O(N*M) into O(N+M).
func (l *Loader) buildStubIndex() (stubIndex, error) {
	stubDir := filepath.Join(l.Root, "imported")
	entries, err := os.ReadDir(stubDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return stubIndex{}, nil
		}
		return nil, fmt.Errorf("library: read imported dir: %w", err)
	}
	idx := make(stubIndex, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(stubDir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("library: read %s: %w", path, err)
		}
		var c Component
		if err := yaml.Unmarshal(body, &c); err != nil {
			// Skip malformed stubs rather than abort the whole import — the
			// next Reload will surface the error to the operator.
			continue
		}
		key := strings.ToLower(c.Library)
		if key == "" {
			continue
		}
		idx[key] = append(idx[key], &stubRecord{path: path, comp: c})
	}
	return idx, nil
}

// attachSymbolUsingIndex applies sym to every stub the index has for sym's
// ModelFile. Mutates the in-memory comp in place so a second .asy hitting
// the same family sees the already-applied SymbolDef, and writes the
// updated YAML back to disk. Returns the slice of updated components.
//
// groupOverride, when non-empty, replaces the stub's Group — used by the
// archive importer to bucket parts under the .asy's directory path
// (`sym/Capacitors/Electrolytic/` → `Imported · Capacitors · Electrolytic`).
// Pass "" from the single-file path so the importLibCore default ("Imported"
// or "Tubes") is preserved.
func (l *Loader) attachSymbolUsingIndex(sym *SymbolDef, groupOverride string, idx stubIndex) ([]Component, error) {
	target := strings.ToLower(sym.ModelFile)
	records := idx[target]
	if len(records) == 0 {
		return nil, nil
	}
	updated := make([]Component, 0, len(records))
	for _, rec := range records {
		rec.comp.SymbolDef = sym
		rec.comp.SymbolSVG = "" // structured supersedes flat
		if groupOverride != "" {
			rec.comp.Group = groupOverride
		}
		// Promote the .asy's richer Description over the "Imported from X.lib"
		// boilerplate that componentFromSubckt assigns when no symbol metadata
		// is available. Don't clobber a description the user (or a future
		// import path) might have set deliberately — only replace the known
		// boilerplate or an empty string.
		if sym.Description != "" && isBoilerplateDescription(rec.comp.Description) {
			rec.comp.Description = sym.Description
		}
		out, err := yaml.Marshal(&rec.comp)
		if err != nil {
			return nil, fmt.Errorf("library: marshal %s: %w", rec.path, err)
		}
		if err := os.WriteFile(rec.path, out, 0o644); err != nil {
			return nil, fmt.Errorf("library: write %s: %w", rec.path, err)
		}
		c := rec.comp
		rel, _ := filepath.Rel(l.Root, rec.path)
		c.Source = filepath.ToSlash(rel)
		updated = append(updated, c)
	}
	return updated, nil
}

// isBoilerplateDescription reports whether a stub's existing description is
// the default "Imported from <lib>" string that componentFromSubckt assigns —
// i.e. safe to overwrite with a richer SYMATTR Description.
func isBoilerplateDescription(s string) bool {
	if s == "" {
		return true
	}
	return strings.HasPrefix(s, "Imported from ") || strings.HasPrefix(s, "Triode model from ")
}

// sanitiseAsyFilename mirrors sanitiseLibFilename for .asy uploads. The leaf
// is stripped to its basename and reduced to the [A-Za-z0-9._-] character set.
func sanitiseAsyFilename(name string) string {
	leaf := filepath.Base(strings.TrimSpace(name))
	if leaf == "" || leaf == "." || leaf == "/" {
		return ""
	}
	leaf = strings.ReplaceAll(leaf, "..", "")
	if strings.ToLower(filepath.Ext(leaf)) != ".asy" {
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
	if !strings.HasSuffix(strings.ToLower(out), ".asy") || out == ".asy" {
		return ""
	}
	return out
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
