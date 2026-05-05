package netlist_test

import (
	"bytes"
	"strings"
	"testing"

	"circuit-designer/backend/internal/netlist"
)

// emitFor parses fixture, applies the dialect, and returns the emitted text.
func emitFor(t *testing.T, d netlist.Dialect) string {
	t.Helper()
	c, err := netlist.Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var buf bytes.Buffer
	if err := netlist.EmitDialect(c, d, &buf); err != nil {
		t.Fatalf("EmitDialect(%s): %v", d, err)
	}
	return buf.String()
}

func TestEmitDialectNgspiceMatchesEmit(t *testing.T) {
	c, err := netlist.Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var ngOut, plain bytes.Buffer
	if err := netlist.EmitDialect(c, netlist.DialectNgspice, &ngOut); err != nil {
		t.Fatalf("EmitDialect ngspice: %v", err)
	}
	if err := netlist.Emit(c, &plain); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if ngOut.String() != plain.String() {
		t.Fatalf("ngspice dialect must match canonical Emit. got=%q want=%q", ngOut.String(), plain.String())
	}
}

func TestEmitDialectLTspiceRewritesUIC(t *testing.T) {
	out := emitFor(t, netlist.DialectLTspice)
	if !strings.Contains(out, ".TRAN 1u 5m startup") {
		t.Fatalf("LTspice export should contain .TRAN 1u 5m startup; got:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ".TRAN") && strings.Contains(line, "uic") {
			t.Fatalf("LTspice export still has uic on a .TRAN line: %q", line)
		}
	}
	if !strings.Contains(out, "LTspice") {
		t.Fatalf("LTspice export should self-identify in a header comment; got:\n%s", out)
	}
}

func TestEmitDialectBerkeley3StripsLibSection(t *testing.T) {
	c, err := netlist.Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	// Add a section to make the strip observable on this fixture.
	c.Libraries[0].Section = "triode"

	var buf bytes.Buffer
	if err := netlist.EmitDialect(c, netlist.DialectBerkeley3, &buf); err != nil {
		t.Fatalf("EmitDialect berkeley3: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, ".LIB tubes_koren.lib triode") {
		t.Fatalf("Berkeley3 export should strip .LIB section; got:\n%s", out)
	}
	if !strings.Contains(out, ".LIB tubes_koren.lib") {
		t.Fatalf("Berkeley3 export should keep the .LIB path; got:\n%s", out)
	}
}

func TestEmitDialectKiCadAddsBanner(t *testing.T) {
	out := emitFor(t, netlist.DialectKiCad)
	if !strings.Contains(strings.ToLower(out), "kicad") {
		t.Fatalf("KiCad export should self-identify; got:\n%s", out)
	}
}

func TestEmitDialectUnknown(t *testing.T) {
	c, err := netlist.Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var buf bytes.Buffer
	if err := netlist.EmitDialect(c, "lattice", &buf); err == nil {
		t.Fatalf("expected error for unknown dialect")
	}
}
