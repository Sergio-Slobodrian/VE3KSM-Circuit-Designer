# Circuit Lab — design

This document is the spec the implementation should follow. It captures the
decisions that have already been made; the open questions are explicitly
marked.

## 1. Goals and non-goals

### Goals
- Interactive circuit design and simulation aimed at audio amplifier and
  amateur radio filter work.
- Schematic editing, signal injection at any node, time-domain (scope) and
  frequency-domain (spectrum, network analyzer) views of the response.
- Round-trippable SPICE netlist as the canonical format. Industry-standard
  vendor models (`.lib` files) drop in unchanged.
- Vacuum tube, BJT, FET, and MOSFET support out of the box, plus the
  passives (R, L, C).

### Non-goals
- Beating LTspice or ngspice on the simulation engine. We are not writing a
  simulator. ngspice has fifty years of convergence work behind it.
- PCB layout, routing, or any physical-design feature.
- Mixed-signal or digital simulation. Analog only.
- Cloud collaboration, multi-user editing.

## 2. Tech stack

| Layer       | Choice                                  | Why                              |
|-------------|-----------------------------------------|----------------------------------|
| Engine      | `ngspice` 42+ as subprocess             | Mature, BSD-licensed, SPICE compatible |
| Backend     | Go 1.22+, no cgo                        | Memory safety, easy cross-compile |
| Transport   | HTTP REST + WebSocket                   | REST for circuit CRUD, WS for streaming sim results |
| Frontend    | React via Vite                          | Standard, well-supported          |
| Browser JS  | Plain JavaScript only (.jsx, no .ts)    | User constraint                   |
| Schematic   | SVG + React                             | Free hit-testing, accessibility   |
| Scope       | uPlot                                   | 60fps streaming waveforms         |
| Spectrum    | Plotly                                  | Interactive log-axis, marker UX   |
| Bode/NA     | uPlot                                   | Cheap, two stacked plots          |
| State       | Zustand                                 | Simple store, undo/redo support   |

## 3. Architecture

```
┌────────────────────┐        WebSocket (sim results, streaming)
│   React frontend   │  ◄──────────────────────────────────────┐
│   - Schematic      │        REST (circuits, libraries, ops)  │
│   - Scope          │  ◄──────────────────────────────────────┤
│   - Spectrum       │                                          │
│   - Network anlz   │                                          │
│   - Netlist editor │                                          │
└────────────────────┘                                          │
                                                                │
                                                  ┌─────────────┴────────────┐
                                                  │       Go backend         │
                                                  │   ┌──────────────────┐   │
                                                  │   │ HTTP/WS handlers │   │
                                                  │   └────────┬─────────┘   │
                                                  │            │             │
                                                  │   ┌────────┴─────────┐   │
                                                  │   │ Circuit (model)  │   │
                                                  │   └────────┬─────────┘   │
                                                  │            │             │
                                                  │   ┌────────┴─────────┐   │
                                                  │   │ Netlist parser/  │   │
                                                  │   │ emitter          │   │
                                                  │   └────────┬─────────┘   │
                                                  │            │             │
                                                  │   ┌────────┴─────────┐   │
                                                  │   │ Engine adapter   │   │
                                                  │   └────────┬─────────┘   │
                                                  └────────────┼─────────────┘
                                                               │
                                                               │ stdin/stdout
                                                               ▼
                                                    ┌─────────────────────┐
                                                    │   ngspice (subproc) │
                                                    └─────────────────────┘
```

The `Circuit` package is the spine. The schematic editor, the netlist editor,
and every simulation request all read or write `Circuit` values. The
simulation engine adapter takes a `Circuit`, asks the netlist emitter to
serialize it, hands the result to ngspice, parses the raw output, and emits
a `Result` payload.

## 4. Data model

These are the canonical Go types. JSON tags are the wire format the frontend
depends on — do not rename.

```go
package circuit

// Circuit is the top-level container. One in-memory instance equals one
// schematic equals one .cir file.
type Circuit struct {
    Title       string         `json:"title"`
    Comments    []string       `json:"comments"`        // free-text header comments
    Libraries   []LibraryRef   `json:"libraries"`
    Parameters  []Param        `json:"parameters"`
    Components  []Component    `json:"components"`
    Wires       []Wire         `json:"wires"`
    Probes      []Probe        `json:"probes"`
    Analyses    []Analysis     `json:"analyses"`
}

// Component is one R, C, L, V, I, X, Q, M, J, D, etc.
type Component struct {
    Ref      string            `json:"ref"`        // e.g. "R1", "X1", "VBB"
    Kind     string            `json:"kind"`       // "resistor", "capacitor", "inductor",
                                                   // "voltage_source", "current_source",
                                                   // "subcircuit", "bjt", "fet", "mosfet",
                                                   // "diode", "tube"
    Nodes    []string          `json:"nodes"`      // ordered: e.g. ["plate","grid","cathode"] for a triode
    Value    string            `json:"value"`      // raw SPICE value or expression: "100k", "{R_PLATE}", "10n"
    Model    string            `json:"model,omitempty"` // model/subckt name for X, Q, M, J, D, tubes
    Source   *SourceSpec       `json:"source,omitempty"` // populated for V/I sources
    Layout   Layout            `json:"layout"`     // schematic-editor metadata
    Params   map[string]string `json:"params,omitempty"` // per-instance params: {"L":"1.99m","IC":"0"}
}

// SourceSpec is the waveform on a voltage or current source. It maps 1:1 to
// the SPICE source statement variants. See §7 for waveform semantics.
type SourceSpec struct {
    Mode    string            `json:"mode"`    // "dc", "sin", "pulse", "pwl", "sffm",
                                                // "noise", "chirp", "twotone", "am", "fm", "arb"
    Params  map[string]string `json:"params"`  // mode-specific: {"freq":"1k","ampl":"0.25"}
    AC      *ACSpec           `json:"ac,omitempty"`  // for AC analysis stimulus
}

type ACSpec struct {
    Magnitude string `json:"magnitude"` // "1"
    Phase     string `json:"phase"`     // "0"
}

// Wire is a graphical edge in the schematic. The simulator only sees nodes;
// wires are an editor concern. Multiple wires that share endpoints collapse
// into a single SPICE node at netlist time.
type Wire struct {
    From  Point  `json:"from"`
    To    Point  `json:"to"`
    Node  string `json:"node"`     // assigned node name, e.g. "in_ac"
}

type Point struct {
    X int `json:"x"`
    Y int `json:"y"`
}

// Probe is a measurement attachment to a node. Probes are first-class —
// they survive across analyses and feed the scope/spectrum/network views
// independently of how the circuit is being driven.
type Probe struct {
    Name   string `json:"name"`     // user-visible label, e.g. "Vout"
    Node   string `json:"node"`     // SPICE node name
    Kind   string `json:"kind"`     // "voltage" or "current"
    Layout Layout `json:"layout"`
}

// Analysis is one .TRAN / .AC / .DC / .OP / .NOISE directive.
type Analysis struct {
    Kind     string            `json:"kind"`     // "tran", "ac", "dc", "op", "noise"
    Args     []string          `json:"args"`     // raw SPICE args: ["1u","5m","uic"]
    Enabled  bool              `json:"enabled"`  // false = commented out in netlist
    Options  map[string]string `json:"options,omitempty"`
}

// LibraryRef is a .LIB include. Resolved against the project's library path.
type LibraryRef struct {
    Path     string `json:"path"`
    Section  string `json:"section,omitempty"` // for .LIB <file> <section> form
}

type Param struct {
    Name  string `json:"name"`
    Value string `json:"value"` // expression, e.g. "250", "{Rload*2}"
}

// Layout is schematic-editor metadata. Round-trips through the netlist as
// structured comments — see §5.2.
type Layout struct {
    X      int  `json:"x"`
    Y      int  `json:"y"`
    Rot    int  `json:"rot"`    // 0, 90, 180, 270
    Mirror bool `json:"mirror"`
}
```

### Notes on field choices

- `Value` is a string, not a number. SPICE values carry engineering suffixes
  (`100k`, `1MEG`, `10n`, `4.7p`, `2.2u`) and may be parameter expressions
  (`{B_PLUS}`, `{Rload*2}`). Don't lose that by parsing too eagerly.
- `Nodes` is positional. The order matters: a BJT is `[collector, base,
  emitter, substrate]` in SPICE order; a triode subckt is `[plate, grid,
  cathode]`. The component-library entry (§8) defines the canonical order.
- `Source` is only populated for V/I sources. For everything else it's nil.
- `Probe.Node` is a string node name, not a wire reference. Wires are an
  editor concept; nodes are what SPICE sees.

## 5. SPICE format and round-trip

The netlist is the canonical format. The schematic editor and the netlist
editor are two views of the same underlying `Circuit`.

### 5.1. Dialect

Target ngspice 42. Specifically:

- Use `MEG` (not `M`) for mega — `M` is milli in SPICE. This is everyone's
  favorite footgun.
- `.AC dec <points-per-decade> <fstart> <fstop>` — points-per-decade form,
  not total points.
- `.TRAN <step> <stop>` with optional `uic` to skip operating-point.
- `.SAVE` for selecting which nodes get persisted to the raw file.
- `.PARAM` for design parameters; `.STEP PARAM` for sweeps.
- Behavioral sources (`B`-elements) should be supported in the parser even
  though the UI doesn't expose them yet.

### 5.2. Layout metadata in structured comments

The schematic editor needs positions, rotations, and mirror flags that
SPICE has no place for. We store them as comments that survive round-trip:

```
*+ R1 pos=(220,170) rot=90 mirror=false
*+ X1 pos=(290,130) rot=0  mirror=false
*+ V1 pos=(50,180)  rot=0  mirror=false probe=in_ac
```

Rules:
- The `*+` prefix is reserved for Circuit Lab metadata. Plain `*` comments
  are user free-text and pass through untouched.
- One line per component, identified by `Ref`.
- Unknown keys are preserved on round-trip even if the parser doesn't
  understand them. Future-compat.
- Layout comments live at the bottom of the file, after `.END`. ngspice
  ignores everything past `.END`, so this is safe.

### 5.3. Whitespace normalization

The emitter normalizes whitespace on round-trip. This means a parse-emit
cycle is **not** byte-equivalent — but the resulting `Circuit` after a
second parse must equal the first.

Rules:
- Tabs become spaces.
- Multiple spaces between tokens collapse to one, except inside trailing
  comments (which are preserved).
- Section header comments (`* --- supply ---`) survive untouched.
- Blank lines between sections survive.
- Trailing whitespace on lines is stripped.

### 5.4. Round-trip test

The reference test (`examples/preamp_12ax7.cir`):

```
parse(source) → c1
emit(c1)      → source'
parse(source')→ c2
assert deepEqual(c1, c2)
```

This is the contract. If you find yourself special-casing in either the
parser or emitter to make this pass, the data model is wrong.

## 6. The five tabs

Each tab is a view over the same `Circuit`. Mockups in `mockups/` are the
visual contract.

### 6.1 Schematic editor (`mockups/01_schematic_editor.html`)

Three columns: component palette, canvas, inspector.

- Canvas is SVG. Grid is a CSS background, not SVG.
- Components render as small SVG groups. The SVG path lives in the
  component-library entry (§8) so symbols are data, not code.
- Wires are drawn between component pins. Pin coordinates come from the
  rotated symbol bounding box.
- Selection state lives in Zustand. Multi-select, drag, rotate (R), mirror
  (F), delete (Del).
- Inspector switches based on selection: a source shows the signal
  generator panel (§7); a passive shows value + tolerance + footprint;
  an `X` (subcircuit) shows the model name plus per-instance params.

### 6.2 Scope (`mockups/02_scope_and_spectrum.html`, top half)

Time-domain probe display. Channels are display assignments over the probe
set; the same simulation result feeds all channels.

- Up to 4 channels (CH1–CH4), assignable to any probe.
- Per-channel: V/div, position, coupling (DC/AC/GND), invert, color.
- Time base: time/div, position, sample-rate readout.
- Trigger: source channel, slope, level, mode (auto/normal/single). In
  simulation, trigger is a display concern only — we have all the data
  already; trigger picks the window.
- Math channels: ratio, difference, product, integral, derivative — JS
  expression box that evaluates over the channel data client-side.
- Measurement bar: Vpp, Vrms, mean, frequency, period, phase per channel.

### 6.3 Spectrum analyzer (`mockups/02_scope_and_spectrum.html`, bottom half)

FFT-based. The transient run is the underlying data; the spectrum view runs
the FFT itself client-side or server-side (see §10.4).

- Frequency axis: log or linear; configurable start/stop.
- Amplitude: dBV reference, dB/div, log y-axis.
- Window: Hann (default), Hamming, Blackman-Harris, flat-top, rectangular.
- RBW is a function of the windowed FFT length. RBW ≥ 1/T_capture, so
  finer RBW means a longer transient run — surface this trade-off.
- Detector: peak, sample, RMS, average. RMS for power-correct, default.
- Trace modes: clear/write, max hold, min hold, average (with N count).
- Markers: M1 absolute, M2 delta from M1, peak-find, harmonic-tracking.
- THD/THD+N/SINAD/SNR readouts in the bottom panel. Harmonic table with
  level (dBV), dBc, %, and phase per harmonic.

### 6.4 Network analyzer (`mockups/03_network_analyzer.html`)

AC sweep. Bode plot (magnitude + phase stacked) is the primary view.

- Magnitude: dB scale. Phase: degrees.
- Stimulus: one V or I source designated as port 1; one probe as port 2.
- Auto-markers: `-3 dB find`, `peak find`, `-40 dB find`, `unity-gain
  find`, `phase margin`, `gain margin`.
- Group delay computed numerically from the phase trace (`-dφ/dω`),
  toggle in the side panel.
- Smith chart inset for `S11` (input reflection) when `Z₀` is set.
- VSWR derived from `S11` magnitude.
- Note in the UI when the circuit contains nonlinear devices: `.AC` is
  small-signal; for large-signal frequency response, point the user to
  the spectrum tab.

### 6.5 Netlist editor (`mockups/04_netlist.html`)

Text view of the same `Circuit`.

- Editable. Edits are debounced, then re-parsed; if parse succeeds, the
  schematic updates.
- Syntax highlighting: comment, directive, ref-designator, number, keyword,
  node, string, parameter, operator. Color tokens are in the mockup CSS.
- Gutter with line numbers.
- Outline panel: parsed structure tree (libraries, parameters, sources,
  passives, subcircuits, probes, analyses).
- Status bar: parser state, element count, sync state with schematic,
  target dialect.
- Toolbar:
  - **Format** — safe whitespace normalization only. Never reorders lines.
  - **Validate** — re-parse and report errors with line/column.
  - **Sync** — force-reload from schematic, discarding text edits.
  - **Export** — emit for ngspice, LTspice, KiCad, or generic Berkeley
    SPICE 3 (translation table — see §10.5).

## 7. Signal generator

The "signal generator" is a property of a voltage or current source. The UI
exposes high-level waveforms; the engine adapter lowers them to SPICE source
statements.

### 7.1. Waveform list

| Mode      | SPICE primitive                         | UI parameters                          |
|-----------|-----------------------------------------|----------------------------------------|
| `dc`      | `DC <value>`                            | level                                  |
| `sin`     | `SIN(off ampl freq td damp phase)`      | freq, amplitude, offset, phase         |
| `pulse`   | `PULSE(v1 v2 td tr tf pw per)`          | period, pulse-width, rise, fall, v_lo, v_hi, delay |
| `pwl`     | `PWL(t1 v1 t2 v2 ...)`                  | source: file, sample-rate, gain, loop  |
| `sffm`    | `SFFM(off ampl fc mdi fm)`              | carrier-freq, mod-freq, deviation      |
| `noise`   | (synthesized via `PWL` or `B`-source)   | type (white/pink/band-limited), RMS, BW, seed |
| `chirp`   | (synthesized via `B`-source expression) | f-start, f-stop, duration, shape, mode |
| `twotone` | sum of two `SIN` sources                | f1, f2, a1, a2, phase-offset           |
| `am`      | `B`-source expression                   | carrier-freq, mod-freq, depth, shape   |
| `fm`      | `SFFM` with depth derived               | carrier-freq, mod-freq, deviation      |
| `arb`     | `PWL` from imported CSV/WAV             | file path, sample-rate, gain, loop     |

### 7.2. Lowering

The engine adapter rewrites high-level waveforms to SPICE-native ones at
netlist-emit time. The user never sees the lowered form unless they switch
to the netlist tab — and even then, the high-level form is preserved as a
structured comment so the next round-trip restores it:

```
*+ V1 waveform=chirp f0=20 f1=20k duration=1 shape=log
V1 in_ac 0 PWL(...generated...)
```

### 7.3. Output impedance

Modeled as a series resistor between the source and the connected node, not
a property of the source. `0Ω`, `50`, `75`, `600` are presets. When the user
selects nonzero output Z, the emitter inserts an extra resistor named
`<Vsrc>_Zout` between the source's positive terminal and the user's node;
the schematic shows this resistor explicitly.

## 8. Component library

Components are data, not code. The library is a directory of YAML files,
one per component family. The frontend palette is generated from this
library at build time.

```yaml
# library/passive/resistor.yaml
ref_prefix: R
kind: resistor
spice_card: "{ref} {nodes[0]} {nodes[1]} {value}"
node_count: 2
node_names: [a, b]
default_value: "1k"
symbol_svg: |
  <path d="M0 5 H4 L6 1 L9 9 L12 1 L15 9 L17 5 H22"/>
inspector_fields:
  - { name: value,     label: Resistance, unit: "Ω",  type: spice_value }
  - { name: tolerance, label: Tolerance,  unit: "%",  type: number, default: 5 }
  - { name: power,     label: Power,      unit: "W",  type: number, default: 0.25 }
```

For active devices and tubes, the library entry references a `.lib` file:

```yaml
# library/tubes/12ax7.yaml
ref_prefix: X
kind: tube
spice_card: "{ref} {nodes[0]} {nodes[1]} {nodes[2]} 12AX7"
node_count: 3
node_names: [plate, grid, cathode]
library: tubes_koren.lib
model_name: 12AX7
symbol_svg: |
  <circle cx="11" cy="8" r="6"/>
  <path d="M11 2 V0 M7 5 H15 M11 14 V16"/>
  <path d="M5 8 H17" stroke-dasharray="1.5 1.2"/>
```

When the user drops a `.lib` file into the project, the library loader
scans for `.SUBCKT` definitions and auto-creates a YAML stub for each
discovered subcircuit. The user fills in the symbol and pin order, then
saves.

## 9. Engine integration

### 9.1. Subprocess model

ngspice runs as a long-lived subprocess in **interactive mode** (`ngspice
-p`). The Go side sends commands over stdin and reads results from stdout.
Use `ngspice`'s control-mode commands (`source`, `run`, `print`, `wrdata`)
rather than batch mode — interactive mode survives multiple analyses on
the same loaded circuit, which is much faster.

### 9.2. Result transport

ngspice writes raw simulation data to a `.raw` file (binary or ASCII). Don't
parse `.raw` directly; use the `wrdata` command to dump specific vectors as
ASCII columns to a known path, then read those. Stream them to the frontend
as the simulation proceeds — for long transient runs, partial results
should appear on the scope before the run finishes.

### 9.3. Cancellation

A user changing parameters or hitting "Stop" should kill the active sim.
Send `quit` over stdin (graceful) with a 200ms deadline; if no response,
`SIGKILL`. The supervisor restarts ngspice for the next request.

### 9.4. Convergence reporting

When ngspice can't converge, parse the error stream for the offending node
and last-attempted voltage. Report to the frontend as a structured error,
not a string. The schematic editor highlights the offending node.

```json
{ "error": "no_convergence",
  "node": "plate",
  "last_voltage": 187.3,
  "iteration": 200,
  "hint": "Try .options gmin=1e-9 or add a small resistor to ground" }
```

## 10. Frontend

### 10.1. Bundle constraint

No `.ts` or `.tsx` files. Vite is configured with `esbuild` JSX-only
transforms, no TypeScript pipeline. JSDoc annotations are encouraged for
IDE intellisense without shipping types.

### 10.2. State

Zustand stores:
- `useCircuit` — the `Circuit` object plus dirty flag and undo/redo stack.
- `useSelection` — selection set and active inspector target.
- `useSimulation` — current run state, partial results, errors.
- `useUI` — active tab, panel widths, theme.

### 10.3. Charts

| View              | Library  | Why                                       |
|-------------------|----------|-------------------------------------------|
| Scope             | uPlot    | 60fps, 100k+ points, tiny bundle          |
| Spectrum analyzer | Plotly   | Log x, hover markers, pan/zoom for free   |
| Network (Bode)    | uPlot    | Two stacked plots, simple                 |
| Smith chart       | custom   | SVG with parametric trace                 |

### 10.4. FFT

For the spectrum view, ngspice can compute FFTs server-side via its
`spec` and `fft` commands. Use them. Don't ship a JS FFT just to recompute
what the engine already did. Exception: marker math (peak-find, THD over
markers) runs client-side over the dB-scaled bins.

### 10.5. Export translation

The Netlist tab's Export action picks a target dialect:

- ngspice — emit as-is.
- Berkeley SPICE 3 — strip ngspice-only directives (`.MEAS`, `.STEP PARAM`,
  `B`-source expressions outside of LBNL form).
- LTspice — translate `.tran ... uic` to `.tran ... startup`, rename `MEG`
  is fine, change behavioral source syntax where it differs.
- KiCad — emit as a flat netlist that KiCad's eeschema importer accepts.

The translation table lives in `backend/internal/netlist/dialects.go`.

## 10.6. Static asset serving

Production runs as a single Go process on a single port. The Go server
serves `frontend/dist/` (produced by `npm run build`) for everything that
isn't `/api/*` or `/ws/*`. The dev-mode two-process setup with Vite is a
convenience for UI iteration (HMR, JSX transform) — not a deployment
requirement. Routing precedence on the backend:

1. `/healthz` — liveness probe
2. `/api/...` — REST handlers
3. `/ws/...`  — WebSocket upgrades
4. everything else — static SPA fallback to `index.html`

Eventually we may switch to `embed.FS` so a built binary ships with the
frontend baked in. Defer that until the UI stabilizes; on-disk serving is
fine for now and avoids forcing a frontend build before every backend
build.

## 11. JSON-over-WebSocket protocol

Frame format:

```json
{ "op": "<verb>", "id": "<correlation-id>", "payload": { ... } }
```

### Client → server

- `circuit.load`     — load a `Circuit` (replaces server-side state)
- `circuit.update`   — diff or full replace
- `sim.run`          — run an analysis. payload: `{ "analysis": "tran", "args": [...] }`
- `sim.cancel`       — cancel an in-flight run
- `library.list`     — list available components
- `library.import`   — import a `.lib` file

### Server → client

- `sim.result`       — partial or final analysis output. Streams.
- `sim.error`        — structured error (see §9.4)
- `sim.done`         — simulation complete
- `circuit.changed`  — circuit modified server-side (e.g. by netlist edit)

## 12. Project conventions

### Go

- `gofmt` is law. `golangci-lint` runs in CI with the default ruleset.
- Errors are wrapped with `fmt.Errorf("doing X: %w", err)`. No raw error
  strings.
- Public types and functions have doc comments.
- Tests live next to code in `_test.go` files. Table-driven where the
  cases are independent.
- One package per directory. Avoid `internal/utils`; if you need a utility,
  it has a real name.
- No global state. Inject dependencies via constructor.

### Frontend

- Files: `kebab-case.jsx` for components, `kebab-case.js` for plain modules.
- Components are functions with default exports. No class components.
- Side effects in `useEffect` only. Cleanup is mandatory.
- Tailwind-style className strings allowed for layout, but actual styling
  goes in colocated `.css` modules. We do not ship Tailwind itself.

### Naming

- Component refs follow SPICE convention: `R1`, `C1`, `V1`, `X1`, `Q1`.
- Internal node names are lowercase with underscores: `in_ac`, `vout`,
  `b_plus`. The literal `0` is always ground.
- Library file names are lowercase with underscores: `tubes_koren.lib`,
  `bjt_onsemi.lib`.

## 13. Roadmap

Milestones in order. Each is a stopping point — we review before proceeding.

1. **Data model + netlist round-trip.** This is the spine. KICKOFF.md
   targets this milestone explicitly. ← **start here**
2. **ngspice subprocess adapter.** Spawn, feed circuit, parse `wrdata`
   output, return time-series and frequency-domain results.
3. **HTTP/WS API.** Wire the protocol from §11. End-to-end smoke test:
   load `preamp_12ax7.cir`, run `.tran`, get streaming results.
4. **Schematic editor (read-only first).** Render `Circuit` to SVG. No
   editing yet — just make the existing fixture render correctly.
5. **Scope view.** Hook the streaming results to a uPlot canvas. CH1/CH2
   measurements working.
6. **Spectrum + Network analyzer.** Plotly for spectrum, uPlot for Bode.
7. **Schematic editing.** Selection, drag, rotate, wire-drawing, palette
   drop. This is the hardest UI work; budget accordingly.
8. **Netlist editor + round-trip with schematic.** Live bidirectional
   sync. Debounced re-parse.
9. **Library import.** `.lib` ingestion, palette generation, the Koren
   tube models.
10. **Signal generator polish.** All eleven waveforms lowered to SPICE
    correctly. Arbitrary waveform import (CSV, WAV).
11. **Marker math, THD/THD+N readouts, derived measurements.**
12. **Smith chart, VSWR, S-parameter views for RF work.**

## 14. Open questions

- Persistence: do circuits live as files only, or do we add a project
  database? Files-only for v1.
- Multi-circuit projects (one schematic referencing another via subcircuit
  hierarchy): supported in the model from day one, but no UI for it in v1.
- `.NOISE` analysis tab: deferred. Useful for amp design but not on the
  critical path.
- Plug-in architecture for custom waveform generators or analyses: maybe
  v2. For v1, edit the source.
