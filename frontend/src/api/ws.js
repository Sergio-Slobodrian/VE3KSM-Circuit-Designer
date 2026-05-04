// WebSocket client for the Circuit Lab JSON envelope protocol (DESIGN.md §11
// plus the milestone-3 extensions documented in api/protocol.go).
//
// Public API:
//   const sock = openSocket({ onOpen, onClose, onError })
//   sock.send({ op, id, payload })
//   sock.on(op, handler)         // returns an unsubscribe fn
//   sock.close()
//
// The connection upgrades against the dev-proxy `/ws` path (see vite.config.js)
// in development, and against the same-origin `/ws` path in production. Both
// resolve to the Go backend.
//
// Reconnect is *not* automatic — Scope-tab use is request/response and
// reconnecting silently would mask a backend that has actually gone away.
// Callers re-open if they want to retry.

let nextID = 1;

/** @typedef {{op: string, id?: string, payload?: any}} Envelope */

/**
 * @param {{
 *   onOpen?: () => void,
 *   onClose?: (ev: CloseEvent) => void,
 *   onError?: (ev: Event) => void,
 *   onAnyMessage?: (env: Envelope) => void,
 * }} [opts]
 */
export function openSocket(opts = {}) {
  const url = wsURL('/ws');
  const conn = new WebSocket(url);

  /** @type {Map<string, Set<(env: Envelope) => void>>} */
  const handlers = new Map();

  conn.addEventListener('open', () => opts.onOpen?.());
  conn.addEventListener('close', (ev) => opts.onClose?.(ev));
  conn.addEventListener('error', (ev) => opts.onError?.(ev));
  conn.addEventListener('message', (ev) => {
    let env;
    try { env = JSON.parse(ev.data); }
    catch (err) {
      console.warn('ws: bad json', err);
      return;
    }
    opts.onAnyMessage?.(env);
    const set = handlers.get(env.op);
    if (set) for (const h of set) h(env);
  });

  return {
    /** @returns {0|1|2|3} CONNECTING|OPEN|CLOSING|CLOSED */
    get readyState() { return conn.readyState; },

    /** @param {Envelope} env */
    send(env) {
      if (conn.readyState !== WebSocket.OPEN) {
        throw new Error(`ws.send: socket not open (state ${conn.readyState})`);
      }
      conn.send(JSON.stringify(env));
    },

    /** @param {string} op @param {(env: Envelope) => void} handler */
    on(op, handler) {
      let set = handlers.get(op);
      if (!set) { set = new Set(); handlers.set(op, set); }
      set.add(handler);
      return () => set.delete(handler);
    },

    close() {
      try { conn.close(); } catch { /* ignore */ }
    },
  };
}

/** Build the absolute ws:// URL for a backend path. */
export function wsURL(path) {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}${path}`;
}

/** Generate a fresh correlation id for an envelope. */
export function nextEnvelopeID(prefix = 'r') {
  return `${prefix}-${nextID++}`;
}

/**
 * runAnalysisStream — open a fresh WebSocket, load the circuit, run the given
 * analysis, and dispatch lifecycle events to the callbacks. Returns a handle
 * whose `cancel()` aborts the run by sending `sim.cancel` and closing the WS.
 *
 * The Scope tab uses its own run() (in useSimulation) because it needs
 * per-rAF frame batching for high-rate transient streams. Spectrum and Bode
 * runs are a single shot (one frame per FFT bin or sweep point), so the
 * frames-collected-into-an-array pattern this helper enables is sufficient.
 *
 * @param {{
 *   circuit: object,
 *   analysis: { kind: string, args: string[], options?: Record<string, string> },
 *   onConnecting?: () => void,
 *   onLoading?: () => void,
 *   onRunning?: (runID: string) => void,
 *   onFrame?: (frame: { x: number, values: Record<string, number> }) => void,
 *   onDone?: (payload?: { run_id: string, frame_count: number }) => void,
 *   onError?: (message: string) => void,
 * }} opts
 * @returns {{ cancel: () => void }}
 */
export function runAnalysisStream(opts) {
  const { circuit, analysis } = opts;
  let socket = null;
  let activeRunID = null;
  let finished = false;

  const teardown = () => {
    if (finished) return;
    finished = true;
    if (socket) { socket.close(); socket = null; }
  };

  socket = openSocket({
    onOpen: () => {
      if (finished) return;
      opts.onLoading?.();
      socket.send({
        op: 'circuit.load',
        id: nextEnvelopeID('load'),
        payload: { circuit },
      });
    },
    onClose: () => {
      // Only surface the close as an error when nothing else has wrapped up
      // first — the done/error/cancel paths close the socket themselves.
      if (finished) return;
      teardown();
      opts.onError?.('connection closed unexpectedly');
    },
    onError: () => {
      if (finished) return;
      teardown();
      opts.onError?.('websocket error');
    },
  });
  opts.onConnecting?.();

  socket.on('circuit.changed', () => {
    if (finished || !socket) return;
    const id = nextEnvelopeID('run');
    activeRunID = id;
    opts.onRunning?.(id);
    socket.send({
      op: 'sim.run',
      id,
      payload: { analysis },
    });
  });

  socket.on('sim.result', (env) => {
    if (finished || env.id !== activeRunID) return;
    const f = env.payload?.frame;
    if (f) opts.onFrame?.(f);
  });

  socket.on('sim.done', (env) => {
    if (finished || env.id !== activeRunID) return;
    const payload = env.payload;
    teardown();
    opts.onDone?.(payload);
  });

  socket.on('sim.error', (env) => {
    if (finished || env.id !== activeRunID) return;
    const err = env.payload?.error;
    teardown();
    opts.onError?.(err?.message || 'simulation failed');
  });

  socket.on('error', (env) => {
    if (finished) return;
    const ep = env.payload || {};
    teardown();
    opts.onError?.(`${ep.code || 'error'}: ${ep.message || ''}`);
  });

  return {
    cancel() {
      if (finished) return;
      if (socket && activeRunID) {
        try {
          socket.send({
            op: 'sim.cancel',
            id: nextEnvelopeID('cancel'),
            payload: { run_id: activeRunID },
          });
        } catch { /* socket may already be closed */ }
      }
      teardown();
    },
  };
}
