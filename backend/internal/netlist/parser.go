package netlist

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"circuit-designer/backend/internal/circuit"
)

// Parse reads SPICE source and produces a Circuit.
//
// Milestone 1 scope: the constructs that appear in
// examples/preamp_12ax7.cir — title/comments, .LIB, .PARAM, R/C/L/V/I/X
// components, DC and SIN source specs, .SAVE V(...)/I(...), .TRAN/.AC/.DC/
// .OP/.NOISE analyses (enabled or commented out via leading *), .END,
// and *+ layout-metadata comments. Anything outside that scope returns
// ErrUnsupported; milestone-2+ extends coverage without changing this API.
func Parse(r io.Reader) (*circuit.Circuit, error) {
	rawLines, err := readAllLines(r)
	if err != nil {
		return nil, err
	}
	logical := mergeContinuations(rawLines)

	c := &circuit.Circuit{
		Comments:   []string{},
		Libraries:  []circuit.LibraryRef{},
		Parameters: []circuit.Param{},
		Components: []circuit.Component{},
		Wires:      []circuit.Wire{},
		Probes:     []circuit.Probe{},
		Analyses:   []circuit.Analysis{},
	}
	layouts := map[string]circuit.Layout{}
	// waveformMeta carries `*+ <ref> waveform=<mode> k=v ...` records keyed by
	// component ref. Applied after parse so the order of the V/I component line
	// vs. the *+ companion does not matter (m10 emits the *+ block past .END,
	// but the parser shouldn't depend on that).
	waveformMeta := map[string]waveformMetaRecord{}

	titleSeen := false
	endSeen := false

	for _, lg := range logical {
		if err := dispatch(lg.text, lg.line, &titleSeen, &endSeen, layouts, waveformMeta, c); err != nil {
			return nil, err
		}
	}

	// Apply collected layout metadata to components by ref.
	for ref, layout := range layouts {
		for i := range c.Components {
			if c.Components[i].Ref == ref {
				c.Components[i].Layout = layout
				break
			}
		}
	}

	// Apply collected high-level waveform metadata. Overrides whatever
	// SourceSpec parseSource derived from the V/I line; the lowered PULSE/PWL
	// params are dropped in favour of the high-level form so the inspector
	// shows what the user originally entered.
	for ref, meta := range waveformMeta {
		for i := range c.Components {
			if c.Components[i].Ref != ref {
				continue
			}
			src := c.Components[i].Source
			if src == nil {
				src = &circuit.SourceSpec{}
			}
			src.Mode = meta.mode
			src.Params = recoverFromPWLPoints(meta.mode, src.Params, meta.kv)
			c.Components[i].Source = src
			break
		}
	}

	return c, nil
}

// waveformMetaRecord captures one parsed *+ waveform= line. `mode` is the
// canonical lowered mode name; `kv` are the rest of the key=value pairs.
type waveformMetaRecord struct {
	mode string
	kv   map[string]string
}

type logicalLine struct {
	text string
	line int
}

func readAllLines(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var out []string
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return out, nil
}

// mergeContinuations combines lines that start with '+' (after optional
// whitespace) into the preceding logical line, per SPICE convention.
func mergeContinuations(raw []string) []logicalLine {
	var out []logicalLine
	for i, r := range raw {
		trimmedLeft := strings.TrimLeft(r, " \t")
		if strings.HasPrefix(trimmedLeft, "+") {
			if len(out) == 0 {
				// Continuation with no prior line — treat as ordinary line.
				out = append(out, logicalLine{text: r, line: i + 1})
				continue
			}
			out[len(out)-1].text += " " + strings.TrimSpace(strings.TrimPrefix(trimmedLeft, "+"))
			continue
		}
		out = append(out, logicalLine{text: r, line: i + 1})
	}
	return out
}

// dispatch routes one logical line to the appropriate handler. The line is
// normalized (whitespace-trimmed) once at entry so all subsequent prefix
// and slice operations work consistently.
func dispatch(rawLine string, num int, titleSeen, endSeen *bool, layouts map[string]circuit.Layout, waveformMeta map[string]waveformMetaRecord, c *circuit.Circuit) error {
	line := strings.TrimSpace(rawLine)
	if line == "" {
		return nil
	}

	// Past .END only *+ metadata is meaningful; everything else is ignored.
	if *endSeen {
		if strings.HasPrefix(line, "*+") {
			return parseStructuredMeta(line, num, layouts, waveformMeta)
		}
		return nil
	}

	// First non-empty line is always the title (per ngspice convention).
	if !*titleSeen {
		*titleSeen = true
		if strings.HasPrefix(line, "*+") {
			// First line is structured metadata; no title set.
			return parseStructuredMeta(line, num, layouts, waveformMeta)
		}
		if strings.HasPrefix(line, "*") {
			c.Title = line[1:]
		} else {
			c.Title = line
		}
		return nil
	}

	// Layout metadata may appear pre-.END too.
	if strings.HasPrefix(line, "*+") {
		return parseStructuredMeta(line, num, layouts, waveformMeta)
	}

	// Comment line, or commented-out directive.
	if strings.HasPrefix(line, "*") {
		rest := line[1:]
		inner := strings.TrimSpace(rest)
		if strings.HasPrefix(inner, ".") {
			if a, ok := tryParseAnalysisDirective(inner, false); ok {
				c.Analyses = append(c.Analyses, a)
				return nil
			}
		}
		c.Comments = append(c.Comments, rest)
		return nil
	}

	// Strip inline trailing comment; the content is dropped (milestone 1).
	body := strings.TrimSpace(stripInlineComment(line))
	if body == "" {
		return nil
	}

	if strings.HasPrefix(body, ".") {
		return parseDirective(body, num, endSeen, c)
	}

	return parseComponent(body, num, c)
}

func stripInlineComment(line string) string {
	if i := strings.Index(line, ";"); i >= 0 {
		return strings.TrimRight(line[:i], " \t")
	}
	return line
}

// parseDirective handles .LIB, .PARAM, .SAVE, .TRAN, .AC, .DC, .OP, .NOISE,
// and .END.
func parseDirective(line string, num int, endSeen *bool, c *circuit.Circuit) error {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	head := strings.ToUpper(fields[0])
	rest := fields[1:]

	switch head {
	case ".LIB":
		if len(rest) == 0 {
			return errorAt(num, ".LIB requires a path")
		}
		ref := circuit.LibraryRef{Path: rest[0]}
		if len(rest) > 1 {
			ref.Section = rest[1]
		}
		c.Libraries = append(c.Libraries, ref)
		return nil

	case ".PARAM":
		afterParam := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		eq := strings.Index(afterParam, "=")
		if eq < 0 {
			return errorAt(num, ".PARAM requires NAME = VALUE")
		}
		name := strings.TrimSpace(afterParam[:eq])
		value := strings.TrimSpace(afterParam[eq+1:])
		if name == "" || value == "" {
			return errorAt(num, ".PARAM has empty name or value")
		}
		c.Parameters = append(c.Parameters, circuit.Param{Name: name, Value: value})
		return nil

	case ".SAVE":
		for _, arg := range rest {
			switch {
			case strings.HasPrefix(arg, "V(") && strings.HasSuffix(arg, ")"):
				node := arg[2 : len(arg)-1]
				c.Probes = append(c.Probes, circuit.Probe{
					Name: node, Node: node, Kind: "voltage",
				})
			case strings.HasPrefix(arg, "I(") && strings.HasSuffix(arg, ")"):
				branch := arg[2 : len(arg)-1]
				c.Probes = append(c.Probes, circuit.Probe{
					Name: branch, Node: branch, Kind: "current",
				})
			default:
				return errorAtf(num, "unsupported .SAVE arg %q", arg)
			}
		}
		return nil

	case ".TRAN", ".AC", ".DC", ".OP", ".NOISE":
		c.Analyses = append(c.Analyses, circuit.Analysis{
			Kind:    strings.ToLower(strings.TrimPrefix(head, ".")),
			Args:    append([]string(nil), rest...),
			Enabled: true,
		})
		return nil

	case ".END":
		*endSeen = true
		return nil

	default:
		return ErrUnsupported{Line: num, Directive: head}
	}
}

// tryParseAnalysisDirective returns an Analysis if the given commented-out
// line matches a recognised analysis directive, otherwise (zero, false).
func tryParseAnalysisDirective(line string, enabled bool) (circuit.Analysis, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return circuit.Analysis{}, false
	}
	head := strings.ToUpper(fields[0])
	switch head {
	case ".TRAN", ".AC", ".DC", ".OP", ".NOISE":
		return circuit.Analysis{
			Kind:    strings.ToLower(strings.TrimPrefix(head, ".")),
			Args:    append([]string(nil), fields[1:]...),
			Enabled: enabled,
		}, true
	}
	return circuit.Analysis{}, false
}

// parseComponent handles R, C, L, V, I, X. Anything else returns
// ErrUnsupported so milestone 2 can extend.
func parseComponent(line string, num int, c *circuit.Circuit) error {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return errorAtf(num, "component line too short: %q", line)
	}
	ref := fields[0]
	if ref == "" {
		return errorAt(num, "empty ref designator")
	}
	prefix := strings.ToUpper(string(ref[0]))

	switch prefix {
	case "R", "C", "L":
		if len(fields) < 4 {
			return errorAtf(num, "%s needs 2 nodes and a value", prefix)
		}
		c.Components = append(c.Components, circuit.Component{
			Ref:   ref,
			Kind:  twoTerminalKind(prefix),
			Nodes: []string{fields[1], fields[2]},
			Value: fields[3],
		})
		return nil

	case "V", "I":
		return parseSource(line, num, ref, sourceKind(prefix), c)

	case "X":
		if len(fields) < 3 {
			return errorAt(num, "X needs at least 1 node and a model name")
		}
		nodes := append([]string(nil), fields[1:len(fields)-1]...)
		model := fields[len(fields)-1]
		c.Components = append(c.Components, circuit.Component{
			Ref:   ref,
			Kind:  "subcircuit",
			Nodes: nodes,
			Model: model,
		})
		return nil

	case "E", "G":
		// Voltage- or current-controlled source (linear): 4 nodes + gain.
		// Format: E<n> n+ n- nc+ nc- <gain>. Stored generically; the default
		// emitter writes "<ref> <nodes...> <value>" which round-trips this
		// shape exactly. The schematic editor doesn't render controlled
		// sources yet (DESIGN.md §6.1 doesn't call them out) but the engine
		// runs them natively.
		if len(fields) < 6 {
			return errorAt(num, prefix+" needs 4 nodes and a gain")
		}
		c.Components = append(c.Components, circuit.Component{
			Ref:   ref,
			Kind:  controlledKind(prefix),
			Nodes: append([]string(nil), fields[1:5]...),
			Value: fields[5],
		})
		return nil

	case "F", "H":
		// Current-controlled source: 2 nodes + controlling V-source ref +
		// gain. Format: F<n> n+ n- vctrl <gain>.
		if len(fields) < 5 {
			return errorAt(num, prefix+" needs 2 nodes, a controlling source, and a gain")
		}
		c.Components = append(c.Components, circuit.Component{
			Ref:   ref,
			Kind:  controlledKind(prefix),
			Nodes: append([]string(nil), fields[1:3]...),
			Model: fields[3],
			Value: fields[4],
		})
		return nil

	default:
		return ErrUnsupported{Line: num, Refdesig: prefix}
	}
}

func controlledKind(prefix string) string {
	switch prefix {
	case "E":
		return "vcvs"
	case "G":
		return "vccs"
	case "F":
		return "cccs"
	case "H":
		return "ccvs"
	}
	return "controlled_source"
}

func twoTerminalKind(prefix string) string {
	switch prefix {
	case "R":
		return "resistor"
	case "C":
		return "capacitor"
	case "L":
		return "inductor"
	}
	return "unknown"
}

func sourceKind(prefix string) string {
	if prefix == "V" {
		return "voltage_source"
	}
	return "current_source"
}

// parseSource handles V/I lines. The grammar accepted (per ngspice/LTspice
// convention):
//
//	V1 n+ n- [DC <value>] [AC <mag> [<phase>]] [SIN(...)|PULSE(...)|...]
//	V1 n+ n- <bare-numeric>                            // bare DC shorthand
//
// At least one of DC, AC, or a transient-spec must be present. Tokens may
// appear in any order; conflicts (two transient specs, two DC values) error.
// SourceSpec.Mode reflects the primary stimulus: "dc" if DC was the only
// thing, "sin" / "pulse" / etc. when a transient spec is present, "ac" when
// only AC stimulus was given. SourceSpec.AC is populated from the AC token.
//
// Anything outside this grammar returns ErrUnsupported. Unsupported transient
// specs (PULSE/PWL/SFFM/etc.) also return ErrUnsupported until a later
// milestone wires them in (DESIGN.md §7).
func parseSource(line string, num int, ref, kind string, c *circuit.Circuit) error {
	expanded := strings.ReplaceAll(strings.ReplaceAll(line, "(", " ( "), ")", " ) ")
	fields := strings.Fields(expanded)
	if len(fields) < 4 {
		return errorAt(num, "source needs 2 nodes and a value")
	}

	comp := circuit.Component{
		Ref:   ref,
		Kind:  kind,
		Nodes: []string{fields[1], fields[2]},
	}
	spec := fields[3:]
	src := &circuit.SourceSpec{}
	i := 0

	// Bare-value shorthand: "V1 in 0 5" → DC 5. Only honoured when the spec
	// is exactly one non-keyword token; otherwise the caller meant something
	// else and a missing keyword is an error.
	if len(spec) == 1 && !looksLikeKeyword(spec[0]) {
		src.Mode = "dc"
		src.Params = map[string]string{"value": spec[0]}
		comp.Source = src
		c.Components = append(c.Components, comp)
		return nil
	}

	for i < len(spec) {
		head := strings.ToUpper(spec[i])
		switch head {
		case "DC":
			if i+1 >= len(spec) {
				return errorAt(num, "DC requires a value")
			}
			if src.Params == nil {
				src.Params = map[string]string{}
			}
			if _, dup := src.Params["value"]; dup && src.Mode == "dc" {
				return errorAt(num, "duplicate DC value")
			}
			src.Params["value"] = spec[i+1]
			if src.Mode == "" {
				src.Mode = "dc"
			}
			i += 2

		case "AC":
			// AC <mag> [<phase>]; phase is optional and only consumed when
			// the next token does not look like another keyword.
			if i+1 >= len(spec) {
				return errorAt(num, "AC requires a magnitude")
			}
			ac := &circuit.ACSpec{Magnitude: spec[i+1]}
			i += 2
			if i < len(spec) && !looksLikeKeyword(spec[i]) && spec[i] != "(" {
				ac.Phase = spec[i]
				i++
			}
			src.AC = ac

		case "SIN", "PULSE", "SFFM", "PWL":
			closeIdx, args, err := readParenList(spec, i, num, head)
			if err != nil {
				return err
			}
			if src.Mode != "" && src.Mode != "dc" {
				return errorAt(num, "source has more than one transient spec")
			}
			params, mode, perr := parseTransientArgs(head, args, num)
			if perr != nil {
				return perr
			}
			// Any transient spec supersedes a bare DC: ngspice treats DC as
			// the offset under transient analysis when both are given.
			// Preserve any DC value alongside in Params["dc"] so round-trip
			// recovers it.
			if src.Mode == "dc" && src.Params != nil {
				if dcv, ok := src.Params["value"]; ok {
					params["dc"] = dcv
				}
			}
			src.Mode = mode
			src.Params = params
			i = closeIdx + 1
			// PWL accepts optional `r=<offset>` and `td=<delay>` trailing
			// tokens (ngspice qualifiers). Consume them in either order so a
			// round-trip preserves the repeat + time-shift hints. `td=` is
			// stored under the user-facing `td` key so the inspector / *+
			// meta path picks it up; `r=` stays under the internal `_repeat`
			// marker.
			if mode == "pwl" {
				for i < len(spec) {
					low := strings.ToLower(spec[i])
					if strings.HasPrefix(low, "r=") {
						params["_repeat"] = strings.TrimPrefix(low, "r=")
						i++
						continue
					}
					if strings.HasPrefix(low, "td=") {
						params["td"] = strings.TrimPrefix(low, "td=")
						i++
						continue
					}
					break
				}
			}

		default:
			return ErrUnsupported{Line: num, SourceMode: head}
		}
	}

	if src.Mode == "" && src.AC == nil {
		return errorAt(num, "source needs DC, AC, or a transient spec")
	}
	if src.Mode == "" {
		// AC-only stimulus.
		src.Mode = "ac"
	}
	comp.Source = src
	c.Components = append(c.Components, comp)
	return nil
}

// readParenList scans a "( ... )" group starting at spec[i+1] (where spec[i]
// is the keyword), returning the index of the closing paren and the inner
// tokens.
func readParenList(spec []string, i, num int, head string) (int, []string, error) {
	if i+1 >= len(spec) || spec[i+1] != "(" {
		return 0, nil, errorAt(num, head+" requires ( ... )")
	}
	for j := i + 2; j < len(spec); j++ {
		if spec[j] == ")" {
			return j, spec[i+2 : j], nil
		}
	}
	return 0, nil, errorAt(num, head+" missing )")
}

// parseTransientArgs maps the inner tokens of a transient-spec parenthesised
// list onto the canonical Params schema for that mode. For PWL the args are
// (t,v) pairs and end up in Params["points"] using the same `t:v;t:v;...`
// encoding the synthesizers use.
func parseTransientArgs(head string, args []string, num int) (params map[string]string, mode string, err error) {
	switch head {
	case "SIN":
		if len(args) == 0 {
			return nil, "", errorAt(num, "SIN needs at least offset/ampl/freq")
		}
		names := []string{"offset", "ampl", "freq", "td", "damp", "phase"}
		params = map[string]string{}
		for k, a := range args {
			if k >= len(names) {
				return nil, "", errorAt(num, "SIN has too many args")
			}
			if names[k] == "ampl" {
				a = peakToVppSpiceArg(a)
			}
			params[names[k]] = a
		}
		return params, "sin", nil

	case "PULSE":
		if len(args) == 0 {
			return nil, "", errorAt(num, "PULSE needs at least v1/v2/td")
		}
		names := []string{"v1", "v2", "td", "tr", "tf", "pw", "per"}
		params = map[string]string{}
		for k, a := range args {
			if k >= len(names) {
				return nil, "", errorAt(num, "PULSE has too many args")
			}
			params[names[k]] = a
		}
		return params, "pulse", nil

	case "SFFM":
		if len(args) == 0 {
			return nil, "", errorAt(num, "SFFM needs at least offset/ampl/fc")
		}
		names := []string{"offset", "ampl", "fc", "mdi", "fm"}
		params = map[string]string{}
		for k, a := range args {
			if k >= len(names) {
				return nil, "", errorAt(num, "SFFM has too many args")
			}
			if names[k] == "ampl" {
				a = peakToVppSpiceArg(a)
			}
			params[names[k]] = a
		}
		return params, "sffm", nil

	case "PWL":
		if len(args)%2 != 0 {
			return nil, "", errorAt(num, "PWL needs an even number of args (t v t v ...)")
		}
		if len(args) == 0 {
			return map[string]string{"points": ""}, "pwl", nil
		}
		// Concatenate as `t:v;t:v;...` so the inspector + synthesizer share
		// one canonical points encoding (see waveforms.go formatPointsString).
		var sb strings.Builder
		for k := 0; k < len(args); k += 2 {
			if k > 0 {
				sb.WriteByte(';')
			}
			sb.WriteString(args[k])
			sb.WriteByte(':')
			sb.WriteString(args[k+1])
		}
		return map[string]string{"points": sb.String()}, "pwl", nil
	}
	return nil, "", errorAt(num, "unknown transient spec "+head)
}

// looksLikeKeyword identifies tokens that begin a source-spec section so the
// optional-phase consumer in the AC handler doesn't accidentally swallow a
// following SIN/DC/etc. word as a phase value.
func looksLikeKeyword(s string) bool {
	up := strings.ToUpper(s)
	switch up {
	case "PULSE", "PWL", "SFFM", "EXP", "AM", "FM", "AC", "SIN", "DC":
		return true
	}
	return false
}

// parseStructuredMeta routes one *+ line to the right map. Lines whose first
// key is `waveform=` go to the waveform-meta dispatcher (m10); everything
// else flows through the original layout-meta path. Lines that mix layout
// keys with `waveform=` are split: layout keys land in the layouts map, the
// waveform record goes to waveformMeta keyed on the same ref.
func parseStructuredMeta(line string, num int, layouts map[string]circuit.Layout, waveformMeta map[string]waveformMetaRecord) error {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "*+"))
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return errorAt(num, "structured metadata needs ref and at least one key=value")
	}
	hasWaveform := false
	for _, f := range fields[1:] {
		if strings.HasPrefix(strings.ToLower(f), "waveform=") {
			hasWaveform = true
			break
		}
	}
	if hasWaveform {
		return parseWaveformMeta(line, num, waveformMeta)
	}
	return parseLayoutMeta(line, num, layouts)
}

// parseWaveformMeta consumes one `*+ <ref> waveform=<mode> k=v ...` line and
// stashes the record in the waveformMeta map. The mode is required; unknown
// modes are accepted (so future modes don't break older binaries) but they
// will silently overwrite SourceSpec.Mode on the matching component.
func parseWaveformMeta(line string, num int, waveformMeta map[string]waveformMetaRecord) error {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "*+"))
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return errorAt(num, "waveform metadata needs ref and waveform=<mode>")
	}
	ref := fields[0]
	rec := waveformMetaRecord{kv: map[string]string{}}
	for _, kv := range fields[1:] {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			continue
		}
		key := strings.ToLower(kv[:eq])
		val := unescapeMetaValue(kv[eq+1:])
		if key == "waveform" {
			rec.mode = strings.ToLower(val)
			continue
		}
		rec.kv[key] = val
	}
	if rec.mode == "" {
		return errorAt(num, "waveform metadata missing waveform=<mode>")
	}
	waveformMeta[ref] = rec
	return nil
}

// parseLayoutMeta parses one *+ comment line into the layouts map.
// Format: *+ <ref> pos=(x,y) rot=N mirror=true|false [probe=name]
func parseLayoutMeta(line string, num int, layouts map[string]circuit.Layout) error {
	rest := strings.TrimSpace(strings.TrimPrefix(line, "*+"))
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return errorAt(num, "layout metadata needs ref and at least one key=value")
	}
	ref := fields[0]
	layout := circuit.Layout{}
	for _, kv := range fields[1:] {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			continue
		}
		key := strings.ToLower(kv[:eq])
		val := kv[eq+1:]
		switch key {
		case "pos":
			x, y, err := parsePos(val)
			if err != nil {
				return errorAtf(num, "bad pos= value: %v", err)
			}
			layout.X = x
			layout.Y = y
		case "rot":
			r, err := strconv.Atoi(val)
			if err != nil {
				return errorAtf(num, "bad rot= value: %v", err)
			}
			layout.Rot = r
		case "mirror":
			layout.Mirror = strings.EqualFold(val, "true")
		case "probe":
			// Informational only in milestone 1; preserved through round-trip
			// only via the matching .SAVE directive.
		default:
			// Unknown key — ignore (forward compat per DESIGN.md §5.2).
		}
	}
	layouts[ref] = layout
	return nil
}

func parsePos(v string) (int, int, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "(")
	v = strings.TrimSuffix(v, ")")
	parts := strings.SplitN(v, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected (x,y), got %q", v)
	}
	x, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, err
	}
	y, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, err
	}
	return x, y, nil
}
