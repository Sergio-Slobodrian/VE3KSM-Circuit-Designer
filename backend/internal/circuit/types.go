package circuit

// Circuit is the top-level container. One in-memory instance corresponds to
// one schematic, which corresponds to one .cir source file.
//
// Field names and JSON tags below are the contract the frontend depends on
// (see DESIGN.md §4). Do not rename without updating the frontend in lockstep.
type Circuit struct {
	Title      string       `json:"title"`
	Comments   []string     `json:"comments"`
	Libraries  []LibraryRef `json:"libraries"`
	Parameters []Param      `json:"parameters"`
	Components []Component  `json:"components"`
	Wires      []Wire       `json:"wires"`
	Probes     []Probe      `json:"probes"`
	Analyses   []Analysis   `json:"analyses"`
}

// Component is one R, C, L, V, I, X (subcircuit), Q, M, J, or D element.
// Milestone 1 implements R, C, L, V, I, X. Active devices are deferred.
type Component struct {
	Ref    string            `json:"ref"`
	Kind   string            `json:"kind"`
	Nodes  []string          `json:"nodes"`
	Value  string            `json:"value"`
	Model  string            `json:"model,omitempty"`
	Source *SourceSpec       `json:"source,omitempty"`
	Layout Layout            `json:"layout"`
	Params map[string]string `json:"params,omitempty"`
}

// SourceSpec is the waveform on a voltage or current source. Milestone 1
// implements Mode "dc" and "sin"; everything else lowers later.
type SourceSpec struct {
	Mode   string            `json:"mode"`
	Params map[string]string `json:"params"`
	AC     *ACSpec           `json:"ac,omitempty"`
}

// ACSpec is the small-signal stimulus magnitude/phase used by .AC analyses.
type ACSpec struct {
	Magnitude string `json:"magnitude"`
	Phase     string `json:"phase"`
}

// Wire is a graphical edge in the schematic. The simulator only sees nodes;
// wires are an editor concern. Multiple wires that share endpoints collapse
// into a single SPICE node at netlist time.
type Wire struct {
	From Point  `json:"from"`
	To   Point  `json:"to"`
	Node string `json:"node"`
}

// Point is a 2D integer coordinate on the schematic grid.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Probe is a measurement attachment to a node. Probes survive across
// analyses and feed every instrument view (scope, spectrum, network).
type Probe struct {
	Name   string `json:"name"`
	Node   string `json:"node"`
	Kind   string `json:"kind"`
	Layout Layout `json:"layout"`
}

// Analysis is one .TRAN / .AC / .DC / .OP / .NOISE directive.
type Analysis struct {
	Kind    string            `json:"kind"`
	Args    []string          `json:"args"`
	Enabled bool              `json:"enabled"`
	Options map[string]string `json:"options,omitempty"`
}

// LibraryRef is a .LIB include. Resolved against the project library path.
type LibraryRef struct {
	Path    string `json:"path"`
	Section string `json:"section,omitempty"`
}

// Param is a .PARAM definition. Value is the raw expression string; we do
// not evaluate it.
type Param struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Layout is schematic-editor metadata. Round-trips through the netlist as
// structured *+ comments after .END (see DESIGN.md §5.2).
type Layout struct {
	X      int  `json:"x"`
	Y      int  `json:"y"`
	Rot    int  `json:"rot"`
	Mirror bool `json:"mirror"`
}
