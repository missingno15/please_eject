// please_eject finds and stops the programs that keep a macOS external volume
// from ejecting, then confirms the volume is safe to remove.
//
// Usage:
//
//	please_eject "My Passport for Mac"
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"please_eject/internal/tui"
	"please_eject/internal/volume"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" {
		usage()
		os.Exit(exitCode(len(os.Args) < 2))
	}

	name := os.Args[1]

	vol, err := volume.Resolve(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "please_eject: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nMounted volumes:\n")
		listVolumes(os.Stderr)
		os.Exit(1)
	}

	p := tea.NewProgram(tui.New(vol))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "please_eject: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `please_eject — free a stuck external volume so it can eject.

Usage:
  please_eject "<volume name>"

Example:
  please_eject "My Passport for Mac"

It lists the programs holding the volume open, lets you stop them
(all at once or one-by-one), then rechecks whether it's safe to eject.
`)
}

func exitCode(missingArg bool) int {
	if missingArg {
		return 1
	}
	return 0
}

// listVolumes prints the currently mounted volumes to help the user pick the
// right name when resolution fails.
func listVolumes(w *os.File) {
	entries, err := os.ReadDir("/Volumes")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(w, "  • %s\n", filepath.Base(e.Name()))
		}
	}
}
