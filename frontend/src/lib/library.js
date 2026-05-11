// Client-side helpers for the imported component library.
//
// The server returns one library entry per .subckt — a Würth pack with 30
// part variants becomes 30 rows. That worked for tube models (~4 entries per
// pack) but a passive-component pack would explode the palette. Phase 1.5 of
// symbol_enhancement.md collapses sibling rows (same kind, same .lib file)
// into a single "family" row whose variants live on the manifest itself; the
// inspector renders a dropdown so the user picks the part number after drop.
//
// Collapse is purely a UI concern. The server's library snapshot, the YAML
// manifests on disk, and the SPICE round-trip (.lib + .subckt model name on
// each placed component) are unchanged.

/**
 * Group library entries by (kind, library). Imported families with two or
 * more variants collapse to a single row; primitives, single-variant imports,
 * and entries without a library reference pass through unchanged. The
 * collapsed row carries:
 *   - all the fields of its "primary" variant (the first member, which is
 *     where the .asy-derived symbol_def landed during the asy merge)
 *   - a `variants` array of `{ model_name, default_value, label }` records
 *     covering every member of the family
 *
 * The `label` is a short hint extracted from the model_name (the trailing
 * value-bearing token, e.g. `861140783006_2.2mF` → `2.2mF`). Falls back to
 * the full model_name if no underscore-delimited tail is found.
 *
 * @param {Array} library raw library snapshot
 * @returns {Array} rows shaped { ...primary, variants?: Array<{...}> }
 */
export function collapseLibrary(library) {
  if (!Array.isArray(library) || library.length === 0) return [];

  const groups = new Map();           // key → { primary, members[] }
  const passthrough = [];

  for (const c of library) {
    if (!c || !c.library || !c.kind) {
      passthrough.push(c);
      continue;
    }
    const key = `${c.kind}::${c.library}`;
    let g = groups.get(key);
    if (!g) {
      g = { primary: c, members: [] };
      groups.set(key, g);
    } else {
      // Primary preference: any entry with a structured symbol_def beats one
      // without. Within ties, keep the first one we saw — server already
      // returned them in deterministic order.
      if (!g.primary.symbol_def && c.symbol_def) {
        g.primary = c;
      }
    }
    g.members.push(c);
  }

  const collapsed = [];
  for (const g of groups.values()) {
    if (g.members.length <= 1) {
      // Single-member family — pass through, no need for the variant UI.
      collapsed.push(g.members[0]);
      continue;
    }
    const variants = g.members.map((m) => ({
      model_name: m.model_name,
      default_value: m.default_value || '',
      label: variantLabel(m.model_name),
    }));
    collapsed.push({
      ...g.primary,
      variants,
    });
  }

  // Order the result the same way the server already orders its components:
  // group, then kind, then model_name. Family rows use the primary's
  // model_name as the sort key, which is fine since members are siblings.
  const all = [...passthrough, ...collapsed];
  all.sort((a, b) => {
    const ag = a.group || '';
    const bg = b.group || '';
    if (ag !== bg) return ag.localeCompare(bg);
    if (a.kind !== b.kind) return a.kind.localeCompare(b.kind);
    return (a.model_name || a.kind).localeCompare(b.model_name || b.kind);
  });
  return all;
}

/**
 * Locate the collapsed family that contains a given component instance. Used
 * by the Inspector to render a variant dropdown when the placed component
 * came from a multi-variant .lib import.
 *
 * @param {Array} library raw (non-collapsed) library snapshot
 * @param {{kind: string, model?: string}} comp
 * @returns {object|null} the collapsed family row, or null
 */
export function findFamily(library, comp) {
  if (!comp || !library) return null;
  const collapsed = collapseLibrary(library);
  for (const row of collapsed) {
    if (row.kind !== comp.kind) continue;
    if (!row.variants) {
      // Single-variant or primitive — only matches if the model lines up
      // exactly (or both are empty).
      if ((row.model_name || '') === (comp.model || '')) return row;
      continue;
    }
    if (row.variants.some((v) => v.model_name === comp.model)) return row;
  }
  return null;
}

/**
 * Friendlier label for a SPICE model name. Würth's pack uses pattern
 * `<order>_<series>_<value>` so the trailing `_<value>` token is the
 * user-recognisable part. Falls back to the full name when the heuristic
 * can't make a reasonable choice (no underscore, or the tail is too short).
 */
export function variantLabel(modelName) {
  if (!modelName) return '';
  const parts = String(modelName).split('_');
  if (parts.length < 2) return modelName;
  const tail = parts[parts.length - 1];
  if (tail.length < 2 || tail.length > 16) return modelName;
  return tail;
}

/**
 * Family label rendered as the palette row title for a collapsed family. We
 * prefer the .lib basename (e.g. `WCAP-AI3H` from `WCAP-AI3H.lib`) since the
 * primary variant's model_name is usually the long opaque part number.
 */
export function familyLabel(row) {
  if (!row?.library) return row?.model_name || row?.kind || '';
  return row.library.replace(/\.lib$/i, '');
}
