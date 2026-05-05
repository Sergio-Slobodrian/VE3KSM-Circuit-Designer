// Auto-marker readouts under the Bode stack. Same .scope-meas-bar shell as
// the Scope's MeasurementBar. Each block is gated by the side-panel toggle
// in `autoMarkers` so the bar shrinks when fewer measurements are wanted.

import { formatHz, formatDb } from '../../lib/frequency.js';

/**
 * @param {{
 *   markers: null | {
 *     peak:  null | { freq: number, mag_db: number },
 *     bw:    null | { lo: number, hi: number, bw: number, target_db: number },
 *     bw40:  null | { lo: number, hi: number, bw: number, target_db: number },
 *     unity: null | { freq: number },
 *     pm:    number,
 *     gm:    null | { freq: number, mag_db: number, gm_db: number },
 *   },
 *   autoMarkers?: { minus3dB?: boolean, peak?: boolean, unityGain?: boolean,
 *                   phaseMargin?: boolean, minus40dB?: boolean, gainMargin?: boolean },
 *   probe: string | null,
 * }} props
 */
export default function NetworkReadout({ markers, probe, autoMarkers = {} }) {
  if (!markers) {
    return (
      <div className="scope-meas-bar empty">
        Run an AC sweep to populate Bode markers.
      </div>
    );
  }
  const showPeak  = autoMarkers.peak !== false;
  const showBW    = autoMarkers.minus3dB !== false;
  const showBW40  = autoMarkers.minus40dB === true;
  const showUnity = autoMarkers.unityGain !== false;
  const showPM    = autoMarkers.phaseMargin !== false;
  const showGM    = autoMarkers.gainMargin !== false;
  return (
    <div className="scope-meas-bar">
      {showPeak && (
        <div className="meas-block meas-cursor">
          <div className="meas-head">{probe || 'probe'}</div>
          <Cell label="peak f"  value={markers.peak ? formatHz(markers.peak.freq)  : '—'} />
          <Cell label="peak G"  value={markers.peak ? formatDb(markers.peak.mag_db): '—'} />
        </div>
      )}
      {showBW && (
        <div className="meas-block">
          <div className="meas-head">−3 dB BW</div>
          <Cell label="lo"   value={markers.bw && Number.isFinite(markers.bw.lo) ? formatHz(markers.bw.lo) : '—'} />
          <Cell label="hi"   value={markers.bw && Number.isFinite(markers.bw.hi) ? formatHz(markers.bw.hi) : '—'} />
          <Cell label="span" value={markers.bw && Number.isFinite(markers.bw.bw) ? formatHz(markers.bw.bw) : '—'} />
        </div>
      )}
      {showBW40 && (
        <div className="meas-block">
          <div className="meas-head">−40 dB</div>
          <Cell label="lo"   value={markers.bw40 && Number.isFinite(markers.bw40.lo) ? formatHz(markers.bw40.lo) : '—'} />
          <Cell label="hi"   value={markers.bw40 && Number.isFinite(markers.bw40.hi) ? formatHz(markers.bw40.hi) : '—'} />
          <Cell label="span" value={markers.bw40 && Number.isFinite(markers.bw40.bw) ? formatHz(markers.bw40.bw) : '—'} />
        </div>
      )}
      {(showUnity || showPM) && (
        <div className="meas-block">
          <div className="meas-head">Unity / PM</div>
          <Cell label="f₀ dB"  value={showUnity && markers.unity ? formatHz(markers.unity.freq) : '—'} />
          <Cell label="phase margin" value={showPM && Number.isFinite(markers.pm) ? `${markers.pm.toFixed(1)}°` : '—'} />
        </div>
      )}
      {showGM && (
        <div className="meas-block">
          <div className="meas-head">Gain margin</div>
          <Cell label="f₁₈₀"  value={markers.gm ? formatHz(markers.gm.freq) : '—'} />
          <Cell label="GM"    value={markers.gm && Number.isFinite(markers.gm.gm_db) ? formatDb(markers.gm.gm_db) : '—'} />
        </div>
      )}
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
