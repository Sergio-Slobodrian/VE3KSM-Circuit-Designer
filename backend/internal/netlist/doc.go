// Package netlist parses SPICE source into circuit.Circuit values and emits
// circuit.Circuit values back to SPICE source. Round-trip is the primary
// contract — see DESIGN.md §5.4.
//
// Target dialect is ngspice 42 (see DESIGN.md §5.1). Layout metadata is
// preserved in structured *+ comments after .END (see §5.2). Whitespace is
// normalized on emit (§5.3); a parse-emit cycle is not byte-equivalent but
// must be data-equivalent.
//
// Public surface:
//
//	func Parse(r io.Reader) (*circuit.Circuit, error)
//	func Emit(c *circuit.Circuit, w io.Writer) error
//	type ErrUnsupported struct { ... }
//	type ParseError    struct { ... }
//
// Translate (cross-dialect emission for LTspice / KiCad / Berkeley SPICE 3)
// is deferred to milestone 2+; see DESIGN.md §10.5.
package netlist
