// Outline + sync side panel for the netlist tab. Reads from the parsed
// Circuit (when available) so counts always reflect what the engine would
// see. The "Last edit" / "Round-trip" rows surface the bidirectional-sync
// state — the operator wants to know which side is canonical right now.

import { useMemo } from 'react';

const DIALECT_LABELS = {
  ngspice:   'ngspice 42',
  berkeley3: 'Berkeley SPICE 3',
  ltspice:   'LTspice',
  kicad:     'KiCad',
};

/**
 * @param {object} props
 * @param {object|null} props.circuit  current Circuit (may be null while loading)
 * @param {{state:'in_sync'|'editing'|'error', detail?:string}} props.sync
 * @param {string} props.lastEdit            human label (e.g. "schematic 12s")
 * @param {string} props.target              dialect id
 * @param {(t:string)=>void} props.onTargetChange
 */
export default function NetlistOutline({ circuit, sync, lastEdit, target, onTargetChange }) {
  const counts = useMemo(() => summarize(circuit), [circuit]);

  return (
    <aside className="ne-outline">
      <section className="ne-section">
        <h4>Structure</h4>
        <Row label="Header"      value={counts.header} />
        <Row label="Libraries"   value={counts.libraries} />
        <Row label="Parameters"  value={counts.parameters} />
        <Row label="Sources"     value={counts.sources} />
        <Row label="Passives"    value={counts.passives} />
        <Row label="Subcircuits" value={counts.subcircuits} />
        <Row label="Probes"      value={counts.probes} />
      </section>

      {counts.libraryItems.length > 0 && (
        <section className="ne-section">
          <h4>Libraries</h4>
          {counts.libraryItems.map((p) => (
            <div key={p} className="ne-lib-item">{p}</div>
          ))}
        </section>
      )}

      <section className="ne-section">
        <h4>Analyses</h4>
        {counts.analyses.length === 0
          ? <div className="ne-ana-item is-off">no analyses defined</div>
          : counts.analyses.map((a, i) => (
              <div key={i} className={`ne-ana-item ${a.enabled ? 'is-on' : 'is-off'}`}>
                <span className="cmd">
                  {a.enabled ? '✓ ' : '◌ '}.{a.kind.toUpperCase()} {a.args.join(' ')}
                </span>
                <span className="cmt">{describe(a)}</span>
              </div>
            ))}
      </section>

      <section className="ne-section">
        <h4>Sync</h4>
        <Row label="State"      value={syncBadge(sync)} ok={sync.state === 'in_sync'} />
        <Row label="Last edit"  value={lastEdit} />
        <Row label="Round-trip" value="lossless" />
      </section>

      <section className="ne-section">
        <h4>Target</h4>
        <select
          className="ne-target-select"
          value={target}
          onChange={(e) => onTargetChange(e.target.value)}
        >
          {Object.entries(DIALECT_LABELS).map(([id, label]) => (
            <option key={id} value={id}>{label}</option>
          ))}
        </select>
      </section>
    </aside>
  );
}

function Row({ label, value, ok }) {
  return (
    <div className="ne-row">
      <span className="ne-row-lab">{label}</span>
      <span className={`ne-row-val ${ok ? 'is-ok' : ''}`}>{value}</span>
    </div>
  );
}

function syncBadge(sync) {
  switch (sync.state) {
    case 'in_sync': return '⇄ in sync';
    case 'editing': return '… editing';
    case 'error':   return `✗ ${sync.detail || 'parse error'}`;
    default:        return sync.state;
  }
}

function summarize(c) {
  if (!c) {
    return {
      header: 0, libraries: 0, parameters: 0, sources: 0, passives: 0,
      subcircuits: 0, probes: 0, libraryItems: [], analyses: [],
    };
  }
  const components = c.components || [];
  const sources = components.filter((x) => x.kind === 'voltage_source' || x.kind === 'current_source').length;
  const passives = components.filter((x) => x.kind === 'resistor' || x.kind === 'capacitor' || x.kind === 'inductor').length;
  const subcircuits = components.filter((x) => x.kind === 'subcircuit').length;
  const headerLines = (c.title ? 1 : 0) + (c.comments?.length || 0);

  return {
    header: headerLines,
    libraries: c.libraries?.length || 0,
    parameters: c.parameters?.length || 0,
    sources,
    passives,
    subcircuits,
    probes: c.probes?.length || 0,
    libraryItems: (c.libraries || []).map((l) => l.path),
    analyses: c.analyses || [],
  };
}

function describe(a) {
  const k = (a.kind || '').toLowerCase();
  if (!a.enabled) return 'commented out';
  switch (k) {
    case 'tran':  return `transient, ${a.args[1] ?? '?'} window`;
    case 'ac':    return `AC sweep, ${a.args[1] ?? '?'} pts/${a.args[0] ?? '?'}`;
    case 'dc':    return 'DC sweep';
    case 'op':    return 'operating point';
    case 'noise': return 'noise analysis';
    default:      return k;
  }
}
