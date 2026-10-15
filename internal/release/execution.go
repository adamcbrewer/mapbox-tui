package release

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

type operationResultMsg struct {
	result Result
	err    error
}

// executionLifecycle is shared by the value models Bubble Tea passes around.
// Closing prevents commands queued by Bubble Tea from starting after shutdown.
type executionLifecycle struct {
	mu      sync.Mutex
	closing bool
	workers sync.WaitGroup
	cancel  context.CancelFunc
}

func (e *executionLifecycle) begin() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing {
		return false
	}
	e.workers.Add(1)
	return true
}

// Shutdown cancels and joins adapter work before its files and journal are closed.
func (m Model) Shutdown() {
	if m.execution == nil {
		if m.cancel != nil {
			m.cancel()
		}
		return
	}
	e := m.execution
	e.mu.Lock()
	e.closing = true
	e.cancel()
	e.mu.Unlock()
	e.workers.Wait()
}

// NewExecutingModel uses the same UI/session for real and fake adapters.
func NewExecutingModel(session Session, adapter Adapter, interval time.Duration, noColor bool) Model {
	m := NewModel(session, interval, noColor)
	m.adapter = adapter
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.execution = &executionLifecycle{cancel: m.cancel}
	// A bounded queue prevents verbose commands from blocking process cancellation.
	// Each leaf keeps a bounded tail; stderr is also retained by the command runner.
	m.output = make(chan string, 128)
	m.editor = textarea.New()
	m.editor.CharLimit = 128 * 1024
	return m
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Model) command() tea.Cmd {
	if m.adapter == nil {
		return m.tick()
	}
	if m.busy || m.session.Status != Running {
		return nil
	}
	request := m.session.Request()
	m.busy = true
	adapter, ctx, output := m.adapter, m.ctx, m.output
	lifecycle := m.execution
	emit := func(line string) {
		select {
		case output <- safeText(line):
		default:
		}
	}
	delay := time.Duration(0)
	if m.session.Mode == TestMode {
		delay = m.interval
	}
	if request.Operation == OpenViewer && m.session.Mode != TestMode {
		real, ok := adapter.(*RealAdapter)
		if !ok {
			return func() tea.Msg {
				return operationResultMsg{err: fmt.Errorf("adapter does not support terminal handoff")}
			}
		}
		// The binary was verified in the preceding operation. ExecProcess suspends
		// Bubble Tea and restores it on exit; the user then confirms the interaction.
		cmd, err := real.ViewerCommand(ctx, request.State)
		if err != nil {
			return func() tea.Msg { return operationResultMsg{err: err} }
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return operationResultMsg{result: Result{State: request.State, Message: "Offline viewer exited; confirm it started and quit correctly"}, err: err}
		})
	}
	return func() tea.Msg {
		if lifecycle != nil {
			if !lifecycle.begin() {
				return operationResultMsg{err: context.Canceled}
			}
			defer lifecycle.workers.Done()
		}
		if err := ctx.Err(); err != nil {
			return operationResultMsg{err: err}
		}
		if delay > 0 {
			if err := waitContext(ctx, delay); err != nil {
				return operationResultMsg{err: err}
			}
		}
		result, err := adapter.Execute(ctx, request, emit)
		return operationResultMsg{result: result, err: err}
	}
}

func (m *Model) drainOutput() {
	if m.output == nil {
		return
	}
	// Only consume the current batch so a chatty process cannot monopolise input.
	for range len(m.output) {
		select {
		case line := <-m.output:
			entries := m.session.Activity[m.session.Step]
			if len(entries) == 0 {
				continue
			}
			leaf := &entries[len(entries)-1]
			for _, part := range strings.Split(line, "\n") {
				if len(part) > 2048 {
					part = part[:2048] + "…"
				}
				leaf.Output = append(leaf.Output, part)
			}
			if len(leaf.Output) > 12 {
				leaf.Output = leaf.Output[len(leaf.Output)-12:]
			}
		default:
			return
		}
	}
}

func (m Model) retryDelay() time.Duration {
	if m.session.Mode == TestMode {
		return m.interval
	}
	return min(30*time.Second, time.Second*time.Duration(1<<min(m.session.ticks, 5)))
}
