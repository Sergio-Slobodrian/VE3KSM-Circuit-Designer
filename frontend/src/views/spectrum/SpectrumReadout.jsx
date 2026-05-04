// THD / SINAD / harmonic readouts for the Spectrum tab. Modeled after
// MeasurementBar — same .scope-meas-bar styling, same per-block grid.

import { formatHz, formatDb, nearestBin } from '../../lib/frequency.js';

/**
 * @param {{
 *   peak: { freq: number, mag_db: number } | null,
 *   markers: { m1: number|null, m2: number|null },
 *   freqs: ArrayLike<number>,
 *   mag: ArrayLike<number> | null,
 *   distortion: {
 *     thdPercent: number, thdDb: number, sinad: number,
 *     harmonics: Array<{ n: number, freq: number, mag_db: number, dbc: number }>,
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
    <div className="scope-meas-bar">
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
        <Cell label="dBc"   value={Number.isFinite(distortion?.thdDb) ? `${distortion.thdDb.toFixed(1)} dB` : '—'} />
        <Cell label="SINAD" value={Number.isFinite(distortion?.sinad) ? `${distortion.sinad.toFixed(1)} dB` : '—'} />
      </div>
      {distortion && distortion.harmonics.length > 0 && (
        <div className="meas-block" style={{ minWidth: 220 }}>
          <div className="meas-head">Harmonics</div>
          {distortion.harmonics.slice(0, 6).map((h) => (
            <Cell
              key={h.n}
              label={`H${h.n} ${formatHz(h.freq)}`}
              value={`${h.dbc.toFixed(1)} dBc`}
            />
          ))}
        </div>
      )}
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

function Cell({ label, value }) {
  return (
    <div className="meas-cell">
      <span className="l">{label}</span>
      <span className="v">{value}</span>
    </div>
  );
}
