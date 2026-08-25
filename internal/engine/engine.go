package engine

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/DavidMarsanic/bulk-file-renamer/internal/paths"
)

// ListFiles returns every regular file directly inside folder (recursive
// false, via os.ReadDir) or anywhere beneath it (recursive true, via
// filepath.WalkDir) — directories themselves are never included. Every
// Path is absolute, and the result is sorted alphabetically by Path for
// deterministic ordering, since that ordering is what sequence numbering
// counts against.
func ListFiles(folder string, recursive bool) ([]FileEntry, error) {
	folder, err := filepath.Abs(folder)
	if err != nil {
		return nil, fmt.Errorf("resolving folder: %w", err)
	}

	var entries []FileEntry

	if recursive {
		err = filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			entries = append(entries, FileEntry{
				Path:    path,
				Name:    d.Name(),
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking folder: %w", err)
		}
	} else {
		dirEntries, err := os.ReadDir(folder)
		if err != nil {
			return nil, fmt.Errorf("reading folder: %w", err)
		}
		for _, d := range dirEntries {
			if d.IsDir() {
				continue
			}
			info, err := d.Info()
			if err != nil {
				return nil, fmt.Errorf("stat %s: %w", d.Name(), err)
			}
			entries = append(entries, FileEntry{
				Path:    filepath.Join(folder, d.Name()),
				Name:    d.Name(),
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// PreviewBatch computes, for every entry (in the given order — this order
// is what feeds seqIndex into ApplyRules), the name that rules would
// produce, and flags each row that would collide with either another
// entry's computed destination in this same batch or a pre-existing file
// on disk that isn't itself part of this batch.
func PreviewBatch(entries []FileEntry, rules []Rule) ([]PreviewResult, error) {
	results := make([]PreviewResult, len(entries))
	newPaths := make([]string, len(entries))
	originalPaths := make(map[string]bool, len(entries))
	for _, e := range entries {
		originalPaths[e.Path] = true
	}

	countByNewPath := map[string]int{}
	for i, e := range entries {
		newName, err := ApplyRules(e.Name, rules, i, e.ModTime)
		if err != nil {
			return nil, err
		}
		newPath := filepath.Join(filepath.Dir(e.Path), newName)
		newPaths[i] = newPath
		countByNewPath[newPath]++
		results[i] = PreviewResult{Path: e.Path, OldName: e.Name, NewName: newName}
	}

	for i, newPath := range newPaths {
		collision := countByNewPath[newPath] > 1
		if !collision {
			// A genuine external collision: something already sits at
			// newPath that isn't this same file and isn't another file in
			// this batch (which is what the two-phase apply exists to
			// handle safely, e.g. a straight two-file swap).
			if _, err := os.Lstat(newPath); err == nil && !originalPaths[newPath] {
				collision = true
			}
		}
		results[i].Collision = collision
	}

	return results, nil
}

// tempMove records one phase-1 rename (original -> temp path) so it can be
// rolled back if a later step in the batch fails.
type tempMove struct {
	index    int
	from     string
	tempPath string
}

// ApplyBatch performs plan as a single crash-safe unit: every file is first
// renamed to a temp name in its own directory (phase 1), then every temp
// name is renamed to its real destination (phase 2). Routing through a
// temp name first means the plan can freely contain destinations that
// overlap with other files' current names in the same batch — a straight
// two-file swap (a.txt<->b.txt) being the canonical case — without one
// rename ever clobbering a file that hasn't moved yet. Any failure in
// either phase rolls back everything already done in this batch, so a
// batch either fully applies or leaves every file exactly where it
// started.
func ApplyBatch(folder string, plan []RenamePair) (BatchResult, error) {
	batchID := fmt.Sprintf("%d", time.Now().UnixNano())

	completed := make([]tempMove, 0, len(plan))

	for i, pair := range plan {
		tempPath := filepath.Join(filepath.Dir(pair.From), fmt.Sprintf(".bfr-tmp-%d-%s", i, filepath.Base(pair.From)))
		if err := os.Rename(pair.From, tempPath); err != nil {
			errs := append([]string{fmt.Sprintf("renaming %s: %v", pair.From, err)}, rollbackPhase1(completed)...)
			return BatchResult{Errors: errs, BatchID: batchID}, fmt.Errorf("%w: %s: %v", ErrRenameFailed, pair.From, err)
		}
		completed = append(completed, tempMove{index: i, from: pair.From, tempPath: tempPath})
	}

	finalized := make([]tempMove, 0, len(completed))
	for _, tm := range completed {
		to := plan[tm.index].To
		if err := os.Rename(tm.tempPath, to); err != nil {
			errs := []string{fmt.Sprintf("renaming %s: %v", tm.tempPath, err)}
			for i := len(finalized) - 1; i >= 0; i-- {
				fm := finalized[i]
				finalTo := plan[fm.index].To
				if rbErr := os.Rename(finalTo, fm.tempPath); rbErr != nil {
					errs = append(errs, fmt.Sprintf("rollback: renaming %s back to %s: %v", finalTo, fm.tempPath, rbErr))
				}
			}
			errs = append(errs, rollbackPhase1(completed)...)
			return BatchResult{Errors: errs, BatchID: batchID}, fmt.Errorf("%w: %s: %v", ErrRenameFailed, tm.tempPath, err)
		}
		finalized = append(finalized, tm)
	}

	if err := writeUndoLog(batchID, plan); err != nil {
		// The renames themselves fully succeeded; losing the ability to
		// undo isn't worth reporting as a batch failure, just surfacing it.
		return BatchResult{Renamed: plan, BatchID: batchID, Errors: []string{fmt.Sprintf("writing undo log: %v", err)}}, nil
	}

	return BatchResult{Renamed: plan, BatchID: batchID}, nil
}

// rollbackPhase1 renames every completed phase-1 temp path back to its
// original From path, in reverse order, best-effort (a failed rollback
// step doesn't stop the rest from being attempted).
func rollbackPhase1(completed []tempMove) []string {
	var errs []string
	for i := len(completed) - 1; i >= 0; i-- {
		tm := completed[i]
		if err := os.Rename(tm.tempPath, tm.from); err != nil {
			errs = append(errs, fmt.Sprintf("rollback: renaming %s back to %s: %v", tm.tempPath, tm.from, err))
		}
	}
	return errs
}

// UndoBatch reverses a previously applied batch by reading its undo log,
// swapping From/To, and running it back through the same two-phase apply
// (so an undo is exactly as crash-safe as the original apply, including
// undoing a batch that itself contained a swap). The log is removed once
// the reversal succeeds.
func UndoBatch(batchID string) (BatchResult, error) {
	dir, err := paths.UndoLogDir()
	if err != nil {
		return BatchResult{}, err
	}
	logPath := filepath.Join(dir, batchID+".json")

	data, err := os.ReadFile(logPath)
	if err != nil {
		return BatchResult{}, fmt.Errorf("reading undo log for batch %s: %w", batchID, err)
	}
	var original []RenamePair
	if err := json.Unmarshal(data, &original); err != nil {
		return BatchResult{}, fmt.Errorf("parsing undo log for batch %s: %w", batchID, err)
	}

	reverse := make([]RenamePair, len(original))
	for i, p := range original {
		reverse[i] = RenamePair{From: p.To, To: p.From}
	}

	folder := ""
	if len(reverse) > 0 {
		folder = filepath.Dir(reverse[0].From)
	}

	result, err := ApplyBatch(folder, reverse)
	if err != nil {
		return result, err
	}

	if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
		result.Errors = append(result.Errors, fmt.Sprintf("removing undo log: %v", err))
	}

	return result, nil
}

func writeUndoLog(batchID string, plan []RenamePair) error {
	dir, err := paths.UndoLogDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, batchID+".json"), data, 0o644)
}
