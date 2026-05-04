package api

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"
	"circuit-designer/backend/internal/netlist"

	"github.com/gorilla/websocket"
)

// TestE2EPreampOverWebSocket is the milestone-3 acceptance test from
// DESIGN.md §13: load examples/preamp_12ax7.cir over the wire, run a .tran
// against the real ngspice subprocess, and assert that streaming sim.result
// frames arrive followed by sim.done. Skipped when ngspice is not installed.
func TestE2EPreampOverWebSocket(t *testing.T) {
	if _, err := exec.LookPath("ngspice"); err != nil {
		t.Skip("ngspice not on PATH; skipping milestone-3 e2e smoke test")
	}

	// The fixture references tubes_koren.lib by relative path; the engine
	// resolves it against WorkDir. We point WorkDir at the examples dir for
	// the same reason ngspice_test.go does.
	examples, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatalf("resolve examples dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(examples, "preamp_12ax7.cir")); err != nil {
		t.Fatalf("fixture missing at %s: %v", examples, err)
	}

	src, err := os.ReadFile(filepath.Join(examples, "preamp_12ax7.cir"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// We parse client-side and send the structured Circuit so the test goes
	// through the full code path the schematic editor will use; the netlist
	// form is exercised by TestWSCircuitLoadAndStreaming with a synthetic
	// circuit.
	c, err := netlist.Parse(strings.NewReader(string(src)))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	eng := engine.NewWithOptions(engine.Options{WorkDir: examples})
	t.Cleanup(func() { _ = eng.Close() })

	srv := New(eng, Options{Logger: quietLogger()})
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { hs.Close(); _ = srv.Close() })

	u, _ := url.Parse(hs.URL + "/ws")
	u.Scheme = "ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 1. Load the parsed circuit.
	if err := conn.WriteJSON(mustEnvelope(OpCircuitLoad, "load-1", CircuitLoadPayload{Circuit: c})); err != nil {
		t.Fatalf("write circuit.load: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	chg := readUntilOp(t, conn, OpCircuitChanged)
	var chgP CircuitChangedPayload
	if err := json.Unmarshal(chg.Payload, &chgP); err != nil {
		t.Fatalf("decode circuit.changed: %v", err)
	}
	if chgP.Circuit == nil || len(chgP.Circuit.Probes) == 0 {
		t.Fatalf("loaded circuit has no probes; cannot stream: %+v", chgP.Circuit)
	}

	// 2. Kick off a 5 ms .tran with uic.
	if err := conn.WriteJSON(mustEnvelope(OpSimRun, "run-1", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "tran", Args: []string{"1u", "5m", "uic"}},
	})); err != nil {
		t.Fatalf("write sim.run: %v", err)
	}

	// 3. Drain results.
	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	var (
		count       int
		peakAbsVout float64
		seenDone    bool
	)
	for !seenDone {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatalf("read: %v", err)
		}
		switch env.Op {
		case OpSimResult:
			var p SimResultPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				t.Fatalf("decode sim.result: %v", err)
			}
			count++
			if v, ok := p.Frame.Values["vout"]; ok {
				if a := math.Abs(v); a > peakAbsVout {
					peakAbsVout = a
				}
			}
		case OpSimDone:
			var p SimDonePayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				t.Fatalf("decode sim.done: %v", err)
			}
			if p.RunID != "run-1" {
				t.Errorf("sim.done run_id: got %q want run-1", p.RunID)
			}
			if p.FrameCount != count {
				t.Errorf("sim.done frame_count %d != observed %d", p.FrameCount, count)
			}
			seenDone = true
		case OpSimError, OpError:
			t.Fatalf("unexpected error envelope: %s", env.Payload)
		default:
			t.Fatalf("unexpected op %q", env.Op)
		}
	}

	// Sanity bounds — same shape as the engine-level acceptance test, but
	// here we are asserting the wire-level contract is intact end-to-end.
	if count < 100 {
		t.Errorf("expected many frames over 5 ms TRAN; got %d", count)
	}
	if peakAbsVout < 1.0 {
		t.Errorf("|peak vout| too small over the wire: %g", peakAbsVout)
	}
	t.Logf("e2e: frames=%d peak|vout|=%.2f V", count, peakAbsVout)
}

// TestE2EPreampACOverWebSocket is the milestone-6 acceptance test for the
// AC analysis path over the wire. Same shape as TestE2EPreampOverWebSocket
// but issues an `ac` analysis and asserts that frames carry the expected
// per-probe `mag_db` + `phase_deg` keys.
func TestE2EPreampACOverWebSocket(t *testing.T) {
	if _, err := exec.LookPath("ngspice"); err != nil {
		t.Skip("ngspice not on PATH; skipping milestone-6 e2e smoke test")
	}
	examples, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatalf("resolve examples dir: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(examples, "preamp_12ax7.cir"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	c, err := netlist.Parse(strings.NewReader(string(src)))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	eng := engine.NewWithOptions(engine.Options{WorkDir: examples})
	t.Cleanup(func() { _ = eng.Close() })
	srv := New(eng, Options{Logger: quietLogger()})
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { hs.Close(); _ = srv.Close() })

	u, _ := url.Parse(hs.URL + "/ws")
	u.Scheme = "ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(mustEnvelope(OpCircuitLoad, "load-ac", CircuitLoadPayload{Circuit: c})); err != nil {
		t.Fatalf("write circuit.load: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readUntilOp(t, conn, OpCircuitChanged)

	if err := conn.WriteJSON(mustEnvelope(OpSimRun, "run-ac", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "ac", Args: []string{"dec", "20", "10", "100k"}},
	})); err != nil {
		t.Fatalf("write sim.run: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	var (
		count    int
		seenDone bool
		sawMag   bool
		sawPhase bool
	)
	for !seenDone {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatalf("read: %v", err)
		}
		switch env.Op {
		case OpSimResult:
			var p SimResultPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				t.Fatalf("decode sim.result: %v", err)
			}
			count++
			if _, ok := p.Frame.Values["vout:mag_db"]; ok {
				sawMag = true
			}
			if _, ok := p.Frame.Values["vout:phase_deg"]; ok {
				sawPhase = true
			}
		case OpSimDone:
			seenDone = true
		case OpSimError, OpError:
			t.Fatalf("unexpected error envelope: %s", env.Payload)
		default:
			t.Fatalf("unexpected op %q", env.Op)
		}
	}
	if count < 50 {
		t.Errorf("expected many AC sweep frames; got %d", count)
	}
	if !sawMag || !sawPhase {
		t.Errorf("AC frames missing complex keys: mag=%v phase=%v", sawMag, sawPhase)
	}
	t.Logf("e2e ac: frames=%d", count)
}

// TestE2EPreampSpectrumOverWebSocket exercises the milestone-6 spectrum path
// (tran → linearize → fft → wrdata) end-to-end through the WS API.
func TestE2EPreampSpectrumOverWebSocket(t *testing.T) {
	if _, err := exec.LookPath("ngspice"); err != nil {
		t.Skip("ngspice not on PATH; skipping milestone-6 e2e smoke test")
	}
	examples, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatalf("resolve examples dir: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(examples, "preamp_12ax7.cir"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	c, err := netlist.Parse(strings.NewReader(string(src)))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	eng := engine.NewWithOptions(engine.Options{WorkDir: examples})
	t.Cleanup(func() { _ = eng.Close() })
	srv := New(eng, Options{Logger: quietLogger()})
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { hs.Close(); _ = srv.Close() })

	u, _ := url.Parse(hs.URL + "/ws")
	u.Scheme = "ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(mustEnvelope(OpCircuitLoad, "load-fft", CircuitLoadPayload{Circuit: c})); err != nil {
		t.Fatalf("write circuit.load: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readUntilOp(t, conn, OpCircuitChanged)

	if err := conn.WriteJSON(mustEnvelope(OpSimRun, "run-fft", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "spectrum", Args: []string{"1u", "5m"}},
	})); err != nil {
		t.Fatalf("write sim.run: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	var (
		count       int
		peakInAcDB  float64 = -math.MaxFloat64
		peakInAcFreq float64
		seenDone    bool
	)
	for !seenDone {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatalf("read: %v", err)
		}
		switch env.Op {
		case OpSimResult:
			var p SimResultPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				t.Fatalf("decode sim.result: %v", err)
			}
			count++
			if v, ok := p.Frame.Values["in_ac:mag_db"]; ok && v > peakInAcDB {
				peakInAcDB = v
				peakInAcFreq = p.Frame.X
			}
		case OpSimDone:
			seenDone = true
		case OpSimError, OpError:
			t.Fatalf("unexpected error envelope: %s", env.Payload)
		default:
			t.Fatalf("unexpected op %q", env.Op)
		}
	}
	if count < 100 {
		t.Errorf("expected many FFT bins; got %d", count)
	}
	// 1 kHz fundamental within one 200 Hz bin.
	if peakInAcFreq < 700 || peakInAcFreq > 1300 {
		t.Errorf("peak in_ac bin not near 1 kHz: %.1f Hz (%.1f dB)", peakInAcFreq, peakInAcDB)
	}
	t.Logf("e2e spectrum: bins=%d peak in_ac %.1f Hz / %.2f dB", count, peakInAcFreq, peakInAcDB)
}

// TestE2ELowPassACOverWebSocket runs the milestone-6 AC sweep on the
// Sallen-Key Butterworth fixture and asserts the textbook shape: flat 0 dB
// in the passband, ~-3 dB at fc=4 kHz, and well below -20 dB at 40 kHz
// (2nd order = -40 dB/dec rolloff). This catches both the parser/AC stimulus
// extension and the engine's AC vector layout in one shot.
func TestE2ELowPassACOverWebSocket(t *testing.T) {
	if _, err := exec.LookPath("ngspice"); err != nil {
		t.Skip("ngspice not on PATH; skipping milestone-6 e2e smoke test")
	}
	examples, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatalf("resolve examples dir: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(examples, "lp_butter_sallenkey_4k.cir"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	c, err := netlist.Parse(strings.NewReader(string(src)))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	eng := engine.NewWithOptions(engine.Options{WorkDir: examples})
	t.Cleanup(func() { _ = eng.Close() })
	srv := New(eng, Options{Logger: quietLogger()})
	hs := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { hs.Close(); _ = srv.Close() })

	u, _ := url.Parse(hs.URL + "/ws")
	u.Scheme = "ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(mustEnvelope(OpCircuitLoad, "load-lp", CircuitLoadPayload{Circuit: c})); err != nil {
		t.Fatalf("write circuit.load: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readUntilOp(t, conn, OpCircuitChanged)

	if err := conn.WriteJSON(mustEnvelope(OpSimRun, "run-lp", SimRunPayload{
		Analysis: circuit.Analysis{Kind: "ac", Args: []string{"dec", "20", "10", "100k"}},
	})); err != nil {
		t.Fatalf("write sim.run: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	type sample struct{ f, db float64 }
	var (
		samples  []sample
		seenDone bool
	)
	for !seenDone {
		var env Envelope
		if err := conn.ReadJSON(&env); err != nil {
			t.Fatalf("read: %v", err)
		}
		switch env.Op {
		case OpSimResult:
			var p SimResultPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				t.Fatalf("decode sim.result: %v", err)
			}
			if v, ok := p.Frame.Values["out:mag_db"]; ok {
				samples = append(samples, sample{p.Frame.X, v})
			}
		case OpSimDone:
			seenDone = true
		case OpSimError, OpError:
			t.Fatalf("unexpected error envelope: %s", env.Payload)
		}
	}
	if len(samples) < 50 {
		t.Fatalf("too few AC frames: %d", len(samples))
	}

	// Find values nearest the diagnostic frequencies.
	near := func(target float64) sample {
		best := samples[0]
		for _, s := range samples {
			if math.Abs(math.Log10(s.f)-math.Log10(target)) < math.Abs(math.Log10(best.f)-math.Log10(target)) {
				best = s
			}
		}
		return best
	}
	pass100 := near(100).db   // passband
	atFC := near(4000).db     // -3 dB target
	at40k := near(40000).db   // -40 dB/dec → ~-40 dB (10× past fc, 2nd order: -40 dB)

	if pass100 < -1 || pass100 > 1 {
		t.Errorf("passband (100 Hz) not flat: %.2f dB", pass100)
	}
	if atFC > -1 || atFC < -6 {
		t.Errorf("4 kHz fc not near -3 dB: %.2f dB", atFC)
	}
	if at40k > -20 {
		t.Errorf("40 kHz not well past -20 dB: %.2f dB", at40k)
	}
	t.Logf("e2e LP ac: 100Hz=%.2fdB 4kHz=%.2fdB 40kHz=%.2fdB", pass100, atFC, at40k)
}

// readUntilOp is the conn-only variant of readUntil that does not reset the
// read deadline on every call — the e2e test sets one big deadline up front.
func readUntilOp(t *testing.T, c *websocket.Conn, want string) Envelope {
	t.Helper()
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

