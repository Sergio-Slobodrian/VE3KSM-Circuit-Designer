package netlist

import (
	"fmt"
	"io"
	"strings"

	"circuit-designer/backend/internal/circuit"
)

// Emit writes c as ngspice 42 source. The emitter pairs with Parse:
// parse(source) → c1; emit(c1) → s'; parse(s') → c2 must satisfy
// reflect.DeepEqual(c1, c2). The actual bytes of source and s' may differ
// (whitespace normalization, comment placement) — see DESIGN.md §5.3.
//
// Output order:
//
//	1. title (one * line)
//	2. comments (each one * line)
//	3. .LIB lines
//	4. .PARAM lines
//	5. components in slice order
//	6. .SAVE (one line, all probes joined)
//	7. analyses, enabled then commented-out
//	8. .END
//	9. *+ layout-metadata lines, in component order
func Emit(c *circuit.Circuit, w io.Writer) error {
	var sb strings.Builder

	if c.Title != "" {
		fmt.Fprintf(&sb, "*%s\n", c.Title)
	}
	for _, cm := range c.Comments {
		fmt.Fprintf(&sb, "*%s\n", cm)
	}
	if c.Title != "" || len(c.Comments) > 0 {
		sb.WriteString("\n")
	}

	for _, lib := range c.Libraries {
		if lib.Section != "" {
			fmt.Fprintf(&sb, ".LIB %s %s\n", lib.Path, lib.Section)
		} else {
			fmt.Fprintf(&sb, ".LIB %s\n", lib.Path)
		}
	}
	for _, p := range c.Parameters {
		fmt.Fprintf(&sb, ".PARAM %s = %s\n", p.Name, p.Value)
	}
	if len(c.Libraries) > 0 || len(c.Parameters) > 0 {
		sb.WriteString("\n")
	}

	for _, comp := range c.Components {
		sb.WriteString(emitComponent(comp))
		sb.WriteString("\n")
	}
	if len(c.Components) > 0 {
		sb.WriteString("\n")
	}

	if len(c.Probes) > 0 {
		args := make([]string, 0, len(c.Probes))
		for _, p := range c.Probes {
			switch p.Kind {
			case "current":
				args = append(args, "I("+p.Node+")")
			default:
				args = append(args, "V("+p.Node+")")
			}
		}
		fmt.Fprintf(&sb, ".SAVE %s\n", strings.Join(args, " "))
	}

	// Enabled analyses first, commented-out second, both in slice-relative
	// order — preserves the order between two analyses of the same enabled
	// state, which is what reflect.DeepEqual cares about.
	for _, a := range c.Analyses {
		if !a.Enabled {
			continue
		}
		fmt.Fprintf(&sb, ".%s %s\n", strings.ToUpper(a.Kind), strings.Join(a.Args, " "))
	}
	for _, a := range c.Analyses {
		if a.Enabled {
			continue
		}
		fmt.Fprintf(&sb, "*.%s %s\n", strings.ToUpper(a.Kind), strings.Join(a.Args, " "))
	}
	if len(c.Probes) > 0 || len(c.Analyses) > 0 {
		sb.WriteString("\n")
	}

	sb.WriteString(".END\n")

	// Two trailing *+ comment blocks: layout metadata first (per DESIGN.md
	// §5.2), then the m10 waveform metadata for high-level signal-generator
	// sources whose lowered SPICE form (PULSE/PWL) doesn't carry the
	// inspector-facing parameters. Both blocks survive ngspice because they
	// sit past .END; both blocks survive a round-trip because the parser
	// recognises the `*+ <ref> ...` prefix in either order.
	hasLayout := false
	for _, comp := range c.Components {
		if comp.Layout != (circuit.Layout{}) {
			hasLayout = true
			break
		}
	}
	if hasLayout {
		sb.WriteString("\n")
		for _, comp := range c.Components {
			l := comp.Layout
			if l == (circuit.Layout{}) {
				continue
			}
			mirror := "false"
			if l.Mirror {
				mirror = "true"
			}
			fmt.Fprintf(&sb, "*+ %s pos=(%d,%d) rot=%d mirror=%s\n",
				comp.Ref, l.X, l.Y, l.Rot, mirror)
		}
	}

	hasWaveformMeta := false
	for _, comp := range c.Components {
		if line := emitWaveformMeta(comp.Ref, comp.Source); line != "" {
			hasWaveformMeta = true
			break
		}
	}
	if hasWaveformMeta {
		sb.WriteString("\n")
		for _, comp := range c.Components {
			line := emitWaveformMeta(comp.Ref, comp.Source)
			if line == "" {
				continue
			}
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	_, err := io.WriteString(w, sb.String())
	return err
}

func emitComponent(comp circuit.Component) string {
	switch comp.Kind {
	case "resistor", "capacitor", "inductor":
		if len(comp.Nodes) < 2 {
			return fmt.Sprintf("%s ? ? %s", comp.Ref, comp.Value)
		}
		return fmt.Sprintf("%s %s %s %s",
			comp.Ref, comp.Nodes[0], comp.Nodes[1], comp.Value)

	case "voltage_source", "current_source":
		nodes := strings.Join(comp.Nodes, " ")
		spec := emitSourceSpec(comp.Source)
		if spec == "" {
			return fmt.Sprintf("%s %s", comp.Ref, nodes)
		}
		return fmt.Sprintf("%s %s %s", comp.Ref, nodes, spec)

	case "subcircuit":
		return fmt.Sprintf("%s %s %s",
			comp.Ref, strings.Join(comp.Nodes, " "), comp.Model)

	case "vcvs", "vccs":
		// 4-node controlled source: ref n+ n- nc+ nc- gain.
		return fmt.Sprintf("%s %s %s",
			comp.Ref, strings.Join(comp.Nodes, " "), comp.Value)

	case "cccs", "ccvs":
		// 2-node controlled source with controlling V ref in Model.
		return fmt.Sprintf("%s %s %s %s",
			comp.Ref, strings.Join(comp.Nodes, " "), comp.Model, comp.Value)
	}
	// Unknown kind — emit what we know so a human can debug.
	return fmt.Sprintf("%s %s %s",
		comp.Ref, strings.Join(comp.Nodes, " "), comp.Value)
}

func emitSourceSpec(s *circuit.SourceSpec) string {
	if s == nil {
		return ""
	}
	// Tokens are assembled in canonical order: DC, AC, transient. The parser
	// accepts any order but the emitter is opinionated to keep round-tripped
	// netlists stable.
	parts := []string{}
	mode := strings.ToLower(s.Mode)
	switch mode {
	case ModeDC:
		parts = append(parts, "DC "+s.Params["value"])
	case ModeAC:
		// Pure AC stimulus — no DC, no transient. Nothing to emit here; the
		// AC token is appended below.
	default:
		// Every transient mode (sin/pulse/sffm/synthesized) may also carry a
		// DC bias in Params["dc"]; ngspice treats it as the source's quiescent
		// value under transient analysis. Preserve it so a round-trip through
		// "DC x AC y SIN(...)" survives.
		if dc, ok := s.Params["dc"]; ok && dc != "" {
			parts = append(parts, "DC "+dc)
		}
	}
	if s.AC != nil {
		ac := "AC " + s.AC.Magnitude
		if s.AC.Phase != "" {
			ac += " " + s.AC.Phase
		}
		parts = append(parts, ac)
	}
	transient, _, err := lowerSource(s)
	if err != nil {
		// Fall back to a comment-shaped token so the emitter never crashes;
		// the netlist will fail to simulate, which surfaces the bug
		// immediately. The error path is exercised by waveforms_test.
		transient = "; lowering error: " + err.Error()
	}
	if transient != "" {
		parts = append(parts, transient)
	}
	return strings.Join(parts, " ")
}
