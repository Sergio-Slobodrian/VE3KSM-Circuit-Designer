// Wire routing for the read-only schematic. The Circuit data model has no
// explicit graphical wires (Wires[] is empty for the bundled fixtures —
// netlist nodes are the source of truth), so we synthesize them from
// component-pin connectivity:
//
//   1. Walk every component and emit (node, world-pin) pairs.
//   2. Group by node. Drop "0" — ground is rendered as a glyph at each pin
//      rather than as a wire.
//   3. For each remaining node, route a "spider" of L-shaped paths from each
//      pin toward the centroid of all pins on that node. Two pins horizontally
//      aligned collapse to a clean straight line; three+ pins fan out cleanly.
//
// This is good enough for the canonical preamp_12ax7 fixture and lp_butter
// fixture. Editing milestones will replace this with user-routed wires
// stored in Circuit.Wires.

import { SYMBOLS, pinWorld } from '../../symbols/symbols.jsx';

const GROUND_NODE = '0';

/**
 * @returns {{
 *   wires: Array<{node: string, d: string}>,
 *   junctions: Array<{node: string, x: number, y: number}>,
 *   grounds: Array<{x: number, y: number, rot: number}>,
 *   nodePins: Map<string, Array<{x: number, y: number, ref: string}>>,
 * }}
 */
export function routeWires(circuit) {
  const nodePins = new Map();

  for (const c of circuit?.components || []) {
    const sym = SYMBOLS[c.kind];
    if (!sym) continue;
    (c.nodes || []).forEach((node, i) => {
      if (node == null || node === '') return;
      const w = pinWorld(c, i);
      if (!w) return;
      if (!nodePins.has(node)) nodePins.set(node, []);
      nodePins.get(node).push({ x: w.x, y: w.y, ref: c.ref });
    });
  }

  const wires = [];
  const junctions = [];
  const grounds = [];

  for (const [node, pins] of nodePins) {
    if (node === GROUND_NODE) {
      // Ground — every pin gets its own glyph; no connecting wire.
      for (const p of pins) grounds.push({ x: p.x, y: p.y, rot: 0 });
      continue;
    }
    if (pins.length < 2) continue;

    const cx = mean(pins.map((p) => p.x));
    const cy = mean(pins.map((p) => p.y));

    for (const p of pins) {
      const segs = [];
      // Horizontal run from pin to centroid x at pin y.
      if (Math.abs(p.x - cx) > 0.5) segs.push(`M${p.x} ${p.y} L${cx} ${p.y}`);
      // Vertical run from (cx, pin.y) to (cx, cy).
      if (Math.abs(p.y - cy) > 0.5) segs.push(`M${cx} ${p.y} L${cx} ${cy}`);
      if (segs.length > 0) wires.push({ node, d: segs.join(' ') });
    }

    // Junction dot at the centroid for any 3+ pin node, so the visual
    // intent (multi-way tap) reads at a glance. 2-pin nodes are unambiguous.
    if (pins.length >= 3) junctions.push({ node, x: cx, y: cy });
  }

  return { wires, junctions, grounds, nodePins };
}

function mean(xs) {
  if (xs.length === 0) return 0;
  let s = 0;
  for (const x of xs) s += x;
  return s / xs.length;
}

/**
 * Compute an axis-aligned bounding box that fits every component and wire.
 * Used to pick the SVG viewBox.
 */
export function circuitBounds(circuit) {
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const c of circuit?.components || []) {
    const sym = SYMBOLS[c.kind];
    if (!sym) continue;
    const half = Math.max(sym.bbox.w, sym.bbox.h) / 2 + 6;
    const x = c.layout?.x ?? 0;
    const y = c.layout?.y ?? 0;
    minX = Math.min(minX, x - half);
    minY = Math.min(minY, y - half);
    maxX = Math.max(maxX, x + half);
    maxY = Math.max(maxY, y + half);
  }
  if (!Number.isFinite(minX)) return { x: 0, y: 0, w: 540, h: 290 };
  // Pad enough room for ground glyphs and labels.
  const pad = 32;
  return {
    x: Math.floor(minX - pad),
    y: Math.floor(minY - pad),
    w: Math.ceil(maxX - minX + 2 * pad),
    h: Math.ceil(maxY - minY + 2 * pad),
  };
}
