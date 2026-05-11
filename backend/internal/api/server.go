package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"circuit-designer/backend/internal/circuit"
	"circuit-designer/backend/internal/engine"
	"circuit-designer/backend/internal/netlist"
	"circuit-designer/backend/internal/waveform"

	"github.com/gorilla/websocket"
)

// Server exposes the HTTP and WebSocket surface defined in DESIGN.md §3 and
// §11. Construct with New, mount under any prefix via Routes, and shut down
// active sessions via Close.
type Server struct {
	eng      engine.Engine
	library  LibraryProvider
	examples ExamplesProvider
	logger   *log.Logger

	upgrader websocket.Upgrader

	mu       sync.Mutex
	sessions map[*Session]struct{}
	closed   bool
}

// Options configures Server.New.
type Options struct {
	// Library overrides the default stub library. Production callers pass the
	// YAML/SPICE-backed loader; tests are happy with the stub.
	Library LibraryProvider

	// Examples overrides the default examples provider. When nil, no example
	// catalog is exposed and the /api/examples routes return 404 — callers
	// that want the bundled fixtures must inject NewDirExamples("examples").
	Examples ExamplesProvider

	// Logger receives WebSocket lifecycle and protocol-error messages.
	// Defaults to log.Default().
	Logger *log.Logger

	// CheckOrigin lets callers restrict WebSocket upgrades to specific
	// origins. The default permits any origin — appropriate for the
	// localhost dev tool Circuit Lab is today; production deployments that
	// expose the service publicly should override this.
	CheckOrigin func(*http.Request) bool
}

// New builds a Server backed by eng. eng must remain valid for the server's
// lifetime; closing the Server does not close the engine.
func New(eng engine.Engine, opts Options) *Server {
	if eng == nil {
		panic("api.New: nil engine")
	}
	library := opts.Library
	if library == nil {
		library = NewStubLibrary()
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	check := opts.CheckOrigin
	if check == nil {
		check = func(*http.Request) bool { return true }
	}
	return &Server{
		eng:      eng,
		library:  library,
		examples: opts.Examples,
		logger:   logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4 << 10,
			WriteBufferSize: 4 << 10,
			CheckOrigin:     check,
		},
		sessions: map[*Session]struct{}{},
	}
}

// Routes returns the http.Handler that serves the API. Register it under the
// root mux; all paths are absolute (DESIGN.md §10.6).
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/healthz", s.handleHealthz)
	mux.HandleFunc("/api/library", s.handleLibraryHTTP)
	mux.HandleFunc("/api/library/import", s.handleLibraryImportHTTP)
	mux.HandleFunc("/api/library/import-archive", s.handleLibraryImportArchiveHTTP)
	mux.HandleFunc("/api/circuit/parse", s.handleCircuitParse)
	mux.HandleFunc("/api/circuit/emit", s.handleCircuitEmit)
	mux.HandleFunc("/api/circuit/export", s.handleCircuitExport)
	mux.HandleFunc("/api/waveform/import", s.handleWaveformImport)
	mux.HandleFunc("/api/examples", s.handleExamplesList)
	mux.HandleFunc("/api/examples/", s.handleExamplesLoad)
	mux.HandleFunc("/ws", s.handleWebSocket)
	return mux
}

// Close terminates every live session. Safe to call concurrently with active
// requests; in-flight upgrades after Close are rejected with 503.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	sessions := make([]*Session, 0, len(s.sessions))
	for sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()

	for _, sess := range sessions {
		_ = sess.Close()
	}
	return nil
}

// --- HTTP handlers ----------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
	})
}

func (s *Server) handleLibraryHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	out := LibraryListPayload{
		Filter:     r.URL.Query().Get("filter"),
		Components: s.library.List(r.URL.Query().Get("filter")),
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLibraryImportHTTP accepts a SPICE .lib body as JSON and forwards it to
// the LibraryProvider. Returns the freshly-discovered components so the
// frontend can update its palette in one round-trip. Mirrors the WebSocket
// OpLibraryImport flow for clients that prefer REST.
func (s *Server) handleLibraryImportHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	var p LibraryImportPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	libFile, imported, updated, err := s.library.Import(p.Filename, p.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "import", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, LibraryImportResultPayload{
		LibFile:  libFile,
		Imported: imported,
		Updated:  updated,
	})
}

// handleLibraryImportArchiveHTTP ingests a .zip pack of .lib + .asy files via
// multipart/form-data and forwards it to the LibraryProvider. The archive is
// the canonical bulk-import path for vendor packs (Würth, Coilcraft, etc.)
// where dozens of .libs ship alongside hundreds of .asy companions; per-file
// uploads of those packs would be unusable. Body cap is 32 MB so the Würth
// passive components pack (~9 MB) fits comfortably with headroom.
func (s *Server) handleLibraryImportArchiveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", "missing form field 'file'")
		return
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 32<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	libFile, imported, updated, warnings, err := s.library.ImportArchive(header.Filename, body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "import", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, LibraryImportResultPayload{
		LibFile:  libFile,
		Imported: imported,
		Updated:  updated,
		Warnings: warnings,
	})
}

// handleCircuitParse accepts SPICE source as the request body and returns the
// parsed Circuit. Used by the Netlist tab's live re-parse on edit.
func (s *Server) handleCircuitParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read_body", err.Error())
		return
	}
	c, err := netlist.Parse(bytes.NewReader(body))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "parse", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handleExamplesList returns the catalog of bundled example circuits as JSON.
// When no ExamplesProvider is configured, returns an empty list rather than
// 404 so the frontend's "open example" dropdown can render an empty state
// without distinguishing "no examples" from "not implemented".
func (s *Server) handleExamplesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if s.examples == nil {
		writeJSON(w, http.StatusOK, ExamplesListPayload{Examples: []ExampleSummary{}})
		return
	}
	writeJSON(w, http.StatusOK, ExamplesListPayload{Examples: s.examples.List()})
}

// handleExamplesLoad parses one bundled fixture and returns it as a Circuit
// JSON body. Path: /api/examples/{name}, where name is the .cir basename
// without extension. Validation lives in dirExamples.Load to keep the file
// system access centralised.
func (s *Server) handleExamplesLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	if s.examples == nil {
		writeJSONError(w, http.StatusNotFound, "no_examples", "examples are not configured")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/examples/")
	c, err := s.examples.Load(name)
	if err != nil {
		if errors.Is(err, ErrExampleNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "load", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// handleCircuitExport accepts a Circuit JSON body and returns SPICE source
// translated for the requested dialect (`?target=ngspice|berkeley3|ltspice|
// kicad`). Backs the Netlist tab's Export menu (DESIGN.md §10.5).
func (s *Server) handleCircuitExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	target := netlist.Dialect(r.URL.Query().Get("target"))
	if target == "" {
		target = netlist.DialectNgspice
	}
	known := false
	for _, d := range netlist.Dialects {
		if d == target {
			known = true
			break
		}
	}
	if !known {
		writeJSONError(w, http.StatusBadRequest, "bad_target", fmt.Sprintf("unknown export target %q", target))
		return
	}
	var c circuit.Circuit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	var sb strings.Builder
	if err := netlist.EmitDialect(&c, target, &sb); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "emit", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, sb.String())
}

// handleWaveformImport ingests a CSV or WAV file via multipart/form-data and
// returns the decoded (t,v) point list plus inferred sample rate. The
// frontend stores the result on the active V/I source's SourceSpec.Params so
// the next netlist emit lowers it to a PWL transient spec (DESIGN.md §7.1
// "arb" / "pwl"). Body cap is 16 MB so a moderate WAV (e.g. 30 s mono 48 kHz
// 16-bit ≈ 2.9 MB) fits comfortably.
func (s *Server) handleWaveformImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", "missing form field 'file'")
		return
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	peak := 1.0
	if v := r.FormValue("peak"); v != "" {
		if p, err := strconv.ParseFloat(v, 64); err == nil && p > 0 {
			peak = p
		}
	}
	rate := 0.0
	if v := r.FormValue("sample_rate"); v != "" {
		if p, err := strconv.ParseFloat(v, 64); err == nil && p > 0 {
			rate = p
		}
	}
	dec, err := waveform.Decode(header.Filename, body, waveform.DecodeOptions{
		Peak:           peak,
		SampleRateHint: rate,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "import", err.Error())
		return
	}
	flat := make([]float64, 0, len(dec.Points)*2)
	var sb strings.Builder
	for i, p := range dec.Points {
		flat = append(flat, p[0], p[1])
		if i > 0 {
			sb.WriteByte(';')
		}
		sb.WriteString(strconv.FormatFloat(p[0], 'g', -1, 64))
		sb.WriteByte(':')
		sb.WriteString(strconv.FormatFloat(p[1], 'g', -1, 64))
	}
	writeJSON(w, http.StatusOK, WaveformImportResultPayload{
		Name:         dec.Name,
		SampleRate:   dec.SampleRate,
		Duration:     dec.Duration,
		PointCount:   len(dec.Points),
		Points:       flat,
		PointsString: sb.String(),
	})
}

// handleCircuitEmit accepts a Circuit JSON body and returns SPICE source.
// Used by the Netlist tab's "regenerate from schematic" action.
func (s *Server) handleCircuitEmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
		return
	}
	var c circuit.Circuit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "decode", err.Error())
		return
	}
	src, err := emitNetlist(&c)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "emit", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, src)
}

// --- WebSocket --------------------------------------------------------------

// pongWait is how long we wait for a pong before declaring the conn dead.
// pingInterval is when we send pings; must be < pongWait.
const (
	pongWait     = 60 * time.Second
	pingInterval = 30 * time.Second
	writeWait    = 10 * time.Second
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		http.Error(w, "server closing", http.StatusServiceUnavailable)
		return
	}
	s.mu.Unlock()

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrader has already written an error response.
		s.logger.Printf("ws upgrade: %v", err)
		return
	}

	sender := newWSSender(conn)
	session := NewSession(s.eng, s.library, sender)

	s.mu.Lock()
	s.sessions[session] = struct{}{}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Pong handler resets the read deadline on every pong; without it a stuck
	// client times out after pongWait.
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Writer goroutine owns conn writes (gorilla requires this) and emits
	// keepalive pings on a fixed interval.
	go sender.run(ctx, pingInterval, writeWait)

	defer func() {
		sender.close()
		_ = session.Close()
		_ = conn.Close()
		s.mu.Lock()
		delete(s.sessions, session)
		s.mu.Unlock()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !isExpectedCloseErr(err) {
				s.logger.Printf("ws read: %v", err)
			}
			return
		}
		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			_ = sender.Send(mustEnvelope(OpError, "", ErrorPayload{
				Code:    ErrCodeBadJSON,
				Message: err.Error(),
			}))
			continue
		}
		if err := session.Handle(ctx, env); err != nil {
			s.logger.Printf("ws session.Handle(op=%s id=%s): %v", env.Op, env.ID, err)
			return
		}
	}
}

// isExpectedCloseErr returns true for the close codes a well-behaved client
// might generate — used to keep the log quiet on normal disconnects.
func isExpectedCloseErr(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
	) {
		return true
	}
	// gorilla wraps timeouts in a custom net.Error; treat read-deadline
	// expiry on a broken conn as expected too.
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	if strings.Contains(err.Error(), "use of closed network connection") {
		return true
	}
	return false
}

// --- WebSocket sender -------------------------------------------------------

// wsSender serialises writes to a *websocket.Conn through a single writer
// goroutine. Send marshals and enqueues; run drains the queue and writes.
type wsSender struct {
	conn  *websocket.Conn
	queue chan []byte

	closeOnce sync.Once
	closed    chan struct{}
}

func newWSSender(conn *websocket.Conn) *wsSender {
	return &wsSender{
		conn:   conn,
		queue:  make(chan []byte, 64),
		closed: make(chan struct{}),
	}
}

// Send enqueues env for the writer goroutine. Returns an error if the sender
// has been closed.
func (s *wsSender) Send(env Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	select {
	case <-s.closed:
		return errors.New("ws sender closed")
	case s.queue <- data:
		return nil
	}
}

// close signals the writer goroutine to drain and exit. Idempotent.
func (s *wsSender) close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		close(s.queue)
	})
}

// run drains s.queue, writing each message as a text frame. Sends ping frames
// every pingInterval to keep the connection alive. Exits when ctx is done or
// the queue is closed.
func (s *wsSender) run(ctx context.Context, pingInterval, writeWait time.Duration) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flushClose()
			return
		case <-ticker.C:
			_ = s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case data, ok := <-s.queue:
			if !ok {
				s.flushClose()
				return
			}
			_ = s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}
}

// flushClose sends a courtesy close frame; ignore failures.
func (s *wsSender) flushClose() {
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = s.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)
}

// --- small json helpers -----------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorPayload{Code: code, Message: msg})
}
