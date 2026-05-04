// Component palette for the schematic editor. Items are drag-from-here
// sources for the canvas; the kind travels in the dataTransfer envelope
// `application/x-circuit-kind` and the canvas's onDrop instantiates a
// new Component at the cursor (see Canvas.jsx#handleDrop).

import { useUI } from '../../store/index.js';

const GROUPS = [
  { label: 'Passive', kinds: ['resistor', 'capacitor', 'inductor'] },
  { label: 'Sources', kinds: ['voltage_source', 'current_source'] },
  { label: 'Active',  kinds: ['subcircuit'] },
];

const ICONS = {
  resistor:       'M0 5 H4 L6 1 L9 9 L12 1 L15 9 L17 5 H22',
  capacitor:      'M0 5 H9 M9 1 V9 M13 1 V9 M13 5 H22',
  inductor:       'M0 5 H3 a2.5 2.5 0 0 1 4 0 a2.5 2.5 0 0 1 4 0 a2.5 2.5 0 0 1 4 0 a2.5 2.5 0 0 1 4 0 H22',
  voltage_source: null, // rendered as circle
  current_source: null, // rendered as circle
  subcircuit:     null, // rendered as circle
};

const LABELS = {
  resistor:       'Resistor',
  capacitor:      'Capacitor',
  inductor:       'Inductor',
  voltage_source: 'Voltage src',
  current_source: 'Current src',
  subcircuit:     'Subcircuit',
};

export default function Palette() {
  const library = useUI((s) => s.library);
  const known = new Set(library.map((c) => c.kind));

  return (
    <div className="palette" aria-label="Component palette">
      {GROUPS.map((g) => (
        <div key={g.label}>
          <div className="palgroup">{g.label}</div>
          {g.kinds.map((k) => (
            <PaletteItem
              key={k}
              kind={k}
              available={known.size === 0 || known.has(k)}
            />
          ))}
        </div>
      ))}
      <div className="palette-hint">drag onto canvas to place</div>
    </div>
  );
}

function PaletteItem({ kind, available }) {
  function onDragStart(ev) {
    if (!available) { ev.preventDefault(); return; }
    ev.dataTransfer.setData('application/x-circuit-kind', kind);
    ev.dataTransfer.effectAllowed = 'copy';
  }

  return (
    <div
      className={`palitem ${available ? '' : 'palitem--disabled'}`}
      title={LABELS[kind]}
      draggable={available}
      onDragStart={onDragStart}
    >
      <PaletteIcon kind={kind} />
      <span>{LABELS[kind]}</span>
    </div>
  );
}

function PaletteIcon({ kind }) {
  if (ICONS[kind]) {
    return (
      <svg width={22} height={10} viewBox="0 0 22 10" fill="none" stroke="currentColor" strokeWidth={1}>
        <path d={ICONS[kind]} />
      </svg>
    );
  }
  if (kind === 'subcircuit') {
    return (
      <svg width={22} height={14} viewBox="0 0 22 14" fill="none" stroke="currentColor" strokeWidth={1}>
        <circle cx={11} cy={7} r={5.5} />
        <path d="M5 7 H17" strokeDasharray="1.5 1.2" />
      </svg>
    );
  }
  if (kind === 'voltage_source') {
    return (
      <svg width={22} height={14} viewBox="0 0 22 14" fill="none" stroke="currentColor" strokeWidth={1}>
        <circle cx={11} cy={7} r={5} />
        <path d="M7 7 q2 -3 4 0 t4 0" />
      </svg>
    );
  }
  return (
    <svg width={22} height={14} viewBox="0 0 22 14" fill="none" stroke="currentColor" strokeWidth={1}>
      <circle cx={11} cy={7} r={5} />
      <path d="M11 4 V10 M9 7 H13" />
    </svg>
  );
}
