package inductor

import "math"

// solenoidQ returns the unloaded Q of a solenoid at the operating
// frequency. Q = ω·L / R_ac uses the AC resistance (already includes
// skin-effect) as the loss term. Returns 0 for non-positive frequency or
// zero inductance — those cases never reach here in normal flow but the
// guards keep the function total.
func solenoidQ(inductanceH, acResistanceOhm, freqHz float64) float64 {
	if freqHz <= 0 || inductanceH <= 0 || acResistanceOhm <= 0 {
		return 0
	}
	omega := 2.0 * math.Pi * freqHz
	return omega * inductanceH / acResistanceOhm
}
