// File toolbar for the Schematic tab — New / Open… / Save. Sits above the
// palette+canvas+inspector grid so it's the first thing the user sees when
// they enter the tab.
//
// Save downloads the circuit as a .cir file via the existing
// /api/circuit/emit endpoint (ngspice dialect, the lossless one). The
// Netlist tab keeps its Export ▾ menu for non-ngspice dialects (LTspice /
// Berkeley3 / KiCad); the toolbar's Save is the one-click "save my work"
// affordance.
//
// Open… reads a .cir file as text, routes it through /api/circuit/parse,
// then openCircuit() so the titlebar's filename label updates in lockstep.

import { useRef, useState } from 'react';
import { useCircuit } from '../../store/index.js';
import { parseNetlist, emitNetlist } from '../../api/client.js';

export default function SchematicToolbar() {
  const circuit = useCircuit((s) => s.circuit);
  const sourceName = useCircuit((s) => s.sourceName);
  const dirty = useCircuit((s) => s.dirty);
  const newCircuit = useCircuit((s) => s.newCircuit);
  const openCircuit = useCircuit((s) => s.openCircuit);
  const fileInputRef = useRef(null);
  const [status, setStatus] = useState(null);

  function onNew() {
    if (dirty && !window.confirm('Discard unsaved changes and start a new schematic?')) {
      return;
    }
    newCircuit();
    setStatus({ kind: 'ok', message: 'New schematic created' });
  }

  function onOpenPick() { fileInputRef.current?.click(); }

  async function onOpenChosen(ev) {
    const file = ev.target.files?.[0];
    ev.target.value = '';
    if (!file) return;
    if (dirty && !window.confirm(`Discard unsaved changes and open ${file.name}?`)) {
      return;
    }
    setStatus({ kind: 'busy', message: `Opening ${file.name}…` });
    try {
      const text = await file.text();
      const parsed = await parseNetlist(text);
      // Strip the .cir extension for the titlebar (it appends `.cir` itself).
      const base = file.name.replace(/\.cir$/i, '');
      openCircuit(base, parsed);
      setStatus({ kind: 'ok', message: `Opened ${file.name}` });
    } catch (err) {
      setStatus({ kind: 'error', message: `Open failed: ${err.message ?? err}` });
    }
  }

  async function onSave() {
    if (!circuit) return;
    setStatus({ kind: 'busy', message: 'Saving…' });
    try {
      const src = await emitNetlist(circuit);
      downloadText(src, `${sourceName || 'untitled'}.cir`);
      setStatus({ kind: 'ok', message: `Saved ${sourceName || 'untitled'}.cir` });
    } catch (err) {
      setStatus({ kind: 'error', message: `Save failed: ${err.message ?? err}` });
    }
  }

  return (
    <div className="schematic-toolbar" role="toolbar" aria-label="File">
      <button type="button" className="schematic-toolbar-btn" onClick={onNew} title="Start a blank schematic (Ctrl+N)">
        New
      </button>
      <button type="button" className="schematic-toolbar-btn" onClick={onOpenPick} title="Open a .cir file">
        Open…
      </button>
      <button
        type="button"
        className="schematic-toolbar-btn"
        onClick={onSave}
        disabled={!circuit}
        title="Download the current schematic as .cir"
      >
        Save
      </button>
      <input
        ref={fileInputRef}
        type="file"
        accept=".cir,.txt,.sp,.spice"
        style={{ display: 'none' }}
        onChange={onOpenChosen}
      />
      {status && (
        <div className={`schematic-toolbar-status schematic-toolbar-status--${status.kind}`}>
          {status.message}
        </div>
      )}
    </div>
  );
}

function downloadText(text, filename) {
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
