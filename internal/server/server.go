// Package server exposes the bulk-file-renamer engine over a small
// JSON+SSE HTTP API, bound to loopback only, for the embedded
// browser-based UI. Shared server plumbing (loopback bind, idle-timeout
// shutdown, job events/cancel routes) comes from brightencode-appkit;
// reveal/open are this app's own (gated by the "known path" allowlist
// below, not appkit's bare passthrough), and everything else is specific
// to renaming.
package server

import (
	"context"
	"net/http"
	"sync"

	appkit "github.com/DavidMarsanic/brightencode-appkit/server"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/engine"
	"github.com/DavidMarsanic/bulk-file-renamer/web"
)

type Server struct {
	*appkit.Server

	mu          sync.Mutex
	knownFolder string                        // the one folder /api/choose-folder most recently returned
	knownPaths  map[string]struct{}           // every file path a /api/list call has ever returned for the current folder
	entries     map[string]engine.FileEntry   // path -> cached listing entry, so /api/preview and /api/apply don't need to re-stat
	results     map[string]engine.BatchResult // jobID -> finished ApplyBatch result
}

func New(ctx context.Context) *Server {
	appkitSrv := appkit.New(ctx, 0)
	appkitSrv.SkipReveal = true
	appkitSrv.SkipOpen = true
	return &Server{
		Server:     appkitSrv,
		knownPaths: map[string]struct{}{},
		entries:    map[string]engine.FileEntry{},
		results:    map[string]engine.BatchResult{},
	}
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

func (s *Server) Start(port int) (string, error) {
	return s.Server.Start(port, web.Static, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/choose-folder", s.handleChooseFolder)
		mux.HandleFunc("POST /api/list", s.handleList)
		mux.HandleFunc("POST /api/preview", s.handlePreview)
		mux.HandleFunc("POST /api/apply", s.handleApply)
		mux.HandleFunc("GET /api/apply/{id}", s.handleApplyResult)
		mux.HandleFunc("POST /api/undo", s.handleUndo)
		mux.HandleFunc("POST /api/reveal", s.handleReveal)
		mux.HandleFunc("POST /api/open", s.handleOpen)
	})
}
