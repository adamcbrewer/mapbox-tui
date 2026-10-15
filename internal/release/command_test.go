package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerSeparatesOutputAndSanitizesLogs(t *testing.T) {
	var lines []string
	output, err := (ExecRunner{}).Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", "printf 'result\\n'; printf '\\033[31mproblem\\033[0m\\n' >&2"},
	}, func(line string) { lines = append(lines, line) })
	if err != nil {
		t.Fatal(err)
	}
	if output != "result" {
		t.Fatalf("stdout = %q", output)
	}
	logs := strings.Join(lines, "\n")
	if !strings.Contains(logs, "problem") || strings.Contains(logs, "\x1b") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestExecRunnerEnvironmentAndDirectory(t *testing.T) {
	t.Setenv("RELEASE_TEST_REMOVE", "inherited")
	t.Setenv("RELEASE_TEST_OVERRIDE", "inherited")
	dir := t.TempDir()
	output, err := (ExecRunner{}).Run(context.Background(), Command{
		Name: "sh", Args: []string{"-c", `printf '%s|%s|%s|%s' "$PWD" "$RELEASE_TEST_OVERRIDE" "${RELEASE_TEST_REMOVE-unset}" "$GIT_TERMINAL_PROMPT"`},
		Dir: dir, Env: map[string]string{"RELEASE_TEST_OVERRIDE": "replacement"}, Unset: []string{"RELEASE_TEST_REMOVE"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := dir + "|replacement|unset|0"; output != want {
		t.Fatalf("environment = %q, want %q", output, want)
	}
}

func TestExecRunnerCancelsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	done := make(chan error, 1)
	go func() {
		_, err := (ExecRunner{}).Run(ctx, Command{Name: "sh", Args: []string{"-c", `sleep 30 & echo $! > "$1"; printf 'ready\n'; wait`, "sh", pidFile}}, func(line string) {
			if line == "ready" {
				cancel()
			}
		})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not join process tree")
	}
	pid, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	// A reparented zombie can remain briefly until init reaps it, but must not run.
	status, err := os.ReadFile("/proc/" + strings.TrimSpace(string(pid)) + "/status")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err == nil && !strings.Contains(string(status), "State:\tZ") {
		t.Fatalf("child survived cancellation: %s", status)
	}
}

func TestExecRunnerTimeout(t *testing.T) {
	_, err := (ExecRunner{}).Run(context.Background(), Command{Name: "sh", Args: []string{"-c", "sleep 30"}, Timeout: 20 * time.Millisecond}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout = %v", err)
	}
}

func TestExecRunnerRejectsOversizedStdout(t *testing.T) {
	_, err := (ExecRunner{}).Run(context.Background(), Command{Name: "head", Args: []string{"-c", "8388609", "/dev/zero"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeded 8 MiB") {
		t.Fatalf("overflow = %v", err)
	}
}
