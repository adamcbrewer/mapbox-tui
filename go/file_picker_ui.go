package main

import (
	"errors"
	"io"
	"os"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type filePickerReady struct {
	request uint64
	msg     tea.Msg
	err     error
}

func (m *viewer) filePickerOpen() bool {
	return m.ui.dialog == dialogFiles || m.ui.dialog == dialogSavedWorkspace &&
		(m.savedWork.mode == "browse" || m.savedWork.mode == "locate-browse")
}

func (m *viewer) readFilePicker(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	m.ui.pickerRequest++
	request, path := m.ui.pickerRequest, m.ui.picker.CurrentDirectory
	m.ui.pickerBusy, m.ui.pickerError = true, ""
	return func() tea.Msg {
		// Bubbles' private directory errors aren't exposed by its update method.
		if err := readablePickerDirectory(path); err != nil {
			return filePickerReady{request: request, err: err}
		}
		return filePickerReady{request: request, msg: cmd()}
	}
}

func readablePickerDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	_, err = directory.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return errors.Join(err, directory.Close())
}

func (m *viewer) acceptFilePicker(result filePickerReady) tea.Cmd {
	if result.request != m.ui.pickerRequest || !m.filePickerOpen() {
		return nil
	}
	m.ui.pickerBusy = false
	m.dirty = true
	if result.err != nil {
		m.ui.pickerError = cleanPlaceText(result.err.Error())
		return nil
	}
	cmd := m.updateDialog(result.msg)
	if m.ui.picker.HighlightedPath() == "" {
		m.ui.picker, _ = m.ui.picker.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	}
	return cmd
}

func (m *viewer) updateFilePicker(msg tea.Msg) tea.Cmd {
	if pressed, ok := msg.(tea.KeyPressMsg); ok {
		if m.ui.pickerBusy {
			return nil
		}
		if m.ui.pickerError != "" {
			if pressed.String() == "r" {
				return m.readFilePicker(m.ui.picker.Init())
			}
			if !key.Matches(pressed, m.ui.picker.KeyMap.Back) {
				return nil
			}
		}
	}
	highlighted := m.ui.picker.HighlightedPath()
	var cmd tea.Cmd
	m.ui.picker, cmd = m.ui.picker.Update(msg)
	atEdge := highlighted != "" && highlighted == m.ui.picker.HighlightedPath()
	if pressed, ok := msg.(tea.KeyPressMsg); ok && atEdge {
		switch {
		case key.Matches(pressed, m.ui.picker.KeyMap.Up):
			m.ui.picker, _ = m.ui.picker.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
		case key.Matches(pressed, m.ui.picker.KeyMap.Down):
			m.ui.picker, _ = m.ui.picker.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
		}
	}
	return m.readFilePicker(cmd)
}

func (m *viewer) filePickerContent(width, height int) string {
	m.ui.picker.SetHeight(max(1, height-2))
	s := m.styles
	m.ui.picker.Styles.Selected = s.selected.Width(max(1, width-1)).MaxWidth(max(1, width-1))
	m.ui.picker.Styles.DisabledSelected = m.ui.picker.Styles.DisabledSelected.
		Width(max(1, width-1)).MaxWidth(max(1, width-1))
	m.ui.picker.Styles.File = s.dialogText.Width(max(1, width-2)).MaxWidth(max(1, width-2))
	m.ui.picker.Styles.Directory = s.dialogAccent.Width(max(1, width-2)).MaxWidth(max(1, width-2))
	m.ui.picker.Styles.DisabledFile = s.dialogMuted.Width(max(1, width-2)).MaxWidth(max(1, width-2))
	if m.ui.pickerBusy {
		return s.dialogAccent.Render(pad(m.busyLabel("Reading folder"), width))
	}
	if m.ui.pickerError != "" {
		return s.dialogText.Render(ansi.Hardwrap("Could not read folder\n"+m.ui.pickerError, width, true))
	}
	if height <= 2 {
		m.ui.picker.SetHeight(max(1, height-1))
		return m.ui.picker.View()
	}
	path := abbreviatedWorkspacePath(m.ui.picker.CurrentDirectory, width, m.settings.ASCII)
	return s.dialogMuted.Render(pad(path, width)) + "\n\n" + m.ui.picker.View()
}

func (m *viewer) filePickerHints(hints string) string {
	if m.ui.pickerBusy {
		return "esc cancel"
	}
	if m.ui.pickerError != "" {
		return "r retry / left parent / esc cancel"
	}
	return hints
}
