package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"
	"circuit-designer/backend/internal/library"
)

// --- test doubles -----------------------------------------------------------

// fakeEngine is a programmable engine.Engine. produce, if set, drives the
// outgoing Frame channel for each Run. The default produce closes the channel
// immediately (zero frames, no error).
type fakeEngine struct {
	produce func(ctx context.Context, c *circuit.Circuit, a circuit.Analysis, out chan<- engine.Frame)

	mu       sync.Mutex
	runCount atomic.Int32
	closed   atomic.Bool
}

func (f *fakeEngine) Run(ctx context.Context, c *circuit.Circuit, a circuit.Analysis) (<-chan engine.Frame, error) {
	if f.closed.Load() {
		return nil, errors.New("engine: closed")
	}
	f.runCount.Add(1)
	out := make(chan engine.Frame, 16)
	go func() {
		defer close(out)
		if f.produce != nil {
			f.produce(ctx, c, a, out)
		}
	}()
	return out, nil
}

func (f *fakeEngine) Cancel(_ context.Context, _ string) error { return nil }

func (f *fakeEngine) Close() error { f.closed.Store(true); return nil }

// memSender is an in-memory Sender that forwards every envelope to a buffered
// channel, where tests can pluck them out with waitOp.
type memSender struct {
	out    chan Envelope
	closed atomic.Bool
}

func newMemSender() *memSender {
	return &memSender{out: make(chan Envelope, 256)}
}

func (m *memSender) Send(env Envelope) error {
	if m.closed.Load() {
		return errors.New("sender closed")
	}
	select {
	case m.out <- env:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("memSender: backed up")
	}
}

func (m *memSender) close() { m.closed.Store(true) }

// waitOp blocks until an envelope with op equal to want arrives, or fails the
// test on timeout. Envelopes with other ops are returned via skipped, so the
// caller can assert on intermediate frames if it cares.
func waitOp(t *testing.T, m *memSender, want string, timeout time.Duration) Envelope {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case env := <-m.out:
			if env.Op == want {
				return env
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for op=%q (have a %d-deep buffer)", want, len(m.out))
			return Envelope{}
		}
	}
}

// rawEnvelope builds an Envelope whose payload is the JSON of v. Tests pass
// the result straight to Session.Handle, mirroring the wire path.
func rawEnvelope(t *testing.T, op, id string, v any) Envelope {
	t.Helper()
	if v == nil {
		return Envelope{Op: op, ID: id}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("rawEnvelope marshal: %v", err)
	}
	return Envelope{Op: op, ID: id, Payload: raw}
}

// --- tests ------------------------------------------------------------------

// TestSessionCircuitLoadFromCircuit verifies a circuit.load with a Circuit
// payload replaces server-side state and triggers a circuit.changed reply
// carrying the same id and an emitted netlist.
func TestSessionCircuitLoadFromCircuit(t *testing.T) {
	eng := &fakeEngine{}
	send := newMemSender()
	sess := NewSession(eng, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	c := &circuit.Circuit{
		Title: "test",
		Components: []circuit.Component{
			{Ref: "R1", Kind: "resistor", Nodes: []string{"a", "0"}, Value: "1k"},
		},
	}

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpCircuitLoad, "1", CircuitLoadPayload{Circuit: c})); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	env := waitOp(t, send, OpCircuitChanged, time.Second)
	if env.ID != "1" {
		t.Errorf("reply id: got %q want %q", env.ID, "1")
	}
	var got CircuitChangedPayload
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Source != "load" {
		t.Errorf("source: got %q want load", got.Source)
	}
	if got.Circuit == nil || got.Circuit.Title != "test" {
		t.Errorf("circuit not echoed: %+v", got.Circuit)
	}
	if got.Netlist == "" {
		t.Errorf("netlist not emitted")
	}
	if sess.Circuit() == nil {
		t.Errorf("session.Circuit() still nil after load")
	}
}

// TestSessionCircuitLoadFromNetlist verifies the netlist string path: server
// parses, replaces state, replies with parsed Circuit + re-emitted netlist.
func TestSessionCircuitLoadFromNetlist(t *testing.T) {
	eng := &fakeEngine{}
	send := newMemSender()
	sess := NewSession(eng, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	src := "*divider\nR1 in mid 10k\nR2 mid 0 10k\nV1 in 0 DC 5\n.END\n"
	if err := sess.Handle(context.Background(), rawEnvelope(t, OpCircuitLoad, "x", CircuitLoadPayload{Netlist: src})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpCircuitChanged, time.Second)
	var got CircuitChangedPayload
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Circuit == nil || len(got.Circuit.Components) != 3 {
		t.Errorf("expected 3 components, got %+v", got.Circuit)
	}
	if got.Source != "load" {
		t.Errorf("source: got %q want load", got.Source)
	}
}

// TestSessionCircuitLoadEmptyPayload verifies a load with neither circuit nor
// netlist produces a structured error rather than crashing.
func TestSessionCircuitLoadEmptyPayload(t *testing.T) {
	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpCircuitLoad, "1", CircuitLoadPayload{})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpError, time.Second)
	var ep ErrorPayload
	_ = json.Unmarshal(env.Payload, &ep)
	if ep.Code != ErrCodeBadPayload {
		t.Errorf("code: got %q want %q", ep.Code, ErrCodeBadPayload)
	}
}

// TestSessionUnknownOp verifies unrecognised verbs come back as a structured
// error with code "unknown_op".
func TestSessionUnknownOp(t *testing.T) {
	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Handle(context.Background(), Envelope{Op: "wibble", ID: "1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpError, time.Second)
	var ep ErrorPayload
	_ = json.Unmarshal(env.Payload, &ep)
	if ep.Code != ErrCodeUnknownOp {
		t.Errorf("code: got %q want %q", ep.Code, ErrCodeUnknownOp)
	}
}

// TestSessionSimRunStreamsResults drives a fake engine that emits four data
// frames, then closes the channel cleanly. Expect four sim.result envelopes
// followed by a sim.done with the right count.
func TestSessionSimRunStreamsResults(t *testing.T) {
	eng := &fakeEngine{
		produce: func(ctx context.Context, _ *circuit.Circuit, _ circuit.Analysis, out chan<- engine.Frame) {
			for i := 0; i < 4; i++ {
				out <- engine.Frame{
					RunID:  "rid",
					Index:  i,
					X:      float64(i) * 1e-3,
					Values: map[string]float64{"vout": float64(i)},
				}
			}
		},
	}
	send := newMemSender()
	sess := NewSession(eng, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	loadCircuit(t, sess, send)

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpSimRun, "run-1", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "1m"}},
	})); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	results := 0
	for {
		env := waitOp2(t, send, time.Second, OpSimResult, OpSimDone, OpSimError, OpError)
		switch env.Op {
		case OpSimResult:
			if env.ID != "run-1" {
				t.Errorf("sim.result id: got %q want run-1", env.ID)
			}
			results++
		case OpSimDone:
			if env.ID != "run-1" {
				t.Errorf("sim.done id: got %q want run-1", env.ID)
			}
			var dp SimDonePayload
			_ = json.Unmarshal(env.Payload, &dp)
			if dp.FrameCount != 4 {
				t.Errorf("frame_count: got %d want 4", dp.FrameCount)
			}
			if results != 4 {
				t.Errorf("got %d sim.result envelopes, want 4", results)
			}
			return
		default:
			t.Fatalf("unexpected op %q while streaming", env.Op)
		}
	}
}

// TestSessionSimRunReportsEngineError verifies a terminal error frame from
// the engine is reported as sim.error with the structured RunError preserved.
func TestSessionSimRunReportsEngineError(t *testing.T) {
	wantErr := &engine.RunError{
		Kind:        "no_convergence",
		Node:        "plate",
		LastVoltage: 187.3,
		Iteration:   200,
		Message:     "no convergence at node plate",
		Hint:        "try .options gmin=1e-11",
	}
	eng := &fakeEngine{
		produce: func(_ context.Context, _ *circuit.Circuit, _ circuit.Analysis, out chan<- engine.Frame) {
			out <- engine.Frame{Err: wantErr}
		},
	}
	send := newMemSender()
	sess := NewSession(eng, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	loadCircuit(t, sess, send)

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpSimRun, "run-x", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "1m"}},
	})); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	env := waitOp(t, send, OpSimError, 2*time.Second)
	if env.ID != "run-x" {
		t.Errorf("sim.error id: got %q want run-x", env.ID)
	}
	var ep SimErrorPayload
	if err := json.Unmarshal(env.Payload, &ep); err != nil {
		t.Fatalf("decode sim.error payload: %v", err)
	}
	if ep.Error == nil || ep.Error.Kind != "no_convergence" || ep.Error.Node != "plate" {
		t.Errorf("structured error not preserved: %+v", ep.Error)
	}
}

// TestSessionSimRunRequiresCircuit verifies sim.run before circuit.load is
// rejected with code no_circuit.
func TestSessionSimRunRequiresCircuit(t *testing.T) {
	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpSimRun, "1", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "1m"}},
	})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpError, time.Second)
	var ep ErrorPayload
	_ = json.Unmarshal(env.Payload, &ep)
	if ep.Code != ErrCodeNoCircuit {
		t.Errorf("code: got %q want %q", ep.Code, ErrCodeNoCircuit)
	}
}

// TestSessionSimCancelStopsRun starts a fake engine that emits one frame then
// blocks until ctx is done. After cancelling via sim.cancel the engine ctx
// should fire and the channel should close — yielding sim.done with a small
// frame_count and the run should be removed from the map.
func TestSessionSimCancelStopsRun(t *testing.T) {
	eng := &fakeEngine{
		produce: func(ctx context.Context, _ *circuit.Circuit, _ circuit.Analysis, out chan<- engine.Frame) {
			out <- engine.Frame{Index: 0, X: 0}
			<-ctx.Done()
		},
	}
	send := newMemSender()
	sess := NewSession(eng, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	loadCircuit(t, sess, send)

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpSimRun, "run-c", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "1m"}},
	})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// Ensure the first sim.result has landed before we cancel.
	waitOp(t, send, OpSimResult, time.Second)

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpSimCancel, "cancel-1", SimCancelPayload{RunID: "run-c"})); err != nil {
		t.Fatalf("Handle cancel: %v", err)
	}
	waitOp(t, send, OpAck, time.Second)
	waitOp(t, send, OpSimDone, time.Second)
}

// TestSessionSimCancelUnknownRun verifies cancelling an unknown run reports
// a structured error with code unknown_run.
func TestSessionSimCancelUnknownRun(t *testing.T) {
	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpSimCancel, "1", SimCancelPayload{RunID: "ghost"})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpError, time.Second)
	var ep ErrorPayload
	_ = json.Unmarshal(env.Payload, &ep)
	if ep.Code != ErrCodeUnknownRun {
		t.Errorf("code: got %q want %q", ep.Code, ErrCodeUnknownRun)
	}
}

// TestSessionLibraryListReturnsStub verifies the stub library is reachable
// through library.list and returns the documented primitives.
func TestSessionLibraryListReturnsStub(t *testing.T) {
	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpLibraryList, "1", LibraryListPayload{})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpLibraryList, time.Second)
	var p LibraryListPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Components) == 0 {
		t.Errorf("expected stub primitives; got none")
	}
	// Spot-check that core primitives are listed.
	have := map[string]bool{}
	for _, c := range p.Components {
		have[c.Kind] = true
	}
	for _, want := range []string{"resistor", "capacitor", "voltage_source"} {
		if !have[want] {
			t.Errorf("missing primitive %q", want)
		}
	}
}

// TestSessionLibraryImportRoundTrip verifies that the loaded LibraryProvider
// (the production wiring) accepts a .lib body, returns the discovered
// subcircuits in the response payload, and that a follow-up library.list
// reflects them — i.e. the snapshot is reloaded synchronously.
func TestSessionLibraryImportRoundTrip(t *testing.T) {
	root := t.TempDir()
	libDir := t.TempDir()
	loader := library.NewLoader(root, libDir)
	if err := loader.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewLoadedLibrary(loader), send)
	t.Cleanup(func() { _ = sess.Close() })

	body := ".SUBCKT 12AX7 P G K\n.ENDS 12AX7\n"
	if err := sess.Handle(context.Background(), rawEnvelope(t, OpLibraryImport, "1", LibraryImportPayload{
		Filename: "tubes_koren.lib", Body: body,
	})); err != nil {
		t.Fatalf("Handle import: %v", err)
	}
	env := waitOp(t, send, OpLibraryImport, time.Second)
	var rp LibraryImportResultPayload
	if err := json.Unmarshal(env.Payload, &rp); err != nil {
		t.Fatalf("decode import reply: %v", err)
	}
	if rp.LibFile != "tubes_koren.lib" {
		t.Errorf("LibFile: got %q want tubes_koren.lib", rp.LibFile)
	}
	if len(rp.Imported) != 1 || rp.Imported[0].ModelName != "12AX7" {
		t.Errorf("Imported: got %+v want one 12AX7 entry", rp.Imported)
	}
	if rp.Imported[0].Group != "Tubes" {
		t.Errorf("Group: got %q want Tubes", rp.Imported[0].Group)
	}

	// Follow-up library.list sees the new component.
	if err := sess.Handle(context.Background(), rawEnvelope(t, OpLibraryList, "2", LibraryListPayload{})); err != nil {
		t.Fatalf("Handle list: %v", err)
	}
	env = waitOp(t, send, OpLibraryList, time.Second)
	var lp LibraryListPayload
	if err := json.Unmarshal(env.Payload, &lp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, c := range lp.Components {
		if c.ModelName == "12AX7" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("library.list after import did not list 12AX7")
	}
}

// TestSessionLibraryImportStubRejects verifies the stub LibraryProvider
// (used when the on-disk library cannot be located) reports a structured
// error rather than silently accepting an import it cannot persist.
func TestSessionLibraryImportStubRejects(t *testing.T) {
	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	if err := sess.Handle(context.Background(), rawEnvelope(t, OpLibraryImport, "1", LibraryImportPayload{
		Filename: "tubes.lib", Body: ".SUBCKT foo a b\n.ENDS\n",
	})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpError, time.Second)
	var ep ErrorPayload
	_ = json.Unmarshal(env.Payload, &ep)
	if ep.Code != ErrCodeBadPayload {
		t.Errorf("code: got %q want %q", ep.Code, ErrCodeBadPayload)
	}
}

// TestSessionNetlistEmit verifies the helper that re-emits SPICE source for
// a client-supplied Circuit (Netlist tab "regenerate" flow).
func TestSessionNetlistEmit(t *testing.T) {
	send := newMemSender()
	sess := NewSession(&fakeEngine{}, NewStubLibrary(), send)
	t.Cleanup(func() { _ = sess.Close() })

	c := &circuit.Circuit{
		Title: "emit-test",
		Components: []circuit.Component{
			{Ref: "R1", Kind: "resistor", Nodes: []string{"a", "0"}, Value: "1k"},
		},
	}
	if err := sess.Handle(context.Background(), rawEnvelope(t, OpNetlistEmit, "1", NetlistEmitPayload{Circuit: c})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	env := waitOp(t, send, OpNetlistEmit, time.Second)
	var p NetlistEmitResultPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Netlist == "" {
		t.Error("expected non-empty netlist")
	}
}

// TestSessionCloseCancelsActiveRun verifies Session.Close terminates an
// in-flight run promptly. The fake engine blocks on ctx; Close must cancel
// the run context so the goroutine exits.
func TestSessionCloseCancelsActiveRun(t *testing.T) {
	eng := &fakeEngine{
		produce: func(ctx context.Context, _ *circuit.Circuit, _ circuit.Analysis, out chan<- engine.Frame) {
			out <- engine.Frame{}
			<-ctx.Done()
		},
	}
	send := newMemSender()
	sess := NewSession(eng, NewStubLibrary(), send)

	loadCircuit(t, sess, send)
	if err := sess.Handle(context.Background(), rawEnvelope(t, OpSimRun, "run-z", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "1m"}},
	})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	waitOp(t, send, OpSimResult, time.Second)

	done := make(chan struct{})
	go func() {
		_ = sess.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Session.Close did not return promptly")
	}
}

// --- helpers ----------------------------------------------------------------

// loadCircuit pushes a tiny circuit + probe through circuit.load and waits
// for the ack. Used by sim.run tests so they have a circuit to run against.
func loadCircuit(t *testing.T, sess *Session, send *memSender) {
	t.Helper()
	c := &circuit.Circuit{
		Title:  "fake",
		Probes: []circuit.Probe{{Name: "vout", Node: "vout", Kind: "voltage"}},
		Components: []circuit.Component{
			{Ref: "R1", Kind: "resistor", Nodes: []string{"vout", "0"}, Value: "1k"},
		},
	}
	if err := sess.Handle(context.Background(), rawEnvelope(t, OpCircuitLoad, "load-0", CircuitLoadPayload{Circuit: c})); err != nil {
		t.Fatalf("load: %v", err)
	}
	waitOp(t, send, OpCircuitChanged, time.Second)
}

// waitOp2 returns the next envelope whose op matches any of want. Useful for
// streams where multiple op types interleave.
func waitOp2(t *testing.T, m *memSender, timeout time.Duration, want ...string) Envelope {
	t.Helper()
	allowed := map[string]bool{}
	for _, w := range want {
		allowed[w] = true
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case env := <-m.out:
			if allowed[env.Op] {
				return env
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for one of %v", want)
			return Envelope{}
		}
	}
}
