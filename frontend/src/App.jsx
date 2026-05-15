// Five-tab shell. Schematic is the milestone-4 deliverable; the rest stay
// placeholder cards until their owning milestone (DESIGN.md §13) lands.
//
// On mount, the shell:
//   1. pings /api/healthz so the status bar can show backend connectivity,
//   2. loads /api/library to populate the palette,
//   3. loads /api/examples and opens the first one (preamp_12ax7) so the
//      schematic canvas is non-empty on first paint.

import { useEffect, useRef } from 'react';
import Schematic from './views/schematic/Schematic.jsx';
import Scope from './views/scope/Scope.jsx';
import Spectrum from './views/spectrum/Spectrum.jsx';
import Network from './views/network/Network.jsx';
import Netlist from './views/netlist/Netlist.jsx';
import { useCircuit, useSimulation, useSpectrum, useNetwork, useUI } from './store/index.js';

const TABS = [
  { id: 'schematic', label: 'Schematic'        },
  { id: 'scope',     label: 'Scope'            },
  { id: 'spectrum',  label: 'Spectrum'         },
  { id: 'network',   label: 'Network analyzer' },
  { id: 'netlist',   label: 'Netlist'          },
];

export default function App() {
  const activeTab = useUI((s) => s.activeTab);
  const setTab = useUI((s) => s.setTab);
  const backendOnline = useUI((s) => s.backendOnline);
  const pingBackend = useUI((s) => s.pingBackend);
  const loadLibrary = useUI((s) => s.loadLibrary);
  const sourceName = useCircuit((s) => s.sourceName);
  const catalog = useCircuit((s) => s.catalog);
  const loadCatalog = useCircuit((s) => s.loadCatalog);
  const load = useCircuit((s) => s.load);

  useEffect(() => {
    pingBackend();
    loadLibrary();
    loadCatalog();
  }, [pingBackend, loadLibrary, loadCatalog]);

  // First-paint bootstrap: open the preferred example once the catalog
  // arrives. Guarded by a ref so it only fires on initial mount — otherwise
  // hitting Schematic's "New" button (which sets sourceName=null on purpose)
  // would immediately reload preamp_12ax7 on top of the blank circuit.
  const didAutoLoad = useRef(false);
  useEffect(() => {
    if (didAutoLoad.current) return;
    if (catalog.length === 0 || sourceName) return;
    didAutoLoad.current = true;
    const preferred = catalog.find((e) => e.name === 'preamp_12ax7') ?? catalog[0];
    load(preferred.name);
  }, [catalog, sourceName, load]);

  // Drop any stale captured frames when the user swaps in a different example —
  // their probe nodes don't match and the old trace would no longer correspond
  // to anything on screen. Same logic for the spectrum + network stores so
  // every analysis tab starts from a clean slate when the user opens a
  // different fixture.
  const resetSim = useSimulation((s) => s.reset);
  const resetSpectrum = useSpectrum((s) => s.reset);
  const resetNetwork = useNetwork((s) => s.reset);
  useEffect(() => {
    resetSim();
    resetSpectrum();
    resetNetwork();
  }, [sourceName, resetSim, resetSpectrum, resetNetwork]);

  return (
    <div className="app-shell">
      <header className="app-titlebar">
        <div className="app-name">
          Circuit Lab <code>{sourceName ? `${sourceName}.cir` : 'no circuit'}</code>
        </div>
        <div className={`app-backend-status ${backendOnline ? 'is-on' : 'is-off'}`}>
          {backendOnline ? 'backend connected' : 'backend offline'}
        </div>
      </header>

      <nav className="app-tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            className={`app-tab ${t.id === activeTab ? 'is-active' : ''}`}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      <main className="app-main">
        {activeTab === 'schematic' && <Schematic />}
        {activeTab === 'scope'     && <Scope />}
        {activeTab === 'spectrum'  && <Spectrum />}
        {activeTab === 'network'   && <Network />}
        {activeTab === 'netlist'   && <Netlist />}
      </main>
    </div>
  );
}

function Placeholder({ tab }) {
  return (
    <div className="placeholder">
      <div className="placeholder-title">{tab} — not implemented yet</div>
      <div className="placeholder-body">
        See <code>mockups/</code> for the visual contract and <code>DESIGN.md</code> §6
        for the per-view spec. Implementation order is tracked in <code>DESIGN.md</code> §13.
      </div>
    </div>
  );
}
