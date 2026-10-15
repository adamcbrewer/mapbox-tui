package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

type Command struct {
	Name    string
	Args    []string
	Dir     string
	Env     map[string]string
	Unset   []string
	Timeout time.Duration
}

type CommandRunner interface {
	Run(context.Context, Command, func(string)) (string, error)
}

type ExecRunner struct{}

func commandEnv(command Command) []string {
	var env []string
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if _, exists := command.Env[name]; exists {
			continue
		}
		skip := false
		for _, unset := range command.Unset {
			if name == unset {
				skip = true
			}
		}
		if !skip {
			env = append(env, value)
		}
	}
	for name, value := range command.Env {
		env = append(env, name+"="+value)
	}
	return append(env, "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "GH_PAGER=cat", "PAGER=cat")
}

func (ExecRunner) Run(ctx context.Context, command Command, emit func(string)) (string, error) {
	timeout := command.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir, cmd.Env = command.Dir, commandEnv(command)
	// Releases are supported on Linux. Kill the entire make/process tree on cancel.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	output := &commandOutput{emit: emit}
	var stdout limitedBuffer
	cmd.Stdout = io.MultiWriter(&stdout, output)
	cmd.Stderr = output
	err := cmd.Run()
	output.flush()
	if ctx.Err() != nil {
		return stdout.String(), fmt.Errorf("%s stopped: %w", command.Name, ctx.Err())
	}
	if err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w\n%s", command.Name, strings.Join(command.Args, " "), err, output.tail)
	}
	if stdout.overflow {
		return "", fmt.Errorf("%s output exceeded 8 MiB", command.Name)
	}
	return strings.TrimSpace(stdout.String()), nil
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	const limit = 8 << 20
	n := len(p)
	room := limit - b.Len()
	if n > room {
		b.overflow = true
		p = p[:room]
	}
	_, err := b.Buffer.Write(p)
	return n, err
}

type commandOutput struct {
	mu      sync.Mutex
	pending string
	tail    string
	emit    func(string)
}

func safeText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func (w *commandOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(p)
	for {
		line, rest, found := strings.Cut(w.pending, "\n")
		if !found {
			if len(w.pending) > 4096 {
				w.line(w.pending[:4096])
				w.pending = w.pending[4096:]
				continue
			}
			break
		}
		w.line(line)
		w.pending = rest
	}
	return len(p), nil
}

func (w *commandOutput) line(line string) {
	line = safeText(line)
	if len(line) > 2048 {
		line = line[:2048] + "…"
	}
	w.tail += line + "\n"
	if len(w.tail) > 8192 {
		w.tail = w.tail[len(w.tail)-8192:]
	}
	if w.emit != nil {
		w.emit(line)
	}
}
func (w *commandOutput) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != "" {
		w.line(w.pending)
		w.pending = ""
	}
}
