package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/filepicker"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFilePickerDistinguishesLoadingEmptyAndFailedReads(t *testing.T) {
	for _, mode := range []string{"track", "browse", "locate-browse"} {
		t.Run(mode, func(t *testing.T) {
			m := themeViewer(t, 70, 24, false)
			m.openDialog(dialogFiles)
			if mode != "track" {
				m.ui.dialog, m.savedWork.mode = dialogSavedWorkspace, mode
			}
			m.ui.picker = filepicker.New()
			m.ui.picker.CurrentDirectory = filepath.Join(t.TempDir(), "missing")
			m.stylePicker()
			cmd := m.readFilePicker(m.ui.picker.Init())
			for _, height := range []int{1, 2, 10} {
				body := ansi.Strip(m.filePickerContent(30, height))
				if !strings.Contains(body, "* Reading folder") || strings.Contains(body, "No files") {
					t.Fatal("pending read looked empty", body)
				}
			}
			m.Update(cmd())
			if m.ui.pickerBusy || m.ui.pickerError == "" ||
				!strings.Contains(ansi.Strip(m.filePickerContent(30, 10)), "Could not read folder") ||
				!strings.Contains(m.filePickerHints(""), "r retry") {
				t.Fatal("directory failure had no visible recovery", m.ui.pickerError)
			}
			if err := os.Mkdir(m.ui.picker.CurrentDirectory, 0700); err != nil {
				t.Fatal(err)
			}
			_, cmd = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
			if cmd == nil || !m.ui.pickerBusy {
				t.Fatal("retry did not start a new read")
			}
			m.Update(cmd())
			if m.ui.pickerBusy || m.ui.pickerError != "" ||
				!strings.Contains(ansi.Strip(m.filePickerContent(30, 10)), "No files in this directory") {
				t.Fatal("successful empty read retained an error", m.filePickerContent(30, 10))
			}
		})
	}
}

func TestFilePickerRejectsLateResultsAfterReopening(t *testing.T) {
	m := themeViewer(t, 70, 24, false)
	old := m.openDialog(dialogFiles)
	m.closeDialog()
	current := m.openDialog(dialogFiles)
	request := m.ui.pickerRequest
	m.Update(old())
	if !m.ui.pickerBusy || m.ui.pickerRequest != request {
		t.Fatal("old directory read completed the current picker")
	}
	m.Update(current())
	if m.ui.pickerBusy || m.ui.pickerError != "" {
		t.Fatal("current directory read was not accepted")
	}
}

func TestFilePickerRetainsSelectionAndScrollAfterNestedCommands(t *testing.T) {
	for _, mode := range []string{"track", "browse", "locate-browse"} {
		t.Run(mode, func(t *testing.T) {
			m := themeViewer(t, 70, 24, false)
			m.openDialog(dialogFiles)
			if mode != "track" {
				m.ui.dialog, m.savedWork.mode = dialogSavedWorkspace, mode
			}
			m.ui.picker.CurrentDirectory = t.TempDir()
			for i := range 25 {
				path := filepath.Join(m.ui.picker.CurrentDirectory, fmt.Sprintf("%02d.gpx", i))
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			m.Update(m.readFilePicker(m.ui.picker.Init())())
			m.filePickerContent(40, 7)
			for range 12 {
				m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			selected := m.ui.picker.HighlightedPath()
			before := ansi.Strip(m.filePickerContent(40, 7))
			pending := m.readFilePicker(m.ui.picker.Init())
			m.pushDialog(dialogCommands)
			current := m.backDialog()
			m.Update(pending())
			if !m.ui.pickerBusy || current == nil {
				t.Fatal("returning did not restart the invalidated folder read")
			}
			m.Update(current())
			if selected != m.ui.picker.HighlightedPath() || ansi.Strip(m.filePickerContent(40, 7)) != before {
				t.Fatal("returning from commands lost file selection or scroll")
			}
		})
	}
}

func TestFilePickerRestoresValidSelectionAfterDirectoryShrinks(t *testing.T) {
	for _, mode := range []string{"track", "browse", "locate-browse"} {
		for _, remaining := range []int{5, 0} {
			t.Run(fmt.Sprintf("%s/%d-files", mode, remaining), func(t *testing.T) {
				m := themeViewer(t, 70, 24, false)
				m.openDialog(dialogFiles)
				if mode != "track" {
					m.ui.dialog, m.savedWork.mode = dialogSavedWorkspace, mode
				}
				m.ui.picker.CurrentDirectory = t.TempDir()
				for i := range 25 {
					path := filepath.Join(m.ui.picker.CurrentDirectory, fmt.Sprintf("%02d.gpx", i))
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				m.Update(m.readFilePicker(m.ui.picker.Init())())
				for range 12 {
					m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				}
				m.pushDialog(dialogCommands)
				for i := remaining; i < 25; i++ {
					path := filepath.Join(m.ui.picker.CurrentDirectory, fmt.Sprintf("%02d.gpx", i))
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				m.Update(m.backDialog()())
				if remaining > 0 {
					want := filepath.Join(m.ui.picker.CurrentDirectory, "00.gpx")
					if got := m.ui.picker.HighlightedPath(); got != want {
						t.Fatalf("refreshed picker has invalid selection: got %q, want %q", got, want)
					}
				} else if !strings.Contains(ansi.Strip(m.filePickerContent(40, 7)), "No files in this directory") {
					t.Fatal("empty directory did not recover after refreshing")
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			})
		}
	}
}
