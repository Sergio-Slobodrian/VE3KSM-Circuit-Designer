package engine

import (
	"context"
	"fmt"

	"circuit-designer/backend/internal/circuit"
)

// Engine wraps a SPICE simulation engine. The default implementation runs
// ngspice as a subprocess in interactive mode (see DESIGN.md §9). Construct
// with New or NewWithOptions.
type Engine interface {
	// Run starts an analysis on c and returns a channel that receives
	// streaming Frames. The channel closes when the run completes (success,
	// error, or cancellation). Mid-run failures are reported as one final
	// Frame with Err non-nil, immediately before the channel closes.
	//
	// The context governs the run lifetime. Cancelling ctx triggers a
	// graceful "quit" over stdin; if the subprocess does not exit within
	// 200 ms it is SIGKILLed (DESIGN.md §9.3).
	Run(ctx context.Context, c *circuit.Circuit, a circuit.Analysis) (<-chan Frame, error)

	// Cancel terminates an in-flight run by ID. It blocks until the run has
	// released its resources or until ctx is done.
	Cancel(ctx context.Context, runID string) error

	// Close cancels every active run and waits for them to release. Subsequent
	// Run calls succeed (the engine is reusable); Close is idempotent.
	Close() error
}

// Frame is one streaming sample from a simulation. The independent variable
// X is time (seconds) for .TRAN, frequency (Hz) for .AC, the swept source
// value for .DC. Values is keyed by the user-facing name from circuit.Probe
// — typically the SPICE node name.
//
// If Err is non-nil, this is a terminal error frame: no other fields carry
// data and the channel closes immediately afterwards.
type Frame struct {
	RunID  string             `json:"run_id"`
	Index  int                `json:"index"`
	X      float64            `json:"x"`
	Values map[string]float64 `json:"values,omitempty"`
	Err    *RunError          `json:"err,omitempty"`
}

// RunError is the structured form of a simulation failure. Convergence errors
// carry the offending node and last-attempted voltage so the schematic editor
// can highlight them (DESIGN.md §9.4).
type RunError struct {
	Kind        string  `json:"kind"`                  // "no_convergence", "subprocess", "spawn", "internal"
	Node        string  `json:"node,omitempty"`        // for no_convergence
	LastVoltage float64 `json:"last_voltage,omitempty"`
	Iteration   int     `json:"iteration,omitempty"`
	Message     string  `json:"message"`
	Hint        string  `json:"hint,omitempty"`
}

// Error implements the error interface.
func (e *RunError) Error() string {
	if e == nil {
		return ""
	}
	if e.Node != "" {
		return fmt.Sprintf("ngspice %s at node %s: %s", e.Kind, e.Node, e.Message)
	}
	return fmt.Sprintf("ngspice %s: %s", e.Kind, e.Message)
}

// Options configures the ngspice engine.
type Options struct {
	// NgspicePath overrides the executable name. Default: "ngspice" (looked
	// up on $PATH).
	NgspicePath string

	// WorkDir is the directory ngspice runs in. Relative .LIB and .INCLUDE
	// references in the circuit resolve against this directory.
	//
	// If empty, each run uses its own private temp directory and any
	// external library file the circuit references must be locatable from
	// there (typically by absolute path).
	WorkDir string
}
