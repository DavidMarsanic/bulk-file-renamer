// Package server exposes the bulk-file-renamer engine over a small
// JSON+SSE HTTP API, bound to loopback only, for the embedded
// browser-based UI.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DavidMarsanic/bulk-file-renamer/internal/engine"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/jobs"
	"github.com/DavidMarsanic/bulk-file-renamer/web"
)

// idleTimeout is the only auto-shutdown mechanism: if nothing has hit the
// server in this long AND no rename batch is actively running, the process
// exits.
const idleTimeout = 30 * time.Minute

type Server struct {
	Jobs *jobs.Registry
	ctx  context.Context

	mu          sync.Mutex
	knownFolder string                        // the one folder /api/choose-folder most recently returned
	knownPaths  map[string]struct{}           // every file path a /api/list call has ever returned for the current folder
	entries     map[string]engine.FileEntry   // path -> cached listing entry, so /api/preview and /api/apply don't need to re-stat
	results     map[string]engine.BatchResult // jobID -> finished ApplyBatch result

	lastActivity atomic.Int64
}

func New(ctx context.Context) *Server {
	s := &Server{
		ctx:        ctx,
		Jobs:       jobs.NewRegistry(),
		knownPaths: map[string]struct{}{},
		entries:    map[string]engine.FileEntry{},
		results:    map[string]engine.BatchResult{},
	}
	s.touch()
	return s
}

// setKnownFolder records folder as the only folder this server will act on
// until the next choose-folder call. A fresh folder invalidates every path
// learned from the previous one, since it came from a different listing.
func (s *Server) setKnownFolder(folder string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.knownFolder = folder
	s.knownPaths = map[string]struct{}{}
	s.entries = map[string]engine.FileEntry{}
}

func (s *Server) isKnownFolder(folder string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return folder != "" && folder == s.knownFolder
}

// recordEntries marks every entry's path as known (safe for /api/preview,
// /api/apply, /api/reveal, /api/open to act on) and caches the entry
// itself so later calls don't need to re-stat the file.
func (s *Server) recordEntries(entries []engine.FileEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		s.knownPaths[e.Path] = struct{}{}
		s.entries[e.Path] = e
	}
}

func (s *Server) isKnownPath(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.knownPaths[path]
	return ok
}

func (s *Server) getEntry(path string) (engine.FileEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[path]
	return e, ok
}

// resolveEntries validates folder against the known folder and every path
// in files against knownPaths, returning their cached FileEntry values in
// the same order — the shared gate behind /api/preview and /api/apply.
func (s *Server) resolveEntries(folder string, files []string) ([]engine.FileEntry, bool) {
	if !s.isKnownFolder(folder) {
		return nil, false
	}
	entries := make([]engine.FileEntry, 0, len(files))
	for _, f := range files {
		e, ok := s.getEntry(f)
		if !ok {
			return nil, false
		}
		entries = append(entries, e)
	}
	return entries, true
}

// isRevealable is the gate for /api/reveal and /api/open: either a known
// individual file, or the known folder itself.
func (s *Server) isRevealable(path string) bool {
	s.mu.Lock()
	knownFolder := s.knownFolder
	_, isFile := s.knownPaths[path]
	s.mu.Unlock()
	return path != "" && (path == knownFolder || isFile)
}

func (s *Server) storeResult(jobID string, result engine.BatchResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[jobID] = result
}

func (s *Server) getResult(jobID string) (engine.BatchResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[jobID]
	return r, ok
}

// Start binds 127.0.0.1:port (port 0 picks any free port — this UI is
// never exposed beyond loopback) and serves until the process exits.
func (s *Server) Start(port int) (string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", fmt.Errorf("starting local server: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/choose-folder", s.handleChooseFolder)
	mux.HandleFunc("POST /api/list", s.handleList)
	mux.HandleFunc("POST /api/preview", s.handlePreview)
	mux.HandleFunc("POST /api/apply", s.handleApply)
	mux.HandleFunc("GET /api/jobs/{id}/events", s.handleJobEvents)
	mux.HandleFunc("GET /api/apply/{id}", s.handleApplyResult)
	mux.HandleFunc("POST /api/undo", s.handleUndo)
	mux.HandleFunc("POST /api/reveal", s.handleReveal)
	mux.HandleFunc("POST /api/open", s.handleOpen)
	mux.Handle("GET /", http.FileServer(http.FS(web.Static)))

	httpSrv := &http.Server{Handler: s.trackActivity(mux)}
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	go s.watchIdle()

	return "http://" + ln.Addr().String(), nil
}

func (s *Server) trackActivity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) touch() {
	s.lastActivity.Store(time.Now().Unix())
}

func (s *Server) watchIdle() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		idleFor := time.Now().Unix() - s.lastActivity.Load()
		if idleFor > int64(idleTimeout.Seconds()) && !s.Jobs.HasActive() {
			os.Exit(0)
		}
	}
}
