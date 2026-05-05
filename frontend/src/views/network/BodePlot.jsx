// Two stacked uPlot canvases for the Network/Bode view: magnitude (dB) on
// top and phase (degrees) on the bottom. Both share a log-x frequency axis;
// uPlot doesn't sync cursors across separate instances by itself, so we
// glue them with a shared `cursor.sync` key.
//
// Design choices kept consistent with the Scope tab's ScopeCanvas:
//   - dark scope screen background (--bg-screen) for both panels
//   - cool slate-blue grid colour
//   - uPlot.draw hooks emphasise the 0 dB line (mag) and the -180° line
//     (phase) so a user can read crossings off the trace at a glance
//   - no built-in legend; the side panel and the bottom readout name the trace

import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';

const SYNC_KEY = 'circuitlab-bode-cursor';
const TRACE_COLOR = '#5dcaa5';
const PHASE_COLOR = '#f5b840';
const TAU_COLOR   = 'rgba(212, 83, 126, 0.9)';
const GRID_COLOR  = 'rgba(140, 160, 200, 0.18)';
const ACCENT_LINE = 'rgba(160, 180, 220, 0.45)';

/**
 * @param {{
 *   freqs: ArrayLike<number>,
 *   mag: ArrayLike<number> | null,
 *   phase: ArrayLike<number> | null,
 *   tau: ArrayLike<number> | null,
 *   markers: object | null,
 *   autoMarkers?: object,
 *   probe: string | null,
 * }} props
 */
export default function BodePlot({ freqs, mag, phase, tau, markers, autoMarkers, probe }) {
  const markersRef = useRef({ markers, autoMarkers });
  markersRef.current = { markers, autoMarkers };
  const magRef = useRef(null);
  const phaseRef = useRef(null);
  const magPlot = useRef(/** @type {uPlot|null} */(null));
  const phasePlot = useRef(/** @type {uPlot|null} */(null));

  // (Re)build both plots whenever the trace identity (probe / tau toggle)
  // changes. We rebuild rather than try setData() on a series-shape change
  // because the tau overlay needs a second series on the magnitude plot.
  useEffect(() => {
    if (!magRef.current || !phaseRef.current) return;

    const haveTau = tau && tau.length > 0;
    const baseGrid = { stroke: GRID_COLOR, width: 0.5 };
    const baseAxis = (label) => ({
      stroke: '#7f8a9c',
      grid: baseGrid,
      ticks: { show: true, stroke: GRID_COLOR, size: 4, width: 0.5 },
      label,
      labelSize: 12,
      size: 32,
    });

    const magOpts = {
      width: magRef.current.clientWidth,
      height: magRef.current.clientHeight,
      legend: { show: false },
      cursor: { show: true, sync: { key: SYNC_KEY }, drag: { x: true, y: false } },
      scales: {
        x: { time: false, distr: 3 }, // log
        y: { auto: true },
        tau: { auto: true },
      },
      axes: [
        { ...baseAxis(''), values: (_self, ticks) => ticks.map(formatHzShort) },
        { ...baseAxis('Magnitude (dB)'), scale: 'y' },
        ...(haveTau ? [{ ...baseAxis('τ (s)'), scale: 'tau', side: 1, grid: { show: false } }] : []),
      ],
      series: [
        {},
        {
          label: probe || 'mag',
          stroke: TRACE_COLOR,
          width: 1.5,
          spanGaps: true,
          points: { show: false },
        },
        ...(haveTau ? [{
          label: 'τ',
          stroke: TAU_COLOR,
          width: 1,
          dash: [4, 3],
          scale: 'tau',
          spanGaps: true,
          points: { show: false },
        }] : []),
      ],
      hooks: {
        drawAxes: [
          (u) => emphasizeLevel(u, 0, 'y'),
          (u) => drawMarkers(u, markersRef.current, 'mag'),
        ],
      },
    };

    const phaseOpts = {
      width: phaseRef.current.clientWidth,
      height: phaseRef.current.clientHeight,
      legend: { show: false },
      cursor: { show: true, sync: { key: SYNC_KEY }, drag: { x: true, y: false } },
      scales: {
        x: { time: false, distr: 3 },
        y: { range: () => [-180, 180] },
      },
      axes: [
        { ...baseAxis(''), values: (_self, ticks) => ticks.map(formatHzShort) },
        {
          ...baseAxis('Phase (°)'),
          scale: 'y',
          splits: () => [-180, -135, -90, -45, 0, 45, 90, 135, 180],
        },
      ],
      series: [
        {},
        {
          label: probe || 'phase',
          stroke: PHASE_COLOR,
          width: 1.5,
          spanGaps: true,
          points: { show: false },
        },
      ],
      hooks: {
        drawAxes: [
          (u) => {
            emphasizeLevel(u, 0, 'y');
            emphasizeLevel(u, -180, 'y');
            emphasizeLevel(u, 180, 'y');
          },
          (u) => drawMarkers(u, markersRef.current, 'phase'),
        ],
      },
    };

    if (magPlot.current) { magPlot.current.destroy(); magPlot.current = null; }
    if (phasePlot.current) { phasePlot.current.destroy(); phasePlot.current = null; }

    const empty = (n) => {
      const arrs = [new Float64Array(0)];
      for (let i = 0; i < n; i++) arrs.push(new Float64Array(0));
      return arrs;
    };
    magPlot.current = new uPlot(magOpts, empty(haveTau ? 2 : 1), magRef.current);
    phasePlot.current = new uPlot(phaseOpts, empty(1), phaseRef.current);

    return () => {
      if (magPlot.current) { magPlot.current.destroy(); magPlot.current = null; }
      if (phasePlot.current) { phasePlot.current.destroy(); phasePlot.current = null; }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [probe, tau != null]);

  // Push fresh data + freq window whenever the inputs change.
  useEffect(() => {
    const m = magPlot.current;
    const p = phasePlot.current;
    if (!m || !p) return;

    const xs = Float64Array.from(freqs);
    const haveTau = tau && tau.length > 0;
    const magData = haveTau
      ? [xs, mag ? Float64Array.from(mag) : new Float64Array(xs.length), Float64Array.from(tau)]
      : [xs, mag ? Float64Array.from(mag) : new Float64Array(xs.length)];
    m.setData(magData);
    p.setData([xs, phase ? Float64Array.from(phase) : new Float64Array(xs.length)]);
  }, [freqs, mag, phase, tau]);

  // Resize on container changes.
  useEffect(() => {
    const m = magRef.current;
    const p = phaseRef.current;
    if (!m || !p) return;
    const ro = new ResizeObserver(() => {
      if (magPlot.current)   magPlot.current.setSize({   width: m.clientWidth, height: m.clientHeight });
      if (phasePlot.current) phasePlot.current.setSize({ width: p.clientWidth, height: p.clientHeight });
    });
    ro.observe(m);
    ro.observe(p);
    return () => ro.disconnect();
  }, []);

  // Marker overlays — drawn imperatively from the drawAxes hook above. Force
  // both plots to redraw when the marker payload or the auto-marker toggles
  // change so the overlay updates without a full plot rebuild.
  useEffect(() => {
    if (magPlot.current)   magPlot.current.redraw(false);
    if (phasePlot.current) phasePlot.current.redraw(false);
  }, [markers, autoMarkers]);

  return (
    <div className="bode-stack">
      <div ref={magRef} className="bode-pane bode-pane-mag" />
      <div ref={phaseRef} className="bode-pane bode-pane-phase" />
    </div>
  );
}

/**
 * Draw vertical lines + small dots at the auto-marker frequencies on whichever
 * pane is being painted. Each marker honours the same on/off toggles the
 * readout uses; muted colours so the trace stays the dominant element.
 *
 * pane is 'mag' or 'phase' — distinguishes the two stacked uPlots so we can
 * dot a marker on the magnitude pane only when it has a meaningful y value
 * (peak / unity / -3 dB / -40 dB / gain margin).
 */
function drawMarkers(u, ref, pane) {
  if (!ref || !ref.markers) return;
  const m = ref.markers;
  const am = ref.autoMarkers || {};
  const ctx = u.ctx;
  const top = u.bbox.top;
  const bottom = u.bbox.top + u.bbox.height;
  const xMin = u.scales.x?.min ?? -Infinity;
  const xMax = u.scales.x?.max ??  Infinity;

  const drawVLine = (freq, color, dash) => {
    if (!Number.isFinite(freq) || freq <= 0) return null;
    if (freq < xMin || freq > xMax) return null;
    const x = u.valToPos(freq, 'x', true);
    if (!Number.isFinite(x)) return null;
    ctx.save();
    ctx.strokeStyle = color;
    ctx.lineWidth = 1;
    if (dash) ctx.setLineDash(dash);
    ctx.beginPath();
    ctx.moveTo(x, top);
    ctx.lineTo(x, bottom);
    ctx.stroke();
    ctx.restore();
    return x;
  };
  const drawDot = (x, y, color) => {
    if (!Number.isFinite(x) || !Number.isFinite(y)) return;
    ctx.save();
    ctx.fillStyle = color;
    ctx.beginPath();
    ctx.arc(x, y, 3, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  };

  if (am.peak !== false && m.peak) {
    const x = drawVLine(m.peak.freq, 'rgba(245, 184, 64, 0.55)', null);
    if (pane === 'mag' && x != null) {
      drawDot(x, u.valToPos(m.peak.mag_db, 'y', true), '#f5b840');
    }
  }
  if (am.minus3dB !== false && m.bw) {
    drawVLine(m.bw.lo, 'rgba(150, 200, 255, 0.45)', [3, 3]);
    drawVLine(m.bw.hi, 'rgba(150, 200, 255, 0.45)', [3, 3]);
  }
  if (am.minus40dB === true && m.bw40) {
    drawVLine(m.bw40.lo, 'rgba(150, 200, 255, 0.30)', [2, 4]);
    drawVLine(m.bw40.hi, 'rgba(150, 200, 255, 0.30)', [2, 4]);
  }
  if (am.unityGain !== false && m.unity) {
    const x = drawVLine(m.unity.freq, 'rgba(212, 83, 126, 0.7)', null);
    if (pane === 'mag' && x != null) drawDot(x, u.valToPos(0, 'y', true), '#d4537e');
  }
  if (am.gainMargin !== false && m.gm) {
    const x = drawVLine(m.gm.freq, 'rgba(151, 196, 89, 0.7)', [4, 2]);
    if (pane === 'mag' && x != null) {
      drawDot(x, u.valToPos(m.gm.mag_db, 'y', true), '#97c459');
    }
    if (pane === 'phase' && x != null) {
      drawDot(x, u.valToPos(-180, 'y', true), '#97c459');
    }
  }
}

/**
 * Draw a faint horizontal accent line at value `v` on uPlot scale `scaleKey`.
 * Used to emphasise 0 dB on the magnitude plot and ±180° on the phase plot.
 */
function emphasizeLevel(u, v, scaleKey) {
  const ctx = u.ctx;
  const y = u.valToPos(v, scaleKey, true);
  if (!Number.isFinite(y)) return;
  ctx.save();
  ctx.strokeStyle = ACCENT_LINE;
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(u.bbox.left, y);
  ctx.lineTo(u.bbox.left + u.bbox.width, y);
  ctx.stroke();
  ctx.restore();
}

function formatHzShort(v) {
  const a = Math.abs(v);
  if (a >= 1e9)      return `${(v / 1e9).toFixed(1)}G`;
  if (a >= 1e6)      return `${(v / 1e6).toFixed(1)}M`;
  if (a >= 1e3)      return `${(v / 1e3).toFixed(1)}k`;
  if (a >= 1)        return `${v.toFixed(0)}`;
  return v.toExponential(0);
}
