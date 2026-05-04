// Auto-marker readouts under the Bode stack. Same .scope-meas-bar shell as
// the Scope's MeasurementBar.

import { formatHz, formatDb } from '../../lib/frequency.js';

/**
 * @param {{
 *   markers: null | {
 *     peak:  null | { freq: number, mag_db: number },
 *     bw:    null | { lo: number, hi: number, bw: number, target_db: number },
 *     unity: null | { freq: number },
 *     pm:    number,
 *   },
 *   probe: string | null,
 * }} props
 */
export default function NetworkReadout({ markers, probe }) {
  if (!markers) {
    return (
      <div className="scope-meas-bar empty">
        Run an AC sweep to populate Bode markers.
      </div>
    );
  }
  return (
    <div className="scope-meas-bar">
      <div className="meas-block meas-cursor">
        <div className="meas-head">{probe || 'probe'}</div>
        <Cell label="peak f"  value={markers.peak ? formatHz(markers.peak.freq)  : '—'} />
        <Cell label="peak G"  value={markers.peak ? formatDb(markers.peak.mag_db): '—'} />
      </div>
      <div className="meas-block">
        <div className="meas-head">−3 dB BW</div>
        <Cell label="lo"   value={markers.bw && Number.isFinite(markers.bw.lo) ? formatHz(markers.bw.lo) : '—'} />
        <Cell label="hi"   value={markers.bw && Number.isFinite(markers.bw.hi) ? formatHz(markers.bw.hi) : '—'} />
        <Cell label="span" value={markers.bw && Number.isFinite(markers.bw.bw) ? formatHz(markers.bw.bw) : '—'} />
      </div>
      <div className="meas-block">
        <div className="meas-head">Unity gain</div>
        <Cell label="f₀ dB"  value={markers.unity ? formatHz(markers.unity.freq) : '—'} />
        <Cell label="phase margin" value={Number.isFinite(markers.pm) ? `${markers.pm.toFixed(1)}°` : '—'} />
      </div>
    </div>
  );
}

function Cell({ label, value }) {
  return (
    <div className="meas-cell">
      <span className="l">{label}</span>
      <span className="v">{value}</span>
    </div>
  );
}
