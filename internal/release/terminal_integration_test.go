//go:build integration

package release

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Run by release_interactions.py in a PTY. Exercise the real viewer handoff
// independently of GitHub, using the built viewer and its offline fixture.
func TestViewerTerminalHandoff(t *testing.T) {
	root := os.Getenv("RELEASE_TERMINAL_ROOT")
	if root == "" {
		t.Skip("requires the release-interactions PTY driver")
	}
	session, err := NewSession("", "success")
	if err != nil {
		t.Fatal(err)
	}
	session.Mode = Rehearsal
	session.Root = root
	session.Binary = filepath.Join(root, "bin/mapbox-tui")
	session.Step = Viewer
	adapter := &RealAdapter{Mode: Rehearsal, Root: root}
	model := NewExecutingModel(session, adapter, time.Millisecond, true)
	defer model.Shutdown()
	final, err := tea.NewProgram(model).Run()
	if err != nil {
		t.Fatal(err)
	}
	if final.(Model).session.Status != Done {
		t.Fatal("viewer handoff did not complete")
	}
}
