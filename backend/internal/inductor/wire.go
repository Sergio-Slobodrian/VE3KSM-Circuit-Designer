package inductor

import (
	"errors"
	"math"
)

// ResolvedWire is what the kernel actually uses — a WireSpec with the
// diameter pinned down and the material lookup done. Constructing one
// runs all the wire-side validation rules from §9.
type ResolvedWire struct {
	DiameterM            float64
	InsulationThicknessM float64
	Material             WireMaterial
	TemperatureC         float64
}

// Resolve normalises a WireSpec into a ResolvedWire. If both DiameterM and
// AWG are supplied, DiameterM wins (see §4). Either must be present.
func (w WireSpec) Resolve() (ResolvedWire, error) {
	var d float64
	switch {
	case w.DiameterM > 0:
		d = w.DiameterM
	case w.AWG != nil:
		v, err := AWGToDiameterM(*w.AWG)
		if err != nil {
			return ResolvedWire{}, err
		}
		d = v
	default:
		return ResolvedWire{}, errors.New("wire: must supply either diameter_m or awg")
	}
	if d <= 0 {
		return ResolvedWire{}, errors.New("wire: diameter must be positive")
	}
	mat, err := ResolveWireMaterial(w.Material)
	if err != nil {
		return ResolvedWire{}, err
	}
	temp := w.TemperatureC
	if temp == 0 {
		temp = 25.0
	}
	ins := w.InsulationThicknessM
	if ins < 0 {
		return ResolvedWire{}, errors.New("wire: insulation thickness must be non-negative")
	}
	return ResolvedWire{
		DiameterM:            d,
		InsulationThicknessM: ins,
		Material:             mat,
		TemperatureC:         temp,
	}, nil
}

// CrossSectionM2 returns the conductor cross-section in m².
func (r ResolvedWire) CrossSectionM2() float64 {
	return math.Pi * (r.DiameterM / 2.0) * (r.DiameterM / 2.0)
}

// OuterDiameterM returns the diameter including insulation. Used to compute
// minimum pitch for close-wound coils.
func (r ResolvedWire) OuterDiameterM() float64 {
	return r.DiameterM + 2.0*r.InsulationThicknessM
}

// DCResistanceOhm returns the DC resistance for the given wire length.
//   R = ρ(T) · L / A
func (r ResolvedWire) DCResistanceOhm(lengthM float64) float64 {
	rho := r.Material.ResistivityAt(r.TemperatureC)
	return rho * lengthM / r.CrossSectionM2()
}

// SkinDepthM returns the skin depth at the given frequency.
//   δ = sqrt(ρ / (π·f·μ))   with μ = μ₀·μ_r
//
// For frequency_hz ≤ 0, returns +Inf so the AC-resistance calculation cleanly
// reduces to the DC case.
func (r ResolvedWire) SkinDepthM(freqHz float64) float64 {
	if freqHz <= 0 {
		return math.Inf(1)
	}
	rho := r.Material.ResistivityAt(r.TemperatureC)
	const mu0 = 4e-7 * math.Pi
	mu := mu0 * r.Material.RelMu
	return math.Sqrt(rho / (math.Pi * freqHz * mu))
}

// ACResistanceOhm returns the AC resistance for the given wire length at
// frequency f. Uses the standard round-wire approximation
//   x = d / (δ·√2)
//   R_ac/R_dc ≈ 1 + x⁴/48                  for x < 1
//   R_ac/R_dc ≈ x/(2·√2) + 1/4              for x ≥ 1
// which matches the Bessel-function exact ratio to a few percent across the
// full range and reduces to R_dc at low frequency.
func (r ResolvedWire) ACResistanceOhm(lengthM, freqHz float64) float64 {
	rdc := r.DCResistanceOhm(lengthM)
	if freqHz <= 0 {
		return rdc
	}
	delta := r.SkinDepthM(freqHz)
	if math.IsInf(delta, 1) || delta <= 0 {
		return rdc
	}
	x := r.DiameterM / (delta * math.Sqrt(2.0))
	var ratio float64
	if x < 1.0 {
		ratio = 1.0 + (x*x*x*x)/48.0
	} else {
		ratio = x/(2.0*math.Sqrt(2.0)) + 0.25
	}
	return rdc * ratio
}
