// Vertical drag handle that resizes the column to its right. Used between
// the screen pane and the side control panel on the Scope, Spectrum,
// Network, and Netlist tabs.
//
// Computes the new width from the parent container's right edge so the
// splitter works regardless of the surrounding chrome (sidebar, padding,
// etc.). Pointer capture keeps drag events flowing even when the cursor
// leaves the 6px hit area, which makes thin handles ergonomic.

import { useRef } from 'react';

/**
 * @param {{
 *   width: number,                     // current right-pane width in px
 *   onChange: (next: number) => void,  // called with clamped width during drag
 *   min?: number,                      // minimum allowed width (default 200)
 *   max?: number,                      // maximum allowed width (default 600)
 *   title?: string,                    // hover tooltip
 * }} props
 */
export default function Splitter({ width, onChange, min = 200, max = 600, title = 'Drag to resize' }) {
  const handleRef = useRef(null);
  const dragRef = useRef({ active: false, parentRight: 0 });

  const onPointerDown = (ev) => {
    const el = handleRef.current;
    const parent = el?.parentElement;
    if (!parent) return;
    ev.preventDefault();
    const rect = parent.getBoundingClientRect();
    dragRef.current = { active: true, parentRight: rect.right };
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    el.setPointerCapture?.(ev.pointerId);
  };

  const onPointerMove = (ev) => {
    const d = dragRef.current;
    if (!d.active) return;
    const next = Math.max(min, Math.min(max, d.parentRight - ev.clientX));
    onChange(next);
  };

  const endDrag = () => {
    if (!dragRef.current.active) return;
    dragRef.current.active = false;
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  };

  return (
    <div
      ref={handleRef}
      className="splitter splitter-v"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      role="separator"
      aria-orientation="vertical"
      aria-valuenow={Math.round(width)}
      aria-valuemin={min}
      aria-valuemax={max}
      title={title}
    />
  );
}
