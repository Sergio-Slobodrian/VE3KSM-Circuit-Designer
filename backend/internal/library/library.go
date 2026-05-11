package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Component is one entry in the loaded palette. Field tags double as YAML
// schema (DESIGN.md §8) and the API JSON schema (api.LibraryComponent embeds
// these via direct copy).
type Component struct {
	Kind            string           `yaml:"kind"             json:"kind"`
	RefPrefix       string           `yaml:"ref_prefix"       json:"ref_prefix"`
	Symbol          string           `yaml:"symbol,omitempty" json:"symbol"`
	Description     string           `yaml:"description"      json:"description,omitempty"`
	Group           string           `yaml:"group,omitempty"  json:"group,omitempty"`
	NodeCount       int              `yaml:"node_count"       json:"node_count"`
	NodeNames       []string         `yaml:"node_names,omitempty"      json:"node_names,omitempty"`
	DefaultValue    string           `yaml:"default_value,omitempty"   json:"default_value,omitempty"`
	SymbolSVG       string           `yaml:"symbol_svg,omitempty"      json:"symbol_svg,omitempty"`
	Library         string           `yaml:"library,omitempty"         json:"library,omitempty"`
	ModelName       string           `yaml:"model_name,omitempty"      json:"model_name,omitempty"`
	InspectorFields []InspectorField `yaml:"inspector_fields,omitempty" json:"inspector_fields,omitempty"`
	// SymbolDef carries structured symbol geometry (bbox/origin/pins/body) for
	// manifests sourced from LTspice .asy converters. When set it supersedes
	// the flat SymbolSVG string for icon and (in later phases) canvas
	// rendering. The YAML key is symbol_def rather than symbol because the
	// short Symbol string field already owns symbol:.
	SymbolDef *SymbolDef `yaml:"symbol_def,omitempty" json:"symbol_def,omitempty"`
	// Source is the relative path of the manifest file on disk, populated by
	// the loader. Useful for "edit symbol" affordances and tests.
	Source string `yaml:"-" json:"-"`
}

// InspectorField is one row in a component's inspector panel. The frontend
// renders these into appropriate input controls.
type InspectorField struct {
	Name    string `yaml:"name"             json:"name"`
	Label   string `yaml:"label"            json:"label"`
	Unit    string `yaml:"unit,omitempty"   json:"unit,omitempty"`
	Type    string `yaml:"type"             json:"type"`
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
}

// Library is a snapshot of all known components. Reload to refresh after a
// .lib import; the value is otherwise immutable.
type Library struct {
	Components []Component
}

// Loader reads YAML manifests from a directory tree, ingests user-supplied
// .lib files into a writable subdirectory, and serves immutable Library
// snapshots. Safe for concurrent use.
//
// Directory layout:
//
//	<Root>/
//	├── passive/*.yaml         — built-in components
//	├── sources/*.yaml
//	├── active/*.yaml
//	├── tubes/*.yaml
//	└── imported/*.yaml        — auto-generated YAML stubs, one per .SUBCKT
//
//	<LibDir>/                  — raw .lib bodies (Loader writes here, engine reads)
//	├── tubes_koren.lib
//	└── …
//
// The split exists because the engine's ngspice subprocess resolves `.LIB`
// references against its working directory, and that working directory has
// historically been the bundled examples/ dir (so `examples/preamp_12ax7.cir`
// can do `.LIB tubes_koren.lib`). Pointing LibDir at the same directory
// preserves that contract; user imports drop into the existing examples/
// search path rather than introducing a second one.
//
// Manifests anywhere under Root are loaded recursively; the `imported/`
// subdirectory just happens to be where Import writes them.
type Loader struct {
	Root   string
	LibDir string

	mu       sync.RWMutex
	snapshot *Library
}

// NewLoader constructs a Loader. Reload must be called before the loader
// returns useful data; the constructor does not touch the disk so it is safe
// to call before either directory exists.
func NewLoader(root, libDir string) *Loader {
	return &Loader{Root: root, LibDir: libDir, snapshot: &Library{}}
}

// Snapshot returns the current Library. The returned slice is shared with
// future readers — do not mutate it.
func (l *Loader) Snapshot() *Library {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.snapshot
}

// Reload re-walks Root and rebuilds the Library snapshot. A missing Root is
// treated as an empty library (the user may not have imported anything yet);
// any other error is reported.
func (l *Loader) Reload() error {
	if l.Root == "" {
		return errors.New("library: loader root is empty")
	}
	info, err := os.Stat(l.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			l.replace(&Library{})
			return nil
		}
		return fmt.Errorf("library: stat root %q: %w", l.Root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("library: root %q is not a directory", l.Root)
	}

	var components []Component
	walkErr := filepath.WalkDir(l.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var c Component
		if err := yaml.Unmarshal(body, &c); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if c.Kind == "" {
			return fmt.Errorf("manifest %s: missing kind", path)
		}
		rel, _ := filepath.Rel(l.Root, path)
		c.Source = filepath.ToSlash(rel)
		components = append(components, c)
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	components = dedupeComponents(components)

	// Stable ordering: by group then kind. The frontend Palette groups by the
	// Group field; within a group, kind is the deterministic sort key.
	sort.SliceStable(components, func(i, j int) bool {
		if components[i].Group != components[j].Group {
			return components[i].Group < components[j].Group
		}
		if components[i].Kind != components[j].Kind {
			return components[i].Kind < components[j].Kind
		}
		return components[i].ModelName < components[j].ModelName
	})

	l.replace(&Library{Components: components})
	return nil
}

// dedupeComponents collapses entries that resolve to the same palette item
// (same Kind + ModelName, case-insensitive on ModelName so SPICE's
// case-insensitive identifier rules don't surprise the user). Bundled
// manifests outrank auto-generated stubs from imports — a stub lives under
// `imported/` and its Source path starts with that prefix, so we treat any
// non-`imported/` entry as the canonical one. This makes re-importing a .lib
// whose models the project already ships idempotent at the palette level
// while still persisting the .lib body so `.LIB foo.lib` resolves at sim time.
func dedupeComponents(in []Component) []Component {
	seen := make(map[string]int, len(in))
	out := make([]Component, 0, len(in))
	for _, c := range in {
		k := paletteKey(c.Kind, c.ModelName)
		if i, ok := seen[k]; ok {
			if isStubSource(out[i].Source) && !isStubSource(c.Source) {
				out[i] = c
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, c)
	}
	return out
}

func isStubSource(src string) bool {
	return strings.HasPrefix(src, "imported/")
}

func (l *Loader) replace(snap *Library) {
	l.mu.Lock()
	l.snapshot = snap
	l.mu.Unlock()
}

// Filter returns the subset of components whose Kind, Symbol, ModelName, or
// Description contains q (case-insensitive). Empty q returns the full slice.
func (lib *Library) Filter(q string) []Component {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		out := make([]Component, len(lib.Components))
		copy(out, lib.Components)
		return out
	}
	out := make([]Component, 0, len(lib.Components))
	for _, c := range lib.Components {
		if strings.Contains(strings.ToLower(c.Kind), q) ||
			strings.Contains(strings.ToLower(c.Symbol), q) ||
			strings.Contains(strings.ToLower(c.ModelName), q) ||
			strings.Contains(strings.ToLower(c.Description), q) {
			out = append(out, c)
		}
	}
	return out
}
