package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"circuit-designer/backend/internal/inductor"
)

// InductorCoresResponse is the body of GET /api/inductor/cores. The shape
// matches inductor_designer.md §8.
type InductorCoresResponse struct {
	Cores []inductor.CoreSpec `json:"cores"`
}

// handleInductorDesign accepts a design request (any of the four modes),
// runs it through the kernel, and returns the populated response.
// Validation failures map to 400 with the kernel's own structured error
// shape (error_code / field / message); physics or catalog faults are 500.
func (s *Server) handleInductorDesign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	var req inductor.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	resp, err := inductor.Design(&req, nil)
	if err != nil {
		writeInductorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleInductorCores exposes the bundled preset catalog so the frontend
// can render preset pickers without hard-coding the list. Cached by the
// frontend for the session — the catalog is `go:embed`-baked, so its
// contents only change with a new server binary.
func (s *Server) handleInductorCores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	cat, err := inductor.DefaultCatalog()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "catalog_load", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, InductorCoresResponse{Cores: cat.List()})
}

// writeInductorError maps a kernel error onto the HTTP response. The
// kernel's *ValidationError already serialises to the wire format
// described in inductor_designer.md §8 (error_code / field / message),
// so we just hand it back as JSON. Everything else is 500 with the
// generic protocol-level ErrorPayload — it represents a kernel-internal
// failure the user can't fix by adjusting their input.
func writeInductorError(w http.ResponseWriter, err error) {
	var ve *inductor.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusBadRequest, ve)
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "internal", err.Error())
}
