package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"
)

// TestEnvelopeRoundTrip verifies the wire envelope marshals to the shape
// DESIGN.md §11 specifies: top-level "op", "id", "payload", with payload
// preserved verbatim.
func TestEnvelopeRoundTrip(t *testing.T) {
	payload := map[string]any{"hello": "world", "n": 42.0}
	env, err := newEnvelope(OpSimRun, "abc", payload)
	if err != nil {
		t.Fatalf("newEnvelope: %v", err)
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["op"] != OpSimRun {
		t.Errorf("op: got %v want %v", got["op"], OpSimRun)
	}
	if got["id"] != "abc" {
		t.Errorf("id: got %v want abc", got["id"])
	}
	pl, ok := got["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload: want object, got %T", got["payload"])
	}
	if !reflect.DeepEqual(pl, payload) {
		t.Errorf("payload: got %v want %v", pl, payload)
	}
}

// TestEnvelopeNilPayload verifies that envelopes with no payload omit the
// field entirely (omitempty on RawMessage), keeping the wire form compact.
func TestEnvelopeNilPayload(t *testing.T) {
	env, err := newEnvelope(OpAck, "1", nil)
	if err != nil {
		t.Fatalf("newEnvelope: %v", err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, has := got["payload"]; has {
		t.Errorf("expected no payload field; got %s", string(data))
	}
}

// TestSimRunPayloadRoundTrip ensures the analysis pass-through preserves
// kind, args, options and enabled — these flow straight to the engine.
func TestSimRunPayloadRoundTrip(t *testing.T) {
	in := SimRunPayload{
		Analysis: circuit.Analysis{
			Kind:    "tran",
			Args:    []string{"1u", "5m", "uic"},
			Enabled: true,
			Options: map[string]string{"reltol": "1e-3"},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SimRunPayload
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip diverged:\n in: %+v\nout: %+v", in, out)
	}
}

// TestSimResultPayloadCarriesFrame ensures the engine.Frame embedded in
// sim.result keeps its keys at the documented JSON names — the frontend
// scope renderer indexes Values by the user-facing probe key.
func TestSimResultPayloadCarriesFrame(t *testing.T) {
	in := SimResultPayload{
		Frame: engine.Frame{
			RunID:  "r1",
			Index:  3,
			X:      1.5e-3,
			Values: map[string]float64{"vout": 12.5, "vin": 0.25},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	frame, ok := got["frame"].(map[string]any)
	if !ok {
		t.Fatalf("frame field missing or wrong type: %v", got)
	}
	if frame["run_id"] != "r1" || frame["index"].(float64) != 3 {
		t.Errorf("frame metadata wrong: %v", frame)
	}
	values, ok := frame["values"].(map[string]any)
	if !ok {
		t.Fatalf("values not an object: %v", frame["values"])
	}
	if values["vout"].(float64) != 12.5 || values["vin"].(float64) != 0.25 {
		t.Errorf("values lost on round trip: %v", values)
	}
}
