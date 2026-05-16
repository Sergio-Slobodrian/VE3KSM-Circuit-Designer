package inductor

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// designCoupled composes two single-winding designs into a transformer or
// coupled-coil result. The strategy is:
//   1. Run primary and secondary through their respective single-mode
//      handlers — this reuses every solenoid/toroid validation and gives
//      us authoritative L, R_dc, and details for each winding.
//   2. Determine the coupling factor k. Sources, in order of preference:
//      coupling_k_override → shared-core constant → geometric estimate.
//   3. Combine into M, leakage, turns/impedance ratios.
//
// q_at_frequency is intentionally null for coupled designs — a transformer
// Q is loading-dependent (source/load impedance) and the kernel has no
// view of either. The frontend can render per-winding Q from the embedded
// details if it wants to.
func designCoupled(req *Request, cat *Catalog) (*Response, error) {
	var p CoupledParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params",
			Message: "coupled params: " + err.Error(),
		}
	}
	if err := ValidateCoupledParams(&p); err != nil {
		return nil, err
	}

	primaryResp, err := runSubWinding(p.Primary, req.FrequencyHz, cat, "primary")
	if err != nil {
		return nil, err
	}
	secondaryResp, err := runSubWinding(p.Secondary, req.FrequencyHz, cat, "secondary")
	if err != nil {
		return nil, err
	}

	nPrimary, err := extractTurns(p.Primary.Mode, p.Primary.Params)
	if err != nil {
		return nil, err
	}
	nSecondary, err := extractTurns(p.Secondary.Mode, p.Secondary.Params)
	if err != nil {
		return nil, err
	}

	k, kSource, kWarnings, err := determineCouplingK(&p, cat)
	if err != nil {
		return nil, err
	}

	lPrimary := primaryResp.InductanceH
	lSecondary := secondaryResp.InductanceH
	mutual := k * math.Sqrt(lPrimary*lSecondary)

	leakagePrimary := lPrimary * (1.0 - k*k)
	leakageSecondary := lSecondary * (1.0 - k*k)

	turnsRatio := 0.0
	impedanceRatio := 0.0
	if nPrimary > 0 {
		turnsRatio = nSecondary / nPrimary
		impedanceRatio = turnsRatio * turnsRatio
	}

	totalDCR := primaryResp.DCResistanceOhm + secondaryResp.DCResistanceOhm

	warnings := append([]Warning{}, primaryResp.Warnings...)
	warnings = append(warnings, secondaryResp.Warnings...)
	warnings = append(warnings, kWarnings...)
	_ = kSource

	details := CoupledDetails{
		Primary:                     primaryResp.Details,
		Secondary:                   secondaryResp.Details,
		MutualInductanceH:           mutual,
		CouplingK:                   k,
		LeakageInductancePrimaryH:   leakagePrimary,
		LeakageInductanceSecondaryH: leakageSecondary,
		TurnsRatio:                  turnsRatio,
		ImpedanceRatio:              impedanceRatio,
	}

	return &Response{
		Mode:            ModeCoupled,
		InductanceH:     lPrimary, // self-inductance of the primary, per §6
		DCResistanceOhm: totalDCR,
		QAtFrequency:    nil, // see function doc
		SRFHz:           nil, // not modelled for coupled in v1
		Details:         details,
		Warnings:        warnings,
	}, nil
}

// runSubWinding dispatches one winding through its single-mode handler.
// Field-prefixed validation errors are rewritten to point at the parent
// coupled-params field so the UI highlights the right input.
func runSubWinding(w CoupledWinding, freqHz float64, cat *Catalog, prefix string) (*Response, error) {
	subReq := &Request{
		Mode:        w.Mode,
		FrequencyHz: freqHz,
		Params:      w.Params,
	}
	var resp *Response
	var err error
	switch w.Mode {
	case ModeSolenoid:
		resp, err = designSolenoid(subReq, cat)
	case ModeToroid:
		resp, err = designToroid(subReq, cat)
	default:
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params." + prefix + ".mode",
			Message: fmt.Sprintf("unsupported coupled winding mode %q", w.Mode),
		}
	}
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			ve.Field = "params." + prefix + "." + trimParamsPrefix(ve.Field)
			return nil, ve
		}
		return nil, err
	}
	return resp, nil
}

// trimParamsPrefix removes a leading "params." token from a validator
// field path so we can re-anchor it under the coupled-params parent.
func trimParamsPrefix(field string) string {
	const prefix = "params."
	if len(field) >= len(prefix) && field[:len(prefix)] == prefix {
		return field[len(prefix):]
	}
	return field
}

// determineCouplingK picks the source of k and produces the value, an
// identifier of which source was used, and any soft warnings. Sources:
//
//   1. coupling_k_override (when supplied): used as-is, no warning.
//   2. shared_core = true: a fixed tight-coupling constant per shared-mode.
//      0.99 for toroid+toroid (closed magnetic path, k → 1); 0.95 for
//      solenoid+solenoid (two coils sharing a slug — empirically close).
//   3. geometric estimate from `geometry` + `separation_m` + each winding's
//      characteristic diameter. Always emits coupling_geometry_estimate.
func determineCouplingK(p *CoupledParams, cat *Catalog) (k float64, source string, warnings []Warning, err error) {
	if p.CouplingKOverride != nil {
		return *p.CouplingKOverride, "override", nil, nil
	}
	if p.SharedCore {
		switch p.Primary.Mode {
		case ModeToroid:
			return 0.99, "shared_core_toroid", nil, nil
		case ModeSolenoid:
			return 0.95, "shared_core_solenoid", nil, nil
		}
	}
	// Geometric estimate.
	dPrim, err := characteristicDiameterM(p.Primary, cat)
	if err != nil {
		return 0, "", nil, err
	}
	dSec, err := characteristicDiameterM(p.Secondary, cat)
	if err != nil {
		return 0, "", nil, err
	}
	avgD := math.Sqrt(dPrim * dSec)
	if avgD <= 0 {
		return 0, "", nil, errors.New("coupled: invalid characteristic diameter (got zero)")
	}
	switch p.Geometry {
	case "coaxial":
		// Coaxial coils: k ≈ 1 / (1 + (s/D)^3). Approaches 1 as s → 0,
		// 0 as s ≫ D. Within ±15% of measured for HF air-cored coils.
		ratio := p.SeparationM / avgD
		k = 1.0 / (1.0 + ratio*ratio*ratio)
	case "stacked":
		// Stacked coils with axial gap: k ≈ exp(-2·g/r) where r is the
		// mean coil radius. Decays faster than coaxial.
		r := avgD / 2.0
		k = math.Exp(-2.0 * p.SeparationM / r)
	case "side_by_side":
		// Parallel-axis coils: weak coupling, k ≈ (D/(2·s))² capped at 0.5.
		ratio := avgD / (2.0 * p.SeparationM)
		k = ratio * ratio
		if k > 0.5 {
			k = 0.5
		}
	default:
		return 0, "", nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params.geometry",
			Message: "geometry must be coaxial|side_by_side|stacked",
		}
	}
	// Clamp to (0,1) for numerical hygiene.
	if k <= 0 {
		k = 1e-6
	}
	if k > 0.999 {
		k = 0.999
	}
	warnings = append(warnings, Warning{
		Code:    "coupling_geometry_estimate",
		Message: fmt.Sprintf("coupling k = %.3f is a geometric estimate (±15%% — supply coupling_k_override for serious work)", k),
	})
	return k, "geometric", warnings, nil
}

// characteristicDiameterM returns the diameter the geometric-k estimate
// should treat as the coil's effective size:
//   solenoid: the form diameter
//   toroid:   the core's outer diameter
func characteristicDiameterM(w CoupledWinding, cat *Catalog) (float64, error) {
	switch w.Mode {
	case ModeSolenoid:
		var p SolenoidParams
		if err := json.Unmarshal(w.Params, &p); err != nil {
			return 0, err
		}
		return p.DiameterM, nil
	case ModeToroid:
		var p ToroidParams
		if err := json.Unmarshal(w.Params, &p); err != nil {
			return 0, err
		}
		core, err := p.Core.Resolve(cat)
		if err != nil {
			return 0, err
		}
		od := must(core.Spec.Dimensions, "od_m")
		return od, nil
	}
	return 0, fmt.Errorf("characteristicDiameterM: unsupported mode %q", w.Mode)
}

// extractTurns peeks at sub-params to retrieve the turn count for the
// turns/impedance-ratio calculation, without re-running the full handler.
func extractTurns(mode Mode, params json.RawMessage) (float64, error) {
	switch mode {
	case ModeSolenoid:
		var p SolenoidParams
		if err := json.Unmarshal(params, &p); err != nil {
			return 0, err
		}
		return p.Turns, nil
	case ModeToroid:
		var p ToroidParams
		if err := json.Unmarshal(params, &p); err != nil {
			return 0, err
		}
		return float64(p.Turns), nil
	}
	return 0, fmt.Errorf("extractTurns: unsupported mode %q", mode)
}
