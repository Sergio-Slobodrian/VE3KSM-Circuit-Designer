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

import { useId, useMemo } from 'react';
import { formatHz } from '../../lib/frequency.js';

const VIEW = 140;
const CX = 70;
const CY = 70;
const R  = 60;

/**
 * @param {{
 *   trace: null | {
 *     freqs: ArrayLike<number>,
 *     gammaRe: ArrayLike<number>,
 *     gammaIm: ArrayLike<number>,
 *     gammaMag: ArrayLike<number>,
 *     vswr: ArrayLike<number>,
 *     returnLossDb: ArrayLike<number>,
 *     zinRe: ArrayLike<number>,
 *     zinIm: ArrayLike<number>,
 *   },
 *   z0: number,
 * }} props
 */
export default function SmithChart({ trace, z0 }) {
  const { pathD, marker, footL, footH } = useMemo(() => buildPath(trace), [trace]);
  const clipId = useId();
  return (
    <div className="smith-pane">
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
        {/* The S11 trace. */}
        {pathD && <path d={pathD} stroke="#f5b840" strokeWidth="1.1" fill="none" />}
        {/* Best-matched marker dot. */}
        {marker && (
          <>
            <circle cx={marker.x} cy={marker.y} r={2.4} fill="#d4537e" />
            <text
              x={marker.x + 4}
              y={marker.y - 4}
              fontFamily="ui-monospace, Menlo, monospace"
              fontSize="8"
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
    </div>
  );
}

/**
 * buildPath converts the parametric S11 trace to an SVG path. Points outside
 * the unit disc (|Γ|>1 — an active circuit reflecting more than incident
 * energy) are clamped to the boundary so the trace doesn't fly off the chart.
 * The returned `marker` sits on the lowest-|Γ| sample (best matched bin); the
 * footer labels are the first and last frequency in the sweep so the chart
 * reads "low f → high f" the way the mockup shows it.
 */
function buildPath(trace) {
  if (!trace || !trace.freqs || trace.freqs.length === 0) {
    return { pathD: '', marker: null, footL: '', footH: '' };
  }
  const { freqs, gammaRe, gammaIm, gammaMag } = trace;
  const n = freqs.length;
  let d = '';
  let bestIdx = -1;
  let bestMag = Infinity;
  for (let i = 0; i < n; i++) {
    let gr = gammaRe[i];
    let gi = gammaIm[i];
    if (!Number.isFinite(gr) || !Number.isFinite(gi)) continue;
    const m = gammaMag[i];
    if (Number.isFinite(m) && m > 1) {
      // Clamp to the boundary so the trace stays inside the chart.
      gr /= m; gi /= m;
    }
    const x = CX + gr * R;
    const y = CY - gi * R;
    d += (d === '' ? 'M' : ' L') + x.toFixed(2) + ' ' + y.toFixed(2);
    if (Number.isFinite(m) && m < bestMag) { bestMag = m; bestIdx = i; }
  }
  let marker = null;
  if (bestIdx >= 0) {
    let gr = gammaRe[bestIdx];
    let gi = gammaIm[bestIdx];
    const m = gammaMag[bestIdx];
    if (m > 1) { gr /= m; gi /= m; }
    marker = { x: CX + gr * R, y: CY - gi * R, freq: freqs[bestIdx], mag: m };
  }
  return {
    pathD: d,
    marker,
    footL: formatHz(freqs[0]),
    footH: formatHz(freqs[n - 1]),
  };
}
