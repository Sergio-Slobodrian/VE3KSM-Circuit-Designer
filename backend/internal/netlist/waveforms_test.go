package netlist

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"circuit-designer/backend/internal/circuit"
)

// TestLowerSinNative verifies SIN sources emit the bare ngspice form without
// a *+ waveform companion (m1 contract preserved). Stored ampl is Vpp; the
// emitter halves to ngspice's peak-amplitude convention, so 0.5 Vpp in
// Params produces SIN(0 0.25 1k) on the wire — same SPICE source the
// preamp_12ax7.cir fixture has shipped since m1.
func TestLowerSinNative(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeSin,
		Params: map[string]string{
			"offset": "0", "ampl": "0.5", "freq": "1k",
		},
	}
	spec, needsMeta, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	if needsMeta {
		t.Errorf("SIN should be self-describing; needsMeta=true")
	}
	if want := "SIN(0 0.25 1k)"; spec != want {
		t.Errorf("spec: got %q, want %q", spec, want)
	}
}

// TestLowerSquareToPulse spot-checks the square→PULSE adapter at a 50% duty.
// vlo/vhi must straddle the offset, and pulse-width must be half the period.
func TestLowerSquareToPulse(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeSquare,
		Params: map[string]string{
			"freq": "1k", "ampl": "1", "offset": "0", "duty": "50",
		},
	}
	spec, needsMeta, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	if !needsMeta {
		t.Errorf("square should request waveform=meta companion")
	}
	if !strings.HasPrefix(spec, "PULSE(") {
		t.Errorf("expected PULSE(...) lowering; got %q", spec)
	}
	// vlo=-0.5 vhi=0.5, td=0, tr/tf small, pw=0.0005, per=0.001
	if !strings.Contains(spec, "-0.5") || !strings.Contains(spec, "0.5") {
		t.Errorf("vlo/vhi missing from PULSE spec: %q", spec)
	}
	if !strings.Contains(spec, "0.001") {
		t.Errorf("period (1/freq=1ms) missing from PULSE spec: %q", spec)
	}
}

// TestLowerChirpToPWL ensures the chirp synthesizer produces the expected
// number of points and starts at t=0, v≈0.
func TestLowerChirpToPWL(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeChirp,
		Params: map[string]string{
			"f0": "100", "f1": "1k", "dur": "0.01", "ampl": "1", "shape": "linear",
		},
	}
	spec, needsMeta, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	if !needsMeta {
		t.Errorf("chirp should request waveform=meta companion")
	}
	if !strings.HasPrefix(spec, "PWL(") {
		t.Errorf("expected PWL(...) lowering; got %q", spec[:40])
	}
	// First two tokens are "0 0" since chirp starts at t=0 with sin(0)=0.
	inner := strings.TrimSuffix(strings.TrimPrefix(spec, "PWL("), ")")
	tokens := strings.Fields(inner)
	if len(tokens) < 4 {
		t.Fatalf("expected several PWL points; got %d tokens", len(tokens))
	}
	if tokens[0] != "0" {
		t.Errorf("first time should be 0; got %q", tokens[0])
	}
}

// TestLowerNoiseScalesToRMS verifies the noise synthesizer normalises sample
// energy to the requested RMS.
func TestLowerNoiseScalesToRMS(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeNoise,
		Params: map[string]string{
			"type": "white", "rms": "0.1", "bw": "20k", "dur": "0.01", "seed": "7",
		},
	}
	pts, err := synthNoise(s.Params)
	if err != nil {
		t.Fatalf("synthNoise: %v", err)
	}
	var sumsq float64
	for _, p := range pts {
		sumsq += p[1] * p[1]
	}
	gotRMS := sqrt(sumsq / float64(len(pts)))
	if gotRMS < 0.099 || gotRMS > 0.101 {
		t.Errorf("noise RMS: got %g, want ~0.1", gotRMS)
	}
}

// sqrt avoids the math import in the test file body so the only dep here is
// the package under test.
func sqrt(x float64) float64 {
	z := x
	for i := 0; i < 32; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// findComponent is a local copy of the parser_test helper (which lives in the
// external _test package, so it isn't visible from this internal test file).
func findComponent(c *circuit.Circuit, ref string) *circuit.Component {
	for i := range c.Components {
		if c.Components[i].Ref == ref {
			return &c.Components[i]
		}
	}
	return nil
}

// TestSinAmplVppRoundTrip locks the m10 convention: ampl is stored in Params
// as Vpp, halved on emit, doubled back on parse. A user typing 1 Vpp into
// the inspector should see ±0.5 V on the scope, not ±1 V.
func TestSinAmplVppRoundTrip(t *testing.T) {
	c1 := &circuit.Circuit{
		Title:    " test",
		Comments: []string{},
		Components: []circuit.Component{{
			Ref: "V1", Kind: "voltage_source",
			Nodes: []string{"in", "0"},
			Source: &circuit.SourceSpec{
				Mode:   ModeSin,
				Params: map[string]string{"offset": "0", "ampl": "1", "freq": "1k"},
			},
		}},
		Libraries:  []circuit.LibraryRef{},
		Parameters: []circuit.Param{},
		Wires:      []circuit.Wire{},
		Probes:     []circuit.Probe{},
		Analyses:   []circuit.Analysis{},
	}
	var buf bytes.Buffer
	if err := Emit(c1, &buf); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(buf.String(), "SIN(0 0.5 1k)") {
		t.Errorf("expected SIN(0 0.5 1k) in emit (1 Vpp → 0.5 V peak):\n%s", buf.String())
	}
	c2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v2 := findComponent(c2, "V1")
	if v2.Source.Params["ampl"] != "1" {
		t.Errorf("ampl after parse: got %q, want %q (Vpp round-trip)",
			v2.Source.Params["ampl"], "1")
	}
}

// TestSinAmplExpressionPassesThrough confirms parameter expressions like
// `{AMP_PEAK}` are not silently halved/doubled — the user owns the .PARAM
// value's interpretation when going through expressions.
func TestSinAmplExpressionPassesThrough(t *testing.T) {
	got := vppToPeakSpiceArg("{AMP}")
	if got != "{AMP}" {
		t.Errorf("vppToPeakSpiceArg({AMP}) = %q; want passthrough", got)
	}
	got = peakToVppSpiceArg("{AMP}")
	if got != "{AMP}" {
		t.Errorf("peakToVppSpiceArg({AMP}) = %q; want passthrough", got)
	}
}

// TestRoundTripSquare confirms a full parse → emit → parse cycle preserves
// the high-level square mode and its parameters via the *+ waveform meta.
func TestRoundTripSquare(t *testing.T) {
	c1 := &circuit.Circuit{
		Title:    " test",
		Comments: []string{},
		Components: []circuit.Component{{
			Ref: "V1", Kind: "voltage_source",
			Nodes: []string{"in", "0"},
			Source: &circuit.SourceSpec{
				Mode: ModeSquare,
				Params: map[string]string{
					"freq": "1k", "ampl": "1", "offset": "0", "duty": "50",
					"tr": "1e-08", "tf": "1e-08",
				},
			},
		}},
		Analyses: []circuit.Analysis{{Kind: "tran", Args: []string{"1u", "5m"}, Enabled: true}},
		Libraries:  []circuit.LibraryRef{},
		Parameters: []circuit.Param{},
		Wires:      []circuit.Wire{},
		Probes:     []circuit.Probe{},
	}
	var buf bytes.Buffer
	if err := Emit(c1, &buf); err != nil {
		t.Fatalf("emit: %v", err)
	}
	t.Logf("emitted:\n%s", buf.String())
	c2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	v2 := findComponent(c2, "V1")
	if v2 == nil {
		t.Fatal("V1 missing after round-trip")
	}
	if v2.Source.Mode != ModeSquare {
		t.Errorf("Mode: got %q, want %q", v2.Source.Mode, ModeSquare)
	}
	for _, k := range []string{"freq", "ampl", "offset", "duty"} {
		if v2.Source.Params[k] != c1.Components[0].Source.Params[k] {
			t.Errorf("Params[%s]: got %q, want %q",
				k, v2.Source.Params[k], c1.Components[0].Source.Params[k])
		}
	}
}

// TestRoundTripChirp confirms the chirp metadata round-trips even though the
// V1 line itself only contains the lowered PWL points.
func TestRoundTripChirp(t *testing.T) {
	c1 := &circuit.Circuit{
		Title:    " test",
		Comments: []string{},
		Components: []circuit.Component{{
			Ref: "V1", Kind: "voltage_source",
			Nodes: []string{"in", "0"},
			Source: &circuit.SourceSpec{
				Mode: ModeChirp,
				Params: map[string]string{
					"f0": "20", "f1": "20k", "dur": "0.05", "ampl": "1", "shape": "log",
				},
			},
		}},
		Libraries:  []circuit.LibraryRef{},
		Parameters: []circuit.Param{},
		Wires:      []circuit.Wire{},
		Probes:     []circuit.Probe{},
		Analyses:   []circuit.Analysis{},
	}
	var buf bytes.Buffer
	if err := Emit(c1, &buf); err != nil {
		t.Fatalf("emit: %v", err)
	}
	c2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v2 := findComponent(c2, "V1")
	if v2 == nil {
		t.Fatal("V1 missing")
	}
	if v2.Source.Mode != ModeChirp {
		t.Errorf("Mode: got %q, want chirp", v2.Source.Mode)
	}
	want := map[string]string{"f0": "20", "f1": "20k", "dur": "0.05", "ampl": "1", "shape": "log"}
	if !reflect.DeepEqual(v2.Source.Params, want) {
		t.Errorf("Params:\n got %+v\nwant %+v", v2.Source.Params, want)
	}
}

// TestLowerTriangleZeroStarting locks the m10 follow-up: triangle uses
// direct PWL synthesis (zero-start, in phase with sin) instead of a PULSE
// with pw=0. For freq=1k, ampl=1Vpp, sym=50, offset=0 the points should be
// (0,0) (0.00025,0.5) (0.0005,0) (0.00075,-0.5) (0.001,0) — peak at T/4
// matches a phase=0 sine's peak.
func TestLowerTriangleZeroStarting(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeTriangle,
		Params: map[string]string{
			"freq": "1k", "ampl": "1", "offset": "0", "sym": "50",
		},
	}
	spec, _, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	if !strings.HasPrefix(spec, "PWL(") {
		t.Errorf("triangle should lower to PWL, not %q", spec[:6])
	}
	if !strings.Contains(spec, "r=0") {
		t.Errorf("triangle PWL should set r=0 to repeat: %q", spec)
	}
	// Zero-start triangle in phase with a sine: each waypoint matches what
	// a sine with the same period+ampl hits.
	for _, want := range []string{
		"0 0",          // t=0   → offset (zero crossing rising)
		"0.00025 0.5",  // t=T/4 → vhi  (peak; sin would also peak here)
		"0.0005 0",     // t=T/2 → offset (zero crossing falling)
		"0.00075 -0.5", // t=3T/4 → vlo (trough; sin would trough here)
		"0.001 0",      // t=T   → offset (end of cycle, wraps via r=0)
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("triangle PWL missing waypoint %q in %q", want, spec)
		}
	}
}

// TestSinPhaseFillsIntermediates locks the m10 follow-up: setting phase
// without td/damp emits SIN with the intermediates filled with 0 so phase
// lands at the correct positional. m1's "stop at first missing" rule is
// preserved when phase isn't set (no extra trailing zeros).
func TestSinPhaseFillsIntermediates(t *testing.T) {
	// Just basics — should still emit 3 args (m1 contract).
	bare := emitSinSpec(map[string]string{"offset": "0", "ampl": "1", "freq": "1k"})
	if bare != "SIN(0 0.5 1k)" {
		t.Errorf("bare emit: got %q, want SIN(0 0.5 1k)", bare)
	}
	// Phase set without td/damp — must emit 6 args with 0 fillers.
	withPhase := emitSinSpec(map[string]string{
		"offset": "0", "ampl": "1", "freq": "1k", "phase": "90",
	})
	if withPhase != "SIN(0 0.5 1k 0 0 90)" {
		t.Errorf("phase emit: got %q, want SIN(0 0.5 1k 0 0 90)", withPhase)
	}
	// td set without damp/phase — emits 4 args (no trailing zeros).
	withTd := emitSinSpec(map[string]string{
		"offset": "0", "ampl": "1", "freq": "1k", "td": "0.001",
	})
	if withTd != "SIN(0 0.5 1k 0.001)" {
		t.Errorf("td emit: got %q, want SIN(0 0.5 1k 0.001)", withTd)
	}
}

// TestTrianglePhaseFoldsIntoTd: phase=90 on a 1 kHz triangle adds T/4 = 0.25
// ms to the PWL td qualifier. Combined with a user-entered td=0.5m, the total
// time-shift is 0.75 ms.
func TestTrianglePhaseFoldsIntoTd(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeTriangle,
		Params: map[string]string{
			"freq": "1k", "ampl": "1", "offset": "0", "sym": "50",
			"td": "0.5m", "phase": "90",
		},
	}
	spec, _, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	if !strings.Contains(spec, "td=0.00075") {
		t.Errorf("triangle td=0.5m + phase=90° should fold to td=0.00075:\n%s", spec)
	}
}

// TestSquarePhaseFoldsIntoPulseTd: phase=90 on a 1 kHz square sets the PULSE
// td positional to T/4 = 0.25 ms.
func TestSquarePhaseFoldsIntoPulseTd(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeSquare,
		Params: map[string]string{
			"freq": "1k", "ampl": "1", "offset": "0", "duty": "50",
			"phase": "90",
		},
	}
	spec, _, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	// PULSE positional 3 = td. Should be 0.00025 (1/1000 / 4).
	if !strings.Contains(spec, "0.00025") {
		t.Errorf("square td positional should be 0.00025 (T/4):\n%s", spec)
	}
}

// TestChirpTdEmitsPwlTdQualifier: chirp's td appends `td=<value>` to the PWL
// spec. No phase concept for one-shot chirp.
func TestChirpTdEmitsPwlTdQualifier(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeChirp,
		Params: map[string]string{
			"f0": "100", "f1": "1k", "dur": "0.01", "ampl": "1", "shape": "linear",
			"td": "0.001",
		},
	}
	spec, _, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	if !strings.HasSuffix(spec, "td=0.001") {
		t.Errorf("chirp td=0.001 should append td=0.001 to PWL:\n%s", spec)
	}
}

// TestPwlTdRoundTrips a literal PWL with `td=` consumed by the parser as
// Params["td"], emitted back as `td=`.
func TestPwlTdRoundTrips(t *testing.T) {
	c1 := &circuit.Circuit{
		Title:    " test",
		Comments: []string{},
		Components: []circuit.Component{{
			Ref: "V1", Kind: "voltage_source",
			Nodes: []string{"in", "0"},
			Source: &circuit.SourceSpec{
				Mode: ModePWL,
				Params: map[string]string{
					"points": "0:0;1e-3:1;2e-3:0",
					"td":     "0.0005",
				},
			},
		}},
		Libraries:  []circuit.LibraryRef{},
		Parameters: []circuit.Param{},
		Wires:      []circuit.Wire{},
		Probes:     []circuit.Probe{},
		Analyses:   []circuit.Analysis{},
	}
	var buf bytes.Buffer
	if err := Emit(c1, &buf); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(buf.String(), "td=0.0005") {
		t.Errorf("emit missing td=0.0005:\n%s", buf.String())
	}
	c2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v2 := findComponent(c2, "V1")
	if v2.Source.Params["td"] != "0.0005" {
		t.Errorf("td after parse: got %q, want 0.0005", v2.Source.Params["td"])
	}
}

// TestLowerSawtoothEmitsTwoPointPWL — rising sawtooth = (0,vlo) → (per,vhi)
// then ngspice's r=0 wrap snaps back to vlo at the next period start.
func TestLowerSawtoothEmitsTwoPointPWL(t *testing.T) {
	s := &circuit.SourceSpec{
		Mode: ModeSawtooth,
		Params: map[string]string{
			"freq": "1k", "ampl": "1", "offset": "0", "dir": "rising",
		},
	}
	spec, _, err := lowerSource(s)
	if err != nil {
		t.Fatalf("lowerSource: %v", err)
	}
	if !strings.Contains(spec, "0 -0.5 0.001 0.5") {
		t.Errorf("rising sawtooth PWL points wrong: %q", spec)
	}
	if !strings.Contains(spec, "r=0") {
		t.Errorf("sawtooth must repeat: %q", spec)
	}
}

// TestRoundTripPulseNative ensures a literal PULSE source round-trips without
// dropping any positional argument.
func TestRoundTripPulseNative(t *testing.T) {
	c1 := &circuit.Circuit{
		Title:    " test",
		Comments: []string{},
		Components: []circuit.Component{{
			Ref: "V1", Kind: "voltage_source",
			Nodes: []string{"in", "0"},
			Source: &circuit.SourceSpec{
				Mode: ModePulse,
				Params: map[string]string{
					"v1": "0", "v2": "5", "td": "0", "tr": "1e-08", "tf": "1e-08", "pw": "0.5m", "per": "1m",
				},
			},
		}},
		Libraries:  []circuit.LibraryRef{},
		Parameters: []circuit.Param{},
		Wires:      []circuit.Wire{},
		Probes:     []circuit.Probe{},
		Analyses:   []circuit.Analysis{},
	}
	var buf bytes.Buffer
	if err := Emit(c1, &buf); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(buf.String(), "PULSE(0 5 0 1e-08 1e-08 0.5m 1m)") {
		t.Errorf("expected canonical PULSE in emit:\n%s", buf.String())
	}
	c2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(c1.Components[0].Source, findComponent(c2, "V1").Source) {
		t.Errorf("Source diverged:\n got %+v\nwant %+v",
			findComponent(c2, "V1").Source, c1.Components[0].Source)
	}
}

// TestRoundTripPWLInline checks the canonical t:v;t:v point encoding round-
// trips through PWL(t v t v ...) on the wire.
func TestRoundTripPWLInline(t *testing.T) {
	c1 := &circuit.Circuit{
		Title:    " test",
		Comments: []string{},
		Components: []circuit.Component{{
			Ref: "V1", Kind: "voltage_source",
			Nodes: []string{"in", "0"},
			Source: &circuit.SourceSpec{
				Mode: ModePWL,
				Params: map[string]string{
					"points": "0:0;1e-3:0.5;2e-3:0;3e-3:-0.5;4e-3:0",
				},
			},
		}},
		Libraries:  []circuit.LibraryRef{},
		Parameters: []circuit.Param{},
		Wires:      []circuit.Wire{},
		Probes:     []circuit.Probe{},
		Analyses:   []circuit.Analysis{},
	}
	var buf bytes.Buffer
	if err := Emit(c1, &buf); err != nil {
		t.Fatalf("emit: %v", err)
	}
	c2, err := Parse(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v2 := findComponent(c2, "V1")
	if v2 == nil || v2.Source == nil {
		t.Fatal("V1 missing")
	}
	if v2.Source.Mode != ModePWL {
		t.Errorf("Mode: got %q, want pwl", v2.Source.Mode)
	}
	gotPoints, err := parsePointsString(v2.Source.Params["points"])
	if err != nil {
		t.Fatalf("re-parse points: %v", err)
	}
	if len(gotPoints) != 5 {
		t.Errorf("got %d PWL points, want 5", len(gotPoints))
	}
}
