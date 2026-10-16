package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRunningEnterDoesNotScheduleMoreWork(t *testing.T) {
	s, _ := NewSession("v0.2.0", "success")
	m := NewModel(s, time.Second, true)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("start did not schedule work")
	}
	_, cmd = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("enter during work scheduled duplicate work")
	}
}

func TestViews(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{name: "normal", width: 90, height: 32},
		{name: "narrow", width: 40, height: 22},
	} {
		for _, state := range []string{"ready", "confirmation", "failure", "activity", "version-error"} {
			t.Run(size.name+"/"+state, func(t *testing.T) {
				s, _ := NewSession("v0.2.0", "success")
				switch state {
				case "confirmation":
					s.Step = Publish
					s.operation = 1
					s.URL = "https://example.invalid/releases/v0.2.0"
					s.enter()
				case "failure":
					s.Scenario = "auth-error"
					s.Start()
					s.Advance()
				case "activity":
					s.Start()
					s.Advance()
				}
				m := NewModel(s, time.Second, true)
				if state == "version-error" {
					m.versionError = "tag v0.1.0 already exists; existing tags cannot be overwritten"
				}
				updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				content := updated.View().Content
				if strings.Contains(content, "\x1b") {
					t.Fatal("NO_COLOR view contains escape sequences")
				}
				for _, line := range strings.Split(content, "\n") {
					if ansi.StringWidth(line) > size.width {
						t.Fatalf("line overflows: %q", line)
					}
				}
				if len(strings.Split(content, "\n")) > size.height {
					t.Fatal("view exceeds terminal height")
				}
				// Padding is checked above; omit invisible trailing cells from fixtures.
				lines := strings.Split(content, "\n")
				for i := range lines {
					lines[i] = strings.TrimRight(lines[i], " ")
				}
				content = strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
				path := filepath.Join("testdata", size.name+"-"+state+".txt")
				if os.Getenv("UPDATE_RELEASE_SNAPSHOTS") == "1" {
					if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if content != string(want) {
					t.Fatalf("snapshot differs: %s\n%s", path, content)
				}
			})
		}
	}
}

func TestEditableVersionAndNestedActivity(t *testing.T) {
	s, _ := NewSession("", "success")
	m := NewModel(s, time.Second, true)
	m.input.SetValue("v0.3.0")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || m.session.Status != Ready || m.versionError == "" {
		t.Fatal("invalid version started preparation")
	}
	m.input.SetValue("v1.0.0")
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || m.session.Version != "v1.0.0" || m.input.Value() != "" {
		t.Fatal("edited version not accepted or confirmation input not cleared")
	}
	for range 3 {
		updated, _ = m.Update(tickMsg{})
		m = updated.(Model)
	}
	content := m.checklist()
	if !strings.Contains(content, "✓ 1") || !strings.Contains(content, "Running formatting") ||
		strings.Contains(content, "CI passed for the selected commit") {
		t.Fatalf("completed section was not summarised: %s", content)
	}
	if !strings.Contains(ansi.Strip(m.header()), "1/9 steps complete") {
		t.Fatal("progress did not advance")
	}
}

func TestChecklistScrolling(t *testing.T) {
	s, _ := NewSession("", "success")
	s.Start()
	for s.Step != Tag {
		s.Advance()
	}
	s.Activity[Tag] = append(s.Activity[Tag], ActivityEntry{Text: strings.Repeat("operation output\n", 20), Status: ActivityNote})
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{name: "arrow", msg: tea.KeyPressMsg{Code: tea.KeyUp}},
		{name: "page", msg: tea.KeyPressMsg{Code: tea.KeyPgUp}},
		{name: "wheel", msg: tea.MouseWheelMsg{Button: tea.MouseWheelUp}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(s, time.Second, true)
			resized, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
			m = resized.(Model)
			before := m.viewport.YOffset()
			updated, _ := m.Update(tc.msg)
			m = updated.(Model)
			if m.viewport.YOffset() >= before {
				t.Fatal("scroll did not move overflowing checklist")
			}
			offset := m.viewport.YOffset()
			m.refresh()
			if m.viewport.YOffset() != offset {
				t.Fatal("redraw reset manual scroll")
			}
			m.input.SetValue("yes")
			updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = updated.(Model)
			if m.session.Step != Tag || m.session.Status != Running || !strings.Contains(m.viewport.View(), "Rechecking source") {
				t.Fatal("confirming next step did not bring it into view")
			}
		})
	}
}

func TestScrollHintsOnlyWhenNeeded(t *testing.T) {
	s, _ := NewSession("", "success")
	m := NewModel(s, time.Second, true)
	if strings.Contains(m.controls(), "scroll") {
		t.Fatal("scroll hint shown when checklist fits")
	}
	m.session.Activity[Preflight] = []ActivityEntry{{Text: strings.Repeat("line\n", 30), Status: ActivityRunning}}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 22})
	m = updated.(Model)
	if !strings.Contains(m.controls(), "scroll") {
		t.Fatal("missing overflow hint")
	}
	if strings.Contains(m.View().Content, "ctrl+l") || strings.Contains(m.View().Content, "ctrl+f") {
		t.Fatal("obsolete logs or follow shortcut displayed")
	}
}

func TestPastedInput(t *testing.T) {
	s, _ := NewSession("", "success")
	m := NewModel(s, time.Second, true)
	m.input.SetValue("")
	updated, _ := m.Update(tea.PasteMsg{Content: "v0.2.0"})
	updated, _ = updated.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.session.Version != "v0.2.0" || m.session.Status != Running {
		t.Fatal("pasted version was not accepted")
	}
	for m.session.Step != Tag {
		updated, _ = m.Update(tickMsg{})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.PasteMsg{Content: "yes"})
	m = updated.(Model)
	if m.session.Step != Tag || m.input.Value() != "yes" {
		t.Fatal("paste must fill confirmation without submitting it")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if updated.(Model).session.Step != Tag || updated.(Model).session.Status != Running {
		t.Fatal("pasted confirmation was not accepted")
	}
}

func TestShortTerminalKeepsConfirmationUsable(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{
		{name: "short", width: 90, height: 5},
		{name: "narrow", width: 35, height: 19},
	} {
		t.Run(size.name, func(t *testing.T) {
			s, _ := NewSession("v0.2.0", "success")
			s.Step = Publish
			s.operation = 1
			s.URL = "https://example.invalid/releases/v0.2.0"
			s.enter()
			m := NewModel(s, time.Second, true)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = updated.(Model)
			content := m.View().Content
			if strings.Contains(content, "enlarge terminal") || !strings.Contains(content, "[y/N]") || !strings.Contains(content, ">") {
				t.Fatalf("confirmation unavailable: %s", content)
			}
			if lipglossHeight := len(strings.Split(content, "\n")); lipglossHeight > size.height {
				t.Fatalf("view exceeds height: %s", content)
			}
			for _, line := range strings.Split(content, "\n") {
				if ansi.StringWidth(line) > size.width {
					t.Fatalf("line exceeds width: %q", line)
				}
			}
			updated, _ = m.Update(tea.PasteMsg{Content: "yes"})
			m = updated.(Model)
			updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if updated.(Model).session.Status != Running || cmd == nil {
				t.Fatal("visible confirmation did not accept input")
			}
		})
	}
}

func TestNotesReviewRetainsFullScrollableContent(t *testing.T) {
	s, _ := NewSession("v0.2.0", "success")
	s.Step, s.Status = Notes, Confirmation
	s.Notes = "Beginning: breaking changes\n" + strings.Repeat("Release detail\n", 40) + "End: limitations"
	m := NewModel(s, time.Second, true)
	m.width, m.height = 90, 19
	m.refresh()
	if !strings.Contains(m.checklist(), "Beginning: breaking changes") ||
		!strings.Contains(m.checklist(), "End: limitations") {
		t.Fatal("full notes are missing from review content")
	}
	m.follow = false
	m.viewport.GotoTop()
	if !strings.Contains(m.View().Content, "Beginning: breaking changes") {
		t.Fatal("cannot scroll to the start of the notes")
	}
	topView := strings.Split(m.View().Content, "\n")
	m.viewport.GotoBottom()
	bottomView := strings.Split(m.View().Content, "\n")
	footerRows := strings.Count(m.footer(), "\n")
	if strings.Join(topView[len(topView)-footerRows:], "\n") !=
		strings.Join(bottomView[len(bottomView)-footerRows:], "\n") {
		t.Fatal("question prompt moved when scrolling notes")
	}
	if !strings.Contains(m.View().Content, "End: limitations") {
		t.Fatal("cannot scroll to the end of the notes")
	}
	m.session.Notes = "Edited notes"
	if !strings.Contains(m.checklist(), "Edited notes") ||
		strings.Contains(m.checklist(), "Beginning: breaking changes") {
		t.Fatal("review did not update to the edited notes")
	}
}

func TestEmptyNotesStayEditable(t *testing.T) {
	s, _ := NewSession("v0.2.0", "success")
	s.Step, s.Status, s.Notes = Notes, Confirmation, "  \n "
	m := NewModel(s, time.Second, true)
	m.input.SetValue("y")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.session.Step != Notes || m.session.Status != Confirmation ||
		!strings.Contains(m.View().Content, "cannot be empty") {
		t.Fatal("empty notes escaped the editable review step")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	m = updated.(Model)
	if !m.editing {
		t.Fatal("notes editor is unavailable after validation failure")
	}
	m.session.Notes = "Reviewed release notes"
	m.session.Confirm("y")
	if m.session.Step != Publish || m.session.Status != Running {
		t.Fatal("corrected notes did not advance")
	}
}

func TestPublishedNotesCanBeRefreshedButNotEditedLocally(t *testing.T) {
	s, err := NewSession("v0.2.0", "success")
	if err != nil {
		t.Fatal(err)
	}
	s.Step, s.Status, s.NotesPublic = Notes, Confirmation, true
	s.Notes = "Published release notes"
	m := NewExecutingModel(s, FixtureAdapter{Scenario: "success"}, time.Second, true)
	defer m.Shutdown()
	view := m.View().Content
	if !strings.Contains(view, "Accept the published notes") || strings.Contains(view, "e: edit") {
		t.Fatalf("incorrect public notes prompt: %s", view)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	m = updated.(Model)
	if m.editing {
		t.Fatal("public notes editor should be on GitHub")
	}
	m.input.SetValue("")
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = updated.(Model)
	if cmd == nil || !m.busy || m.session.Status != Running || m.session.Request().Operation != ReadNotes {
		t.Fatal("refresh did not reload published notes")
	}
}
