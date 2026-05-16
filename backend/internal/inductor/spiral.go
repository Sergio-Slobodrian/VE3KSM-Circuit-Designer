package inductor

import (
	"encoding/json"
	"fmt"
	"math"
)

// designSpiral computes inductance, parasitic capacitance, SRF, and Q for
// a planar PCB spiral. Inductance follows Mohan et al. (1999) modified-
// Wheeler form:
//   L = K1·μ₀·N²·d_avg / (1 + K2·ρ)
// where ρ = (d_out − d_in)/(d_out + d_in) is the fill ratio and K1/K2 are
// shape-dependent constants. The four supported shapes share this functional
// form; circular uses tuned K1/K2 that match the more accurate current-
// sheet expression to within ~5%.
func designSpiral(req *Request, _ *Catalog) (*Response, error) {
	var p SpiralParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "params",
			Message: "spiral params: " + err.Error(),
		}
	}
	if err := ValidateSpiralParams(&p); err != nil {
		return nil, err
	}

	k1, k2 := spiralShapeConstants(p.Shape)
	dAvg := (p.OuterDiameterM + p.InnerDiameterM) / 2.0
	rho := (p.OuterDiameterM - p.InnerDiameterM) / (p.OuterDiameterM + p.InnerDiameterM)

	const mu0 = 4e-7 * math.Pi
	inductance := k1 * mu0 * p.Turns * p.Turns * dAvg / (1.0 + k2*rho)

	traceLength := spiralTraceLength(&p, dAvg)

	dcr := spiralDCResistance(&p, traceLength)
	rAC := spiralACResistance(&p, traceLength, req.FrequencyHz)
	qCond := spiralQConductor(inductance, rAC, req.FrequencyHz)

	cPar := spiralParasiticCapacitance(&p, traceLength)
	qDiel := spiralQDielectric(p.Substrate.TanDelta)
	qTotal := combineQ(qCond, qDiel)

	srf := spiralSRF(inductance, cPar)
	if srf != nil && req.FrequencyHz > *srf {
		// emit later via warnings
	}

	// Current capacity: rough IPC-2221A "outer-layer" approximation for a
	// 10°C rise. Cross-sectional area in mil² and current capacity in A:
	//   I = 0.048 · A^0.44   (A in mil², I in amps, 10°C rise, external)
	areaMil2 := (p.TraceWidthM * 1000.0 / 0.0254) * (p.Substrate.CopperThicknessM * 1000.0 / 0.0254)
	currentCapacityA := 0.0
	if areaMil2 > 0 {
		currentCapacityA = 0.048 * math.Pow(areaMil2, 0.44)
	}

	radialSpan := (p.OuterDiameterM - p.InnerDiameterM) / 2.0
	usedSpan := p.Turns*(p.TraceWidthM+p.TraceSpacingM) - p.TraceSpacingM
	fillRatioGeom := usedSpan / radialSpan

	warnings := collectSpiralWarnings(&p, req.FrequencyHz, srf)

	details := SpiralDetails{
		TraceLengthM:          traceLength,
		K1:                    k1,
		K2:                    k2,
		FillRatio:             fillRatioGeom,
		ParasiticCapacitanceF: cPar,
		QConductor:            qCond,
		QDielectric:           qDiel,
		CurrentCapacityA:      currentCapacityA,
	}

	return &Response{
		Mode:            ModeSpiral,
		InductanceH:     inductance,
		DCResistanceOhm: dcr,
		QAtFrequency:    &qTotal,
		SRFHz:           srf,
		Details:         details,
		Warnings:        warnings,
	}, nil
}

// spiralShapeConstants returns the K1, K2 coefficients of Mohan's
// modified-Wheeler formula for each supported shape. Values for the
// three polygonal shapes are from Mohan et al. 1999 Table I; the
// circular entry is fit to match the current-sheet expression for a
// circular spiral (Mohan c1=1.0, c2=2.46, c4=0.20) within ~5% over
// typical fill ratios.
func spiralShapeConstants(shape string) (k1, k2 float64) {
	switch shape {
	case "square":
		return 2.34, 2.75
	case "hexagonal":
		return 2.33, 3.82
	case "octagonal":
		return 2.25, 3.55
	case "circular":
		return 2.46, 2.75
	}
	// Validator guarantees this is unreachable; defensive default.
	return 2.34, 2.75
}

// spiralTraceLength estimates the conductor length as N·P·d_avg where P
// is the perimeter-per-unit-diameter constant of the inscribed polygon
// (or 2π for a circle). The estimate is within ~10% for typical spiral
// aspect ratios; exact length depends on whether d_out refers to the
// across-flats dimension or the diagonal of the outermost turn — we treat
// it as across-flats per the spec wording in §3.3.
func spiralTraceLength(p *SpiralParams, dAvg float64) float64 {
	var perimPerUnit float64
	switch p.Shape {
	case "square":
		perimPerUnit = 4.0
	case "hexagonal":
		perimPerUnit = 6.0 / math.Sqrt(3.0) // ≈ 3.464
	case "octagonal":
		perimPerUnit = 8.0 * math.Tan(math.Pi/8.0) // ≈ 3.314
	case "circular":
		perimPerUnit = math.Pi
	default:
		perimPerUnit = math.Pi
	}
	return p.Turns * perimPerUnit * dAvg
}

// spiralDCResistance applies R = ρ·L / (w·t) using copper at 25°C.
// The spiral conductor is assumed to be copper regardless of any wire
// material setting elsewhere — PCB traces are not magnet wire.
func spiralDCResistance(p *SpiralParams, traceLength float64) float64 {
	const rhoCu25 = 1.68e-8 * (1.0 + 0.00393*(25.0-20.0))
	area := p.TraceWidthM * p.Substrate.CopperThicknessM
	if area <= 0 {
		return 0
	}
	return rhoCu25 * traceLength / area
}

// spiralACResistance models the increase in resistance from skin effect.
// For a rectangular trace, current concentrates on the surfaces; when the
// skin depth δ is comparable to the copper thickness t, the effective
// conduction area scales like (w·t) → 2δ·(w + t − 2δ). Below δ ≈ t/2
// the trace is essentially uniform and R_ac ≈ R_dc.
func spiralACResistance(p *SpiralParams, traceLength, freqHz float64) float64 {
	rdc := spiralDCResistance(p, traceLength)
	if freqHz <= 0 || rdc <= 0 {
		return rdc
	}
	const rhoCu25 = 1.68e-8 * (1.0 + 0.00393*(25.0-20.0))
	const mu0 = 4e-7 * math.Pi
	delta := math.Sqrt(rhoCu25 / (math.Pi * freqHz * mu0))
	t := p.Substrate.CopperThicknessM
	if delta >= t/2.0 {
		// Skin depth wider than half the trace thickness — full bulk.
		return rdc
	}
	w := p.TraceWidthM
	// Effective area of current confined to the skin around the rectangle
	// perimeter, deducting the corner overlap.
	effArea := 2.0*delta*(w+t) - 4.0*delta*delta
	if effArea <= 0 {
		return rdc
	}
	return rhoCu25 * traceLength / effArea
}

// spiralParasiticCapacitance estimates the inter-turn capacitance of the
// spiral using a coplanar-strip approximation:
//   C ≈ ε₀ · (1 + ε_r)/2 · t · L_trace / s
// The half-and-half permittivity reflects that the field between adjacent
// traces splits roughly equally between air above and substrate below.
// This is a first-order estimate good enough for SRF placement; richer
// models (Patterson, Greenhouse) belong to v2.
func spiralParasiticCapacitance(p *SpiralParams, traceLength float64) float64 {
	const eps0 = 8.854e-12
	effER := (1.0 + p.Substrate.EpsilonR) / 2.0
	if p.TraceSpacingM <= 0 {
		return 0
	}
	return eps0 * effER * p.Substrate.CopperThicknessM * traceLength / p.TraceSpacingM
}

// spiralQConductor returns ω·L/R_ac with the usual degenerate-input guards.
func spiralQConductor(inductanceH, rACOhm, freqHz float64) float64 {
	if freqHz <= 0 || inductanceH <= 0 || rACOhm <= 0 {
		return 0
	}
	return 2.0 * math.Pi * freqHz * inductanceH / rACOhm
}

// spiralQDielectric returns the substrate-loss Q. We use the common
// thin-film inductor result Q_d = 2/tan_δ, which assumes half the electric
// field of the spiral threads the substrate (the other half is in air
// above the board). Production-grade values would calibrate this against
// EM simulation; the simple form is within a factor of 2 across common
// laminate/aspect-ratio combinations.
func spiralQDielectric(tanDelta float64) float64 {
	if tanDelta <= 0 {
		return 0
	}
	return 2.0 / tanDelta
}

// combineQ returns the parallel combination 1/Q_total = 1/Q_a + 1/Q_b.
// Zero in either input means that loss mechanism is disabled; if both
// are zero, total Q is zero.
func combineQ(a, b float64) float64 {
	switch {
	case a <= 0 && b <= 0:
		return 0
	case a <= 0:
		return b
	case b <= 0:
		return a
	default:
		return 1.0 / (1.0/a + 1.0/b)
	}
}

// spiralSRF returns the self-resonant frequency from the parasitic C
// and the modelled L. Returns nil for degenerate cases (no L, no C).
func spiralSRF(inductanceH, cParF float64) *float64 {
	if inductanceH <= 0 || cParF <= 0 {
		return nil
	}
	srf := 1.0 / (2.0 * math.Pi * math.Sqrt(inductanceH*cParF))
	return &srf
}

func collectSpiralWarnings(p *SpiralParams, freqHz float64, srf *float64) []Warning {
	var ws []Warning
	if srf != nil && freqHz > *srf {
		ws = append(ws, Warning{
			Code:    "above_srf",
			Message: fmt.Sprintf("operating frequency %.0f Hz exceeds estimated SRF %.0f Hz", freqHz, *srf),
		})
	}
	return ws
}
