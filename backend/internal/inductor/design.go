package inductor

import "fmt"

// Design is the public entry point. It validates the envelope, looks up the
// shared catalog, then dispatches to the per-mode handler. The Catalog
// argument is the bundled preset library — pass DefaultCatalog() in normal
// production code; tests pass a custom catalog when they need a fixture.
//
// On success returns a *Response. On client-facing failure returns a
// *ValidationError (HTTP 400); on physics or catalog-parse failure returns
// a bare error (HTTP 500).
func Design(req *Request, cat *Catalog) (*Response, error) {
	if req == nil {
		return nil, &ValidationError{Code: "validation.field", Field: "", Message: "request is nil"}
	}
	if err := ValidateRequest(req); err != nil {
		return nil, err
	}
	if cat == nil {
		c, err := DefaultCatalog()
		if err != nil {
			return nil, fmt.Errorf("catalog load: %w", err)
		}
		cat = c
	}

	switch req.Mode {
	case ModeSolenoid:
		return designSolenoid(req, cat)
	case ModeToroid:
		return designToroid(req, cat)
	case ModeSpiral:
		return designSpiral(req, cat)
	case ModeCoupled:
		return designCoupled(req, cat)
	default:
		// ValidateRequest already filtered this, but the compiler can't see that.
		return nil, &ValidationError{
			Code:    "validation.field",
			Field:   "mode",
			Message: fmt.Sprintf("unknown mode %q", req.Mode),
		}
	}
}
