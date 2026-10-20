package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestAuthMethodPickerSelectionAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		keys   []tea.KeyPressMsg
		method string
	}{
		{name: "default token", method: "token"},
		{name: "arrow browser", keys: []tea.KeyPressMsg{{Code: tea.KeyDown}}, method: "browser"},
		{name: "arrow wraps", keys: []tea.KeyPressMsg{{Code: tea.KeyUp}}, method: "browser"},
		{name: "tab returns", keys: []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyTab}}, method: "token"},
		{name: "numeric browser", keys: []tea.KeyPressMsg{{Code: '2'}}, method: "browser"},
	} {
		t.Run(test.name, func(t *testing.T) {
			picker := &authMethodPicker{width: 80}
			for _, key := range test.keys {
				if _, cmd := picker.Update(key); cmd != nil {
					t.Fatal("navigation submitted a choice before Enter")
				}
			}
			_, cmd := picker.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil || picker.method != test.method || picker.cancelled {
				t.Fatalf("selection = %q, want %q", picker.method, test.method)
			}
			if !strings.Contains(picker.View().Content, "Login method:") {
				t.Fatal("finished picker did not summarise the selection")
			}
		})
	}
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'c', Mod: tea.ModCtrl}} {
		picker := &authMethodPicker{width: 80}
		_, cmd := picker.Update(key)
		if cmd == nil || !picker.cancelled || picker.method != "" {
			t.Fatal("cancelled picker selected a login method")
		}
	}
}

func TestAuthMethodPickerFitsSmallTerminal(t *testing.T) {
	for _, selection := range []int{0, 1} {
		picker := &authMethodPicker{selected: selection}
		picker.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
		view := picker.View().Content
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > 30 {
				t.Fatalf("picker overflows narrow terminal: %q", line)
			}
		}
		if !strings.Contains(view, "> ") || !strings.Contains(view, "cancel") {
			t.Fatal("picker lacks selected indicator or cancellation hint")
		}
	}
}
