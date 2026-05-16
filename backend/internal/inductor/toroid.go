package inductor

import (
	"encoding/json"
	"fmt"
	"math"
)

// designToroid computes the inductance, losses, and Q of a toroidal coil.
// Inductance follows L = AL · N² where AL is the manufacturer-published
// (or geometry-derived) inductance factor in H/N². Core loss is folded
// into Q via the material's tan δ at its reference frequency.
func designToroid(req *Request, cat *Catalog) (*Response, error) {
	var p ToroidParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params",
			Message: "toroid params: " + err.Error(),
		}
	}
	if err := ValidateToroidParams(&p); err != nil {
		return nil, err
	}

	core, err := p.Core.Resolve(cat)
	if err != nil {
		return nil, err
	}
	if core.Spec.Geometry != "toroid" {
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params.core.geometry",
			Message: fmt.Sprintf("toroid mode requires a core of toroidal geometry, got %q", core.Spec.Geometry),
		}
	}

	wire, err := p.Wire.Resolve()
	if err != nil {
		return nil, err
	}

	// Inductance: AL is stored in nH/N², so multiply by 1e-9 to get H.
	inductance := core.ALnHPerN2 * 1e-9 * float64(p.Turns) * float64(p.Turns)

	// Wire length: each turn traces the perimeter of the core cross-section
	// (rectangular approximation, width = (OD-ID)/2, height = h):
	//   perimeter_per_turn = (OD−ID) + 2·h
	// plus a turn-to-turn slack of one wire diameter to account for the
	// bend radius — this consistently undercounts otherwise.
	od := must(core.Spec.Dimensions, "od_m")
	id := must(core.Spec.Dimensions, "id_m")
	h := must(core.Spec.Dimensions, "h_m")
	perimeterPerTurn := (od - id) + 2.0*h + wire.OuterDiameterM()
	wireLength := float64(p.Turns) * perimeterPerTurn

	dcr := wire.DCResistanceOhm(wireLength)
	skinDepth := wire.SkinDepthM(req.FrequencyHz)
	acr := wire.ACResistanceOhm(wireLength, req.FrequencyHz)

	// Operating B at 1 A reference: B = μ_0 · μ_r · N · I / l_e.
	// For toroid we use the bulk μ_r (no demagnetisation correction —
	// closed magnetic path).
	const mu0 = 4e-7 * math.Pi
	muR := core.Spec.Material.MuRInitial
	operatingB := mu0 * muR * float64(p.Turns) * 1.0 / core.LeM

	// Core loss equivalent series resistance. Using tan δ at the
	// material's reference frequency, the core Q at that frequency is
	// 1/tan_delta. We approximate the dependence on operating frequency
	// as constant tan δ (Stage 2; richer μ(f)-curve handling lives in §5.2
	// open-questions territory). R_core = ω·L·tan_delta.
	var rCore float64
	if core.Spec.Material.LossFactorAtFreq != nil && core.Spec.Material.LossFactorAtFreq.TanDelta > 0 {
		omega := 2.0 * math.Pi * req.FrequencyHz
		rCore = omega * inductance * core.Spec.Material.LossFactorAtFreq.TanDelta
	}

	totalR := acr + rCore
	qVal := toroidQ(inductance, totalR, req.FrequencyHz)

	// Core loss power at the reference 1 A current.
	var coreLossW float64
	if rCore > 0 {
		const refCurrentA = 1.0
		coreLossW = 0.5 * rCore * refCurrentA * refCurrentA
	}

	// Window-fill check: turn count × outer wire diameter must fit around
	// the inner-hole circumference (each turn passes through the hole
	// once, so the wire-to-wire spacing on the inside is π·ID/N).
	fillFraction := 0.0
	if id > 0 {
		innerCircumference := math.Pi * id
		fillFraction = float64(p.Turns) * wire.OuterDiameterM() / innerCircumference
	}

	warnings := collectToroidWarnings(&p, &core, operatingB, fillFraction, req.FrequencyHz)

	details := ToroidDetails{
		WireLengthM:           wireLength,
		ALnHPerN2:             core.ALnHPerN2,
		EffectivePermeability: muR,
		CoreLossW:             coreLossW,
		OperatingBT:           operatingB,
		FillFraction:          fillFraction,
	}
	// Skin depth is part of the diagnostic surface even if it's not in the
	// formal details struct; we leave it as a free-floating warning-trigger
	// for now. Stash via skinDepth for the linter.
	_ = skinDepth

	return &Response{
		Mode:            ModeToroid,
		InductanceH:     inductance,
		DCResistanceOhm: dcr,
		QAtFrequency:    &qVal,
		SRFHz:           nil, // toroids: SRF not modelled in v1 — see §6
		Details:         details,
		Warnings:        warnings,
	}, nil
}

// toroidQ is just ωL/R_total but factored out so the toroid handler reads
// cleanly. R_total already includes core loss (R_core) and wire AC loss.
func toroidQ(inductanceH, totalResistanceOhm, freqHz float64) float64 {
	if freqHz <= 0 || inductanceH <= 0 || totalResistanceOhm <= 0 {
		return 0
	}
	omega := 2.0 * math.Pi * freqHz
	return omega * inductanceH / totalResistanceOhm
}

// must extracts a dimension from the catalog map. The catalog has already
// passed buildResolvedCore validation by the time we get here, so a
// missing key would be an internal contract violation. Returning 0 is the
// safe degenerate path — calculations that depend on it will produce
// obviously-bogus numbers rather than silently right-looking ones.
func must(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	default:
		return 0
	}
}

func collectToroidWarnings(p *ToroidParams, core *ResolvedCore, operatingB, fillFraction, freqHz float64) []Warning {
	var ws []Warning

	if p.FillCheck && fillFraction > 1.0 {
		ws = append(ws, Warning{
			Code:    "window_fill_exceeded",
			Message: fmt.Sprintf("turns fill %.0f%% of the inner-hole circumference (>100%% — wire will not physically fit)", fillFraction*100),
		})
	}

	if core.Spec.Material.BSatT > 0 && operatingB >= 0.70*core.Spec.Material.BSatT {
		ws = append(ws, Warning{
			Code:    "near_saturation",
			Message: fmt.Sprintf("Operating B = %.3f T (%.0f%% of B_sat = %.3f T)", operatingB, operatingB/core.Spec.Material.BSatT*100, core.Spec.Material.BSatT),
		})
	}

	if r := core.Spec.Material.FreqRangeHz; r != nil && (freqHz < r.Min || freqHz > r.Max) {
		ws = append(ws, Warning{
			Code:    "frequency_out_of_range",
			Message: fmt.Sprintf("operating frequency %.0f Hz is outside the material's validated range [%.0f, %.0f] Hz", freqHz, r.Min, r.Max),
		})
	}

	return ws
}
