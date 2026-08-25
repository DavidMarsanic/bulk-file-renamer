//go:build windows

package dialog

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ChooseFolder opens the native folder-browser dialog via PowerShell's
// System.Windows.Forms.FolderBrowserDialog and returns the chosen path,
// or "" if the user canceled.
func ChooseFolder() (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = "Choose a folder to rename files in"
if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
    Write-Output $dialog.SelectedPath
}`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
