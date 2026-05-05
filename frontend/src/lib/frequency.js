// Frequency-domain math helpers for the Spectrum and Network analyzer tabs.
// All functions operate on parallel Float64Arrays (or plain arrays) of
// frequencies (Hz) + magnitudes (dB) / phases (degrees). None mutate inputs.

/**
 * Linearly interpolate y at x given two surrounding samples (x0,y0)-(x1,y1).
 * Returns y0 when x0 == x1.
 */
function lerpY(x0, y0, x1, y1, x) {
  if (x1 === x0) return y0;
  const t = (x - x0) / (x1 - x0);
  return y0 + t * (y1 - y0);
}

/** Index of the frequency bin closest to target. Returns -1 on empty input. */
export function nearestBin(freqs, target) {
  if (!freqs || freqs.length === 0) return -1;
  let lo = 0, hi = freqs.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (freqs[mid] < target) lo = mid + 1;
    else hi = mid;
  }
  if (lo > 0 && Math.abs(freqs[lo - 1] - target) < Math.abs(freqs[lo] - target)) {
    return lo - 1;
  }
  return lo;
}

/**
 * Find the global peak in mag (dB), skipping the DC bin (i=0). Returns
 * { index, freq, mag_db } or null when the trace is empty.
 */
export function findPeak(freqs, mag) {
  if (!freqs || freqs.length < 2 || !mag) return null;
  let best = 1;
  for (let i = 2; i < mag.length; i++) {
    if (mag[i] > mag[best]) best = i;
  }
  return { index: best, freq: freqs[best], mag_db: mag[best] };
}

/**
 * Find the lower and upper frequencies where the magnitude drops `dropDb`
 * below the peak. Returns { lo, hi, bw } or null when the bandwidth can't be
 * resolved (e.g. trace never falls below threshold within the swept range).
 *
 * `dropDb` defaults to 3 — the canonical -3 dB / half-power bandwidth.
 */
export function bandwidth(freqs, mag, dropDb = 3) {
  const peak = findPeak(freqs, mag);
  if (!peak) return null;
  const target = peak.mag_db - dropDb;
  let lo = NaN, hi = NaN;
  // Walk left from peak to find the first crossing under target.
  for (let i = peak.index; i > 0; i--) {
    if (mag[i] >= target && mag[i - 1] < target) {
      lo = lerpY(mag[i - 1], freqs[i - 1], mag[i], freqs[i], target);
      break;
    }
  }
  // Walk right.
  for (let i = peak.index; i < mag.length - 1; i++) {
    if (mag[i] >= target && mag[i + 1] < target) {
      hi = lerpY(mag[i], freqs[i], mag[i + 1], freqs[i + 1], target);
      break;
    }
  }
  if (Number.isNaN(lo) && Number.isNaN(hi)) return null;
  return {
    lo: Number.isFinite(lo) ? lo : NaN,
    hi: Number.isFinite(hi) ? hi : NaN,
    bw: Number.isFinite(lo) && Number.isFinite(hi) ? hi - lo : NaN,
    target_db: target,
  };
}

/**
 * Find the frequency where magnitude crosses 0 dB (unity gain). Returns the
 * lowest crossing on a downward slope (i.e. the first time the response
 * leaves the passband). Null when the trace stays above or below 0 dB
 * throughout.
 */
export function unityGainCrossover(freqs, mag) {
  if (!freqs || freqs.length < 2 || !mag) return null;
  for (let i = 0; i < mag.length - 1; i++) {
    const a = mag[i], b = mag[i + 1];
    if ((a >= 0 && b < 0) || (a < 0 && b >= 0)) {
      const f = lerpY(a, freqs[i], b, freqs[i + 1], 0);
      return { freq: f, index: i };
    }
  }
  return null;
}

/**
 * Phase margin at the unity-gain crossover, in degrees. The convention used
 * here matches a Bode plot: phase margin = 180 + φ(f_ugc), so a stable system
 * with phase well above -180° at unity gain returns a large positive value.
 *
 * Returns NaN when there is no unity-gain crossing.
 */
export function phaseMargin(freqs, mag, phaseDeg) {
  const ugc = unityGainCrossover(freqs, mag);
  if (!ugc) return NaN;
  const i = ugc.index;
  const phaseAtUGC = lerpY(freqs[i], phaseDeg[i], freqs[i + 1], phaseDeg[i + 1], ugc.freq);
  // Wrap into (-360, 0] so the result lives in the canonical range.
  const wrapped = wrapPhase(phaseAtUGC);
  return 180 + wrapped;
}

/**
 * First frequency where phase crosses `targetDeg`, in either direction. The
 * returned `dir` field is `'down'` when the phase is decreasing across the
 * boundary and `'up'` otherwise. Operates on raw (unwrapped) phase.
 *
 * Returns null when no crossing is found within the swept range.
 */
export function phaseCrossover(freqs, phaseDeg, targetDeg) {
  if (!freqs || freqs.length < 2 || !phaseDeg) return null;
  for (let i = 0; i < phaseDeg.length - 1; i++) {
    const a = phaseDeg[i] - targetDeg;
    const b = phaseDeg[i + 1] - targetDeg;
    if ((a >= 0 && b < 0) || (a <= 0 && b > 0)) {
      const f = lerpY(a, freqs[i], b, freqs[i + 1], 0);
      return { freq: f, index: i, dir: a > b ? 'down' : 'up' };
    }
  }
  return null;
}

/**
 * Gain margin: the magnitude (in dB) below 0 dB at the frequency where the
 * open-loop phase crosses -180°. Returns `{ freq, mag_db, gm_db }` where
 * gm_db = -mag_db (positive = stable, negative = unstable).
 *
 * Operates on raw (unwrapped) phase since wrapping confuses the crossing
 * search. Returns null when no -180° crossing exists.
 */
export function gainMargin(freqs, mag, phaseDeg) {
  const xover = phaseCrossover(freqs, phaseDeg, -180);
  if (!xover) return null;
  const i = xover.index;
  const magAt = lerpY(freqs[i], mag[i], freqs[i + 1], mag[i + 1], xover.freq);
  return { freq: xover.freq, mag_db: magAt, gm_db: -magAt };
}

/** Wrap a phase (degrees) into (-180, 180]. */
export function wrapPhase(deg) {
  if (!Number.isFinite(deg)) return NaN;
  let p = ((deg + 180) % 360 + 360) % 360 - 180;
  if (p === -180) p = 180;
  return p;
}

/**
 * Group delay τ(ω) = -dφ/dω, in seconds, computed via central differences
 * on the unwrapped phase. The first/last samples use forward/backward
 * differences. Phase is converted from degrees to radians inside.
 */
export function groupDelay(freqs, phaseDeg) {
  const n = freqs.length;
  const out = new Float64Array(n);
  if (n < 2) return out;
  const phaseRad = new Float64Array(n);
  for (let i = 0; i < n; i++) phaseRad[i] = phaseDeg[i] * Math.PI / 180;
  const dwdf = 2 * Math.PI;
  out[0] = -(phaseRad[1] - phaseRad[0]) / ((freqs[1] - freqs[0]) * dwdf);
  for (let i = 1; i < n - 1; i++) {
    out[i] = -(phaseRad[i + 1] - phaseRad[i - 1]) / ((freqs[i + 1] - freqs[i - 1]) * dwdf);
  }
  out[n - 1] = -(phaseRad[n - 1] - phaseRad[n - 2]) / ((freqs[n - 1] - freqs[n - 2]) * dwdf);
  return out;
}

/**
 * Total Harmonic Distortion + Noise readouts for a fundamental at f0,
 * computed from a dB magnitude spectrum (and an optional phase array, for
 * per-harmonic phase reporting).
 *
 * Returns:
 *   thdPercent  — sqrt(Σ harmonic²) / fundamental, ×100
 *   thdDb       — same ratio in dB (negative = clean)
 *   thdPlusNDb  — sqrt(Σ everything-but-fundamental²) / fundamental, in dB
 *   thdPlusNPercent — same as percent
 *   snrDb       — fundamental / Σ noise (excluding fundamental + harmonics)
 *   sinad       — fundamental / RMS of all other bins, in dB
 *   harmonics   — Array<{ n, freq, mag_db, dbc, percent, phase_deg }>
 *
 * All ratios degrade gracefully (NaN) when f0 is out of the sweep or the
 * spectrum has fewer than 2 bins. The fundamental bin and each harmonic bin
 * are excluded from the SNR noise sum so a clean tone reports very high SNR
 * rather than ~0 dB.
 */
export function thd(freqs, mag, f0, maxHarmonics = 10, phaseDegArr = null) {
  if (!freqs || freqs.length < 2 || !mag || !(f0 > 0)) {
    return {
      thdPercent: NaN, thdDb: NaN,
      thdPlusNDb: NaN, thdPlusNPercent: NaN,
      snrDb: NaN, sinad: NaN, harmonics: [],
    };
  }
  const fmax = freqs[freqs.length - 1];
  const fundIdx = nearestBin(freqs, f0);
  const fundDb = mag[fundIdx];
  const fundLin = Math.pow(10, fundDb / 20);
  const harmonics = [];
  const harmonicIdx = new Set([fundIdx]);
  let harmSumSq = 0;
  for (let n = 2; n <= maxHarmonics; n++) {
    const fn = n * f0;
    if (fn > fmax) break;
    const idx = nearestBin(freqs, fn);
    if (idx === fundIdx) continue;
    harmonicIdx.add(idx);
    const db = mag[idx];
    const lin = Math.pow(10, db / 20);
    harmSumSq += lin * lin;
    const dbc = db - fundDb;
    harmonics.push({
      n,
      freq: freqs[idx],
      mag_db: db,
      dbc,
      percent: Math.pow(10, dbc / 20) * 100,
      phase_deg: phaseDegArr && idx < phaseDegArr.length ? phaseDegArr[idx] : NaN,
    });
  }

  // Σ everything-but-fundamental — used for THD+N and SINAD.
  let totalSqExFund = 0;
  let noiseSqExHarm = 0;       // Σ ex-fundamental, ex-harmonics — for SNR
  let noiseCountExHarm = 0;
  for (let i = 1; i < mag.length; i++) {
    const lin = Math.pow(10, mag[i] / 20);
    if (i !== fundIdx) totalSqExFund += lin * lin;
    if (!harmonicIdx.has(i)) {
      noiseSqExHarm += lin * lin;
      noiseCountExHarm++;
    }
  }

  const thdRatio = harmonics.length > 0
    ? Math.sqrt(harmSumSq) / Math.max(fundLin, 1e-30)
    : NaN;
  const thdPercent = thdRatio * 100;
  const thdDb = Number.isFinite(thdRatio)
    ? 20 * Math.log10(Math.max(thdRatio, 1e-30))
    : NaN;

  const thdNRatio = totalSqExFund > 0
    ? Math.sqrt(totalSqExFund) / Math.max(fundLin, 1e-30)
    : NaN;
  const thdPlusNDb = Number.isFinite(thdNRatio)
    ? 20 * Math.log10(Math.max(thdNRatio, 1e-30))
    : NaN;
  const thdPlusNPercent = thdNRatio * 100;

  // SNR: fundamental / RMS noise (ex-harmonics). We average over the bin
  // count so a denser sweep doesn't inflate the noise sum.
  const noiseRMSExHarm = noiseCountExHarm > 0
    ? Math.sqrt(noiseSqExHarm / noiseCountExHarm) : 0;
  const snrRatio = fundLin / Math.max(noiseRMSExHarm, 1e-30);
  const snrDb = noiseRMSExHarm > 0
    ? 20 * Math.log10(Math.max(snrRatio, 1e-30))
    : NaN;

  // SINAD uses the same RMS-over-bins convention as before but now with the
  // total (harmonics + noise) energy in the denominator. Matches the
  // canonical SINAD = signal / (signal + noise + distortion - signal).
  const sinadRMS = (mag.length - 1) > 0
    ? Math.sqrt(totalSqExFund / Math.max(mag.length - 1, 1)) : 0;
  const sinadRatio = fundLin / Math.max(sinadRMS, 1e-30);
  const sinad = sinadRMS > 0
    ? 20 * Math.log10(Math.max(sinadRatio, 1e-30))
    : NaN;

  return {
    thdPercent, thdDb,
    thdPlusNDb, thdPlusNPercent,
    snrDb, sinad,
    harmonics,
  };
}

/**
 * Format a frequency with appropriate engineering suffix.
 * 1234 → "1.23 kHz"; 12.5e6 → "12.5 MHz".
 */
export function formatHz(v) {
  if (!Number.isFinite(v)) return '—';
  const a = Math.abs(v);
  if (a >= 1e9)      return `${(v / 1e9).toFixed(2)} GHz`;
  if (a >= 1e6)      return `${(v / 1e6).toFixed(2)} MHz`;
  if (a >= 1e3)      return `${(v / 1e3).toFixed(2)} kHz`;
  if (a >= 1)        return `${v.toFixed(2)} Hz`;
  return `${(v * 1000).toFixed(2)} mHz`;
}

/** Format a dB value with one decimal and explicit sign. */
export function formatDb(v) {
  if (!Number.isFinite(v)) return '—';
  return `${v >= 0 ? '+' : ''}${v.toFixed(1)} dB`;
}
