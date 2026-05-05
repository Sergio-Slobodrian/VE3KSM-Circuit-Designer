// Component palette for the schematic editor. Items are drag-from-here
// sources for the canvas; the kind travels in the dataTransfer envelope
// `application/x-circuit-kind` and the canvas's onDrop instantiates a
// new Component at the cursor (see Canvas.jsx#handleDrop).
//
// As of m9 the entries come from the server's library snapshot
// (/api/library), driven by YAML manifests in the project's library/ tree
// and topped up with user-imported .lib subcircuits. Tubes and other
// subcircuits-from-.lib carry an explicit ModelName and Library reference;
// the canvas reads those from a richer JSON payload on a second MIME type.

import { useRef, useState } from 'react';
import { useUI } from '../../store/index.js';

// Order groups ship in. Anything not in this list ends up at the bottom in
// alphabetical order — that catches new groups (e.g. "BJT", "Diodes")
// gracefully without a code change.
const GROUP_ORDER = ['Passive', 'Sources', 'Active', 'Tubes', 'Imported'];

export default function Palette() {
  const library = useUI((s) => s.library);
  const importLibrary = useUI((s) => s.importLibrary);
  const fileInputRef = useRef(null);
  const [importStatus, setImportStatus] = useState(null);
  const groups = useGrouped(library);

  function onPickFile() { fileInputRef.current?.click(); }

  async function onFileChosen(ev) {
    const file = ev.target.files?.[0];
    ev.target.value = '';
    if (!file) return;
    setImportStatus({ kind: 'busy', message: `Reading ${file.name}…` });
    try {
      const body = await file.text();
      const res = await importLibrary(file.name, body);
      const n = res?.imported?.length ?? 0;
      setImportStatus({
        kind: 'ok',
        message: n === 1
          ? `Imported 1 model from ${file.name}`
          : `Imported ${n} models from ${file.name}`,
      });
    } catch (err) {
      setImportStatus({ kind: 'error', message: String(err.message ?? err) });
    }
  }

  return (
    <div className="palette" aria-label="Component palette">
      {groups.map((g) => (
        <div key={g.label}>
          <div className="palgroup">{g.label}</div>
          {g.items.map((c) => (
            <PaletteItem key={paletteKey(c)} component={c} />
          ))}
        </div>
      ))}
      {groups.length === 0 && <div className="palette-hint">library empty</div>}

      <div className="palgroup palgroup--actions">Library</div>
      <button type="button" className="palette-import" onClick={onPickFile}>
        Import .lib…
      </button>
      <input
        ref={fileInputRef}
        type="file"
        accept=".lib,.cir,.sub,.txt"
        style={{ display: 'none' }}
        onChange={onFileChosen}
      />
      {importStatus && (
        <div className={`palette-import-status palette-import-status--${importStatus.kind}`}>
          {importStatus.message}
        </div>
      )}
      <div className="palette-hint">drag onto canvas to place</div>
    </div>
  );
}

function PaletteItem({ component }) {
  function onDragStart(ev) {
    // Carry both the legacy kind-only payload (Canvas read this since m7) and
    // a richer JSON descriptor that lets the drop handler instantiate
    // model-bearing subcircuits with the right .lib reference.
    ev.dataTransfer.setData('application/x-circuit-kind', component.kind);
    ev.dataTransfer.setData('application/x-circuit-component', JSON.stringify(component));
    ev.dataTransfer.effectAllowed = 'copy';
  }

  return (
    <div
      className="palitem"
      title={component.description || component.kind}
      draggable
      onDragStart={onDragStart}
    >
      <PaletteIcon component={component} />
      <span>{paletteLabel(component)}</span>
    </div>
  );
}

// PaletteIcon renders the manifest's symbol_svg inline. The viewBox shape is
// kind-dependent because the original symbols were authored at slightly
// different aspect ratios; matching them per-kind keeps the icons readable
// instead of squashed into a single canonical box.
function PaletteIcon({ component }) {
  const svg = component.symbol_svg;
  if (!svg) {
    return (
      <svg width={22} height={14} viewBox="0 0 22 14" fill="none" stroke="currentColor" strokeWidth={1}>
        <rect x={2} y={2} width={18} height={10} />
      </svg>
    );
  }
  const viewBox = pickViewBox(component);
  return (
    <svg
      width={22}
      height={viewBox.h}
      viewBox={`0 0 ${viewBox.w} ${viewBox.h}`}
      fill="none"
      stroke="currentColor"
      strokeWidth={1}
      // dangerouslySetInnerHTML is safe here because symbol_svg comes from
      // the server's library manifests — built-ins authored in this repo and
      // import-generated stubs that pick from a closed set of templates.
      // Both produce only <path>/<rect>/<circle>/<line> markup.
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}

// pickViewBox keeps SVG content from being clipped or squashed across kinds.
// 22×10 is the historic two-terminal box; 22×14 is the historic active-device
// box; 22×16 is the tube box (it has top/bottom stubs).
function pickViewBox(c) {
  if (c.group === 'Tubes') return { w: 22, h: 16 };
  if (c.kind === 'resistor' || c.kind === 'capacitor' || c.kind === 'inductor') return { w: 22, h: 10 };
  return { w: 22, h: 14 };
}

// paletteLabel produces the user-visible text under each palette icon. Tubes
// show their model name (e.g. "12AX7"), generic subcircuits also show the
// model name when set, and primitives show their kind in friendly form.
function paletteLabel(c) {
  if (c.model_name) return c.model_name;
  return PRIMITIVE_LABELS[c.kind] ?? c.kind;
}

const PRIMITIVE_LABELS = {
  resistor: 'Resistor',
  capacitor: 'Capacitor',
  inductor: 'Inductor',
  voltage_source: 'Voltage src',
  current_source: 'Current src',
  subcircuit: 'Subcircuit',
};

// paletteKey is the React key — kind alone collides for tubes (every
// imported .SUBCKT has kind="subcircuit"), so disambiguate with model_name.
function paletteKey(c) {
  return c.model_name ? `${c.kind}:${c.model_name}` : c.kind;
}

// useGrouped buckets components by their Group field. Components without a
// group land in "Other". Group display order honours GROUP_ORDER; unknown
// groups get appended alphabetically. Items within a group come back in the
// server's already-sorted order.
function useGrouped(library) {
  const buckets = new Map();
  for (const c of library) {
    const g = c.group || 'Other';
    if (!buckets.has(g)) buckets.set(g, []);
    buckets.get(g).push(c);
  }
  const known = GROUP_ORDER.filter((g) => buckets.has(g));
  const extras = [...buckets.keys()].filter((g) => !GROUP_ORDER.includes(g)).sort();
  return [...known, ...extras].map((label) => ({ label, items: buckets.get(label) }));
}
