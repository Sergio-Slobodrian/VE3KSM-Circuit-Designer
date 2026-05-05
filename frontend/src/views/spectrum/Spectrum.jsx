// Spectrum analyzer tab — milestone 6.
//
// Layout mirrors the Scope tab's three-region structure:
//
//   ┌─────────────────────────────┬─────────────────┐
//   │  Plotly trace (log f / dB)  │  Control panel  │
//   ├─────────────────────────────┴─────────────────┤
//   │  THD / SINAD / harmonic readouts              │
//   └───────────────────────────────────────────────┘
//
// The trace and the readouts re-derive from the committed FFT frames via
// useMemo, so changing the marker / window / detector controls re-runs only
// the cheap derivation, not the FFT itself.

import { useMemo, useEffect } from 'react';
import { useCircuit, useSpectrum, useUI, defaultProbe, pivotComplexFrames } from '../../store/index.js';
import SpectrumPlot from './SpectrumPlot.jsx';
import SpectrumControls from './SpectrumControls.jsx';
import SpectrumReadout from './SpectrumReadout.jsx';
import Splitter from '../common/Splitter.jsx';
import { findPeak, thd, formatHz, formatDb } from '../../lib/frequency.js';

export default function Spectrum() {
  const circuit = useCircuit((s) => s.circuit);
  const frames = useSpectrum((s) => s.frames);
  const maxHoldFrames = useSpectrum((s) => s.maxHoldFrames);
  const config = useSpectrum((s) => s.config);
  const status = useSpectrum((s) => s.status);
  const setConfig = useSpectrum((s) => s.setConfig);
  const controlPanelWidth = useUI((s) => s.controlPanelWidth);
  const setControlPanelWidth = useUI((s) => s.setControlPanelWidth);

  // Default the active probe to the circuit's first probe whenever circuit
  // changes and no probe is selected. Keeps the view non-empty after a
  // circuit swap without overriding an explicit user pick.
  useEffect(() => {
    if (!config.probe && circuit) {
      const dp = defaultProbe(circuit);
      if (dp) setConfig({ probe: dp });
    }
  }, [circuit, config.probe, setConfig]);

  // Pivot frames into per-probe arrays. Recomputed only when frames change —
  // not when the user wiggles a marker.
  const pivot = useMemo(() => pivotComplexFrames(frames), [frames]);
  const holdPivot = useMemo(() => pivotComplexFrames(maxHoldFrames), [maxHoldFrames]);

  const probe = config.probe || defaultProbe(circuit);
  const mag = probe ? pivot.mag.get(probe) : null;
  const phase = probe ? pivot.phase.get(probe) : null;
  const holdMag = probe ? holdPivot.mag.get(probe) : null;
  const freqs = pivot.freqs;

  // Marker / peak / THD computation — cheap, reruns whenever the user moves
  // a marker or changes f0 / harmonics count without re-running the analysis.
  const peak = useMemo(() => (mag && freqs.length ? findPeak(freqs, mag) : null), [freqs, mag]);
  const distortion = useMemo(() => {
    if (!mag || !freqs.length || !(config.f0 > 0)) return null;
    return thd(freqs, mag, config.f0, config.harmonics || 10, phase || null);
  }, [freqs, mag, phase, config.f0, config.harmonics]);

  // Harmonic-tracking markers: when the toggle is on, project the configured
  // f0 + N harmonics into a list of {n, freq, mag_db} so the plot can overlay
  // a small dashed line and dot at each one. Distortion already has the per-
  // harmonic levels — we just gate it on the toggle.
  const trackingMarkers = useMemo(() => {
    if (!config.trackHarmonics || !distortion) return null;
    const out = [];
    if (config.f0 > 0) out.push({ n: 1, freq: config.f0, mag_db: peak?.mag_db ?? NaN });
    for (const h of distortion.harmonics) out.push(h);
    return out;
  }, [config.trackHarmonics, config.f0, distortion, peak]);

  return (
    <div className="spectrum">
      <div className="spectrum-workspace" style={{ '--ctrl-width': `${controlPanelWidth}px` }}>
        <div className="spectrum-screen-pane">
          <div className="spectrum-screen">
            {!circuit && <div className="scope-overlay">No circuit loaded.</div>}
            {circuit && frames.length === 0 && (
              <div className="scope-overlay">
                {status === 'idle' || status === 'done' || status === 'cancelled'
                  ? 'Press ▶ Run to capture a spectrum.'
                  : status === 'connecting' ? 'Connecting…'
                  : status === 'loading'    ? 'Loading circuit…'
                  : status === 'running'    ? 'Computing FFT…'
                  : status === 'error'      ? 'Run failed.'
                  : ''}
              </div>
            )}
            <SpectrumPlot
              freqs={freqs}
              mag={mag}
              holdMag={holdMag}
              probe={probe}
              markers={config.markers}
              peak={peak}
              detector={config.detector}
              harmonicMarkers={trackingMarkers}
              onMarkerSet={(slot, freq) => {
                setConfig({ markers: { ...config.markers, [slot]: freq } });
              }}
            />
            {/* Top-right legend: window + RBW (= 1/captureSec). */}
            <div className="scope-legend scope-legend-top">
              <span>{config.window}</span>
              <span>RBW {formatHz(rbwFromSpan(config.span))}</span>
              {peak && (
                <span style={{ color: 'var(--accent-yellow)' }}>
                  peak {formatHz(peak.freq)} {formatDb(peak.mag_db)}
                </span>
              )}
            </div>
          </div>
        </div>
        <Splitter width={controlPanelWidth} onChange={setControlPanelWidth} />
        <SpectrumControls />
      </div>
      <SpectrumReadout
        peak={peak}
        markers={config.markers}
        freqs={freqs}
        mag={mag}
        distortion={distortion}
        f0={config.f0}
      />
    </div>
  );
}

/**
 * Resolve a SPICE engineering string ("5m", "1u", "20") into a numeric
 * value in seconds. Returns NaN when unparseable. Used to derive the FFT's
 * RBW from the user-configured tran capture span (RBW = 1/T).
 */
export function parseEngTime(s) {
  if (typeof s !== 'string') return NaN;
  const m = s.trim().match(/^([+\-]?\d*\.?\d+(?:e[+\-]?\d+)?)\s*([a-zA-Z]*)$/i);
  if (!m) return NaN;
  const v = parseFloat(m[1]);
  switch (m[2].toLowerCase()) {
    case '':   return v;
    case 's':  return v;
    case 'ms': case 'm': return v * 1e-3;
    case 'us': case 'u': return v * 1e-6;
    case 'ns': case 'n': return v * 1e-9;
    case 'ps': case 'p': return v * 1e-12;
    case 'k':  return v * 1e3;
    default:   return NaN;
  }
}

function rbwFromSpan(span) {
  const T = parseEngTime(span);
  return T > 0 ? 1 / T : NaN;
}
