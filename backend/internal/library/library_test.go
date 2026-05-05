package library_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"circuit-designer/backend/internal/library"
)

// TestScannerSubcircuits exercises the .SUBCKT scanner against a body that
// covers the four known shapes: a Koren-style triode with PARAMS continuation,
// a bare 2-pin macromodel, an inline-comment header, and a multi-line pin
// list folded across `+` continuations.
func TestScannerSubcircuits(t *testing.T) {
	body := `* mixed library

.SUBCKT 12AX7 P G K
+ PARAMS: MU=100 EX=1.4 KG1=600
+ KP=600 KVB=300
E1 7 0 VALUE={V(P,K)/KP}
.ENDS 12AX7

.SUBCKT diode_1n4148 a c ; trailing comment
D1 a c DMOD
.ENDS diode_1n4148

.SUBCKT BIG p1 p2 p3 p4
+ p5 p6
.ENDS BIG
`
	subs, err := library.ScanSubcircuits(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("got %d subcircuits, want 3", len(subs))
	}

	// 12AX7 — triode, 5 params after PARAMS+continuation fold.
	if got, want := subs[0].Name, "12AX7"; got != want {
		t.Errorf("name[0]: got %q want %q", got, want)
	}
	if !reflect.DeepEqual(subs[0].Pins, []string{"P", "G", "K"}) {
		t.Errorf("pins[0]: got %v want [P G K]", subs[0].Pins)
	}
	wantParams := []library.ParamDef{
		{Name: "MU", Default: "100"},
		{Name: "EX", Default: "1.4"},
		{Name: "KG1", Default: "600"},
		{Name: "KP", Default: "600"},
		{Name: "KVB", Default: "300"},
	}
	if !reflect.DeepEqual(subs[0].Params, wantParams) {
		t.Errorf("params[0]: got %v want %v", subs[0].Params, wantParams)
	}

	// diode_1n4148 — 2-pin, the inline `; trailing comment` must not leak
	// into the pin list.
	if got, want := subs[1].Pins, []string{"a", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pins[1]: got %v want %v", got, want)
	}

	// BIG — 6 pins assembled from header + continuation.
	wantBigPins := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	if !reflect.DeepEqual(subs[2].Pins, wantBigPins) {
		t.Errorf("pins[2]: got %v want %v", subs[2].Pins, wantBigPins)
	}
}

// TestScannerErrors covers the three structural failure modes.
func TestScannerErrors(t *testing.T) {
	cases := []struct{ name, body string }{
		{"unterminated", ".SUBCKT foo a b\nR1 a b 1k\n"},
		{"stray_ends", ".ENDS foo\n"},
		{"nested", ".SUBCKT outer a b\n.SUBCKT inner c d\n.ENDS inner\n.ENDS outer\n"},
		{"missing_name", ".SUBCKT\n.ENDS\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := library.ScanSubcircuits(tc.body); err == nil {
				t.Errorf("expected error for %q", tc.name)
			}
		})
	}
}

// TestLoaderReloadsManifests writes a tiny manifest tree to a temp dir and
// verifies that Reload picks up every YAML file regardless of nesting depth,
// sorts them deterministically, and surfaces the on-disk Source path.
func TestLoaderReloadsManifests(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "passive", "resistor.yaml"), `
kind: resistor
ref_prefix: R
group: Passive
node_count: 2
default_value: 1k
`)
	mustWrite(t, filepath.Join(root, "tubes", "12ax7.yaml"), `
kind: subcircuit
ref_prefix: X
group: Tubes
node_count: 3
node_names: [P, G, K]
library: tubes_koren.lib
model_name: 12AX7
`)
	// Non-YAML files in the tree are ignored.
	mustWrite(t, filepath.Join(root, "README.md"), "ignored")

	loader := library.NewLoader(root, filepath.Join(root, "lib"))
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	snap := loader.Snapshot()
	if got, want := len(snap.Components), 2; got != want {
		t.Fatalf("components: got %d want %d", got, want)
	}

	// Sort order: by group ("Passive" < "Tubes"), so resistor first.
	if snap.Components[0].Kind != "resistor" || snap.Components[1].ModelName != "12AX7" {
		t.Errorf("unexpected order: %+v", snap.Components)
	}
	if snap.Components[1].Source != "tubes/12ax7.yaml" {
		t.Errorf("source: got %q want tubes/12ax7.yaml", snap.Components[1].Source)
	}

	// Filter narrows by case-insensitive substring across kind/symbol/model/desc.
	matches := snap.Filter("12ax")
	if len(matches) != 1 || matches[0].ModelName != "12AX7" {
		t.Errorf("filter 12ax: got %v", matches)
	}
}

// TestLoaderImportPersistsAndReloads verifies the round-trip:
//  1. Import a .lib body — server writes it to LibDir + emits YAML stubs.
//  2. The loader's snapshot now lists the new components.
//  3. The on-disk .lib bytes match what we sent.
func TestLoaderImportPersistsAndReloads(t *testing.T) {
	root := t.TempDir()
	libDir := t.TempDir()
	loader := library.NewLoader(root, libDir)
	if err := loader.Reload(); err != nil {
		t.Fatalf("initial Reload: %v", err)
	}

	body := `.SUBCKT 12AX7 P G K
+ PARAMS: MU=100
.ENDS 12AX7

.SUBCKT 6L6GC P G K
.ENDS 6L6GC
`
	res, err := loader.Import("../../etc/passwd/tubes.lib", body)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.LibFile != "tubes.lib" {
		t.Errorf("LibFile: got %q want tubes.lib (basename + sanitised)", res.LibFile)
	}
	if len(res.Imported) != 2 {
		t.Fatalf("Imported: got %d want 2", len(res.Imported))
	}

	// Both should have been classified as Tubes (P/G/K pin pattern).
	groups := []string{res.Imported[0].Group, res.Imported[1].Group}
	for _, g := range groups {
		if g != "Tubes" {
			t.Errorf("group: got %q want Tubes", g)
		}
	}

	// .lib body landed in libDir verbatim — ngspice will resolve it via
	// `.LIB tubes.lib` against its WorkDir.
	on, err := os.ReadFile(filepath.Join(libDir, "tubes.lib"))
	if err != nil {
		t.Fatalf("read persisted lib: %v", err)
	}
	if string(on) != body {
		t.Errorf("persisted body differs from input")
	}

	// YAML stubs landed under root/imported/ and are visible in the next
	// snapshot.
	snap := loader.Snapshot()
	names := []string{}
	for _, c := range snap.Components {
		names = append(names, c.ModelName)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"12AX7", "6L6GC"}) {
		t.Errorf("snapshot models: got %v want [12AX7 6L6GC]", names)
	}
	stubs, err := os.ReadDir(filepath.Join(root, "imported"))
	if err != nil {
		t.Fatalf("read stub dir: %v", err)
	}
	if len(stubs) != 2 {
		t.Errorf("stubs on disk: got %d want 2", len(stubs))
	}
}

// TestLoaderImportRejectsBadFilename verifies the filename guard. We accept
// only plain .lib basenames; path components, missing extension, and pure
// extension are all rejected.
func TestLoaderImportRejectsBadFilename(t *testing.T) {
	loader := library.NewLoader(t.TempDir(), t.TempDir())
	cases := []string{"foo.txt", "/etc/passwd", "..", ".lib", ""}
	body := ".SUBCKT FOO a b\n.ENDS\n"
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loader.Import(name, body); err == nil {
				t.Errorf("expected error for filename %q", name)
			}
		})
	}
}

// TestLoaderImportDedupesAgainstBundled covers the user-reported case from
// 2026-05-04: a project ships hand-written manifests for 12AX7/12AU7 and the
// user re-imports tubes_koren.lib. The resulting palette should still show
// each tube exactly once (the bundled manifest wins because it isn't under
// imported/), and the import response should report zero new models so the
// status banner doesn't lie.
func TestLoaderImportDedupesAgainstBundled(t *testing.T) {
	root := t.TempDir()
	libDir := t.TempDir()
	mustWrite(t, filepath.Join(root, "tubes", "12ax7.yaml"), `
kind: subcircuit
ref_prefix: X
group: Tubes
node_count: 3
node_names: [P, G, K]
library: tubes_koren.lib
model_name: 12AX7
`)
	loader := library.NewLoader(root, libDir)
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	body := ".SUBCKT 12AX7 P G K\n.ENDS 12AX7\n.SUBCKT 6L6GC P G K\n.ENDS 6L6GC\n"
	res, err := loader.Import("tubes_koren.lib", body)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Imported reports only the genuinely new row (6L6GC); 12AX7 was already
	// covered by the bundled manifest.
	if len(res.Imported) != 1 || res.Imported[0].ModelName != "6L6GC" {
		t.Errorf("Imported: got %+v want one 6L6GC entry", res.Imported)
	}

	// Snapshot has each model exactly once. The 12AX7 we keep is the bundled
	// one (Source = "tubes/12ax7.yaml"), not the stub.
	snap := loader.Snapshot()
	models := map[string][]string{}
	for _, c := range snap.Components {
		models[c.ModelName] = append(models[c.ModelName], c.Source)
	}
	if got := models["12AX7"]; len(got) != 1 || got[0] != "tubes/12ax7.yaml" {
		t.Errorf("12AX7: got %v want one entry from tubes/12ax7.yaml", got)
	}
	if got := models["6L6GC"]; len(got) != 1 {
		t.Errorf("6L6GC: got %v want one entry", got)
	}
}

// TestLoaderImportRequiresSubckts ensures a .lib body with no .SUBCKT
// definitions is rejected up-front so the user sees a useful error rather
// than a silently empty palette refresh.
func TestLoaderImportRequiresSubckts(t *testing.T) {
	loader := library.NewLoader(t.TempDir(), t.TempDir())
	body := "* just a comment\nR1 a b 1k\n"
	if _, err := loader.Import("nope.lib", body); err == nil {
		t.Errorf("expected error for body with no .SUBCKT")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
