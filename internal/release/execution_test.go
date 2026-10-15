package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type adapterFunc func(context.Context, Request, func(string)) (Result, error)

func (f adapterFunc) Execute(ctx context.Context, q Request, emit func(string)) (Result, error) {
	return f(ctx, q, emit)
}

func TestShutdownJoinsRunningAdapter(t *testing.T) {
	started, finished := make(chan struct{}), make(chan struct{})
	s, err := NewSession("", "success")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	adapter := adapterFunc(func(ctx context.Context, q Request, emit func(string)) (Result, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return Result{}, ctx.Err()
	})
	original := NewExecutingModel(s, adapter, 0, true)
	m := original
	cmd := m.command()
	if cmd == nil {
		t.Fatal("no command")
	}
	if m.command() != nil {
		t.Fatal("scheduled duplicate work")
	}
	result := make(chan operationResultMsg, 1)
	go func() { result <- cmd().(operationResultMsg) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("adapter did not start")
	}
	original.Shutdown()
	select {
	case <-finished:
	default:
		t.Fatal("shutdown returned before adapter stopped")
	}
	if msg := <-result; !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("result = %v", msg.err)
	}
}

func TestShutdownPreventsQueuedAdapterFromStarting(t *testing.T) {
	s, err := NewSession("", "success")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	called := false
	m := NewExecutingModel(s, adapterFunc(func(context.Context, Request, func(string)) (Result, error) { called = true; return Result{}, nil }), 0, true)
	cmd := m.command()
	m.Shutdown()
	msg := cmd().(operationResultMsg)
	if called || !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("queued work after shutdown: called=%v, error=%v", called, msg.err)
	}
}

func TestAdapterOutputIsBoundedAndAttachedBeforeTransition(t *testing.T) {
	s, err := NewSession("", "success")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	m := NewExecutingModel(s, adapterFunc(func(_ context.Context, q Request, emit func(string)) (Result, error) {
		for range 1000 {
			emit("\x1b[31moutput\x1b[0m")
		}
		return Result{State: q.State, Message: "finished"}, nil
	}), 0, true)
	defer m.Shutdown()
	msg := m.command()()
	updated, _ := m.Update(msg)
	m = updated.(Model)
	entries := m.session.Activity[Preflight]
	if len(entries) < 2 || len(entries[0].Output) != 12 || len(entries[1].Output) != 0 {
		t.Fatalf("output moved to next leaf: %+v", entries)
	}
	for _, line := range entries[0].Output {
		if strings.Contains(line, "\x1b") {
			t.Fatalf("unsafe output: %q", line)
		}
	}
}
