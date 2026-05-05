// Side panel for the Network analyzer. Mirrors the Scope's ChannelPanel shape.

import { useCircuit, useNetwork } from '../../store/index.js';

const MODES = [
  { value: 'dec', label: 'Decade' },
  { value: 'oct', label: 'Octave' },
  { value: 'lin', label: 'Linear' },
];

export default function NetworkControls() {
  const circuit = useCircuit((s) => s.circuit);
  const config = useNetwork((s) => s.config);
  const setConfig = useNetwork((s) => s.setConfig);
  const status = useNetwork((s) => s.status);
  const run = useNetwork((s) => s.run);
  const cancel = useNetwork((s) => s.cancel);
  const error = useNetwork((s) => s.error);

  const probes = circuit?.probes || [];
  const sources = (circuit?.components || []).filter((c) => c.kind === 'voltage_source');
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
        <h4>Ports</h4>
        <Row label="Source">
          <select className="ch-coupling" value="" disabled>
            <option value="">{sources.length ? `all V × ac=1` : '— none —'}</option>
          </select>
        </Row>
        <Row label="Probe">
          <select
            className="ch-probe"
            value={config.probeOut ?? ''}
            onChange={(ev) => setConfig({ probeOut: ev.target.value || null })}
          >
            <option value="">— first probe —</option>
            {probes.map((p) => (
              <option key={p.node} value={p.node}>{p.name || p.node}</option>
            ))}
          </select>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Sweep</h4>
        <Row label="Mode">
          <select
            className="ch-coupling"
            value={config.mode}
            onChange={(ev) => setConfig({ mode: ev.target.value })}
          >
            {MODES.map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
          </select>
        </Row>
        <Row label={config.mode === 'lin' ? 'Points' : config.mode === 'oct' ? 'pts/oct' : 'pts/dec'}>
          <input
            type="number"
            min={5}
            max={500}
            value={config.ptsPerDecade}
            onChange={(ev) => setConfig({ ptsPerDecade: Number(ev.target.value) })}
            className="ch-coupling"
            style={{ width: 60 }}
          />
        </Row>
        <Row label="Start">
          <input
            type="number"
            value={config.startHz}
            onChange={(ev) => setConfig({ startHz: Number(ev.target.value) })}
            className="ch-coupling"
            style={{ width: 80 }}
          />
          <span className="muted">Hz</span>
        </Row>
        <Row label="Stop">
          <input
            type="number"
            value={config.stopHz}
            onChange={(ev) => setConfig({ stopHz: Number(ev.target.value) })}
            className="ch-coupling"
            style={{ width: 80 }}
          />
          <span className="muted">Hz</span>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Display</h4>
        <Row label="Group delay">
          <button
            type="button"
            className={`ch-invert ${config.groupDelay ? 'is-on' : ''}`}
            onClick={() => setConfig({ groupDelay: !config.groupDelay })}
          >
            {config.groupDelay ? 'on' : 'off'}
          </button>
        </Row>
      </div>

      <div className="scope-section">
        <h4>Auto markers</h4>
        {Object.entries({
          minus3dB: '−3 dB',
          minus40dB: '−40 dB',
          peak: 'Peak',
          unityGain: 'Unity gain',
          phaseMargin: 'Phase margin',
          gainMargin: 'Gain margin',
        }).map(([k, label]) => (
          <Row key={k} label={label}>
            <button
              type="button"
              className={`ch-invert ${config.autoMarkers[k] ? 'is-on' : ''}`}
              onClick={() => setConfig({ autoMarkers: { ...config.autoMarkers, [k]: !config.autoMarkers[k] } })}
            >
              {config.autoMarkers[k] ? 'on' : 'off'}
            </button>
          </Row>
        ))}
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
