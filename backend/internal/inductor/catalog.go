package inductor

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

//go:embed data/cores.json
var coresJSON []byte

// Catalog is the bundled preset library, parsed once at first lookup.
// Concurrent reads are safe; mutation is not supported (presets are static).
type Catalog struct {
	byID  map[string]CoreSpec
	order []string // preserves the source-file order for stable List() output
}

var (
	defaultCatalog *Catalog
	catalogOnce    sync.Once
	catalogErr     error
)

// DefaultCatalog returns the singleton parsed from the embedded JSON.
// The error is the parse error from first load and is sticky.
func DefaultCatalog() (*Catalog, error) {
	catalogOnce.Do(func() {
		defaultCatalog, catalogErr = parseCatalog(coresJSON)
	})
	return defaultCatalog, catalogErr
}

func parseCatalog(raw []byte) (*Catalog, error) {
	var entries []CoreSpec
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("inductor catalog parse: %w", err)
	}
	c := &Catalog{
		byID:  make(map[string]CoreSpec, len(entries)),
		order: make([]string, 0, len(entries)),
	}
	for _, e := range entries {
		if e.PresetID == "" {
			return nil, errors.New("inductor catalog: entry missing preset_id")
		}
		if _, dup := c.byID[e.PresetID]; dup {
			return nil, fmt.Errorf("inductor catalog: duplicate preset_id %q", e.PresetID)
		}
		c.byID[e.PresetID] = e
		c.order = append(c.order, e.PresetID)
	}
	return c, nil
}

// Lookup returns the preset with the given id, or ok=false if not found.
func (c *Catalog) Lookup(id string) (CoreSpec, bool) {
	if c == nil {
		return CoreSpec{}, false
	}
	s, ok := c.byID[id]
	return s, ok
}

// List returns all presets in source-file order. The slice is freshly
// allocated and safe for the caller to mutate.
func (c *Catalog) List() []CoreSpec {
	if c == nil {
		return nil
	}
	out := make([]CoreSpec, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.byID[id])
	}
	return out
}

// SortedIDs returns the preset IDs in lexical order. Used by tests.
func (c *Catalog) SortedIDs() []string {
	if c == nil {
		return nil
	}
	ids := append([]string(nil), c.order...)
	sort.Strings(ids)
	return ids
}
