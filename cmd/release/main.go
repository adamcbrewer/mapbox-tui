// Command release guides maintainers through the shared release workflow.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"mapbox-tui/internal/release"
)

func run() error {
	modeFlag := flag.String("mode", "release", "release, rehearsal or test")
	version := flag.String("version", "", "initial editable version (default: next available increment)")
	scenario := flag.String("scenario", "success", release.Scenarios)
	delay := flag.Duration("delay", time.Second, "interactive test delay per operation")
	resume := flag.Bool("resume", false, "reconcile a previously interrupted real release or rehearsal")
	flag.Parse()
	mode := release.Mode(*modeFlag)
	if (mode != release.TestMode && mode != release.Rehearsal && mode != release.Live) || *delay <= 0 || flag.NArg() != 0 {
		return fmt.Errorf("choose --mode=release, rehearsal or test; --delay must be positive; no positional arguments")
	}
	inspectionCtx, stopInspection := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopInspection()
	ctx, cancel := context.WithTimeout(inspectionCtx, 10*time.Minute)
	defer cancel()
	var adapter release.Adapter
	var state release.State
	if mode == release.TestMode {
		if *resume {
			return fmt.Errorf("test mode is deterministic and starts fresh; --resume is for real runs")
		}
		fixture := release.FixtureAdapter{Scenario: *scenario}
		result, err := fixture.Execute(ctx, release.Request{Operation: release.Inspect}, func(string) {})
		if err != nil {
			return err
		}
		state = result.State
		adapter = fixture
	} else {
		if *scenario != "success" {
			return fmt.Errorf("--scenario applies only to test mode")
		}
		fmt.Println("Inspecting repository and release history (read-only GitHub requests); checking main before opening the UI…")
		runner := release.ExecRunner{}
		root, err := runner.Run(ctx, release.Command{Name: "git", Args: []string{"rev-parse", "--show-toplevel"}}, nil)
		if err != nil {
			return err
		}
		gitDir, err := runner.Run(ctx, release.Command{Name: "git", Args: []string{"rev-parse", "--absolute-git-dir"}, Dir: root}, nil)
		if err != nil {
			return err
		}
		journal, err := release.OpenJournal(filepath.Join(gitDir, "mapbox-release-"+string(mode)+".json"))
		if err != nil {
			return err
		}
		defer journal.Close()
		real := &release.RealAdapter{Mode: mode, Root: root, Runner: runner, Journal: journal, Resume: *resume}
		defer real.Cleanup()
		result, err := real.Execute(ctx, release.Request{Operation: release.Inspect}, func(line string) { fmt.Println(line) })
		if err != nil {
			return err
		}
		adapter, state = real, result.State
	}
	stopInspection()
	if *version != "" {
		if *resume && state.Version != *version {
			return fmt.Errorf("resume version is fixed at %s", state.Version)
		}
		state.Version = *version
	}
	session, err := release.NewSessionWithState(state, *scenario)
	if err != nil {
		return err
	}
	model := release.NewExecutingModel(session, adapter, *delay, os.Getenv("NO_COLOR") != "")
	defer model.Shutdown()
	final, err := tea.NewProgram(model).Run()
	if err != nil {
		return err
	}
	fmt.Println(final.(release.Model).Summary())
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
