// Package engine implements bulk-file-renamer's core logic: listing files,
// applying a chain of renaming rules to compute a preview, and performing
// the actual on-disk rename as a two-phase, crash-safe, reversible batch.
// No third-party dependencies — everything here is Go stdlib.
package engine

import (
	"errors"
	"time"
)

// FileEntry describes one file found by ListFiles.
type FileEntry struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Rule is one step in a renaming chain. Only the fields relevant to Type
// are meaningful; the rest are left at their zero value.
type Rule struct {
	Type          string `json:"type"` // "find-replace" | "regex-replace" | "sequence" | "date" | "case" | "trim" | "remove-chars" | "extension"
	Find          string `json:"find,omitempty"`
	Replace       string `json:"replace,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	Position      string `json:"position,omitempty"` // "prefix" | "suffix" — for sequence/date
	Start         int    `json:"start,omitempty"`    // sequence
	Padding       int    `json:"padding,omitempty"`  // sequence, e.g. 3 -> "001"
	Step          int    `json:"step,omitempty"`     // sequence, default 1
	Separator     string `json:"separator,omitempty"`
	DateSource    string `json:"dateSource,omitempty"` // "modified" | "today"
	DateFormat    string `json:"dateFormat,omitempty"` // Go time layout, e.g. "2006-01-02"
	CaseMode      string `json:"caseMode,omitempty"`   // "lower" | "upper" | "title"
	Chars         string `json:"chars,omitempty"`      // trim / remove-chars
	NewExtension  string `json:"newExtension,omitempty"`
}

// PreviewResult is one row of a computed rename preview.
type PreviewResult struct {
	Path      string `json:"path"`
	OldName   string `json:"oldName"`
	NewName   string `json:"newName"`
	Collision bool   `json:"collision"`
}

// RenamePair is one from -> to move, either a planned rename or the
// reverse of one recorded in an undo log.
type RenamePair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// BatchResult is the outcome of ApplyBatch or UndoBatch.
type BatchResult struct {
	Renamed []RenamePair `json:"renamed"`
	Errors  []string     `json:"errors"`
	BatchID string       `json:"batchId"`
}

var (
	ErrInvalidRegex = errors.New("invalid regular expression")
	ErrRenameFailed = errors.New("rename failed")
)
