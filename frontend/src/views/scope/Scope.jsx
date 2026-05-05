// Scope tab — milestone 5 (channels) + milestone 11 (math channels). Three
// regions stacked top-to-bottom:
//
//   ┌─────────────────────────────┬─────────────────┐
//   │  uPlot screen (volts → div) │  Channel panel  │
//   ├─────────────────────────────┴─────────────────┤
//   │  Measurement bar (per-channel readouts)       │
//   └───────────────────────────────────────────────┘
//
// Frames stream into useSimulation; this component re-derives the per-channel
// xs/ys arrays + measurements via useMemo so the work only repeats when the
// frame buffer or channel mapping changes.
//
// Math channels (M1 / M2) compile a user expression like `CH1 - CH2` or
// `INT(CH1)` into a sample-by-sample Float64Array, then flow through the same
// transform / measurement pipeline as physical channels. Display label always
// reads "Mn" so the user can spot them in the legend without reading the
// expression.

import { useMemo, useState } from 'react';
import { useCircuit, useSimulation, useUI } from '../../store/index.js';
import ScopeCanvas from './ScopeCanvas.jsx';
import ChannelPanel from './ChannelPanel.jsx';
import MeasurementBar from './MeasurementBar.jsx';
import Splitter from '../common/Splitter.jsx';
import {
  vpp, vrms, mean, frequency, period, phaseDeg, formatEng,
  compileMathExpression, evaluateMathChannel,
} from '../../lib/measurements.js';

export default function Scope() {
  const circuit = useCircuit((s) => s.circuit);
  const frames = useSimulation((s) => s.frames);
  const channels = useSimulation((s) => s.channels);
  const mathChannels = useSimulation((s) => s.mathChannels);
  const timebase = useSimulation((s) => s.timebase);
  const status = useSimulation((s) => s.status);
  const gridBrightness = useUI((s) => s.gridBrightness);
  const controlPanelWidth = useUI((s) => s.controlPanelWidth);
  const setControlPanelWidth = useUI((s) => s.setControlPanelWidth);

  // Cursor data-index, owned here so the measurement bar can display the
  // sample's time + per-channel voltage. uPlot pushes this via setCursor;
  // null when the pointer leaves the plot area.
  const [cursorIdx, setCursorIdx] = useState(null);

  // Pivot the streamed frames into one xs array + a value-by-node map. The
  // raw frame format is { x, values: { node: volt } } so each node may or
  // may not appear in every frame; we tolerate gaps as NaN.
  const { xs, byNode } = useMemo(() => buildSeries(frames), [frames]);

  // Per-channel derived data: scaled ys + the six measurements. Returns null
  // for disabled or unmapped channels so MeasurementBar can skip them.
  const derived = useMemo(() => {
    return channels.map((ch) => {
      if (!ch.enabled || !ch.probeNode) return null;
      const ys = byNode.get(ch.probeNode);
      if (!ys || ys.length === 0) return null;
      const dcMean = mean(ys);
      const refYs = (channels[0].enabled && channels[0].probeNode)
        ? byNode.get(channels[0].probeNode) : null;
      const ph = (refYs && ch !== channels[0]) ? phaseDeg(xs, refYs, ys) : NaN;
      return {
        kind: 'channel',
        channel: ch,
        ys,
        xs,
        dcMean,
        vpp: vpp(ys),
        vrms: vrms(ys),
        mean: dcMean,
        frequency: frequency(xs, ys),
        period: period(xs, ys),
        phaseDeg: ph,
      };
    });
  }, [channels, xs, byNode]);

  // Math channel derivation: compile each enabled expression once per render,
  // run the per-sample evaluator over the four physical channel arrays, then
  // re-run the standard measurement set. Compile errors surface in the side
  // panel; we render an empty trace for the math channel and skip its block in
  // the measurement bar so the screen stays uncluttered.
  const mathDerived = useMemo(() => {
    const sourceArrays = channels.map((ch) =>
      (ch.enabled && ch.probeNode) ? (byNode.get(ch.probeNode) || null) : null
    );
    return mathChannels.map((m) => {
      if (!m.enabled || !m.expr || !m.expr.trim()) {
        return { kind: 'math', channel: m, error: null, ys: [], xs };
      }
      const compiled = compileMathExpression(m.expr);
      if (compiled.error) {
        return { kind: 'math', channel: m, error: compiled.error, ys: [], xs };
      }
      const ys = evaluateMathChannel(compiled, sourceArrays, xs);
      if (!ys.length) {
        return { kind: 'math', channel: m, error: null, ys: [], xs };
      }
      const dcMean = mean(ys);
      const refYs = (channels[0].enabled && channels[0].probeNode)
        ? byNode.get(channels[0].probeNode) : null;
      const ph = refYs ? phaseDeg(xs, refYs, ys) : NaN;
      return {
        kind: 'math',
        channel: m,
        error: null,
        ys,
        xs,
        dcMean,
        vpp: vpp(ys),
        vrms: vrms(ys),
        mean: dcMean,
        frequency: frequency(xs, ys),
        period: period(xs, ys),
        phaseDeg: ph,
      };
    });
  }, [mathChannels, channels, xs, byNode]);

  // Combined channel list passed to the canvas: physical channels first
  // (CH1..CH4), then enabled math channels. Math channels carry their own
  // coupling / invert / vDiv / position so the user can AC-couple a derived
  // waveform that has a large DC offset (e.g. CH2 - CH1 on a tube anode).
  const canvasChannels = [
    ...channels.map((ch, i) => ({
      ...ch,
      ys: derived[i]?.ys ?? [],
      dcMean: derived[i]?.dcMean ?? 0,
    })),
    ...mathDerived
      .filter((d) => d.channel.enabled && !d.error && d.ys.length > 0)
      .map((d) => ({
        ...d.channel,
        ys: d.ys,
        dcMean: d.dcMean ?? 0,
        enabled: true,
      })),
  ];

  const enabledChannels = channels.filter((ch) => ch.enabled && ch.probeNode);
  const enabledMath = mathDerived.filter((d) => d.channel.enabled && !d.error && d.ys.length > 0);

  return (
    <div className="scope">
      <div className="scope-workspace" style={{ '--ctrl-width': `${controlPanelWidth}px` }}>
        <div className="scope-screen-pane">
          <div className="scope-screen">
            {!circuit && (
              <div className="scope-overlay">No circuit loaded.</div>
            )}
            {circuit && frames.length === 0 && (
              <div className="scope-overlay">
                {status === 'idle' || status === 'done' || status === 'cancelled'
                  ? 'Press ▶ Run to capture a transient.'
                  : status === 'connecting' ? 'Connecting…'
                  : status === 'loading'    ? 'Loading circuit…'
                  : status === 'running'    ? 'Waiting for first sample…'
                  : status === 'error'      ? 'Run failed.'
                  : ''}
              </div>
            )}
            <ScopeCanvas
              xs={xs}
              channels={canvasChannels}
              timebase={timebase}
              gridBrightness={gridBrightness}
              onCursorChange={setCursorIdx}
            />

            {/* On-screen legend strip: per-channel V/div + coupling at the
                bottom, time/div in the top-right. Matches a bench scope's
                bezel readouts so the user can read voltage off the trace by
                counting screen divisions × V/div. */}
            <div className="scope-legend scope-legend-top">
              <span>{formatEng(timebase.perDiv, 's')}/div</span>
            </div>
            <div className="scope-legend scope-legend-bottom">
              {enabledChannels.map((ch) => (
                <span key={ch.id} style={{ color: ch.color }}>
                  {ch.label} {formatEng(ch.vDiv, 'V')}/div · {ch.coupling.toUpperCase()}
                </span>
              ))}
              {enabledMath.map((d) => (
                <span key={d.channel.id} style={{ color: d.channel.color }}>
                  {d.channel.label} {formatEng(d.channel.vDiv, 'V')}/div · {d.channel.coupling.toUpperCase()}{d.channel.invert ? ' · INV' : ''}
                </span>
              ))}
            </div>
          </div>
        </div>
        <Splitter width={controlPanelWidth} onChange={setControlPanelWidth} />
        <ChannelPanel derived={derived} mathDerived={mathDerived} />
      </div>
      <MeasurementBar
        derived={derived}
        mathDerived={mathDerived}
        cursor={cursorReadout(cursorIdx, xs, derived, mathDerived)}
      />
    </div>
  );
}

/**
 * Build the cursor readout payload for the measurement bar: the time at the
 * cursor plus a voltage value per enabled channel (physical + math). Returns
 * null when the cursor is outside the plot or no frames have been captured.
 */
function cursorReadout(idx, xs, derived, mathDerived) {
  if (idx == null || idx < 0 || idx >= xs.length) return null;
  const time = xs[idx];
  const channels = [];
  for (const d of derived) {
    if (!d) continue;
    const v = d.ys[idx];
    if (typeof v !== 'number' || Number.isNaN(v)) continue;
    channels.push({
      label: d.channel.label,
      color: d.channel.color,
      voltage: v,
    });
  }
  for (const d of mathDerived) {
    if (!d || !d.channel.enabled || d.error || !d.ys.length) continue;
    const v = d.ys[idx];
    if (typeof v !== 'number' || Number.isNaN(v)) continue;
    channels.push({
      label: d.channel.label,
      color: d.channel.color,
      voltage: v,
    });
  }
  return { time, channels };
}

/**
 * Build flat xs + per-node ys arrays from the streamed frame buffer.
 * Frames are guaranteed to arrive in time order; we trust that.
 */
function buildSeries(frames) {
  const xs = new Array(frames.length);
  const byNode = new Map();
  for (let i = 0; i < frames.length; i++) {
    const f = frames[i];
    xs[i] = f.x;
    if (!f.values) continue;
    for (const [node, v] of Object.entries(f.values)) {
      let arr = byNode.get(node);
      if (!arr) { arr = new Array(frames.length); byNode.set(node, arr); }
      arr[i] = v;
    }
  }
  return { xs, byNode };
}
