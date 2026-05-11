// Inspector pane — milestone 7 made it editable. Selecting a single
// component reveals the value/ref/model fields as inputs that commit on
// blur (or Enter). The rotate/mirror/delete buttons act on the entire
// selection, not just the primary, so multi-select operations have a
// mouse-driven affordance for users who don't reach for keyboard shortcuts.
//
// Milestone 10: V/I sources now expose the full 11-waveform signal generator
// panel via <SignalGenerator/>. The raw `value` text input is gone for those
// kinds; everything that used to live in `value` (the SPICE source spec
// shorthand) now flows through SourceSpec.Mode + Params.

import { useEffect, useMemo, useState } from 'react';
import { useCircuit, useSelection, useUI } from '../../store/index.js';
import { findFamily } from '../../lib/library.js';
import SignalGenerator from './SignalGenerator.jsx';

const KIND_LABEL = {
  resistor: 'Resistor',
  capacitor: 'Capacitor',
  inductor: 'Inductor',
  voltage_source: 'Voltage source',
  current_source: 'Current source',
  subcircuit: 'Subcircuit',
};

export default function Inspector() {
  const circuit = useCircuit((s) => s.circuit);
  const updateComponent = useCircuit((s) => s.updateComponent);
  const rotateComponents = useCircuit((s) => s.rotateComponents);
  const mirrorComponents = useCircuit((s) => s.mirrorComponents);
  const deleteComponents = useCircuit((s) => s.deleteComponents);
  const addProbe = useCircuit((s) => s.addProbe);
  const removeProbe = useCircuit((s) => s.removeProbe);
  const selectedRefs = useSelection((s) => s.selectedRefs);
  const clearSel = useSelection((s) => s.clear);
  const setSelection = useSelection((s) => s.setSelection);

  const primaryRef = selectedRefs[0] ?? null;
  const comp = (circuit?.components || []).find((c) => c.ref === primaryRef);

  // Resolve the component's library family. For multi-variant imports the
  // family carries a `variants` list which the Model field renders as a
  // dropdown rather than a freeform text input.
  const library = useUI((s) => s.library);
  const family = useMemo(() => (comp ? findFamily(library, comp) : null), [library, comp]);

  if (selectedRefs.length === 0) {
    return (
      <div className="inspector">
        <div className="insp-empty">
          Select a component on the canvas to inspect or edit its properties.
          <br /><br />
          Drop a component from the palette to add one. Use{' '}
          <kbd>R</kbd>/<kbd>F</kbd>/<kbd>Del</kbd> to rotate, mirror, or delete.
          <br /><br />
          <kbd>wheel</kbd> zooms, <kbd>middle-drag</kbd> or <kbd>alt-drag</kbd>{' '}
          pans, <kbd>0</kbd> refits, <kbd>+</kbd>/<kbd>-</kbd> step zoom.
        </div>
      </div>
    );
  }

  // Multi-select: show the count + group actions only. The "primary" inputs
  // would be confusing when they only edit one of several selected items.
  if (selectedRefs.length > 1) {
    return (
      <div className="inspector">
        <div className="insp-head">
          <span className="insp-title">{selectedRefs.length} components selected</span>
          <span className="insp-pill">multi</span>
        </div>
        <ActionRow
          onRotate={() => rotateComponents(selectedRefs)}
          onRotateCCW={() => rotateComponents(selectedRefs, -90)}
          onMirror={() => mirrorComponents(selectedRefs)}
          onDelete={() => { deleteComponents(selectedRefs); clearSel(); }}
        />
        <ul className="insp-multilist">
          {selectedRefs.map((r) => (
            <li key={r}>
              <button
                type="button"
                className="insp-multilink"
                onClick={() => setSelection([r])}
              >
                {r}
              </button>
            </li>
          ))}
        </ul>
      </div>
    );
  }

  if (!comp) {
    return (
      <div className="inspector">
        <div className="insp-empty">Selected component not found.</div>
      </div>
    );
  }

  return (
    <div className="inspector">
      <div className="insp-head">
        <span className="insp-title">{comp.ref} — {KIND_LABEL[comp.kind] ?? comp.kind}</span>
        <span className="insp-sub">{(comp.nodes || []).join(' → ')}</span>
        <span className="insp-pill">edit</span>
      </div>

      <ActionRow
        onRotate={() => rotateComponents([comp.ref])}
        onRotateCCW={() => rotateComponents([comp.ref], -90)}
        onMirror={() => mirrorComponents([comp.ref])}
        onDelete={() => { deleteComponents([comp.ref]); clearSel(); }}
      />

      <ProbeRow
        comp={comp}
        probes={circuit?.probes || []}
        onAdd={(node) => addProbe(node, 'voltage')}
        onRemove={(node) => removeProbe(node)}
      />

      <dl className="insp-fields">
        <Field name="Ref">
          <TextInput
            value={comp.ref}
            onCommit={(next) => {
              if (!next || next === comp.ref) return;
              updateComponent(comp.ref, { ref: next });
              setSelection([next]);
            }}
            mono
          />
        </Field>

        {isPassive(comp.kind) && (
          <Field name="Value">
            <TextInput
              value={comp.value || ''}
              placeholder={defaultValuePlaceholder(comp.kind)}
              onCommit={(next) => updateComponent(comp.ref, { value: next })}
              mono
            />
          </Field>
        )}

        {comp.kind === 'subcircuit' && (
          <Field name="Model">
            {family && Array.isArray(family.variants) && family.variants.length > 1 ? (
              <VariantSelect
                value={comp.model || ''}
                variants={family.variants}
                onChange={(next) => {
                  const v = family.variants.find((x) => x.model_name === next);
                  const patch = { model: next };
                  // Carry the variant's default_value too so the component's
                  // value reflects the selected part — Würth uses this for
                  // capacitance, Coilcraft for inductance, etc.
                  if (v?.default_value) patch.value = v.default_value;
                  updateComponent(comp.ref, patch);
                }}
              />
            ) : (
              <TextInput
                value={comp.model || ''}
                placeholder="12AX7"
                onCommit={(next) => updateComponent(comp.ref, { model: next })}
                mono
              />
            )}
          </Field>
        )}

        {/* Non-source components still get a read-only source/params summary */}
        {(comp.kind !== 'voltage_source' && comp.kind !== 'current_source') && comp.source?.mode &&
          <ReadOnlyField name="Source mode" value={comp.source.mode.toUpperCase()} />}
        {(comp.kind !== 'voltage_source' && comp.kind !== 'current_source') && comp.source?.ac && (
          <ReadOnlyField name="AC stim" value={`${comp.source.ac.magnitude || '1'} ∠ ${comp.source.ac.phase || '0'}°`} />
        )}

        {family?.description && (
          <DescriptionField description={family.description} />
        )}

        <ReadOnlyField name="Position" value={`(${comp.layout?.x ?? 0}, ${comp.layout?.y ?? 0})`} />
        {comp.layout?.rot ? <ReadOnlyField name="Rotation" value={`${comp.layout.rot}°`} /> : null}
        {comp.layout?.mirror ? <ReadOnlyField name="Mirrored" value="yes" /> : null}
        {comp.params && Object.entries(comp.params).map(([k, v]) => (
          <ReadOnlyField key={`p-${k}`} name={k} value={v} />
        ))}
      </dl>

      {(comp.kind === 'voltage_source' || comp.kind === 'current_source') && (
        <SignalGenerator comp={comp} />
      )}
    </div>
  );
}

// ProbeRow lists each non-ground node on the selected component with a
// single-button toggle: + probe when unprobed, ✓ probe when already covered.
// Single-node components (R/C/L between two pins) get one row per terminal so
// the user can probe either side; for V/I sources the positive terminal is
// what the auto-attach in addComponent chose, but the user can probe either.
function ProbeRow({ comp, probes, onAdd, onRemove }) {
  const nodes = (comp.nodes || []).filter((n) => n && n !== '0');
  if (nodes.length === 0) return null;
  const probedSet = new Set(probes.filter((p) => p.kind === 'voltage').map((p) => p.node));
  return (
    <div className="insp-probes" role="group" aria-label="Probes">
      <span className="insp-probes-label">Probe</span>
      {nodes.map((node) => {
        const probed = probedSet.has(node);
        return (
          <button
            key={node}
            type="button"
            className={`insp-probe-btn${probed ? ' is-probed' : ''}`}
            onClick={() => probed ? onRemove(node) : onAdd(node)}
            title={probed ? `Remove probe at ${node}` : `Add voltage probe at ${node}`}
          >
            {probed ? '✓' : '+'} {node}
          </button>
        );
      })}
    </div>
  );
}

function ActionRow({ onRotate, onRotateCCW, onMirror, onDelete }) {
  return (
    <div className="insp-actions" role="toolbar">
      <button type="button" className="insp-btn" onClick={onRotateCCW} title="Rotate 90° CCW (Shift+R)">
        ↺
      </button>
      <button type="button" className="insp-btn" onClick={onRotate} title="Rotate 90° CW (R)">
        ↻
      </button>
      <button type="button" className="insp-btn" onClick={onMirror} title="Mirror (F)">
        ⇋
      </button>
      <button type="button" className="insp-btn insp-btn--danger" onClick={onDelete} title="Delete (Del)">
        ✕
      </button>
    </div>
  );
}

function Field({ name, children }) {
  return (
    <>
      <dt>{name}</dt>
      <dd>{children}</dd>
    </>
  );
}

function ReadOnlyField({ name, value }) {
  return (
    <>
      <dt>{name}</dt>
      <dd>{value}</dd>
    </>
  );
}

// DescriptionField renders a library family's description. An http(s) URL
// inside the text is split out and rendered as a `<a target="_blank">` so the
// user can jump to the manufacturer datasheet from the inspector. React
// escapes the surrounding text by default so the only sanitisation we need is
// the URL pattern itself.
function DescriptionField({ description }) {
  const segments = useMemo(() => splitDescriptionURLs(description), [description]);
  return (
    <>
      <dt>Description</dt>
      <dd className="insp-desc">
        {segments.map((seg, i) =>
          seg.url ? (
            <a
              key={i}
              href={seg.url}
              target="_blank"
              rel="noopener noreferrer"
              className="insp-desc-link"
            >
              {seg.text}
            </a>
          ) : (
            <span key={i}>{seg.text}</span>
          ),
        )}
      </dd>
    </>
  );
}

// splitDescriptionURLs returns an array of segments where http(s) URLs are
// tagged with `url`. The regex deliberately stops at whitespace and common
// punctuation that LTspice users tend to put after the URL ("…datasheet:
// https://… .") so trailing periods don't get baked into the href.
function splitDescriptionURLs(text) {
  if (!text) return [];
  const re = /https?:\/\/[^\s<>"'()\[\]]+[^\s<>"'()\[\].,;:!?]/g;
  const out = [];
  let last = 0;
  let m;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push({ text: text.slice(last, m.index) });
    out.push({ text: m[0], url: m[0] });
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push({ text: text.slice(last) });
  return out;
}

// Controlled input that commits the value on blur or Enter — keeps the user's
// keystrokes local and only pushes to the store when they finish, so the
// undo stack records the whole edit as one frame.
function TextInput({ value, onCommit, placeholder, mono }) {
  const [draft, setDraft] = useState(value ?? '');
  // Reset the draft when the underlying value changes (selection switch,
  // undo/redo) — otherwise the input would shadow the canonical value.
  useEffect(() => { setDraft(value ?? ''); }, [value]);

  function commit() {
    if (draft === value) return;
    onCommit(draft);
  }

  return (
    <input
      type="text"
      className={mono ? 'insp-input insp-input--mono' : 'insp-input'}
      value={draft}
      placeholder={placeholder}
      onChange={(ev) => setDraft(ev.target.value)}
      onBlur={commit}
      onKeyDown={(ev) => {
        if (ev.key === 'Enter') { commit(); ev.target.blur(); }
        else if (ev.key === 'Escape') { setDraft(value ?? ''); ev.target.blur(); }
      }}
    />
  );
}

// VariantSelect renders a <select> over the family's variants. The label is
// `${shortLabel} — ${model_name}` so the user sees both the friendly value
// (e.g. "2.2mF") and the full SPICE model name they're committing to.
function VariantSelect({ value, variants, onChange }) {
  return (
    <select
      className="insp-input insp-input--mono insp-variant"
      value={value}
      onChange={(ev) => onChange(ev.target.value)}
    >
      {/* Tolerate legacy values that aren't in the family (e.g. user typed a
          custom model before the .asy enrichment landed) — show as a
          dangling option so the value isn't silently overridden. */}
      {!variants.some((v) => v.model_name === value) && value && (
        <option value={value}>{value} (custom)</option>
      )}
      {variants.map((v) => (
        <option key={v.model_name} value={v.model_name}>
          {v.label && v.label !== v.model_name
            ? `${v.label} — ${v.model_name}`
            : v.model_name}
        </option>
      ))}
    </select>
  );
}

function isPassive(kind) {
  return kind === 'resistor' || kind === 'capacitor' || kind === 'inductor';
}

function defaultValuePlaceholder(kind) {
  switch (kind) {
    case 'resistor':  return '1k';
    case 'capacitor': return '10n';
    case 'inductor':  return '1m';
    default:          return '';
  }
}
