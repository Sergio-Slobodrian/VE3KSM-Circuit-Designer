// Side panel for the Scope tab: per-channel probe + V/div + coupling + invert,
// math channels (m11), the time-base controls, and the Run/Stop button.
//
// V/div and time/div use a 1-2-5 sequence so adjusting them feels like a real
// scope. The buttons step the current value through the sequence rather than
// editing the raw number.

import { useCircuit, useSimulation, useUI } from '../../store/index.js';
import { formatEng, autoVDiv, autoTimeDiv } from '../../lib/measurements.js';

const STEP_125 = [1, 2, 5];

/**
 * Step a 1-2-5 value up (+1 step) or down (-1).
 */
function step125(value, dir) {
  if (!Number.isFinite(value) || value <= 0) return 1;
  const exp = Math.floor(Math.log10(value));
  const base = Math.pow(10, exp);
  const idx = STEP_125.findIndex((s) => Math.abs(s - value / base) < 0.001);
  if (idx < 0) return value;
  const ni = idx + dir;
  if (ni < 0) return STEP_125[STEP_125.length - 1] * base / 10;
  if (ni >= STEP_125.length) return STEP_125[0] * base * 10;
  return STEP_125[ni] * base;
}

export default function ChannelPanel({ derived, mathDerived }) {
  const circuit = useCircuit((s) => s.circuit);
  const channels = useSimulation((s) => s.channels);
  const setChannel = useSimulation((s) => s.setChannel);
  const mathChannels = useSimulation((s) => s.mathChannels);
  const setMathChannel = useSimulation((s) => s.setMathChannel);
  const timebase = useSimulation((s) => s.timebase);
  const setTimebase = useSimulation((s) => s.setTimebase);
  const status = useSimulation((s) => s.status);
  const run = useSimulation((s) => s.run);
  const cancel = useSimulation((s) => s.cancel);
  const error = useSimulation((s) => s.error);
  const gridBrightness = useUI((s) => s.gridBrightness);
  const setGridBrightness = useUI((s) => s.setGridBrightness);

  const probes = circuit?.probes || [];
  const isRunning = status === 'running' || status === 'connecting' || status === 'loading';

  const onRun = () => {
    if (isRunning) { cancel(); return; }
    if (!circuit) return;
    run(circuit);
  };

  const onAutoFit = () => {
    derived?.forEach((d, i) => {
      if (!d) return;
      setChannel(i, { vDiv: autoVDiv(d.vpp), position: 0 });
    });
    if (derived?.length) {
      const xs = derived.find((d) => d?.xs?.length)?.xs;
      if (xs) setTimebase({ perDiv: autoTimeDiv(xs[xs.length - 1] - xs[0]), position: 0 });
    }
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
        <button type="button" className="scope-auto" onClick={onAutoFit} disabled={!derived?.some((d) => d)}>
          Auto-fit
        </button>
      </div>
      {error && <div className="scope-error">{error}</div>}

      <div className="scope-section">
        <h4>Channels</h4>
        {channels.map((ch, i) => (
          <ChannelRow
            key={ch.id}
            ch={ch}
            probes={probes}
            onChange={(patch) => setChannel(i, patch)}
          />
        ))}
      </div>

      <div className="scope-section">
        <h4>Math (m11)</h4>
        <div className="scope-math-help">
          <span>vars: CH1..CH4 · ops: + − * /</span>
          <span>wrap: <code>INT(…)</code> <code>DIFF(…)</code></span>
        </div>
        {mathChannels.map((m, i) => (
          <MathRow
            key={m.id}
            m={m}
            error={mathDerived?.[i]?.error || null}
            onChange={(patch) => setMathChannel(i, patch)}
          />
        ))}
      </div>

      <div className="scope-section">
        <h4>Time base</h4>
        <Row label="Time/div">
          <Stepper
            value={formatEng(timebase.perDiv, 's')}
            onUp={() => setTimebase({ perDiv: step125(timebase.perDiv, +1) })}
            onDown={() => setTimebase({ perDiv: step125(timebase.perDiv, -1) })}
          />
        </Row>
        <Row label="Position"><span className="val">{formatEng(timebase.position, 's')}</span></Row>
      </div>

      <div className="scope-section">
        <h4>Trigger</h4>
        <Row label="Source"><span className="val muted">CH1 (m6)</span></Row>
        <Row label="Mode"><span className="val muted">auto</span></Row>
      </div>

      <div className="scope-section">
        <h4>Display</h4>
        <Row label="Grid">
          <input
            type="range"
            min={0}
            max={100}
            step={1}
            value={gridBrightness}
            onChange={(ev) => setGridBrightness(Number(ev.target.value))}
            className="grid-brightness"
            aria-label="Grid brightness"
          />
          <span className="val">{gridBrightness}</span>
        </Row>
      </div>
    </div>
  );
}

function ChannelRow({ ch, probes, onChange }) {
  return (
    <div className="scope-ch">
      <div className="scope-ch-head">
        <span className="ch-dot" style={{ background: ch.color, opacity: ch.enabled ? 1 : 0.35 }} />
        <span className="ch-name">{ch.label}</span>
        <select
          className="ch-probe"
          value={ch.probeNode ?? ''}
          onChange={(ev) => {
            const v = ev.target.value;
            onChange({ probeNode: v || null, enabled: !!v });
          }}
        >
          <option value="">— off —</option>
          {probes.map((p) => (
            <option key={p.node} value={p.node}>{p.name || p.node}</option>
          ))}
        </select>
      </div>
      <div className="scope-ch-row">
        <Stepper
          value={formatEng(ch.vDiv, 'V')}
          onUp={() => onChange({ vDiv: step125(ch.vDiv, +1) })}
          onDown={() => onChange({ vDiv: step125(ch.vDiv, -1) })}
        />
        <select
          className="ch-coupling"
          value={ch.coupling}
          onChange={(ev) => onChange({ coupling: ev.target.value })}
        >
          <option value="dc">DC</option>
          <option value="ac">AC</option>
          <option value="gnd">GND</option>
        </select>
        <button
          type="button"
          className={`ch-invert ${ch.invert ? 'is-on' : ''}`}
          onClick={() => onChange({ invert: !ch.invert })}
          title="Invert"
        >
          inv
        </button>
      </div>
    </div>
  );
}

function MathRow({ m, error, onChange }) {
  return (
    <div className="scope-ch scope-math">
      <div className="scope-ch-head">
        <span className="ch-dot" style={{ background: m.color, opacity: m.enabled ? 1 : 0.35 }} />
        <span className="ch-name">{m.label}</span>
        <input
          type="text"
          className="ch-probe ch-math-expr"
          placeholder="e.g. CH1 - CH2"
          value={m.expr}
          spellCheck={false}
          autoCorrect="off"
          autoCapitalize="off"
          onChange={(ev) => onChange({ expr: ev.target.value })}
        />
      </div>
      <div className="scope-ch-row">
        <Stepper
          value={formatEng(m.vDiv, 'V')}
          onUp={() => onChange({ vDiv: step125(m.vDiv, +1) })}
          onDown={() => onChange({ vDiv: step125(m.vDiv, -1) })}
        />
        <select
          className="ch-coupling"
          value={m.coupling}
          onChange={(ev) => onChange({ coupling: ev.target.value })}
          title="DC: pass through · AC: subtract mean · GND: zero"
        >
          <option value="dc">DC</option>
          <option value="ac">AC</option>
          <option value="gnd">GND</option>
        </select>
        <button
          type="button"
          className={`ch-invert ${m.invert ? 'is-on' : ''}`}
          onClick={() => onChange({ invert: !m.invert })}
          title="Invert"
        >
          inv
        </button>
        <button
          type="button"
          className={`ch-invert ${m.enabled ? 'is-on' : ''}`}
          onClick={() => onChange({ enabled: !m.enabled })}
          title={m.enabled ? 'disable math channel' : 'enable math channel'}
        >
          {m.enabled ? 'on' : 'off'}
        </button>
      </div>
      {error && <div className="scope-math-err" title={error}>parse: {error}</div>}
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

function Stepper({ value, onUp, onDown }) {
  return (
    <span className="stepper">
      <button type="button" onClick={onDown} title="Decrease">−</button>
      <span className="val">{value}</span>
      <button type="button" onClick={onUp} title="Increase">+</button>
    </span>
  );
}
