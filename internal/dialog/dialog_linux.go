//go:build linux

package dialog

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ChooseFolder tries zenity (GNOME/most distros' default), then kdialog
// (KDE) — there's no single folder-picker binary guaranteed present on
// every Linux desktop, unlike macOS/Windows.
func ChooseFolder() (string, error) {
	if path, err := runPicker("zenity", "--file-selection", "--directory", "--title=Choose a folder to rename files in"); err == nil {
		return path, nil
	}
	if path, err := runPicker("kdialog", "--getexistingdirectory"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("no folder picker found — install zenity or kdialog")
}

func runPicker(name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", err
	}
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Nonzero exit here almost always just means the user hit Cancel —
		// treat it as "no folder chosen", not a reason to fail outright.
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}
