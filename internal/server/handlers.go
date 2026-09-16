package server

import (
	"fmt"
	"net/http"
	"path/filepath"

	appkit "github.com/DavidMarsanic/brightencode-appkit/server"
	"github.com/DavidMarsanic/brightencode-appkit/jobs"
	"github.com/DavidMarsanic/brightencode-appkit/browser"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/dialog"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/engine"
)

func (s *Server) handleChooseFolder(w http.ResponseWriter, r *http.Request) {
	path, err := dialog.ChooseFolder()
	if err != nil {
		appkit.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if path != "" {
		s.setKnownFolder(path)
	}
	appkit.WriteJSON(w, http.StatusOK, map[string]string{"path": path})
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
	if !appkit.DecodeJSON(w, r, &req) {
		return
	}
	if !s.isKnownFolder(req.Folder) {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	entries, err := engine.ListFiles(req.Folder, req.Recursive)
	if err != nil {
		appkit.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.recordEntries(entries)
	appkit.WriteJSON(w, http.StatusOK, map[string]any{"files": entries})
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Folder string        `json:"folder"`
		Files  []string      `json:"files"`
		Rules  []engine.Rule `json:"rules"`
	}
	if !appkit.DecodeJSON(w, r, &req) {
		return
	}
	entries, ok := s.resolveEntries(req.Folder, req.Files)
	if !ok {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	results, err := engine.PreviewBatch(entries, req.Rules)
	if err != nil {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "bad-request"})
		return
	}
	appkit.WriteJSON(w, http.StatusOK, map[string]any{"results": results, "collisionCount": countCollisions(results)})
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
	if !appkit.DecodeJSON(w, r, &req) {
		return
	}
	entries, ok := s.resolveEntries(req.Folder, req.Files)
	if !ok {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	if len(entries) == 0 {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "no files selected", "code": "bad-request"})
		return
	}

	previews, err := engine.PreviewBatch(entries, req.Rules)
	if err != nil {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "bad-request"})
		return
	}
	if countCollisions(previews) > 0 {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot apply: unresolved naming collisions", "code": "bad-request"})
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
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "no files would be renamed by these rules", "code": "bad-request"})
		return
	}

	job, _ := s.Jobs.Create(s.Ctx)
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

	appkit.WriteJSON(w, http.StatusOK, map[string]string{"jobId": job.ID})
}

func (s *Server) handleApplyResult(w http.ResponseWriter, r *http.Request) {
	result, ok := s.getResult(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	appkit.WriteJSON(w, http.StatusOK, result)
}

// handleUndo reverses a previously applied batch. If the undo log itself
// can't be found or read, that's reported as a plain bad request (nothing
// on disk was touched); any failure partway through the reversal comes
// back inside the normal result body, same shape as a successful undo.
func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BatchID string `json:"batchId"`
	}
	if !appkit.DecodeJSON(w, r, &req) {
		return
	}
	if req.BatchID == "" {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "missing batchId", "code": "bad-request"})
		return
	}

	result, err := engine.UndoBatch(req.BatchID)
	if err != nil && result.BatchID == "" {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "bad-request"})
		return
	}
	appkit.WriteJSON(w, http.StatusOK, map[string]any{"undone": result.Renamed, "errors": result.Errors})
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !appkit.DecodeJSON(w, r, &req) {
		return
	}
	if !s.isRevealable(req.Path) {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	if err := browser.Reveal(req.Path); err != nil {
		appkit.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !appkit.DecodeJSON(w, r, &req) {
		return
	}
	if !s.isRevealable(req.Path) {
		appkit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown path", "code": "bad-request"})
		return
	}
	if err := browser.Open(req.Path); err != nil {
		appkit.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
