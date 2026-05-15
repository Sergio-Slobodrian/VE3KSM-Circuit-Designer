// Status bar at the bottom of the schematic. Surfaces engine info, node and
// component counts, selection, dirty indicator, and undo/redo buttons. The
// example dropdown still lets the user swap fixtures without leaving the tab.

import { useCircuit, useSelection, useUI } from '../../store/index.js';

export default function StatusBar() {
  const circuit = useCircuit((s) => s.circuit);
  const sourceName = useCircuit((s) => s.sourceName);
  const catalog = useCircuit((s) => s.catalog);
  const load = useCircuit((s) => s.load);
  const dirty = useCircuit((s) => s.dirty);
  const undo = useCircuit((s) => s.undo);
  const redo = useCircuit((s) => s.redo);
  const canUndo = useCircuit((s) => s.history.length > 0);
  const canRedo = useCircuit((s) => s.future.length > 0);
  const selectedRefs = useSelection((s) => s.selectedRefs);
  const backendOnline = useUI((s) => s.backendOnline);

  const nodeCount = countNodes(circuit);
  const compCount = circuit?.components?.length ?? 0;

  const selectionLabel = selectedRefs.length === 0
    ? '—'
    : selectedRefs.length === 1
      ? selectedRefs[0]
      : `${selectedRefs.length} items`;

  return (
    <div className="statusbar">
      <span>Engine: ngspice 42 (subprocess)</span>
      <span>Components: {compCount}</span>
      <span>Nodes: {nodeCount}</span>
      <span>Selected: {selectionLabel}</span>

      <span className="statusbar-edit">
        <button
          type="button"
          className="statusbar-btn"
          disabled={!canUndo}
          onClick={undo}
          title="Undo (Ctrl/⌘ + Z)"
        >
          ↶ Undo
        </button>
        <button
          type="button"
          className="statusbar-btn"
          disabled={!canRedo}
          onClick={redo}
          title="Redo (Ctrl/⌘ + Shift + Z)"
        >
          ↷ Redo
        </button>
      </span>

      {dirty && <span className="status-dirty" title="Edits not yet saved to disk">● modified</span>}

      <span style={{ marginLeft: 'auto' }}>
        <label htmlFor="example-select" className="status-label">File:&nbsp;</label>
        <select
          id="example-select"
          value={sourceName ?? ''}
          onChange={(ev) => {
            const v = ev.target.value;
            // The sentinel value for the current file (or blank) is a no-op;
            // only catalog selections trigger a load.
            if (v && v !== sourceName) load(v);
          }}
          disabled={catalog.length === 0 && !sourceName}
        >
          {/* When the current file isn't an example (user opened a .cir or
              hit New), synthesise an option so the dropdown reflects the
              actual loaded state instead of silently falling back to the
              first catalog entry. */}
          {sourceName && !catalog.some((e) => e.name === sourceName) && (
            <option value={sourceName}>{sourceName} (current)</option>
          )}
          {!sourceName && <option value="">(blank)</option>}
          {catalog.map((e) => (
            <option key={e.name} value={e.name}>{e.title || e.name}</option>
          ))}
        </select>
      </span>
      <span className={backendOnline ? 'status-ok' : 'status-off'}>
        ● {backendOnline ? 'Backend online' : 'Backend offline'}
      </span>
    </div>
  );
}

function countNodes(circuit) {
  if (!circuit?.components) return 0;
  const set = new Set();
  for (const c of circuit.components) for (const n of (c.nodes || [])) if (n) set.add(n);
  return set.size;
}
