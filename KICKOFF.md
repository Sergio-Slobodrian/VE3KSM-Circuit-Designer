# Kickoff prompt

Milestone 1 (data model + netlist round-trip) was pre-implemented in this
packaging build. **Before pasting the prompt below into your AI assistant,
verify the implementation builds and tests pass on your machine:**

```sh
cd backend
go test ./...
go vet ./...
```

Both should be clean. If `go test ./...` reports failures, fix or report
back rather than handing off — the data model is the spine of every
subsequent milestone, and a broken parser will compound.

If everything is green, copy the prompt block below (between the `---`
separators) into Claude Code (or Cursor / Aider / etc.) to start
milestone 2.

---

You are taking over development of Circuit Lab, a circuit simulation app
for audio amp and amateur radio filter design.

## Required reading, in order

1. `README.md` — orient yourself.
2. `DESIGN.md` — full architecture, data model, view contracts, engine
   integration, project conventions.
3. `mockups/01_schematic_editor.html` through `mockups/04_netlist.html` —
   the five tab views you'll be implementing. Open them in a browser.
4. `backend/internal/circuit/types.go` and `backend/internal/netlist/` —
   milestone 1 is already implemented. Read this code to understand the
   in-memory model the rest of the system renders against.
5. `examples/preamp_12ax7.cir` — the canonical fixture circuit referenced
   throughout `DESIGN.md` and the netlist tests.

## Non-negotiables

- Go 1.22+ on the backend, **no cgo**. ngspice is invoked as a subprocess.
- React on the frontend, **plain JavaScript only in the browser** — no
  TypeScript files in the shipped bundle. Vite is configured for this.
- SPICE netlist (ngspice 42 dialect) is the canonical interchange format.
  Layout metadata lives in structured *+ comments — see DESIGN.md §5.
- The simulation engine is **always** an external `ngspice` subprocess.
  Never link `libngspice`, never reimplement the simulator.
- The data model in `internal/circuit/types.go` and the public surface of
  `internal/netlist` are stable. Extend, don't restructure.

## Milestone 2 — ngspice subprocess adapter

Implement only this. Stop and report when done.

### Tasks

1. `backend/internal/engine/engine.go` — define the public interface:

   ```go
   type Engine interface {
       Run(ctx context.Context, c *circuit.Circuit, a circuit.Analysis) (<-chan Frame, error)
       Cancel(ctx context.Context, runID string) error
       Close() error
   }
   type Frame struct { ... }   // streaming result row
   ```

2. `backend/internal/engine/ngspice.go` — implementation. Spawns
   `ngspice -p` (interactive control mode), feeds it the netlist via
   `netlist.Emit`, drives the analysis with `source` / `run`, and
   streams results back via `wrdata` to a temp file the goroutine tails.

3. Cancellation: graceful `quit` over stdin with 200 ms deadline,
   then `SIGKILL`. Restart the subprocess for the next request.

4. Convergence errors: parse ngspice's stderr for "no convergence"
   patterns and return a structured error with the offending node
   (DESIGN.md §9.4).

5. `backend/internal/engine/ngspice_test.go` — at minimum: load
   `examples/preamp_12ax7.cir`, run `.TRAN 1u 5m uic`, assert the
   `vout` channel has the expected number of samples and the peak
   amplitude is in a sane range. Test is allowed to skip when ngspice
   is missing from PATH.

### Acceptance criteria

- `cd backend && go test ./internal/engine/...` passes when ngspice
  is installed.
- A streaming `.TRAN` run on the preamp fixture produces samples
  visible to the caller before the simulation finishes (verify by
  reading more than one Frame from the channel before the channel
  closes).
- Cancelling mid-run via the returned `context.CancelFunc` kills the
  subprocess and returns within ~250 ms.
- No goroutine leaks (run with `-race`).

### Stop after milestone 2

Report back with: the engine interface as committed, test results,
and any deviations from DESIGN.md §9 with reasoning. Do not start the
HTTP/WS API or the schematic editor — those are milestones 3 and 4.

## Style guide pointers in `DESIGN.md`

- §4 — data model field names (already locked in by milestone 1)
- §9 — engine integration rules
- §11 — JSON-over-WebSocket protocol (for milestone 3)
- §12 — project conventions
- §13 — full milestone roadmap

---

End of prompt.
