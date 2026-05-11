// Pure-JS waveform helpers for the m10 signal generator inspector. The
// preview SVG draws from the same parameter shape the backend's lowering
// layer expects, so what the user sees in the inspector is the same shape
// the simulator runs.
//
// Mode catalog and parameter schema match backend/internal/netlist/
// waveforms.go and the *+ waveform=... metadata round-trip — keep the two
// files in sync when adding a mode.

/**
 * Catalog of the 11 waveform modes the inspector exposes. Each entry:
 *   - label: user-facing name
 *   - icon: 12×10 SVG path drawn into the wave-row button
 *   - params: ordered list of param descriptors. Each descriptor is one of:
 *       { key, label, default, unit }                    — text/number input
 *       { key, label, default, options }                 — <select>
 *   - meta(p): short readout shown next to the preview
 *   - preview(p, w, h): returns an SVG path string fitted into [0..w]×[0..h]
 *
 * The default values are the ones a freshly-dropped V/I source picks up when
 * the user clicks the wave button — they should produce a non-degenerate
 * preview without further input.
 */
export const WAVEFORM_MODES = [
  {
    key: 'dc', label: 'DC',
    icon: 'M0 5 H12',
    params: [
      { key: 'value', label: 'Value', default: '0', unit: 'V' },
    ],
    meta: (p) => `DC ${p.value ?? '0'} V`,
    preview: previewDC,
  },
  {
    key: 'sin', label: 'Sine',
    icon: 'M0 5 q3 -5 6 0 t6 0',
    params: [
      { key: 'freq',   label: 'Frequency', default: '1k',  unit: 'Hz' },
      { key: 'ampl',   label: 'Amplitude', default: '1',   unit: 'Vpp' },
      { key: 'offset', label: 'DC offset', default: '0',   unit: 'V' },
      { key: 'td',     label: 'Delay',     default: '0',   unit: 's' },
      { key: 'phase',  label: 'Phase',     default: '0',   unit: '°' },
    ],
    meta: (p) => `${p.freq || '?'} Hz`,
    preview: previewSin,
  },
  {
    key: 'square', label: 'Square',
    icon: 'M0 9 V1 H6 V9 H12 V1',
    params: [
      { key: 'freq',   label: 'Frequency', default: '1k',  unit: 'Hz' },
      { key: 'ampl',   label: 'Amplitude', default: '1',   unit: 'Vpp' },
      { key: 'offset', label: 'DC offset', default: '0',   unit: 'V' },
      { key: 'duty',   label: 'Duty',      default: '50',  unit: '%' },
      { key: 'tr',     label: 'Rise',      default: '10n', unit: 's' },
      { key: 'tf',     label: 'Fall',      default: '10n', unit: 's' },
      { key: 'td',     label: 'Delay',     default: '0',   unit: 's' },
      { key: 'phase',  label: 'Phase',     default: '0',   unit: '°' },
    ],
    meta: (p) => `${p.freq} · ${p.duty}%`,
    preview: previewSquare,
  },
  {
    key: 'triangle', label: 'Triangle',
    icon: 'M0 9 L3 1 L9 9 L12 5',
    params: [
      { key: 'freq',   label: 'Frequency', default: '1k', unit: 'Hz' },
      { key: 'ampl',   label: 'Amplitude', default: '1',  unit: 'Vpp' },
      { key: 'offset', label: 'DC offset', default: '0',  unit: 'V' },
      { key: 'sym',    label: 'Symmetry',  default: '50', unit: '%' },
      { key: 'td',     label: 'Delay',     default: '0',  unit: 's' },
      { key: 'phase',  label: 'Phase',     default: '0',  unit: '°' },
    ],
    meta: (p) => `${p.freq}`,
    preview: previewTriangle,
  },
  {
    key: 'sawtooth', label: 'Sawtooth',
    icon: 'M0 9 L6 1 L6 9 L12 1',
    params: [
      { key: 'freq',   label: 'Frequency', default: '1k', unit: 'Hz' },
      { key: 'ampl',   label: 'Amplitude', default: '1',  unit: 'Vpp' },
      { key: 'offset', label: 'DC offset', default: '0',  unit: 'V' },
      { key: 'dir',    label: 'Direction', default: 'rising', options: ['rising', 'falling'] },
      { key: 'td',     label: 'Delay',     default: '0',  unit: 's' },
      { key: 'phase',  label: 'Phase',     default: '0',  unit: '°' },
    ],
    meta: (p) => `${p.freq} · ${p.dir}`,
    preview: previewSawtooth,
  },
  {
    key: 'pulse', label: 'Pulse',
    icon: 'M0 9 H3 V1 H5 V9 H9 V1 H11 V9',
    params: [
      { key: 'per', label: 'Period',  default: '1m',   unit: 's' },
      { key: 'pw',  label: 'Width',   default: '500u', unit: 's' },
      { key: 'tr',  label: 'Rise',    default: '10n',  unit: 's' },
      { key: 'tf',  label: 'Fall',    default: '10n',  unit: 's' },
      { key: 'v1',  label: 'V low',   default: '0',    unit: 'V' },
      { key: 'v2',  label: 'V high',  default: '5',    unit: 'V' },
      { key: 'td',  label: 'Delay',   default: '0',    unit: 's' },
    ],
    meta: (p) => `${p.pw} / ${p.per}`,
    preview: previewPulse,
  },
  {
    key: 'noise', label: 'Noise',
    icon: 'M0 5 L2 2 L4 7 L6 3 L8 8 L10 4 L12 6',
    params: [
      { key: 'type', label: 'Type', default: 'white', options: ['white', 'pink', '1/f²', 'band-limited'] },
      { key: 'rms',  label: 'RMS',  default: '0.1', unit: 'Vrms' },
      { key: 'bw',   label: 'BW',   default: '20k', unit: 'Hz' },
      { key: 'dur',  label: 'Span', default: '50m', unit: 's' },
      { key: 'seed', label: 'Seed', default: '42',  unit: '' },
      { key: 'td',   label: 'Delay', default: '0',  unit: 's' },
    ],
    meta: (p) => `${p.type} · ${p.bw}`,
    preview: previewNoise,
  },
  {
    key: 'chirp', label: 'Chirp',
    icon: 'M0 5 q1 -4 2 0 t2 0 q1 -4 2 0 t2 0 q0.7 -3 1.4 0',
    params: [
      { key: 'f0',    label: 'Start',     default: '20',  unit: 'Hz' },
      { key: 'f1',    label: 'Stop',      default: '20k', unit: 'Hz' },
      { key: 'dur',   label: 'Duration',  default: '1',   unit: 's' },
      { key: 'ampl',  label: 'Amplitude', default: '1',   unit: 'Vpp' },
      { key: 'shape', label: 'Shape',     default: 'log', options: ['linear', 'log', 'exponential'] },
      { key: 'mode',  label: 'Mode',      default: 'one-shot', options: ['one-shot', 'continuous'] },
      { key: 'td',    label: 'Delay',     default: '0',   unit: 's' },
    ],
    meta: (p) => `${p.f0} → ${p.f1}`,
    preview: previewChirp,
  },
  {
    key: 'twotone', label: 'Two-tone',
    icon: 'M0 5 q1.5 -4 3 0 q1.5 4 3 0 q1.5 -4 3 0 q1.5 4 3 0',
    params: [
      { key: 'f1',   label: 'Freq 1',  default: '700',  unit: 'Hz' },
      { key: 'f2',   label: 'Freq 2',  default: '1900', unit: 'Hz' },
      { key: 'a1',   label: 'Ampl 1',  default: '0.5',  unit: 'Vpp' },
      { key: 'a2',   label: 'Ampl 2',  default: '0.5',  unit: 'Vpp' },
      { key: 'dphi', label: 'Δ phase', default: '0',    unit: '°' },
      { key: 'td',   label: 'Delay',   default: '0',    unit: 's' },
    ],
    meta: (p) => `${p.f1}/${p.f2}`,
    preview: previewTwoTone,
  },
  {
    key: 'am', label: 'AM',
    icon: 'M0 5 q1.5 -4 3 0 q1.5 4 3 0',
    params: [
      { key: 'fc',    label: 'Carrier',      default: '1MEG', unit: 'Hz' },
      { key: 'fm',    label: 'Mod',          default: '1k',   unit: 'Hz' },
      { key: 'depth', label: 'Depth',        default: '80',   unit: '%' },
      { key: 'ampl',  label: 'Carrier ampl', default: '1',    unit: 'Vpp' },
      { key: 'shape', label: 'Mod shape',    default: 'sine', options: ['sine'] },
      { key: 'td',    label: 'Delay',        default: '0',    unit: 's' },
    ],
    meta: (p) => `fc ${p.fc} · ${p.depth}%`,
    preview: previewAM,
  },
  {
    key: 'fm', label: 'FM',
    icon: 'M0 5 q2 -4 4 0 q1 4 2 0 q2 -4 4 0',
    params: [
      { key: 'fc',     label: 'Carrier',   default: '10.7MEG', unit: 'Hz' },
      { key: 'fm',     label: 'Mod',       default: '1k',   unit: 'Hz' },
      { key: 'dev',    label: 'Deviation', default: '5k',   unit: 'Hz' },
      { key: 'ampl',   label: 'Amplitude', default: '1',    unit: 'Vpp' },
      { key: 'offset', label: 'DC offset', default: '0',    unit: 'V' },
    ],
    meta: (p) => `fc ${p.fc} · Δf ${p.dev}`,
    preview: previewFM,
  },
  {
    key: 'pwl', label: 'Arbitrary',
    icon: 'M0 5 H3 L4 1 L5 9 L6 5 H9 L10 3 L11 7 H12',
    params: [
      { key: 'src',  label: 'Source', default: '(none loaded)', readOnly: true },
      { key: 'rate', label: 'Rate',   default: '48000', unit: 'Hz' },
      { key: 'gain', label: 'Gain',   default: '1.0',   unit: '×' },
      { key: 'loop', label: 'Loop',   default: 'true',  options: ['true', 'false'] },
      { key: 'td',   label: 'Delay',  default: '0',     unit: 's' },
    ],
    meta: (p) => p.src || '(no file)',
    preview: previewPWL,
  },
];

/** Lookup table: mode key → catalog entry. */
export const WAVEFORM_BY_KEY = Object.fromEntries(WAVEFORM_MODES.map((m) => [m.key, m]));

/** Default Params object for a freshly-picked mode. */
export function defaultParams(modeKey) {
  const m = WAVEFORM_BY_KEY[modeKey];
  if (!m) return {};
  const out = {};
  for (const p of m.params) out[p.key] = p.default;
  return out;
}

/**
 * Parse a SPICE engineering value (1k, 4.7p, 100m, 1MEG, plain) into a JS
 * number. Mirrors backend/internal/netlist/waveforms.go parseEngFloat — the
 * preview uses it to scale axes, the lowering uses its sibling to emit. Falls
 * back to `fallback` on garbage so a typing-in-progress field doesn't yank
 * the preview to NaN.
 */
export function parseEng(value, fallback = 0) {
  if (value == null) return fallback;
  const s = String(value).trim();
  if (s === '') return fallback;
  const n = Number(s);
  if (Number.isFinite(n)) return n;
  const m = s.match(/^([-+]?[\d.eE+-]+)([a-zA-Z]+)$/);
  if (!m) return fallback;
  const head = Number(m[1]);
  if (!Number.isFinite(head)) return fallback;
  const sfx = m[2].toUpperCase();
  const table = {
    MEG: 1e6, T: 1e12, G: 1e9, K: 1e3,
    M: 1e-3, U: 1e-6, N: 1e-9, P: 1e-12, F: 1e-15,
  };
  return sfx in table ? head * table[sfx] : fallback;
}

// --- preview generators -----------------------------------------------------

const PREVIEW_W = 200;
const PREVIEW_H = 70;
const PREVIEW_MID = PREVIEW_H / 2;
const PREVIEW_AMP = 22; // pixels at full ampl

function previewDC() {
  return `M0 ${PREVIEW_MID} H${PREVIEW_W}`;
}

function previewSin(p) {
  return sineLikePath(p, (t) => Math.sin(2 * Math.PI * t));
}

function previewSquare(p) {
  const duty = clamp(parseEng(p.duty, 50), 1, 99) / 100;
  return stepPath(2, (t) => (t < duty ? 1 : -1));
}

function previewTriangle(p) {
  const sym = clamp(parseEng(p.sym, 50), 1, 99) / 100;
  return polylinePath(2, (t) => {
    if (t < sym) return -1 + (2 * t) / sym;
    return 1 - (2 * (t - sym)) / (1 - sym);
  });
}

function previewSawtooth(p) {
  const dir = (p.dir || 'rising') === 'falling' ? -1 : 1;
  return polylinePath(2, (t) => dir * (-1 + 2 * t));
}

function previewPulse(p) {
  const per = parseEng(p.per, 1e-3);
  const pw = parseEng(p.pw, 0.5e-3);
  const duty = clamp(pw / Math.max(per, 1e-30), 0.01, 0.99);
  return stepPath(2, (t) => (t < duty ? 1 : -1));
}

function previewNoise() {
  const rng = mulberry32(1234);
  const path = ['M0 ' + PREVIEW_MID];
  for (let x = 4; x <= PREVIEW_W; x += 4) {
    const y = clamp(PREVIEW_MID + (rng() - 0.5) * 32, 12, 58);
    path.push(`L${x} ${y.toFixed(1)}`);
  }
  return path.join(' ');
}

function previewChirp(p) {
  const shape = (p.shape || 'log').toLowerCase();
  const path = [`M0 ${PREVIEW_MID}`];
  for (let x = 0; x <= PREVIEW_W; x++) {
    const t = x / PREVIEW_W;
    const f = shape === 'linear' ? 1 + t * 8 : Math.exp(t * 2.4);
    const y = PREVIEW_MID - PREVIEW_AMP * Math.sin(t * Math.PI * 2 * f);
    path.push(`L${x} ${y.toFixed(1)}`);
  }
  return path.join(' ');
}

function previewTwoTone() {
  const path = [`M0 ${PREVIEW_MID}`];
  for (let x = 0; x <= PREVIEW_W; x++) {
    const y = PREVIEW_MID
      - PREVIEW_AMP * 0.5 * Math.sin((x * Math.PI) / 40)
      - PREVIEW_AMP * 0.5 * Math.sin((x * Math.PI) / 14.7);
    path.push(`L${x} ${y.toFixed(1)}`);
  }
  return path.join(' ');
}

function previewAM(p) {
  const depth = clamp(parseEng(p.depth, 80) / 100, 0, 1);
  const path = [`M0 ${PREVIEW_MID}`];
  for (let x = 0; x <= PREVIEW_W; x++) {
    const env = 1 + depth * Math.sin((x * Math.PI) / 100);
    const y = PREVIEW_MID - PREVIEW_AMP * 0.7 * env * Math.sin(x * 1.05);
    path.push(`L${x} ${y.toFixed(1)}`);
  }
  return path.join(' ');
}

function previewFM() {
  const path = [`M0 ${PREVIEW_MID}`];
  let phase = 0;
  for (let x = 0; x <= PREVIEW_W; x++) {
    const inst = 0.7 + 0.5 * Math.sin((x * Math.PI) / 100);
    phase += inst;
    const y = PREVIEW_MID - PREVIEW_AMP * Math.sin(phase);
    path.push(`L${x} ${y.toFixed(1)}`);
  }
  return path.join(' ');
}

function previewPWL(p, points) {
  // If the user has uploaded a waveform, show its actual points.
  if (points && points.length >= 2) {
    const ts = points.filter((_, i) => i % 2 === 0);
    const vs = points.filter((_, i) => i % 2 === 1);
    const tMin = ts[0];
    const tMax = ts[ts.length - 1] || (tMin + 1);
    let vMax = 1e-30;
    for (const v of vs) if (Math.abs(v) > vMax) vMax = Math.abs(v);
    const stride = Math.max(1, Math.floor(ts.length / PREVIEW_W));
    const path = [`M0 ${PREVIEW_MID}`];
    for (let i = 0; i < ts.length; i += stride) {
      const x = ((ts[i] - tMin) / (tMax - tMin)) * PREVIEW_W;
      const y = PREVIEW_MID - (vs[i] / vMax) * PREVIEW_AMP;
      path.push(`L${x.toFixed(2)} ${y.toFixed(2)}`);
    }
    return path.join(' ');
  }
  // No file: stub triangle.
  return 'M0 35 L20 35 L25 14 L30 56 L35 35 L60 35 L80 28 L100 42 L120 22 L140 48 L160 35 L200 35';
}

// --- helpers ---------------------------------------------------------------

function sineLikePath(_, fn) {
  const path = [`M0 ${PREVIEW_MID}`];
  const cycles = 4;
  for (let x = 0; x <= PREVIEW_W; x += 1) {
    const t = (cycles * x) / PREVIEW_W;
    const y = PREVIEW_MID - PREVIEW_AMP * fn(t);
    path.push(`L${x} ${y.toFixed(1)}`);
  }
  return path.join(' ');
}

function polylinePath(cycles, fn) {
  const segs = 100 * cycles;
  const path = [];
  for (let i = 0; i <= segs; i++) {
    const t = (i / segs) * cycles;
    const phase = t - Math.floor(t);
    const v = fn(phase);
    const x = (i / segs) * PREVIEW_W;
    const y = PREVIEW_MID - PREVIEW_AMP * v;
    path.push(`${i === 0 ? 'M' : 'L'}${x.toFixed(1)} ${y.toFixed(1)}`);
  }
  return path.join(' ');
}

function stepPath(cycles, fn) {
  // Render a square-ish trace by sampling and connecting with right-angled
  // line segments — looks like a CRT scope rather than a smooth interpolant.
  const path = [];
  let prev = null;
  const N = 200;
  for (let i = 0; i <= N; i++) {
    const t = ((i / N) * cycles) % 1;
    const v = fn(t);
    const x = (i / N) * PREVIEW_W;
    const y = PREVIEW_MID - PREVIEW_AMP * v;
    if (prev != null && Math.sign(prev) !== Math.sign(v)) {
      path.push(`L${x.toFixed(1)} ${prev > 0 ? PREVIEW_MID - PREVIEW_AMP : PREVIEW_MID + PREVIEW_AMP}`);
      path.push(`L${x.toFixed(1)} ${y.toFixed(1)}`);
    } else {
      path.push(`${i === 0 ? 'M' : 'L'}${x.toFixed(1)} ${y.toFixed(1)}`);
    }
    prev = v;
  }
  return path.join(' ');
}

function clamp(v, lo, hi) {
  return v < lo ? lo : v > hi ? hi : v;
}

// Deterministic PRNG so the noise preview doesn't flicker on every render.
function mulberry32(seed) {
  let a = seed | 0;
  return () => {
    a = (a + 0x6D2B79F5) | 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
