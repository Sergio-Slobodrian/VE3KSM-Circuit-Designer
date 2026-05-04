// Per-channel measurement readouts under the scope screen. The bar shows the
// six measurements DESIGN.md §6.2 calls out (Vpp, Vrms, mean, freq, period,
// phase) for each enabled channel; phase is relative to CH1.

import { formatEng } from '../../lib/measurements.js';

/**
 * @param {{
 *   derived: Array<null | {
 *     vpp: number, vrms: number, mean: number,
 *     frequency: number, period: number, phaseDeg: number,
 *     channel: { id: string, label: string, color: string }
 *   }>,
 *   cursor?: null | {
 *     time: number,
 *     channels: Array<{ label: string, color: string, voltage: number }>,
 *   },
 * }} props
 */
export default function MeasurementBar({ derived, cursor }) {
  const visible = derived.filter((d) => d != null);
  if (visible.length === 0) {
    return (
      <div className="scope-meas-bar empty">
        Run an analysis to populate measurements.
      </div>
    );
  }
  return (
    <div className="scope-meas-bar">
      <CursorBlock cursor={cursor} />
      {visible.map((d) => (
        <ChannelBlock key={d.channel.id} d={d} />
      ))}
    </div>
  );
}

function CursorBlock({ cursor }) {
  // Always reserve the column so the per-channel blocks don't reflow when
  // the user moves their mouse on or off the plot. When idle, show em-dashes
  // so it's obvious the cursor isn't tracking anything yet.
  const idle = cursor == null;
  return (
    <div className="meas-block meas-cursor">
      <div className="meas-head">Cursor</div>
      <div className="meas-cell">
        <span className="l">Time</span>
        <span className="v">{idle ? '—' : formatEng(cursor.time, 's')}</span>
      </div>
      {idle && (
        <div className="meas-cell">
          <span className="l">hover trace</span>
          <span className="v">—</span>
        </div>
      )}
      {!idle && cursor.channels.map((c) => (
        <div className="meas-cell" key={c.label}>
          <span className="l" style={{ color: c.color }}>{c.label}</span>
          <span className="v">{formatEng(c.voltage, 'V')}</span>
        </div>
      ))}
    </div>
  );
}

function ChannelBlock({ d }) {
  const { channel } = d;
  return (
    <div className="meas-block" style={{ borderLeftColor: channel.color }}>
      <div className="meas-head">{channel.label}</div>
      <Cell label="Vpp"    value={formatEng(d.vpp, 'V')} />
      <Cell label="Vrms"   value={formatEng(d.vrms, 'V')} />
      <Cell label="Mean"   value={formatEng(d.mean, 'V')} />
      <Cell label="Freq"   value={formatEng(d.frequency, 'Hz')} />
      <Cell label="Period" value={formatEng(d.period, 's')} />
      <Cell label="Phase"  value={formatPhase(d.phaseDeg)} />
    </div>
  );
}

function Cell({ label, value }) {
  return (
    <div className="meas-cell">
      <span className="l">{label}</span>
      <span className="v">{value}</span>
    </div>
  );
}

function formatPhase(deg) {
  if (!Number.isFinite(deg)) return '—';
  return `${deg.toFixed(1)}°`;
}
