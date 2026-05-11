// SVG canvas for the schematic editor. Renders the active Circuit's
// components, synthesized wires, ground glyphs, and probes; handles all
// editing pointer interactions for milestone 7:
//
//   - click / shift-click / rubber-band selection
//   - drag selected components (snap to the 14 px grid)
//   - rotate (R), mirror (F), delete (Del/Backspace) keyboard shortcuts
//   - undo / redo (⌘/ctrl-Z, shift-⌘/ctrl-Z)
//   - draw a wire by dragging from one pin to another (unifies node names)
//   - palette drag-and-drop for new components
//
// Pin coordinates come from the rotated symbol bounding box in symbols.jsx;
// hit testing is data-attribute based (`data-hit="component|pin"`) so a
// single pointer-down handler on the SVG resolves the target without each
// element registering its own listener.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { resolveSymbol, pinWorld } from '../../symbols/symbols.jsx';
import { useCircuit, useSelection, useUI } from '../../store/index.js';
import { circuitBounds, routeWires } from './wires.js';
import {
  snap, eventToWorld, findHit, normalizeRect, rectContains,
} from './edit.js';

// Pin hit-zones are slightly larger than the visible pin dot so the user
// doesn't have to land precisely on the centre. Anything inside this radius
// of a pin (in SVG world units) targets that pin.
const PIN_HIT_RADIUS = 5;

export default function Canvas() {
  const svgRef = useRef(null);

  const circuit = useCircuit((s) => s.circuit);
  const status = useCircuit((s) => s.status);
  const error = useCircuit((s) => s.error);
  const moveComponents = useCircuit((s) => s.moveComponents);
  const rotateComponents = useCircuit((s) => s.rotateComponents);
  const mirrorComponents = useCircuit((s) => s.mirrorComponents);
  const deleteComponents = useCircuit((s) => s.deleteComponents);
  const addComponent = useCircuit((s) => s.addComponent);
  const connectPins = useCircuit((s) => s.connectPins);
  const undo = useCircuit((s) => s.undo);
  const redo = useCircuit((s) => s.redo);

  const selectedRefs = useSelection((s) => s.selectedRefs);
  const setSelection = useSelection((s) => s.setSelection);
  const clearSel = useSelection((s) => s.clear);

  // Phase 2: imported components with a structured symbol_def render from the
  // manifest geometry rather than the static SYMBOLS map. We resolve once per
  // render via a memo'd callback so wires.js, ComponentNode, the pin hit-test,
  // and the probe locator all see the same answer for a given component.
  const library = useUI((s) => s.library);
  const symbolFor = useCallback((comp) => resolveSymbol(comp, library), [library]);

  // Active interaction. Held in component state because drag/rubber-band/wire
  // all need preview rendering on every pointer-move; the alternative (refs)
  // would need a forceUpdate dance. The state object is a discriminated union
  // by `kind`.
  //
  //   { kind: 'idle' }
  //   { kind: 'drag',       refs, start, dx, dy }            // moving
  //   { kind: 'rubberband', start, current, additive }
  //   { kind: 'wire',       fromRef, fromPin, fromXY, current, hoverPin? }
  const [interaction, setInteraction] = useState({ kind: 'idle' });
  const [dropHover, setDropHover] = useState(false);

  // Routed wires + ground glyphs + per-node pin lookup, rebuilt on every
  // circuit change. Memoised on circuit identity so dragging a single
  // component doesn't re-route in a tight loop — useState in interaction
  // captures intermediate state without forcing a re-route until commit.
  // The hooks have to fire on every render (Rules of Hooks), so we guard
  // each computation against a null circuit and fall back to safe defaults.
  const routed = useMemo(
    () => (circuit ? routeWires(circuit, library) : { wires: [], junctions: [], grounds: [] }),
    [circuit, library],
  );
  const bounds = useMemo(
    () => (circuit ? circuitBounds(circuit, library) : { x: 0, y: 0, w: 540, h: 290 }),
    [circuit, library],
  );
  const probedNodes = useMemo(
    () => new Set((circuit?.probes || []).map((p) => p.node)),
    [circuit?.probes],
  );
  const selectionSet = useMemo(() => new Set(selectedRefs), [selectedRefs]);

  // Live preview: when the user drags components, apply the in-flight delta
  // to a transient set of overrides so the SVG follows the cursor without
  // committing the move (and pushing onto the undo stack) until pointer-up.
  const dragOverrides = (interaction.kind === 'drag' && (interaction.dx || interaction.dy))
    ? buildDragOverrides(interaction)
    : null;

  // Map ref → effective layout for this render pass.
  const effectiveLayout = (ref, layout) => {
    if (!dragOverrides) return layout;
    const dx = dragOverrides.refs.has(ref) ? dragOverrides.dx : 0;
    const dy = dragOverrides.refs.has(ref) ? dragOverrides.dy : 0;
    if (!dx && !dy) return layout;
    return { ...layout, x: (layout?.x ?? 0) + dx, y: (layout?.y ?? 0) + dy };
  };

  // ---------------------------------------------------------------- pointers

  function handlePointerDown(ev) {
    if (ev.button !== 0) return;
    const w = eventToWorld(svgRef.current, ev);
    if (!w) return;
    const hit = findHit(ev.target, svgRef.current);

    // Pin → start wire-draw. Components inside the pin's parent <g> still
    // resolve as 'pin' because data-hit walks ancestors nearest-first.
    if (hit?.kind === 'pin' && hit.ref) {
      const comp = circuit.components.find((c) => c.ref === hit.ref);
      if (!comp) return;
      const pos = pinWorld(comp, hit.pinIndex, symbolFor(comp));
      if (!pos) return;
      svgRef.current?.setPointerCapture?.(ev.pointerId);
      setInteraction({
        kind: 'wire',
        fromRef: hit.ref, fromPin: hit.pinIndex, fromXY: pos,
        current: w, hoverPin: null,
      });
      ev.preventDefault();
      ev.stopPropagation();
      return;
    }

    // Component → set selection (or shift-toggle), then start drag.
    if (hit?.kind === 'component' && hit.ref) {
      const wasSelected = selectedRefs.includes(hit.ref);
      let nextRefs = selectedRefs;
      if (ev.shiftKey) {
        nextRefs = wasSelected ? selectedRefs.filter((r) => r !== hit.ref) : selectedRefs.concat([hit.ref]);
        setSelection(nextRefs);
      } else if (!wasSelected) {
        nextRefs = [hit.ref];
        setSelection(nextRefs);
      }
      // Shift-click without prior selection: act as toggle, no drag — the
      // user's intent is to grow the set, not to move the just-toggled item.
      if (ev.shiftKey) return;

      svgRef.current?.setPointerCapture?.(ev.pointerId);
      setInteraction({ kind: 'drag', refs: nextRefs, start: w, dx: 0, dy: 0 });
      ev.preventDefault();
      ev.stopPropagation();
      return;
    }

    // Background → start rubber-band. Plain click clears selection; shift-
    // click extends the existing one when the rubber-band commits.
    if (!ev.shiftKey) clearSel();
    svgRef.current?.setPointerCapture?.(ev.pointerId);
    setInteraction({ kind: 'rubberband', start: w, current: w, additive: ev.shiftKey });
  }

  function handlePointerMove(ev) {
    if (interaction.kind === 'idle') return;
    const w = eventToWorld(svgRef.current, ev);
    if (!w) return;
    if (interaction.kind === 'drag') {
      setInteraction({
        ...interaction,
        dx: snap(w.x - interaction.start.x),
        dy: snap(w.y - interaction.start.y),
      });
    } else if (interaction.kind === 'rubberband') {
      setInteraction({ ...interaction, current: w });
    } else if (interaction.kind === 'wire') {
      // Resolve the pin under the cursor, if any — used for the snap preview
      // and to commit the wire on pointer-up.
      const hover = findPinAtWorld(circuit, w, interaction, symbolFor);
      setInteraction({ ...interaction, current: w, hoverPin: hover });
    }
  }

  function handlePointerUp(ev) {
    const w = eventToWorld(svgRef.current, ev);
    if (interaction.kind === 'drag') {
      if (interaction.dx || interaction.dy) {
        moveComponents(interaction.refs, interaction.dx, interaction.dy);
      }
    } else if (interaction.kind === 'rubberband' && w) {
      const r = normalizeRect(interaction.start, w);
      // Skip tiny rectangles — usually a misclick rather than a drag.
      if (r.w >= 2 || r.h >= 2) {
        const inside = (circuit.components || [])
          .filter((c) => rectContains(r, c.layout?.x ?? 0, c.layout?.y ?? 0))
          .map((c) => c.ref);
        if (interaction.additive) {
          setSelection(selectedRefs.concat(inside));
        } else {
          setSelection(inside);
        }
      }
    } else if (interaction.kind === 'wire') {
      const target = interaction.hoverPin || findPinAtWorld(circuit, w, interaction, symbolFor);
      if (target && target.ref !== interaction.fromRef) {
        connectPins(interaction.fromRef, interaction.fromPin, target.ref, target.pinIndex);
      }
    }
    setInteraction({ kind: 'idle' });
  }

  function handlePointerCancel() { setInteraction({ kind: 'idle' }); }

  // ------------------------------------------------------- keyboard shortcuts

  useEffect(() => {
    function onKey(ev) {
      // Ignore keys typed into form fields — the inspector edits values.
      const tag = ev.target?.tagName;
      if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || ev.target?.isContentEditable) return;

      const meta = ev.ctrlKey || ev.metaKey;
      if (meta && (ev.key === 'z' || ev.key === 'Z')) {
        if (ev.shiftKey) redo(); else undo();
        ev.preventDefault();
        return;
      }
      if (meta && (ev.key === 'y' || ev.key === 'Y')) {
        redo();
        ev.preventDefault();
        return;
      }
      if (ev.key === 'Escape') {
        if (interaction.kind !== 'idle') {
          setInteraction({ kind: 'idle' });
        } else {
          clearSel();
        }
        ev.preventDefault();
        return;
      }
      if (selectedRefs.length === 0) return;

      if (ev.key === 'r' || ev.key === 'R') {
        rotateComponents(selectedRefs, ev.shiftKey ? -90 : 90);
        ev.preventDefault();
      } else if (ev.key === 'f' || ev.key === 'F') {
        mirrorComponents(selectedRefs);
        ev.preventDefault();
      } else if (ev.key === 'Delete' || ev.key === 'Backspace') {
        deleteComponents(selectedRefs);
        clearSel();
        ev.preventDefault();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selectedRefs, interaction.kind, rotateComponents, mirrorComponents, deleteComponents, clearSel, undo, redo]);

  // ------------------------------------------------------- palette drop

  function handleDragOver(ev) {
    if (ev.dataTransfer?.types?.includes('application/x-circuit-kind')) {
      ev.preventDefault();
      ev.dataTransfer.dropEffect = 'copy';
      if (!dropHover) setDropHover(true);
    }
  }

  function handleDragLeave() { setDropHover(false); }

  function handleDrop(ev) {
    setDropHover(false);
    const kind = ev.dataTransfer?.getData('application/x-circuit-kind');
    if (!kind) return;
    ev.preventDefault();
    const w = eventToWorld(svgRef.current, ev);
    if (!w) return;

    // The richer JSON payload (m9) carries node_count, model_name, and the
    // .lib file the subcircuit lives in. Drop it onto addComponent; the store
    // falls back to kind-only behaviour when this MIME is absent.
    let manifest = null;
    const raw = ev.dataTransfer.getData('application/x-circuit-component');
    if (raw) {
      try { manifest = JSON.parse(raw); }
      catch { /* malformed — ignore, drop becomes kind-only */ }
    }
    const ref = addComponent({ kind, x: w.x, y: w.y, manifest });
    if (ref) setSelection([ref]);
  }

  // ------------------------------------------------------- render

  // Early returns happen *after* every hook above so the hook count is
  // identical on the loading→ready transition. Calling fewer hooks on the
  // first render would crash with "Rendered more hooks than during the
  // previous render" once the circuit arrives and the full tree mounts.
  if (status === 'loading' || (status === 'idle' && !circuit)) {
    return <CanvasMessage>Loading circuit…</CanvasMessage>;
  }
  if (status === 'error') {
    return <CanvasMessage tone="error">Failed to load: {error}</CanvasMessage>;
  }
  if (!circuit) return <CanvasMessage>No circuit loaded.</CanvasMessage>;

  return (
    <div
      className={`canvas ${dropHover ? 'canvas--drop-hover' : ''}`}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <svg
        ref={svgRef}
        viewBox={`${bounds.x} ${bounds.y} ${bounds.w} ${bounds.h}`}
        preserveAspectRatio="xMidYMid meet"
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerCancel}
        style={{ touchAction: 'none' }}
      >
        <g className="wires" stroke="currentColor" strokeWidth={0.9} fill="none">
          {routed.wires.map((w, i) => <path key={i} d={w.d} />)}
        </g>

        <g className="junctions" fill="currentColor">
          {routed.junctions.map((j, i) => <circle key={i} cx={j.x} cy={j.y} r={1.6} />)}
        </g>

        <g className="grounds">
          {routed.grounds.map((g, i) => <Ground key={i} x={g.x} y={g.y} />)}
        </g>

        <g className="probes">
          {(circuit.probes || []).map((p, i) => (
            <Probe
              key={i}
              probe={p}
              circuit={circuit}
              layoutOverride={effectiveLayout}
              symbolFor={symbolFor}
            />
          ))}
        </g>

        <g className="components">
          {(circuit.components || []).map((c) => (
            <ComponentNode
              key={c.ref}
              comp={c}
              sym={symbolFor(c)}
              layout={effectiveLayout(c.ref, c.layout)}
              selected={selectionSet.has(c.ref)}
            />
          ))}
        </g>

        {/* Probe-node highlight: light blue tint over wires on probed nodes */}
        <g className="probe-overlay" stroke="var(--probe-color)" strokeWidth={1.2} fill="none" opacity={0.55}>
          {routed.wires.filter((w) => probedNodes.has(w.node)).map((w, i) => (
            <path key={i} d={w.d} />
          ))}
        </g>

        {interaction.kind === 'rubberband' && (
          <RubberBand from={interaction.start} to={interaction.current} />
        )}

        {interaction.kind === 'wire' && (
          <WirePreview
            from={interaction.fromXY}
            to={interaction.hoverPin
              ? { x: interaction.hoverPin.x, y: interaction.hoverPin.y }
              : interaction.current}
            snapped={!!interaction.hoverPin}
          />
        )}
      </svg>

      {selectedRefs.length === 0 && circuit.components?.length === 0 && (
        <div className="canvas-hint">Drop a component from the palette to begin.</div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------

function ComponentNode({ comp, sym, layout, selected }) {
  if (!sym) {
    return (
      <g
        data-hit="component"
        data-ref={comp.ref}
        transform={`translate(${layout?.x ?? 0} ${layout?.y ?? 0})`}
        style={{ cursor: 'pointer' }}
      >
        <rect x={-12} y={-8} width={24} height={16} fill="var(--bg-secondary)" stroke="currentColor" strokeWidth={0.6} />
        <text x={0} y={3} fontSize={8} textAnchor="middle" fill="currentColor">{comp.ref}</text>
      </g>
    );
  }
  const x = layout?.x ?? 0;
  const y = layout?.y ?? 0;
  const rot = layout?.rot ?? 0;
  const scale = layout?.mirror ? 'scale(-1 1)' : '';
  const labelOffset = labelPosition(sym, rot);

  return (
    <g
      data-hit="component"
      data-ref={comp.ref}
      transform={`translate(${x} ${y})`}
      style={{ cursor: 'move' }}
    >
      {selected && (
        <rect
          x={-sym.bbox.w / 2 - 4}
          y={-sym.bbox.h / 2 - 4}
          width={sym.bbox.w + 8}
          height={sym.bbox.h + 8}
          fill="none"
          stroke="var(--selection-color)"
          strokeWidth={1}
          strokeDasharray="3 2"
          rx={3}
        />
      )}
      {sym.kind === 'manifest' ? (
        // Manifest body is server-emitted SVG sanitised by
        // backend/internal/library/asy.go's sanitiseSymbolSVG. dangerouslySetInnerHTML
        // is the price of skipping a JSX parser at runtime.
        <g
          transform={`rotate(${rot}) ${scale}`}
          className="symbol-body"
          dangerouslySetInnerHTML={{ __html: sym.body }}
        />
      ) : (
        <g transform={`rotate(${rot}) ${scale}`} className="symbol-body">
          {sym.render(comp)}
        </g>
      )}
      <text
        className="ref-label"
        x={labelOffset.refX}
        y={labelOffset.refY}
        fontSize={10}
        fontFamily="var(--font-mono)"
        fontWeight={500}
        fill="var(--text-primary)"
      >
        {comp.ref}
      </text>
      {comp.value && (
        <text
          className="value-label"
          x={labelOffset.valX}
          y={labelOffset.valY}
          fontSize={9}
          fontFamily="var(--font-mono)"
          fill="var(--text-secondary)"
        >
          {prettyValue(comp.value)}
        </text>
      )}
      {!comp.value && comp.model && (
        <text
          className="value-label"
          x={labelOffset.valX}
          y={labelOffset.valY}
          fontSize={9}
          fontFamily="var(--font-mono)"
          fill="var(--text-secondary)"
        >
          {comp.model}
        </text>
      )}

      {/* Pin hit-zones drawn last so they sit on top of the body. The visible
          dot is small but the click target is wider — see PIN_HIT_RADIUS. */}
      {sym.pins.map((pin, i) => {
        // Local pin position in the unrotated symbol — Canvas's outer <g>
        // already rotates/mirrors, so we render the dot at the local coord.
        const px = layout?.mirror ? -pin.x : pin.x;
        return (
          <g
            key={`pin-${i}`}
            data-hit="pin"
            data-ref={comp.ref}
            data-pin={i}
            transform={`rotate(${rot})`}
            style={{ cursor: 'crosshair' }}
          >
            <circle
              cx={px}
              cy={pin.y}
              r={PIN_HIT_RADIUS}
              fill="transparent"
              stroke="none"
              pointerEvents="all"
            />
            <circle
              cx={px}
              cy={pin.y}
              r={1.3}
              fill="var(--text-tertiary)"
              opacity={0.6}
              pointerEvents="none"
            />
          </g>
        );
      })}
    </g>
  );
}

function Ground({ x, y }) {
  return (
    <g stroke="currentColor" strokeWidth={0.9} fill="none">
      <line x1={x} y1={y} x2={x} y2={y + 6} />
      <line x1={x - 6} y1={y + 6} x2={x + 6} y2={y + 6} />
      <line x1={x - 4} y1={y + 9} x2={x + 4} y2={y + 9} />
      <line x1={x - 2} y1={y + 12} x2={x + 2} y2={y + 12} />
    </g>
  );
}

function Probe({ probe, circuit, layoutOverride, symbolFor }) {
  const pos = locateProbePin(circuit, probe.node, layoutOverride, symbolFor);
  if (!pos) return null;
  const label = probe.name || probe.node;
  return (
    <g className="probe">
      <circle cx={pos.x} cy={pos.y} r={2.4} fill="var(--probe-color)" />
      <path
        d={`M${pos.x} ${pos.y - 14} L${pos.x} ${pos.y - 4} M${pos.x - 5} ${pos.y - 12} L${pos.x + 5} ${pos.y - 12} L${pos.x} ${pos.y - 4} Z`}
        stroke="var(--probe-color)"
        strokeWidth={0.9}
        fill="none"
      />
      <text
        x={pos.x + 6}
        y={pos.y - 10}
        fontSize={10}
        fontFamily="var(--font-mono)"
        fontWeight={500}
        fill="var(--probe-color)"
      >
        {label}
      </text>
    </g>
  );
}

function locateProbePin(circuit, node, layoutOverride, symbolFor) {
  for (const c of circuit?.components || []) {
    if (!c.nodes) continue;
    const sym = symbolFor ? symbolFor(c) : null;
    if (!sym) continue;
    for (let i = 0; i < c.nodes.length; i++) {
      if (c.nodes[i] === node) {
        const layout = layoutOverride ? layoutOverride(c.ref, c.layout) : c.layout;
        const p = pinWorld({ ...c, layout }, i, sym);
        if (p) return p;
      }
    }
  }
  return null;
}

function RubberBand({ from, to }) {
  const r = normalizeRect(from, to);
  return (
    <rect
      className="rubberband"
      x={r.x0}
      y={r.y0}
      width={r.w}
      height={r.h}
      fill="var(--selection-color)"
      fillOpacity={0.08}
      stroke="var(--selection-color)"
      strokeWidth={0.6}
      strokeDasharray="3 2"
      pointerEvents="none"
    />
  );
}

function WirePreview({ from, to, snapped }) {
  // Manhattan-style L: horizontal first, then vertical, matching how the
  // synthesized routing draws connectivity.
  const cx = to.x;
  const d = `M${from.x} ${from.y} L${cx} ${from.y} L${cx} ${to.y}`;
  return (
    <g pointerEvents="none">
      <path d={d} stroke="var(--selection-color)" strokeWidth={1.2} fill="none" strokeDasharray="3 2" />
      <circle cx={to.x} cy={to.y} r={snapped ? 2.6 : 1.8} fill={snapped ? 'var(--selection-color)' : 'var(--text-tertiary)'} />
    </g>
  );
}

// Find a pin under (world.x, world.y), excluding the wire-draw's origin pin
// to avoid self-snapping. Returns { ref, pinIndex, x, y } or null.
function findPinAtWorld(circuit, world, interaction, symbolFor) {
  if (!world || !circuit?.components) return null;
  const r2 = PIN_HIT_RADIUS * PIN_HIT_RADIUS;
  let best = null;
  let bestDist = Infinity;
  for (const c of circuit.components) {
    const sym = symbolFor ? symbolFor(c) : null;
    if (!sym) continue;
    for (let i = 0; i < sym.pins.length; i++) {
      if (interaction?.fromRef === c.ref && interaction?.fromPin === i) continue;
      const p = pinWorld(c, i, sym);
      if (!p) continue;
      const dx = p.x - world.x;
      const dy = p.y - world.y;
      const d = dx * dx + dy * dy;
      if (d <= r2 && d < bestDist) {
        bestDist = d;
        best = { ref: c.ref, pinIndex: i, x: p.x, y: p.y };
      }
    }
  }
  return best;
}

// Translate the in-flight drag delta into a Set + dx + dy used by the render
// pass to nudge selected components to the cursor without committing the
// move (and pushing onto the undo stack) until pointer-up.
function buildDragOverrides(interaction) {
  return {
    refs: new Set(interaction.refs || []),
    dx: interaction.dx || 0,
    dy: interaction.dy || 0,
  };
}

function labelPosition(sym, rot) {
  const isVertical = rot === 90 || rot === 270;
  if (isVertical) {
    return { refX: 10, refY: -2, valX: 10, valY: 10 };
  }
  return { refX: -sym.bbox.w / 2, refY: -sym.bbox.h / 2 - 3, valX: -sym.bbox.w / 2, valY: sym.bbox.h / 2 + 9 };
}

function prettyValue(value) {
  if (!value) return '';
  const m = /^([0-9.]+)\s*([a-zA-Z]+)?$/.exec(value);
  if (!m) return value;
  const num = m[1];
  const suf = (m[2] || '').toLowerCase();
  const map = { meg: 'M', k: 'k', m: 'm', u: 'µ', n: 'n', p: 'p', g: 'G', t: 'T' };
  const label = map[suf] ?? (m[2] || '');
  return `${num}${label ? ' ' + label : ''}`;
}

function CanvasMessage({ children, tone }) {
  return (
    <div className="canvas">
      <div className={`canvas-message ${tone === 'error' ? 'canvas-message--error' : ''}`}>{children}</div>
    </div>
  );
}
