package main

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "mapbox-tui-test-settings-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot create test settings:", err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", directory); err != nil {
		os.RemoveAll(directory)
		fmt.Fprintln(os.Stderr, "Cannot isolate test settings:", err)
		os.Exit(1)
	}
	// Existing network fixtures model acknowledged online use. Billing tests
	// explicitly clear this setting to exercise first-use and offline behaviour.
	if err := os.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "1"); err != nil {
		os.RemoveAll(directory)
		fmt.Fprintln(os.Stderr, "Cannot configure fixture billing acknowledgement:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}
