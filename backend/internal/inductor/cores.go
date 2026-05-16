package inductor

import (
	"fmt"
	"math"
)

// ResolvedCore is the in-kernel representation of a core after preset
// lookup or inline parse. It carries the geometry-derived quantities the
// solver needs (A_e, l_e, A_L for toroid; μ_rod for slug).
type ResolvedCore struct {
	Spec       CoreSpec
	AeM2       float64 // effective cross-section, m²
	LeM        float64 // effective magnetic path length, m
	ALnHPerN2  float64 // resolved A_L (catalog override or geometry-derived)
	MuRod      float64 // open-circuit μ for rod/slug geometry, 1 otherwise
}

// Resolve turns a CoreRef into a ResolvedCore by either looking up the
// preset in the catalog or normalising the inline user form. Returns a
// *ValidationError for client-facing problems and a bare error for
// internal/catalog faults.
func (r CoreRef) Resolve(cat *Catalog) (ResolvedCore, error) {
	switch r.Kind {
	case "preset":
		if r.ID == "" {
			return ResolvedCore{}, &ValidationError{
				Code:    "validation.field",
				Field:   "core.id",
				Message: "preset core requires id",
			}
		}
		spec, ok := cat.Lookup(r.ID)
		if !ok {
			return ResolvedCore{}, &ValidationError{
				Code:    "core.not_found",
				Field:   "core.id",
				Message: fmt.Sprintf("core preset %q not found in catalog", r.ID),
			}
		}
		return buildResolvedCore(spec)

	case "user":
		spec := CoreSpec{
			Geometry:   r.Geometry,
			Dimensions: r.Dimensions,
		}
		if r.Material != nil {
			spec.Material = *r.Material
		}
		return buildResolvedCore(spec)

	default:
		return ResolvedCore{}, &ValidationError{
			Code:    "validation.field",
			Field:   "core.kind",
			Message: fmt.Sprintf("core.kind must be 'preset' or 'user', got %q", r.Kind),
		}
	}
}

func buildResolvedCore(spec CoreSpec) (ResolvedCore, error) {
	rc := ResolvedCore{Spec: spec, MuRod: 1.0}
	switch spec.Geometry {
	case "toroid":
		od, err := dimFloat(spec.Dimensions, "od_m")
		if err != nil {
			return rc, err
		}
		id, err := dimFloat(spec.Dimensions, "id_m")
		if err != nil {
			return rc, err
		}
		h, err := dimFloat(spec.Dimensions, "h_m")
		if err != nil {
			return rc, err
		}
		if od <= id || od <= 0 || id <= 0 || h <= 0 {
			return rc, &ValidationError{
				Code:    "validation.field",
				Field:   "core.dimensions",
				Message: "toroid requires od_m > id_m > 0 and h_m > 0",
			}
		}
		rc.AeM2 = h * (od - id) / 2.0
		rc.LeM = math.Pi * (od + id) / 2.0
		if spec.ALnHPerN2Override != nil && *spec.ALnHPerN2Override > 0 {
			rc.ALnHPerN2 = *spec.ALnHPerN2Override
		} else {
			const mu0 = 4e-7 * math.Pi
			rc.ALnHPerN2 = (mu0 * spec.Material.MuRInitial * rc.AeM2 / rc.LeM) * 1e9
		}

	case "rod", "slug":
		d, err := dimFloat(spec.Dimensions, "diameter_m")
		if err != nil {
			return rc, err
		}
		l, err := dimFloat(spec.Dimensions, "length_m")
		if err != nil {
			return rc, err
		}
		if d <= 0 || l <= 0 {
			return rc, &ValidationError{
				Code:    "validation.field",
				Field:   "core.dimensions",
				Message: "rod/slug requires diameter_m > 0 and length_m > 0",
			}
		}
		rc.AeM2 = math.Pi * (d / 2.0) * (d / 2.0)
		rc.LeM = l
		// Apparent-permeability correction for a finite-length rod
		// magnetised along its axis. The bulk μ is reduced by the
		// demagnetisation factor N(l/d):
		//   μ_app = μ_r / (1 + N·(μ_r − 1))
		// N is the axial demagnetising factor of a prolate spheroid of
		// the same aspect ratio (Bozorth, "Ferromagnetism" §11):
		//   N = (1/(m²−1)) · ((m/(2·√(m²−1)))·ln((m+√(m²−1))/(m−√(m²−1))) − 1)
		// where m = l/d. For m → 1 (sphere) N → 1/3; for m ≫ 1 (long rod)
		// N → 0. Cylinders are slightly different from spheroids but the
		// spheroid formula is the standard engineering approximation. For
		// l/d < 1 the rod is a flat disc and the axial μ_app collapses to
		// ≈1; we cap at l/d = 1 for stability.
		ldRatio := l / d
		if ldRatio < 1.0 {
			ldRatio = 1.0
		}
		var n float64
		if ldRatio == 1.0 {
			n = 1.0 / 3.0
		} else {
			m2m1 := ldRatio*ldRatio - 1.0
			sr := math.Sqrt(m2m1)
			n = (1.0 / m2m1) * ((ldRatio/(2.0*sr))*math.Log((ldRatio+sr)/(ldRatio-sr)) - 1.0)
		}
		mu := spec.Material.MuRInitial
		rc.MuRod = mu / (1.0 + n*(mu-1.0))
		rc.ALnHPerN2 = 0 // not meaningful for rod/slug

	case "bobbin":
		ae, err := dimFloat(spec.Dimensions, "a_e_m2")
		if err != nil {
			return rc, err
		}
		le, err := dimFloat(spec.Dimensions, "l_e_m")
		if err != nil {
			return rc, err
		}
		if ae <= 0 || le <= 0 {
			return rc, &ValidationError{
				Code:    "validation.field",
				Field:   "core.dimensions",
				Message: "bobbin requires a_e_m2 > 0 and l_e_m > 0",
			}
		}
		rc.AeM2 = ae
		rc.LeM = le
		const mu0 = 4e-7 * math.Pi
		rc.ALnHPerN2 = (mu0 * spec.Material.MuRInitial * ae / le) * 1e9

	default:
		return rc, &ValidationError{
			Code:    "validation.field",
			Field:   "core.geometry",
			Message: fmt.Sprintf("unknown core geometry %q (expected toroid|rod|slug|bobbin)", spec.Geometry),
		}
	}
	return rc, nil
}

func dimFloat(m map[string]any, key string) (float64, error) {
	v, ok := m[key]
	if !ok {
		return 0, &ValidationError{
			Code:    "validation.field",
			Field:   "core.dimensions." + key,
			Message: "missing dimension " + key,
		}
	}
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	default:
		return 0, &ValidationError{
			Code:    "validation.field",
			Field:   "core.dimensions." + key,
			Message: fmt.Sprintf("dimension %s must be a number", key),
		}
	}
}
