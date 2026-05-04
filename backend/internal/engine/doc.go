// Package engine adapts ngspice as a subprocess. The engine receives a
// circuit.Circuit, asks netlist.Emit to serialize it, feeds it to ngspice
// in interactive control mode (`ngspice -p`), and streams the result rows
// emitted by `wrdata`.
//
// Implementation rules from DESIGN.md §9:
//
//   - Use `ngspice -p`, not batch mode.
//   - No cgo. ngspice is always exec'd as a subprocess.
//   - Cancellation: write "quit" to stdin with a 200 ms grace, then
//     SIGKILL. The grace is enforced by exec.Cmd.WaitDelay.
//   - Result transport: drive ngspice with `wrdata` to write a known set
//     of vectors as a single-scale ASCII table; the engine reads that
//     file row-by-row and streams Frames to the caller.
//   - Convergence errors return structured RunError data (see §9.4).
//
// Public surface:
//
//	type Engine interface {
//	    Run(ctx context.Context, c *circuit.Circuit, a circuit.Analysis) (<-chan Frame, error)
//	    Cancel(ctx context.Context, runID string) error
//	    Close() error
//	}
//	type Frame    struct{ ... }
//	type RunError struct{ ... }
//
// Construct the default ngspice-backed implementation with engine.New() or
// engine.NewWithOptions(engine.Options{...}); the latter accepts a WorkDir
// (for resolving .LIB / .INCLUDE references) and an NgspicePath override.
package engine
