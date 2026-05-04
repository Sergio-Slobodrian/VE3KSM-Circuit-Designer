package engine

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/netlist"
)

// cancelGrace is the deadline ngspice has to exit after a graceful "quit" has
// been written to its stdin (DESIGN.md §9.3). When it elapses the os/exec
// machinery closes pipes and sends SIGKILL.
const cancelGrace = 200 * time.Millisecond

// New returns an ngspice-backed Engine using default options.
func New() Engine { return NewWithOptions(Options{}) }

// NewWithOptions returns an ngspice-backed Engine. NgspicePath defaults to
// "ngspice" (looked up on $PATH).
func NewWithOptions(opts Options) Engine {
	if opts.NgspicePath == "" {
		opts.NgspicePath = "ngspice"
	}
	return &ngspiceEngine{
		opts: opts,
		runs: map[string]*runHandle{},
	}
}

type ngspiceEngine struct {
	opts Options

	mu     sync.Mutex
	runs   map[string]*runHandle
	closed bool
}

type runHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Run implements Engine.
func (e *ngspiceEngine) Run(ctx context.Context, c *circuit.Circuit, a circuit.Analysis) (<-chan Frame, error) {
	if c == nil {
		return nil, errors.New("engine.Run: nil circuit")
	}
	// Validate the analysis kind synchronously so callers get an error from
	// Run rather than via an error frame. The richer plan is rebuilt in
	// execute once we have a temp directory.
	if _, err := planAnalysis(c, a); err != nil {
		return nil, err
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, errors.New("engine: closed")
	}
	runID := newRunID()
	runCtx, cancel := context.WithCancel(ctx)
	handle := &runHandle{cancel: cancel, done: make(chan struct{})}
	e.runs[runID] = handle
	e.mu.Unlock()

	frames := make(chan Frame, 16)

	go func() {
		defer close(frames)
		defer close(handle.done)
		defer cancel()
		defer e.removeRun(runID)
		e.execute(runCtx, runID, c, a, frames)
	}()

	return frames, nil
}

// Cancel implements Engine.
func (e *ngspiceEngine) Cancel(ctx context.Context, runID string) error {
	e.mu.Lock()
	h, ok := e.runs[runID]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("engine.Cancel: no such run %q", runID)
	}
	h.cancel()
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close implements Engine.
func (e *ngspiceEngine) Close() error {
	e.mu.Lock()
	e.closed = true
	handles := make([]*runHandle, 0, len(e.runs))
	for _, h := range e.runs {
		handles = append(handles, h)
	}
	e.mu.Unlock()

	for _, h := range handles {
		h.cancel()
	}
	for _, h := range handles {
		<-h.done
	}
	return nil
}

func (e *ngspiceEngine) removeRun(id string) {
	e.mu.Lock()
	delete(e.runs, id)
	e.mu.Unlock()
}

// execute drives one ngspice subprocess from spawn through teardown. Only
// goroutine that ever writes to frames; the caller closes the channel after
// execute returns.
func (e *ngspiceEngine) execute(ctx context.Context, runID string, c *circuit.Circuit, a circuit.Analysis, frames chan<- Frame) {
	tmpDir, err := os.MkdirTemp("", "circuit-lab-run-")
	if err != nil {
		emitError(ctx, frames, runID, &RunError{Kind: "internal", Message: fmt.Sprintf("create temp dir: %v", err)})
		return
	}
	defer os.RemoveAll(tmpDir)

	netlistPath := filepath.Join(tmpDir, "circuit.cir")
	dataPath := filepath.Join(tmpDir, "data.txt")

	if err := writeNetlist(netlistPath, c); err != nil {
		emitError(ctx, frames, runID, &RunError{Kind: "internal", Message: err.Error()})
		return
	}

	plan, err := planAnalysis(c, a)
	if err != nil {
		emitError(ctx, frames, runID, &RunError{Kind: "internal", Message: err.Error()})
		return
	}
	if len(plan.vectors) == 0 {
		emitError(ctx, frames, runID, &RunError{
			Kind:    "internal",
			Message: "no probes on circuit; nothing to record (add a circuit.Probe before Run)",
		})
		return
	}

	workDir := e.opts.WorkDir
	if workDir == "" {
		workDir = tmpDir
	}

	script := buildControlScript(netlistPath, dataPath, plan)

	cmd := exec.CommandContext(ctx, e.opts.NgspicePath, "-p")
	cmd.Dir = workDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		emitError(ctx, frames, runID, &RunError{Kind: "spawn", Message: err.Error()})
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emitError(ctx, frames, runID, &RunError{Kind: "spawn", Message: err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		emitError(ctx, frames, runID, &RunError{Kind: "spawn", Message: err.Error()})
		return
	}

	// Graceful cancellation: write "quit\n" so ngspice can wind down at the
	// next prompt; if it is busy mid-analysis the write will sit in the pipe
	// and WaitDelay will fire SIGKILL after the 200 ms grace window.
	// Returning nil keeps that grace window honoured (returning an error or
	// calling Process.Kill here would short-circuit it).
	cmd.Cancel = func() error {
		_, _ = io.WriteString(stdin, "\nquit\n")
		return nil
	}
	cmd.WaitDelay = cancelGrace

	if err := cmd.Start(); err != nil {
		emitError(ctx, frames, runID, &RunError{Kind: "spawn", Message: err.Error()})
		return
	}

	// Drain stdout to discard. We must read it or the pipe will fill and
	// block ngspice; we do not parse status messages there because wrdata
	// is the data path.
	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		_, _ = io.Copy(io.Discard, stdout)
	}()

	// Capture stderr for convergence-error parsing. The whole stream is
	// kept (it is small in practice) so we can match across multi-line
	// reports.
	stderrDone := make(chan struct{})
	var stderrBuf strings.Builder
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			stderrBuf.WriteString(sc.Text())
			stderrBuf.WriteByte('\n')
		}
	}()

	// Feed the control script. The script is small (well under one pipe
	// buffer) and ngspice processes commands sequentially, so a synchronous
	// write does not deadlock. We close stdin after writing so ngspice
	// exits naturally on EOF if `quit` somehow never reaches it.
	_, _ = io.WriteString(stdin, script)
	_ = stdin.Close()

	// Wait for the subprocess to fully exit. exec.Cmd.Wait also closes the
	// stdout/stderr pipes once it returns, so the drainers above complete
	// shortly after.
	waitErr := cmd.Wait()
	<-stdoutDone
	<-stderrDone

	// Diagnose, in priority order: cancellation, convergence, generic
	// subprocess failure. Cancellation is detected via ctx rather than
	// waitErr because exec returns "signal: killed" or context errors that
	// vary across platforms.
	if cerr := ctx.Err(); cerr != nil {
		emitError(ctx, frames, runID, &RunError{
			Kind:    "subprocess",
			Message: fmt.Sprintf("cancelled: %v", cerr),
		})
		return
	}
	if cerr := parseConvergenceError(stderrBuf.String()); cerr != nil {
		emitError(ctx, frames, runID, cerr)
		return
	}
	if waitErr != nil {
		emitError(ctx, frames, runID, &RunError{
			Kind:    "subprocess",
			Message: fmt.Sprintf("ngspice exit: %v; stderr: %s", waitErr, trimStderr(stderrBuf.String())),
		})
		return
	}

	// Stream the data file row-by-row to frames. By the time we reach this
	// line ngspice has fully written the file (wrdata is synchronous after
	// the analysis), but the consumer still sees Frames trickling through
	// the channel as we read.
	if err := streamDataFile(ctx, dataPath, runID, plan.vectors, frames); err != nil {
		emitError(ctx, frames, runID, &RunError{
			Kind:    "internal",
			Message: fmt.Sprintf("read data file: %v", err),
		})
	}
}

func writeNetlist(path string, c *circuit.Circuit) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create netlist: %w", err)
	}
	if err := netlist.Emit(c, f); err != nil {
		_ = f.Close()
		return fmt.Errorf("emit netlist: %w", err)
	}
	return f.Close()
}

// vectorRequest pairs the ngspice-side vector expression (e.g. "v(vout)" or
// "vdb(vout)") with the user-facing key used in Frame.Values. For tran/dc/op
// the key is just the probe node; for ac/spectrum it's "<node>:mag_db" or
// "<node>:phase_deg" so the frontend can pivot back to per-probe pairs.
type vectorRequest struct {
	Spice string
	Key   string
}

// analysisPlan is the lowering of one (Circuit, Analysis) pair to the ngspice
// commands that produce a wrdata file. Composed by planAnalysis and consumed
// by buildControlScript; everything else in execute is plan-agnostic.
type analysisPlan struct {
	// commands run in order after `source <netlist>`. May contain multiple
	// lines (e.g. "tran 1u 5m" + "linearize" + "fft v(out)" for spectrum).
	commands []string
	// vectors fed to the trailing wrdata, in order. Each vector becomes one
	// column in the data file (after the leading scale column).
	vectors []vectorRequest
	// settings prepended to the script, e.g. "set specwindow=hanning". Run
	// before `source` so they affect the analysis itself.
	settings []string
}

// planAnalysis lowers a (Circuit, Analysis) pair to a runnable plan. The
// supported kinds are:
//
//	tran     — time-domain. Per probe: v(node) → key "node" (or i(node) for
//	           current probes).
//	dc       — swept .DC. Same vector layout as tran.
//	op       — operating point. Single sample, same layout as tran.
//	ac       — small-signal frequency response. Per voltage probe two vectors
//	           are emitted: vdb(node) → "node:mag_db", vp(node) → "node:phase_deg".
//	spectrum — FFT of a transient run. Args mirror tran. After tran the engine
//	           runs `linearize` (FFT requires uniform spacing) then `fft` over
//	           every voltage probe; vectors are db(mag(...)) + cph(...) in the
//	           resulting frequency-domain plot. Optional Options["window"] sets
//	           ngspice's specwindow (hanning|hamming|blackman|bartlet|cosine_n|
//	           triangle|none); default hanning.
//
// Current probes for ac/spectrum are not yet supported (no UI exposes them).
func planAnalysis(c *circuit.Circuit, a circuit.Analysis) (analysisPlan, error) {
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	switch kind {
	case "tran", "dc":
		args := strings.Join(a.Args, " ")
		return analysisPlan{
			commands: []string{strings.TrimSpace(kind + " " + args)},
			vectors:  scalarProbeVectors(c),
		}, nil
	case "op":
		return analysisPlan{
			commands: []string{"op"},
			vectors:  scalarProbeVectors(c),
		}, nil
	case "noise":
		// No UI surface yet; pass-through for completeness so a future
		// .NOISE tab does not need to revisit the engine.
		args := strings.Join(a.Args, " ")
		return analysisPlan{
			commands: []string{strings.TrimSpace("noise " + args)},
			vectors:  scalarProbeVectors(c),
		}, nil
	case "ac":
		commands := acStimulusCommands(c)
		commands = append(commands, strings.TrimSpace("ac "+strings.Join(a.Args, " ")))
		return analysisPlan{
			commands: commands,
			vectors:  complexProbeVectors(c, false),
		}, nil
	case "spectrum":
		settings := []string{}
		win := strings.ToLower(strings.TrimSpace(a.Options["window"]))
		if win == "" {
			win = "hanning"
		}
		settings = append(settings, "set specwindow="+win)
		// fft requires every voltage probe spelled out. We lean on the fact
		// that linearize is a no-op when the data is already uniform.
		fftArgs := make([]string, 0, len(c.Probes))
		for _, p := range c.Probes {
			if p.Kind == "current" {
				continue
			}
			fftArgs = append(fftArgs, "v("+p.Node+")")
		}
		if len(fftArgs) == 0 {
			return analysisPlan{}, errors.New("engine.Run: spectrum analysis requires at least one voltage probe")
		}
		commands := []string{
			strings.TrimSpace("tran " + strings.Join(a.Args, " ")),
			"linearize",
			"fft " + strings.Join(fftArgs, " "),
		}
		return analysisPlan{
			commands: commands,
			vectors:  complexProbeVectors(c, true),
			settings: settings,
		}, nil
	case "":
		return analysisPlan{}, errors.New("engine.Run: analysis.Kind is empty")
	default:
		return analysisPlan{}, fmt.Errorf("engine.Run: unsupported analysis kind %q", a.Kind)
	}
}

// scalarProbeVectors lowers each Probe to one wrdata column (v(node) for
// voltage, i(node) for current). Used for tran/dc/op/noise.
func scalarProbeVectors(c *circuit.Circuit) []vectorRequest {
	out := make([]vectorRequest, 0, len(c.Probes))
	for _, p := range c.Probes {
		switch p.Kind {
		case "current":
			out = append(out, vectorRequest{
				Spice: fmt.Sprintf("i(%s)", p.Node),
				Key:   p.Node,
			})
		default:
			out = append(out, vectorRequest{
				Spice: fmt.Sprintf("v(%s)", p.Node),
				Key:   p.Node,
			})
		}
	}
	return out
}

// complexProbeVectors expands voltage probes into magnitude (dB) + phase
// (degrees) pairs. Used for ac and spectrum.
//
// Two ngspice quirks shape the expressions:
//
//  1. db(0) is a fatal error in ngspice 42 ("argument out of range for db").
//     Adding a 1e-30 floor inside the magnitude lets nodes that are silent at
//     a given bin survive without aborting wrdata. -300 dB on a quiet node is
//     a fine sentinel; the UI clamps the y-axis well above that.
//  2. cph() and vp() document themselves as "phase in degrees" but on this
//     ngspice 42 build they return radians. Multiplying by 180/pi explicitly
//     normalizes regardless of which unit the build emits — converting
//     pre-converted degrees would over-rotate by ~57×, but the build is
//     consistent here and the test asserts a sensible degree range.
func complexProbeVectors(c *circuit.Circuit, _ bool) []vectorRequest {
	out := make([]vectorRequest, 0, 2*len(c.Probes))
	for _, p := range c.Probes {
		if p.Kind == "current" {
			// AC current probes would need db(mag(i(...)))/cph(i(...)); the
			// UI does not expose current probes for ac/spectrum, so skip them
			// rather than emit potentially invalid expressions.
			continue
		}
		mag := fmt.Sprintf("db(mag(v(%s)) + 1e-30)", p.Node)
		phase := fmt.Sprintf("cph(v(%s)) * 180 / pi", p.Node)
		out = append(out,
			vectorRequest{Spice: mag, Key: p.Node + ":mag_db"},
			vectorRequest{Spice: phase, Key: p.Node + ":phase_deg"},
		)
	}
	return out
}

// acStimulusCommands returns `alter` commands that give every voltage source
// AC magnitude 1 (if not already explicitly set in the netlist). Without this,
// AC analysis on a circuit whose only source is a SIN/PWL/etc. waveform
// returns silence (all zeros) because the small-signal solver sees no AC
// stimulus.
//
// This is the simplest workable default for milestone 6; per DESIGN.md §6.4
// the eventual UI lets the user designate one specific source as the stimulus
// "port 1" and one probe as "port 2". When that ships, the source-selector
// will narrow this to a single alter command.
func acStimulusCommands(c *circuit.Circuit) []string {
	out := []string{}
	for _, comp := range c.Components {
		if comp.Kind != "voltage_source" {
			continue
		}
		// If the netlist already supplies AC stimulus on this source we leave
		// it alone — the user explicitly set it. The data model carries this
		// in SourceSpec.AC; netlist round-trip support lands in a later
		// milestone, so for now this is effectively always nil and we always
		// inject. Either way, AC=1 is harmless on already-AC-1 sources.
		if comp.Source != nil && comp.Source.AC != nil && comp.Source.AC.Magnitude != "" {
			continue
		}
		out = append(out, fmt.Sprintf("alter %s ac=1", strings.ToLower(comp.Ref)))
	}
	return out
}

// buildControlScript composes the stdin command stream sent to `ngspice -p`.
//
//   - `set ngbehavior=lt` so `.LIB <file>` (no section) is treated as
//     .INCLUDE — the LTspice convention used by tubes_koren.lib and most
//     vendor model libraries.
//   - `set wr_singlescale` and `set wr_vecnames` keep wrdata in the simplest
//     ASCII shape: one shared time/freq column, a header row of vector names,
//     one row per sample.
//   - plan.settings (if any) run before `source` so they shape the analysis
//     itself (e.g. `set specwindow=hanning` before fft).
//   - plan.commands run in order; the trailing wrdata captures plan.vectors.
func buildControlScript(netlistPath, dataPath string, plan analysisPlan) string {
	names := make([]string, len(plan.vectors))
	for i, v := range plan.vectors {
		names[i] = v.Spice
	}
	var b strings.Builder
	b.WriteString("set ngbehavior=lt\n")
	b.WriteString("set wr_singlescale\n")
	b.WriteString("set wr_vecnames\n")
	for _, s := range plan.settings {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "source %s\n", netlistPath)
	for _, cmd := range plan.commands {
		b.WriteString(cmd)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "wrdata %s %s\n", dataPath, strings.Join(names, " "))
	b.WriteString("quit\n")
	return b.String()
}

// streamDataFile opens the wrdata output and emits one Frame per row. ngspice
// 42 writes the path verbatim (no .data suffix is added when an extension is
// present), but we accept the suffixed form too for forward compat.
func streamDataFile(ctx context.Context, basePath, runID string, vectors []vectorRequest, frames chan<- Frame) error {
	f, err := openDataFile(basePath)
	if err != nil {
		// File never appeared — typically because the analysis errored
		// before producing any output. The error path in execute already
		// reports stderr-based diagnostics; surface nothing further here.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	headerSkipped := false
	index := 0
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if frame, ok := parseDataRow(line, runID, index, vectors, &headerSkipped); ok {
				select {
				case frames <- frame:
				case <-ctx.Done():
					return nil
				}
				index++
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// openDataFile returns an opened handle to basePath, falling back to
// basePath+".data" if needed. Returns the os.IsNotExist error from the last
// candidate when none exist.
func openDataFile(basePath string) (*os.File, error) {
	candidates := []string{basePath, basePath + ".data"}
	var lastErr error
	for _, c := range candidates {
		f, err := os.Open(c)
		if err == nil {
			return f, nil
		}
		lastErr = err
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// parseDataRow handles one ngspice wrdata line. The first non-empty line is
// the vector-name header (skipped); subsequent lines are space-separated
// floats: scale value followed by one column per requested vector.
func parseDataRow(line, runID string, index int, vectors []vectorRequest, headerSkipped *bool) (Frame, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Frame{}, false
	}
	if !*headerSkipped {
		*headerSkipped = true
		return Frame{}, false
	}
	fields := strings.Fields(line)
	if len(fields) < 1+len(vectors) {
		// Trailing partial row — skip rather than fail.
		return Frame{}, false
	}
	x, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return Frame{}, false
	}
	values := make(map[string]float64, len(vectors))
	for i, v := range vectors {
		val, err := strconv.ParseFloat(fields[1+i], 64)
		if err != nil {
			continue
		}
		values[v.Key] = val
	}
	return Frame{RunID: runID, Index: index, X: x, Values: values}, true
}

// Convergence-error parsing. ngspice prints variants like:
//
//	Error: NIiter: no convergence at node "plate", iter 200, last v=187.3
//	Note: timestep too small ... convergence problem
//	Error: too many subdivisions; last v(plate)=187.3
//
// We match on a few canonical phrases and pull whichever fields are present.
var (
	noConvNodeRe = regexp.MustCompile(`(?i)(?:node|v\()\s*["']?([\w.+\-:#]+)["']?\)?`)
	noConvVoltRe = regexp.MustCompile(`(?i)last\s*v(?:[^=]*?)=\s*([+\-0-9.eE]+)`)
	noConvIterRe = regexp.MustCompile(`(?i)iter(?:ation)?\s*=?\s*(\d+)`)
)

func parseConvergenceError(stderr string) *RunError {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "no convergence"),
		strings.Contains(low, "convergence problem"),
		strings.Contains(low, "iteration limit"),
		strings.Contains(low, "timestep too small"):
	default:
		return nil
	}
	r := &RunError{
		Kind:    "no_convergence",
		Message: firstMatchingLine(stderr, "convergence", "timestep too small", "iteration limit"),
		Hint:    "try .options gmin=1e-11 reltol=1e-3, lower the analysis step, or add a small resistor to ground at the offending node",
	}
	if m := noConvNodeRe.FindStringSubmatch(stderr); len(m) > 1 {
		r.Node = m[1]
	}
	if m := noConvVoltRe.FindStringSubmatch(stderr); len(m) > 1 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			r.LastVoltage = v
		}
	}
	if m := noConvIterRe.FindStringSubmatch(stderr); len(m) > 1 {
		if v, err := strconv.Atoi(m[1]); err == nil {
			r.Iteration = v
		}
	}
	return r
}

func firstMatchingLine(s string, needles ...string) string {
	for _, line := range strings.Split(s, "\n") {
		low := strings.ToLower(line)
		for _, n := range needles {
			if strings.Contains(low, strings.ToLower(n)) {
				return strings.TrimSpace(line)
			}
		}
	}
	return strings.TrimSpace(s)
}

func trimStderr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}

// emitError sends a terminal error frame, honouring ctx so a cancelled
// caller does not block the goroutine forever.
func emitError(ctx context.Context, frames chan<- Frame, runID string, e *RunError) {
	select {
	case frames <- Frame{RunID: runID, Err: e}:
	case <-ctx.Done():
	}
}

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
