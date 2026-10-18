package main

import "testing"

func TestWindowsViewerSkipsUnsupportedControl(t *testing.T) {
	for _, args := range []Args{{}, {NoControl: true}} {
		server, err := startViewerSession(args)
		if server != nil || err != nil {
			t.Fatal("ordinary Windows launch attempted Unix control", server, err)
		}
	}
	if server, err := startViewerSession(Args{SessionName: "main"}); server != nil || err == nil {
		t.Fatal("explicit named session did not report unsupported transport", server, err)
	}
}
