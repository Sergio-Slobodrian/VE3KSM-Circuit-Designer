// Zustand stores per DESIGN.md §10.2. useCircuit/useSelection/useUI cover the
// schematic tab (milestone 4); useSimulation drives the Scope tab (milestone 5)
// over the WebSocket protocol.

import { create } from 'zustand';
import { fetchExample, fetchExamples, fetchLibrary, fetchHealth, importLibrary as apiImportLibrary } from '../api/client.js';
import { openSocket, nextEnvelopeID, runAnalysisStream } from '../api/ws.js';
import { autoVDiv, autoTimeDiv } from '../lib/measurements.js';
import {
  newComponent, normRot, snap, renameNode, chooseWinningNode, uniqueRefs,
} from '../views/schematic/edit.js';

/**
 * useCircuit — the active Circuit, its load lifecycle, and the editing
 * mutations the schematic editor (m7) drives.
 *
 * status transitions: idle → loading → ready, with loading → error on failure.
 * Calling load() while a previous load is in flight cancels the older promise's
 * effect by ignoring its resolution (the latest sourceName wins).
 *
 * Editing operations mutate `circuit` immutably (each call returns a fresh
 * object) and push the prior value onto a bounded history stack so undo/redo
 * are O(1) snapshots. `dirty` flips true on the first edit after a load and
 * clears on the next successful load — the status bar surfaces it so the
 * user knows the on-screen circuit no longer matches what's on disk.
 */
const HISTORY_LIMIT = 64;

export const useCircuit = create((set, get) => {
  let inflight = 0;

  // Wrap a synchronous Circuit transformer so it pushes the prior circuit onto
  // history, applies the transform, and clears the redo stack. Returns the
  // post-edit circuit so callers can chain (e.g. addComponent returning ref).
  const edit = (transform) => {
    const cur = get().circuit;
    if (!cur) return null;
    const next = transform(cur);
    if (!next || next === cur) return cur;
    const history = get().history.concat([cur]).slice(-HISTORY_LIMIT);
    set({ circuit: next, history, future: [], dirty: true });
    return next;
  };

  return {
    circuit: null,
    sourceName: null,
    catalog: [],
    status: 'idle',
    error: null,
    dirty: false,
    /** @type {Array<object>} */ history: [],
    /** @type {Array<object>} */ future: [],

    async loadCatalog() {
      try {
        const { examples } = await fetchExamples();
        set({ catalog: examples || [] });
      } catch (err) {
        set({ catalog: [], error: err.message });
      }
    },

    async load(name) {
      const ticket = ++inflight;
      set({ status: 'loading', error: null, sourceName: name });
      try {
        const circuit = await fetchExample(name);
        if (ticket !== inflight) return;
        // Successful load resets edit history — the on-disk circuit becomes
        // the new origin for undo. Editing it from here makes the store dirty.
        set({ circuit, status: 'ready', history: [], future: [], dirty: false });
      } catch (err) {
        if (ticket !== inflight) return;
        set({ status: 'error', error: err.message });
      }
    },

    /**
     * Replace the active circuit wholesale (used by the netlist editor in
     * milestone 8 and by undo/redo internally). Like load() it resets dirty
     * because the caller is supplying the canonical state.
     */
    replaceCircuit(circuit) {
      set({ circuit, history: [], future: [], dirty: false });
    },

    undo() {
      const { history, future, circuit } = get();
      if (history.length === 0 || !circuit) return;
      const prev = history[history.length - 1];
      set({
        circuit: prev,
        history: history.slice(0, -1),
        future: future.concat([circuit]),
        dirty: true,
      });
    },

    redo() {
      const { history, future, circuit } = get();
      if (future.length === 0 || !circuit) return;
      const next = future[future.length - 1];
      set({
        circuit: next,
        history: history.concat([circuit]),
        future: future.slice(0, -1),
        dirty: true,
      });
    },

    /**
     * Patch a single component's top-level fields by ref. Used by the
     * inspector for value/model/ref edits. Pass `{ ref: 'R7' }` to rename;
     * the ref change propagates to selection separately (see useSelection).
     */
    updateComponent(ref, patch) {
      return edit((c) => ({
        ...c,
        components: (c.components || []).map((comp) => (comp.ref === ref ? { ...comp, ...patch } : comp)),
      }));
    },

    /** Translate a set of refs by (dx, dy). No-ops when both are zero. */
    moveComponents(refs, dx, dy) {
      if ((!dx && !dy) || !refs || refs.length === 0) return;
      const set_ = new Set(refs);
      return edit((c) => ({
        ...c,
        components: (c.components || []).map((comp) => {
          if (!set_.has(comp.ref)) return comp;
          return {
            ...comp,
            layout: {
              ...comp.layout,
              x: snap((comp.layout?.x ?? 0) + dx),
              y: snap((comp.layout?.y ?? 0) + dy),
            },
          };
        }),
      }));
    },

    /** Rotate selected components in place by `delta` degrees (default 90 CW). */
    rotateComponents(refs, delta = 90) {
      if (!refs || refs.length === 0) return;
      const set_ = new Set(refs);
      return edit((c) => ({
        ...c,
        components: (c.components || []).map((comp) => {
          if (!set_.has(comp.ref)) return comp;
          return { ...comp, layout: { ...comp.layout, rot: normRot((comp.layout?.rot ?? 0) + delta) } };
        }),
      }));
    },

    /** Toggle the mirror flag on each ref in `refs`. */
    mirrorComponents(refs) {
      if (!refs || refs.length === 0) return;
      const set_ = new Set(refs);
      return edit((c) => ({
        ...c,
        components: (c.components || []).map((comp) => {
          if (!set_.has(comp.ref)) return comp;
          return { ...comp, layout: { ...comp.layout, mirror: !comp.layout?.mirror } };
        }),
      }));
    },

    /**
     * Delete the listed components. Probes anchored to a node those components
     * exclusively touched aren't pruned — the node may exist on other devices,
     * and dangling probes can be cleaned up via the inspector if needed.
     */
    deleteComponents(refs) {
      if (!refs || refs.length === 0) return;
      const set_ = new Set(refs);
      return edit((c) => ({
        ...c,
        components: (c.components || []).filter((comp) => !set_.has(comp.ref)),
      }));
    },

    /**
     * Append a fresh component centred at the snapped (x, y). Returns the
     * generated ref. The new component starts with auto-named per-pin nodes
     * the user wires up via Canvas pin-clicks.
     *
     * `spec.manifest`, when supplied (m9 palette drop), carries node_count,
     * model_name, and a library file reference; tube/subcircuit drops use
     * those to pin the right model and add a `.LIB` include if missing.
     *
     * V/I sources auto-attach a voltage probe at their positive (nodes[0])
     * terminal. Without this, a freshly-dropped Signal source has nothing the
     * Scope tab can bind a channel to — and there is currently no other UI
     * affordance to add a probe from the schematic. The probe shows up in
     * the next sim run's auto-channel-binding (useSimulation.run, channel
     * defaulting), so a click of "Run sim" gives the user a visible trace.
     */
    addComponent(spec) {
      const cur = get().circuit;
      if (!cur) return null;
      const fresh = newComponent(cur, spec.kind, spec.x ?? 0, spec.y ?? 0, spec.manifest);
      const libRef = spec.manifest?.library;
      const isSource = fresh.kind === 'voltage_source' || fresh.kind === 'current_source';
      const probeNode = isSource ? fresh.nodes?.[0] : null;
      edit((c) => {
        const next = { ...c, components: (c.components || []).concat([fresh]) };
        if (libRef && !(c.libraries || []).some((l) => l.path === libRef)) {
          next.libraries = (c.libraries || []).concat([{ path: libRef }]);
        }
        if (probeNode && probeNode !== '0' && !(c.probes || []).some((p) => p.node === probeNode)) {
          next.probes = (c.probes || []).concat([{
            name: probeNode, node: probeNode, kind: 'voltage', layout: { x: 0, y: 0, rot: 0, mirror: false },
          }]);
        }
        return next;
      });
      return fresh.ref;
    },

    /**
     * Add a probe at `node` if not already present. `kind` is "voltage" or
     * "current" (the latter expects `node` to be a component ref, since
     * ngspice's `I(...)` operates on devices not nodes). No-op when `node`
     * is empty or "0" (ground is not a useful probe target).
     */
    addProbe(node, kind = 'voltage', name) {
      if (!node || node === '0') return null;
      return edit((c) => {
        if ((c.probes || []).some((p) => p.node === node && p.kind === kind)) return c;
        const probe = {
          name: name || node, node, kind,
          layout: { x: 0, y: 0, rot: 0, mirror: false },
        };
        return { ...c, probes: (c.probes || []).concat([probe]) };
      });
    },

    /**
     * Remove every probe whose node matches `node`. The Inspector's probe
     * toggle calls this to undo a prior addProbe; the deleteComponents path
     * deliberately does NOT call it (a probed node may exist on other
     * components).
     */
    removeProbe(node) {
      if (!node) return null;
      return edit((c) => {
        const next = (c.probes || []).filter((p) => p.node !== node);
        if (next.length === (c.probes || []).length) return c;
        return { ...c, probes: next };
      });
    },

    /**
     * Connect two pins by unifying their nodes. The "winning" node is picked
     * via `chooseWinningNode` (ground beats names; named nodes beat auto
     * `nN`). The losing node is renamed everywhere it appears.
     *
     * The graphical wire isn't stored — see edit.js header for rationale.
     * The synthesized routing in wires.js redraws connectivity from the
     * unified node names.
     */
    connectPins(fromRef, fromPinIndex, toRef, toPinIndex) {
      return edit((c) => {
        const from = (c.components || []).find((x) => x.ref === fromRef);
        const to   = (c.components || []).find((x) => x.ref === toRef);
        if (!from || !to) return c;
        const fromNode = from.nodes?.[fromPinIndex] ?? '';
        const toNode   = to.nodes?.[toPinIndex] ?? '';
        if (!fromNode && !toNode) return c;
        if (fromNode === toNode) return c;
        if (!fromNode) {
          return {
            ...c,
            components: c.components.map((comp) => {
              if (comp.ref !== fromRef) return comp;
              const nodes = comp.nodes.slice();
              nodes[fromPinIndex] = toNode;
              return { ...comp, nodes };
            }),
          };
        }
        if (!toNode) {
          return {
            ...c,
            components: c.components.map((comp) => {
              if (comp.ref !== toRef) return comp;
              const nodes = comp.nodes.slice();
              nodes[toPinIndex] = fromNode;
              return { ...comp, nodes };
            }),
          };
        }
        const winner = chooseWinningNode(fromNode, toNode);
        const loser = winner === fromNode ? toNode : fromNode;
        return renameNode(c, loser, winner);
      });
    },

    /** Rename a single node across the circuit (used by the inspector). */
    renameNode(fromName, toName) {
      if (!fromName || !toName || fromName === toName) return;
      return edit((c) => renameNode(c, fromName, toName));
    },
  };
});

/**
 * useSelection — the schematic's selection set. Now multi-select to support
 * milestone-7's drag/rotate/delete on groups. Stored as a string[] in
 * insertion order (rather than a Set) so Zustand's shallow equality picks up
 * changes consistently and consumers can iterate deterministically.
 *
 * Most consumers care about `selectedRefs[0]` — the inspector treats it as
 * the "primary" component, and the status bar shows it as the active selection.
 */
export const useSelection = create((set, get) => ({
  selectedRefs: [],

  /** Replace the selection with the given refs. */
  setSelection(refs) {
    set({ selectedRefs: uniqueRefs(refs || []) });
  },
  /** Set selection to a single ref (or clear when null). */
  select(ref) { set({ selectedRefs: ref ? [ref] : [] }); },
  /** Add ref if absent; remove if present. */
  toggle(ref) {
    if (!ref) return;
    const xs = get().selectedRefs;
    set({ selectedRefs: xs.includes(ref) ? xs.filter((r) => r !== ref) : xs.concat([ref]) });
  },
  /** Add ref to the selection (no-op if already present). */
  add(ref) {
    if (!ref) return;
    const xs = get().selectedRefs;
    if (!xs.includes(ref)) set({ selectedRefs: xs.concat([ref]) });
  },
  /** Empty the selection set. */
  clear() { set({ selectedRefs: [] }); },
  /** True when ref is currently selected. */
  isSelected(ref) { return get().selectedRefs.includes(ref); },
}));

/**
 * useSimulation — drives a transient run over the WebSocket protocol and
 * accumulates the streaming Frame data the Scope view renders.
 *
 * Lifecycle:
 *
 *   idle → connecting → loading → running → done | error | cancelled → idle
 *
 * `run(circuit, analysis)` opens a fresh WS, loads the circuit, starts the
 * analysis, and streams frames until sim.done/sim.error or cancel.
 *
 * Frames arrive at engine sample rate — for a 5 ms .TRAN run that's ~5000
 * messages — so we buffer raw frames in a module-local array and flush them
 * to React state once per animation frame. Without batching, every per-frame
 * setState would force a uPlot redraw and tank the framerate.
 *
 * Channels are user-facing display assignments over the available probes.
 * CH1/CH2 default to the first two probes; CH3/CH4 are off until the user
 * picks a probe in the Channels panel.
 */
const SCOPE_CHANNEL_COLORS = ['#f5b840', '#5dcaa5', '#d4537e', '#97c459'];
const MATH_CHANNEL_COLORS  = ['#7fb3ff', '#cf86e3'];

function makeChannel(idx, probeNode = null) {
  return {
    id: `ch${idx + 1}`,
    label: `CH${idx + 1}`,
    color: SCOPE_CHANNEL_COLORS[idx],
    probeNode,                  // null = channel off
    enabled: probeNode != null,
    coupling: 'dc',             // 'dc' | 'ac' | 'gnd'
    invert: false,
    vDiv: idx === 0 ? 0.2 : 1,  // volts per division
    position: 0,                 // vertical offset, in divisions
  };
}

/**
 * Math channels are user-defined client-side derivations of the raw scope
 * channels — DESIGN.md §6.2 lists "ratio, difference, product, integral,
 * derivative — JS expression box that evaluates over the channel data".
 *
 * Each math channel carries an `expr` string the user types in the side panel
 * (e.g. `CH1 - CH2`, `INT(CH1)`). The evaluation happens in Scope.jsx via
 * compileMathExpression / evaluateMathChannel; the store just owns the
 * persistent display state.
 */
function makeMathChannel(idx) {
  return {
    id: `m${idx + 1}`,
    label: `M${idx + 1}`,
    color: MATH_CHANNEL_COLORS[idx % MATH_CHANNEL_COLORS.length],
    enabled: false,
    expr: '',
    coupling: 'dc',             // 'dc' | 'ac' | 'gnd' — same semantics as a physical channel
    invert: false,
    vDiv: 1,
    position: 0,
  };
}

export const useSimulation = create((set, get) => {
  /** @type {ReturnType<import('../api/ws.js').openSocket> | null} */
  let socket = null;
  let activeRunID = null;
  let pendingFrames = [];
  let flushScheduled = false;

  const flushFrames = () => {
    flushScheduled = false;
    if (pendingFrames.length === 0) return;
    const batch = pendingFrames;
    pendingFrames = [];
    set((s) => ({ frames: s.frames.concat(batch) }));
  };
  const scheduleFlush = () => {
    if (flushScheduled) return;
    flushScheduled = true;
    requestAnimationFrame(flushFrames);
  };

  const teardown = (nextStatus, error = null) => {
    activeRunID = null;
    if (socket) {
      const s = socket;
      socket = null;
      s.close();
    }
    pendingFrames = [];
    set({ status: nextStatus, error });
  };

  return {
    status: 'idle',           // 'idle' | 'connecting' | 'loading' | 'running' | 'done' | 'error' | 'cancelled'
    error: null,
    frames: [],               // Array<{ index, x, values: Record<string, number> }>
    runID: null,
    analysis: null,
    channels: [makeChannel(0), makeChannel(1), makeChannel(2), makeChannel(3)],
    mathChannels: [makeMathChannel(0), makeMathChannel(1)],
    timebase: { perDiv: 5e-4, position: 0 },   // s/div, s offset
    // True when the next successful run should auto-fit V/div + position +
    // time/div from the captured data. Set on circuit change, cleared after
    // the first auto-fit so subsequent runs honour the user's adjustments.
    autoFitPending: true,

    /**
     * Start a transient analysis. Closes any existing run, opens a fresh
     * WebSocket, loads the circuit server-side, then issues sim.run.
     *
     * @param {object} circuit  the loaded Circuit JSON
     * @param {{kind: string, args: string[]}} [analysis]  defaults to the
     *        circuit's first enabled .TRAN, falling back to .TRAN 1u 5m.
     */
    run(circuit, analysis) {
      if (!circuit) {
        set({ status: 'error', error: 'no circuit loaded' });
        return;
      }
      const a = analysis || pickDefaultAnalysis(circuit);

      // Default channel mapping: first two probes drive CH1 + CH2 if the user
      // hasn't bound them yet.
      const probes = (circuit.probes || []).map((p) => p.node);
      const channels = get().channels.map((ch, i) => {
        if (ch.probeNode || probes.length === 0) return ch;
        if (i < probes.length) return { ...ch, probeNode: probes[i], enabled: true };
        return ch;
      });

      // Tear down any prior socket cleanly before opening a new one.
      if (socket) { socket.close(); socket = null; }
      activeRunID = null;
      pendingFrames = [];
      set({
        status: 'connecting',
        error: null,
        frames: [],
        runID: null,
        analysis: a,
        channels,
      });

      socket = openSocket({
        onOpen: () => {
          if (!socket) return;
          set({ status: 'loading' });
          socket.send({
            op: 'circuit.load',
            id: nextEnvelopeID('load'),
            payload: { circuit },
          });
          // sim.run is sent on receipt of circuit.changed below, so we know the
          // server has the up-to-date circuit before kicking off the run.
        },
        onClose: () => {
          if (get().status === 'running' || get().status === 'loading' || get().status === 'connecting') {
            // Unexpected close — surface it. The 'cancelled' / 'done' / 'error'
            // paths run teardown themselves before the close fires.
            set({ status: 'error', error: 'connection closed unexpectedly' });
          }
          activeRunID = null;
          socket = null;
        },
        onError: () => {
          set({ status: 'error', error: 'websocket error' });
        },
      });

      socket.on('circuit.changed', () => {
        if (!socket) return;
        const id = nextEnvelopeID('run');
        activeRunID = id;
        set({ status: 'running', runID: id });
        socket.send({
          op: 'sim.run',
          id,
          payload: { analysis: a },
        });
      });

      socket.on('sim.result', (env) => {
        if (env.id !== activeRunID) return;
        const f = env.payload?.frame;
        if (!f) return;
        pendingFrames.push(f);
        scheduleFlush();
      });

      socket.on('sim.done', (env) => {
        if (env.id !== activeRunID) return;
        flushFrames();
        // First good run after a circuit change: rescale V/div + position +
        // time/div so the trace fills the screen. The preamp's vout in
        // particular has a multi-volt startup transient that pegs CH2 at
        // the default 1 V/div, so without this the user's first impression
        // of the scope is a flat line at the top.
        const after = get();
        if (after.autoFitPending && after.frames.length > 0) {
          const fitted = applyAutoFit(after.frames, after.channels);
          set({
            channels: fitted.channels,
            timebase: fitted.timebase,
            autoFitPending: false,
          });
        }
        teardown('done');
      });

      socket.on('sim.error', (env) => {
        if (env.id !== activeRunID) return;
        flushFrames();
        const err = env.payload?.error;
        const message = err?.message || 'simulation failed';
        teardown('error', message);
      });

      socket.on('error', (env) => {
        const ep = env.payload || {};
        teardown('error', `${ep.code || 'error'}: ${ep.message || ''}`);
      });
    },

    cancel() {
      if (!socket || !activeRunID) {
        set({ status: 'idle' });
        return;
      }
      try {
        socket.send({
          op: 'sim.cancel',
          id: nextEnvelopeID('cancel'),
          payload: { run_id: activeRunID },
        });
      } catch { /* socket may have already closed */ }
      teardown('cancelled');
    },

    /** Edit one channel's display state. Pass a partial channel object. */
    setChannel(index, patch) {
      set((s) => ({
        channels: s.channels.map((ch, i) => (i === index ? { ...ch, ...patch } : ch)),
      }));
    },

    /** Edit one math channel's display state (expr / enabled / vDiv / etc.). */
    setMathChannel(index, patch) {
      set((s) => ({
        mathChannels: s.mathChannels.map((m, i) => (i === index ? { ...m, ...patch } : m)),
      }));
    },

    setTimebase(patch) {
      set((s) => ({ timebase: { ...s.timebase, ...patch } }));
    },

    /** Drop accumulated frames and abandon any in-flight run. Used when the
     *  user switches the active example: the old probe nodes don't match, so
     *  the trace would no longer correspond to anything on screen. */
    reset() {
      if (socket) { socket.close(); socket = null; }
      activeRunID = null;
      pendingFrames = [];
      set({ frames: [], status: 'idle', error: null, runID: null, autoFitPending: true });
    },
  };
});

/**
 * Compute auto-fit V/div + position per channel and time/div from captured
 * frames. Mirrors what a bench scope's "Auto-set" button does: snap V/div to
 * a 1-2-5 sequence sized to the AC excursion, recentre vertically on the DC
 * mean, and pick a time/div that fits the captured window.
 *
 * Channels not bound to a probe (or with no samples) keep their current
 * settings; an off channel doesn't disturb a previously-fit on channel.
 */
function applyAutoFit(frames, channels) {
  const stats = new Map(); // node → { lo, hi, sum, count }
  for (const f of frames) {
    if (!f.values) continue;
    for (const [node, v] of Object.entries(f.values)) {
      let s = stats.get(node);
      if (!s) { s = { lo: Infinity, hi: -Infinity, sum: 0, count: 0 }; stats.set(node, s); }
      if (v < s.lo) s.lo = v;
      if (v > s.hi) s.hi = v;
      s.sum += v;
      s.count++;
    }
  }
  const fittedChannels = channels.map((ch) => {
    if (!ch.enabled || !ch.probeNode) return ch;
    const s = stats.get(ch.probeNode);
    if (!s || s.count === 0) return ch;
    const ppk = s.hi - s.lo;
    const meanV = s.sum / s.count;
    const vd = autoVDiv(ppk > 0 ? ppk : Math.max(Math.abs(meanV), 1));
    // For DC coupling, recentre on the mean so a non-zero-centred trace sits
    // mid-screen. AC coupling subtracts the mean already, so position stays 0.
    const position = ch.coupling === 'dc' ? -meanV / vd : 0;
    return { ...ch, vDiv: vd, position };
  });

  let timebase = { perDiv: 5e-4, position: 0 };
  if (frames.length >= 2) {
    const dur = frames[frames.length - 1].x - frames[0].x;
    timebase = { perDiv: autoTimeDiv(dur), position: 0 };
  }
  return { channels: fittedChannels, timebase };
}

/**
 * Read a UI preference from localStorage with a fallback. Wrapped so SSR or
 * sandboxed contexts where localStorage throws don't crash the store.
 */
function loadUIPref(key, fallback) {
  try {
    const raw = localStorage.getItem(`circuitlab.ui.${key}`);
    if (raw == null) return fallback;
    const v = JSON.parse(raw);
    return typeof v === typeof fallback ? v : fallback;
  } catch { return fallback; }
}

function saveUIPref(key, value) {
  try { localStorage.setItem(`circuitlab.ui.${key}`, JSON.stringify(value)); }
  catch { /* private mode / quota — silently drop */ }
}

/**
 * Pick the first enabled .TRAN analysis on the circuit, defaulting to a
 * 5 ms / 1 µs sweep so the preamp fixture renders something useful even
 * when the user has cleared its analyses.
 */
function pickDefaultAnalysis(circuit) {
  for (const a of circuit.analyses || []) {
    if (a.enabled !== false && (a.kind || '').toLowerCase() === 'tran') {
      return { kind: 'tran', args: a.args || ['1u', '5m'] };
    }
  }
  return { kind: 'tran', args: ['1u', '5m'] };
}

/**
 * Pivot the streamed AC/spectrum frames into per-probe magnitude+phase arrays.
 * The backend emits two columns per probe with keys `<node>:mag_db` and
 * `<node>:phase_deg`; this helper unwinds them so views can read by node.
 *
 * Returns:
 *   { freqs, mag: Map<node, Float64Array>, phase: Map<node, Float64Array> }
 */
export function pivotComplexFrames(frames) {
  const n = frames.length;
  const freqs = new Float64Array(n);
  const mag = new Map();
  const phase = new Map();
  for (let i = 0; i < n; i++) {
    const f = frames[i];
    freqs[i] = f.x;
    if (!f.values) continue;
    for (const [k, v] of Object.entries(f.values)) {
      const sep = k.lastIndexOf(':');
      if (sep < 0) continue;
      const node = k.slice(0, sep);
      const kind = k.slice(sep + 1);
      const target = kind === 'mag_db' ? mag : kind === 'phase_deg' ? phase : null;
      if (!target) continue;
      let arr = target.get(node);
      if (!arr) { arr = new Float64Array(n); target.set(node, arr); }
      arr[i] = v;
    }
  }
  return { freqs, mag, phase };
}

/**
 * Default probe pick for spectrum/network: first probe in the circuit, or
 * null when none exist. Reused by the views to seed selection on first load.
 */
export function defaultProbe(circuit) {
  const ps = circuit?.probes || [];
  return ps.length > 0 ? ps[0].node : null;
}

/**
 * useSpectrum — drives a `spectrum` analysis (ngspice transient + linearize +
 * fft + wrdata) over the WebSocket protocol. Frames arrive once per FFT bin;
 * we buffer them and commit once on sim.done so the Plotly trace is rebuilt
 * exactly once per run rather than streamed (the bin count is fixed by the
 * tran capture length, so progressive rendering buys little).
 *
 * The view's max-hold trace, M1/M2 markers, and THD readouts derive from the
 * committed frames + per-store config — no separate state needed for them.
 */
export const useSpectrum = create((set, get) => {
  let activeRun = null;
  let collected = [];

  const begin = (analysis) => {
    if (activeRun) { activeRun.cancel(); activeRun = null; }
    collected = [];
    set({ status: 'connecting', error: null, frames: [], runID: null, analysis });
  };

  return {
    status: 'idle',          // 'idle' | 'connecting' | 'loading' | 'running' | 'done' | 'error' | 'cancelled'
    error: null,
    frames: [],              // Array<{ x, values: { 'node:mag_db', 'node:phase_deg' } }>
    maxHoldFrames: [],       // bin-by-bin running max of mag_db, cleared on circuit reset
    runID: null,
    analysis: null,

    config: {
      span: '5m',            // tran capture length — sets RBW (= 1/span)
      step: '5u',            // tran step
      window: 'hanning',     // 'hanning' | 'hamming' | 'blackman' | 'bartlet' | 'cosine_n' | 'triangle' | 'none'
      detector: 'rms',       // display-only: 'rms' | 'peak' | 'sample' | 'avg'
      maxHold: false,
      probe: null,           // selected probe to feature; null = first available
      markers: { m1: null, m2: null }, // marker frequencies in Hz, null = unset
      f0: 1000,              // fundamental for THD/SINAD (Hz)
      harmonics: 10,         // number of harmonics included in THD sum (2..N)
      trackHarmonics: false, // overlay markers on every harmonic of f0
    },

    setConfig(patch) {
      set((s) => ({ config: { ...s.config, ...patch } }));
    },

    run(circuit) {
      if (!circuit) { set({ status: 'error', error: 'no circuit loaded' }); return; }
      const cfg = get().config;
      const analysis = {
        kind: 'spectrum',
        args: [cfg.step, cfg.span],
        options: { window: cfg.window },
      };
      begin(analysis);
      activeRun = runAnalysisStream({
        circuit,
        analysis,
        onConnecting: () => set({ status: 'connecting' }),
        onLoading:    () => set({ status: 'loading' }),
        onRunning:    (id) => set({ status: 'running', runID: id }),
        onFrame:      (f) => collected.push(f),
        onDone: () => {
          activeRun = null;
          const finalFrames = collected;
          collected = [];
          set((s) => {
            const hold = s.config.maxHold ? mergeMaxHold(s.maxHoldFrames, finalFrames) : [];
            return { frames: finalFrames, maxHoldFrames: hold, status: 'done' };
          });
        },
        onError: (msg) => {
          activeRun = null;
          collected = [];
          set({ status: 'error', error: msg });
        },
      });
    },

    cancel() {
      if (activeRun) { activeRun.cancel(); activeRun = null; }
      collected = [];
      set({ status: 'cancelled' });
    },

    clearMaxHold() { set({ maxHoldFrames: [] }); },

    reset() {
      if (activeRun) { activeRun.cancel(); activeRun = null; }
      collected = [];
      set({
        status: 'idle', error: null, frames: [], maxHoldFrames: [],
        runID: null, analysis: null,
        // keep config (window/detector/etc.) — survives circuit changes.
      });
    },
  };
});

/**
 * Merge two spectrum-frame arrays bin-by-bin, keeping the max magnitude per
 * frequency. Both arrays are assumed sorted by f.x. When the bin counts
 * differ (e.g. user changed span), prev is replaced with next so the older
 * RBW doesn't strand stale bins on the trace.
 */
function mergeMaxHold(prev, next) {
  if (prev.length !== next.length) return next.slice();
  const out = new Array(next.length);
  for (let i = 0; i < next.length; i++) {
    const p = prev[i], n = next[i];
    if (!p || !n || p.x !== n.x) return next.slice();
    const merged = { x: n.x, values: { ...n.values } };
    for (const [k, v] of Object.entries(p.values)) {
      if (k.endsWith(':mag_db')) {
        const nv = merged.values[k];
        merged.values[k] = nv == null ? v : Math.max(nv, v);
      }
    }
    out[i] = merged;
  }
  return out;
}

/**
 * useNetwork — drives a small-signal `ac` sweep for the Bode/Network tab.
 * Same shape as useSpectrum: open WS once, run, collect frames, commit on
 * sim.done. The view derives magnitude (dB), phase (deg), and group delay
 * (-dφ/dω) from the committed frames.
 */
export const useNetwork = create((set, get) => {
  let activeRun = null;
  let collected = [];

  return {
    status: 'idle',
    error: null,
    frames: [],
    runID: null,
    analysis: null,

    config: {
      mode: 'dec',           // 'dec' | 'oct' | 'lin'
      ptsPerDecade: 50,
      startHz: 10,
      stopHz: 100000,
      probeOut: null,        // probe at port 2 (frontend display only)
      groupDelay: false,     // overlay -dφ/dω on the magnitude plot
      autoMarkers: {
        minus3dB: true, peak: true, unityGain: true, phaseMargin: true,
        minus40dB: false, gainMargin: true,
      },
    },

    setConfig(patch) {
      set((s) => ({ config: { ...s.config, ...patch } }));
    },

    run(circuit) {
      if (!circuit) { set({ status: 'error', error: 'no circuit loaded' }); return; }
      const cfg = get().config;
      const args = cfg.mode === 'lin'
        ? ['lin', String(cfg.ptsPerDecade), String(cfg.startHz), String(cfg.stopHz)]
        : [cfg.mode, String(cfg.ptsPerDecade), String(cfg.startHz), String(cfg.stopHz)];
      const analysis = { kind: 'ac', args };
      if (activeRun) { activeRun.cancel(); activeRun = null; }
      collected = [];
      set({ status: 'connecting', error: null, frames: [], runID: null, analysis });

      activeRun = runAnalysisStream({
        circuit,
        analysis,
        onConnecting: () => set({ status: 'connecting' }),
        onLoading:    () => set({ status: 'loading' }),
        onRunning:    (id) => set({ status: 'running', runID: id }),
        onFrame:      (f) => collected.push(f),
        onDone: () => {
          activeRun = null;
          const finalFrames = collected;
          collected = [];
          set({ frames: finalFrames, status: 'done' });
        },
        onError: (msg) => {
          activeRun = null;
          collected = [];
          set({ status: 'error', error: msg });
        },
      });
    },

    cancel() {
      if (activeRun) { activeRun.cancel(); activeRun = null; }
      collected = [];
      set({ status: 'cancelled' });
    },

    reset() {
      if (activeRun) { activeRun.cancel(); activeRun = null; }
      collected = [];
      set({ status: 'idle', error: null, frames: [], runID: null, analysis: null });
    },
  };
});

/**
 * useUI — top-level chrome state: active tab, backend connection.
 */
export const useUI = create((set) => ({
  activeTab: 'schematic',
  backendOnline: false,
  library: [],
  // Scope grid brightness, 0..100. The scope screen is dark; against the
  // bright app chrome the grid washes out, so the user gets a slider rather
  // than a fixed value. Persisted in localStorage so opening the app again
  // restores the chosen brightness.
  gridBrightness: loadUIPref('gridBrightness', 70),
  // Width (px) of the right-hand control / outline panel on the analysis
  // and netlist tabs. User-resizable via the Splitter handle between the
  // screen pane and the panel; persisted so a wider panel survives reload.
  controlPanelWidth: loadUIPref('controlPanelWidth', 280),

  setTab(id) { set({ activeTab: id }); },

  setGridBrightness(v) {
    const clamped = Math.max(0, Math.min(100, v));
    set({ gridBrightness: clamped });
    saveUIPref('gridBrightness', clamped);
  },

  setControlPanelWidth(v) {
    const clamped = Math.max(200, Math.min(600, Math.round(v)));
    set({ controlPanelWidth: clamped });
    saveUIPref('controlPanelWidth', clamped);
  },

  async pingBackend() {
    try {
      await fetchHealth();
      set({ backendOnline: true });
    } catch {
      set({ backendOnline: false });
    }
  },

  async loadLibrary() {
    try {
      const { components } = await fetchLibrary();
      set({ library: components || [] });
    } catch {
      set({ library: [] });
    }
  },

  /**
   * Import a SPICE .lib file via /api/library/import. On success, refreshes
   * the palette so newly-discovered subcircuits appear immediately. Returns
   * the import result (filename + imported components) so the caller can
   * surface a confirmation toast.
   */
  async importLibrary(filename, body) {
    const res = await apiImportLibrary(filename, body);
    try {
      const { components } = await fetchLibrary();
      set({ library: components || [] });
    } catch {
      // Non-fatal: import succeeded but refresh failed. Keep the prior list
      // so the user does not see an empty palette flash.
    }
    return res;
  },
}));
