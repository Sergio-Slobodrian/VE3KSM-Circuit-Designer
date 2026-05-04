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
