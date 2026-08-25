package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/DavidMarsanic/bulk-file-renamer/internal/browser"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/dialog"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/engine"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/jobs"
)

func (s *Server) handleChooseFolder(w http.ResponseWriter, r *http.Request) {
	path, err := dialog.ChooseFolder()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if path != "" {
		s.setKnownFolder(path)
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

// handleList lists the files directly in (or, recursively, beneath) a
// folder this server itself handed back from a prior choose-folder call.
// Every path it returns becomes "known" — usable by /api/preview,
// /api/apply, /api/reveal, and /api/open from here on.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Folder    string `json:"folder"`
		Recursive bool   `json:"recursive"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.isKnownFolder(req.Folder) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	entries, err := engine.ListFiles(req.Folder, req.Recursive)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.recordEntries(entries)
	writeJSON(w, http.StatusOK, map[string]any{"files": entries})
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Folder string        `json:"folder"`
		Files  []string      `json:"files"`
		Rules  []engine.Rule `json:"rules"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	entries, ok := s.resolveEntries(req.Folder, req.Files)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	results, err := engine.PreviewBatch(entries, req.Rules)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "bad-request"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "collisionCount": countCollisions(results)})
}

// handleApply re-validates and re-computes the preview server-side (never
// trusting the client's word that it's collision-free) before performing
// the actual on-disk rename as a background job.
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Folder string        `json:"folder"`
		Files  []string      `json:"files"`
		Rules  []engine.Rule `json:"rules"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	entries, ok := s.resolveEntries(req.Folder, req.Files)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no files selected", "code": "bad-request"})
		return
	}

	previews, err := engine.PreviewBatch(entries, req.Rules)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "bad-request"})
		return
	}
	if countCollisions(previews) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot apply: unresolved naming collisions", "code": "bad-request"})
		return
	}

	plan := make([]engine.RenamePair, 0, len(previews))
	for _, p := range previews {
		to := filepath.Join(filepath.Dir(p.Path), p.NewName)
		if to == p.Path {
			continue // name is unchanged under these rules — nothing to do
		}
		plan = append(plan, engine.RenamePair{From: p.Path, To: to})
	}
	if len(plan) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no files would be renamed by these rules", "code": "bad-request"})
		return
	}

	job, _ := s.Jobs.Create(s.ctx)
	folder := req.Folder

	go func() {
		result, err := engine.ApplyBatch(folder, plan)
		s.storeResult(job.ID, result)
		if err != nil {
			job.Publish(jobs.Event{Stage: "error", Message: err.Error()})
			return
		}
		job.Publish(jobs.Event{Stage: "done", Message: fmt.Sprintf("%d file(s) renamed", len(result.Renamed))})
	}()

	writeJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}

func (s *Server) handleApplyResult(w http.ResponseWriter, r *http.Request) {
	result, ok := s.getResult(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	job, ok := s.Jobs.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := job.Subscribe()
	defer cancel()

	for {
		select {
		case e, open := <-ch:
			if !open {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			if e.Stage == "done" || e.Stage == "error" || e.Stage == "canceled" {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// handleUndo reverses a previously applied batch. If the undo log itself
// can't be found or read, that's reported as a plain bad request (nothing
// on disk was touched); any failure partway through the reversal comes
// back inside the normal result body, same shape as a successful undo.
func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BatchID string `json:"batchId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.BatchID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing batchId", "code": "bad-request"})
		return
	}

	result, err := engine.UndoBatch(req.BatchID)
	if err != nil && result.BatchID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "bad-request"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"undone": result.Renamed, "errors": result.Errors})
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.isRevealable(req.Path) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	if err := browser.Reveal(req.Path); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.isRevealable(req.Path) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	if err := browser.Open(req.Path); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func countCollisions(results []engine.PreviewResult) int {
	n := 0
	for _, r := range results {
		if r.Collision {
			n++
		}
	}
	return n
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body", "code": "bad-request"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
