// Command release previews the maintainer release flow independently of the viewer.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"mapbox-tui/internal/release"
)

func main() {
	mode := flag.String("mode", "demo", "execution mode (currently demo only)")
	version := flag.String("version", "", "initial version to preview and edit (default: next available increment)")
	scenario := flag.String("scenario", "success", release.Scenarios)
	delay := flag.Duration("delay", time.Second, "demo delay per simulated unit of work")
	flag.Parse()
	if *mode != "demo" || *delay <= 0 || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Only --mode=demo is implemented; --delay must be positive; positional arguments are not accepted.")
		os.Exit(2)
	}
	session, err := release.NewSession(*version, *scenario)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	model := release.NewModel(session, *delay, os.Getenv("NO_COLOR") != "")
	final, err := tea.NewProgram(model).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(final.(release.Model).Summary())
}
