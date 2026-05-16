package inductor

import "math"

// solenoidSRF returns the self-resonant frequency of a single-layer
// solenoid, derived from Medhurst's empirical formula for the distributed
// self-capacitance of an air-cored coil:
//
//   C_self (pF) = H(l/d) · d_cm
//   H(l/d) ≈ 0.1126·(l/d) + 0.08 + 0.27/sqrt(l/d)
//
// then SRF = 1 / (2π·√(L·C_self)). Medhurst is air-cored only; for a
// slug-cored coil the effective C is approximately the same (the slug
// barely affects the inter-turn capacitance) so we use the same formula
// across the board, with the understanding that ±20% accuracy is the best
// any closed-form estimate achieves.
//
// Returns nil for degenerate inputs (zero length, zero diameter, or
// non-positive inductance) so the response can carry srf_hz: null.
func solenoidSRF(p SolenoidParams, _ ResolvedWire, inductanceH float64) *float64 {
	if p.LengthM <= 0 || p.DiameterM <= 0 || inductanceH <= 0 {
		return nil
	}
	ld := p.LengthM / p.DiameterM
	if ld <= 0 {
		return nil
	}
	h := 0.1126*ld + 0.08 + 0.27/math.Sqrt(ld)
	dCm := p.DiameterM * 100.0
	cSelfPF := h * dCm
	cSelfF := cSelfPF * 1e-12
	if cSelfF <= 0 {
		return nil
	}
	srf := 1.0 / (2.0 * math.Pi * math.Sqrt(inductanceH*cSelfF))
	return &srf
}
