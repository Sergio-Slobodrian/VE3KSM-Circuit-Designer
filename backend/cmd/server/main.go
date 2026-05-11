// Command server runs the Circuit Lab backend.
//
// On startup it locates ngspice on PATH, verifies the version, and starts an
// HTTP server that exposes the REST and WebSocket API. The same server also
// serves the built frontend (single-port, single-process production).
//
// Production: cd frontend && npm run build, then go run ./cmd/server, then
// browse http://localhost:8080.
//
// Development: run go run ./cmd/server and (in another terminal) cd frontend
// && npm run dev; browse http://localhost:5173 for HMR.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"circuit-designer/backend/internal/api"
	"circuit-designer/backend/internal/engine"
	"circuit-designer/backend/internal/library"
)

const listenAddr = ":8080"

// Candidate paths for the bundled assets, evaluated in order. Two layouts
// matter:
//
//   - `cd backend && go run ./cmd/server` (used by `make dev`) — relative
//     paths resolve from backend/, so "../frontend/dist" wins.
//   - `./bin/circuit-lab-server` from the project root (used by `make run`) —
//     CWD is the project root, so "frontend/dist" wins. We try both so the
//     binary is robust to either invocation without env-var ceremony.
var (
	frontendCandidates = []string{"frontend/dist", "../frontend/dist"}
	examplesCandidates = []string{"examples", "../examples"}
	libraryCandidates  = []string{"library", "../library"}
)

func main() {
	version, err := ngspiceVersion()
	if err != nil {
		log.Fatalf("ngspice not usable: %v", err)
	}

	// Run ngspice with cmd.Dir = examples/ so .LIB references in bundled
	// fixtures (e.g. `tubes_koren.lib` in preamp_12ax7.cir) resolve. Without
	// this, the engine's per-run private temp dir is used, no library file is
	// found, and ngspice errors out with "Could not find include file". The
	// library loader (m9) writes user-imported .lib files into this same
	// directory so freshly-imported subcircuits resolve too.
	examplesDir := resolveExamplesDir()
	eng := engine.NewWithOptions(engine.Options{WorkDir: examplesDir})
	defer func() { _ = eng.Close() }()

	libraryProvider := buildLibrary(examplesDir)

	apiSrv := api.New(eng, api.Options{
		Logger:   log.Default(),
		Examples: api.NewDirExamples(examplesDir),
		Library:  libraryProvider,
	})
	defer func() { _ = apiSrv.Close() }()
	apiHandler := apiSrv.Routes()

	mux := http.NewServeMux()
	// Liveness probe — kept at the bare /healthz path for container-orchestrator
	// defaults; /api/healthz (served by apiHandler) is the structured form.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok\nngspice %s\n", version)
	})
	// /api/* and /ws are owned by the API server; the inner mux re-resolves
	// the path so all of api.Server's routes light up here.
	mux.Handle("/api/", apiHandler)
	mux.Handle("/ws", apiHandler)

	if abs, ok := frontendBuilt(); ok {
		log.Printf("serving frontend from %s", abs)
		mux.Handle("/", spaHandler(abs))
	} else {
		log.Printf("frontend/dist not found; serving placeholder page")
		mux.Handle("/", placeholderHandler())
	}

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM: stop accepting new connections,
	// wait for in-flight handlers up to 10 s, then close engine and api.
	idleConnsClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		s := <-sig
		log.Printf("received %s; shutting down", s)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
		close(idleConnsClosed)
	}()

	// Bind explicitly first so we can log success only after the listener is
	// actually accepting connections — the previous "listening on …" line was
	// printed unconditionally and lied during a port-already-in-use restart,
	// which made it look like a successful boot when the bind had failed.
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("server: bind %s: %v", listenAddr, err)
	}
	log.Printf("circuit-lab backend listening on %s (engine: ngspice subprocess, %s)", ln.Addr(), version)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
	<-idleConnsClosed
}

func ngspiceVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ngspice", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("invoking ngspice: %w", err)
	}
	first := strings.SplitN(string(out), "\n", 2)[0]
	first = strings.TrimSpace(first)
	if first == "" {
		return "unknown", nil
	}
	return first, nil
}

// buildLibrary instantiates the YAML/SPICE-backed library provider. Falls
// back to the in-memory stub provider if the library directory cannot be
// found or fails to load, so a misconfigured deployment degrades to the
// previous milestone-3 behaviour rather than refusing to boot.
func buildLibrary(libDir string) api.LibraryProvider {
	root := resolveLibraryRoot()
	if root == "" {
		log.Printf("library: no manifests directory found; using in-memory stub palette")
		return api.NewStubLibrary()
	}
	loader := library.NewLoader(root, libDir)
	if err := loader.Reload(); err != nil {
		log.Printf("library: load %s: %v; falling back to stub palette", root, err)
		return api.NewStubLibrary()
	}
	count := len(loader.Snapshot().Components)
	log.Printf("library: loaded %d components from %s (lib dir: %s)", count, root, libDir)
	return api.NewLoadedLibrary(loader)
}

// resolveLibraryRoot picks the first library directory that exists.
func resolveLibraryRoot() string {
	for _, c := range libraryCandidates {
		if abs, err := filepath.Abs(c); err == nil {
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				return abs
			}
		}
	}
	return ""
}

// resolveExamplesDir picks the first examples directory that exists. Returns
// the first candidate as an absolute path when none match so the empty-list
// fallback in dirExamples stays the user-visible behaviour rather than a
// startup panic.
func resolveExamplesDir() string {
	for _, c := range examplesCandidates {
		if abs, err := filepath.Abs(c); err == nil {
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				return abs
			}
		}
	}
	abs, _ := filepath.Abs(examplesCandidates[0])
	return abs
}

// frontendBuilt returns the first candidate that contains an index.html.
func frontendBuilt() (string, bool) {
	for _, c := range frontendCandidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(abs, "index.html")); err == nil && !info.IsDir() {
			return abs, true
		}
	}
	return "", false
}

func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}

func placeholderHandler() http.Handler {
	body := `<!doctype html><meta charset=utf-8><title>Circuit Lab</title>
<style>
body{font:14px/1.6 system-ui,sans-serif;max-width:560px;margin:64px auto;padding:0 16px;color:#222}
code{background:#f3f1eb;padding:1px 5px;border-radius:3px;font-family:ui-monospace,Menlo,monospace}
h1{font-size:18px;font-weight:500}
</style>
<h1>Circuit Lab backend is up</h1>
<p>The frontend has not been built yet. Two options:</p>
<p><strong>Production (one process):</strong><br>
<code>cd frontend &amp;&amp; npm install &amp;&amp; npm run build</code><br>
then reload this page.</p>
<p><strong>Development (HMR):</strong><br>
<code>cd frontend &amp;&amp; npm install &amp;&amp; npm run dev</code><br>
then browse <a href="http://localhost:5173">http://localhost:5173</a>.</p>
<p style="color:#888;font-size:12px">API health: <a href="/healthz">/healthz</a></p>
`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	})
}
