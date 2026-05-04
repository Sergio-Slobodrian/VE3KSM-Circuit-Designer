// uPlot wrapper for the Scope tab. Owns the imperative uPlot instance, sizes
// it to the parent box, and pushes new data on every prop change.
//
// Each enabled channel is rendered as one uPlot series. We display values in
// "screen divisions" (-4..+4) rather than raw volts so per-channel V/div +
// position controls behave like a real bench scope:
//
//     y_div = ((volt - couplingOffset) / vDiv) + position
//
// The y-axis is hidden (it's an artificial unitless space); the axis legend
// in the side panel and the Channels readout supply the volts/division a user
// reads off a real screen.

import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';

const Y_DIVS = 4;        // screen extends ±4 divisions

/**
 * @param {{
 *   xs: number[],
 *   channels: Array<{
 *     id: string, label: string, color: string,
 *     enabled: boolean, vDiv: number, position: number,
 *     coupling: 'dc'|'ac'|'gnd', invert: boolean,
 *     ys: number[],          // raw volts, parallel to xs
 *     dcMean: number,        // pre-computed channel mean for AC coupling
 *   }>,
 *   timebase: { perDiv: number, position: number },
 *   gridBrightness: number,   // 0..100, controls grid + crosshair alpha
 *   onCursorChange?: (idx: number | null) => void,
 * }} props
 */
export default function ScopeCanvas({ xs, channels, timebase, gridBrightness = 70, onCursorChange }) {
  const cursorRef = useRef(onCursorChange);
  cursorRef.current = onCursorChange;
  const brightness = Math.max(0, Math.min(100, gridBrightness)) / 100;
  // Base colour is a cool slate-blue that contrasts against the trace hues
  // without competing with them. Major grid + crosshair use different alphas
  // off the same base so they read as a hierarchy.
  const gridColor      = `rgba(140, 160, 200, ${(0.10 + brightness * 0.55).toFixed(3)})`;
  const crosshairColor = `rgba(160, 180, 220, ${(0.20 + brightness * 0.65).toFixed(3)})`;
  const containerRef = useRef(null);
  const plotRef = useRef(/** @type {uPlot|null} */(null));

  // (Re)build the plot whenever the channel set, colors, or visibility change.
  // This is heavier than a setData() update, but channel toggles are rare.
  useEffect(() => {
    if (!containerRef.current) return;

    const series = [
      {}, // x
      ...channels.map((ch) => ({
        label: ch.label,
        stroke: ch.color,
        width: 1.5,
        show: ch.enabled,
        points: { show: false },
        spanGaps: true,
        // Hide individual series from the built-in legend; we render our own.
        scale: 'div',
      })),
    ];

    const opts = {
      width: containerRef.current.clientWidth,
      height: containerRef.current.clientHeight,
      legend: { show: false },
      cursor: { show: true, drag: { x: true, y: false } },
      scales: {
        x: { time: false },
        div: { range: () => [-Y_DIVS, Y_DIVS] },
      },
      axes: [
        {
          stroke: '#7f8a9c',
          // Time-axis tick spacing tracks the configured time/div so grid
          // lines fall on the same boundaries the time-base panel reports.
          splits: (_self, _scaleKey, min, max) => buildLinearSplits(min, max, 10),
          grid: { stroke: gridColor, width: 0.5 },
          ticks: { show: true, stroke: gridColor, size: 4, width: 0.5 },
          values: (_self, ticks) => ticks.map((t) => formatTimeShort(t)),
          size: 28,
        },
        {
          scale: 'div',
          stroke: '#7f8a9c',
          // Major lines at every integer division (-4..+4). The y axis is in
          // unitless screen-divisions, so this is the same regardless of the
          // per-channel V/div picked in the side panel.
          splits: () => [-4, -3, -2, -1, 0, 1, 2, 3, 4],
          grid: { stroke: gridColor, width: 0.5 },
          ticks: { show: false },
          values: (_self, ticks) => ticks.map(() => ''),
          size: 18,
        },
      ],
      series,
      hooks: {
        // Surface the cursor's data index back to the parent so the
        // measurement bar can render time + per-channel voltage at the
        // crosshair. Pulled from a ref to avoid rebuilding the plot every
        // time the consumer's callback identity changes.
        setCursor: [
          (u) => {
            const idx = u.cursor.idx;
            cursorRef.current?.(typeof idx === 'number' ? idx : null);
          },
        ],
        // Emphasise the centre lines (0 V on every channel, t=0 on the
        // x axis) so a user reading voltages off the screen has an obvious
        // reference. uPlot's draw hook fires after the axes/grid lines but
        // before the series, so the highlight sits behind the trace.
        drawAxes: [
          (u) => {
            const ctx = u.ctx;
            const plotLeft = u.bbox.left;
            const plotTop = u.bbox.top;
            const plotW = u.bbox.width;
            const plotH = u.bbox.height;
            ctx.save();
            ctx.strokeStyle = crosshairColor;
            ctx.lineWidth = 1;
            // Horizontal centre line (y = 0 div)
            const yMid = u.valToPos(0, 'div', true);
            ctx.beginPath();
            ctx.moveTo(plotLeft, yMid);
            ctx.lineTo(plotLeft + plotW, yMid);
            ctx.stroke();
            // Vertical centre line — the screen midpoint in time, i.e. the
            // current x-axis midpoint regardless of timebase.position.
            const xMin = u.scales.x.min ?? 0;
            const xMax = u.scales.x.max ?? 0;
            const xMid = u.valToPos((xMin + xMax) / 2, 'x', true);
            ctx.beginPath();
            ctx.moveTo(xMid, plotTop);
            ctx.lineTo(xMid, plotTop + plotH);
            ctx.stroke();
            ctx.restore();
          },
        ],
      },
    };

    if (plotRef.current) {
      plotRef.current.destroy();
      plotRef.current = null;
    }
    const empty = [new Float64Array(0), ...channels.map(() => new Float64Array(0))];
    plotRef.current = new uPlot(opts, empty, containerRef.current);

    return () => {
      if (plotRef.current) { plotRef.current.destroy(); plotRef.current = null; }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    channels.length,
    channels.map((c) => `${c.id}:${c.color}:${c.enabled}`).join('|'),
    gridBrightness,
  ]);

  // Push fresh data + adjust the x window to the configured time base whenever
  // any of the relevant inputs change.
  useEffect(() => {
    const plot = plotRef.current;
    if (!plot) return;

    const transformed = channels.map((ch) => transform(ch.ys, ch));
    const data = [Float64Array.from(xs), ...transformed];
    plot.setData(data);

    if (xs.length >= 2) {
      const span = 10 * timebase.perDiv;
      const start = (timebase.position ?? 0) - span / 2 + (xs[0] + xs[xs.length - 1]) / 2;
      plot.setScale('x', { min: start, max: start + span });
    }
  }, [xs, channels, timebase.perDiv, timebase.position]);

  // Resize on container changes — Vite HMR + DevTools panel resizing both
  // need this for the canvas to stay sharp.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      const plot = plotRef.current;
      if (!plot) return;
      plot.setSize({ width: el.clientWidth, height: el.clientHeight });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  return <div ref={containerRef} className="scope-plot" />;
}

/**
 * Transform raw volts → screen divisions for one channel, applying coupling
 * (DC pass-through, AC subtract mean, GND zero), V/div scale, vertical
 * position, and invert.
 */
function transform(ys, ch) {
  const out = new Float64Array(ys.length);
  if (ch.coupling === 'gnd') {
    out.fill(ch.position);
    return out;
  }
  const offset = ch.coupling === 'ac' ? (ch.dcMean ?? 0) : 0;
  const sign = ch.invert ? -1 : 1;
  for (let i = 0; i < ys.length; i++) {
    out[i] = sign * ((ys[i] - offset) / ch.vDiv) + ch.position;
  }
  return out;
}

/**
 * Generate `n` evenly-spaced split values across [min, max], inclusive of
 * both endpoints. uPlot uses splits to place ticks and grid lines, so this
 * keeps the time grid aligned to the configured 10 divisions/screen.
 */
function buildLinearSplits(min, max, n) {
  if (!Number.isFinite(min) || !Number.isFinite(max) || max <= min) return [];
  const out = new Array(n + 1);
  const step = (max - min) / n;
  for (let i = 0; i <= n; i++) out[i] = min + i * step;
  return out;
}

function formatTimeShort(t) {
  const a = Math.abs(t);
  if (a >= 1) return `${t.toFixed(2)} s`;
  if (a >= 1e-3) return `${(t * 1e3).toFixed(2)} ms`;
  if (a >= 1e-6) return `${(t * 1e6).toFixed(0)} µs`;
  if (a >= 1e-9) return `${(t * 1e9).toFixed(0)} ns`;
  return `${t.toExponential(1)} s`;
}
