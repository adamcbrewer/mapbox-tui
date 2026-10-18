//go:build unix

package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceAndImportRejectNamedPipes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := prepareWorkspaceContext(t.Context(), path)
		if err == nil {
			done <- fmt.Errorf("workspace accepted pipe")
			return
		}
		_, err = loadDocumentContext(t.Context(), path)
		if err == nil {
			done <- fmt.Errorf("import accepted pipe")
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("regular-file validation blocked on named pipe")
	}
}
