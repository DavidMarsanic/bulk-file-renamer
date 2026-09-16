// Command bulk-file-renamer renames many files at once, entirely on this
// machine, driven by a chain of find/replace, regex, numbering, date, and
// case rules with a live preview before anything touches disk and a
// one-click undo after. Bare invocation opens a local browser UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/DavidMarsanic/brightencode-appkit/browser"
	"github.com/DavidMarsanic/bulk-file-renamer/internal/server"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("bulk-file-renamer", flag.ContinueOnError)

	port := fs.Int("port", 0, "local UI server port (default: automatic)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() { printUsage(fs) }

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}

	if *showVersion {
		fmt.Println("bulk-file-renamer " + version)
		return 0
	}

	widenPATH()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(ctx)
	addr, err := srv.Start(*port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	fmt.Fprintln(os.Stderr, "Bulk File Renamer running at", addr, "— press Ctrl+C to quit")

	// When a host process (securexe-launcher) is the one showing the UI —
	// in its own native window, so it can get a real Dock identity instead
	// of a spawned Chrome window — it sets SECUREXE_HOSTED before starting
	// us and watches this same stderr line to discover the URL.
	// OpenIfNotHosted no-ops in that case; opening our own Chrome window
	// too would just leave a second, redundant one.
	if err := browser.OpenIfNotHosted("bulk-file-renamer", addr+"/"); err != nil {
		fmt.Fprintln(os.Stderr, "couldn't open a window automatically:", err)
		fmt.Fprintln(os.Stderr, "open this URL manually:", addr+"/")
	}

	<-ctx.Done()
	return 0
}

func printUsage(fs *flag.FlagSet) {
	fmt.Fprint(os.Stderr, `bulk-file-renamer — rename many files at once, entirely on this machine,
with a live preview before anything touches disk and one-click undo after.

Bare invocation opens a local browser UI: choose a folder, build a chain of
rename rules, review the preview, apply.

Usage:
  bulk-file-renamer          open the browser UI

Flags:
`)
	fs.PrintDefaults()
}

// widenPATH adds common tool-install directories that a GUI-launched
// process often lacks. macOS gives an app spawned outside a shell (Finder,
// Spotlight, or another GUI app like a Securexe-style launcher — anything
// that isn't a terminal) a bare PATH of /usr/bin:/bin:/usr/sbin:/sbin. This
// app has no external CLI dependencies of its own, but internal/dialog
// shells out to osascript/zenity/kdialog/powershell.exe for the native
// folder picker, so widening PATH here keeps this binary consistent with
// the rest of the family and cheap insurance against an unusual PATH.
func widenPATH() {
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/opt/homebrew/bin", "/opt/homebrew/sbin", // Apple Silicon Homebrew
		"/usr/local/bin", "/usr/local/sbin", // Intel Homebrew / common Linux
		filepath.Join(home, ".local", "bin"),
	}

	current := os.Getenv("PATH")
	existing := map[string]bool{}
	for _, p := range filepath.SplitList(current) {
		existing[p] = true
	}

	var toAdd []string
	for _, dir := range candidates {
		if dir == "" || existing[dir] {
			continue
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			toAdd = append(toAdd, dir)
		}
	}
	if len(toAdd) == 0 {
		return
	}
	toAdd = append(toAdd, current)
	os.Setenv("PATH", strings.Join(toAdd, string(os.PathListSeparator)))
}
