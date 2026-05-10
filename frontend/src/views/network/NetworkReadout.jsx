// Auto-marker readouts under the Bode stack. Same .scope-meas-bar shell as
// the Scope's MeasurementBar. Each block is gated by the side-panel toggle
// in `autoMarkers` so the bar shrinks when fewer measurements are wanted.
//
// Milestone 12 adds optional VSWR / return-loss / Zin cells driven by the
// `smith` trace and the `showVSWR` toggle, plus a `children` slot the
// parent uses to render the Smith chart inset alongside the readouts.

import { formatHz, formatDb, formatVSWR, formatImpedance } from '../../lib/frequency.js';

/**
 * @param {{
 *   markers: null | object,
 *   autoMarkers?: object,
 *   probe: string | null,
 *   smith?: null | {
 *     freqs: ArrayLike<number>,
 *     gammaMag: ArrayLike<number>,
 *     vswr: ArrayLike<number>,
 *     returnLossDb: ArrayLike<number>,
 *     zinRe: ArrayLike<number>,
 *     zinIm: ArrayLike<number>,
 *   },
 *   showVSWR?: boolean,
 *   z0?: number,
 *   children?: any,
 * }} props
 */
export default function NetworkReadout({
  markers,
  probe,
  autoMarkers = {},
  smith = null,
  showVSWR = false,
  z0 = 50,
  children = null,
}) {
  if (!markers && !smith && !children) {
    return (
      <div className="scope-meas-bar empty">
        Run an AC sweep to populate Bode markers.
      </div>
    );
  }
  const showPeak  = markers && autoMarkers.peak !== false;
  const showBW    = markers && autoMarkers.minus3dB !== false;
  const showBW40  = markers && autoMarkers.minus40dB === true;
  const showUnity = markers && autoMarkers.unityGain !== false;
  const showPM    = markers && autoMarkers.phaseMargin !== false;
  const showGM    = markers && autoMarkers.gainMargin !== false;
  const sParams = smithSummary(smith);
  const showSPanel = showVSWR && smith != null;
  return (
    <div className="scope-meas-bar network-readout">
      <div className="meas-stats">
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
        {showSPanel && (
          <div className="meas-block meas-rf">
            <div className="meas-head">S₁₁ · {z0}Ω</div>
            <Cell label="best |Γ|"   value={sParams ? formatDb(sParams.gammaDbMin) : '—'} />
            <Cell label="match f"   value={sParams ? formatHz(sParams.matchedFreq) : '—'} />
            <Cell label="VSWR min"  value={sParams ? formatVSWR(sParams.vswrMin) : '—'} />
            <Cell label="RL max"   value={sParams ? formatDb(sParams.rlMax) : '—'} />
            <Cell label="Zin @match" value={sParams ? formatImpedance(sParams.zinReMatched, sParams.zinImMatched) : '—'} />
          </div>
        )}
      </div>
      {children}
    </div>
  );
}

/**
 * Walk the per-bin Smith arrays once and pull out the headline numbers the
 * readout cells display: lowest |Γ| (best match), the frequency of that bin,
 * VSWR at that bin, and the matched-bin Zin. Kept in this file because nothing
 * else needs the same rollup — frequency.js stays focused on the per-bin math.
 */
function smithSummary(smith) {
  if (!smith) return null;
  const { freqs, gammaMag, vswr, returnLossDb, zinRe, zinIm } = smith;
  let best = -1;
  let bestMag = Infinity;
  for (let i = 0; i < gammaMag.length; i++) {
    const m = gammaMag[i];
    if (Number.isFinite(m) && m < bestMag) { bestMag = m; best = i; }
  }
  if (best < 0) return null;
  const gammaDbMin = bestMag > 0 ? 20 * Math.log10(bestMag) : -Infinity;
  return {
    matchedFreq: freqs[best],
    gammaDbMin,
    vswrMin: vswr[best],
    rlMax: returnLossDb[best],
    zinReMatched: zinRe[best],
    zinImMatched: zinIm[best],
  };
}

function Cell({ label, value }) {
  return (
    <div className="meas-cell">
      <span className="l">{label}</span>
      <span className="v">{value}</span>
    </div>
  );
}
