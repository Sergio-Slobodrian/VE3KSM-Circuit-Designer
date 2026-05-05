// Netlist tab — DESIGN.md §6.5. Wraps the editor + outline + status bar and
// owns the bidirectional sync between the SPICE text and the active Circuit.
//
// Sync model (per project_milestones.md m8 plan):
//   1. The schematic is canonical — circuit changes flow into text via the
//      backend's Emit (POST /api/circuit/emit). We trigger this whenever the
//      circuit reference in useCircuit changes from the one we last parsed
//      from text.
//   2. The user typing into the editor is the un-canonical side. After a
//      350ms idle window we POST /api/circuit/parse; on success the parsed
//      Circuit is fed into useCircuit.replaceCircuit, which updates the
//      schematic + sim tabs through normal Zustand subscription. We also
//      remember the new circuit ref so the schematic-to-text effect doesn't
//      immediately overwrite the text with a freshly-emitted copy.
//   3. The Sync button bypasses the user-edit branch — emit and replace,
//      discarding any pending text edits.

import { useEffect, useMemo, useRef, useState } from 'react';
import { useCircuit } from '../../store/index.js';
import { emitNetlist, parseNetlist, exportNetlist } from '../../api/client.js';
import NetlistEditor from './NetlistEditor.jsx';
import NetlistOutline from './NetlistOutline.jsx';

const DEBOUNCE_MS = 350;

export default function Netlist() {
  const circuit = useCircuit((s) => s.circuit);
  const status = useCircuit((s) => s.status);
  const sourceName = useCircuit((s) => s.sourceName);
  const replaceCircuit = useCircuit((s) => s.replaceCircuit);

  const [text, setText] = useState('');
  const [target, setTarget] = useState('ngspice');
  const [error, setError] = useState(null); // { line, message } | null
  const [sync, setSync] = useState({ state: 'in_sync' });
  const [lastEditAt, setLastEditAt] = useState(Date.now());
  const [lastEditSide, setLastEditSide] = useState('schematic'); // 'schematic' | 'text'
  const [tick, setTick] = useState(0); // re-render so "12s ago" stays fresh

  // Tracks the Circuit reference whose emit produced the current `text`.
  // When useCircuit.circuit !== syncedRef.current, the schematic-to-text
  // effect re-emits. Updated both by the schematic→text path and by the
  // text→schematic parse path.
  const syncedRef = useRef(null);
  const debounceRef = useRef(null);

  // Schematic → text. Runs whenever the circuit changes from a source other
  // than this tab's parse. While the user has un-parsed text edits we surface
  // an "out of sync" badge instead of overwriting their work — they can hit
  // ↻ Sync to discard the text edits explicitly.
  const syncStateRef = useRef(sync.state);
  syncStateRef.current = sync.state;
  useEffect(() => {
    if (!circuit || circuit === syncedRef.current) return;
    if (syncStateRef.current === 'editing') {
      setSync({ state: 'error', detail: 'schematic edited externally — Sync to refresh' });
      return undefined;
    }
    let cancelled = false;
    (async () => {
      try {
        const src = await emitNetlist(circuit);
        if (cancelled) return;
        setText(src);
        setError(null);
        setSync({ state: 'in_sync' });
        setLastEditSide('schematic');
        setLastEditAt(Date.now());
        syncedRef.current = circuit;
      } catch (err) {
        if (!cancelled) setError({ line: 0, message: `emit failed: ${err.message}` });
      }
    })();
    return () => { cancelled = true; };
  }, [circuit]);

  // Tick a re-render every second so the relative "Last edit" label refreshes
  // without polling state. Cheap; one setState per second.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);
  void tick;

  // Cancel any pending parse on unmount so we don't dispatch after teardown.
  useEffect(() => () => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
  }, []);

  // Text → schematic. Debounced parse-then-replace. On parse failure we keep
  // the text as-is and surface the line/column in the gutter + status bar.
  const onTextChange = (next) => {
    setText(next);
    setSync({ state: 'editing' });
    setLastEditSide('text');
    setLastEditAt(Date.now());
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      debounceRef.current = null;
      runParse(next, /*applyOnSuccess*/ true);
    }, DEBOUNCE_MS);
  };

  // runParse is shared by the debounced auto-parse and the toolbar Validate.
  // applyOnSuccess controls whether we feed the parse result into useCircuit.
  const runParse = async (src, applyOnSuccess = true) => {
    try {
      const parsed = await parseNetlist(src);
      setError(null);
      if (applyOnSuccess) {
        syncedRef.current = parsed;
        replaceCircuit(parsed);
      }
      setSync({ state: 'in_sync' });
      return true;
    } catch (err) {
      const m = parseSpiceError(err.message);
      setError(m);
      setSync({ state: 'error', detail: m.message });
      return false;
    }
  };

  const onValidate = () => { runParse(text, /*applyOnSuccess*/ false); };

  const onSync = async () => {
    if (!circuit) return;
    try {
      const src = await emitNetlist(circuit);
      setText(src);
      setError(null);
      setSync({ state: 'in_sync' });
      setLastEditSide('schematic');
      setLastEditAt(Date.now());
      syncedRef.current = circuit;
    } catch (err) {
      setError({ line: 0, message: `emit failed: ${err.message}` });
    }
  };

  // Format = "safe whitespace normalization, no reorder" (DESIGN.md §6.5).
  // We piggyback on parse + emit when the text round-trips cleanly: the
  // emitter has stable, opinionated whitespace so this gives the user a
  // canonical layout without writing a new normaliser. If the text has
  // unsaved errors, we leave it alone.
  const onFormat = async () => {
    try {
      const parsed = await parseNetlist(text);
      const src = await emitNetlist(parsed);
      setText(src);
      setError(null);
      setSync({ state: 'in_sync' });
      setLastEditSide('text');
      setLastEditAt(Date.now());
      syncedRef.current = parsed;
      replaceCircuit(parsed);
    } catch (err) {
      const m = parseSpiceError(err.message);
      setError(m);
      setSync({ state: 'error', detail: m.message });
    }
  };

  const onExport = async () => {
    if (!circuit) return;
    try {
      const src = await exportNetlist(circuit, target);
      downloadText(src, `${sourceName || 'circuit'}.${exportExtension(target)}`);
    } catch (err) {
      setError({ line: 0, message: `export failed: ${err.message}` });
    }
  };

  const toolbar = useMemo(() => ([
    { label: 'Format',   onClick: onFormat,   primary: true, title: 'Normalize whitespace via parse → emit round-trip' },
    { label: 'Validate', onClick: onValidate, title: 'Re-parse and report any syntax errors' },
    { label: '↻ Sync',   onClick: onSync,     title: 'Force-reload from schematic, discarding text edits' },
    { label: `Export ▾`, onClick: onExport,   title: `Download as ${target}` },
  // eslint-disable-next-line react-hooks/exhaustive-deps
  ]), [text, circuit, target]);

  if (status === 'loading') {
    return <div className="ne-loading">Loading…</div>;
  }
  if (status === 'error' || !circuit) {
    return (
      <div className="ne-loading">
        {status === 'error' ? 'Failed to load circuit.' : 'No circuit loaded.'}
      </div>
    );
  }

  const dialect = target === 'ngspice' ? 'ngspice 42' :
                  target === 'berkeley3' ? 'Berkeley SPICE 3' :
                  target === 'ltspice' ? 'LTspice' : 'KiCad';
  const statusLabel = sync.state === 'in_sync'
    ? `⇄ in sync · ${dialect}`
    : sync.state === 'editing'
      ? `… editing · ${dialect}`
      : `✗ ${sync.detail} · ${dialect}`;

  const elementCount = (circuit.components || []).length;
  const nodeCount = countNodes(circuit);

  return (
    <div className="netlist">
      <div className="ne-workspace">
        <div className="ne-pane">
          <NetlistEditor
            text={text}
            onChange={onTextChange}
            errorMark={error && error.line > 0 ? error : null}
            statusLabel={statusLabel}
            toolbar={toolbar}
          />
        </div>
        <NetlistOutline
          circuit={circuit}
          sync={sync}
          lastEdit={`${lastEditSide} ${formatRelative(lastEditAt)}`}
          target={target}
          onTargetChange={setTarget}
        />
      </div>

      <div className="ne-statline">
        <Cell label="Parser"   value={statusBadge(sync, error)} primary />
        <Cell label="Elements" value={`${elementCount} · ${nodeCount} nodes`} />
        <Cell label="Sync"     value={lastEditSide === 'schematic' ? '← schematic' : '→ schematic'} />
        <Cell label="Target"   value={dialect} />
      </div>
    </div>
  );
}

function Cell({ label, value, primary }) {
  return (
    <div className={`ne-stcell ${primary ? '' : 'is-aux'}`}>
      <span className="ne-stcell-l">{label}</span>
      <span className="ne-stcell-v">{value}</span>
    </div>
  );
}

function statusBadge(sync, error) {
  if (sync.state === 'in_sync') return '✓ ok · 0 warn';
  if (sync.state === 'editing') return '… reparse pending';
  return `✗ line ${error?.line ?? '?'}`;
}

function countNodes(c) {
  const set = new Set();
  for (const comp of c.components || []) {
    for (const n of comp.nodes || []) set.add(n);
  }
  return set.size;
}

// parseSpiceError pulls a 1-based line number out of the backend's parser
// error message. The Go parser formats failures as "line N: …". For non-
// matching messages we drop line=0 so the gutter shows no marker.
function parseSpiceError(message) {
  const m = /line (\d+)\s*:\s*(.*)$/m.exec(message || '');
  if (m) return { line: parseInt(m[1], 10), message: m[2].trim() };
  // Backend wraps responses as JSON {code, message}; surface the message.
  let inner = message || 'parse error';
  try {
    const parsed = JSON.parse(message);
    if (parsed && typeof parsed.message === 'string') inner = parsed.message;
  } catch { /* not JSON; leave inner alone */ }
  const m2 = /line (\d+)\s*:\s*(.*)$/m.exec(inner);
  if (m2) return { line: parseInt(m2[1], 10), message: m2[2].trim() };
  return { line: 0, message: inner };
}

function formatRelative(ts) {
  const dt = Math.max(0, Math.round((Date.now() - ts) / 1000));
  if (dt < 60) return `${dt}s`;
  if (dt < 3600) return `${Math.round(dt / 60)}m`;
  return `${Math.round(dt / 3600)}h`;
}

function exportExtension(target) {
  if (target === 'ltspice') return 'asc.cir';
  if (target === 'kicad')   return 'kicad.cir';
  return 'cir';
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
