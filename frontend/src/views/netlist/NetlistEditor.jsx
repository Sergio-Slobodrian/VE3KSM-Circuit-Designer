// Lightweight netlist editor: a transparent textarea layered over a
// syntax-highlighted overlay so we get colourised tokens, line-number gutter,
// and parser-error markers without pulling CodeMirror into the bundle.
//
// Layout:
//   <div class="ne-screen">
//     <div class="ne-toolbar">…buttons…</div>
//     <div class="ne-body">
//       <div class="ne-gutter">…line numbers + error pip…</div>
//       <pre class="ne-overlay">…coloured spans…</pre>
//       <textarea class="ne-input" />          ← transparent caret + selection
//     </div>
//   </div>
//
// The overlay and textarea share font-family/size/line-height/padding so that
// caret position, click-through, and scroll align byte-for-byte. Wrapping is
// disabled (white-space: pre, no horizontal wrap) so a single character offset
// always maps to the same on-screen column on both layers.

import { useEffect, useMemo, useRef } from 'react';
import { tokenizeLine } from './highlight.js';

/**
 * @param {object} props
 * @param {string} props.text        the source the user is editing
 * @param {(t:string)=>void} props.onChange
 * @param {{line:number,message:string}|null} props.errorMark line is 1-based; pass null to clear
 * @param {string} props.statusLabel right-side toolbar label ("⇄ in sync · ngspice 42")
 * @param {object[]} props.toolbar   [{ label, onClick, primary?, disabled? }, …]
 */
export default function NetlistEditor({ text, onChange, errorMark, statusLabel, toolbar }) {
  const taRef = useRef(null);
  const overlayRef = useRef(null);
  const gutterRef = useRef(null);

  // Recompute tokens whenever the text changes. tokenizeLine is cheap (linear
  // in line length) and runs on every keystroke; the source files we deal with
  // are O(100) lines so this stays well under a frame on commodity hardware.
  const lines = useMemo(() => text.split('\n'), [text]);
  const tokenized = useMemo(() => lines.map(tokenizeLine), [lines]);

  // Keep the overlay and gutter scroll-aligned with the textarea. We attach
  // the listener once; the ref doesn't change.
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return undefined;
    const sync = () => {
      if (overlayRef.current) {
        overlayRef.current.scrollTop = ta.scrollTop;
        overlayRef.current.scrollLeft = ta.scrollLeft;
      }
      if (gutterRef.current) {
        gutterRef.current.scrollTop = ta.scrollTop;
      }
    };
    ta.addEventListener('scroll', sync, { passive: true });
    sync();
    return () => ta.removeEventListener('scroll', sync);
  }, []);

  const errLine = errorMark?.line ?? 0;

  return (
    <div className="ne-screen">
      <div className="ne-toolbar">
        {toolbar.map((b, i) => (
          <button
            key={`${b.label}-${i}`}
            type="button"
            className={`ne-tb-btn ${b.primary ? 'is-primary' : ''}`}
            onClick={b.onClick}
            disabled={b.disabled}
            title={b.title || b.label}
          >
            {b.label}
          </button>
        ))}
        <span className="ne-tb-spacer" />
        <span className="ne-tb-status">{statusLabel}</span>
      </div>

      <div className="ne-body">
        <div className="ne-gutter" ref={gutterRef}>
          {lines.map((_, i) => (
            <span
              key={i}
              className={`ne-gutter-line ${i + 1 === errLine ? 'is-error' : ''}`}
              title={i + 1 === errLine ? errorMark.message : ''}
            >
              {i + 1 === errLine ? '✗' : ''}{i + 1}
            </span>
          ))}
        </div>

        <pre className="ne-overlay" ref={overlayRef} aria-hidden="true">
          {tokenized.map((tokens, i) => (
            <div key={i} className={`ne-line ${i + 1 === errLine ? 'is-error' : ''}`}>
              {tokens.length === 0
                ? <span>{'​' /* zero-width so empty lines still measure */}</span>
                : tokens.map((t, j) => (
                    <span key={j} className={`t-${t.type}`}>{t.text}</span>
                  ))}
            </div>
          ))}
        </pre>

        <textarea
          ref={taRef}
          className="ne-input"
          value={text}
          onChange={(e) => onChange(e.target.value)}
          spellCheck={false}
          autoCorrect="off"
          autoCapitalize="off"
          wrap="off"
        />
      </div>
    </div>
  );
}
