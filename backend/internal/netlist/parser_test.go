package netlist_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/netlist"
)

// fixture is the canonical milestone-1 example. It must stay byte-equivalent
// to examples/preamp_12ax7.cir at the project root — if you change one,
// change the other.
const fixture = `* 12AX7 common-cathode gain stage — preamp_12ax7.cir
* Auto-synced with schematic — edit either side.

.LIB tubes_koren.lib
.PARAM B_PLUS = 250

* --- supply ---
VBB B+ 0 DC {B_PLUS}

* --- input network ---
V1 in_ac 0 SIN(0 0.25 1k)    ; sine 1 kHz, 500 mVpp
C1 in_ac grid 10n
R1 grid 0 1MEG

* --- amplifier stage ---
X1 plate grid cathode 12AX7  ; Koren triode model
R2 B+ plate 100k
R3 cathode 0 1.5k

* --- output coupling ---
C2 plate vout 100n

* --- probes ---
.SAVE V(in_ac) V(vout)

* --- analyses ---
.TRAN 1u 5m uic
*.AC dec 200 10 100k

.END

*+ VBB pos=(450,30)  rot=0   mirror=false
*+ V1  pos=(50,180)  rot=0   mirror=false probe=in_ac
*+ C1  pos=(110,135) rot=0   mirror=false
*+ R1  pos=(220,170) rot=90  mirror=false
*+ X1  pos=(290,130) rot=0   mirror=false
*+ R2  pos=(290,75)  rot=90  mirror=false
*+ R3  pos=(290,200) rot=90  mirror=false
*+ C2  pos=(390,105) rot=0   mirror=false probe=vout
`

// TestRoundTrip is the milestone-1 acceptance criterion (KICKOFF.md):
// parse → emit → parse and assert deep equality on the data model.
func TestRoundTrip(t *testing.T) {
	c1, err := netlist.Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}

	var buf bytes.Buffer
	if err := netlist.Emit(c1, &buf); err != nil {
		t.Fatalf("emit: %v", err)
	}

	c2, err := netlist.Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("second parse: %v\nemitted:\n%s", err, buf.String())
	}

	if !reflect.DeepEqual(c1, c2) {
		t.Errorf("round-trip mismatch")
		t.Logf("emitted source:\n%s", buf.String())
		t.Logf("c1: %+v", c1)
		t.Logf("c2: %+v", c2)
	}
}

// TestFixtureContents checks the explicit element counts and key values
// from KICKOFF.md milestone-1 acceptance criteria.
func TestFixtureContents(t *testing.T) {
	c, err := netlist.Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Libraries
	if got, want := len(c.Libraries), 1; got != want {
		t.Errorf("Libraries count: got %d, want %d", got, want)
	} else if c.Libraries[0].Path != "tubes_koren.lib" {
		t.Errorf("Library path: got %q, want %q",
			c.Libraries[0].Path, "tubes_koren.lib")
	}

	// Parameters
	if got, want := len(c.Parameters), 1; got != want {
		t.Errorf("Parameters count: got %d, want %d", got, want)
	} else {
		p := c.Parameters[0]
		if p.Name != "B_PLUS" || p.Value != "250" {
			t.Errorf("Parameter[0]: got %+v, want {B_PLUS, 250}", p)
		}
	}

	// Components
	expected := []struct {
		ref, kind, model string
	}{
		{"VBB", "voltage_source", ""},
		{"V1", "voltage_source", ""},
		{"C1", "capacitor", ""},
		{"R1", "resistor", ""},
		{"X1", "subcircuit", "12AX7"},
		{"R2", "resistor", ""},
		{"R3", "resistor", ""},
		{"C2", "capacitor", ""},
	}
	if got, want := len(c.Components), len(expected); got != want {
		t.Fatalf("Components count: got %d, want %d", got, want)
	}
	for i, e := range expected {
		got := c.Components[i]
		if got.Ref != e.ref || got.Kind != e.kind || got.Model != e.model {
			t.Errorf("Component[%d]: got {%s, %s, %s}, want {%s, %s, %s}",
				i, got.Ref, got.Kind, got.Model, e.ref, e.kind, e.model)
		}
	}

	// V1 source spec — must be SIN
	v1 := findComponent(c, "V1")
	if v1 == nil {
		t.Fatalf("V1 not found")
	}
	if v1.Source == nil || v1.Source.Mode != "sin" {
		t.Errorf("V1.Source: got %+v, want Mode=sin", v1.Source)
	} else {
		want := map[string]string{"offset": "0", "ampl": "0.25", "freq": "1k"}
		if !reflect.DeepEqual(v1.Source.Params, want) {
			t.Errorf("V1.Source.Params: got %+v, want %+v", v1.Source.Params, want)
		}
	}

	// VBB source spec — must be DC with parameter expression
	vbb := findComponent(c, "VBB")
	if vbb == nil {
		t.Fatalf("VBB not found")
	}
	if vbb.Source == nil || vbb.Source.Mode != "dc" {
		t.Errorf("VBB.Source: got %+v, want Mode=dc", vbb.Source)
	} else if vbb.Source.Params["value"] != "{B_PLUS}" {
		t.Errorf("VBB.Source value: got %q, want %q",
			vbb.Source.Params["value"], "{B_PLUS}")
	}

	// Probes
	if got, want := len(c.Probes), 2; got != want {
		t.Errorf("Probes count: got %d, want %d", got, want)
	} else {
		if c.Probes[0].Node != "in_ac" || c.Probes[1].Node != "vout" {
			t.Errorf("Probes: got %s/%s, want in_ac/vout",
				c.Probes[0].Node, c.Probes[1].Node)
		}
	}

	// Analyses
	var enabled, commented int
	for _, a := range c.Analyses {
		if a.Enabled {
			enabled++
		} else {
			commented++
		}
	}
	if enabled != 1 || commented != 1 {
		t.Errorf("analyses: got %d enabled, %d commented; want 1/1",
			enabled, commented)
	}

	// Layouts
	for _, ref := range []string{"VBB", "V1", "C1", "R1", "X1", "R2", "R3", "C2"} {
		comp := findComponent(c, ref)
		if comp == nil {
			t.Fatalf("%s not found for layout check", ref)
		}
		if comp.Layout == (circuit.Layout{}) {
			t.Errorf("%s missing layout metadata", ref)
		}
	}

	// Spot-check one specific layout
	r1 := findComponent(c, "R1")
	if r1 == nil {
		t.Fatal("R1 not found")
	}
	want := circuit.Layout{X: 220, Y: 170, Rot: 90, Mirror: false}
	if r1.Layout != want {
		t.Errorf("R1.Layout: got %+v, want %+v", r1.Layout, want)
	}
}

func findComponent(c *circuit.Circuit, ref string) *circuit.Component {
	for i := range c.Components {
		if c.Components[i].Ref == ref {
			return &c.Components[i]
		}
	}
	return nil
}

// TestUnsupportedReturnsStructuredError verifies milestone-1 failure mode:
// constructs outside scope return ErrUnsupported (not panics, not generic
// errors), so milestone-2+ code can detect and extend.
func TestUnsupportedReturnsStructuredError(t *testing.T) {
	const src = `* test
.MODEL DMY D
.END
`
	_, err := netlist.Parse(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for .MODEL, got nil")
	}
	var unsupp netlist.ErrUnsupported
	if !errorsAs(err, &unsupp) {
		t.Errorf("expected ErrUnsupported, got %T: %v", err, err)
	}
}

// errorsAs is a tiny wrapper so the test does not need to import errors
// just for one call.
func errorsAs(err error, target *netlist.ErrUnsupported) bool {
	for err != nil {
		if e, ok := err.(netlist.ErrUnsupported); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
