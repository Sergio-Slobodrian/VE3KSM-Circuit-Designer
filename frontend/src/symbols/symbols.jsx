// SVG symbol library for the schematic canvas. Each entry is keyed by
// circuit.Component.Kind and exposes:
//
//   pins   — local-coordinate pin positions in the unrotated symbol. Index
//            order matches the SPICE node order (DESIGN.md §4): for X
//            (subcircuit) the order is the .SUBCKT pin order; for V/I the
//            first node is "+", second is "−".
//   render — the SVG fragment for the body of the symbol, drawn around (0, 0).
//            The schematic wraps it in a <g transform="translate rotate"> so
//            the body never needs to know about world coordinates.
//
// Symbols are intentionally small (≈22–30 px) so they sit on the 14 px grid
// the mockup uses without crowding labels.
//
// Phase 2 of symbol_enhancement.md adds a second source: imported library
// manifests carrying a structured `symbol_def` (LTspice .asy converter
// output). resolveSymbol() unifies the two — manifest first, static fallback —
// so the canvas, wire router, and pin-hit detector all read pins/bbox the
// same way regardless of authoring origin.

import { findFamily } from '../lib/library.js';

const STROKE = 0.9;

function pinAt(x, y, name, labelSide) {
  const out = { x, y, name };
  if (labelSide) out.label_side = labelSide;
  return out;
}

export const SYMBOLS = {
  resistor: {
    pins: [pinAt(-11, 0, 'a'), pinAt(11, 0, 'b')],
    bbox: { w: 22, h: 10 },
    render: () => (
      <g fill="none" stroke="currentColor" strokeWidth={STROKE} strokeLinejoin="round">
        <path d="M-11 0 H-7 L-5 -4 L-2 4 L1 -4 L4 4 L7 -4 L9 0 H11" />
      </g>
    ),
  },

  capacitor: {
    pins: [pinAt(-11, 0, 'a'), pinAt(11, 0, 'b')],
    bbox: { w: 22, h: 10 },
    render: () => (
      <g fill="none" stroke="currentColor" strokeWidth={STROKE}>
        <path d="M-11 0 H-2 M-2 -5 V5 M2 -5 V5 M2 0 H11" />
      </g>
    ),
  },

  inductor: {
    pins: [pinAt(-11, 0, 'a'), pinAt(11, 0, 'b')],
    bbox: { w: 22, h: 10 },
    render: () => (
      <g fill="none" stroke="currentColor" strokeWidth={STROKE}>
        <path d="M-11 0 H-8" />
        <path d="M-8 0 a2.5 2.5 0 0 1 4 0 a2.5 2.5 0 0 1 4 0 a2.5 2.5 0 0 1 4 0 a2.5 2.5 0 0 1 4 0" />
        <path d="M8 0 H11" />
      </g>
    ),
  },

  voltage_source: {
    pins: [pinAt(0, -17, '+'), pinAt(0, 17, '−')],
    bbox: { w: 24, h: 34 },
    render: (comp) => (
      <g fill="none" stroke="currentColor" strokeWidth={STROKE}>
        <circle cx={0} cy={0} r={11} />
        {sourceMarking(comp?.source?.mode)}
        <line x1={0} y1={-11} x2={0} y2={-17} />
        <line x1={0} y1={11} x2={0} y2={17} />
      </g>
    ),
  },

  current_source: {
    pins: [pinAt(0, -17, '+'), pinAt(0, 17, '−')],
    bbox: { w: 24, h: 34 },
    render: () => (
      <g fill="none" stroke="currentColor" strokeWidth={STROKE}>
        <circle cx={0} cy={0} r={11} />
        <path d="M0 -6 V6 M-3 3 L0 6 L3 3" />
        <line x1={0} y1={-11} x2={0} y2={-17} />
        <line x1={0} y1={11} x2={0} y2={17} />
      </g>
    ),
  },

  // Triode subcircuit — the canonical X-family symbol Circuit Lab needs to
  // render the preamp_12ax7 fixture. Pin order matches the .SUBCKT 12AX7
  // header: plate (top), grid (left), cathode (bottom).
  subcircuit: {
    // Triode pins carry label_side hints so the zoom-gated pin-label pass
    // in Canvas.jsx (rendered when pxPerUnit ≥ PIN_LABEL_PX_PER_UNIT) shows
    // "plate", "grid", "cathode" next to each lead — same convention as
    // .asy-imported parts, just authored by hand because this symbol
    // predates the import pipeline.
    pins: [
      pinAt(0, -22, 'plate',   'top'),
      pinAt(-22, 0, 'grid',    'left'),
      pinAt(0,  22, 'cathode', 'bottom'),
    ],
    bbox: { w: 44, h: 44 },
    render: () => (
      <g fill="none" stroke="currentColor" strokeWidth={STROKE}>
        <circle cx={0} cy={0} r={14} />
        <path d="M-7 -8 H7" />
        <path d="M-9 0 H9" strokeDasharray="1.6 1.4" />
        <path d="M-7 8 L-3 8 L3 8 L7 8" />
        <line x1={0} y1={-14} x2={0} y2={-22} />
        <line x1={0} y1={14} x2={0} y2={22} />
        <line x1={-14} y1={0} x2={-22} y2={0} />
      </g>
    ),
  },
};

// Inner glyph for a voltage source — picks a small mark hinting at the source
// mode. Read-only milestone: just sine, dc, and a fallback. Other modes lower
// to one of these in the netlist for now.
function sourceMarking(mode) {
  switch ((mode || '').toLowerCase()) {
    case 'sin':
      return <path d="M-5 0 q1.7 -4 3.4 0 t3.4 0" fill="none" stroke="currentColor" strokeWidth={STROKE} />;
    case 'dc':
      return (
        <g fill="none" stroke="currentColor" strokeWidth={STROKE}>
          <line x1={-5} y1={-2} x2={5} y2={-2} />
          <line x1={-3} y1={3} x2={3} y2={3} strokeDasharray="1.2 1.2" />
        </g>
      );
    default:
      return <text x={0} y={3} fontSize={8} textAnchor="middle" fill="currentColor">~</text>;
  }
}

/** Rotate (x, y) clockwise by `rot` degrees around the origin. */
export function rotatePoint(x, y, rot) {
  const r = (((rot ?? 0) % 360) + 360) % 360;
  switch (r) {
    case 0: return { x, y };
    case 90: return { x: -y, y: x };
    case 180: return { x: -x, y: -y };
    case 270: return { x: y, y: -x };
    default: {
      const t = (r * Math.PI) / 180;
      return { x: x * Math.cos(t) - y * Math.sin(t), y: x * Math.sin(t) + y * Math.cos(t) };
    }
  }
}

/**
 * Pin world position for one Component, indexed by pin slot. The resolved
 * `sym` (from resolveSymbol) carries the pin coordinates — passing it in
 * rather than re-resolving lets callers iterating many components share a
 * single resolution per component.
 */
export function pinWorld(component, pinIndex, sym) {
  if (!sym || !sym.pins || pinIndex < 0 || pinIndex >= sym.pins.length) return null;
  const local = sym.pins[pinIndex];
  const r = rotatePoint(local.x, local.y, component.layout?.rot);
  const sx = component.layout?.mirror ? -r.x : r.x;
  return {
    x: (component.layout?.x ?? 0) + sx,
    y: (component.layout?.y ?? 0) + r.y,
  };
}

/**
 * Resolve the canvas symbol for a placed component. Imported components with
 * a structured symbol_def (carried on the manifest after an .asy merge) win
 * over the static SYMBOLS fallback; tubes / primitives that have no manifest
 * geometry use SYMBOLS.
 *
 * Returns one of:
 *   { kind: 'manifest', bbox, pins, body }       — server-emitted SVG body
 *   { kind: 'static',   bbox, pins, render }     — JSX render function
 *   null                                          — no match (placeholder)
 *
 * @param {{kind: string, model?: string}} comp
 * @param {Array} library palette snapshot (collapsed or raw — both work)
 */
export function resolveSymbol(comp, library) {
  if (!comp) return null;
  const fam = library ? findFamily(library, comp) : null;
  if (fam?.symbol_def?.body && Array.isArray(fam.symbol_def.pins)) {
    return {
      kind: 'manifest',
      bbox: fam.symbol_def.bbox || { w: 22, h: 14 },
      pins: fam.symbol_def.pins,
      body: fam.symbol_def.body,
    };
  }
  const built = SYMBOLS[comp.kind];
  if (built) {
    return {
      kind: 'static',
      bbox: built.bbox,
      pins: built.pins,
      render: built.render,
    };
  }
  return null;
}

/** Returns true if Circuit Lab knows how to render this kind. */
export function hasSymbol(kind) {
  return Object.prototype.hasOwnProperty.call(SYMBOLS, kind);
}
