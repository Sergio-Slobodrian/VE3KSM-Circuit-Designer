// Tiny SPICE syntax-highlighter for the netlist tab. Operates line-by-line;
// returns an array of { type, text } tokens per line. The token taxonomy
// matches the colour palette in mockups/04_netlist.html:
//
//   cm — comment / inline comment
//   dv — directive (.LIB, .PARAM, .TRAN, .SAVE, .END, etc.)
//   rf — refdesignator (R1, V1, X1, …) at start of a component line
//   nm — number / value (250, 100k, 1MEG, 0.25, 1.5k)
//   kw — keyword (DC, AC, SIN, PULSE, uic, plus model names like 12AX7)
//   nd — node (any unquoted word that isn't another category)
//   st — string (a path/filename argument to .LIB)
//   pm — parameter reference {NAME} or .PARAM target
//   op — operator/punctuation (=, {, }, (, ))
//   ws — whitespace (preserved verbatim)
//
// The output is consumed by NetlistEditor.jsx's highlight overlay. Tokens
// always reproduce the input verbatim — concatenating .text fields equals
// the input line — so positioning under the textarea stays exact.

const SOURCE_MODE_KEYWORDS = new Set([
  'DC', 'AC', 'SIN', 'PULSE', 'PWL', 'SFFM', 'EXP', 'AM', 'FM', 'NOISE',
]);
const TRAILING_KEYWORDS = new Set(['UIC', 'STARTUP']);
const ANALYSIS_DIRECTIVES = new Set([
  '.TRAN', '.AC', '.DC', '.OP', '.NOISE', '.SAVE', '.LIB', '.PARAM', '.END',
  '.MODEL', '.SUBCKT', '.ENDS', '.INCLUDE', '.MEAS', '.STEP', '.IC',
]);

/** @typedef {{type:string, text:string}} Token */

/**
 * Tokenize one line of SPICE source. Returns Token[] whose .text values
 * concatenate back to `line` exactly.
 * @param {string} line
 * @returns {Token[]}
 */
export function tokenizeLine(line) {
  if (line.length === 0) return [];

  // Wholly-commented lines: leading * (with optional whitespace before it).
  // The mockup also treats commented-out directives as comments — keep the
  // whole thing one teal-grey blob so the user can spot disabled sections.
  const leading = line.match(/^[\t ]*/)[0];
  const body = line.slice(leading.length);
  const tokens = [];
  if (leading) tokens.push({ type: 'ws', text: leading });

  if (body.startsWith('*')) {
    tokens.push({ type: 'cm', text: body });
    return tokens;
  }

  // Inline trailing comment: ;... — colour the whole tail as comment.
  let active = body;
  let trailingComment = '';
  const semi = active.indexOf(';');
  if (semi >= 0) {
    trailingComment = active.slice(semi);
    active = active.slice(0, semi);
  }

  // Directive lines start with '.': .LIB / .PARAM / .TRAN / .AC / .SAVE /
  // .END / etc. The directive keyword itself is "dv"; the rest of the line
  // is tokenised generically.
  if (active.startsWith('.')) {
    const m = active.match(/^(\.[A-Za-z]+)(\s*)(.*)$/);
    if (m) {
      tokens.push({ type: 'dv', text: m[1] });
      if (m[2]) tokens.push({ type: 'ws', text: m[2] });
      tokenizeBody(m[3], tokens, /*isDirective*/ true, m[1].toUpperCase());
    } else {
      tokens.push({ type: 'dv', text: active });
    }
  } else if (active.length > 0) {
    // Component line: first word is the ref, then nodes/values/keywords.
    const m = active.match(/^([A-Za-z][A-Za-z0-9_]*)(\s*)(.*)$/);
    if (m) {
      tokens.push({ type: 'rf', text: m[1] });
      if (m[2]) tokens.push({ type: 'ws', text: m[2] });
      tokenizeBody(m[3], tokens, false, '');
    } else {
      tokens.push({ type: 'nd', text: active });
    }
  }

  if (trailingComment) tokens.push({ type: 'cm', text: trailingComment });
  return tokens;
}

// Split the post-head portion of a line into tokens. We keep this dumb on
// purpose — it doesn't try to understand the grammar, just colourises the
// most-recognisable shapes (numbers, braced parameter refs, keyword words).
function tokenizeBody(body, out, isDirective, headUpper) {
  let i = 0;
  while (i < body.length) {
    const ch = body[i];

    // Whitespace
    if (ch === ' ' || ch === '\t') {
      let j = i + 1;
      while (j < body.length && (body[j] === ' ' || body[j] === '\t')) j++;
      out.push({ type: 'ws', text: body.slice(i, j) });
      i = j;
      continue;
    }

    // Operators / punctuation
    if (ch === '=' || ch === '(' || ch === ')' || ch === ',' || ch === '{' || ch === '}') {
      out.push({ type: 'op', text: ch });
      i++;
      continue;
    }

    // Braced parameter reference {NAME} or {expr} — colour the inside as a
    // parameter regardless of contents; SPICE evaluates it.
    if (ch === '{') {
      const end = body.indexOf('}', i + 1);
      if (end < 0) { out.push({ type: 'pm', text: body.slice(i) }); i = body.length; continue; }
      out.push({ type: 'op', text: '{' });
      out.push({ type: 'pm', text: body.slice(i + 1, end) });
      out.push({ type: 'op', text: '}' });
      i = end + 1;
      continue;
    }

    // Generic word: identifiers, numbers, model names, file paths.
    let j = i;
    while (j < body.length && !/[\s=(){},]/.test(body[j])) j++;
    const word = body.slice(i, j);

    out.push({ type: classifyWord(word, isDirective, headUpper, out), text: word });
    i = j;
  }
}

// classifyWord assigns one of {nm, kw, st, pm, nd} to a bare word based on a
// few heuristics:
//   - all-numeric (with SI suffix) → number
//   - matches a known keyword → kw
//   - inside a .LIB / .INCLUDE → string (file path)
//   - looks like a parameter-name (.PARAM head, before '=') → pm
//   - else → node
function classifyWord(word, isDirective, headUpper, prevTokens) {
  if (NUMBER_RE.test(word)) return 'nm';
  const up = word.toUpperCase();
  if (SOURCE_MODE_KEYWORDS.has(up) || TRAILING_KEYWORDS.has(up)) return 'kw';

  if (isDirective) {
    if (headUpper === '.LIB' || headUpper === '.INCLUDE') {
      // First non-ws token on .LIB / .INCLUDE is the path; subsequent tokens
      // (sections) are also strings in our model.
      return 'st';
    }
    if (headUpper === '.PARAM') {
      // First word is the parameter being defined; subsequent values fall
      // through to number/node classification.
      const seen = prevTokens.some((t) => t.type === 'pm');
      if (!seen) return 'pm';
    }
    if (headUpper === '.SAVE') {
      // V(node) / I(branch) — V/I letters are kw, the inside is nd.
      if (up === 'V' || up === 'I') return 'kw';
    }
  }

  // Subcircuit model names tend to start with a digit-prefixed letter or
  // upper-case (12AX7, ECC83). Heuristic: contains both digits and letters.
  if (/[A-Za-z]/.test(word) && /[0-9]/.test(word) && /^[A-Za-z0-9_]+$/.test(word) && /^[0-9]/.test(word)) {
    return 'kw';
  }
  return 'nd';
}

// SPICE numeric literal: optional sign, digits with optional decimal, optional
// exponent, optional SI suffix (T, G, MEG, K, M, U, N, P, F).
const NUMBER_RE = /^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?(T|G|MEG|K|M|U|N|P|F|MIL)?(HZ|S|V|A|F|H|OHM)?$/i;
