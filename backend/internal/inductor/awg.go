package inductor

import (
	"errors"
	"math"
)

// Brown & Sharpe American Wire Gauge. The diameter is defined by the closed
// form d = 0.005" × 92^((36−n)/39), where n is the gauge number. Gauges
// above 0 use negative n: 0→0, 00→−1, 000→−2, 0000→−3. Range supported is
// 0000 through 40 inclusive.
//
// See inductor_designer.md §4 for the rationale behind keeping this as a
// compile-time literal rather than a runtime-loaded table.

const (
	awgMin = -3 // 0000
	awgMax = 40
)

// AWGToDiameterM converts an AWG gauge to wire diameter in metres. Returns
// an error for gauges outside the supported range.
//
// The frontend may pass any of the four "thick" gauges as negative integers
// in JSON. For convenience, AWG 0 is also acceptable. Fractional gauges are
// not standard and are rejected.
func AWGToDiameterM(awg int) (float64, error) {
	if awg < awgMin || awg > awgMax {
		return 0, errors.New("awg out of supported range (0000 to 40)")
	}
	const inchToM = 0.0254
	dInches := 0.005 * math.Pow(92.0, float64(36-awg)/39.0)
	return dInches * inchToM, nil
}

// WireMaterial captures the electrical properties used to size losses and
// thermal currents. The kernel rejects unknown material names rather than
// silently substituting copper.
type WireMaterial struct {
	Name       string  // "copper" | "silver" | "aluminum"
	Rho20C     float64 // resistivity at 20°C, Ω·m
	Alpha      float64 // temperature coefficient, /°C
	RelMu      float64 // relative permeability (essentially 1 for all three)
}

var wireMaterials = map[string]WireMaterial{
	"copper": {
		Name:   "copper",
		Rho20C: 1.68e-8,
		Alpha:  0.00393,
		RelMu:  1.0,
	},
	"silver": {
		Name:   "silver",
		Rho20C: 1.59e-8,
		Alpha:  0.00380,
		RelMu:  1.0,
	},
	"aluminum": {
		Name:   "aluminum",
		Rho20C: 2.65e-8,
		Alpha:  0.00429,
		RelMu:  1.0,
	},
}

// ResolveWireMaterial returns the material descriptor for a name. The empty
// string defaults to copper, matching the wire-format implication that
// `material` is optional. Unknown non-empty names are an error.
func ResolveWireMaterial(name string) (WireMaterial, error) {
	if name == "" {
		return wireMaterials["copper"], nil
	}
	m, ok := wireMaterials[name]
	if !ok {
		return WireMaterial{}, errors.New("unknown wire material: " + name)
	}
	return m, nil
}

// ResistivityAt returns the temperature-corrected resistivity in Ω·m.
//   ρ(T) = ρ₂₀ · (1 + α·(T − 20))
func (m WireMaterial) ResistivityAt(tempC float64) float64 {
	return m.Rho20C * (1.0 + m.Alpha*(tempC-20.0))
}
