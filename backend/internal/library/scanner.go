package library

import (
	"bufio"
	"fmt"
	"strings"
)

// Subckt is one .SUBCKT definition discovered in a SPICE library body.
type Subckt struct {
	Name    string
	Pins    []string
	// Params records the optional `+ PARAMS: KEY=VALUE` block on the line
	// following the .SUBCKT header. Order is preserved so the inspector can
	// render them in source order.
	Params []ParamDef
}

// ParamDef is one PARAMS entry, e.g. `MU=100`.
type ParamDef struct {
	Name    string
	Default string
}

// ScanSubcircuits reads a SPICE library body and returns every .SUBCKT it
// finds. Continuation lines (`+ ...`) are folded into their parent. Anything
// outside a `.SUBCKT … .ENDS` block is ignored, which matches the relaxed
// shape of real-world third-party `.lib` files.
//
// The scanner is intentionally lenient: it recognises only `.SUBCKT`, `.ENDS`,
// and `+ PARAMS:`. Unknown directives inside a subcircuit body do not cause
// failure — they are part of the device description and reproduced verbatim
// when the .lib is later sourced by ngspice.
func ScanSubcircuits(body string) ([]Subckt, error) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)

	var (
		subs    []Subckt
		current *Subckt
		// pending holds the most recent header line (.SUBCKT or `+`) so a
		// subsequent `+ …` continuation can be appended to it.
		pendingHeader bool
	)

	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		// Comments — full-line `*` and inline `;` after a directive — do not
		// interrupt header continuation, so we strip them but preserve the
		// pending state.
		if strings.HasPrefix(trimmed, "*") {
			continue
		}
		if i := strings.IndexByte(trimmed, ';'); i >= 0 {
			trimmed = strings.TrimSpace(trimmed[:i])
			if trimmed == "" {
				continue
			}
		}

		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, ".SUBCKT"):
			if current != nil {
				return nil, fmt.Errorf("library: nested .SUBCKT %q inside %q", trimmed, current.Name)
			}
			tokens := splitFields(trimmed[len(".SUBCKT"):])
			if len(tokens) < 1 {
				return nil, fmt.Errorf("library: .SUBCKT without a name: %q", raw)
			}
			name := tokens[0]
			pins := tokens[1:]
			current = &Subckt{Name: name, Pins: append([]string(nil), pins...)}
			pendingHeader = true
		case strings.HasPrefix(upper, ".ENDS"):
			if current == nil {
				return nil, fmt.Errorf("library: stray .ENDS: %q", raw)
			}
			subs = append(subs, *current)
			current = nil
			pendingHeader = false
		case strings.HasPrefix(trimmed, "+"):
			if current == nil || !pendingHeader {
				continue
			}
			// Continuation of the .SUBCKT header. Two flavours:
			//   + <pin> <pin> …                — extend pin list
			//   + PARAMS: KEY=VAL KEY=VAL …    — declare parameters
			cont := strings.TrimSpace(trimmed[1:])
			if cont == "" {
				continue
			}
			if upperCont := strings.ToUpper(cont); strings.HasPrefix(upperCont, "PARAMS:") {
				rest := strings.TrimSpace(cont[len("PARAMS:"):])
				current.Params = append(current.Params, parseParamAssignments(rest)...)
			} else if strings.Contains(cont, "=") {
				// Bare `+ KEY=VAL` (no PARAMS: prefix) — also accepted, since
				// some Koren-style .lib files write the second + line as a
				// continuation of the params without repeating PARAMS:.
				current.Params = append(current.Params, parseParamAssignments(cont)...)
			} else {
				current.Pins = append(current.Pins, splitFields(cont)...)
			}
		default:
			// Body element — pin list and PARAMS: continuations are no longer
			// possible after the first body line.
			pendingHeader = false
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("library: read .lib: %w", err)
	}
	if current != nil {
		return nil, fmt.Errorf("library: unterminated .SUBCKT %q", current.Name)
	}
	return subs, nil
}

// splitFields splits on runs of whitespace. Equivalent to strings.Fields but
// inlined so future changes to handle quoted values stay localised.
func splitFields(s string) []string {
	return strings.Fields(s)
}

// parseParamAssignments parses a sequence of `KEY=VALUE` tokens, skipping
// stray bare tokens. Values may contain SPICE engineering suffixes ("2.4P")
// — those round-trip as opaque strings.
func parseParamAssignments(s string) []ParamDef {
	out := []ParamDef{}
	for _, tok := range splitFields(s) {
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(tok[:eq])
		val := strings.TrimSpace(tok[eq+1:])
		if name == "" {
			continue
		}
		out = append(out, ParamDef{Name: name, Default: val})
	}
	return out
}
