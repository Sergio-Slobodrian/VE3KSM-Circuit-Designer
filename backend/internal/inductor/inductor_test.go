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


// ── Toroid physics ───────────────────────────────────────────────────────────

func TestDesign_Toroid_T50_2_20Turns(t *testing.T) {
	// Classic QRP reference: 20 turns on T-50-2 → AL=4.9 nH/N² → L ≈ 1.96 µH.
	awg := 26
	p := ToroidParams{
		Turns:     20,
		Core:      CoreRef{Kind: "preset", ID: "T-50-2"},
		Wire:      WireSpec{AWG: &awg, Material: "copper"},
		FillCheck: true,
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 7.1e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	uH := resp.InductanceH * 1e6
	if math.Abs(uH-1.96) > 0.05 {
		t.Errorf("T-50-2 20T: got %.3f µH, want 1.96", uH)
	}
	if resp.SRFHz != nil {
		t.Errorf("toroid SRF should be nil, got %v", resp.SRFHz)
	}
	if resp.QAtFrequency == nil || *resp.QAtFrequency < 10 {
		t.Errorf("Q too low or nil: %v", resp.QAtFrequency)
	}
	d, ok := resp.Details.(ToroidDetails)
	if !ok {
		t.Fatalf("details type: %T", resp.Details)
	}
	if d.WireLengthM <= 0 {
		t.Errorf("wire length: got %v", d.WireLengthM)
	}
	if d.FillFraction <= 0 || d.FillFraction > 1.0 {
		t.Errorf("fill fraction out of plausible range: %v", d.FillFraction)
	}
}

func TestDesign_Toroid_FT37_43_5Turns(t *testing.T) {
	// 5 turns on FT-37-43 → AL=350 → L = 350·25 = 8750 nH = 8.75 µH.
	awg := 22
	p := ToroidParams{
		Turns: 5,
		Core:  CoreRef{Kind: "preset", ID: "FT-37-43"},
		Wire:  WireSpec{AWG: &awg, Material: "copper"},
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 7.0e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	uH := resp.InductanceH * 1e6
	if math.Abs(uH-8.75) > 0.1 {
		t.Errorf("FT-37-43 5T: got %.3f µH, want 8.75", uH)
	}
}

func TestDesign_Toroid_FT50_77_30Turns_AudioRange(t *testing.T) {
	// FT-50-77 (μ=2000) with 30 turns → AL=1100 → L=990 µH.
	// Operating at 100 kHz (within material range).
	awg := 24
	p := ToroidParams{
		Turns: 30,
		Core:  CoreRef{Kind: "preset", ID: "FT-50-77"},
		Wire:  WireSpec{AWG: &awg, Material: "copper"},
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 100e3, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	uH := resp.InductanceH * 1e6
	if math.Abs(uH-990.0) > 50.0 {
		t.Errorf("FT-50-77 30T: got %.0f µH, want 990", uH)
	}
}

func TestDesign_Toroid_WindowFillExceeded(t *testing.T) {
	// T-37-2 has ID = 5.21 mm → inner circumference π·5.21 ≈ 16.4 mm.
	// AWG 18 ≈ 1.02 mm → 50 turns × 1.02 = 51 mm, ~310% fill.
	awg := 18
	p := ToroidParams{
		Turns:     50,
		Core:      CoreRef{Kind: "preset", ID: "T-37-2"},
		Wire:      WireSpec{AWG: &awg, Material: "copper"},
		FillCheck: true,
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 7.1e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	found := false
	for _, w := range resp.Warnings {
		if w.Code == "window_fill_exceeded" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected window_fill_exceeded warning, got %v", resp.Warnings)
	}
}

func TestDesign_Toroid_NearSaturation(t *testing.T) {
	// User core with tiny B_sat → 1 A reference current will saturate it.
	// Use a small toroid geometry with high μ and very low B_sat.
	p := ToroidParams{
		Turns: 100,
		Core: CoreRef{
			Kind:     "user",
			Geometry: "toroid",
			Dimensions: map[string]any{
				"od_m": 0.020, "id_m": 0.010, "h_m": 0.005,
			},
			Material: &CoreMaterial{
				MuRInitial: 2000,
				BSatT:      0.01, // very low — easy to saturate
			},
		},
		Wire: WireSpec{DiameterM: 0.0005, Material: "copper"},
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 100e3, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	found := false
	for _, w := range resp.Warnings {
		if w.Code == "near_saturation" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected near_saturation warning, got %v", resp.Warnings)
	}
}

func TestDesign_Toroid_FrequencyOutOfRange(t *testing.T) {
	// T-50-2 is rated 1–30 MHz; trying it at 100 kHz should warn.
	awg := 24
	p := ToroidParams{
		Turns: 20,
		Core:  CoreRef{Kind: "preset", ID: "T-50-2"},
		Wire:  WireSpec{AWG: &awg, Material: "copper"},
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 100e3, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	found := false
	for _, w := range resp.Warnings {
		if w.Code == "frequency_out_of_range" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected frequency_out_of_range warning, got %v", resp.Warnings)
	}
}

func TestDesign_Toroid_RejectsSlugCore(t *testing.T) {
	awg := 24
	p := ToroidParams{
		Turns: 10,
		Core:  CoreRef{Kind: "preset", ID: "SLUG-IF-455"},
		Wire:  WireSpec{AWG: &awg, Material: "copper"},
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 1e6, Params: raw}
	_, err := Design(req, nil)
	if err == nil {
		t.Fatal("expected error for slug core in toroid mode")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

func TestDesign_Toroid_RejectsZeroTurns(t *testing.T) {
	awg := 24
	p := ToroidParams{
		Turns: 0,
		Core:  CoreRef{Kind: "preset", ID: "T-50-2"},
		Wire:  WireSpec{AWG: &awg, Material: "copper"},
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeToroid, FrequencyHz: 7e6, Params: raw}
	if _, err := Design(req, nil); err == nil {
		t.Error("expected zero-turns error")
	}
}

// ── Spiral physics ───────────────────────────────────────────────────────────

func fr4Substrate() SpiralSubstrate {
	return SpiralSubstrate{
		ThicknessM:       0.0016,    // 1.6 mm
		EpsilonR:         4.4,       // FR-4
		TanDelta:         0.02,      // FR-4
		CopperThicknessM: 0.0000350, // 1 oz copper
	}
}

func TestDesign_Spiral_Square_5T_Reference(t *testing.T) {
	// 5T square, OD=6mm, ID=2mm, w=0.2mm, s=0.2mm.
	// d_avg = 4mm, ρ = 4/8 = 0.5
	// L = 2.34·μ₀·25·4e-3 / (1+2.75·0.5) ≈ 124 nH
	p := SpiralParams{
		Shape:          "square",
		Turns:          5,
		OuterDiameterM: 0.006,
		InnerDiameterM: 0.002,
		TraceWidthM:    0.0002,
		TraceSpacingM:  0.0002,
		Substrate:      fr4Substrate(),
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	nH := resp.InductanceH * 1e9
	if math.Abs(nH-124.0) > 3.0 {
		t.Errorf("square 5T: got %.1f nH, want ~124", nH)
	}
	d, ok := resp.Details.(SpiralDetails)
	if !ok {
		t.Fatalf("details type: %T", resp.Details)
	}
	if d.K1 != 2.34 || d.K2 != 2.75 {
		t.Errorf("square constants: K1=%v K2=%v want 2.34/2.75", d.K1, d.K2)
	}
	if d.TraceLengthM <= 0 {
		t.Error("trace length should be positive")
	}
	if d.QConductor <= 0 || d.QDielectric <= 0 {
		t.Errorf("both Q components should be positive: cond=%v diel=%v", d.QConductor, d.QDielectric)
	}
	if resp.SRFHz == nil {
		t.Fatal("SRF should be computed for spiral")
	}
}

func TestDesign_Spiral_Circular_HigherThanSquare(t *testing.T) {
	// Same geometry, square vs circular — circular has K1=2.46 > 2.34,
	// so should yield a slightly higher L for the same params.
	base := SpiralParams{
		Turns:          5,
		OuterDiameterM: 0.006,
		InnerDiameterM: 0.002,
		TraceWidthM:    0.0002,
		TraceSpacingM:  0.0002,
		Substrate:      fr4Substrate(),
	}
	square := base
	square.Shape = "square"
	rawSq, _ := json.Marshal(square)
	respSq, err := Design(&Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: rawSq}, nil)
	if err != nil {
		t.Fatalf("square: %v", err)
	}

	circ := base
	circ.Shape = "circular"
	rawC, _ := json.Marshal(circ)
	respC, err := Design(&Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: rawC}, nil)
	if err != nil {
		t.Fatalf("circular: %v", err)
	}
	if respC.InductanceH <= respSq.InductanceH {
		t.Errorf("circular L (%.3e) should exceed square L (%.3e) at same geometry",
			respC.InductanceH, respSq.InductanceH)
	}
}

func TestDesign_Spiral_AllShapes(t *testing.T) {
	for _, shape := range []string{"square", "circular", "hexagonal", "octagonal"} {
		p := SpiralParams{
			Shape:          shape,
			Turns:          5,
			OuterDiameterM: 0.006,
			InnerDiameterM: 0.002,
			TraceWidthM:    0.0002,
			TraceSpacingM:  0.0002,
			Substrate:      fr4Substrate(),
		}
		raw, _ := json.Marshal(p)
		req := &Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: raw}
		resp, err := Design(req, nil)
		if err != nil {
			t.Fatalf("%s: %v", shape, err)
		}
		if resp.InductanceH <= 0 {
			t.Errorf("%s: non-positive L: %v", shape, resp.InductanceH)
		}
	}
}

func TestDesign_Spiral_FitCheckRejects(t *testing.T) {
	// OD=2mm, ID=1mm, w=0.2mm, s=0.2mm, N=5.
	// Available radial span = 0.5 mm; required ≈ 1.8 mm — fails.
	p := SpiralParams{
		Shape:          "square",
		Turns:          5,
		OuterDiameterM: 0.002,
		InnerDiameterM: 0.001,
		TraceWidthM:    0.0002,
		TraceSpacingM:  0.0002,
		Substrate:      fr4Substrate(),
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: raw}
	_, err := Design(req, nil)
	if err == nil {
		t.Fatal("expected fit-check error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

func TestDesign_Spiral_RejectsBadShape(t *testing.T) {
	p := SpiralParams{
		Shape:          "trapezoidal",
		Turns:          5,
		OuterDiameterM: 0.006,
		InnerDiameterM: 0.002,
		TraceWidthM:    0.0002,
		TraceSpacingM:  0.0002,
		Substrate:      fr4Substrate(),
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: raw}
	if _, err := Design(req, nil); err == nil {
		t.Error("expected bad-shape error")
	}
}

func TestDesign_Spiral_RejectsZeroTanDelta(t *testing.T) {
	sub := fr4Substrate()
	sub.TanDelta = 0 // explicitly invalid
	p := SpiralParams{
		Shape:          "square",
		Turns:          5,
		OuterDiameterM: 0.006,
		InnerDiameterM: 0.002,
		TraceWidthM:    0.0002,
		TraceSpacingM:  0.0002,
		Substrate:      sub,
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: raw}
	if _, err := Design(req, nil); err == nil {
		t.Error("expected tan_delta-required error")
	}
}

func TestDesign_Spiral_QCombination(t *testing.T) {
	// Q_total should be less than either Q_conductor or Q_dielectric alone
	// (parallel combination of losses).
	p := SpiralParams{
		Shape:          "square",
		Turns:          8,
		OuterDiameterM: 0.010,
		InnerDiameterM: 0.003,
		TraceWidthM:    0.00015,
		TraceSpacingM:  0.00015,
		Substrate:      fr4Substrate(),
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeSpiral, FrequencyHz: 100e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	d := resp.Details.(SpiralDetails)
	if resp.QAtFrequency == nil {
		t.Fatal("Q is nil")
	}
	q := *resp.QAtFrequency
	if q > d.QConductor+0.01 || q > d.QDielectric+0.01 {
		t.Errorf("Q_total (%.2f) should not exceed either component (cond=%.2f, diel=%.2f)",
			q, d.QConductor, d.QDielectric)
	}
}

func TestDesign_Spiral_AboveSRFWarning(t *testing.T) {
	// Small dense spiral with a high parasitic C → low SRF.
	// Then ask for a frequency well above it.
	p := SpiralParams{
		Shape:          "square",
		Turns:          20,
		OuterDiameterM: 0.005,
		InnerDiameterM: 0.001,
		TraceWidthM:    0.00005,
		TraceSpacingM:  0.00005,
		Substrate:      fr4Substrate(),
	}
	raw, _ := json.Marshal(p)
	req := &Request{Mode: ModeSpiral, FrequencyHz: 50e9, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	found := false
	for _, w := range resp.Warnings {
		if w.Code == "above_srf" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected above_srf warning at f=50 GHz, got %v (SRF=%v)", resp.Warnings, resp.SRFHz)
	}
}

func TestDesign_Spiral_ParasiticCapacitanceUsesSubstrate(t *testing.T) {
	// Higher εr → higher parasitic C → lower SRF.
	base := SpiralParams{
		Shape:          "square",
		Turns:          5,
		OuterDiameterM: 0.006,
		InnerDiameterM: 0.002,
		TraceWidthM:    0.0002,
		TraceSpacingM:  0.0002,
		Substrate:      fr4Substrate(),
	}
	lowEr := base
	lowEr.Substrate.EpsilonR = 1.0 // air
	rawLow, _ := json.Marshal(lowEr)
	respLow, _ := Design(&Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: rawLow}, nil)

	highEr := base
	highEr.Substrate.EpsilonR = 10.0
	rawHigh, _ := json.Marshal(highEr)
	respHigh, _ := Design(&Request{Mode: ModeSpiral, FrequencyHz: 1e9, Params: rawHigh}, nil)

	dLow := respLow.Details.(SpiralDetails)
	dHigh := respHigh.Details.(SpiralDetails)
	if dHigh.ParasiticCapacitanceF <= dLow.ParasiticCapacitanceF {
		t.Errorf("C_par should increase with ε_r: low=%.3e high=%.3e",
			dLow.ParasiticCapacitanceF, dHigh.ParasiticCapacitanceF)
	}
	if respHigh.SRFHz != nil && respLow.SRFHz != nil && *respHigh.SRFHz >= *respLow.SRFHz {
		t.Errorf("higher ε_r should lower SRF: low=%v high=%v", *respLow.SRFHz, *respHigh.SRFHz)
	}
}

// ── Coupled physics ──────────────────────────────────────────────────────────

func toroidWinding(t *testing.T, turns int, presetID string, awg int) CoupledWinding {
	t.Helper()
	p := ToroidParams{
		Turns: turns,
		Core:  CoreRef{Kind: "preset", ID: presetID},
		Wire:  WireSpec{AWG: &awg, Material: "copper"},
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal toroid: %v", err)
	}
	return CoupledWinding{Mode: ModeToroid, Params: raw}
}

func solenoidWinding(t *testing.T, turns float64, diameterM, lengthM float64, awg int) CoupledWinding {
	t.Helper()
	p := SolenoidParams{
		Turns:     turns,
		DiameterM: diameterM,
		LengthM:   lengthM,
		Wire:      WireSpec{AWG: &awg, Material: "copper"},
		Winding:   "close_wound",
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal solenoid: %v", err)
	}
	return CoupledWinding{Mode: ModeSolenoid, Params: raw}
}

func TestDesign_Coupled_SharedToroid_4to1(t *testing.T) {
	// T-50-2 4:1 step-down: 20T primary, 10T secondary.
	// L_p = 4.9·400 = 1960 nH; L_s = 4.9·100 = 490 nH.
	// k = 0.99 → M ≈ 0.99·sqrt(1960·490) ≈ 970 nH.
	cp := CoupledParams{
		Primary:    toroidWinding(t, 20, "T-50-2", 26),
		Secondary:  toroidWinding(t, 10, "T-50-2", 26),
		SharedCore: true,
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7.1e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	if resp.QAtFrequency != nil {
		t.Errorf("coupled Q should be nil, got %v", *resp.QAtFrequency)
	}
	if resp.SRFHz != nil {
		t.Errorf("coupled SRF should be nil, got %v", *resp.SRFHz)
	}
	d, ok := resp.Details.(CoupledDetails)
	if !ok {
		t.Fatalf("details type: %T", resp.Details)
	}
	if math.Abs(d.CouplingK-0.99) > 1e-9 {
		t.Errorf("shared-core toroid k: got %.3f, want 0.99", d.CouplingK)
	}
	// Mutual ~ 970 nH
	mNh := d.MutualInductanceH * 1e9
	if math.Abs(mNh-970) > 20 {
		t.Errorf("M: got %.0f nH, want ~970", mNh)
	}
	if math.Abs(d.TurnsRatio-0.5) > 1e-9 {
		t.Errorf("turns_ratio: got %v, want 0.5", d.TurnsRatio)
	}
	if math.Abs(d.ImpedanceRatio-0.25) > 1e-9 {
		t.Errorf("Z_ratio: got %v, want 0.25", d.ImpedanceRatio)
	}
	// Leakage L = L · (1 − k²) = L · 0.0199. For L_p = 1960 nH, leakage ≈ 39 nH.
	leakP := d.LeakageInductancePrimaryH * 1e9
	if math.Abs(leakP-39.0) > 2 {
		t.Errorf("primary leakage: got %.1f nH, want ~39", leakP)
	}
}

func TestDesign_Coupled_KOverride(t *testing.T) {
	cp := CoupledParams{
		Primary:           toroidWinding(t, 20, "T-50-2", 26),
		Secondary:         toroidWinding(t, 10, "T-50-2", 26),
		SharedCore:        true,
		CouplingKOverride: ptrFloat(0.85),
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7.1e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	d := resp.Details.(CoupledDetails)
	if math.Abs(d.CouplingK-0.85) > 1e-9 {
		t.Errorf("k override: got %v, want 0.85", d.CouplingK)
	}
	// Override should NOT emit the geometric-estimate warning.
	for _, w := range resp.Warnings {
		if w.Code == "coupling_geometry_estimate" {
			t.Errorf("override path should not emit coupling_geometry_estimate")
		}
	}
}

func TestDesign_Coupled_GeometricEstimate_Coaxial(t *testing.T) {
	// Two air-cored solenoids, 10mm diameter, 5mm separation, coaxial.
	// k ≈ 1/(1+0.5³) = 1/1.125 = 0.889 (clamped slightly under by 0.999 cap).
	cp := CoupledParams{
		Primary:     solenoidWinding(t, 20, 0.010, 0.020, 24),
		Secondary:   solenoidWinding(t, 20, 0.010, 0.020, 24),
		SharedCore:  false,
		Geometry:    "coaxial",
		SeparationM: 0.005,
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7.1e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	d := resp.Details.(CoupledDetails)
	if math.Abs(d.CouplingK-0.889) > 0.01 {
		t.Errorf("coaxial k: got %.3f, want ~0.889", d.CouplingK)
	}
	// Must warn that this is geometric.
	found := false
	for _, w := range resp.Warnings {
		if w.Code == "coupling_geometry_estimate" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected coupling_geometry_estimate warning, got %v", resp.Warnings)
	}
}

func TestDesign_Coupled_GeometricEstimate_StackedDecays(t *testing.T) {
	// Same coils, "stacked" geometry with same separation — should give
	// lower k than coaxial (faster decay).
	base := CoupledParams{
		Primary:     solenoidWinding(t, 20, 0.010, 0.020, 24),
		Secondary:   solenoidWinding(t, 20, 0.010, 0.020, 24),
		SharedCore:  false,
		SeparationM: 0.005,
	}
	coax := base
	coax.Geometry = "coaxial"
	stacked := base
	stacked.Geometry = "stacked"

	rawC, _ := json.Marshal(coax)
	rawS, _ := json.Marshal(stacked)
	respC, _ := Design(&Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: rawC}, nil)
	respS, _ := Design(&Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: rawS}, nil)

	dC := respC.Details.(CoupledDetails)
	dS := respS.Details.(CoupledDetails)
	if dS.CouplingK >= dC.CouplingK {
		t.Errorf("stacked k (%.3f) should be < coaxial k (%.3f)", dS.CouplingK, dC.CouplingK)
	}
}

func TestDesign_Coupled_SideBySide_LowK(t *testing.T) {
	cp := CoupledParams{
		Primary:     solenoidWinding(t, 20, 0.010, 0.020, 24),
		Secondary:   solenoidWinding(t, 20, 0.010, 0.020, 24),
		SharedCore:  false,
		Geometry:    "side_by_side",
		SeparationM: 0.020, // 2× the diameter
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	d := resp.Details.(CoupledDetails)
	// (D/(2s))² = (0.01/0.04)² = 0.0625 — quite weak.
	if d.CouplingK > 0.1 {
		t.Errorf("side_by_side k: got %.3f, want < 0.1 at 2D separation", d.CouplingK)
	}
}

func TestDesign_Coupled_RejectsSharedCoreModeMismatch(t *testing.T) {
	cp := CoupledParams{
		Primary:    solenoidWinding(t, 20, 0.010, 0.020, 24),
		Secondary:  toroidWinding(t, 10, "T-50-2", 26),
		SharedCore: true,
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: raw}
	_, err := Design(req, nil)
	if err == nil {
		t.Fatal("expected coupled_mismatch error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != "validation.coupled_mismatch" {
		t.Errorf("expected validation.coupled_mismatch, got %v", err)
	}
}

func TestDesign_Coupled_RejectsKOverrideOutOfRange(t *testing.T) {
	for _, bad := range []float64{0, -0.5, 1.5, 2.0} {
		cp := CoupledParams{
			Primary:           toroidWinding(t, 20, "T-50-2", 26),
			Secondary:         toroidWinding(t, 10, "T-50-2", 26),
			SharedCore:        true,
			CouplingKOverride: ptrFloat(bad),
		}
		raw, _ := json.Marshal(cp)
		req := &Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: raw}
		if _, err := Design(req, nil); err == nil {
			t.Errorf("k=%v: expected error", bad)
		}
	}
}

func TestDesign_Coupled_RejectsNonSharedMissingGeometry(t *testing.T) {
	cp := CoupledParams{
		Primary:    solenoidWinding(t, 20, 0.010, 0.020, 24),
		Secondary:  solenoidWinding(t, 20, 0.010, 0.020, 24),
		SharedCore: false,
		// no geometry, no separation, no override → invalid
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: raw}
	if _, err := Design(req, nil); err == nil {
		t.Error("expected error for non-shared without geometry")
	}
}

func TestDesign_Coupled_DCRIsSum(t *testing.T) {
	cp := CoupledParams{
		Primary:    toroidWinding(t, 20, "T-50-2", 26),
		Secondary:  toroidWinding(t, 10, "T-50-2", 26),
		SharedCore: true,
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: raw}
	resp, err := Design(req, nil)
	if err != nil {
		t.Fatalf("design: %v", err)
	}
	// Re-run each winding solo to compare.
	rawP, _ := json.Marshal(ToroidParams{
		Turns: 20, Core: CoreRef{Kind: "preset", ID: "T-50-2"},
		Wire: WireSpec{AWG: ptrInt(26), Material: "copper"},
	})
	respP, _ := Design(&Request{Mode: ModeToroid, FrequencyHz: 7e6, Params: rawP}, nil)
	rawS, _ := json.Marshal(ToroidParams{
		Turns: 10, Core: CoreRef{Kind: "preset", ID: "T-50-2"},
		Wire: WireSpec{AWG: ptrInt(26), Material: "copper"},
	})
	respS, _ := Design(&Request{Mode: ModeToroid, FrequencyHz: 7e6, Params: rawS}, nil)

	want := respP.DCResistanceOhm + respS.DCResistanceOhm
	if math.Abs(resp.DCResistanceOhm-want) > 1e-9 {
		t.Errorf("DCR: got %v, want %v (sum of windings)", resp.DCResistanceOhm, want)
	}
}

func TestDesign_Coupled_SubWindingErrorBubbles(t *testing.T) {
	// Force the secondary toroid to fail by referencing an unknown core.
	bad := ToroidParams{
		Turns: 10,
		Core:  CoreRef{Kind: "preset", ID: "T-37-NOPE"},
		Wire:  WireSpec{AWG: ptrInt(26), Material: "copper"},
	}
	rawBad, _ := json.Marshal(bad)
	cp := CoupledParams{
		Primary:    toroidWinding(t, 20, "T-50-2", 26),
		Secondary:  CoupledWinding{Mode: ModeToroid, Params: rawBad},
		SharedCore: true,
	}
	raw, _ := json.Marshal(cp)
	req := &Request{Mode: ModeCoupled, FrequencyHz: 7e6, Params: raw}
	_, err := Design(req, nil)
	if err == nil {
		t.Fatal("expected sub-winding error to bubble up")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	// Field should be re-anchored under params.secondary.*
	if want := "params.secondary."; len(ve.Field) < len(want) || ve.Field[:len(want)] != want {
		t.Errorf("field path: got %q, want prefix %q", ve.Field, want)
	}
}

func ptrFloat(f float64) *float64 { return &f }
func ptrInt(i int) *int           { return &i }

func TestCatalog_FullV1Set(t *testing.T) {
	cat, err := DefaultCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	wantIDs := []string{
		"T-37-2", "T-50-2", "T-68-2", "T-80-2", "T-94-2", "T-106-2",
		"T-37-6", "T-50-6", "T-68-6", "T-80-6",
		"T-37-10", "T-50-10",
		"FT-37-43", "FT-50-43", "FT-82-43", "FT-114-43",
		"FT-37-61", "FT-50-61", "FT-82-61",
		"FT-37-77", "FT-50-77",
		"SLUG-IF-455",
	}
	for _, id := range wantIDs {
		if _, ok := cat.Lookup(id); !ok {
			t.Errorf("catalog missing %q", id)
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
