// Editing helpers for milestone 7. Pure functions only — no React, no
// Zustand. The store calls these to compute new Circuit values; the Canvas
// calls them to convert mouse events into ref/pin/node references.
//
// Connectivity model: m7 keeps the data model's wire-list empty for new
// edits and instead unifies node *names* when the user draws a wire between
// two pins. The synthesized wire renderer in `wires.js` then redraws
// connectivity from the updated node names. This avoids round-tripping
// graphical wires through ngspice (which only sees nodes anyway — see
// DESIGN.md §4) and keeps the schematic readable through component drags.

import { SYMBOLS } from '../../symbols/symbols.jsx';

export const GRID = 14;

/** Snap a coordinate to the schematic's 14 px grid. */
export function snap(v, grid = GRID) {
  return Math.round(v / grid) * grid;
}

/** Snap a {x, y} point to the schematic grid. */
export function snapPoint(p, grid = GRID) {
  return { x: snap(p.x, grid), y: snap(p.y, grid) };
}

/** Clamp v into [lo, hi]. */
export function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }

/** Normalize rotation into [0, 360). */
export function normRot(r) { return (((r ?? 0) % 360) + 360) % 360; }

const KIND_PREFIX = {
  resistor: 'R',
  capacitor: 'C',
  inductor: 'L',
  voltage_source: 'V',
  current_source: 'I',
  subcircuit: 'X',
  diode: 'D',
  bjt: 'Q',
  fet: 'J',
  mosfet: 'M',
  tube: 'X',
};

/** Ref prefix per Component.Kind, falling back to "U" for the unknown kinds. */
export function refPrefix(kind) { return KIND_PREFIX[kind] ?? 'U'; }

/**
 * Pick the next free ref designator for a kind: scans existing refs with the
 * same prefix and returns prefix + (max suffix + 1). E.g. an R1, R2 circuit
 * yields "R3" for a new resistor.
 */
export function nextRef(circuit, kind) {
  const prefix = refPrefix(kind);
  let max = 0;
  for (const c of circuit?.components || []) {
    if (typeof c.ref !== 'string' || !c.ref.startsWith(prefix)) continue;
    const tail = c.ref.slice(prefix.length);
    const n = /^\d+$/.test(tail) ? parseInt(tail, 10) : NaN;
    if (Number.isFinite(n) && n > max) max = n;
  }
  return `${prefix}${max + 1}`;
}

/**
 * Pick a fresh node name. Generates "n1", "n2", ... avoiding all node names
 * already used by any component. Ground ("0") and named user nodes (e.g.
 * "vout", "in_ac") are skipped naturally.
 */
export function nextNodeName(circuit) {
  const used = new Set();
  for (const c of circuit?.components || []) {
    for (const n of c.nodes || []) if (n) used.add(n);
  }
  for (let i = 1; i < 10000; i++) {
    const candidate = `n${i}`;
    if (!used.has(candidate)) return candidate;
  }
  return `n_${Date.now()}`;
}

/**
 * Default Component spec for a fresh palette drop, centred at (x, y). The
 * caller assigns ref/nodes; this returns the kind-specific defaults the
 * inspector and netlist emitter expect to find.
 */
export function defaultsForKind(kind) {
  const layout = { x: 0, y: 0, rot: 0, mirror: false };
  switch (kind) {
    case 'resistor':       return { kind, value: '1k',  layout, params: {} };
    case 'capacitor':      return { kind, value: '10n', layout, params: {} };
    case 'inductor':       return { kind, value: '1m',  layout, params: {} };
    case 'voltage_source': return { kind, value: '',     layout, params: {}, source: { mode: 'dc', params: { value: '1' } } };
    case 'current_source': return { kind, value: '',     layout, params: {}, source: { mode: 'dc', params: { value: '1m' } } };
    case 'subcircuit':     return { kind, value: '',     layout, model: 'X', params: {} };
    default:               return { kind, value: '',     layout, params: {} };
  }
}

/** Number of pin slots a kind exposes (per the symbol library). */
export function pinCount(kind) {
  return SYMBOLS[kind]?.pins?.length ?? 0;
}

/**
 * Build a fresh Component, ready to splice into Circuit.components. Assigns
 * a unique ref + fresh per-pin node names, snaps the position to the grid,
 * and merges the kind-specific defaults.
 */
export function newComponent(circuit, kind, x, y) {
  const base = defaultsForKind(kind);
  const ref = nextRef(circuit, kind);
  const pins = pinCount(kind);
  const used = new Set();
  for (const c of circuit?.components || []) for (const n of c.nodes || []) if (n) used.add(n);
  const nodes = [];
  for (let i = 0; i < pins; i++) {
    let n = '';
    for (let k = 1; k < 10000; k++) {
      const cand = `n${used.size + k}`;
      if (!used.has(cand)) { n = cand; used.add(cand); break; }
    }
    nodes.push(n);
  }
  return {
    ...base,
    ref,
    nodes,
    layout: { ...base.layout, x: snap(x), y: snap(y) },
  };
}

/**
 * Pick the "winning" node when two are unified by a wire-draw. Ground (the
 * literal "0" node) always wins; otherwise prefer the human-named one over a
 * machine-generated `nN`; finally fall back to the first argument.
 */
export function chooseWinningNode(a, b) {
  if (a === b) return a;
  if (a === '0') return '0';
  if (b === '0') return '0';
  const aAuto = /^n\d+$/.test(a);
  const bAuto = /^n\d+$/.test(b);
  if (aAuto && !bAuto) return b;
  if (!aAuto && bAuto) return a;
  return a;
}

/**
 * Rename a node across every component in the circuit and every probe that
 * targets it. Returns a new Circuit value; does not mutate the input.
 */
export function renameNode(circuit, fromName, toName) {
  if (fromName === toName || !fromName) return circuit;
  return {
    ...circuit,
    components: (circuit.components || []).map((c) => ({
      ...c,
      nodes: (c.nodes || []).map((n) => (n === fromName ? toName : n)),
    })),
    probes: (circuit.probes || []).map((p) => (p.node === fromName ? { ...p, node: toName } : p)),
    wires:  (circuit.wires  || []).map((w) => (w.node === fromName ? { ...w, node: toName } : w)),
  };
}

/**
 * Convert a screen-space pointer event into SVG world coordinates using the
 * SVG element's CTM. Returns null if the SVG hasn't laid out yet.
 */
export function eventToWorld(svgEl, ev) {
  if (!svgEl) return null;
  const ctm = svgEl.getScreenCTM();
  if (!ctm) return null;
  const pt = svgEl.createSVGPoint();
  pt.x = ev.clientX;
  pt.y = ev.clientY;
  const w = pt.matrixTransform(ctm.inverse());
  return { x: w.x, y: w.y };
}

/**
 * Walk an event's target ancestry looking for the nearest element annotated
 * with `data-hit`. Returns a structured hit { kind, ref, pinIndex } or null.
 *
 * `kind` is one of the values placed on data-hit by Canvas.jsx — currently
 * "component" or "pin". The traversal stops at the SVG root so handlers
 * outside the canvas (e.g. in the title bar) don't accidentally match.
 */
export function findHit(target, root) {
  let el = target;
  while (el && el !== root) {
    if (el.dataset && el.dataset.hit) {
      return {
        kind: el.dataset.hit,
        ref: el.dataset.ref,
        pinIndex: el.dataset.pin != null ? Number(el.dataset.pin) : undefined,
      };
    }
    el = el.parentNode;
  }
  return null;
}

/** True when refs[] contains ref. Cheap for the small sizes we deal with. */
export function includesRef(refs, ref) { return Array.isArray(refs) && refs.indexOf(ref) >= 0; }

/** Deduplicate a string array, preserving insertion order. */
export function uniqueRefs(refs) {
  const out = [];
  const seen = new Set();
  for (const r of refs || []) {
    if (!seen.has(r)) { seen.add(r); out.push(r); }
  }
  return out;
}

/**
 * Compute the axis-aligned rectangle two corner points define. Used by the
 * rubber-band selection to test which components fall inside.
 */
export function normalizeRect(a, b) {
  const x0 = Math.min(a.x, b.x);
  const y0 = Math.min(a.y, b.y);
  const x1 = Math.max(a.x, b.x);
  const y1 = Math.max(a.y, b.y);
  return { x0, y0, x1, y1, w: x1 - x0, h: y1 - y0 };
}

/** True when (x, y) is inside the (inclusive) rectangle r. */
export function rectContains(r, x, y) {
  return x >= r.x0 && x <= r.x1 && y >= r.y0 && y <= r.y1;
}
