// Thin wrappers over the backend REST surface added in milestones 3 and 4.
// All paths are relative; in dev the Vite proxy (vite.config.js) forwards them
// to the Go backend on :8080, and in production the same Go process serves
// both the API and the SPA bundle.

async function getJSON(path) {
  const resp = await fetch(path, { headers: { Accept: 'application/json' } });
  if (!resp.ok) {
    const body = await resp.text().catch(() => '');
    throw new Error(`${resp.status} ${resp.statusText}: ${body || path}`);
  }
  return resp.json();
}

/** @returns {Promise<{examples: Array<{name: string, title?: string}>}>} */
export function fetchExamples() {
  return getJSON('/api/examples');
}

/** @param {string} name @returns {Promise<object>} parsed Circuit */
export function fetchExample(name) {
  return getJSON(`/api/examples/${encodeURIComponent(name)}`);
}

/** @returns {Promise<{components: Array<{kind: string, symbol: string, description?: string}>}>} */
export function fetchLibrary() {
  return getJSON('/api/library');
}

/** @returns {Promise<{status: string}>} */
export function fetchHealth() {
  return getJSON('/api/healthz');
}

/**
 * Parse SPICE source server-side and return the resulting Circuit.
 * @param {string} text raw SPICE source
 * @returns {Promise<object>} parsed Circuit
 */
export async function parseNetlist(text) {
  const resp = await fetch('/api/circuit/parse', {
    method: 'POST',
    headers: { 'Content-Type': 'text/plain', Accept: 'application/json' },
    body: text,
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => '');
    throw new Error(body || `${resp.status} ${resp.statusText}`);
  }
  return resp.json();
}

/**
 * Emit canonical ngspice source for the given Circuit.
 * @param {object} circuit
 * @returns {Promise<string>} SPICE source
 */
export async function emitNetlist(circuit) {
  const resp = await fetch('/api/circuit/emit', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/plain' },
    body: JSON.stringify(circuit),
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => '');
    throw new Error(body || `${resp.status} ${resp.statusText}`);
  }
  return resp.text();
}

/**
 * Ingest a SPICE .lib body (m9). The server scans for .SUBCKT definitions,
 * persists the file in its library search dir, and returns the freshly-created
 * palette entries plus the canonical (sanitised) basename.
 * @param {string} filename
 * @param {string} body raw .lib source
 * @returns {Promise<{lib_file: string, imported: object[]}>}
 */
export async function importLibrary(filename, body) {
  const resp = await fetch('/api/library/import', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ filename, body }),
  });
  if (!resp.ok) {
    const text = await resp.text().catch(() => '');
    let msg = text;
    try {
      const parsed = JSON.parse(text);
      if (parsed?.message) msg = parsed.message;
    } catch { /* not json — keep raw */ }
    throw new Error(msg || `${resp.status} ${resp.statusText}`);
  }
  return resp.json();
}

/**
 * Import a .zip pack of mixed .lib / .asy files. Mirrors importLibrary but
 * uses multipart/form-data because the body is binary. Returns the same
 * shape as importLibrary plus an optional `warnings` array describing any
 * per-file failures (a single bogus .asy in a 700-file pack lands here
 * rather than aborting the whole import).
 *
 * @param {File} file the user-picked .zip
 * @returns {Promise<{lib_file: string, imported: object[], updated: object[], warnings: object[]}>}
 */
export async function importLibraryArchive(file) {
  const form = new FormData();
  form.append('file', file, file.name);
  const resp = await fetch('/api/library/import-archive', {
    method: 'POST',
    headers: { Accept: 'application/json' },
    body: form,
  });
  if (!resp.ok) {
    const text = await resp.text().catch(() => '');
    let msg = text;
    try {
      const parsed = JSON.parse(text);
      if (parsed?.message) msg = parsed.message;
    } catch { /* not json — keep raw */ }
    throw new Error(msg || `${resp.status} ${resp.statusText}`);
  }
  return resp.json();
}

/**
 * Decode a CSV or WAV file server-side and return the resulting (t,v) point
 * list pre-formatted as the canonical `t:v;t:v;…` string the SourceSpec
 * stores in Params["points"]. Used by the m10 signal generator's
 * "Import waveform…" affordance for the PWL/arb mode.
 *
 * @param {File} file the user-picked file (from <input type="file">)
 * @param {{peak?: number, sampleRateHint?: number}} [opts]
 * @returns {Promise<{name: string, sample_rate: number, duration: number,
 *   point_count: number, points: number[], points_string: string}>}
 */
export async function importWaveform(file, opts = {}) {
  const fd = new FormData();
  fd.append('file', file, file.name);
  if (opts.peak != null) fd.append('peak', String(opts.peak));
  if (opts.sampleRateHint != null) fd.append('sample_rate', String(opts.sampleRateHint));
  const resp = await fetch('/api/waveform/import', { method: 'POST', body: fd });
  if (!resp.ok) {
    const text = await resp.text().catch(() => '');
    let msg = text;
    try {
      const parsed = JSON.parse(text);
      if (parsed?.message) msg = parsed.message;
    } catch { /* not json */ }
    throw new Error(msg || `${resp.status} ${resp.statusText}`);
  }
  return resp.json();
}

/**
 * Emit SPICE source translated for a target dialect (ngspice|berkeley3|
 * ltspice|kicad). DESIGN.md §10.5.
 * @param {object} circuit
 * @param {string} target
 * @returns {Promise<string>} SPICE source
 */
export async function exportNetlist(circuit, target) {
  const resp = await fetch(`/api/circuit/export?target=${encodeURIComponent(target)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/plain' },
    body: JSON.stringify(circuit),
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => '');
    throw new Error(body || `${resp.status} ${resp.statusText}`);
  }
  return resp.text();
}
