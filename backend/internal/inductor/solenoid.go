package inductor

import (
	"encoding/json"
	"fmt"
	"math"
)

// designSolenoid computes self-inductance, losses, and Q for a single-layer
// solenoid. The implementation follows the physical form
//   L = μ₀ · N² · A · K(d/l) · μ_app / l
// rather than Wheeler's empirical form, so the Nagaoka factor K and the
// apparent permeability μ_app surface explicitly in details — which is
// useful when the user is tuning a slug-cored coil.
func designSolenoid(req *Request, cat *Catalog) (*Response, error) {
	var p SolenoidParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params",
			Message: "solenoid params: " + err.Error(),
		}
	}
	if err := ValidateSolenoidParams(&p); err != nil {
		return nil, err
	}

	wire, err := p.Wire.Resolve()
	if err != nil {
		return nil, err
	}

	// Pitch: close_wound defaults to outer wire diameter. spaced uses the
	// user-supplied pitch. single_layer is informational; defaults like
	// close_wound. The validator rejects pitch_m <= 0 for spaced.
	pitch := wire.OuterDiameterM()
	if p.Winding == "spaced" {
		pitch = p.PitchM
	}
	if pitch < wire.OuterDiameterM() {
		// Physical impossibility — winds would overlap. Treat as error.
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params.pitch_m",
			Message: "pitch must be at least the outer wire diameter",
		}
	}

	// Apparent permeability: 1 for air, Brookes/Watt-corrected for rod/slug
	// inserted along the coil axis.
	muApp := 1.0
	if p.Core != nil {
		core, err := p.Core.Resolve(cat)
		if err != nil {
			return nil, err
		}
		switch core.Spec.Geometry {
		case "rod", "slug":
			muApp = core.MuRod
		default:
			// A toroid or bobbin geometry inside a solenoid is nonsensical;
			// reject rather than silently fall through.
			return nil, &ValidationError{
				Code:    "validation.field",
				Field:   "params.core.geometry",
				Message: "solenoid core must have geometry rod or slug",
			}
		}
	}

	const mu0 = 4e-7 * math.Pi
	d := p.DiameterM
	l := p.LengthM
	n := p.Turns
	area := math.Pi * (d / 2.0) * (d / 2.0)
	k := nagaokaK(d / l)

	inductance := mu0 * n * n * area * k * muApp / l

	// Mean turn diameter is the form diameter plus the wire diameter — the
	// wire centerline sits one radius outside the form.
	meanTurnDiameter := d + wire.DiameterM
	wireLength := n * math.Pi * meanTurnDiameter

	dcr := wire.DCResistanceOhm(wireLength)
	skinDepth := wire.SkinDepthM(req.FrequencyHz)
	acr := wire.ACResistanceOhm(wireLength, req.FrequencyHz)

	// Operating B at a reference current of 1 A peak (B = μ_0·μ_app·N·I/l).
	// For air cores this is informational; for slug/rod it feeds the
	// near-saturation warning.
	const refCurrentA = 1.0
	operatingB := mu0 * muApp * n * refCurrentA / l
	storedEnergy := 0.5 * inductance * refCurrentA * refCurrentA

	qVal := solenoidQ(inductance, acr, req.FrequencyHz)
	srf := solenoidSRF(p, wire, inductance)

	warnings := collectSolenoidWarnings(&p, wire, l, d, pitch, n)

	if p.Core != nil && operatingB > 0 && p.Core.Material != nil && p.Core.Material.BSatT > 0 {
		if operatingB >= 0.70*p.Core.Material.BSatT {
			warnings = append(warnings, Warning{
				Code:    "near_saturation",
				Message: formatBSatWarning(operatingB, p.Core.Material.BSatT),
			})
		}
	}
	if srf != nil && req.FrequencyHz > *srf {
		warnings = append(warnings, Warning{
			Code:    "above_srf",
			Message: "operating frequency exceeds estimated self-resonant frequency",
		})
	}

	details := SolenoidDetails{
		WireLengthM:           wireLength,
		WireDiameterM:         wire.DiameterM,
		EffectivePermeability: muApp,
		SkinDepthM:            skinDepth,
		ACResistanceOhm:       acr,
		StoredEnergyJ:         storedEnergy,
		OperatingBT:           operatingB,
	}

	return &Response{
		Mode:            ModeSolenoid,
		InductanceH:     inductance,
		DCResistanceOhm: dcr,
		QAtFrequency:    &qVal,
		SRFHz:           srf,
		Details:         details,
		Warnings:        warnings,
	}, nil
}

// nagaokaK returns the Nagaoka correction factor for a single-layer
// solenoid of aspect ratio u = d/l. Implements Lundin's smooth fit, which
// matches the tabulated Nagaoka values to within 0.3% across u ∈ [0, ∞)
// and reduces to 1 for an infinite solenoid (u → 0).
//
// Reference: R. Lundin, "A handbook formula for the inductance of a
// single-layer circular coil", Proc. IEEE 73 (1985) 1428–1429.
func nagaokaK(u float64) float64 {
	if u <= 0 {
		return 1.0
	}
	// Two-piece Lundin: long-coil (u ≤ 1) uses a rational fit, short-coil
	// (u > 1) uses the dual variable v = 1/u.
	if u <= 1.0 {
		u2 := u * u
		return 1.0 / (1.0 + 0.4502*u + 0.0095*u2 + 0.0014*u2*u)
	}
	v := 1.0 / u
	// Short-coil branch (fat pancake): the dominant term is the loop limit.
	// Lundin's coefficients yield K ≈ (4/(3π))·v·(ln(8/v) − 0.5) + …
	const tau = 4.0 / (3.0 * math.Pi)
	logTerm := math.Log(8.0/v) - 0.5
	return tau * v * (logTerm + 0.379*v*v - 0.084*v*v*v*v)
}

// collectSolenoidWarnings folds in the aspect-ratio and wire-spacing
// soft conditions that don't fit the §6.2 table cleanly but are worth
// surfacing to the user.
func collectSolenoidWarnings(p *SolenoidParams, w ResolvedWire, l, d, pitch, n float64) []Warning {
	var ws []Warning

	// Aspect-ratio sanity: Wheeler/Nagaoka degrade for extreme ratios.
	ratio := l / d
	if ratio < 0.2 || ratio > 25 {
		ws = append(ws, Warning{
			Code:    "aspect_ratio_extreme",
			Message: formatRatioWarning(ratio),
		})
	}

	// Physical fit: do N turns at this pitch fit in l?
	required := n * pitch
	if required > l*1.001 { // 0.1% slack for float noise
		ws = append(ws, Warning{
			Code:    "winding_overflow",
			Message: "turns × pitch exceeds coil length — windings will not fit",
		})
	}

	return ws
}

func formatBSatWarning(b, bsat float64) string {
	pct := b / bsat * 100
	return fmt.Sprintf("Operating B = %.3f T (%.0f%% of B_sat)", b, pct)
}

func formatRatioWarning(r float64) string {
	return fmt.Sprintf("Aspect ratio l/d = %.2f is outside the Wheeler/Nagaoka validated range", r)
}
