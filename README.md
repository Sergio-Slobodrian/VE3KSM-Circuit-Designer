# Circuit Lab

Circuit simulation app for audio amplifier and amateur radio filter design.
Wraps `ngspice` as the simulation engine, with a Go backend (no cgo) and a
React frontend (plain JavaScript in the browser, no TypeScript shipped to the
client).

## Layout

```
circuit-lab/
├── README.md          ← you are here
├── DESIGN.md          ← architecture, data model, view contracts, conventions
├── KICKOFF.md         ← single prompt to drive AI-assisted development
├── mockups/           ← static HTML mockups of the five tabs
│   ├── 01_schematic_editor.html
│   ├── 02_scope_and_spectrum.html
│   ├── 03_network_analyzer.html
│   └── 04_netlist.html
├── examples/          ← example SPICE netlists used as fixtures
│   ├── preamp_12ax7.cir
│   ├── lp_butter_sallenkey_4k.cir
│   └── tubes_koren.lib
├── backend/           ← Go server (ngspice subprocess wrapper + REST/WS API)
│   ├── go.mod
│   ├── cmd/server/main.go
│   └── internal/
│       ├── circuit/     ← in-memory data model (the spine)
│       ├── netlist/     ← SPICE parser + emitter (round-trippable)
│       ├── engine/      ← ngspice subprocess + result parsing
│       ├── analysis/    ← .AC, .TRAN, FFT, derived measurements
│       ├── api/         ← HTTP + WebSocket handlers
│       └── library/     ← .lib loader, component palette
└── frontend/          ← React + Vite, plain JS only in the browser
    ├── package.json
    ├── vite.config.js
    ├── index.html
    └── src/
        ├── main.jsx
        ├── App.jsx
        ├── index.css
        ├── components/
        └── lib/
```

## Prerequisites

- Go 1.22 or later
- Node 20 or later
- `ngspice` 42 or later on `PATH` (the engine; install via `apt install ngspice`,
  `brew install ngspice`, or the Windows installer from the ngspice site)

## Running it — two modes

### Production / single-process (Go serves everything)

```sh
cd frontend && npm install && npm run build
cd ../backend && go run ./cmd/server
# browse http://localhost:8080
```

The Go server detects `frontend/dist/`, serves it as static assets, and
handles `/api` and `/ws` on the same port. One process, one binary in
production.

### Development / two-process (Vite hot-reload for UI work)

```sh
# terminal 1
cd backend && go run ./cmd/server
# API + WS on :8080

# terminal 2
cd frontend && npm install && npm run dev
# Vite + HMR on :5173, proxies /api and /ws to :8080
# browse http://localhost:5173
```

Vite gives you ~50ms hot-module-reload on `.jsx` edits, which is hard to
give up while building UI. Once the UI work is done you can drop back to
the single-process mode.
