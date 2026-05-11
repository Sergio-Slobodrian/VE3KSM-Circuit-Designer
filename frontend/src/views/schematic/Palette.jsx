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

import { useMemo, useRef, useState } from 'react';
import { useUI } from '../../store/index.js';
import { collapseLibrary, familyLabel } from '../../lib/library.js';

// Order groups ship in. Anything not in this list ends up at the bottom in
// alphabetical order — that catches new groups (e.g. "BJT", "Diodes")
// gracefully without a code change.
const GROUP_ORDER = ['Passive', 'Sources', 'Active', 'Tubes', 'Imported'];

export default function Palette() {
  const library = useUI((s) => s.library);
  const importLibrary = useUI((s) => s.importLibrary);
  const importLibraryArchive = useUI((s) => s.importLibraryArchive);
  const fileInputRef = useRef(null);
  const [importStatus, setImportStatus] = useState(null);
  // Collapse multi-variant imported families into a single row before
  // grouping. The drag payload still carries the variants list so the
  // dropped component can be configured via the inspector dropdown.
  const collapsed = useMemo(() => collapseLibrary(library), [library]);
  const groups = useGrouped(collapsed);

  function onPickFile() { fileInputRef.current?.click(); }

  async function onFileChosen(ev) {
    const file = ev.target.files?.[0];
    ev.target.value = '';
    if (!file) return;
    const isZip = /\.zip$/i.test(file.name);
    // Archive imports walk hundreds of files server-side; warn the user
    // explicitly so a multi-second wait doesn't read like a frontend stall.
    setImportStatus({
      kind: 'busy',
      message: isZip
        ? `Uploading ${file.name} (this may take a moment)…`
        : `Reading ${file.name}…`,
    });
    try {
      if (isZip) {
        const res = await importLibraryArchive(file);
        setImportStatus({ kind: 'ok', message: archiveBannerText(file.name, res) });
      } else {
        const body = await file.text();
        const res = await importLibrary(file.name, body);
        setImportStatus({ kind: 'ok', message: importBannerText(file.name, res) });
      }
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
        Import library…
      </button>
      <input
        ref={fileInputRef}
        type="file"
        accept=".lib,.cir,.sub,.txt,.asy,.zip"
        style={{ display: 'none' }}
        onChange={onFileChosen}
      />
      {importStatus && (
        <div className={`palette-import-status palette-import-status--${importStatus.kind}`}>
          {importStatus.message}
        </div>
      )}
      <div className="palette-hint">.lib · .asy · .zip pack — drag onto canvas to place</div>
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

// PaletteIcon renders the manifest's symbol inline. Two shapes are supported:
//   1. Structured `symbol_def` (.asy converter output) — coordinates are
//      canvas-local around (0,0), so the SVG viewBox is centred. Padded by
//      ICON_PAD on each side so the body doesn't touch the icon edges.
//   2. Legacy flat `symbol_svg` (built-in YAML or m9 import stubs) — drawn at
//      a kind-specific top-left viewBox to match historic authoring.
function PaletteIcon({ component }) {
  if (component.symbol_def?.body) {
    return <StructuredIcon symbol={component.symbol_def} />;
  }
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

// ICON_PAD pads the structured-symbol viewBox so strokes near the bbox edge
// aren't clipped by the icon container.
const ICON_PAD = 2;

function StructuredIcon({ symbol }) {
  const w = Math.max(symbol.bbox?.w ?? 22, 1);
  const h = Math.max(symbol.bbox?.h ?? 22, 1);
  const vbW = w + ICON_PAD * 2;
  const vbH = h + ICON_PAD * 2;
  const target = 22;
  // Maintain aspect: pick a render height proportional to the source.
  const renderW = target;
  const renderH = Math.max(10, Math.round((target * vbH) / vbW));
  return (
    <svg
      width={renderW}
      height={renderH}
      viewBox={`${-vbW / 2} ${-vbH / 2} ${vbW} ${vbH}`}
      fill="none"
      stroke="currentColor"
      strokeWidth={0.9}
      // Structured body is server-generated SVG sanitised to the conservative
      // allow-list in backend/internal/library/asy.go (only g/line/rect/circle/
      // ellipse/path with a fixed attribute set), so the dangerouslySetInnerHTML
      // payload is safe by construction.
      dangerouslySetInnerHTML={{ __html: symbol.body }}
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

// importBannerText composes the post-import status message. Accounts for both
// .lib uploads (Imported is non-empty) and .asy uploads (Updated is non-empty),
// since each path conveys a different user-facing change.
function importBannerText(filename, res) {
  const imported = res?.imported?.length ?? 0;
  const updated = res?.updated?.length ?? 0;
  if (updated > 0 && imported === 0) {
    return updated === 1
      ? `Updated 1 symbol from ${filename}`
      : `Updated ${updated} symbols from ${filename}`;
  }
  return imported === 1
    ? `Imported 1 model from ${filename}`
    : `Imported ${imported} models from ${filename}`;
}

// archiveBannerText reports both halves of a zip-pack import (new models +
// enriched symbols) plus a per-file warning count when something inside the
// pack failed to ingest. The user can still see the imported subset even if
// a few entries were rejected — that's the whole point of accumulating
// warnings rather than aborting.
function archiveBannerText(filename, res) {
  const imported = res?.imported?.length ?? 0;
  const updated = res?.updated?.length ?? 0;
  const warnings = res?.warnings?.length ?? 0;
  const parts = [];
  parts.push(`${imported} model${imported === 1 ? '' : 's'}`);
  if (updated > 0) {
    parts.push(`${updated} symbol${updated === 1 ? '' : 's'}`);
  }
  let msg = `Imported ${parts.join(' + ')} from ${filename}`;
  if (warnings > 0) {
    msg += ` (${warnings} warning${warnings === 1 ? '' : 's'})`;
  }
  return msg;
}

// paletteLabel produces the user-visible text under each palette icon.
// Collapsed families (multi-variant .lib imports) show the .lib basename;
// tubes and other model-bearing entries show their model name; primitives
// show their kind in friendly form.
function paletteLabel(c) {
  if (Array.isArray(c.variants) && c.variants.length > 1) return familyLabel(c);
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
// Collapsed families key on the .lib path since one row covers many models.
function paletteKey(c) {
  if (Array.isArray(c.variants) && c.variants.length > 1) {
    return `${c.kind}:lib:${c.library}`;
  }
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
