package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/netlist"
)

// ExamplesProvider lists and loads bundled example circuits. Used by the
// schematic tab to populate its initial canvas before the user opens or imports
// a circuit of their own. Added in milestone 4 — extends the §11 protocol with
// REST-only helpers, in the same spirit as /api/circuit/parse and /api/library.
type ExamplesProvider interface {
	List() []ExampleSummary
	Load(name string) (*circuit.Circuit, error)
}

// ExampleSummary is the catalog entry returned by GET /api/examples. Title is
// the parsed Circuit.Title (the first comment line of the .cir file); Name is
// the basename without extension and is the key used in Load.
type ExampleSummary struct {
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
}

// ErrExampleNotFound is returned by ExamplesProvider.Load when the named
// example does not exist. The HTTP layer maps it to a 404.
var ErrExampleNotFound = errors.New("example not found")

// NewDirExamples returns an ExamplesProvider that reads .cir files from dir.
// Files are parsed on demand; List walks the directory but does not parse, so
// large catalogs stay cheap.
func NewDirExamples(dir string) ExamplesProvider { return &dirExamples{dir: dir} }

type dirExamples struct{ dir string }

func (d *dirExamples) List() []ExampleSummary {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil
	}
	out := make([]ExampleSummary, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cir") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".cir")
		// Pull just the title line so the catalog is human-readable without
		// a full parse pass per entry.
		title, _ := readTitleLine(filepath.Join(d.dir, e.Name()))
		out = append(out, ExampleSummary{Name: name, Title: title})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (d *dirExamples) Load(name string) (*circuit.Circuit, error) {
	if !validExampleName(name) {
		return nil, ErrExampleNotFound
	}
	path := filepath.Join(d.dir, name+".cir")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrExampleNotFound
		}
		return nil, fmt.Errorf("open example: %w", err)
	}
	defer f.Close()
	c, err := netlist.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parse example %q: %w", name, err)
	}
	return c, nil
}

// validExampleName rejects names containing path separators or other characters
// that would let a request escape the examples directory. The accepted set
// mirrors what the bundled fixtures use: lowercase letters, digits, dashes,
// and underscores.
func validExampleName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func readTitleLine(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "*") {
			return strings.TrimSpace(strings.TrimLeft(trim, "*")), nil
		}
		return trim, nil
	}
	return "", nil
}
