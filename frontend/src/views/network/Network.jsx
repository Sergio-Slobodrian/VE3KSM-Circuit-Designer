// Network analyzer tab — milestone 6.
//
// Stacked uPlot panels (magnitude + phase) sharing the log-x frequency axis,
// plus a side panel for sweep config and a bottom strip for auto-markers
// (peak, -3 dB, unity gain, phase margin).
//
// uPlot rather than Plotly because two stacked plots is the canonical Bode
// layout (DESIGN.md §6.4) and uPlot's tiny instances are cheap to mount
// twice. Spectrum keeps Plotly because of its native log axis + marker UX.

import { useEffect, useMemo, useState } from 'react';
import { useCircuit, useNetwork, useUI, defaultProbe, pivotComplexFrames, port1FromPivot } from '../../store/index.js';
import BodePlot from './BodePlot.jsx';
import NetworkControls from './NetworkControls.jsx';
import NetworkReadout from './NetworkReadout.jsx';
import SmithChart, { SmithChartModal } from './SmithChart.jsx';
import Splitter from '../common/Splitter.jsx';
import { findPeak, bandwidth, unityGainCrossover, phaseMargin, gainMargin, groupDelay, wrapPhase, smithTrace } from '../../lib/frequency.js';

export default function Network() {
  const circuit = useCircuit((s) => s.circuit);
  const frames = useNetwork((s) => s.frames);
  const status = useNetwork((s) => s.status);
  const config = useNetwork((s) => s.config);
  const setConfig = useNetwork((s) => s.setConfig);
  const controlPanelWidth = useUI((s) => s.controlPanelWidth);
  const setControlPanelWidth = useUI((s) => s.setControlPanelWidth);

  // Default the output probe to the circuit's first probe whenever the
  // circuit changes and none has been explicitly chosen.
  useEffect(() => {
    if (!config.probeOut && circuit) {
      const dp = defaultProbe(circuit);
      if (dp) setConfig({ probeOut: dp });
    }
  }, [circuit, config.probeOut, setConfig]);

  const pivot = useMemo(() => pivotComplexFrames(frames), [frames]);
  const probe = config.probeOut || defaultProbe(circuit);
  const freqs = pivot.freqs;
  const mag = probe ? pivot.mag.get(probe) : null;
  const phaseRaw = probe ? pivot.phase.get(probe) : null;

  // Wrap phase to (-180, 180] for display. The engine's cph() returns
  // continuous (unwrapped) phase, which is great for derivatives but ugly
  // on a Bode plot.
  const phaseWrapped = useMemo(() => {
    if (!phaseRaw) return null;
    const out = new Float64Array(phaseRaw.length);
    for (let i = 0; i < phaseRaw.length; i++) out[i] = wrapPhase(phaseRaw[i]);
    return out;
  }, [phaseRaw]);

  // Auto-markers — derived once per data update. The `bw40` field uses the
  // same bandwidth() routine as `bw` but with a 40 dB drop, which is the
  // canonical "filter skirt" the RF/audio world quotes for stop-band reach.
  // `gm` is the gain (dB below 0) at the -180° phase crossing.
  const markers = useMemo(() => {
    if (!mag || !phaseRaw || !freqs.length) return null;
    return {
      peak: findPeak(freqs, mag),
      bw: bandwidth(freqs, mag, 3),
      bw40: bandwidth(freqs, mag, 40),
      unity: unityGainCrossover(freqs, mag),
      pm: phaseMargin(freqs, mag, phaseRaw),
      gm: gainMargin(freqs, mag, phaseRaw),
    };
  }, [freqs, mag, phaseRaw]);

  const tau = useMemo(() => {
    if (!config.groupDelay || !phaseRaw || !freqs.length) return null;
    return groupDelay(freqs, phaseRaw);
  }, [freqs, phaseRaw, config.groupDelay]);

  // Milestone 12: derive S11 / VSWR / Zin from the engine's port-1 V & I keys.
  // The trace is null when AC analysis hasn't run (or on a current-driven
  // circuit with no voltage source) — both the Smith inset and the readout
  // gate themselves on the trace being non-null, so the Bode-only audio
  // workflow is unaffected.
  const port1 = useMemo(() => port1FromPivot(pivot), [pivot]);
  const smith = useMemo(() => {
    if (!port1 || !freqs.length || !(config.z0 > 0)) return null;
    return smithTrace(freqs, port1, config.z0);
  }, [port1, freqs, config.z0]);

  // Click-to-expand modal state for the Smith inset. Local to Network — the
  // chart is small + densely-laid-out in the readout strip, the modal gives
  // a usable working size with axis labels.
  const [smithModalOpen, setSmithModalOpen] = useState(false);

  return (
    <div className="spectrum">
      <div className="spectrum-workspace" style={{ '--ctrl-width': `${controlPanelWidth}px` }}>
        <div className="spectrum-screen-pane">
          <div className="spectrum-screen network-stack">
            {!circuit && <div className="scope-overlay">No circuit loaded.</div>}
            {circuit && frames.length === 0 && (
              <div className="scope-overlay">
                {status === 'idle' || status === 'done' || status === 'cancelled'
                  ? 'Press ▶ Run to capture a Bode plot.'
                  : status === 'connecting' ? 'Connecting…'
                  : status === 'loading'    ? 'Loading circuit…'
                  : status === 'running'    ? 'Sweeping…'
                  : status === 'error'      ? 'Run failed.'
                  : ''}
              </div>
            )}
            <BodePlot
              freqs={freqs}
              mag={mag}
              phase={phaseWrapped}
              tau={tau}
              vswr={smith?.vswr ?? null}
              showVSWR={config.showVSWR}
              markers={markers}
              autoMarkers={config.autoMarkers}
              probe={probe}
            />
          </div>
        </div>
        <Splitter width={controlPanelWidth} onChange={setControlPanelWidth} />
        <NetworkControls />
      </div>
      <NetworkReadout
        markers={markers}
        probe={probe}
        autoMarkers={config.autoMarkers}
        smith={smith}
        showVSWR={config.showVSWR}
        z0={config.z0}
      >
        {config.showSmith && smith && (
          <SmithChart
            trace={smith}
            z0={config.z0}
            onExpand={() => setSmithModalOpen(true)}
          />
        )}
      </NetworkReadout>
      {smithModalOpen && smith && (
        <SmithChartModal
          trace={smith}
          z0={config.z0}
          onClose={() => setSmithModalOpen(false)}
        />
      )}
    </div>
  );
}
