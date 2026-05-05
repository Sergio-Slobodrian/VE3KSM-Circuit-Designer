// Time-series measurement helpers for the Scope tab. All functions accept
// equal-length parallel arrays of timestamps (seconds) and signal samples
// (volts); none mutate their inputs.
//
// Frequency / period / phase use rising zero-crossings around the AC mean.
// That is robust for symmetric periodic waveforms (sines, square, triangle)
// at audio rates — the only signals scope users actually meter — and degrades
// to NaN for noise / DC, which the UI displays as "—".

/** @returns {number} max - min, in volts. NaN when ys has length < 1. */
export function vpp(ys) {
  if (!ys || ys.length === 0) return NaN;
  let lo = Infinity, hi = -Infinity;
  for (const v of ys) {
    if (v < lo) lo = v;
    if (v > hi) hi = v;
  }
  return hi - lo;
}

/** @returns {number} arithmetic mean of ys, in volts. */
export function mean(ys) {
  if (!ys || ys.length === 0) return NaN;
  let s = 0;
  for (const v of ys) s += v;
  return s / ys.length;
}

/** @returns {number} AC-coupled RMS (the value an AC voltmeter reports for
 *  a 1 Vpk sine: ~707 mV). DC bias is subtracted before squaring. */
export function vrms(ys) {
  if (!ys || ys.length === 0) return NaN;
  const m = mean(ys);
  let s = 0;
  for (const v of ys) {
    const d = v - m;
    s += d * d;
  }
  return Math.sqrt(s / ys.length);
}

/**
 * Rising zero-crossings of (ys - mean(ys)). Returns the linearly-interpolated
 * crossing times in seconds.
 */
export function risingZeroCrossings(xs, ys) {
  const out = [];
  if (!xs || xs.length < 2) return out;
  const m = mean(ys);
  for (let i = 1; i < ys.length; i++) {
    const a = ys[i - 1] - m;
    const b = ys[i] - m;
    if (a < 0 && b >= 0) {
      // Linear interpolation between sample i-1 and i.
      const t = a === b ? xs[i] : xs[i - 1] + ((xs[i] - xs[i - 1]) * (-a)) / (b - a);
      out.push(t);
    }
  }
  return out;
}

/**
 * Median of crossing-to-crossing intervals. Robust against an odd half-cycle
 * at the start or end of the capture.
 */
export function period(xs, ys) {
  const cross = risingZeroCrossings(xs, ys);
  if (cross.length < 2) return NaN;
  const deltas = [];
  for (let i = 1; i < cross.length; i++) deltas.push(cross[i] - cross[i - 1]);
  deltas.sort((a, b) => a - b);
  return deltas[Math.floor(deltas.length / 2)];
}

/** @returns {number} period derived from the first inter-crossing window, in Hz. */
export function frequency(xs, ys) {
  const T = period(xs, ys);
  return T > 0 ? 1 / T : NaN;
}

/**
 * Phase of `ys` relative to `refYs`, in degrees, in the range [-180, 180).
 *
 * Both signals must share the xs grid. Computed from the first rising
 * zero-crossing of each signal: Δt = t_signal - t_ref, normalised by the
 * reference period, mapped to degrees.
 */
export function phaseDeg(xs, refYs, ys) {
  const refT = period(xs, refYs);
  if (!Number.isFinite(refT) || refT <= 0) return NaN;
  const r = risingZeroCrossings(xs, refYs);
  const s = risingZeroCrossings(xs, ys);
  if (r.length === 0 || s.length === 0) return NaN;
  let dt = (s[0] - r[0]) % refT;
  // Wrap into (-T/2, T/2] so phase reads as a small signed number rather than
  // a value near ±180° for a near-zero shift.
  const half = refT / 2;
  if (dt > half) dt -= refT;
  if (dt <= -half) dt += refT;
  return (dt / refT) * 360;
}

/**
 * Format a numeric value with engineering suffix and an optional unit.
 * NaN → "—". Picks 3-significant-figure precision unless the magnitude is
 * tiny, in which case it falls back to 2 decimals to keep small values legible.
 */
export function formatEng(v, unit = '') {
  if (!Number.isFinite(v)) return '—';
  const abs = Math.abs(v);
  let scale = 1, suffix = '';
  if (abs >= 1e9)      { scale = 1e-9; suffix = 'G'; }
  else if (abs >= 1e6) { scale = 1e-6; suffix = 'M'; }
  else if (abs >= 1e3) { scale = 1e-3; suffix = 'k'; }
  else if (abs >= 1)   { scale = 1;    suffix = '';  }
  else if (abs >= 1e-3){ scale = 1e3;  suffix = 'm'; }
  else if (abs >= 1e-6){ scale = 1e6;  suffix = 'µ'; }
  else if (abs >= 1e-9){ scale = 1e9;  suffix = 'n'; }
  else if (abs > 0)    { scale = 1e12; suffix = 'p'; }
  const scaled = v * scale;
  const digits = Math.abs(scaled) >= 100 ? 0 : Math.abs(scaled) >= 10 ? 1 : 2;
  return `${scaled.toFixed(digits)}${suffix ? ' ' + suffix : ''}${unit}`;
}

/**
 * Pick a "round" volts/division target for a peak-to-peak excursion so the
 * waveform fills 6 of 8 divisions. Snaps to a 1-2-5 sequence so the value
 * reads naturally on a scope panel (200 mV/div, 1 V/div, etc).
 */
export function autoVDiv(vppVal) {
  if (!Number.isFinite(vppVal) || vppVal <= 0) return 1;
  const target = vppVal / 6;
  const exp = Math.floor(Math.log10(target));
  const base = Math.pow(10, exp);
  const r = target / base;
  let snap;
  if (r <= 1) snap = 1;
  else if (r <= 2) snap = 2;
  else if (r <= 5) snap = 5;
  else snap = 10;
  return snap * base;
}

/**
 * Compile a math channel expression for the Scope tab. Math channels combine
 * scope channels (CH1..CH4) with arithmetic and an optional outer transform:
 *
 *   CH1 - CH2          difference (in volts)
 *   CH1 * CH2          product
 *   CH1 / CH2          ratio
 *   (CH1 + CH2) / 2    sum / average
 *   INT(CH1)           cumulative trapezoidal integral (V·s)
 *   DIFF(CH1)          central-difference derivative (V/s)
 *
 * The compiler returns `{ fn, mode }` on success or `{ error }` on failure.
 * `mode` is 'pointwise', 'integrate', or 'differentiate'. The `fn` is built
 * via the Function constructor with a fixed parameter list — there is no
 * access to the global `window` object, but JS keywords like `for` are still
 * legal. This is a local-only tool; the threat model is "user typo crashes
 * the eval", not "user uploads a malicious expression."
 */
export function compileMathExpression(expr) {
  if (typeof expr !== 'string' || !expr.trim()) {
    return { error: 'empty expression' };
  }
  let body = expr.trim();
  let mode = 'pointwise';
  const intMatch = body.match(/^INT\((.*)\)$/i);
  const diffMatch = body.match(/^DIFF\((.*)\)$/i);
  if (intMatch)       { mode = 'integrate';     body = intMatch[1].trim(); }
  else if (diffMatch) { mode = 'differentiate'; body = diffMatch[1].trim(); }
  if (!body) return { error: 'empty inner expression' };
  let fn;
  try {
    // eslint-disable-next-line no-new-func
    fn = new Function('CH1', 'CH2', 'CH3', 'CH4', 't', 'i', 'Math', `return (${body});`);
  } catch (err) {
    return { error: err.message || 'parse error' };
  }
  return { fn, mode };
}

/**
 * Evaluate a compiled math expression sample-by-sample over the given channel
 * data. `chYs` is an array of four optional Float64Array source channels; pass
 * `null` (or an empty array) for channels not bound to a probe and the math
 * function will see `0` for that variable.
 *
 * Returns a Float64Array parallel to xs. Integrate / differentiate are applied
 * after the per-sample pass, so e.g. INT(CH1*CH2) integrates the product.
 */
export function evaluateMathChannel(compiled, chYs, xs) {
  if (!compiled || !compiled.fn) return new Float64Array(0);
  const n = xs?.length || 0;
  const raw = new Float64Array(n);
  const c1 = chYs?.[0] ?? null;
  const c2 = chYs?.[1] ?? null;
  const c3 = chYs?.[2] ?? null;
  const c4 = chYs?.[3] ?? null;
  for (let i = 0; i < n; i++) {
    let v;
    try {
      v = compiled.fn(
        c1?.[i] ?? 0, c2?.[i] ?? 0, c3?.[i] ?? 0, c4?.[i] ?? 0,
        xs[i], i, Math,
      );
    } catch {
      v = NaN;
    }
    raw[i] = typeof v === 'number' ? v : NaN;
  }
  if (compiled.mode === 'integrate') {
    const out = new Float64Array(n);
    for (let i = 1; i < n; i++) {
      const dt = xs[i] - xs[i - 1];
      const a = Number.isFinite(raw[i - 1]) ? raw[i - 1] : 0;
      const b = Number.isFinite(raw[i]) ? raw[i] : 0;
      out[i] = out[i - 1] + ((a + b) / 2) * dt;
    }
    return out;
  }
  if (compiled.mode === 'differentiate') {
    const out = new Float64Array(n);
    if (n >= 2) {
      out[0]     = (raw[1] - raw[0]) / Math.max(xs[1] - xs[0], 1e-30);
      out[n - 1] = (raw[n - 1] - raw[n - 2]) / Math.max(xs[n - 1] - xs[n - 2], 1e-30);
    }
    for (let i = 1; i < n - 1; i++) {
      out[i] = (raw[i + 1] - raw[i - 1]) / Math.max(xs[i + 1] - xs[i - 1], 1e-30);
    }
    return out;
  }
  return raw;
}

/**
 * Pick a "round" time/division target so the captured run fills the screen
 * cleanly (10 divisions wide). Snaps 1-2-5.
 */
export function autoTimeDiv(durationSec) {
  if (!Number.isFinite(durationSec) || durationSec <= 0) return 1e-3;
  const target = durationSec / 10;
  const exp = Math.floor(Math.log10(target));
  const base = Math.pow(10, exp);
  const r = target / base;
  let snap;
  if (r <= 1) snap = 1;
  else if (r <= 2) snap = 2;
  else if (r <= 5) snap = 5;
  else snap = 10;
  return snap * base;
}
