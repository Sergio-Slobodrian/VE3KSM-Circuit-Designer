// Scope tab — milestone 5. Three regions stacked top-to-bottom:
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

import { useMemo, useState } from 'react';
import { useCircuit, useSimulation, useUI } from '../../store/index.js';
import ScopeCanvas from './ScopeCanvas.jsx';
import ChannelPanel from './ChannelPanel.jsx';
import MeasurementBar from './MeasurementBar.jsx';
import { vpp, vrms, mean, frequency, period, phaseDeg, formatEng } from '../../lib/measurements.js';

export default function Scope() {
  const circuit = useCircuit((s) => s.circuit);
  const frames = useSimulation((s) => s.frames);
  const channels = useSimulation((s) => s.channels);
  const timebase = useSimulation((s) => s.timebase);
  const status = useSimulation((s) => s.status);
  const gridBrightness = useUI((s) => s.gridBrightness);

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

  const canvasChannels = channels.map((ch, i) => ({
    ...ch,
    ys: derived[i]?.ys ?? [],
    dcMean: derived[i]?.dcMean ?? 0,
  }));

  const enabledChannels = channels.filter((ch) => ch.enabled && ch.probeNode);

  return (
    <div className="scope">
      <div className="scope-workspace">
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
            </div>
          </div>
        </div>
        <ChannelPanel derived={derived} />
      </div>
      <MeasurementBar derived={derived} cursor={cursorReadout(cursorIdx, xs, derived)} />
    </div>
  );
}

/**
 * Build the cursor readout payload for the measurement bar: the time at the
 * cursor plus a voltage value per enabled channel. Returns null when the
 * cursor is outside the plot or no frames have been captured.
 */
function cursorReadout(idx, xs, derived) {
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
