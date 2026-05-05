// Plotly trace for the Spectrum tab. Plotly because its log x-axis,
// hover tooltips, pan/zoom, and shape-based marker overlays come for free
// (DESIGN.md §10.3). uPlot would have been smaller but the marker UX would
// take a lot of code to match.

import { useEffect, useMemo, useRef } from 'react';
import Plotly from 'plotly.js-dist-min';

const TRACE_COLOR = '#5dcaa5';
const HOLD_COLOR  = 'rgba(212, 83, 126, 0.55)'; // accent-pink at lower alpha
const PEAK_COLOR  = '#f5b840';

/**
 * @param {{
 *   freqs: ArrayLike<number>,
 *   mag: ArrayLike<number> | null,
 *   holdMag: ArrayLike<number> | null,
 *   probe: string | null,
 *   markers: { m1: number|null, m2: number|null },
 *   peak: { freq: number, mag_db: number } | null,
 *   detector: string,
 *   harmonicMarkers?: Array<{ n: number, freq: number, mag_db: number }> | null,
 *   onMarkerSet?: (slot: 'm1'|'m2', freq: number) => void,
 * }} props
 */
export default function SpectrumPlot({ freqs, mag, holdMag, probe, markers, peak, harmonicMarkers, onMarkerSet }) {
  const containerRef = useRef(null);

  // Build trace + layout objects fresh on every render. Plotly.react diffs
  // them internally so this is cheap; we don't do `setData`-style updates
  // here because Plotly's API doesn't make that ergonomic for shape changes
  // (markers, peak annotation).
  const fig = useMemo(() => {
    const traces = [];
    if (mag && freqs.length) {
      traces.push({
        x: Array.from(freqs),
        y: Array.from(mag),
        type: 'scattergl',     // GL renderer handles 4k+ bins smoothly
        mode: 'lines',
        line: { color: TRACE_COLOR, width: 1.4 },
        name: probe || 'trace',
        hovertemplate: '%{x:.0f} Hz<br>%{y:.2f} dB<extra></extra>',
      });
    }
    if (holdMag && holdMag.length === freqs.length && holdMag.some((v) => v !== 0)) {
      traces.push({
        x: Array.from(freqs),
        y: Array.from(holdMag),
        type: 'scattergl',
        mode: 'lines',
        line: { color: HOLD_COLOR, width: 1, dash: 'dot' },
        name: 'max hold',
        hovertemplate: 'max-hold %{x:.0f} Hz<br>%{y:.2f} dB<extra></extra>',
      });
    }
    const shapes = [];
    const annotations = [];
    if (markers?.m1 != null) {
      shapes.push({
        type: 'line', x0: markers.m1, x1: markers.m1, y0: 0, y1: 1,
        xref: 'x', yref: 'paper',
        line: { color: '#5dcaa5', width: 1, dash: 'dash' },
      });
      annotations.push({
        x: markers.m1, y: 1, xref: 'x', yref: 'paper',
        text: 'M1', showarrow: false, font: { color: '#5dcaa5', size: 10 },
        bgcolor: 'rgba(10,13,18,0.6)', borderpad: 2, yshift: -2,
      });
    }
    if (markers?.m2 != null) {
      shapes.push({
        type: 'line', x0: markers.m2, x1: markers.m2, y0: 0, y1: 1,
        xref: 'x', yref: 'paper',
        line: { color: '#d4537e', width: 1, dash: 'dash' },
      });
      annotations.push({
        x: markers.m2, y: 1, xref: 'x', yref: 'paper',
        text: 'M2', showarrow: false, font: { color: '#d4537e', size: 10 },
        bgcolor: 'rgba(10,13,18,0.6)', borderpad: 2, yshift: -2,
      });
    }
    if (peak) {
      annotations.push({
        x: peak.freq, y: peak.mag_db, xref: 'x', yref: 'y',
        text: '▼', showarrow: false, font: { color: PEAK_COLOR, size: 14 }, yshift: 10,
      });
    }
    // Harmonic tracking overlay: dashed verticals at every harmonic of f0
    // plus a tiny "Hn" tag at the top so the user can spot them at a glance.
    // Skips harmonics with no resolvable level (e.g. above the swept range).
    if (harmonicMarkers && harmonicMarkers.length > 0) {
      for (const h of harmonicMarkers) {
        if (!Number.isFinite(h.freq) || h.freq <= 0) continue;
        shapes.push({
          type: 'line', x0: h.freq, x1: h.freq, y0: 0, y1: 1,
          xref: 'x', yref: 'paper',
          line: { color: 'rgba(245, 184, 64, 0.45)', width: 0.8, dash: 'dot' },
        });
        annotations.push({
          x: h.freq, y: 0, xref: 'x', yref: 'paper',
          text: `H${h.n}`, showarrow: false,
          font: { color: PEAK_COLOR, size: 9 },
          bgcolor: 'rgba(10,13,18,0.55)', borderpad: 1, yshift: 12,
        });
      }
    }
    const layout = {
      paper_bgcolor: '#0a0d12',
      plot_bgcolor: '#0a0d12',
      font: { family: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', size: 10, color: '#7f8a9c' },
      margin: { l: 48, r: 16, t: 12, b: 36 },
      xaxis: {
        type: 'log',
        title: { text: 'Frequency (Hz)', font: { size: 11 } },
        gridcolor: '#1a2230',
        zerolinecolor: '#2a3548',
        tickfont: { size: 10 },
      },
      yaxis: {
        title: { text: 'Magnitude (dB)', font: { size: 11 } },
        gridcolor: '#1a2230',
        zerolinecolor: '#2a3548',
        tickfont: { size: 10 },
        // Clamp away from -300 dB sentinel so silent bins don't compress
        // the meaningful part of the trace.
        range: [-120, 60],
      },
      shapes,
      annotations,
      showlegend: false,
      dragmode: 'pan',
      hovermode: 'closest',
    };
    const config = {
      displayModeBar: false,
      responsive: true,
      scrollZoom: true,
    };
    return { traces, layout, config };
  }, [freqs, mag, holdMag, probe, markers, peak, harmonicMarkers]);

  // Render / update on every fig change. Plotly.react is the right API: it
  // diffs the layout/data and only redraws what actually changed.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    Plotly.react(el, fig.traces, fig.layout, fig.config);
  }, [fig]);

  // Click-to-set-marker: alt-click for M1, shift-click for M2.
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !onMarkerSet) return;
    const handler = (ev) => {
      if (!ev.points || ev.points.length === 0) return;
      const slot = ev.event?.shiftKey ? 'm2' : 'm1';
      onMarkerSet(slot, ev.points[0].x);
    };
    el.on?.('plotly_click', handler);
    return () => { el.removeAllListeners?.('plotly_click'); };
  }, [onMarkerSet, fig]);

  // Resize on container changes.
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => Plotly.Plots.resize(el));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Final cleanup — purge the Plotly instance so node detach is clean.
  useEffect(() => {
    const el = containerRef.current;
    return () => { if (el) Plotly.purge(el); };
  }, []);

  return <div ref={containerRef} className="spectrum-plot" />;
}
