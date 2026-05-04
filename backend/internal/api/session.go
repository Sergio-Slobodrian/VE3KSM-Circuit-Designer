package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"
	"circuit-designer/backend/internal/netlist"
)

// Sender pushes one envelope toward the connected client. Implementations
// must be safe for concurrent use; the WebSocket adapter serialises writes
// internally, the in-memory test adapter mutex-guards a slice.
//
// Send may return an error if the underlying transport has been closed; the
// session treats that as terminal and stops processing further envelopes.
type Sender interface {
	Send(Envelope) error
}

// LibraryProvider is the small surface the session needs from the component
// library. Milestone 3 ships a stub implementation in library.go;
// milestone 9 will replace it with the real YAML/SPICE loader (DESIGN.md §8)
// behind the same interface.
type LibraryProvider interface {
	List(filter string) []LibraryComponent
	Import(filename, body string) error
}

// Session holds per-connection state: the current Circuit, the active set of
// engine runs keyed by their originating envelope id, and a Sender used to
// push outbound frames. One Session per WebSocket connection.
//
// Session is safe for concurrent Handle calls in principle, but the WebSocket
// adapter dispatches one envelope at a time from a single read goroutine, so
// the only real concurrency is between the read goroutine and the per-run
// streamer goroutines.
type Session struct {
	eng     engine.Engine
	library LibraryProvider
	send    Sender

	mu      sync.Mutex
	circuit *circuit.Circuit
	runs    map[string]*runEntry
	closed  bool
}

type runEntry struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSession constructs a Session bound to eng, library, and sender. The
// caller drives the session by repeatedly invoking Handle with envelopes
// decoded from the wire, then Close when the connection terminates.
func NewSession(eng engine.Engine, library LibraryProvider, sender Sender) *Session {
	return &Session{
		eng:     eng,
		library: library,
		send:    sender,
		runs:    map[string]*runEntry{},
	}
}

// Handle dispatches one inbound envelope. Any error returned is a transport-
// level write failure; protocol-level errors are reported back to the client
// as ErrorPayload envelopes and Handle returns nil.
func (s *Session) Handle(ctx context.Context, env Envelope) error {
	switch env.Op {
	case OpCircuitLoad:
		return s.handleCircuitLoad(env, "load")
	case OpCircuitUpdate:
		return s.handleCircuitLoad(env, "update")
	case OpSimRun:
		return s.handleSimRun(ctx, env)
	case OpSimCancel:
		return s.handleSimCancel(env)
	case OpLibraryList:
		return s.handleLibraryList(env)
	case OpLibraryImport:
		return s.handleLibraryImport(env)
	case OpNetlistEmit:
		return s.handleNetlistEmit(env)
	default:
		return s.replyError(env, ErrCodeUnknownOp, fmt.Sprintf("unknown op %q", env.Op))
	}
}

// Close cancels every in-flight run and waits for the streaming goroutines to
// release. It marks the session closed so subsequent sim.run requests are
// rejected. Idempotent.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	entries := make([]*runEntry, 0, len(s.runs))
	for _, e := range s.runs {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	for _, e := range entries {
		e.cancel()
	}
	for _, e := range entries {
		<-e.done
	}
	return nil
}

// Circuit returns a pointer to the current server-side circuit, or nil if
// none has been loaded. Exposed for tests; production callers should rely on
// circuit.changed notifications instead.
func (s *Session) Circuit() *circuit.Circuit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.circuit
}

// --- handlers ---------------------------------------------------------------

func (s *Session) handleCircuitLoad(env Envelope, source string) error {
	var p CircuitLoadPayload
	if err := decodePayload(env, &p); err != nil {
		return s.replyError(env, ErrCodeBadPayload, err.Error())
	}
	if p.Circuit == nil && strings.TrimSpace(p.Netlist) == "" {
		return s.replyError(env, ErrCodeBadPayload, "circuit.load requires either 'circuit' or 'netlist'")
	}

	c := p.Circuit
	if c == nil {
		parsed, err := netlist.Parse(strings.NewReader(p.Netlist))
		if err != nil {
			return s.replyError(env, ErrCodeBadPayload, fmt.Sprintf("parse netlist: %v", err))
		}
		c = parsed
	}

	emitted, err := emitNetlist(c)
	if err != nil {
		return s.replyError(env, ErrCodeInternal, fmt.Sprintf("emit netlist: %v", err))
	}

	s.mu.Lock()
	s.circuit = c
	s.mu.Unlock()

	return s.reply(env, OpCircuitChanged, CircuitChangedPayload{
		Circuit: c,
		Netlist: emitted,
		Source:  source,
	})
}

func (s *Session) handleSimRun(ctx context.Context, env Envelope) error {
	var p SimRunPayload
	if err := decodePayload(env, &p); err != nil {
		return s.replyError(env, ErrCodeBadPayload, err.Error())
	}
	if env.ID == "" {
		return s.replyError(env, ErrCodeBadPayload, "sim.run requires a non-empty envelope id")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.replyError(env, ErrCodeInternal, "session closed")
	}
	c := s.circuit
	if c == nil {
		s.mu.Unlock()
		return s.replyError(env, ErrCodeNoCircuit, "no circuit loaded; send circuit.load first")
	}
	if _, dup := s.runs[env.ID]; dup {
		s.mu.Unlock()
		return s.replyError(env, ErrCodeBadPayload, fmt.Sprintf("run id %q is already active", env.ID))
	}
	s.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	frames, err := s.eng.Run(runCtx, c, p.Analysis)
	if err != nil {
		cancel()
		return s.replyError(env, ErrCodeBadPayload, err.Error())
	}

	entry := &runEntry{cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		// Drain so the engine goroutine can exit.
		go func() {
			for range frames {
			}
			close(entry.done)
		}()
		return s.replyError(env, ErrCodeInternal, "session closed")
	}
	s.runs[env.ID] = entry
	s.mu.Unlock()

	go s.streamRun(env.ID, entry, frames)
	return nil
}

// streamRun drains the engine's frame channel, forwards data frames as
// sim.result, terminal error frames as sim.error, and the channel-close as
// sim.done. Always cleans up the run entry and signals done.
func (s *Session) streamRun(id string, entry *runEntry, frames <-chan engine.Frame) {
	defer func() {
		entry.cancel()
		s.mu.Lock()
		delete(s.runs, id)
		s.mu.Unlock()
		close(entry.done)
	}()

	count := 0
	var runErr *engine.RunError
	for f := range frames {
		if f.Err != nil {
			runErr = f.Err
			continue
		}
		count++
		_ = s.send.Send(mustEnvelope(OpSimResult, id, SimResultPayload{Frame: f}))
	}

	if runErr != nil {
		_ = s.send.Send(mustEnvelope(OpSimError, id, SimErrorPayload{
			RunID: id,
			Error: runErr,
		}))
		return
	}
	_ = s.send.Send(mustEnvelope(OpSimDone, id, SimDonePayload{
		RunID:      id,
		FrameCount: count,
	}))
}

func (s *Session) handleSimCancel(env Envelope) error {
	var p SimCancelPayload
	if err := decodePayload(env, &p); err != nil {
		return s.replyError(env, ErrCodeBadPayload, err.Error())
	}
	if p.RunID == "" {
		return s.replyError(env, ErrCodeBadPayload, "sim.cancel requires run_id")
	}

	s.mu.Lock()
	entry, ok := s.runs[p.RunID]
	s.mu.Unlock()
	if !ok {
		return s.replyError(env, ErrCodeUnknownRun, fmt.Sprintf("no active run %q", p.RunID))
	}
	entry.cancel()
	return s.reply(env, OpAck, AckPayload{Of: OpSimCancel})
}

func (s *Session) handleLibraryList(env Envelope) error {
	var p LibraryListPayload
	if err := decodePayload(env, &p); err != nil {
		return s.replyError(env, ErrCodeBadPayload, err.Error())
	}
	out := LibraryListPayload{
		Filter:     p.Filter,
		Components: s.library.List(p.Filter),
	}
	return s.reply(env, OpLibraryList, out)
}

func (s *Session) handleLibraryImport(env Envelope) error {
	var p LibraryImportPayload
	if err := decodePayload(env, &p); err != nil {
		return s.replyError(env, ErrCodeBadPayload, err.Error())
	}
	if err := s.library.Import(p.Filename, p.Body); err != nil {
		return s.replyError(env, ErrCodeNotImplemented, err.Error())
	}
	return s.reply(env, OpAck, AckPayload{Of: OpLibraryImport})
}

func (s *Session) handleNetlistEmit(env Envelope) error {
	var p NetlistEmitPayload
	if err := decodePayload(env, &p); err != nil {
		return s.replyError(env, ErrCodeBadPayload, err.Error())
	}
	if p.Circuit == nil {
		return s.replyError(env, ErrCodeBadPayload, "netlist.emit requires 'circuit'")
	}
	src, err := emitNetlist(p.Circuit)
	if err != nil {
		return s.replyError(env, ErrCodeInternal, err.Error())
	}
	return s.reply(env, OpNetlistEmit, NetlistEmitResultPayload{Netlist: src})
}

// --- helpers ----------------------------------------------------------------

func (s *Session) reply(req Envelope, op string, payload any) error {
	env, err := newEnvelope(op, req.ID, payload)
	if err != nil {
		return s.replyError(req, ErrCodeInternal, err.Error())
	}
	return s.send.Send(env)
}

func (s *Session) replyError(req Envelope, code, message string) error {
	env, err := newEnvelope(OpError, req.ID, ErrorPayload{
		Code:    code,
		Message: message,
		Op:      req.Op,
	})
	if err != nil {
		// Marshalling a static struct should not fail; if it does, surface as
		// a transport error so the connection is torn down rather than silently
		// dropping the report.
		return fmt.Errorf("marshal error envelope: %w", err)
	}
	return s.send.Send(env)
}

func decodePayload(env Envelope, out any) error {
	if len(env.Payload) == 0 || bytes.Equal(env.Payload, []byte("null")) {
		return errors.New("missing payload")
	}
	if err := json.Unmarshal(env.Payload, out); err != nil {
		return fmt.Errorf("decode %s payload: %w", env.Op, err)
	}
	return nil
}

// mustEnvelope is the no-error variant used in streamRun where the payload
// types are statically known to marshal cleanly. A runtime failure would
// indicate a programming error and is allowed to propagate as a panic.
func mustEnvelope(op, id string, payload any) Envelope {
	env, err := newEnvelope(op, id, payload)
	if err != nil {
		panic(fmt.Sprintf("api: marshal envelope: %v", err))
	}
	return env
}

func emitNetlist(c *circuit.Circuit) (string, error) {
	var sb strings.Builder
	if err := netlist.Emit(c, &sb); err != nil {
		return "", err
	}
	return sb.String(), nil
}
