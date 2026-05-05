// THD / THD+N / SINAD / SNR + marker readouts for the Spectrum tab.
// Modeled after MeasurementBar — same .scope-meas-bar styling, same
// per-block grid. The harmonic table renders as its own row beneath the
// main strip when distortion data is available so the columns can breathe.

import { formatHz, formatDb, nearestBin } from '../../lib/frequency.js';

/**
 * @param {{
 *   peak: { freq: number, mag_db: number } | null,
 *   markers: { m1: number|null, m2: number|null },
 *   freqs: ArrayLike<number>,
 *   mag: ArrayLike<number> | null,
 *   distortion: {
 *     thdPercent: number, thdDb: number,
 *     thdPlusNDb: number, thdPlusNPercent: number,
 *     snrDb: number, sinad: number,
 *     harmonics: Array<{ n: number, freq: number, mag_db: number, dbc: number, percent: number, phase_deg: number }>,
 *   } | null,
 *   f0: number,
 * }} props
 */
export default function SpectrumReadout({ peak, markers, freqs, mag, distortion, f0 }) {
  if (!mag || !freqs?.length) {
    return (
      <div className="scope-meas-bar empty">
        Run an analysis to populate spectrum readouts.
      </div>
    );
  }

  const m1Db = markerDb(freqs, mag, markers?.m1);
  const m2Db = markerDb(freqs, mag, markers?.m2);
  const delta = (markers?.m1 != null && markers?.m2 != null && Number.isFinite(m1Db) && Number.isFinite(m2Db))
    ? { dHz: markers.m2 - markers.m1, dDb: m2Db - m1Db }
    : null;

  return (
    <div className="scope-meas-bar scope-meas-stack">
      <div className="scope-meas-row">
        <div className="meas-block meas-cursor">
          <div className="meas-head">Peak</div>
          <Cell label="freq"  value={peak ? formatHz(peak.freq)  : '—'} />
          <Cell label="level" value={peak ? formatDb(peak.mag_db): '—'} />
        </div>
        <div className="meas-block">
          <div className="meas-head">M1</div>
          <Cell label="freq"  value={markers.m1 != null ? formatHz(markers.m1) : '—'} />
          <Cell label="level" value={Number.isFinite(m1Db) ? formatDb(m1Db) : '—'} />
        </div>
        <div className="meas-block">
          <div className="meas-head">M2 / Δ</div>
          <Cell label="freq"  value={markers.m2 != null ? formatHz(markers.m2) : '—'} />
          <Cell label="level" value={Number.isFinite(m2Db) ? formatDb(m2Db) : '—'} />
          <Cell label="Δf" value={delta ? formatHz(delta.dHz) : '—'} />
          <Cell label="Δdb" value={delta ? formatDb(delta.dDb) : '—'} />
        </div>
        <div className="meas-block">
          <div className="meas-head">THD @ {formatHz(f0)}</div>
          <Cell label="THD"   value={fmtPct(distortion?.thdPercent)} />
          <Cell label="THD dB" value={fmtDbValue(distortion?.thdDb)} />
        </div>
        <div className="meas-block">
          <div className="meas-head">THD + N</div>
          <Cell label="ratio" value={fmtPct(distortion?.thdPlusNPercent)} />
          <Cell label="dB"    value={fmtDbValue(distortion?.thdPlusNDb)} />
          <Cell label="SINAD" value={fmtDbValue(distortion?.sinad)} />
        </div>
        <div className="meas-block">
          <div className="meas-head">SNR</div>
          <Cell label="ex-h"  value={fmtDbValue(distortion?.snrDb)} />
        </div>
      </div>
      {distortion && distortion.harmonics.length > 0 && (
        <HarmonicTable harmonics={distortion.harmonics} />
      )}
    </div>
  );
}

/**
 * Tabular harmonic readout: one row per harmonic with absolute level,
 * relative dBc, percent, and phase. Up to 10 rows; horizontal scroll if more.
 */
function HarmonicTable({ harmonics }) {
  return (
    <div className="harmonic-table">
      <div className="harmonic-row harmonic-head">
        <span>n</span>
        <span>freq</span>
        <span>level</span>
        <span>dBc</span>
        <span>%</span>
        <span>phase</span>
      </div>
      {harmonics.map((h) => (
        <div className="harmonic-row" key={h.n}>
          <span>H{h.n}</span>
          <span>{formatHz(h.freq)}</span>
          <span>{formatDb(h.mag_db)}</span>
          <span>{Number.isFinite(h.dbc) ? `${h.dbc.toFixed(1)} dBc` : '—'}</span>
          <span>{fmtPct(h.percent)}</span>
          <span>{Number.isFinite(h.phase_deg) ? `${h.phase_deg.toFixed(0)}°` : '—'}</span>
        </div>
      ))}
    </div>
  );
}

function markerDb(freqs, mag, f) {
  if (f == null || !mag || !freqs?.length) return NaN;
  const idx = nearestBin(freqs, f);
  return idx >= 0 ? mag[idx] : NaN;
}

function fmtPct(v) {
  if (!Number.isFinite(v)) return '—';
  if (v < 0.01) return `${(v * 1000).toFixed(2)} ‰`;
  if (v < 1)    return `${v.toFixed(3)} %`;
  return `${v.toFixed(2)} %`;
}

function fmtDbValue(v) {
  return Number.isFinite(v) ? `${v.toFixed(1)} dB` : '—';
}

function Cell({ label, value }) {
  return (
    <div className="meas-cell">
      <span className="l">{label}</span>
      <span className="v">{value}</span>
    </div>
  );
}
