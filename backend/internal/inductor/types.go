package inductor

import "encoding/json"

// Mode identifies one of the four design modes. See inductor_designer.md §1.
type Mode string

const (
	ModeSolenoid Mode = "solenoid"
	ModeToroid   Mode = "toroid"
	ModeSpiral   Mode = "spiral"
	ModeCoupled  Mode = "coupled"
)

// Request is the top-level POST /api/inductor/design body.
//
// Params is mode-specific and decoded lazily by the dispatcher. Keeping it
// as json.RawMessage avoids a giant tagged union and lets each mode handler
// own its own struct.
type Request struct {
	Mode        Mode            `json:"mode"`
	FrequencyHz float64         `json:"frequency_hz"`
	Params      json.RawMessage `json:"params"`
}

// Response is the top-level success body. Mode-specific extras live under
// Details, which is encoded by the mode handler.
type Response struct {
	Mode             Mode      `json:"mode"`
	InductanceH      float64   `json:"inductance_h"`
	DCResistanceOhm  float64   `json:"dc_resistance_ohm"`
	QAtFrequency     *float64  `json:"q_at_frequency"`
	SRFHz            *float64  `json:"srf_hz"`
	Details          any       `json:"details"`
	Warnings         []Warning `json:"warnings"`
}

// Warning is a soft-condition note attached to a successful response.
// Codes are listed in inductor_designer.md §6.2.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ── Solenoid mode ────────────────────────────────────────────────────────────

// SolenoidParams matches inductor_designer.md §3.1.
type SolenoidParams struct {
	Turns      float64  `json:"turns"`
	DiameterM  float64  `json:"diameter_m"`
	LengthM    float64  `json:"length_m"`
	Wire       WireSpec `json:"wire"`
	Winding    string   `json:"winding"`
	PitchM     float64  `json:"pitch_m,omitempty"`
	Core       *CoreRef `json:"core,omitempty"`
}

// SolenoidDetails populates Response.Details when Mode == solenoid.
type SolenoidDetails struct {
	WireLengthM            float64 `json:"wire_length_m"`
	WireDiameterM          float64 `json:"wire_diameter_m"`
	EffectivePermeability  float64 `json:"effective_permeability"`
	SkinDepthM             float64 `json:"skin_depth_m"`
	ACResistanceOhm        float64 `json:"ac_resistance_ohm"`
	StoredEnergyJ          float64 `json:"stored_energy_j"`
	OperatingBT            float64 `json:"operating_b_t"`
}

// ── Toroid mode (types only for stage 1; handler in stage 2) ─────────────────

// ToroidParams matches inductor_designer.md §3.2.
type ToroidParams struct {
	Turns     int      `json:"turns"`
	Core      CoreRef  `json:"core"`
	Wire      WireSpec `json:"wire"`
	FillCheck bool     `json:"fill_check"`
}

// ToroidDetails populates Response.Details when Mode == toroid.
type ToroidDetails struct {
	WireLengthM           float64 `json:"wire_length_m"`
	ALnHPerN2             float64 `json:"al_nh_per_n2"`
	EffectivePermeability float64 `json:"effective_permeability"`
	CoreLossW             float64 `json:"core_loss_w"`
	OperatingBT           float64 `json:"operating_b_t"`
	FillFraction          float64 `json:"fill_fraction"`
}

// ── Spiral mode (types only for stage 1; handler in stage 3) ─────────────────

// SpiralParams matches inductor_designer.md §3.3.
type SpiralParams struct {
	Shape           string         `json:"shape"`
	Turns           float64        `json:"turns"`
	OuterDiameterM  float64        `json:"outer_diameter_m"`
	InnerDiameterM  float64        `json:"inner_diameter_m"`
	TraceWidthM     float64        `json:"trace_width_m"`
	TraceSpacingM   float64        `json:"trace_spacing_m"`
	Substrate       SpiralSubstrate `json:"substrate"`
}

// SpiralSubstrate is the PCB substrate descriptor. tan_delta is required —
// the kernel does not invent a default. See inductor_designer.md §3.3.
type SpiralSubstrate struct {
	ThicknessM       float64 `json:"thickness_m"`
	EpsilonR         float64 `json:"epsilon_r"`
	TanDelta         float64 `json:"tan_delta"`
	CopperThicknessM float64 `json:"copper_thickness_m"`
}

// SpiralDetails populates Response.Details when Mode == spiral.
type SpiralDetails struct {
	TraceLengthM           float64 `json:"trace_length_m"`
	K1                     float64 `json:"k1"`
	K2                     float64 `json:"k2"`
	FillRatio              float64 `json:"fill_ratio"`
	ParasiticCapacitanceF  float64 `json:"parasitic_capacitance_f"`
	QConductor             float64 `json:"q_conductor"`
	QDielectric            float64 `json:"q_dielectric"`
	CurrentCapacityA       float64 `json:"current_capacity_a"`
}

// ── Coupled mode (types only for stage 1; handler in stage 4) ────────────────

// CoupledParams matches inductor_designer.md §3.4.
type CoupledParams struct {
	Primary           CoupledWinding `json:"primary"`
	Secondary         CoupledWinding `json:"secondary"`
	SharedCore        bool           `json:"shared_core"`
	Geometry          string         `json:"geometry,omitempty"`
	SeparationM       float64        `json:"separation_m,omitempty"`
	CouplingKOverride *float64       `json:"coupling_k_override,omitempty"`
}

// CoupledWinding wraps a per-winding solenoid or toroid descriptor.
type CoupledWinding struct {
	Mode   Mode            `json:"mode"`
	Params json.RawMessage `json:"params"`
}

// CoupledDetails populates Response.Details when Mode == coupled.
type CoupledDetails struct {
	Primary                    any     `json:"primary"`
	Secondary                  any     `json:"secondary"`
	MutualInductanceH          float64 `json:"mutual_inductance_h"`
	CouplingK                  float64 `json:"coupling_k"`
	LeakageInductancePrimaryH  float64 `json:"leakage_inductance_primary_h"`
	LeakageInductanceSecondaryH float64 `json:"leakage_inductance_secondary_h"`
	TurnsRatio                 float64 `json:"turns_ratio"`
	ImpedanceRatio             float64 `json:"impedance_ratio"`
}

// ── Wire ─────────────────────────────────────────────────────────────────────

// WireSpec describes the conductor used by any winding. Either DiameterM or
// AWG must be non-zero/non-nil; if both are set, DiameterM wins. See §4.
type WireSpec struct {
	DiameterM            float64 `json:"diameter_m,omitempty"`
	AWG                  *int    `json:"awg"`
	Material             string  `json:"material"`
	InsulationThicknessM float64 `json:"insulation_thickness_m"`
	TemperatureC         float64 `json:"temperature_c"`
}

// ── Core ─────────────────────────────────────────────────────────────────────

// CoreRef is either a preset reference or an inline user-defined core.
// Exactly one of Kind == "preset" (with ID set) or Kind == "user" (with the
// rest populated) is valid.
type CoreRef struct {
	Kind       string         `json:"kind"`
	ID         string         `json:"id,omitempty"`
	Geometry   string         `json:"geometry,omitempty"`
	Dimensions map[string]any `json:"dimensions,omitempty"`
	Material   *CoreMaterial  `json:"material,omitempty"`
}

// CoreSpec is the resolved form of a core — what the kernel uses after
// preset lookup or inline parse. It is also the shape of an entry in the
// bundled catalog (with PresetID set).
type CoreSpec struct {
	PresetID            string         `json:"preset_id,omitempty"`
	Name                string         `json:"name,omitempty"`
	Family              string         `json:"family,omitempty"`
	ColorCode           string         `json:"color_code,omitempty"`
	Geometry            string         `json:"geometry"`
	Dimensions          map[string]any `json:"dimensions"`
	Material            CoreMaterial   `json:"material"`
	ALnHPerN2Override   *float64       `json:"al_nh_per_n2_override,omitempty"`
}

// CoreMaterial is the magnetic-material descriptor. See §5.2.
type CoreMaterial struct {
	Name              string        `json:"name,omitempty"`
	MuRInitial        float64       `json:"mu_r_initial"`
	MuCurve           []MuPoint     `json:"mu_curve,omitempty"`
	BSatT             float64       `json:"b_sat_t,omitempty"`
	LossFactorAtFreq  *LossFactor   `json:"loss_factor_at_freq,omitempty"`
	FreqRangeHz       *FreqRange    `json:"freq_range_hz,omitempty"`
}

// MuPoint is one sample on a μ(f) curve. Real and imaginary parts let the
// kernel model both inductance and core loss vs frequency.
type MuPoint struct {
	FreqHz   float64 `json:"freq_hz"`
	MuRReal  float64 `json:"mu_r_real"`
	MuRImag  float64 `json:"mu_r_imag"`
}

// LossFactor is a single tan δ sample at a reference frequency.
type LossFactor struct {
	FreqHz   float64 `json:"freq_hz"`
	TanDelta float64 `json:"tan_delta"`
}

// FreqRange is a soft validity window; outside this range the kernel emits
// a `frequency_out_of_range` warning rather than failing.
type FreqRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// ── Errors ───────────────────────────────────────────────────────────────────

// ValidationError is the 400-class failure shape returned to clients.
// Field is dotted JSON-pointer-ish for the UI to highlight.
type ValidationError struct {
	Code    string `json:"error_code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return e.Code + " at " + e.Field + ": " + e.Message
	}
	return e.Code + ": " + e.Message
}
