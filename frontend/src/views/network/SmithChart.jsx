// Smith chart for the Network analyzer (milestone 12).
//
// Custom SVG rather than a charting library because the Smith chart is two
// orthogonal families of circles (constant-resistance + constant-reactance),
// not the cartesian/log axes the off-the-shelf libs assume. Drawing the
// graticule by hand is a few dozen lines and keeps the bundle small —
// DESIGN.md §10.3 explicitly notes "Smith chart: custom SVG with parametric
// trace".
//
// Conventions:
//   • The unit circle |Γ|=1 is the chart boundary; the centre is the matched
//     load (Γ = 0, Zin = Z₀).
//   • Resistance circles are centred at (r/(1+r), 0) with radius 1/(1+r).
//   • Reactance arcs are centred at (1, 1/x) with radius 1/x; they're clipped
//     to the unit disc so only the inside-the-chart portion shows.
//   • The trace is parameterised by frequency from low (start of sweep) to
//     high (stop). The marker dot is the bin with the lowest |Γ| — the
//     best-matched point — which reads as "operating sweet spot" without
//     needing a UI selector.
//
// The chart has two display modes:
//   • inline (default) — fits in the Network readout strip; minimal labels.
//   • large            — used inside the click-to-expand modal; renders axis
//     labels for resistance circles + reactance arcs so the chart is
//     actually readable.
//
// Click anywhere on the inline chart to open the larger modal; Esc or
// backdrop click dismisses it.

import { useEffect, useId, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { formatHz } from '../../lib/frequency.js';

const VIEW = 140;
const CX = 70;
const CY = 70;
const R  = 60;

// Fraction of sample points that need to be clamped to the |Γ|=1 boundary
// before we surface the "trace clipped — try a different Z₀" hint. 70% gets
// it for the typical audio-amp-on-50Ω case without firing on a mostly-matched
// RF circuit that happens to skim the edge at a few harmonics.
const CLIP_HINT_THRESHOLD = 0.7;

/**
 * @param {{
 *   trace: null | object,   // smithTrace() output
 *   z0: number,
 *   large?: boolean,        // axis labels + readout strip; default false
 *   onExpand?: () => void,  // when set, the inline chart is clickable
 * }} props
 */
export default function SmithChart({ trace, z0, large = false, onExpand }) {
  const built = useMemo(() => buildPath(trace), [trace]);
  const { pathD, marker, footL, footH, clipFraction, zinSummary } = built;
  const clipId = useId();
  const clickable = !large && typeof onExpand === 'function';

  return (
    <div
      className={`smith-pane${large ? ' smith-pane--large' : ''}${clickable ? ' smith-pane--clickable' : ''}`}
      onClick={clickable ? onExpand : undefined}
      role={clickable ? 'button' : undefined}
      tabIndex={clickable ? 0 : undefined}
      onKeyDown={clickable ? (ev) => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); onExpand(); } } : undefined}
      title={clickable ? 'Click to expand' : undefined}
    >
      <div className="smith-head">
        <span>S₁₁ Smith</span>
        <em>Z₀ = {Number.isFinite(z0) ? z0.toFixed(0) : 50} Ω</em>
      </div>
      <svg viewBox={`0 0 ${VIEW} ${VIEW}`} preserveAspectRatio="xMidYMid meet" className="smith-svg">
        <defs>
          <clipPath id={clipId}>
            <circle cx={CX} cy={CY} r={R} />
          </clipPath>
        </defs>
        {/* Constant-r circles + constant-x arcs, clipped to the unit disc. */}
        <g clipPath={`url(#${clipId})`} stroke="#1a2a35" strokeWidth="0.4" fill="none">
          {[0.2, 0.5, 1, 2, 5].map((r) => {
            const cx = CX + R * r / (1 + r);
            const rad = R / (1 + r);
            return <circle key={`r-${r}`} cx={cx} cy={CY} r={rad} />;
          })}
          {[0.5, 1, 2, 5].map((x) => {
            const off = R / x;
            const rad = R / x;
            return (
              <g key={`x-${x}`}>
                <circle cx={CX + R} cy={CY - off} r={rad} />
                <circle cx={CX + R} cy={CY + off} r={rad} />
              </g>
            );
          })}
        </g>
        {/* Boundary + horizontal axis on top of the graticule. */}
        <g stroke="#2a4050" strokeWidth="0.6" fill="none">
          <circle cx={CX} cy={CY} r={R} />
          <line x1={CX - R} y1={CY} x2={CX + R} y2={CY} />
        </g>
        {/* Axis labels — only rendered in the large variant. Resistance values
            sit just above the horizontal axis on the constant-r circles where
            they cross y = CY. Reactance values sit just outside the unit
            circle at the ±jx points. */}
        {large && (
          <g fontFamily="ui-monospace, Menlo, monospace" fontSize="3.6" fill="#5fa896">
            {/* r-axis labels (where each constant-r circle meets y=CY on its right side) */}
            <text x={CX - R + 1.5} y={CY + 4} textAnchor="start">0</text>
            {[0.2, 0.5, 1, 2, 5].map((r) => {
              const x = CX + R * (r - 1) / (r + 1);
              return (
                <text key={`rt-${r}`} x={x} y={CY + 4} textAnchor="middle">
                  {r}
                </text>
              );
            })}
            <text x={CX + R - 1.5} y={CY + 4} textAnchor="end">∞</text>
            {/* x-axis labels (parametric position of ±jx on |Γ|=1) */}
            {[0.5, 1, 2, 5].map((x) => {
              const denom = 1 + x * x;
              const px = CX + R * (x * x - 1) / denom;
              const py = CY - R * (2 * x) / denom;
              return (
                <g key={`xt-${x}`}>
                  <text x={px} y={py - 1.8} textAnchor="middle">+j{x}</text>
                  <text x={px} y={CY + (CY - py) + 4.6} textAnchor="middle">−j{x}</text>
                </g>
              );
            })}
          </g>
        )}
        {/* The S11 trace. */}
        {pathD && <path d={pathD} stroke="#f5b840" strokeWidth={large ? 0.7 : 1.1} fill="none" />}
        {/* Best-matched marker dot. */}
        {marker && (
          <>
            <circle cx={marker.x} cy={marker.y} r={large ? 1.4 : 2.4} fill="#d4537e" />
            <text
              x={marker.x + (large ? 2 : 4)}
              y={marker.y - (large ? 2 : 4)}
              fontFamily="ui-monospace, Menlo, monospace"
              fontSize={large ? 4 : 8}
              fill="#d4537e"
            >M</text>
          </>
        )}
      </svg>
      <div className="smith-foot">
        <span>{footL || '—'}</span>
        <span>→</span>
        <span>{footH || '—'}</span>
      </div>
      {/* Diagnostic hint when the trace clamps to the boundary on most bins —
          almost always means Z₀ is mismatched for the circuit under test (an
          audio amp's kΩ-scale Zin on the default 50 Ω reference is the
          canonical case). */}
      {clipFraction >= CLIP_HINT_THRESHOLD && (
        <div className="smith-hint">
          Trace clamps to |Γ|=1 boundary.<br />
          Adjust Z₀ to match the circuit's input impedance{zinSummary ? ` (~${zinSummary})` : ''}.
        </div>
      )}
      {clickable && <div className="smith-expand" aria-hidden="true">⛶</div>}
    </div>
  );
}

/**
 * SmithChartModal renders the chart at a large size in a fixed overlay. Click
 * the backdrop or press Esc to close. Mounted via React Portal so it escapes
 * the Network panel's overflow/transform context.
 */
export function SmithChartModal({ trace, z0, onClose }) {
  useEffect(() => {
    function onKey(ev) { if (ev.key === 'Escape') onClose(); }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  function onBackdrop(ev) {
    if (ev.target === ev.currentTarget) onClose();
  }

  return createPortal(
    <div className="smith-modal-backdrop" onClick={onBackdrop} role="dialog" aria-modal="true">
      <div className="smith-modal">
        <button type="button" className="smith-modal-close" onClick={onClose} title="Close (Esc)">×</button>
        <SmithChart trace={trace} z0={z0} large />
      </div>
    </div>,
    document.body,
  );
}

/**
 * buildPath converts the parametric S11 trace to an SVG path. Points outside
 * the unit disc (|Γ|>1 — an active circuit reflecting more than incident
 * energy) are clamped to the boundary so the trace doesn't fly off the chart.
 * The returned `marker` sits on the lowest-|Γ| sample (best matched bin); the
 * footer labels are the first and last frequency in the sweep so the chart
 * reads "low f → high f" the way the mockup shows it. `clipFraction` is the
 * share of bins clamped to or beyond the unit disc and feeds the Z₀-mismatch
 * hint; `zinSummary` is a rough order-of-magnitude string the hint can quote.
 */
function buildPath(trace) {
  if (!trace || !trace.freqs || trace.freqs.length === 0) {
    return { pathD: '', marker: null, footL: '', footH: '', clipFraction: 0, zinSummary: '' };
  }
  const { freqs, gammaRe, gammaIm, gammaMag, zinRe, zinIm } = trace;
  const n = freqs.length;
  let d = '';
  let bestIdx = -1;
  let bestMag = Infinity;
  let clipped = 0;
  let valid = 0;
  let absZSum = 0;
  let absZN = 0;
  for (let i = 0; i < n; i++) {
    let gr = gammaRe[i];
    let gi = gammaIm[i];
    if (!Number.isFinite(gr) || !Number.isFinite(gi)) continue;
    valid++;
    const m = gammaMag[i];
    if (Number.isFinite(m)) {
      if (m >= 0.99) clipped++;
      if (m > 1) { gr /= m; gi /= m; }
    }
    const x = CX + gr * R;
    const y = CY - gi * R;
    d += (d === '' ? 'M' : ' L') + x.toFixed(2) + ' ' + y.toFixed(2);
    if (Number.isFinite(m) && m < bestMag) { bestMag = m; bestIdx = i; }
    if (zinRe && zinIm && Number.isFinite(zinRe[i]) && Number.isFinite(zinIm[i])) {
      absZSum += Math.hypot(zinRe[i], zinIm[i]);
      absZN++;
    }
  }
  let marker = null;
  if (bestIdx >= 0) {
    let gr = gammaRe[bestIdx];
    let gi = gammaIm[bestIdx];
    const m = gammaMag[bestIdx];
    if (m > 1) { gr /= m; gi /= m; }
    marker = { x: CX + gr * R, y: CY - gi * R, freq: freqs[bestIdx], mag: m };
  }
  const clipFraction = valid > 0 ? clipped / valid : 0;
  const zinSummary = absZN > 0 ? formatZinOrder(absZSum / absZN) : '';
  return {
    pathD: d,
    marker,
    footL: formatHz(freqs[0]),
    footH: formatHz(freqs[n - 1]),
    clipFraction,
    zinSummary,
  };
}

// formatZinOrder picks an order-of-magnitude string for the hint text — we
// only need to nudge the user toward the right ballpark for Z₀, not display
// the impedance precisely.
function formatZinOrder(absZ) {
  if (!Number.isFinite(absZ) || absZ <= 0) return '';
  if (absZ >= 1e6) return `${(absZ / 1e6).toFixed(1)} MΩ`;
  if (absZ >= 1e3) return `${(absZ / 1e3).toFixed(1)} kΩ`;
  if (absZ >= 10)  return `${absZ.toFixed(0)} Ω`;
  return `${absZ.toFixed(2)} Ω`;
}
