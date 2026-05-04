package engine_test

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"
	"circuit-designer/backend/internal/netlist"
)

// requireNgspice skips the test when ngspice is not installed, per the
// milestone-2 acceptance criteria. CI without ngspice should still pass.
func requireNgspice(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ngspice"); err != nil {
		t.Skip("ngspice not on PATH; skipping engine integration test")
	}
}

// examplesDir resolves to <repo>/examples regardless of where `go test` is
// invoked from. The engine needs it as cmd.Dir so .LIB tubes_koren.lib
// resolves.
func examplesDir(t *testing.T) string {
	t.Helper()
	// _test.go runs with cwd = the package directory:
	// backend/internal/engine. examples/ is three levels up (repo root).
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatalf("resolve examples dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "preamp_12ax7.cir")); err != nil {
		t.Fatalf("examples dir not found at %s: %v", abs, err)
	}
	return abs
}

// loadFixture parses examples/preamp_12ax7.cir into a Circuit.
func loadFixture(t *testing.T, dir string) *circuit.Circuit {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "preamp_12ax7.cir"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	c, err := netlist.Parse(f)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return c
}

// TestRunPreampTran is the primary milestone-2 acceptance test: load the
// canonical fixture, run a short transient, assert that vout produces many
// samples in a sane amplitude range.
func TestRunPreampTran(t *testing.T) {
	requireNgspice(t)
	dir := examplesDir(t)
	c := loadFixture(t, dir)

	eng := engine.NewWithOptions(engine.Options{WorkDir: dir})
	t.Cleanup(func() { _ = eng.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	frames, err := eng.Run(ctx, c, circuit.Analysis{
		Kind: "tran",
		Args: []string{"1u", "5m", "uic"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		count       int
		first       *engine.Frame
		runErr      *engine.RunError
		peakAbsVout float64
	)
	for f := range frames {
		f := f // local copy for &
		if f.Err != nil {
			runErr = f.Err
			continue
		}
		if first == nil {
			first = &f
		}
		count++
		if v, ok := f.Values["vout"]; ok {
			if a := math.Abs(v); a > peakAbsVout {
				peakAbsVout = a
			}
		}
	}

	if runErr != nil {
		t.Fatalf("simulation reported error: %+v", runErr)
	}
	if count < 100 {
		t.Errorf("expected many frames for a 5ms TRAN at 1us step; got %d", count)
	}
	// 5ms at 1us nominal step ≈ 5000 timesteps; ngspice's adaptive solver
	// returns ~5000-6000 in practice. Be loose here — exact counts depend on
	// the model and the version, and the contract is just "many".
	if count > 50_000 {
		t.Errorf("frame count looks runaway: %d", count)
	}

	if first == nil {
		t.Fatal("no data frames received")
	}
	if first.X < 0 || first.X > 1e-6 {
		t.Errorf("first frame X out of expected range [0, 1us]: %g", first.X)
	}
	if _, ok := first.Values["vout"]; !ok {
		t.Errorf("first frame missing vout; have keys: %v", keysOf(first.Values))
	}

	// Sane amplitude check: vout should be well above the 0.25 V input swing
	// (the stage has substantial gain) but bounded by B+ = 250 V.
	if peakAbsVout < 1.0 {
		t.Errorf("|peak vout| too small: %g (input is 0.25 V; expected gain >> 1)", peakAbsVout)
	}
	if peakAbsVout > 500.0 {
		t.Errorf("|peak vout| out of plausible range: %g (B+ is 250 V)", peakAbsVout)
	}
	t.Logf("frames=%d peak|vout|=%.2f V", count, peakAbsVout)
}

// TestRunStreamingDuringRun verifies the milestone-2 streaming requirement:
// the consumer can read more than one Frame from the channel before it
// closes. We assert by counting frames received before the channel is
// drained-to-close.
func TestRunStreamingDuringRun(t *testing.T) {
	requireNgspice(t)
	dir := examplesDir(t)
	c := loadFixture(t, dir)

	eng := engine.NewWithOptions(engine.Options{WorkDir: dir})
	t.Cleanup(func() { _ = eng.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	frames, err := eng.Run(ctx, c, circuit.Analysis{
		Kind: "tran",
		Args: []string{"1u", "5m", "uic"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Read just two frames, confirming the channel delivers more than a
	// single batch-at-end. Then drain to let the engine clean up.
	var got int
	for f := range frames {
		if f.Err != nil {
			t.Fatalf("error frame: %+v", f.Err)
		}
		got++
		if got >= 2 {
			break
		}
	}
	if got < 2 {
		t.Errorf("expected at least 2 frames before drain; got %d", got)
	}
	go func() {
		for range frames {
		}
	}()
}

// TestRunCancelMidRun is the cancellation-deadline test: a long transient is
// started, the caller cancels via context, and the engine returns within the
// 200 ms graceful-quit window plus a small slack budget for the goroutine
// shutdown.
func TestRunCancelMidRun(t *testing.T) {
	requireNgspice(t)
	dir := examplesDir(t)
	c := loadFixture(t, dir)

	eng := engine.NewWithOptions(engine.Options{WorkDir: dir})
	t.Cleanup(func() { _ = eng.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 100 ms of simulated time at 1 µs step ≈ 100k timesteps with the tube
	// model — long enough to comfortably exceed the cancellation deadline.
	frames, err := eng.Run(ctx, c, circuit.Analysis{
		Kind: "tran",
		Args: []string{"1u", "100m", "uic"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for the run to actually be in progress before cancelling, so the
	// test exercises mid-run kill rather than racing the spawn.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	cancel()

	// Drain the channel to completion. With the 200 ms WaitDelay plus
	// goroutine teardown, total wall-clock should land below ~500 ms. If
	// SIGKILL is not delivered, the channel never closes and the test
	// will time out via -timeout.
	for f := range frames {
		_ = f
	}
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Errorf("cancellation took too long: %v (expected < 1 s, target ~250 ms)", elapsed)
	} else {
		t.Logf("cancel-to-channel-close: %v", elapsed)
	}
}

// TestRunUnsupportedAnalysis verifies that an empty or unknown analysis kind
// is rejected before any subprocess work happens.
func TestRunUnsupportedAnalysis(t *testing.T) {
	eng := engine.New()
	t.Cleanup(func() { _ = eng.Close() })

	cases := []circuit.Analysis{
		{},
		{Kind: "wibble"},
	}
	for _, a := range cases {
		_, err := eng.Run(context.Background(), &circuit.Circuit{}, a)
		if err == nil {
			t.Errorf("Run(%+v): expected error, got nil", a)
		}
	}
}

// TestRunSpawnFailure verifies that an invalid ngspice path produces a
// structured error frame rather than a hang or panic.
func TestRunSpawnFailure(t *testing.T) {
	c := &circuit.Circuit{
		Probes: []circuit.Probe{{Name: "vout", Node: "vout", Kind: "voltage"}},
	}
	eng := engine.NewWithOptions(engine.Options{
		NgspicePath: "definitely-not-ngspice-" + atomicSuffix(),
	})
	t.Cleanup(func() { _ = eng.Close() })

	frames, err := eng.Run(context.Background(), c, circuit.Analysis{
		Kind: "tran", Args: []string{"1u", "1u"},
	})
	if err != nil {
		// Some platforms surface the lookup failure synchronously; that is
		// acceptable too.
		return
	}
	var got *engine.RunError
	for f := range frames {
		if f.Err != nil {
			got = f.Err
		}
	}
	if got == nil {
		t.Fatal("expected an error frame from a missing-binary spawn")
	}
	if got.Kind != "spawn" && got.Kind != "subprocess" {
		t.Errorf("unexpected error Kind: %q (want spawn or subprocess)", got.Kind)
	}
}

// TestEngineCloseStopsActiveRun verifies Close cancels in-flight runs and
// returns once they have released their resources.
func TestEngineCloseStopsActiveRun(t *testing.T) {
	requireNgspice(t)
	dir := examplesDir(t)
	c := loadFixture(t, dir)

	eng := engine.NewWithOptions(engine.Options{WorkDir: dir})
	frames, err := eng.Run(context.Background(), c, circuit.Analysis{
		Kind: "tran",
		Args: []string{"1u", "100m", "uic"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	closeStart := time.Now()
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closeElapsed := time.Since(closeStart)

	// Drain remaining frames to avoid leaking the consumer goroutine.
	for range frames {
	}

	if closeElapsed > 1*time.Second {
		t.Errorf("Close took too long: %v", closeElapsed)
	}
}

// TestRunPreampAC is the milestone-6 acceptance test for AC analysis. The
// preamp is a textbook common-cathode 12AX7 stage; we expect ~25-35 dB of
// midband gain at vout (the manual run before this test measured 30.3 dB at
// 100 kHz). Phase at 10 kHz should be near -180° because the preamp inverts.
func TestRunPreampAC(t *testing.T) {
	requireNgspice(t)
	dir := examplesDir(t)
	c := loadFixture(t, dir)

	eng := engine.NewWithOptions(engine.Options{WorkDir: dir})
	t.Cleanup(func() { _ = eng.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	frames, err := eng.Run(ctx, c, circuit.Analysis{
		Kind: "ac",
		Args: []string{"dec", "20", "10", "100k"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		count               int
		runErr              *engine.RunError
		voutMagDBAt10kBin   float64 = math.NaN()
		voutPhaseAt10kBin   float64 = math.NaN()
		first               *engine.Frame
	)
	for f := range frames {
		f := f
		if f.Err != nil {
			runErr = f.Err
			continue
		}
		if first == nil {
			first = &f
		}
		count++
		// Track the bin nearest 10 kHz so we can assert midband behaviour.
		if math.Abs(math.Log10(f.X)-4) < 0.05 { // ±5% in log10(f) around 10 kHz
			if v, ok := f.Values["vout:mag_db"]; ok {
				voutMagDBAt10kBin = v
			}
			if v, ok := f.Values["vout:phase_deg"]; ok {
				voutPhaseAt10kBin = v
			}
		}
	}

	if runErr != nil {
		t.Fatalf("simulation reported error: %+v", runErr)
	}
	if count < 50 {
		t.Errorf("expected many frames for a dec-20 sweep across 4 decades; got %d", count)
	}
	if first == nil {
		t.Fatal("no data frames received")
	}
	// First bin should be at the start frequency.
	if first.X < 9 || first.X > 11 {
		t.Errorf("first frame X out of expected ~10 Hz range: %g", first.X)
	}
	// Each AC voltage probe expands to two keys: mag_db + phase_deg.
	if _, ok := first.Values["vout:mag_db"]; !ok {
		t.Errorf("first frame missing vout:mag_db; have keys: %v", keysOf(first.Values))
	}
	if _, ok := first.Values["vout:phase_deg"]; !ok {
		t.Errorf("first frame missing vout:phase_deg; have keys: %v", keysOf(first.Values))
	}

	if math.IsNaN(voutMagDBAt10kBin) {
		t.Fatalf("no frame near 10 kHz; sweep didn't cover the band?")
	}
	// Common-cathode 12AX7 stage: textbook µ ≈ 100, with cathode degeneration
	// + plate load it lands ~25-35 dB. Loose bounds.
	if voutMagDBAt10kBin < 20 || voutMagDBAt10kBin > 40 {
		t.Errorf("vout midband gain out of expected 20-40 dB range: %.2f dB", voutMagDBAt10kBin)
	}
	// Phase at 10 kHz should be near -180° (inverting). Allow ±20° wobble for
	// the LF coupling cap residue.
	if math.Abs(math.Abs(voutPhaseAt10kBin)-180) > 20 {
		t.Errorf("vout midband phase not near ±180°: %.1f°", voutPhaseAt10kBin)
	}
	t.Logf("AC frames=%d vout @ 10 kHz: %.2f dB / %.1f°", count, voutMagDBAt10kBin, voutPhaseAt10kBin)
}

// TestRunPreampSpectrum is the milestone-6 acceptance test for the spectrum
// path: tran → linearize → fft → wrdata. We assert against the in_ac probe
// rather than vout because the preamp's vout has a multi-millisecond startup
// settling transient (no DC path past the 100n coupling cap) that dominates
// the FFT at low frequencies. in_ac sees the raw 1 kHz sine source and is
// the clean "spectrum analyzer correctness" probe.
func TestRunPreampSpectrum(t *testing.T) {
	requireNgspice(t)
	dir := examplesDir(t)
	c := loadFixture(t, dir)

	eng := engine.NewWithOptions(engine.Options{WorkDir: dir})
	t.Cleanup(func() { _ = eng.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	frames, err := eng.Run(ctx, c, circuit.Analysis{
		Kind: "spectrum",
		// 5 ms capture at 1 us → 200 Hz bin spacing; enough to localise the
		// 1 kHz fundamental within one bin.
		Args: []string{"1u", "5m"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var (
		count    int
		runErr   *engine.RunError
		peakBin  float64 = -math.MaxFloat64
		peakFreq float64
	)
	for f := range frames {
		f := f
		if f.Err != nil {
			runErr = f.Err
			continue
		}
		count++
		if v, ok := f.Values["in_ac:mag_db"]; ok && v > peakBin {
			peakBin = v
			peakFreq = f.X
		}
	}

	if runErr != nil {
		t.Fatalf("simulation reported error: %+v", runErr)
	}
	if count < 100 {
		t.Errorf("expected many FFT bins; got %d", count)
	}
	// Peak should land near the 1 kHz fundamental — within 1 RBW (200 Hz).
	if peakFreq < 700 || peakFreq > 1300 {
		t.Errorf("peak in_ac bin not near 1 kHz: %.1f Hz (%.1f dB)", peakFreq, peakBin)
	}
	t.Logf("FFT bins=%d peak in_ac: %.1f Hz @ %.2f dB", count, peakFreq, peakBin)
}

// TestRunNoProbesReturnsError verifies the (rare) case where Run is invoked
// against a circuit that has no probes. With no vectors to record, wrdata
// would be a no-op, so we surface a clear error instead.
func TestRunNoProbesReturnsError(t *testing.T) {
	requireNgspice(t)
	c := &circuit.Circuit{}
	eng := engine.New()
	t.Cleanup(func() { _ = eng.Close() })

	frames, err := eng.Run(context.Background(), c, circuit.Analysis{
		Kind: "tran", Args: []string{"1u", "1u"},
	})
	if err != nil {
		// Acceptable: rejected synchronously.
		return
	}
	var sawErr bool
	for f := range frames {
		if f.Err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("expected error frame for circuit with no probes")
	}
}

func keysOf(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// atomicSuffix returns a per-test-process suffix used to ensure spawn
// failures hit a path that does not exist anywhere on $PATH.
var spawnCounter atomic.Uint64

func atomicSuffix() string {
	n := spawnCounter.Add(1)
	return runtime.GOOS + "-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "") + "-" + itoa(n)
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
