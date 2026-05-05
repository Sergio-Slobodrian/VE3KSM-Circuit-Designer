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
