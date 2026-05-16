// Package inductor designs single-winding and coupled inductors from
// physical geometry. It is the kernel behind the Inductor Designer tab
// described in inductor_designer.md at the repo root.
//
// Stage 1 implements the solenoid mode (Wheeler/Nagaoka with permeability
// scaling for non-air cores) plus the shared infrastructure — types,
// validation, AWG table, wire resolution, and the core catalog. Toroid,
// spiral, and coupled modes are added in later stages.
//
// All physical quantities at the API boundary are SI base units (metres,
// henries, hertz, ohms, teslas). The frontend handles display conversion.
//
// The package exposes Design(req) → (resp, error) as the single entry
// point dispatched on Request.Mode. Validation errors are reported as
// *ValidationError with structured fields; all other errors are physics
// failures and surface as bare errors.
package inductor
