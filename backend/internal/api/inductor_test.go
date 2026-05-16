package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"circuit-designer/backend/internal/inductor"
)

// newInductorTestServer wires the routes against a stub engine and returns
// the live httptest.Server. The inductor handlers don't touch the engine,
// but Server.New requires a non-nil engine — fakeEngine fits.
func newInductorTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	srv := New(&fakeEngine{}, Options{Logger: quietLogger()})
	hs := httptest.NewServer(srv.Routes())
	return hs, func() { hs.Close(); _ = srv.Close() }
}

func TestInductorCores_ReturnsCatalog(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()

	resp, err := http.Get(hs.URL + "/api/inductor/cores")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: got %d", resp.StatusCode)
	}
	var body InductorCoresResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Cores) < 20 {
		t.Errorf("expected ≥20 cores, got %d", len(body.Cores))
	}
	// Spot-check a known preset
	found := false
	for _, c := range body.Cores {
		if c.PresetID == "T-50-2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("catalog missing T-50-2")
	}
}

func TestInductorCores_RejectsPost(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()

	resp, err := http.Post(hs.URL+"/api/inductor/cores", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

func TestInductorDesign_SolenoidSuccess(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()

	awg := 24
	sub := inductor.SolenoidParams{
		Turns: 25, DiameterM: 0.010, LengthM: 0.020,
		Wire:    inductor.WireSpec{AWG: &awg, Material: "copper"},
		Winding: "close_wound",
	}
	subRaw, _ := json.Marshal(sub)
	body, _ := json.Marshal(inductor.Request{
		Mode:        inductor.ModeSolenoid,
		FrequencyHz: 7.1e6,
		Params:      subRaw,
	})

	resp, err := http.Post(hs.URL+"/api/inductor/design", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, body=%s", resp.StatusCode, raw)
	}
	var out inductor.Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Mode != inductor.ModeSolenoid {
		t.Errorf("mode echo: got %q", out.Mode)
	}
	uH := out.InductanceH * 1e6
	if uH < 2.0 || uH > 3.5 {
		t.Errorf("L: got %.2f µH, want 2..3.5", uH)
	}
}

func TestInductorDesign_ToroidSuccess(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()

	awg := 26
	sub := inductor.ToroidParams{
		Turns: 20,
		Core:  inductor.CoreRef{Kind: "preset", ID: "T-50-2"},
		Wire:  inductor.WireSpec{AWG: &awg, Material: "copper"},
	}
	subRaw, _ := json.Marshal(sub)
	body, _ := json.Marshal(inductor.Request{
		Mode:        inductor.ModeToroid,
		FrequencyHz: 7.1e6,
		Params:      subRaw,
	})

	resp, err := http.Post(hs.URL+"/api/inductor/design", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, body=%s", resp.StatusCode, raw)
	}
	var out inductor.Response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.InductanceH < 1.5e-6 || out.InductanceH > 2.5e-6 {
		t.Errorf("L for T-50-2 20T: got %.3e", out.InductanceH)
	}
}

func TestInductorDesign_ValidationErrorMapsTo400(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()

	// Bad mode "rectangle"
	body := []byte(`{"mode":"rectangle","frequency_hz":1e6,"params":{}}`)
	resp, err := http.Post(hs.URL+"/api/inductor/design", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, want 400, body=%s", resp.StatusCode, raw)
	}
	var ve inductor.ValidationError
	if err := json.NewDecoder(resp.Body).Decode(&ve); err != nil {
		t.Fatalf("decode validation error: %v", err)
	}
	if ve.Code == "" {
		t.Errorf("error_code missing in 400 response: %+v", ve)
	}
	if ve.Field != "mode" {
		t.Errorf("field: got %q, want 'mode'", ve.Field)
	}
}

func TestInductorDesign_CorePresetNotFound(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()

	awg := 24
	sub := inductor.ToroidParams{
		Turns: 10,
		Core:  inductor.CoreRef{Kind: "preset", ID: "T-37-NOPE"},
		Wire:  inductor.WireSpec{AWG: &awg, Material: "copper"},
	}
	subRaw, _ := json.Marshal(sub)
	body, _ := json.Marshal(inductor.Request{
		Mode: inductor.ModeToroid, FrequencyHz: 7e6, Params: subRaw,
	})
	resp, err := http.Post(hs.URL+"/api/inductor/design", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, want 400, body=%s", resp.StatusCode, raw)
	}
	var ve inductor.ValidationError
	_ = json.NewDecoder(resp.Body).Decode(&ve)
	if ve.Code != "core.not_found" {
		t.Errorf("error_code: got %q, want 'core.not_found'", ve.Code)
	}
}

func TestInductorDesign_RejectsGet(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()
	resp, err := http.Get(hs.URL + "/api/inductor/design")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

func TestInductorDesign_MalformedJSON(t *testing.T) {
	hs, cleanup := newInductorTestServer(t)
	defer cleanup()
	resp, err := http.Post(hs.URL+"/api/inductor/design", "application/json",
		bytes.NewReader([]byte(`{not json`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}
