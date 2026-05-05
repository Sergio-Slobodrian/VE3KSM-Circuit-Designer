// SignalGenerator — the m10 inspector panel for V/I sources.
//
// The Inspector dispatches here whenever the primary selection is a
// voltage_source or current_source. The panel exposes the eleven waveform
// modes from DESIGN.md §7.1 (mockups/01_schematic_editor.html) — clicking a
// mode button replaces the source's SourceSpec.Mode and seeds Params with
// the catalog defaults; editing a parameter updates Params in place.
//
// Editing flow per `feedback_react_hooks_order.md`: every hook here runs
// before any conditional return, even though the panel never bails out
// mid-render — same shape as the rest of the inspector for safety when a
// future caller short-circuits.
//
// Round-trip note: changing the mode always replaces the entire Params map
// (rather than merging) so a leftover key from the previous mode does not
// silently round-trip through the netlist as an unrecognized *+ key.

import { useEffect, useMemo, useRef, useState } from 'react';
import { useCircuit } from '../../store/index.js';
import { WAVEFORM_MODES, WAVEFORM_BY_KEY, defaultParams } from '../../lib/waveforms.js';
import { importWaveform as apiImportWaveform } from '../../api/client.js';

export default function SignalGenerator({ comp }) {
  const updateComponent = useCircuit((s) => s.updateComponent);

  const source = comp.source || { mode: 'sin', params: defaultParams('sin') };
  const mode = (source.mode || 'sin').toLowerCase();
  const wf = WAVEFORM_BY_KEY[mode] || WAVEFORM_BY_KEY.sin;

  const [importStatus, setImportStatus] = useState(null); // null | 'busy' | 'ok' | 'error'
  const [importMessage, setImportMessage] = useState('');
  const fileInputRef = useRef(null);

  // The catalog drives both UI and lowering, so the inspector form is
  // entirely declarative — no per-mode JSX. The preview path is recomputed
  // on every render because the cost is dwarfed by the SVG's own paint.
  const previewPath = useMemo(() => {
    const points = source.params?._previewPoints;
    return wf.preview(source.params || {}, points);
  }, [wf, source.params]);

  // Reset the import status when the user picks a different source so the
  // confirmation banner doesn't outlive its context.
  useEffect(() => { setImportStatus(null); setImportMessage(''); }, [comp.ref]);

  function pickMode(nextKey) {
    if (nextKey === mode) return;
    updateComponent(comp.ref, {
      source: {
        mode: nextKey,
        params: defaultParams(nextKey),
        ac: source.ac, // preserve any AC stim — orthogonal to the transient mode
      },
    });
  }

  function setParam(key, value) {
    const nextParams = { ...(source.params || {}), [key]: value };
    updateComponent(comp.ref, {
      source: { ...source, params: nextParams },
    });
  }

  async function handleFilePicked(file) {
    if (!file) return;
    setImportStatus('busy');
    setImportMessage('');
    try {
      // peak=0.5 so the imported waveform's swing matches the inspector's
      // Vpp convention: a default-imported file fills 1 Vpp on the scope.
      // The `gain` field in the params multiplies that, so a user wanting
      // the file's full ±1.0 range can just type gain=2.0.
      const res = await apiImportWaveform(file, { peak: 0.5 });
      setImportStatus('ok');
      setImportMessage(
        `Imported ${res.point_count} points from ${res.name} (${formatRate(res.sample_rate)})`,
      );
      // Pick the pwl mode if not already on it, then merge the imported
      // metadata into Params. The actual point data goes in Params["points"]
      // so the netlist round-trip can recover it without another fetch.
      const nextParams = {
        ...(mode === 'pwl' ? source.params : defaultParams('pwl')),
        src: res.name,
        rate: String(Math.round(res.sample_rate)),
        gain: '1.0',
        loop: 'true',
        points: res.points_string,
        // _previewPoints is a transient hint for the preview synth, not a
        // SourceSpec field; it survives in-memory but a netlist round-trip
        // strips it (Params keys not in metaKeysForMode are dropped).
        _previewPoints: res.points,
      };
      updateComponent(comp.ref, {
        source: {
          mode: 'pwl',
          params: nextParams,
          ac: source.ac,
        },
      });
    } catch (err) {
      setImportStatus('error');
      setImportMessage(err?.message || 'Import failed');
    }
  }

  return (
    <div className="siggen">
      <div className="siggen-row" role="toolbar" aria-label="Waveform">
        {WAVEFORM_MODES.map((m) => (
          <button
            key={m.key}
            type="button"
            className={`siggen-btn${m.key === mode ? ' is-active' : ''}`}
            onClick={() => pickMode(m.key)}
            title={m.label}
          >
            <svg width="14" height="10" viewBox="0 0 12 10" fill="none" stroke="currentColor" strokeWidth="1">
              <path d={m.icon} />
            </svg>
            <span>{m.label}</span>
          </button>
        ))}
      </div>

      <div className="siggen-split">
        <div className="siggen-params">
          {wf.params.map((p) => (
            <ParamField
              key={p.key}
              field={p}
              value={source.params?.[p.key] ?? p.default}
              onCommit={(next) => setParam(p.key, next)}
            />
          ))}
        </div>
        <div className="siggen-preview">
          <div className="siggen-preview-head">
            <span>{wf.label}</span>
            <em>{wf.meta(source.params || {})}</em>
          </div>
          <svg viewBox="0 0 200 70" preserveAspectRatio="none">
            <line x1="0" y1="35" x2="200" y2="35" stroke="#1a3a3a" strokeWidth="0.5" strokeDasharray="2 3" />
            <path d={previewPath} stroke="#5fcab0" strokeWidth="1.4" fill="none" />
          </svg>
          <div className="siggen-preview-foot">
            <span>preview</span>
            <span>{(source.ac && `AC ${source.ac.magnitude || '1'}∠${source.ac.phase || '0'}°`) || '—'}</span>
          </div>
        </div>
      </div>

      {mode === 'pwl' && (
        <div className="siggen-import">
          <input
            ref={fileInputRef}
            type="file"
            accept=".csv,.tsv,.txt,.wav"
            style={{ display: 'none' }}
            onChange={(ev) => {
              const f = ev.target.files?.[0];
              ev.target.value = '';
              handleFilePicked(f);
            }}
          />
          <button
            type="button"
            className="siggen-import-btn"
            onClick={() => fileInputRef.current?.click()}
          >
            Import .csv / .wav…
          </button>
          {importStatus && (
            <span className={`siggen-import-status siggen-import-status--${importStatus}`}>
              {importMessage || (importStatus === 'busy' ? 'Importing…' : '')}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

function ParamField({ field, value, onCommit }) {
  if (field.options) {
    return (
      <label className="siggen-param">
        <span className="siggen-param-label">{field.label}</span>
        <span className="siggen-param-input">
          <select
            className="siggen-param-select"
            value={value}
            onChange={(ev) => onCommit(ev.target.value)}
            disabled={field.readOnly}
          >
            {field.options.map((opt) => (
              <option key={opt} value={opt}>{opt}</option>
            ))}
          </select>
        </span>
      </label>
    );
  }
  return (
    <label className="siggen-param">
      <span className="siggen-param-label">{field.label}</span>
      <span className="siggen-param-input">
        <CommittingInput
          value={value ?? ''}
          onCommit={onCommit}
          readOnly={field.readOnly}
        />
        {field.unit ? <span className="siggen-param-unit">{field.unit}</span> : null}
      </span>
    </label>
  );
}

function CommittingInput({ value, onCommit, readOnly }) {
  const [draft, setDraft] = useState(value);
  useEffect(() => { setDraft(value); }, [value]);
  function commit() {
    if (String(draft) === String(value)) return;
    onCommit(String(draft));
  }
  return (
    <input
      type="text"
      className="siggen-param-text"
      value={draft}
      readOnly={readOnly}
      onChange={(ev) => setDraft(ev.target.value)}
      onBlur={commit}
      onKeyDown={(ev) => {
        if (ev.key === 'Enter') { commit(); ev.target.blur(); }
        else if (ev.key === 'Escape') { setDraft(value); ev.target.blur(); }
      }}
    />
  );
}

function formatRate(hz) {
  if (!hz || !Number.isFinite(hz)) return '?';
  if (hz >= 1e6) return `${(hz / 1e6).toFixed(2)} MHz`;
  if (hz >= 1e3) return `${(hz / 1e3).toFixed(1)} kHz`;
  return `${hz.toFixed(0)} Hz`;
}
