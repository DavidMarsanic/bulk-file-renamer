//go:build darwin

// Package dialog opens the OS's native folder-choose dialog. A browser
// <input webkitdirectory> can pick a whole directory tree but never hands
// back a real filesystem path (sandboxed by design) — this app needs an
// actual path to scan on disk, so the picker has to come from the OS, not
// the page.
package dialog

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ChooseFolder opens the native Finder folder-choose dialog and returns
// the chosen absolute path, or "" if the user canceled.
func ChooseFolder() (string, error) {
	// "tell application \"Finder\" to activate" first: without it, the
	// dialog is owned by a background process (this binary has no Dock
	// icon/foreground app status of its own), so macOS can open it
	// behind the Chrome app-mode window instead of in front — it looks
	// exactly like the button did nothing.
	cmd := exec.Command("osascript",
		"-e", `tell application "Finder" to activate`,
		"-e", `POSIX path of (choose folder with prompt "Choose a folder to rename files in")`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "User canceled") {
			return "", nil
		}
		return "", fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
