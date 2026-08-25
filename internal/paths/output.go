// Package paths resolves where this app's own bookkeeping data lives.
// Unlike most of this family of apps, bulk-file-renamer never produces a
// separate output file — it renames files in place, in whatever folder
// the user picked — so the only thing this package needs to resolve is
// where the undo logs for past batches are kept.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// UndoLogDir returns the directory that holds one JSON file per applied
// rename batch (named <batchId>.json), used by UndoBatch to reverse a
// batch after the fact. Created if it doesn't exist yet.
func UndoLogDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving cache directory: %w", err)
	}
	dir := filepath.Join(base, "bulk-file-renamer", "undo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating undo log directory: %w", err)
	}
	return dir, nil
}
