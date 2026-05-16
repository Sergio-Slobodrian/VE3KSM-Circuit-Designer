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

// ValidateToroidParams enforces the §9 rules for toroid mode that don't
// require resolving the core. The core itself (must exist, must be of
// toroidal geometry) is checked by the handler after Resolve().
func ValidateToroidParams(p *ToroidParams) error {
	if p.Turns <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.turns",
			Message: "turns must be > 0",
		}
	}
	return nil
}

// ValidateSpiralParams enforces the §9 rules for spiral mode, including
// the geometric "will N turns fit in the available radial span?" check.
func ValidateSpiralParams(p *SpiralParams) error {
	switch p.Shape {
	case "square", "circular", "hexagonal", "octagonal":
	default:
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.shape",
			Message: fmt.Sprintf("shape must be square|circular|hexagonal|octagonal, got %q", p.Shape),
		}
	}
	if p.Turns <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.turns",
			Message: "turns must be > 0",
		}
	}
	if p.OuterDiameterM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.outer_diameter_m",
			Message: "outer_diameter_m must be > 0",
		}
	}
	if p.InnerDiameterM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.inner_diameter_m",
			Message: "inner_diameter_m must be > 0",
		}
	}
	if p.OuterDiameterM <= p.InnerDiameterM {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.outer_diameter_m",
			Message: "outer_diameter_m must exceed inner_diameter_m",
		}
	}
	if p.TraceWidthM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.trace_width_m",
			Message: "trace_width_m must be > 0",
		}
	}
	if p.TraceSpacingM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.trace_spacing_m",
			Message: "trace_spacing_m must be > 0",
		}
	}
	// Fit check: the radial span must hold N turns of (width + spacing),
	// minus one spacing (the innermost turn doesn't need spacing inside it).
	radialSpan := (p.OuterDiameterM - p.InnerDiameterM) / 2.0
	required := p.Turns*(p.TraceWidthM+p.TraceSpacingM) - p.TraceSpacingM
	if required > radialSpan*1.001 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.outer_diameter_m",
			Message: fmt.Sprintf("geometry doesn't fit: turns require %.4f m of radial span, only %.4f m available",
				required, radialSpan),
		}
	}
	if p.Substrate.ThicknessM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.substrate.thickness_m",
			Message: "substrate.thickness_m must be > 0",
		}
	}
	if p.Substrate.EpsilonR <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.substrate.epsilon_r",
			Message: "substrate.epsilon_r must be > 0",
		}
	}
	if p.Substrate.TanDelta <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.substrate.tan_delta",
			Message: "substrate.tan_delta must be > 0 (no default — supply via laminate preset)",
		}
	}
	if p.Substrate.CopperThicknessM <= 0 {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.substrate.copper_thickness_m",
			Message: "substrate.copper_thickness_m must be > 0",
		}
	}
	return nil
}

// ValidateCoupledParams enforces the §9 rules specific to coupled mode.
// The per-winding sub-params are validated when their respective handler
// runs — no need to duplicate those checks here.
func ValidateCoupledParams(p *CoupledParams) error {
	if p.Primary.Mode != ModeSolenoid && p.Primary.Mode != ModeToroid {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.primary.mode",
			Message: fmt.Sprintf("primary mode must be solenoid or toroid, got %q", p.Primary.Mode),
		}
	}
	if p.Secondary.Mode != ModeSolenoid && p.Secondary.Mode != ModeToroid {
		return &ValidationError{
			Code:    "validation.field",
			Field:   "params.secondary.mode",
			Message: fmt.Sprintf("secondary mode must be solenoid or toroid, got %q", p.Secondary.Mode),
		}
	}
	if p.SharedCore && p.Primary.Mode != p.Secondary.Mode {
		return &ValidationError{
			Code:    "validation.coupled_mismatch",
			Field:   "params.shared_core",
			Message: "shared_core requires primary.mode == secondary.mode",
		}
	}
	if p.CouplingKOverride != nil {
		k := *p.CouplingKOverride
		if k <= 0 || k > 1 {
			return &ValidationError{
				Code:    "validation.field",
				Field:   "params.coupling_k_override",
				Message: "coupling_k_override must be in (0, 1]",
			}
		}
	}
	if !p.SharedCore && p.CouplingKOverride == nil {
		// Geometric estimate path — need geometry + separation.
		switch p.Geometry {
		case "coaxial", "side_by_side", "stacked":
		default:
			return &ValidationError{
				Code:    "validation.field",
				Field:   "params.geometry",
				Message: fmt.Sprintf("geometry must be coaxial|side_by_side|stacked when shared_core=false and no k override, got %q", p.Geometry),
			}
		}
		if p.SeparationM <= 0 {
			return &ValidationError{
				Code:    "validation.field",
				Field:   "params.separation_m",
				Message: "separation_m must be > 0 when shared_core=false and no k override",
			}
		}
	}
	return nil
}
