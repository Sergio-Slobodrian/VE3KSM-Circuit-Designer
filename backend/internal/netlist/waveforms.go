package netlist

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"circuit-designer/backend/internal/circuit"
)

// Waveform mode identifiers used in SourceSpec.Mode and the *+ waveform=
// metadata comment. These are the high-level user-facing names; the
// SPICE-native subset (dc/sin/pulse/sffm/pwl/ac) is a strict subset.
const (
	ModeDC       = "dc"
	ModeAC       = "ac"
	ModeSin      = "sin"
	ModePulse    = "pulse"
	ModeSquare   = "square"
	ModeTriangle = "triangle"
	ModeSawtooth = "sawtooth"
	ModePWL      = "pwl"
	ModeSFFM     = "sffm"
	ModeFM       = "fm"
	ModeAM       = "am"
	ModeNoise    = "noise"
	ModeChirp    = "chirp"
	ModeTwoTone  = "twotone"
)

// allWaveformModes lists every high-level mode the m10 signal generator
// understands. Used by the parser to validate `waveform=` overrides and by
// callers that need to know the closed set.
var allWaveformModes = map[string]struct{}{
	ModeDC: {}, ModeAC: {}, ModeSin: {}, ModePulse: {},
	ModeSquare: {}, ModeTriangle: {}, ModeSawtooth: {},
	ModePWL: {}, ModeSFFM: {}, ModeFM: {}, ModeAM: {},
	ModeNoise: {}, ModeChirp: {}, ModeTwoTone: {},
}

// nativeWaveformModes are the modes whose SPICE form is fully self-describing,
// so the round-trip does not need a *+ waveform= companion line. The parser
// reconstructs them from the V/I line alone.
var nativeWaveformModes = map[string]struct{}{
	ModeDC: {}, ModeAC: {}, ModeSin: {}, ModePulse: {}, ModeSFFM: {},
}

// IsHighLevelWaveform reports whether mode requires a *+ waveform= companion
// to round-trip — i.e. it lowers to a PWL/PULSE/SFFM whose parameters differ
// from the high-level form. Native modes return false.
func IsHighLevelWaveform(mode string) bool {
	if mode == "" {
		return false
	}
	if _, ok := allWaveformModes[strings.ToLower(mode)]; !ok {
		return false
	}
	_, isNative := nativeWaveformModes[strings.ToLower(mode)]
	return !isNative
}

// MaxPWLSamples caps the number of (t,v) pairs the synthesizer will inline
// into a PWL transient spec. Trades simulator performance + netlist legibility
// against high-frequency fidelity. Tuned for the 11 demo waveforms — chirp/AM
// at default rates produce ~1k–4k samples; noise rounds the upper end.
const MaxPWLSamples = 8192

// minPWLSampleStep keeps the synthesizer from emitting near-zero dt steps that
// upset some SPICE implementations' linear-interpolation routines.
const minPWLSampleStep = 1e-9

// lowerSource returns the SPICE transient-spec text for a SourceSpec along
// with whether the emitter should also write a *+ waveform= companion comment
// so a parser can recover the high-level form on round-trip.
//
// The DC/AC tokens are emitted by the caller (emitSourceSpec) — this function
// only handles the transient portion.
func lowerSource(s *circuit.SourceSpec) (spec string, needsMeta bool, err error) {
	if s == nil {
		return "", false, nil
	}
	mode := strings.ToLower(s.Mode)
	switch mode {
	case "", ModeDC, ModeAC:
		return "", false, nil
	case ModeSin:
		return emitSinSpec(s.Params), false, nil
	case ModePulse:
		return emitPulseSpec(s.Params), false, nil
	case ModeSFFM:
		return emitSFFMSpec(s.Params), false, nil
	case ModeSquare:
		return emitPulseSpec(squareToPulse(s.Params)), true, nil
	case ModeTriangle:
		// Triangle uses direct PWL synthesis instead of PULSE because
		// ngspice's PULSE(... pw=0 ...) under `set ngbehavior=lt` (the m6
		// engine convention) renders as a flat-topped trapezoid, not a
		// peaked triangle. Five PWL points with r=0 give an unambiguous
		// shape regardless of the SPICE dialect's pw=0 semantics.
		points, perr := synthTriangle(s.Params)
		if perr != nil {
			return "", false, perr
		}
		return emitPWLSpec(points, periodicExtras(s.Params, periodFromFreq(s.Params))), true, nil
	case ModeSawtooth:
		// Same reasoning as triangle — PULSE with pw=0 + edge≈0.5% doesn't
		// reliably render as a sawtooth on every dialect. Direct PWL is
		// shape-exact.
		points, perr := synthSawtooth(s.Params)
		if perr != nil {
			return "", false, perr
		}
		return emitPWLSpec(points, periodicExtras(s.Params, periodFromFreq(s.Params))), true, nil
	case ModeFM:
		return emitSFFMSpec(fmToSFFM(s.Params)), true, nil
	case ModePWL:
		// PWL with stored points is SPICE-native (the V1 PWL(...) line carries
		// the data). The *+ companion is still useful when the user picked the
		// points by importing a CSV/WAV — `src=foo.wav rate=48000 gain=1.0
		// loop=true td=0.001` lets the inspector restore the file-import
		// affordance and the time-shift on round-trip.
		points, perr := pwlPointsFromParams(s.Params)
		if perr != nil {
			return "", false, perr
		}
		_, hasMeta := pwlMetaKeys(s.Params)
		return emitPWLSpec(points, oneShotExtras(s.Params)), hasMeta, nil
	case ModeChirp:
		points, perr := synthChirp(s.Params)
		if perr != nil {
			return "", false, perr
		}
		// One-shot — no repeat. td is the bare user delay.
		return emitPWLSpec(points, oneShotExtras(s.Params)), true, nil
	case ModeAM:
		points, perr := synthAM(s.Params)
		if perr != nil {
			return "", false, perr
		}
		return emitPWLSpec(points, repeatExtras(s.Params)), true, nil
	case ModeTwoTone:
		points, perr := synthTwoTone(s.Params)
		if perr != nil {
			return "", false, perr
		}
		return emitPWLSpec(points, repeatExtras(s.Params)), true, nil
	case ModeNoise:
		points, perr := synthNoise(s.Params)
		if perr != nil {
			return "", false, perr
		}
		return emitPWLSpec(points, repeatExtras(s.Params)), true, nil
	}
	return "", false, fmt.Errorf("waveform mode %q has no lowering", s.Mode)
}

// repeatPWL returns a Params-shaped marker that emitPWLSpec interprets as
// "append `r=0` to the spec" so periodic waveforms repeat across the full
// transient sweep without the user picking an exact duration. ngspice accepts
// `r=<offset>` after the (t v) pair list per its PWL syntax.
func repeatPWL() map[string]string {
	return map[string]string{"_repeat": "0"}
}

// periodicExtras builds the PWL extras for a periodic single-frequency mode
// (triangle/sawtooth): r=0 to repeat, plus td=<value> when the user has set
// either `td` (seconds) or `phase` (degrees). Phase folds into td via
// effectiveDelay. Returns nil when neither knob is set so the emitted PWL
// stays compact.
func periodicExtras(p map[string]string, period float64) map[string]string {
	td := effectiveDelay(p, period)
	extras := map[string]string{"_repeat": "0"}
	if td > 0 {
		extras["_td"] = formatPWLNumber(td)
	}
	return extras
}

// repeatExtras builds extras for periodic multi-frequency modes (am/twotone/
// noise): r=0 + bare td (no phase, since "phase of what?" isn't well-defined
// when multiple frequencies coexist).
func repeatExtras(p map[string]string) map[string]string {
	extras := map[string]string{"_repeat": "0"}
	td := parseEngFloat(p["td"], 0)
	if td > 0 {
		extras["_td"] = formatPWLNumber(td)
	}
	return extras
}

// oneShotExtras builds extras for aperiodic modes (chirp, user pwl): just
// td when set. No repeat — these are one-shot or already loop-self-aware.
func oneShotExtras(p map[string]string) map[string]string {
	td := parseEngFloat(p["td"], 0)
	if td <= 0 {
		return nil
	}
	return map[string]string{"_td": formatPWLNumber(td)}
}

// periodFromFreq returns 1/freq for a periodic mode, with sane fallback when
// freq is missing or zero. Used by lowerSource to compute effectiveDelay
// without each case duplicating the parsing.
func periodFromFreq(p map[string]string) float64 {
	freq := parseEngFloat(p["freq"], 1e3)
	if freq <= 0 {
		return 0
	}
	return 1.0 / freq
}

// emitSinSpec assembles a SIN(...) transient spec from the canonical SIN
// parameter keys.
//
// Positional emission rule: emit through the highest-indexed Params key that
// has a non-empty value, filling any missing intermediates with "0". Examples:
//   {offset, ampl, freq}                → SIN(off ampl freq)            (3 args)
//   {offset, ampl, freq, td}            → SIN(off ampl freq td)         (4 args)
//   {offset, ampl, freq, phase}         → SIN(off ampl freq 0 0 phase)  (6 args)
// This preserves the m1 fixture's compact 3-arg form when only the basics are
// set, while allowing td/phase to be set independently in m10's signal
// generator without requiring the user to enter every intermediate value.
//
// The ampl key is stored in Params as Vpp (peak-to-peak); we halve numeric
// values on emit to match ngspice's peak-amplitude convention. Parameter
// expressions like `{AMP}` pass through unchanged. Symmetrically,
// parseTransientArgs doubles ampl on parse (see parser.go).
func emitSinSpec(p map[string]string) string {
	names := []string{"offset", "ampl", "freq", "td", "damp", "phase"}
	maxIdx := -1
	for i, n := range names {
		if v, ok := p[n]; ok && v != "" {
			maxIdx = i
		}
	}
	if maxIdx < 0 {
		return "SIN()"
	}
	args := make([]string, 0, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		n := names[i]
		v, ok := p[n]
		if !ok || v == "" {
			v = "0" // fill missing intermediate so positionals line up
		}
		if n == "ampl" {
			v = vppToPeakSpiceArg(v)
		}
		args = append(args, v)
	}
	return "SIN(" + strings.Join(args, " ") + ")"
}

// emitPulseSpec assembles a PULSE(v1 v2 td tr tf pw per) transient spec. The
// canonical positional keys are taken from §7.1; missing tail values are
// dropped so the round-trip Params match.
func emitPulseSpec(p map[string]string) string {
	names := []string{"v1", "v2", "td", "tr", "tf", "pw", "per"}
	var args []string
	for _, n := range names {
		v, ok := p[n]
		if !ok {
			break
		}
		args = append(args, v)
	}
	return "PULSE(" + strings.Join(args, " ") + ")"
}

// emitSFFMSpec assembles an SFFM(off ampl fc mdi fm) transient spec. ampl is
// halved before emit for the same reason as SIN — Params storage convention
// is Vpp (DESIGN.md §7 + the m10 inspector) but ngspice wants peak.
func emitSFFMSpec(p map[string]string) string {
	names := []string{"offset", "ampl", "fc", "mdi", "fm"}
	var args []string
	for _, n := range names {
		v, ok := p[n]
		if !ok {
			break
		}
		if n == "ampl" {
			v = vppToPeakSpiceArg(v)
		}
		args = append(args, v)
	}
	return "SFFM(" + strings.Join(args, " ") + ")"
}

// vppToPeakSpiceArg halves a numeric SPICE value (e.g. "1", "0.25", "250m",
// "1k") into its peak-amplitude form for emission. Parameter expressions
// (anything that doesn't parse as a number) pass through unchanged.
//
// Output formatting: when the input parses cleanly we emit a plain decimal
// to keep the netlist readable; engineering suffixes are not preserved
// because we can't generally halve them (250m → 125m is fine but 1k → 500
// straddles the suffix boundary). The fixture round-trip stays equivalent
// because ngspice consumes both forms identically.
func vppToPeakSpiceArg(s string) string {
	if v, ok := parseNumericSpice(s); ok {
		return strconv.FormatFloat(v/2, 'g', -1, 64)
	}
	return s
}

// peakToVppSpiceArg is the parser-side inverse of vppToPeakSpiceArg. Doubles
// a numeric SPICE value to convert from ngspice's peak amplitude convention
// into the inspector's Vpp storage convention. Parameter expressions pass
// through unchanged (the user is responsible for setting their .PARAM).
func peakToVppSpiceArg(s string) string {
	if v, ok := parseNumericSpice(s); ok {
		return strconv.FormatFloat(v*2, 'g', -1, 64)
	}
	return s
}

// parseNumericSpice parses a SPICE numeric like parseEngFloat but returns an
// ok=false when the input is a parameter expression / non-numeric, so the
// caller can pass-through verbatim instead of substituting a fallback.
func parseNumericSpice(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Reject anything that obviously isn't a numeric: braces, parens, ops.
	for _, ch := range s {
		if ch == '{' || ch == '}' || ch == '(' || ch == ')' || ch == '+' && len(s) > 1 || ch == '*' || ch == '/' {
			return 0, false
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v, true
	}
	upper := strings.ToUpper(s)
	suffixes := []struct {
		s string
		m float64
	}{
		{"MEG", 1e6}, {"MIL", 25.4e-6},
		{"T", 1e12}, {"G", 1e9}, {"K", 1e3},
		{"M", 1e-3}, {"U", 1e-6}, {"N", 1e-9},
		{"P", 1e-12}, {"F", 1e-15},
	}
	for _, sf := range suffixes {
		if strings.HasSuffix(upper, sf.s) {
			head := upper[:len(upper)-len(sf.s)]
			if v, err := strconv.ParseFloat(head, 64); err == nil {
				return v * sf.m, true
			}
		}
	}
	return 0, false
}

// emitPWLSpec renders a (t,v) point list as a SPICE PWL(...) transient spec.
// Points come either from user import (mode=pwl) or from the synthesizers
// below. Empty point lists emit `PWL(0 0)` so the SPICE parser doesn't choke;
// the user's circuit sees a flat zero rail.
//
// extra carries side-channel emit options understood by the lowering layer:
//   - `_repeat`: appends `r=<value>` to wrap the PWL forever
//   - `_td`: appends `td=<value>` to shift the whole PWL by that delay
// The order is r= then td= because that's the order ngspice's manual lists
// — both qualifiers are accepted in either order in practice but staying
// canonical keeps the round-trip diff-clean.
func emitPWLSpec(points [][2]float64, extra map[string]string) string {
	if len(points) == 0 {
		return "PWL(0 0)"
	}
	parts := make([]string, 0, len(points)*2)
	for _, pt := range points {
		parts = append(parts, formatPWLNumber(pt[0]), formatPWLNumber(pt[1]))
	}
	out := "PWL(" + strings.Join(parts, " ") + ")"
	if r, ok := extra["_repeat"]; ok {
		out += " r=" + r
	}
	if t, ok := extra["_td"]; ok {
		out += " td=" + t
	}
	return out
}

// formatPWLNumber renders a Go float as the most compact SPICE-legible string.
// Avoids %g's scientific-notation switch-over for the common dt~µs range so
// the netlist stays readable; falls back to %g for very small or very large
// magnitudes where fixed-point becomes unwieldy.
func formatPWLNumber(v float64) string {
	if v == 0 {
		return "0"
	}
	abs := math.Abs(v)
	if abs >= 1e-6 && abs < 1e6 {
		s := strconv.FormatFloat(v, 'f', -1, 64)
		return s
	}
	return strconv.FormatFloat(v, 'g', 8, 64)
}

// pwlPointsFromParams reads the inline point list out of Source.Params. The
// canonical key is "points" with format "t1:v1;t2:v2;...". Whitespace
// tolerated; empty list is allowed (emit will fall back to PWL(0 0)).
func pwlPointsFromParams(p map[string]string) ([][2]float64, error) {
	raw, ok := p["points"]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	return parsePointsString(raw)
}

func parsePointsString(raw string) ([][2]float64, error) {
	pairs := strings.Split(raw, ";")
	out := make([][2]float64, 0, len(pairs))
	for _, p := range pairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		sep := strings.Index(p, ":")
		if sep < 0 {
			return nil, fmt.Errorf("pwl point %q missing : separator", p)
		}
		t, err := strconv.ParseFloat(strings.TrimSpace(p[:sep]), 64)
		if err != nil {
			return nil, fmt.Errorf("pwl point %q: bad time %v", p, err)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(p[sep+1:]), 64)
		if err != nil {
			return nil, fmt.Errorf("pwl point %q: bad value %v", p, err)
		}
		out = append(out, [2]float64{t, v})
	}
	return out, nil
}

// formatPointsString is the inverse of parsePointsString. Used when the
// parser has reconstituted points from a PWL(...) line and needs to stash
// them in Params["points"] for the inspector / round-trip.
func formatPointsString(points [][2]float64) string {
	if len(points) == 0 {
		return ""
	}
	parts := make([]string, len(points))
	for i, pt := range points {
		parts[i] = formatPWLNumber(pt[0]) + ":" + formatPWLNumber(pt[1])
	}
	return strings.Join(parts, ";")
}

// pwlMetaKeys returns true if Params carries any of the PWL-import metadata
// keys (src/rate/gain/loop) so the emitter knows whether a *+ waveform=
// companion is worth writing for a pwl source. Pure-inline pwl sources (no
// import provenance) round-trip via the V1 PWL line alone.
func pwlMetaKeys(p map[string]string) (keys []string, present bool) {
	for _, k := range []string{"src", "rate", "gain", "loop"} {
		if _, ok := p[k]; ok {
			present = true
			keys = append(keys, k)
		}
	}
	return keys, present
}

// --- derived → PULSE adapters ----------------------------------------------

// squareToPulse maps a high-level square waveform onto SPICE PULSE.
// Inputs (Params keys): freq, ampl, offset, duty (%), tr, tf, td (s),
// phase (°). duty is 0..100; default 50. The effective PULSE delay is
// `td + (phase/360)*period` so the user can shift in either unit.
func squareToPulse(p map[string]string) map[string]string {
	freq := parseEngFloat(p["freq"], 1e3)
	period := 1.0 / max1(freq, 1e-12)
	ampl := parseEngFloat(p["ampl"], 1)
	offset := parseEngFloat(p["offset"], 0)
	duty := parseEngFloat(p["duty"], 50)
	if duty < 0 {
		duty = 0
	}
	if duty > 100 {
		duty = 100
	}
	pw := period * duty / 100
	tr := parseEngFloat(p["tr"], 1e-8)
	tf := parseEngFloat(p["tf"], 1e-8)
	td := effectiveDelay(p, period)

	vlo := offset - ampl/2
	vhi := offset + ampl/2
	return map[string]string{
		"v1":  formatPWLNumber(vlo),
		"v2":  formatPWLNumber(vhi),
		"td":  formatPWLNumber(td),
		"tr":  formatPWLNumber(tr),
		"tf":  formatPWLNumber(tf),
		"pw":  formatPWLNumber(pw),
		"per": formatPWLNumber(period),
	}
}

// effectiveDelay computes the time-shift to apply to a periodic waveform from
// the user's `td` (seconds) and `phase` (degrees) keys. For a 1 kHz sine,
// phase=90° = 0.25 ms — they compose: `td + (phase/360)*period`. Used by
// every periodic mode that doesn't have a native phase positional (square,
// triangle, sawtooth) and by lowerSource for the PWL synthesizers.
func effectiveDelay(p map[string]string, period float64) float64 {
	td := parseEngFloat(p["td"], 0)
	if period > 0 {
		td += (parseEngFloat(p["phase"], 0) / 360) * period
	}
	return td
}

// synthTriangle samples a zero-starting triangle as 5 PWL points: offset →
// +peak → offset → −peak → offset, then `r=0` repeats it forever. The first
// point sits at the user's DC offset (not at vlo), so a phase=0 triangle is
// in phase with a phase=0 sine — both start at 0 with positive derivative
// and reach their peak at one quarter of the period.
//
// `sym` (1..99, default 50) is the rising-half fraction of the period:
// sym=50 produces an isoceles triangle (peak at T/4, trough at 3T/4); sym=30
// produces a steeper rise (peak at T*0.15, trough at T*0.65).
//
// Why PWL over PULSE: ngspice's PULSE(v1 v2 td tr tf pw per) with pw=0 — the
// shape we'd need for a peaked triangle — renders as a flat-topped trapezoid
// under `set ngbehavior=lt` (the m6 engine setting). Direct PWL synthesis
// gives an unambiguous shape regardless of dialect.
func synthTriangle(p map[string]string) ([][2]float64, error) {
	freq := parseEngFloat(p["freq"], 1e3)
	if freq <= 0 {
		return nil, fmt.Errorf("triangle requires freq > 0")
	}
	period := 1.0 / freq
	ampl := parseEngFloat(p["ampl"], 1)
	offset := parseEngFloat(p["offset"], 0)
	sym := parseEngFloat(p["sym"], 50)
	if sym < 1 {
		sym = 1
	}
	if sym > 99 {
		sym = 99
	}
	risePhase := period * sym / 100
	fallPhase := period - risePhase
	peakT := risePhase / 2
	zeroT := risePhase
	troughT := risePhase + fallPhase/2
	vhi := offset + ampl/2
	vlo := offset - ampl/2
	return [][2]float64{
		{0, offset},
		{peakT, vhi},
		{zeroT, offset},
		{troughT, vlo},
		{period, offset},
	}, nil
}

// synthSawtooth samples a sawtooth as 2 PWL points spanning one period;
// ngspice's r=0 wrap creates the discontinuity at the period boundary
// (rising) or start (falling). `dir` selects "rising" (default) or "falling".
func synthSawtooth(p map[string]string) ([][2]float64, error) {
	freq := parseEngFloat(p["freq"], 1e3)
	if freq <= 0 {
		return nil, fmt.Errorf("sawtooth requires freq > 0")
	}
	period := 1.0 / freq
	ampl := parseEngFloat(p["ampl"], 1)
	offset := parseEngFloat(p["offset"], 0)
	dir := strings.ToLower(strings.TrimSpace(p["dir"]))
	if dir == "" {
		dir = "rising"
	}
	vlo := offset - ampl/2
	vhi := offset + ampl/2
	if dir == "falling" {
		return [][2]float64{{0, vhi}, {period, vlo}}, nil
	}
	return [][2]float64{{0, vlo}, {period, vhi}}, nil
}

// fmToSFFM maps the high-level FM panel onto ngspice's SFFM. SFFM
// modulation index = deviation/mod_freq; we compute it so the user can
// specify deviation directly (more familiar from RF practice than "MI").
func fmToSFFM(p map[string]string) map[string]string {
	offset := parseEngFloat(p["offset"], 0)
	ampl := parseEngFloat(p["ampl"], 1)
	fc := parseEngFloat(p["fc"], 10.7e6)
	fm := parseEngFloat(p["fm"], 1e3)
	dev := parseEngFloat(p["dev"], 5e3)
	mdi := dev / max1(fm, 1e-12)
	return map[string]string{
		"offset": formatPWLNumber(offset),
		"ampl":   formatPWLNumber(ampl),
		"fc":     formatPWLNumber(fc),
		"mdi":    formatPWLNumber(mdi),
		"fm":     formatPWLNumber(fm),
	}
}

// --- synthesizers ----------------------------------------------------------

// synthChirp samples a linear/log/exponential frequency sweep over `dur`
// seconds. f0 → f1, peak-to-peak amplitude `ampl` (Vpp; halved internally
// because the synthesized sin term swings ±peak by definition). Always
// one-shot (no PWL repeat).
func synthChirp(p map[string]string) ([][2]float64, error) {
	f0 := parseEngFloat(p["f0"], 20)
	f1 := parseEngFloat(p["f1"], 20e3)
	dur := parseEngFloat(p["dur"], 1)
	ampl := parseEngFloat(p["ampl"], 1) / 2
	shape := strings.ToLower(strings.TrimSpace(p["shape"]))
	if shape == "" {
		shape = "log"
	}
	if dur <= 0 {
		return nil, fmt.Errorf("chirp dur must be > 0")
	}
	// Sample density: aim for ≥ 16 samples per cycle of the highest freq, but
	// cap at MaxPWLSamples to keep the netlist bounded.
	want := int(16 * dur * math.Max(f0, f1))
	if want < 256 {
		want = 256
	}
	if want > MaxPWLSamples {
		want = MaxPWLSamples
	}
	dt := dur / float64(want-1)
	out := make([][2]float64, want)
	for i := 0; i < want; i++ {
		t := float64(i) * dt
		// instantaneous phase = ∫ 2π f(t) dt
		var phase float64
		switch shape {
		case "log", "exponential":
			// f(t) = f0 * (f1/f0)^(t/dur) → integral closed-form
			if f0 <= 0 {
				f0 = 1e-3
			}
			k := math.Log(f1 / f0)
			if math.Abs(k) < 1e-12 {
				phase = 2 * math.Pi * f0 * t
			} else {
				phase = 2 * math.Pi * f0 * dur * (math.Exp(k*t/dur) - 1) / k
			}
		default: // linear
			phase = 2 * math.Pi * (f0*t + 0.5*(f1-f0)/dur*t*t)
		}
		out[i] = [2]float64{t, ampl * math.Sin(phase)}
	}
	return out, nil
}

// synthAM samples carrier × envelope = ampl * (1 + depth*cos(2π fm t)) *
// sin(2π fc t). depth is 0..100 (%). ampl is the unmodulated carrier's Vpp;
// peak excursion at the envelope crests is ampl/2 * (1+depth) per side, so
// fully-modulated waveforms can exceed the nominal Vpp by `depth%`.
func synthAM(p map[string]string) ([][2]float64, error) {
	fc := parseEngFloat(p["fc"], 1e6)
	fm := parseEngFloat(p["fm"], 1e3)
	depth := parseEngFloat(p["depth"], 80) / 100
	ampl := parseEngFloat(p["ampl"], 1) / 2
	if fc <= 0 || fm <= 0 {
		return nil, fmt.Errorf("am requires fc>0 and fm>0")
	}
	// Sample one period of the modulating envelope; carrier needs dense
	// sampling (≥ 16 samples per carrier cycle).
	dur := 1.0 / fm
	want := int(16 * fc * dur)
	if want < 256 {
		want = 256
	}
	if want > MaxPWLSamples {
		want = MaxPWLSamples
	}
	dt := dur / float64(want-1)
	out := make([][2]float64, want)
	for i := 0; i < want; i++ {
		t := float64(i) * dt
		env := 1 + depth*math.Cos(2*math.Pi*fm*t)
		out[i] = [2]float64{t, ampl * env * math.Sin(2*math.Pi*fc*t)}
	}
	return out, nil
}

// synthTwoTone samples a1*sin(2π f1 t) + a2*sin(2π f2 t + dphi).
// a1/a2 are each tone's Vpp (halved internally to match the sin term's ±peak
// definition); the summed waveform's swing depends on phase coincidence.
// Period chosen to capture at least one period of both tones (the synthesizer
// approximates a common period via a small-integer search to keep the loop
// continuous when r=0 wraps it).
func synthTwoTone(p map[string]string) ([][2]float64, error) {
	f1 := parseEngFloat(p["f1"], 700)
	f2 := parseEngFloat(p["f2"], 1900)
	a1 := parseEngFloat(p["a1"], 0.5) / 2
	a2 := parseEngFloat(p["a2"], 0.5) / 2
	dphi := parseEngFloat(p["dphi"], 0) * math.Pi / 180
	if f1 <= 0 || f2 <= 0 {
		return nil, fmt.Errorf("twotone requires f1>0 and f2>0")
	}
	dur := closeCommonPeriod(f1, f2, 0.05) // up to 50 ms, then we accept seam
	want := int(16 * dur * math.Max(f1, f2))
	if want < 256 {
		want = 256
	}
	if want > MaxPWLSamples {
		want = MaxPWLSamples
	}
	dt := dur / float64(want-1)
	out := make([][2]float64, want)
	for i := 0; i < want; i++ {
		t := float64(i) * dt
		v := a1*math.Sin(2*math.Pi*f1*t) + a2*math.Sin(2*math.Pi*f2*t+dphi)
		out[i] = [2]float64{t, v}
	}
	return out, nil
}

// closeCommonPeriod picks a duration that captures an integer number of
// cycles for both frequencies, or the longest of the two if no good common
// period is found within `cap` seconds. Used by twotone to make the PWL
// repeat seamlessly with r=0.
func closeCommonPeriod(f1, f2, cap float64) float64 {
	t1 := 1.0 / f1
	t2 := 1.0 / f2
	best := math.Max(t1, t2)
	for n1 := 1; n1 <= 256; n1++ {
		dur := float64(n1) * t1
		if dur > cap {
			break
		}
		ratio := dur / t2
		// integer match within 0.5%?
		if math.Abs(ratio-math.Round(ratio)) < 5e-3 {
			return dur
		}
		best = dur
	}
	return best
}

// synthNoise samples band-limited noise of the requested type (white|pink),
// scaled to RMS amplitude `rms` over `dur` seconds. Bandwidth `bw` sets the
// upper rolloff (white above bw is attenuated). Uses a deterministic PRNG
// seeded from `seed` so the output is reproducible.
func synthNoise(p map[string]string) ([][2]float64, error) {
	rms := parseEngFloat(p["rms"], 0.1)
	bw := parseEngFloat(p["bw"], 20e3)
	dur := parseEngFloat(p["dur"], 0.05)
	seedStr := strings.TrimSpace(p["seed"])
	seed := int64(42)
	if seedStr != "" {
		if v, err := strconv.ParseInt(seedStr, 10, 64); err == nil {
			seed = v
		}
	}
	kind := strings.ToLower(strings.TrimSpace(p["type"]))
	if kind == "" {
		kind = "white"
	}
	if dur <= 0 {
		return nil, fmt.Errorf("noise dur must be > 0")
	}
	if bw <= 0 {
		bw = 20e3
	}
	want := int(2.5 * bw * dur) // Nyquist-ish + guard
	if want < 256 {
		want = 256
	}
	if want > MaxPWLSamples {
		want = MaxPWLSamples
	}
	dt := dur / float64(want-1)
	out := make([][2]float64, want)
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test waveform, not crypto
	switch kind {
	case "pink":
		// Voss-McCartney approximation: 5 octaves, summed with random refresh.
		var rows [5]float64
		for i := range rows {
			rows[i] = rng.NormFloat64()
		}
		var sum float64
		for i := 0; i < want; i++ {
			// each row refreshes every 2^k samples
			for k := range rows {
				period := 1 << k
				if i%period == 0 {
					rows[k] = rng.NormFloat64()
				}
			}
			sum = 0
			for _, r := range rows {
				sum += r
			}
			out[i] = [2]float64{float64(i) * dt, sum / float64(len(rows))}
		}
	case "1/f²":
		// Brownian: cumulative sum of white, with mild leak to keep bounded.
		var v float64
		for i := 0; i < want; i++ {
			v = 0.999*v + rng.NormFloat64()
			out[i] = [2]float64{float64(i) * dt, v}
		}
	case "band-limited":
		// White then 1-pole low-pass at bw.
		alpha := math.Exp(-2 * math.Pi * bw * dt)
		var v float64
		for i := 0; i < want; i++ {
			v = alpha*v + (1-alpha)*rng.NormFloat64()
			out[i] = [2]float64{float64(i) * dt, v}
		}
	default: // white
		for i := 0; i < want; i++ {
			out[i] = [2]float64{float64(i) * dt, rng.NormFloat64()}
		}
	}
	// Normalise to requested RMS.
	var sumsq float64
	for _, pt := range out {
		sumsq += pt[1] * pt[1]
	}
	scale := 1.0
	if sumsq > 0 {
		actualRMS := math.Sqrt(sumsq / float64(len(out)))
		scale = rms / actualRMS
	}
	for i := range out {
		out[i][1] *= scale
	}
	return out, nil
}

// --- helpers ---------------------------------------------------------------

// parseEngFloat parses a SPICE-style engineering value: "1k", "10n",
// "4.7p", "100m", "1MEG", or a plain numeric. Falls back to `fallback` on
// error so the lowering layer never panics on a missing/garbage param —
// the caller has surfaced the inspector to the user, who can correct.
func parseEngFloat(s string, fallback float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	// Try plain parse first.
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	// Pull engineering suffix off the right.
	upper := strings.ToUpper(s)
	suffixes := []struct {
		s string
		m float64
	}{
		{"MEG", 1e6}, {"MIL", 25.4e-6},
		{"T", 1e12}, {"G", 1e9}, {"K", 1e3},
		{"M", 1e-3}, {"U", 1e-6}, {"N", 1e-9},
		{"P", 1e-12}, {"F", 1e-15},
	}
	for _, sf := range suffixes {
		if strings.HasSuffix(upper, sf.s) {
			head := upper[:len(upper)-len(sf.s)]
			if v, err := strconv.ParseFloat(head, 64); err == nil {
				return v * sf.m
			}
		}
	}
	return fallback
}

func max1(v, lo float64) float64 {
	if v < lo {
		return lo
	}
	return v
}

// --- *+ waveform metadata ---------------------------------------------------

// metaKeysForMode enumerates which Source.Params keys are worth writing into
// the `*+ <ref> waveform=<mode> k=v ...` companion line for round-trip. The
// inline points (PWL key) are intentionally excluded — they round-trip via
// the V1 PWL(...) line itself.
//
// `td` (seconds) and `phase` (degrees) are appended where applicable so the
// m10 time-shift survives parse → emit → parse:
//   - periodic single-freq modes (square/triangle/sawtooth) get both
//   - periodic multi-freq modes (am/twotone/noise) get td only
//   - aperiodic modes (chirp, user pwl) get td only
//   - fm has no td support (SFFM has no delay positional); omitted
func metaKeysForMode(mode string) []string {
	switch strings.ToLower(mode) {
	case ModeSquare:
		return []string{"freq", "ampl", "offset", "duty", "tr", "tf", "td", "phase"}
	case ModeTriangle:
		return []string{"freq", "ampl", "offset", "sym", "td", "phase"}
	case ModeSawtooth:
		return []string{"freq", "ampl", "offset", "dir", "td", "phase"}
	case ModeFM:
		return []string{"offset", "ampl", "fc", "fm", "dev"}
	case ModeChirp:
		return []string{"f0", "f1", "dur", "ampl", "shape", "mode", "td"}
	case ModeAM:
		return []string{"fc", "fm", "depth", "ampl", "shape", "td"}
	case ModeTwoTone:
		return []string{"f1", "f2", "a1", "a2", "dphi", "td"}
	case ModeNoise:
		return []string{"type", "rms", "bw", "seed", "dur", "td"}
	case ModePWL:
		return []string{"src", "rate", "gain", "loop", "td"}
	}
	return nil
}

// emitWaveformMeta returns the `*+ <ref> waveform=<mode> k1=v1 k2=v2` line
// for sources whose lowered SPICE form does not encode the high-level
// parameters. The keys are emitted in a stable order so two emit cycles on
// the same SourceSpec produce byte-identical output.
func emitWaveformMeta(ref string, s *circuit.SourceSpec) string {
	if s == nil {
		return ""
	}
	mode := strings.ToLower(s.Mode)
	keys := metaKeysForMode(mode)
	if len(keys) == 0 {
		return ""
	}
	parts := []string{"waveform=" + mode}
	for _, k := range keys {
		if v, ok := s.Params[k]; ok && v != "" {
			parts = append(parts, k+"="+escapeMetaValue(v))
		}
	}
	if len(parts) == 1 {
		return ""
	}
	return fmt.Sprintf("*+ %s %s", ref, strings.Join(parts, " "))
}

// escapeMetaValue keeps a parameter value safe to round-trip through the
// space-delimited *+ comment line. Currently just rejects spaces by
// substituting underscores; the user-visible inspector unescapes on display.
func escapeMetaValue(v string) string {
	return strings.ReplaceAll(v, " ", "_")
}

// unescapeMetaValue reverses escapeMetaValue.
func unescapeMetaValue(v string) string {
	return strings.ReplaceAll(v, "_", " ")
}

// recoverFromPWLPoints turns a parsed PULSE-derived synth (square/triangle/
// sawtooth) back into its high-level params. Used by the parser when a
// `waveform=<mode>` *+ line is found alongside a V1 PULSE/PWL/SFFM(...) —
// the V1 line has been parsed; we drop the lowered SPICE params in favour
// of the high-level keys carried in `meta`.
//
// The one exception is mode=pwl: the user-supplied points come back from the
// V1 PWL line (the meta only carries import provenance like src/rate/gain/
// loop), so they must be preserved into Params["points"].
func recoverFromPWLPoints(mode string, params map[string]string, meta map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range meta {
		out[k] = v
	}
	if strings.ToLower(mode) == ModePWL {
		if pts, ok := params["points"]; ok {
			out["points"] = pts
		}
	}
	return out
}

// pointsAsString round-trip helper used by tests; sorts pairs by t to compare
// stable shapes regardless of synthesizer order.
func pointsAsString(points [][2]float64) string {
	sorted := append([][2]float64(nil), points...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i][0] < sorted[j][0] })
	parts := make([]string, len(sorted))
	for i, pt := range sorted {
		parts[i] = fmt.Sprintf("(%g,%g)", pt[0], pt[1])
	}
	return strings.Join(parts, " ")
}
