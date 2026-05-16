package inductor

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

// ── AWG ──────────────────────────────────────────────────────────────────────

func TestAWGToDiameterM_KnownGauges(t *testing.T) {
	// Reference values from the Brown & Sharpe formula, rounded to 0.001 mm.
	// These also match common engineering tables.
	cases := []struct {
		awg    int
		mm     float64 // expected diameter in millimetres
	}{
		{0, 8.2515},     // 0 gauge
		{10, 2.5882},    // typical chassis wiring
		{14, 1.6281},    // residential wiring
		{20, 0.8118},    // common hookup
		{24, 0.5106},    // typical magnet wire HF coils
		{30, 0.2546},    // fine magnet wire
		{40, 0.0799},    // limit
	}
	for _, c := range cases {
		got, err := AWGToDiameterM(c.awg)
		if err != nil {
			t.Fatalf("AWG %d: unexpected error %v", c.awg, err)
		}
		gotMM := got * 1000.0
		if math.Abs(gotMM-c.mm) > 0.002 {
			t.Errorf("AWG %d: got %.4f mm, want %.4f mm", c.awg, gotMM, c.mm)
		}
	}
}

func TestAWGToDiameterM_OutOfRange(t *testing.T) {
	for _, awg := range []int{-4, 41, 100, -10} {
		if _, err := AWGToDiameterM(awg); err == nil {
			t.Errorf("AWG %d: expected error, got nil", awg)
		}
	}
}

func TestAWG0000(t *testing.T) {
	// 4/0 gauge = ~11.684 mm
	got, err := AWGToDiameterM(-3)
	if err != nil {
		t.Fatalf("0000: %v", err)
	}
	if math.Abs(got*1000-11.684) > 0.01 {
		t.Errorf("0000: got %.3f mm, want 11.684", got*1000)
	}
}

// ── Wire ─────────────────────────────────────────────────────────────────────

func TestWireSpec_Resolve_FromDiameter(t *testing.T) {
	w := WireSpec{DiameterM: 0.001, Material: "copper", TemperatureC: 25}
	r, err := w.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.DiameterM != 0.001 {
		t.Errorf("diameter: got %v, want 0.001", r.DiameterM)
	}
	if r.Material.Name != "copper" {
		t.Errorf("material: got %v", r.Material.Name)
	}
}

func TestWireSpec_Resolve_FromAWG(t *testing.T) {
	awg := 24
	w := WireSpec{AWG: &awg, Material: "copper"}
	r, err := w.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if math.Abs(r.DiameterM*1000-0.5106) > 0.002 {
		t.Errorf("AWG 24 diameter: got %.4f mm", r.DiameterM*1000)
	}
}

func TestWireSpec_Resolve_NoDiameterNoAWG(t *testing.T) {
	w := WireSpec{Material: "copper"}
	if _, err := w.Resolve(); err == nil {
		t.Error("expected error for missing wire size")
	}
}

func TestWireSpec_Resolve_DiameterWins(t *testing.T) {
	awg := 30 // 0.255 mm
	w := WireSpec{DiameterM: 0.001, AWG: &awg, Material: "copper"}
	r, err := w.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.DiameterM != 0.001 {
		t.Errorf("diameter_m should win over AWG: got %v", r.DiameterM)
	}
}

func TestWireSpec_Resolve_UnknownMaterial(t *testing.T) {
	w := WireSpec{DiameterM: 0.001, Material: "unobtanium"}
	if _, err := w.Resolve(); err == nil {
		t.Error("expected unknown-material error")
	}
}

func TestWire_DCResistance_AWG24Copper(t *testing.T) {
	// AWG 24 copper at 25°C: ~84.2 Ω/km ≈ 0.0842 Ω/m.
	awg := 24
	w := WireSpec{AWG: &awg, Material: "copper", TemperatureC: 25}
	r, _ := w.Resolve()
	rdc := r.DCResistanceOhm(1.0)
	// Hand calculation: ρ(25°C) = 1.68e-8 · (1 + 0.00393·5) = 1.713e-8
	// A = π·(0.5106e-3/2)² = 2.047e-7 m²
	// R = 1.713e-8 / 2.047e-7 = 0.0837 Ω/m
	if math.Abs(rdc-0.0837) > 0.005 {
		t.Errorf("DCR/m for AWG24 Cu @ 25°C: got %.4f, want ≈0.084", rdc)
	}
}

func TestWire_SkinDepth_CopperAt1MHz(t *testing.T) {
	w := WireSpec{DiameterM: 0.001, Material: "copper"}
	r, _ := w.Resolve()
	delta := r.SkinDepthM(1e6)
	// Copper at 1 MHz: ~66 µm
	if math.Abs(delta-66e-6) > 5e-6 {
		t.Errorf("skin depth Cu @ 1 MHz: got %.2f µm, want ~66", delta*1e6)
	}
}

func TestWire_ACResistance_ReducesToDC_AtZeroFreq(t *testing.T) {
	w := WireSpec{DiameterM: 0.001, Material: "copper"}
	r, _ := w.Resolve()
	dcr := r.DCResistanceOhm(1.0)
	acr := r.ACResistanceOhm(1.0, 0)
	if math.Abs(acr-dcr) > 1e-12 {
		t.Errorf("ACR at f=0: got %v, want %v", acr, dcr)
	}
}

func TestWire_ACResistance_RisesWithFrequency(t *testing.T) {
	w := WireSpec{DiameterM: 0.001, Material: "copper"}
	r, _ := w.Resolve()
	r1 := r.ACResistanceOhm(1.0, 1e5)
	r2 := r.ACResistanceOhm(1.0, 1e7)
	r3 := r.ACResistanceOhm(1.0, 1e8)
	if !(r1 < r2 && r2 < r3) {
		t.Errorf("AC R should rise with f: got %.4f, %.4f, %.4f", r1, r2, r3)
	}
}

// ── Catalog + cores ──────────────────────────────────────────────────────────

func TestCatalog_LoadsBundled(t *testing.T) {
	cat, err := DefaultCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	for _, id := range []string{"T-37-2", "T-50-2", "FT-37-43", "SLUG-IF-455"} {
		if _, ok := cat.Lookup(id); !ok {
			t.Errorf("catalog missing %q", id)
		}
	}
}

func TestCoreRef_ResolvePreset_T37_2(t *testing.T) {
	cat, _ := DefaultCatalog()
	ref := CoreRef{Kind: "preset", ID: "T-37-2"}
	core, err := ref.Resolve(cat)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if core.Spec.Geometry != "toroid" {
		t.Errorf("geometry: got %s", core.Spec.Geometry)
	}
	if math.Abs(core.ALnHPerN2-4.0) > 0.001 {
		t.Errorf("AL: got %v, want 4.0", core.ALnHPerN2)
	}
}

func TestCoreRef_ResolvePreset_NotFound(t *testing.T) {
	cat, _ := DefaultCatalog()
	ref := CoreRef{Kind: "preset", ID: "T-37-99"}
	_, err := ref.Resolve(cat)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != "core.not_found" {
		t.Errorf("expected core.not_found, got %v", err)
	}
}

func TestCoreRef_ResolveUser_Slug(t *testing.T) {
	ref := CoreRef{
		Kind:     "user",
		Geometry: "slug",
		Dimensions: map[string]any{
			"diameter_m": 0.006,
			"length_m":   0.024, // l/d = 4
		},
		Material: &CoreMaterial{MuRInitial: 125},
	}
	core, err := ref.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// μ_app for a slug with μ_r=125 and l/d=4 should be substantially lower
	// than 125 (typical demag factor pulls it into the 15–30 range).
	if !(core.MuRod > 5 && core.MuRod < 60) {
		t.Errorf("μ_rod for l/d=4, μ_r=125: got %v, want 5..60", core.MuRod)
	}
}

func TestCoreRef_ResolveUser_Toroid_DerivesAL(t *testing.T) {
	ref := CoreRef{
		Kind:     "user",
		Geometry: "toroid",
		Dimensions: map[string]any{
			"od_m": 0.0095,
			"id_m": 0.0053,
			"h_m":  0.0033,
		},
		Material: &CoreMaterial{MuRInitial: 10},
	}
	core, err := ref.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Geometry-derived AL for a T-37-shape with μ=10 should land near the
	// catalog's 4 nH/N² value (the catalog uses an override but the geometry
	// matches by construction).
	if core.ALnHPerN2 < 2.0 || core.ALnHPerN2 > 7.0 {
		t.Errorf("derived AL: got %v, want ≈4 nH/N²", core.ALnHPerN2)
	}
}

func TestCoreRef_ResolveUser_RejectsBadGeometry(t *testing.T) {
	ref := CoreRef{Kind: "user", Geometry: "pancake"}
	_, err := ref.Resolve(nil)
	if err == nil {
		t.Error("expected validation error for unknown geometry")
	}
}

// ── Validate ─────────────────────────────────────────────────────────────────

func TestValidateRequest_BadMode(t *testing.T) {
	req := &Request{Mode: "rectangle", FrequencyHz: 1e6}
	if err := ValidateRequest(req); err == nil {
		t.Error("expected mode error")
	}
}

func TestValidateRequest_NonPositiveFreq(t *testing.T) {
	req := &Request{Mode: ModeSolenoid, FrequencyHz: 0}
	if err := ValidateRequest(req); err == nil {
		t.Error("expected freq error")
	}
}

func TestValidateSolenoidParams_AllChecks(t *testing.T) {
	cases := []struct {
		name string
		p    SolenoidParams
	}{
		{"zero turns", SolenoidParams{DiameterM: 0.01, LengthM: 0.02}},
		{"zero diameter", SolenoidParams{Turns: 10, LengthM: 0.02}},
		{"zero length", SolenoidParams{Turns: 10, DiameterM: 0.01}},
		{"spaced without pitch", SolenoidParams{Turns: 10, DiameterM: 0.01, LengthM: 0.02, Winding: "spaced"}},
		{"bad winding", SolenoidParams{Turns: 10, DiameterM: 0.01, LengthM: 0.02, Winding: "magic"}},
	}
	for _, c := range cases {
		if err := ValidateSolenoidParams(&c.p); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

// ── Solenoid physics ─────────────────────────────────────────────────────────

func TestNagaokaK_LimitsAndMonotonicity(t *testing.T) {
	// Infinite solenoid (u = 0) → K = 1
	if math.Abs(nagaokaK(0)-1.0) > 1e-9 {
		t.Errorf("K(0): got %v, want 1", nagaokaK(0))
	}
	// K decreases monotonically with u over the long-coil branch
	prev := 1.0
	for u := 0.01; u <= 1.0; u += 0.1 {
		v := nagaokaK(u)
		if v >= prev {
			t.Errorf("K not monotone at u=%v: got %v >= prev %v", u, v, prev)
		}
		if v <= 0 || v > 1 {
			t.Errorf("K(u=%v)=%v outside (0,1]", u, v)
		}
		prev = v
	}
}

func TestDesign_Solenoid_AirCored_25T(t *testing.T) {
	// 25-turn coil, 10mm form diameter, 20mm long, AWG 24 close-wound.
	// Wheeler's metric formula L_µH = N²·d_mm²/(l_mm + 0.45·d_mm) · 0.001
	// gives 25²·100/24.5 · 0.001 = 2.55 µH as a sanity reference.
	awg := 24
	params := SolenoidParams{
		Turns:     25,
		DiameterM: 0.010,
		LengthM:   0.020,
		Wire:      WireSpec{AWG: &awg, Material: "copper"},
		Winding:   "close_wound",
	}
	raw, _ := json.Marshal(params)
	req := &Request{Mode: ModeSolenoid, FrequencyHz: 7.1e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	uH := resp.InductanceH * 1e6
	if uH < 2.0 || uH > 3.5 {
		t.Errorf("air-cored 25T: got %.2f µH, want 2..3.5", uH)
	}
	if resp.QAtFrequency == nil || *resp.QAtFrequency < 10 {
		t.Errorf("Q too low or nil: %v", resp.QAtFrequency)
	}
	if resp.SRFHz == nil || *resp.SRFHz < 10e6 {
		t.Errorf("SRF should land above 10 MHz: %v", resp.SRFHz)
	}
}

func TestDesign_Solenoid_SlugCored_RaisesL(t *testing.T) {
	awg := 24
	base := SolenoidParams{
		Turns:     25,
		DiameterM: 0.006,
		LengthM:   0.014,
		Wire:      WireSpec{AWG: &awg, Material: "copper"},
		Winding:   "close_wound",
	}
	rawAir, _ := json.Marshal(base)
	reqAir := &Request{Mode: ModeSolenoid, FrequencyHz: 1e6, Params: rawAir}
	respAir, err := Design(reqAir, nil)
	if err != nil {
		t.Fatalf("air: %v", err)
	}

	cored := base
	cored.Core = &CoreRef{Kind: "preset", ID: "SLUG-IF-455"}
	rawSlug, _ := json.Marshal(cored)
	reqSlug := &Request{Mode: ModeSolenoid, FrequencyHz: 1e6, Params: rawSlug}
	respSlug, err := Design(reqSlug, nil)
	if err != nil {
		t.Fatalf("slug: %v", err)
	}
	if respSlug.InductanceH <= respAir.InductanceH {
		t.Errorf("slug-cored L (%.3e) should exceed air-cored L (%.3e)",
			respSlug.InductanceH, respAir.InductanceH)
	}
	d, ok := respSlug.Details.(SolenoidDetails)
	if !ok {
		t.Fatalf("details type: %T", respSlug.Details)
	}
	if d.EffectivePermeability <= 1.0 {
		t.Errorf("cored μ_eff should exceed 1: got %v", d.EffectivePermeability)
	}
}

func TestDesign_Solenoid_RejectsToroidCore(t *testing.T) {
	awg := 24
	p := SolenoidParams{
		Turns:     25,
		DiameterM: 0.010,
		LengthM:   0.020,
		Wire:      WireSpec{AWG: &awg, Material: "copper"},
		Winding:   "close_wound",
		Core:      &CoreRef{Kind: "preset", ID: "T-37-2"},
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeSolenoid, FrequencyHz: 7e6, Params: raw}
	_, err := Design(req, nil)
	if err == nil {
		t.Fatal("expected error for toroid in solenoid mode")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

func TestDesign_RejectsUnimplementedModes(t *testing.T) {
	for _, m := range []Mode{ModeToroid, ModeSpiral, ModeCoupled} {
		req := &Request{Mode: m, FrequencyHz: 1e6, Params: json.RawMessage(`{}`)}
		if _, err := Design(req, nil); err == nil {
			t.Errorf("%s: expected stage-1 not-implemented error", m)
		}
	}
}

// ── Q + SRF ──────────────────────────────────────────────────────────────────

func TestSolenoidQ_ScalesWithOmega(t *testing.T) {
	q1 := solenoidQ(1e-6, 0.5, 1e6)
	q2 := solenoidQ(1e-6, 0.5, 2e6)
	if math.Abs(q2-2*q1) > 1e-6 {
		t.Errorf("Q should double with ω at fixed R: q1=%v q2=%v", q1, q2)
	}
}

func TestSolenoidSRF_FiniteForReasonableCoil(t *testing.T) {
	awg := 24
	p := SolenoidParams{
		Turns:     20,
		DiameterM: 0.010,
		LengthM:   0.025,
		Wire:      WireSpec{AWG: &awg, Material: "copper"},
	}
	wire, _ := p.Wire.Resolve()
	srf := solenoidSRF(p, wire, 2e-6)
	if srf == nil {
		t.Fatal("SRF nil for reasonable coil")
	}
	if *srf < 10e6 || *srf > 1e9 {
		t.Errorf("SRF out of plausible range: %v Hz", *srf)
	}
}
