package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const controlVersion = 1
const controlLimit = 64 << 10
const controlResponseLimit = 4 << 20

var sessionNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,47}$`)
var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read cannot fail on supported Go platforms.
	b[6], b[8] = b[6]&15|64, b[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type sessionInfo struct {
	ID        string          `json:"session_id"`
	Name      string          `json:"name,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	Status    json.RawMessage `json:"status,omitempty"`
}

type controlRequest struct {
	Version    int             `json:"version"`
	ID         string          `json:"request_id"`
	SessionID  string          `json:"session_id"`
	Action     string          `json:"action"`
	Generation string          `json:"generation,omitempty"`
	Revision   *uint64         `json:"expected_revision,omitempty"`
	Args       json.RawMessage `json:"args"`
}

type controlError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Recovery string `json:"recovery"`
}

func (e *controlError) Error() string { return e.Message }

type controlResponse struct {
	TransitionID string          `json:"transition_id,omitempty"`
	OutcomeKnown bool            `json:"outcome_known"`
	Version      int             `json:"version"`
	RequestID    string          `json:"request_id"`
	SessionID    string          `json:"session_id"`
	Generation   string          `json:"generation,omitempty"`
	Revision     uint64          `json:"revision"`
	OperationID  string          `json:"operation_id,omitempty"`
	State        string          `json:"state"`
	Changed      bool            `json:"changed"`
	Message      string          `json:"message"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *controlError   `json:"error,omitempty"`
}

func controlFailure(code, message, recovery string) controlResponse {
	return controlResponse{OutcomeKnown: true, Version: controlVersion, State: "failed", Message: message,
		Error: &controlError{Code: code, Message: message, Recovery: recovery}}
}

func strictJSON(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

// Keep wire values detached from mutable viewer state before crossing goroutines.
func controlJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return data
}

type controlMessage struct {
	request controlRequest
	reply   chan<- controlResponse
	ctx     context.Context
}

type sessionServer struct {
	info     sessionInfo
	dir      string
	listener net.Listener
	inbox    chan controlMessage
	ctx      context.Context
	cancel   context.CancelFunc
	workers  sync.WaitGroup
	slots    chan struct{}
}

// Darwin permits at most 103 pathname bytes, including the UUID and suffix.
const sessionSocketPathLimit = 103

func sessionDirectoryAt(root string, uid int) string {
	name := fmt.Sprintf("mapbox-tui-%d", uid)
	dir := filepath.Join(root, name)
	if len(filepath.Join(dir, strings.Repeat("0", 36)+".sock")) > sessionSocketPathLimit {
		dir = filepath.Join("/tmp", name)
	}
	return dir
}

func sessionDirectory() (string, error) {
	root := os.Getenv("XDG_RUNTIME_DIR")
	if root == "" {
		root = os.TempDir()
	}
	dir := sessionDirectoryAt(root, os.Getuid())
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || !sessionOwned(info) {
		return "", fmt.Errorf("session directory must be owned by this user with mode 0700: %s", dir)
	}
	return dir, nil
}

// The registry lock serialises publication and stale endpoint cleanup, including
// competing launches using the same alias. It is never unlinked while in use.
func sessionRegistryLock(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "registry.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err = lockLocalFile(f); err == nil {
			return f, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("session registry busy: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sessionRecords(dir string) ([]sessionInfo, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) > 1024 {
		return nil, fmt.Errorf("too many session records")
	}
	records := []sessionInfo{}
	deadline := time.Now().Add(2 * time.Second)
	for _, path := range paths {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("session registry scan timed out")
		}
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // The viewer may have exited after Glob returned its record.
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, 4097))
		f.Close()
		if err != nil {
			return nil, err
		}
		var info sessionInfo
		if len(data) > 4096 || strictJSON(data, &info) != nil || !sessionIDPattern.MatchString(info.ID) {
			continue
		}
		if filepath.Base(path) != info.ID+".json" {
			continue
		}
		conn, err := net.DialTimeout("unix", filepath.Join(dir, info.ID+".sock"), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			records = append(records, info)
			continue
		}
		// A timeout/busy listener is still live. Never remove it on a failed ping.
		if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, os.ErrNotExist) {
			records = append(records, info)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.Remove(filepath.Join(dir, info.ID+".sock")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	slices.SortFunc(records, func(a, b sessionInfo) int { return a.StartedAt.Compare(b.StartedAt) })
	return records, nil
}

func discoverSessions() ([]sessionInfo, error) {
	dir, err := sessionDirectory()
	if err != nil {
		return nil, err
	}
	lock, err := sessionRegistryLock(dir)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return sessionRecords(dir)
}

// Ordinary launches keep working on platforms without the local control transport.
func startViewerSession(a Args) (*sessionServer, error) {
	if a.NoControl {
		return nil, nil
	}
	if !sessionControlSupported {
		if a.SessionName != "" {
			return nil, fmt.Errorf("named control sessions require Unix")
		}
		return nil, nil
	}
	return startSession(a.SessionName)
}

func startSession(name string) (*sessionServer, error) {
	if name != "" && (!sessionNamePattern.MatchString(name) || sessionIDPattern.MatchString(name)) {
		return nil, fmt.Errorf("session name must start with a letter and contain at most 48 letters, digits, '-' or '_'")
	}
	dir, err := sessionDirectory()
	if err != nil {
		return nil, err
	}
	lock, err := sessionRegistryLock(dir)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	records, err := sessionRecords(dir)
	if err != nil {
		return nil, err
	}
	for _, info := range records {
		if name != "" && name == info.Name {
			return nil, fmt.Errorf("session name %q is already running (%s)", name, info.ID)
		}
	}
	info := sessionInfo{ID: newSessionID(), Name: name, StartedAt: time.Now().UTC()}
	path := filepath.Join(dir, info.ID+".sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("start local session: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, info.ID+".json"), controlJSON(info), 0600); err != nil {
		listener.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &sessionServer{info: info, dir: dir, listener: listener, ctx: ctx, cancel: cancel,
		inbox: make(chan controlMessage, 32), slots: make(chan struct{}, 32)}
	s.workers.Go(s.accept)
	return s, nil
}

func (s *sessionServer) close() {
	s.cancel()
	s.listener.Close()
	s.workers.Wait()
	// UUID paths cannot belong to a replacement process using the same name.
	os.Remove(filepath.Join(s.dir, s.info.ID+".json"))
}

func (s *sessionServer) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		select {
		case s.slots <- struct{}{}:
			s.workers.Go(func() { defer func() { <-s.slots }(); s.handle(conn) })
		default:
			conn.Close()
		}
	}
}

func (s *sessionServer) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	stop := context.AfterFunc(s.ctx, func() { conn.Close() })
	defer stop()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), controlLimit)
	if !scanner.Scan() {
		return
	}
	var request controlRequest
	response := controlFailure("invalid_request", "invalid control request", "use the advertised action schema")
	if err := strictJSON(scanner.Bytes(), &request); err == nil {
		switch {
		case request.Version != controlVersion:
			response = controlFailure("protocol_version", "unsupported protocol version", "use protocol version 1")
		case request.SessionID != s.info.ID:
			response = controlFailure(
				"session_changed",
				"target session no longer matches",
				"list sessions and explicitly choose a target",
			)
		case request.ID == "" || len(request.ID) > 128:
			response = controlFailure(
				"invalid_request",
				"request_id must contain 1..128 characters",
				"supply a unique request ID",
			)
		default:
			ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
			defer cancel()
			reply := make(chan controlResponse, 1) // A disconnected caller must never block Update.
			message := controlMessage{request: request, reply: reply, ctx: ctx}
			response = controlFailure(
				"timeout",
				"viewer did not respond; outcome is unknown",
				"query the operation using request_id before retrying",
			)
			response.OutcomeKnown = false
			if !controlRead(request.Action) {
				response.OperationID = request.ID
			}
			select {
			case s.inbox <- message:
				select {
				case response = <-reply:
				case <-ctx.Done():
				}
			case <-ctx.Done():
			}
		}
	}
	response.SessionID = s.info.ID
	response.RequestID = request.ID
	// A disconnect is expected; the operation remains available in the viewer.
	data := controlJSON(response)
	if len(data)+1 > controlResponseLimit {
		response.Result = nil
		response.State = "failed"
		response.Error = &controlError{Code: "response_too_large",
			Message:  "response exceeds the transport limit",
			Recovery: "use status summary=true or smaller pages; inspect operation and item IDs separately"}
		response.Message = response.Error.Message
		data = controlJSON(response)
	}
	_, _ = conn.Write(append(data, '\n'))
}

func resolveSession(target string) (sessionInfo, error) {
	records, err := discoverSessions()
	if err != nil {
		return sessionInfo{}, err
	}
	for _, info := range records {
		if info.ID == target || info.Name == target && target != "" {
			return info, nil
		}
	}
	return sessionInfo{}, &controlError{
		Code:     "session_not_found",
		Message:  "session not found: " + target,
		Recovery: "list sessions and choose a live UUID or name",
	}
}

func sendControl(ctx context.Context, info sessionInfo, request controlRequest) (controlResponse, error) {
	dir, err := sessionDirectory()
	if err != nil {
		return controlResponse{}, err
	}
	if len(request.Args) == 0 {
		request.Args = json.RawMessage(`{}`)
	}
	request.Version, request.SessionID = controlVersion, info.ID
	if request.ID == "" {
		request.ID = newSessionID()
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, info.ID+".sock"))
	if err != nil {
		return controlResponse{}, &controlError{
			Code:     "session_disconnected",
			Message:  "session disconnected",
			Recovery: "list sessions; explicitly reconnect to a live session",
		}
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return controlResponse{}, err
	}
	var response controlResponse
	err = json.NewDecoder(io.LimitReader(conn, controlResponseLimit)).Decode(&response)
	if err != nil {
		return controlResponse{}, fmt.Errorf("response unavailable; query operation %s before retrying: %w", request.ID, err)
	}
	if response.SessionID != info.ID || response.RequestID != request.ID || response.Version != controlVersion {
		return controlResponse{}, fmt.Errorf("invalid session response identity")
	}
	return response, nil
}

func controlTransportFailure(previous controlResponse, err error, recovery string) controlResponse {
	response := controlFailure("connection_failed", err.Error(), recovery)
	response.OperationID, response.Changed = previous.OperationID, previous.Changed
	response.OutcomeKnown = false
	if typed, ok := err.(*controlError); ok {
		response.Error = typed
		response.OutcomeKnown = true
	}
	return response
}
