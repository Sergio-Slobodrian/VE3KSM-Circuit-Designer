package inductor

import (
	"fmt"
)

// ValidateRequest checks the request envelope itself (mode + frequency).
// Per-mode field checks live alongside each mode's handler, with the
// shared rules below.
func ValidateRequest(req *Request) error {
	switch req.Mode {
	case ModeSolenoid, ModeToroid, ModeSpiral, ModeCoupled:
		// ok
	default:
		return &ValidationError{
			Code:    "validation.field",
			Field:   "mode",
			Message: fmt.Sprintf("mode must be one of solenoid|toroid|spiral|coupled, got %q", string(req.Mode)),
		}
	}
	if req.FrequencyHz <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "frequency_hz",
			Message: "frequency_hz must be > 0",
		}
	}
	return nil
}

// ValidateSolenoidParams enforces §9 rules for the solenoid mode that can
// be checked without resolving the core. Core resolution (which can also
// fail) is the caller's responsibility.
func ValidateSolenoidParams(p *SolenoidParams) error {
	if p.Turns <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.turns",
			Message: "turns must be > 0",
		}
	}
	if p.DiameterM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.diameter_m",
			Message: "diameter_m must be > 0",
		}
	}
	if p.LengthM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.length_m",
			Message: "length_m must be > 0",
		}
	}
	switch p.Winding {
	case "", "close_wound", "spaced", "single_layer":
		// ok; empty defaults to close_wound in the handler
	default:
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.winding",
			Message: fmt.Sprintf("winding must be close_wound|spaced|single_layer, got %q", p.Winding),
		}
	}
	if p.Winding == "spaced" && p.PitchM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.pitch_m",
			Message: "spaced winding requires pitch_m > 0",
		}
	}
	return nil
}
