package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"

	"github.com/gorilla/websocket"
)

// quietLogger keeps the expected ws-disconnect chatter out of test output —
// every test in this file induces some manner of close, and the noise drowns
// real failures.
func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// newTestServer wires Server.Routes onto an httptest.Server, returning the
// server, the engine the test can reach into, and a teardown.
func newTestServer(t *testing.T, eng engine.Engine) (*httptest.Server, func()) {
	t.Helper()
	srv := New(eng, Options{Logger: quietLogger()})
	hs := httptest.NewServer(srv.Routes())
	return hs, func() {
		hs.Close()
		_ = srv.Close()
	}
}

// dialWS upgrades a WebSocket against hs at path /ws.
func dialWS(t *testing.T, hs *httptest.Server) *websocket.Conn {
	t.Helper()
	u, err := url.Parse(hs.URL + "/ws")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Scheme = "ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	return conn
}

// sendJSON marshals env and writes one text frame.
func sendJSON(t *testing.T, c *websocket.Conn, env Envelope) {
	t.Helper()
	if err := c.WriteJSON(env); err != nil {
		t.Fatalf("write %s: %v", env.Op, err)
	}
}

// readUntil consumes envelopes until one with op equal to want arrives, or
// the test deadline fires.
func readUntil(t *testing.T, c *websocket.Conn, want string, timeout time.Duration) Envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	for {
		var env Envelope
		if err := c.ReadJSON(&env); err != nil {
			t.Fatalf("read while waiting for %q: %v", want, err)
		}
		if env.Op == want {
			return env
		}
	}
}

// TestHTTPHealthz verifies /api/healthz returns the documented JSON shape.
func TestHTTPHealthz(t *testing.T) {
	hs, cleanup := newTestServer(t, &fakeEngine{})
	defer cleanup()

	resp, err := http.Get(hs.URL + "/api/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: got %d want 200", resp.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("status: %v", body)
	}
}

// TestHTTPLibrary verifies the REST equivalent of library.list returns the
// stub primitives.
func TestHTTPLibrary(t *testing.T) {
	hs, cleanup := newTestServer(t, &fakeEngine{})
	defer cleanup()

	resp, err := http.Get(hs.URL + "/api/library")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	var p LibraryListPayload
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Components) == 0 {
		t.Errorf("expected components")
	}
}

// TestHTTPCircuitParseEmit verifies the REST helpers round-trip SPICE source
// through the parser and emitter without disturbing server-side state.
func TestHTTPCircuitParseEmit(t *testing.T) {
	hs, cleanup := newTestServer(t, &fakeEngine{})
	defer cleanup()

	src := "*divider\nR1 in mid 10k\nR2 mid 0 10k\nV1 in 0 DC 5\n.END\n"

	// Parse → Circuit
	resp, err := http.Post(hs.URL+"/api/circuit/parse", "text/plain", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("parse status %d: %s", resp.StatusCode, body)
	}
	var c circuit.Circuit
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("decode parsed: %v", err)
	}
	if len(c.Components) != 3 {
		t.Errorf("want 3 components, got %d", len(c.Components))
	}

	// Emit → SPICE source
	body, _ := json.Marshal(c)
	resp2, err := http.Post(hs.URL+"/api/circuit/emit", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("emit post: %v", err)
	}
	defer resp2.Body.Close()
	emitted, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(emitted), "R1") || !strings.Contains(string(emitted), "R2") {
		t.Errorf("emitted source missing components: %s", emitted)
	}
}

// TestHTTPExamplesListAndLoad verifies the milestone-4 catalog and load
// endpoints round-trip a small on-disk fixture into a parsed Circuit.
func TestHTTPExamplesListAndLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tiny.cir"),
		[]byte("* tiny test\nR1 a 0 1k\nV1 a 0 DC 5\n.SAVE V(a)\n.END\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	srv := New(&fakeEngine{}, Options{
		Logger:   quietLogger(),
		Examples: NewDirExamples(dir),
	})
	hs := httptest.NewServer(srv.Routes())
	defer hs.Close()
	defer srv.Close()

	// List
	resp, err := http.Get(hs.URL + "/api/examples")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	var list ExamplesListPayload
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Examples) != 1 || list.Examples[0].Name != "tiny" {
		t.Fatalf("unexpected list: %+v", list)
	}
	if list.Examples[0].Title == "" {
		t.Errorf("expected non-empty title for catalog entry")
	}

	// Load
	resp2, err := http.Get(hs.URL + "/api/examples/tiny")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("load status: %d", resp2.StatusCode)
	}
	var c circuit.Circuit
	if err := json.NewDecoder(resp2.Body).Decode(&c); err != nil {
		t.Fatalf("decode load: %v", err)
	}
	if len(c.Components) != 2 {
		t.Errorf("want 2 components, got %d", len(c.Components))
	}

	// Missing
	resp3, err := http.Get(hs.URL + "/api/examples/does-not-exist")
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 404 {
		t.Errorf("missing status: got %d want 404", resp3.StatusCode)
	}

	// Path traversal must be rejected as 404, not as a leak.
	resp4, err := http.Get(hs.URL + "/api/examples/..%2Fsecret")
	if err != nil {
		t.Fatalf("traversal: %v", err)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != 404 {
		t.Errorf("traversal status: got %d want 404", resp4.StatusCode)
	}
}

// TestHTTPExamplesDisabled verifies the empty-list fallback when no
// ExamplesProvider is configured.
func TestHTTPExamplesDisabled(t *testing.T) {
	hs, cleanup := newTestServer(t, &fakeEngine{})
	defer cleanup()

	resp, err := http.Get(hs.URL + "/api/examples")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	var list ExamplesListPayload
	_ = json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Examples) != 0 {
		t.Errorf("expected empty list, got %+v", list)
	}

	resp2, err := http.Get(hs.URL + "/api/examples/anything")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Errorf("load status: got %d want 404", resp2.StatusCode)
	}
}

// TestHTTPCircuitParseRejectsBadInput verifies bad SPICE source surfaces a
// 400 with a structured ErrorPayload, not a 500.
func TestHTTPCircuitParseRejectsBadInput(t *testing.T) {
	hs, cleanup := newTestServer(t, &fakeEngine{})
	defer cleanup()

	// Q-prefix (BJT) is an unsupported component in milestone 1 → returns
	// ErrUnsupported, which the handler maps to 400.
	resp, err := http.Post(hs.URL+"/api/circuit/parse", "text/plain", strings.NewReader("*bad\nQ1 c b e mymodel\n.END\n"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status: got %d want 400", resp.StatusCode)
	}
}

// TestWSCircuitLoadAndStreaming exercises the milestone-3 acceptance path
// through a real WebSocket: connect, send circuit.load (netlist form), wait
// for circuit.changed, then send sim.run against a fake engine that emits
// three frames + done. Verifies everything round-trips through the wire.
func TestWSCircuitLoadAndStreaming(t *testing.T) {
	eng := &fakeEngine{
		produce: func(_ context.Context, _ *circuit.Circuit, _ circuit.Analysis, out chan<- engine.Frame) {
			for i := 0; i < 3; i++ {
				out <- engine.Frame{
					Index: i, X: float64(i) * 1e-3,
					Values: map[string]float64{"vout": float64(i) * 1.5},
				}
			}
		},
	}
	hs, cleanup := newTestServer(t, eng)
	defer cleanup()

	c := dialWS(t, hs)
	defer c.Close()

	// 1. Load
	sendJSON(t, c, mustEnvelope(OpCircuitLoad, "load-1", CircuitLoadPayload{
		Netlist: "*ws-test\nR1 vout 0 1k\nV1 vin 0 DC 1\n.SAVE V(vout)\n.END\n",
	}))
	chg := readUntil(t, c, OpCircuitChanged, 3*time.Second)
	var chgP CircuitChangedPayload
	if err := json.Unmarshal(chg.Payload, &chgP); err != nil {
		t.Fatalf("decode circuit.changed: %v", err)
	}
	if chgP.Circuit == nil || len(chgP.Circuit.Probes) != 1 {
		t.Errorf("expected one probe, got %+v", chgP.Circuit)
	}

	// 2. Run
	sendJSON(t, c, mustEnvelope(OpSimRun, "run-1", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "3m"}},
	}))

	// 3. Drain results until sim.done.
	gotResults := 0
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var env Envelope
		if err := c.ReadJSON(&env); err != nil {
			t.Fatalf("read: %v", err)
		}
		switch env.Op {
		case OpSimResult:
			gotResults++
		case OpSimDone:
			if gotResults != 3 {
				t.Errorf("want 3 sim.result, got %d", gotResults)
			}
			return
		case OpSimError, OpError:
			t.Fatalf("unexpected error envelope: %s", env.Payload)
		default:
			t.Fatalf("unexpected op: %s", env.Op)
		}
	}
}

// TestWSBadJSONIsReportedNotFatal verifies the read loop doesn't drop the
// connection on a malformed inbound payload — clients can recover.
func TestWSBadJSONIsReportedNotFatal(t *testing.T) {
	hs, cleanup := newTestServer(t, &fakeEngine{})
	defer cleanup()

	c := dialWS(t, hs)
	defer c.Close()

	if err := c.WriteMessage(websocket.TextMessage, []byte("not json {{{")); err != nil {
		t.Fatalf("write: %v", err)
	}
	env := readUntil(t, c, OpError, 2*time.Second)
	var ep ErrorPayload
	_ = json.Unmarshal(env.Payload, &ep)
	if ep.Code != ErrCodeBadJSON {
		t.Errorf("code: got %q want %q", ep.Code, ErrCodeBadJSON)
	}

	// Connection should still be usable: a follow-up library.list must work.
	sendJSON(t, c, mustEnvelope(OpLibraryList, "1", LibraryListPayload{}))
	readUntil(t, c, OpLibraryList, 2*time.Second)
}

// TestServerCloseStopsActiveSessions verifies Server.Close brings down
// connected sessions promptly. We dial, leave the conn idle, then Close and
// expect the next read to fail.
func TestServerCloseStopsActiveSessions(t *testing.T) {
	srv := New(&fakeEngine{}, Options{})
	hs := httptest.NewServer(srv.Routes())
	defer hs.Close()

	c := dialWS(t, hs)
	defer c.Close()

	closeDone := make(chan struct{})
	go func() {
		_ = srv.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Server.Close hung")
	}

	// The conn should observe a close-frame or read failure shortly.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := c.ReadMessage()
	if err == nil {
		t.Errorf("expected read error after server close")
	}
}

// TestWSConcurrentStreamingDoesNotInterleave verifies the per-conn write
// serialiser keeps individual envelopes intact when multiple runs would race.
// We run two sims in parallel with overlapping output windows; each envelope
// arriving must decode cleanly (no torn frames).
func TestWSConcurrentStreamingDoesNotInterleave(t *testing.T) {
	eng := &fakeEngine{
		produce: func(_ context.Context, _ *circuit.Circuit, _ circuit.Analysis, out chan<- engine.Frame) {
			for i := 0; i < 8; i++ {
				out <- engine.Frame{Index: i, X: float64(i)}
				time.Sleep(time.Millisecond)
			}
		},
	}
	hs, cleanup := newTestServer(t, eng)
	defer cleanup()

	c := dialWS(t, hs)
	defer c.Close()

	sendJSON(t, c, mustEnvelope(OpCircuitLoad, "L", CircuitLoadPayload{
		Circuit: &circuit.Circuit{Probes: []circuit.Probe{{Name: "v", Node: "v"}}},
	}))
	readUntil(t, c, OpCircuitChanged, 2*time.Second)

	sendJSON(t, c, mustEnvelope(OpSimRun, "A", SimRunPayload{Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "1m"}}}))
	sendJSON(t, c, mustEnvelope(OpSimRun, "B", SimRunPayload{Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "1m"}}}))

	doneA, doneB := false, false
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !(doneA && doneB) {
		var env Envelope
		if err := c.ReadJSON(&env); err != nil {
			t.Fatalf("read: %v", err)
		}
		if env.Op == OpSimDone {
			switch env.ID {
			case "A":
				doneA = true
			case "B":
				doneB = true
			}
		}
	}
}

