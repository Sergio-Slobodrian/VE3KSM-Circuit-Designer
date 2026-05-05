// Side panel for the Spectrum tab. Same visual language as the Scope's
// ChannelPanel — Run/Stop on top, sectioned controls below.

import { useCircuit, useSpectrum } from '../../store/index.js';

const WINDOWS = [
  { value: 'hanning',   label: 'Hann (default)' },
  { value: 'hamming',   label: 'Hamming' },
  { value: 'blackman',  label: 'Blackman-Harris' },
  { value: 'bartlet',   label: 'Bartlett' },
  { value: 'cosine_n',  label: 'Cosine⁴ (flat-top-ish)' },
  { value: 'triangle',  label: 'Triangular' },
  { value: 'none',      label: 'Rectangular (none)' },
];

const SPANS = ['1m', '2m', '5m', '10m', '20m', '50m', '100m'];
const STEPS = ['100n', '500n', '1u', '2u', '5u', '10u'];
const DETECTORS = [
  { value: 'rms',    label: 'RMS (power-correct)' },
  { value: 'peak',   label: 'Peak' },
  { value: 'sample', label: 'Sample' },
  { value: 'avg',    label: 'Average' },
];

export default function SpectrumControls() {
  const circuit = useCircuit((s) => s.circuit);
  const config = useSpectrum((s) => s.config);
  const setConfig = useSpectrum((s) => s.setConfig);
  const status = useSpectrum((s) => s.status);
  const run = useSpectrum((s) => s.run);
  const cancel = useSpectrum((s) => s.cancel);
  const error = useSpectrum((s) => s.error);
  const clearMaxHold = useSpectrum((s) => s.clearMaxHold);

  const probes = circuit?.probes || [];
  const isRunning = status === 'running' || status === 'connecting' || status === 'loading';

  const onRun = () => {
    if (isRunning) { cancel(); return; }
    if (!circuit) return;
    run(circuit);
  };

  return (
    <div className="scope-ctrl">
      <div className="scope-runbar">
        <button
          type="button"
          className={`scope-run ${isRunning ? 'is-running' : ''}`}
          onClick={onRun}
          disabled={!circuit}
        >
          {isRunning ? '■ Stop' : '▶ Run'}
        </button>
      </div>
      {error && <div className="scope-error">{error}</div>}

      <div className="scope-section">
        <h4>Source</h4>
        <Row label="Probe">
          <select
            value={config.probe ?? ''}
            onChange={(ev) => setConfig({ probe: ev.target.value || null })}
            className="ch-probe"
          >
            <option value="">— first probe —</option>
            {probes.map((p) => (
              <option key={p.node} value={p.node}>{p.name || p.node}</option>
            ))}
          </select>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Capture</h4>
        <Row label="Span">
          <select
            value={config.span}
            onChange={(ev) => setConfig({ span: ev.target.value })}
            className="ch-coupling"
          >
            {SPANS.map((s) => <option key={s} value={s}>{s}s</option>)}
          </select>
        </Row>
        <Row label="Step">
          <select
            value={config.step}
            onChange={(ev) => setConfig({ step: ev.target.value })}
            className="ch-coupling"
          >
            {STEPS.map((s) => <option key={s} value={s}>{s}s</option>)}
          </select>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Window</h4>
        <Row label="Type">
          <select
            value={config.window}
            onChange={(ev) => setConfig({ window: ev.target.value })}
            className="ch-coupling"
          >
            {WINDOWS.map((w) => <option key={w.value} value={w.value}>{w.label}</option>)}
          </select>
        </Row>
        <Row label="Detector">
          <select
            value={config.detector}
            onChange={(ev) => setConfig({ detector: ev.target.value })}
            className="ch-coupling"
          >
            {DETECTORS.map((d) => <option key={d.value} value={d.value}>{d.label}</option>)}
          </select>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Trace</h4>
        <Row label="Max-hold">
          <button
            type="button"
            className={`ch-invert ${config.maxHold ? 'is-on' : ''}`}
            onClick={() => setConfig({ maxHold: !config.maxHold })}
          >
            {config.maxHold ? 'on' : 'off'}
          </button>
          <button
            type="button"
            className="scope-auto"
            onClick={clearMaxHold}
            title="Clear max-hold trace"
          >
            clear
          </button>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Markers</h4>
        <div className="scope-row" style={{ flexDirection: 'column', alignItems: 'stretch', gap: 4 }}>
          <span className="muted" style={{ fontSize: 10, color: 'var(--text-tertiary)' }}>
            click trace to set M1; shift-click for M2.
          </span>
        </div>
        <Row label="M1">
          <span className="val">{config.markers.m1 != null ? `${config.markers.m1.toFixed(0)} Hz` : '—'}</span>
          <button type="button" className="scope-auto"
            onClick={() => setConfig({ markers: { ...config.markers, m1: null } })}>×</button>
        </Row>
        <Row label="M2">
          <span className="val">{config.markers.m2 != null ? `${config.markers.m2.toFixed(0)} Hz` : '—'}</span>
          <button type="button" className="scope-auto"
            onClick={() => setConfig({ markers: { ...config.markers, m2: null } })}>×</button>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Distortion</h4>
        <Row label="f₀ (Hz)">
          <input
            type="number"
            min={1}
            value={config.f0}
            onChange={(ev) => setConfig({ f0: Number(ev.target.value) })}
            className="ch-coupling"
            style={{ width: 80 }}
          />
        </Row>
        <Row label="harmonics">
          <input
            type="number"
            min={2}
            max={40}
            value={config.harmonics}
            onChange={(ev) => setConfig({ harmonics: Math.max(2, Math.min(40, Number(ev.target.value) || 10)) })}
            className="ch-coupling"
            style={{ width: 60 }}
          />
        </Row>
        <Row label="track">
          <button
            type="button"
            className={`ch-invert ${config.trackHarmonics ? 'is-on' : ''}`}
            onClick={() => setConfig({ trackHarmonics: !config.trackHarmonics })}
            title="Overlay markers at every harmonic of f₀"
          >
            {config.trackHarmonics ? 'on' : 'off'}
          </button>
        </Row>
      </div>
    </div>
  );
}

function Row({ label, children }) {
  return (
    <div className="scope-row">
      <span className="lab">{label}</span>
      {children}
    </div>
  );
}
