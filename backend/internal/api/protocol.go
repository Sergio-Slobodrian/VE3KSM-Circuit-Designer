package api

import (
	"encoding/json"
	"fmt"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"
)

// Op identifiers for the JSON-over-WebSocket protocol (DESIGN.md §11).
//
// Client → server verbs are listed under "Inbound"; server-pushed verbs under
// "Outbound". Server replies to a client request reuse the corresponding
// outbound op and echo the request's id, so a client can correlate replies
// without parsing op names.
const (
	// Inbound — client → server.
	OpCircuitLoad    = "circuit.load"
	OpCircuitUpdate  = "circuit.update"
	OpSimRun         = "sim.run"
	OpSimCancel      = "sim.cancel"
	OpLibraryList    = "library.list"
	OpLibraryImport  = "library.import"
	OpNetlistEmit    = "netlist.emit"

	// Outbound — server → client.
	OpCircuitChanged = "circuit.changed"
	OpSimResult      = "sim.result"
	OpSimError       = "sim.error"
	OpSimDone        = "sim.done"
	OpAck            = "ack"
	OpError          = "error"
)

// Envelope is the wire frame from DESIGN.md §11:
//
//	{ "op": "<verb>", "id": "<correlation-id>", "payload": { ... } }
//
// Payload is left as RawMessage so dispatch can defer typed decoding to the
// op-specific handler. Empty payloads are encoded as the JSON null literal
// rather than omitted, so the schema is uniform.
type Envelope struct {
	Op      string          `json:"op"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// newEnvelope marshals payload and builds an Envelope. If payload is nil the
// Payload field is left empty.
func newEnvelope(op, id string, payload any) (Envelope, error) {
	env := Envelope{Op: op, ID: id}
	if payload == nil {
		return env, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return env, fmt.Errorf("marshal %s payload: %w", op, err)
	}
	env.Payload = raw
	return env, nil
}

// CircuitLoadPayload is the body of an OpCircuitLoad request. Exactly one of
// Circuit or Netlist must be set; Netlist is parsed server-side into a
// circuit.Circuit.
type CircuitLoadPayload struct {
	Circuit *circuit.Circuit `json:"circuit,omitempty"`
	Netlist string           `json:"netlist,omitempty"`
}

// CircuitUpdatePayload is identical in shape to CircuitLoadPayload — the
// distinction between load and update is semantic (DESIGN.md §11 lists both
// for forward compatibility with diff-based updates), and milestone 3
// implements both as full replace.
type CircuitUpdatePayload = CircuitLoadPayload

// CircuitChangedPayload is the server's notification that the canonical
// server-side Circuit has changed. The full circuit is included so the client
// does not need to round-trip a separate fetch.
type CircuitChangedPayload struct {
	Circuit *circuit.Circuit `json:"circuit"`
	// Netlist is the freshly-emitted SPICE source matching Circuit. Included
	// so the Netlist tab can render without an extra request.
	Netlist string `json:"netlist"`
	// Source identifies what triggered the change: "load", "update", or in
	// future milestones "netlist_edit", "schematic_edit".
	Source string `json:"source"`
}

// SimRunPayload starts an analysis. Analysis is the same circuit.Analysis the
// engine consumes; we accept it directly rather than re-modelling it here.
type SimRunPayload struct {
	Analysis circuit.Analysis `json:"analysis"`
}

// SimCancelPayload cancels an in-flight run. RunID must match the id of the
// originating sim.run envelope; the server tracks runs by that id.
type SimCancelPayload struct {
	RunID string `json:"run_id"`
}

// SimResultPayload wraps one streaming Frame from the engine. We pass the
// Frame through verbatim — its JSON tags are the wire contract.
type SimResultPayload struct {
	Frame engine.Frame `json:"frame"`
}

// SimErrorPayload reports a structured run failure (DESIGN.md §9.4). RunID is
// the originating sim.run id so the client can dispatch the error to the right
// view; Error is the structured RunError.
type SimErrorPayload struct {
	RunID string           `json:"run_id"`
	Error *engine.RunError `json:"error"`
}

// SimDonePayload announces the end of a streaming run. FrameCount is included
// for convenience; views that already counted frames can ignore it.
type SimDonePayload struct {
	RunID      string `json:"run_id"`
	FrameCount int    `json:"frame_count"`
}

// LibraryListPayload is sent both as a request (empty body) and as a response
// (Components populated). Filter is unused in milestone 3 but reserved.
type LibraryListPayload struct {
	Filter     string             `json:"filter,omitempty"`
	Components []LibraryComponent `json:"components,omitempty"`
}

// LibraryImportPayload submits a SPICE .lib file body for ingestion. The
// milestone-9 implementation will parse .SUBCKT statements; milestone 3 only
// validates that the request is well-formed and returns a stub.
type LibraryImportPayload struct {
	Filename string `json:"filename"`
	Body     string `json:"body"`
}

// LibraryComponent is one entry in a library listing. The shape is the
// minimum the frontend palette needs (DESIGN.md §8); milestone 9 will extend
// it with model parameters and footprint hints.
type LibraryComponent struct {
	Kind        string `json:"kind"`
	Symbol      string `json:"symbol"`
	Description string `json:"description,omitempty"`
}

// ExamplesListPayload is the response body of GET /api/examples. The field is
// always set (possibly to an empty slice) so the frontend never needs a nil
// check.
type ExamplesListPayload struct {
	Examples []ExampleSummary `json:"examples"`
}

// NetlistEmitPayload requests the server emit netlist source for the given
// circuit. Useful for the Netlist tab's "regenerate from schematic" flow
// without disturbing server-side state.
type NetlistEmitPayload struct {
	Circuit *circuit.Circuit `json:"circuit"`
}

// NetlistEmitResultPayload returns the emitted source.
type NetlistEmitResultPayload struct {
	Netlist string `json:"netlist"`
}

// AckPayload acknowledges a request that has no other natural reply
// (e.g. sim.cancel). Some clients ignore it; it exists so every request has a
// reply with a matching id, simplifying client request/response bookkeeping.
type AckPayload struct {
	Of string `json:"of"` // op of the originating request
}

// ErrorPayload reports a non-simulation protocol error: malformed JSON, an
// unknown op, a missing required field, an out-of-state cancel. Code is a
// stable machine-readable identifier; Message is a human-readable description.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Op      string `json:"op,omitempty"` // op of the offending request, if known
}

// Error code identifiers used in ErrorPayload.Code. Stable strings — clients
// switch on them.
const (
	ErrCodeBadJSON       = "bad_json"
	ErrCodeBadPayload    = "bad_payload"
	ErrCodeUnknownOp     = "unknown_op"
	ErrCodeNoCircuit     = "no_circuit"
	ErrCodeUnknownRun    = "unknown_run"
	ErrCodeInternal      = "internal"
	ErrCodeNotImplemented = "not_implemented"
)
