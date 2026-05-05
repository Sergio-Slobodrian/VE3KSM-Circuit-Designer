package netlist

import (
	"fmt"
	"io"
	"strings"

	"circuit-designer/backend/internal/circuit"
)

// Dialect identifies a SPICE flavour the netlist tab can export to.
// DESIGN.md §10.5 lists four targets; the values here are the wire
// strings the frontend's Export menu sends.
type Dialect string

const (
	DialectNgspice    Dialect = "ngspice"
	DialectBerkeley3  Dialect = "berkeley3"
	DialectLTspice    Dialect = "ltspice"
	DialectKiCad      Dialect = "kicad"
)

// Dialects lists every supported export target in display order. Reused by
// the API handler to validate the `target` query parameter and by the
// frontend to populate the dropdown.
var Dialects = []Dialect{
	DialectNgspice,
	DialectBerkeley3,
	DialectLTspice,
	DialectKiCad,
}

// DialectLabel returns a short human-readable name for d. Used by the
// Export menu in the netlist tab.
func DialectLabel(d Dialect) string {
	switch d {
	case DialectNgspice:
		return "ngspice 42"
	case DialectBerkeley3:
		return "Berkeley SPICE 3"
	case DialectLTspice:
		return "LTspice"
	case DialectKiCad:
		return "KiCad"
	}
	return string(d)
}

// EmitDialect writes c as SPICE source for the given dialect. ngspice is the
// canonical form (Emit); the others apply small in-place transformations that
// the target tool expects.
//
// The translations are intentionally conservative — the m1 parser only
// handles a small subset of SPICE in the first place, so the only
// ngspice-isms that can appear are `.tran <...> uic` and a small set of
// transient sources. We translate what we can and leave anything we can't
// touch as a leading comment so the user sees what got dropped.
func EmitDialect(c *circuit.Circuit, d Dialect, w io.Writer) error {
	switch d {
	case "", DialectNgspice:
		return Emit(c, w)
	case DialectBerkeley3:
		return Emit(translateBerkeley3(c), w)
	case DialectLTspice:
		return Emit(translateLTspice(c), w)
	case DialectKiCad:
		return Emit(translateKiCad(c), w)
	}
	return fmt.Errorf("unknown dialect %q", d)
}

// translateBerkeley3 strips ngspice-only directives. In our current model
// .LIB <path> is allowed in Berkeley SPICE 3 but the section form `.LIB
// <path> <section>` is ngspice-specific — drop the section. Analyses,
// parameters, and component lines are otherwise compatible. Unsupported
// inputs (B-source expressions, .MEAS, .STEP) never reach this point because
// the parser rejects them.
func translateBerkeley3(c *circuit.Circuit) *circuit.Circuit {
	out := cloneCircuit(c)
	for i := range out.Libraries {
		out.Libraries[i].Section = ""
	}
	out.Comments = appendNote(out.Comments, "Exported as Berkeley SPICE 3 — section-form .LIB stripped.")
	return out
}

// translateLTspice swaps `uic` for `startup` on .TRAN args (LTspice prefers
// the latter spelling) and notes the dialect in a header comment. Component
// values like "1MEG" are accepted by both engines untouched.
func translateLTspice(c *circuit.Circuit) *circuit.Circuit {
	out := cloneCircuit(c)
	for i, a := range out.Analyses {
		if !strings.EqualFold(a.Kind, "tran") {
			continue
		}
		args := append([]string(nil), a.Args...)
		for j, t := range args {
			if strings.EqualFold(t, "uic") {
				args[j] = "startup"
			}
		}
		out.Analyses[i].Args = args
	}
	out.Comments = appendNote(out.Comments, "Exported for LTspice — .TRAN uic rewritten to startup.")
	return out
}

// translateKiCad emits a flat netlist KiCad's eeschema SPICE importer
// accepts. KiCad shares the Berkeley grammar for the constructs we generate;
// the only behavioural difference is a header comment KiCad inserts on
// import to identify the source. We add it pre-emptively so a round-trip
// through eeschema doesn't double-prefix.
func translateKiCad(c *circuit.Circuit) *circuit.Circuit {
	out := cloneCircuit(c)
	for i := range out.Libraries {
		out.Libraries[i].Section = ""
	}
	out.Comments = appendNote(out.Comments, "Exported for KiCad eeschema SPICE importer.")
	return out
}

// appendNote prefixes a single-space note onto the comment stream so the
// emitter renders it as `* <note>` at the top of the file. Existing comments
// are preserved.
func appendNote(comments []string, note string) []string {
	out := make([]string, 0, len(comments)+1)
	out = append(out, " "+note)
	out = append(out, comments...)
	return out
}

// cloneCircuit returns a deep-enough copy of c that mutations to the slices
// we touch (Libraries, Analyses, Comments) don't leak back into the caller.
// Components and the rest are kept by reference because the dialect
// translators don't modify them.
func cloneCircuit(c *circuit.Circuit) *circuit.Circuit {
	out := *c
	out.Libraries = append([]circuit.LibraryRef(nil), c.Libraries...)
	out.Analyses = append([]circuit.Analysis(nil), c.Analyses...)
	for i, a := range out.Analyses {
		out.Analyses[i].Args = append([]string(nil), a.Args...)
	}
	out.Comments = append([]string(nil), c.Comments...)
	return &out
}
