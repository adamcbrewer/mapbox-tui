//go:build unix && integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testSessionDirectory(t *testing.T) {
	t.Helper()
	// Keep socket paths below the Unix sockaddr limit even with long test names.
	dir, err := os.MkdirTemp("/tmp", "mb-session-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// Exercise real sockets and MCP, with all viewer access confined to one loop.
func testLiveSession(t *testing.T, name string) *sessionServer {
	t.Helper()
	server, err := startSession(name)
	if err != nil {
		t.Fatal(err)
	}
	m := journeyViewer(t)
	m.control = &viewerControl{server: server}
	results := make(chan tea.Msg, 32)
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer m.cancelControlOperations()
		for {
			var cmd tea.Cmd
			select {
			case msg := <-server.inbox:
				cmd = m.handleControl(msg)
			case msg := <-results:
				_, cmd = m.Update(msg)
			case <-server.ctx.Done():
				return
			}
			if cmd != nil {
				workers.Go(func() {
					value := cmd()
					select {
					case results <- value:
					case <-server.ctx.Done():
					}
				})
			}
		}
	}()
	t.Cleanup(func() { server.close(); <-done; workers.Wait() })
	return server
}

func TestSessionDiscoveryLifecycleAndPinnedIdentity(t *testing.T) {
	testSessionDirectory(t)
	a := testLiveSession(t, "main")
	b := testLiveSession(t, "")
	records, err := discoverSessions()
	if err != nil || len(records) != 2 {
		t.Fatal(records, err)
	}
	if a.info.ID == b.info.ID || !sessionIDPattern.MatchString(b.info.ID) {
		t.Fatal("invalid identities")
	}
	if _, err := startSession("main"); err == nil {
		t.Fatal("duplicate name accepted")
	}
	for _, target := range []string{a.info.ID, "main", b.info.ID} {
		info, err := resolveSession(target)
		if err != nil {
			t.Fatal(err)
		}
		response, err := sendControl(t.Context(), info, controlRequest{Action: "status"})
		if err != nil || response.SessionID != info.ID || response.Generation == "" {
			t.Fatal(response, err)
		}
	}
	a.close()
	replacement := testLiveSession(t, "main")
	if replacement.info.ID == a.info.ID {
		t.Fatal("session identity reused")
	}
	if _, err := sendControl(t.Context(), a.info, controlRequest{Action: "status"}); err == nil {
		t.Fatal("pinned client retargeted replacement")
	}
}

func TestSessionStaleRecordsAndDirectoryPermissions(t *testing.T) {
	testSessionDirectory(t)
	dir, err := sessionDirectory()
	if err != nil {
		t.Fatal(err)
	}
	info := sessionInfo{ID: newSessionID(), Name: "stale", StartedAt: time.Now()}
	socket := filepath.Join(dir, info.ID+".sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	if err := os.WriteFile(filepath.Join(dir, info.ID+".json"), controlJSON(info), 0600); err != nil {
		t.Fatal(err)
	}
	records, err := discoverSessions()
	if err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatal("stale socket retained", err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverSessions(); err == nil {
		t.Fatal("unsafe runtime directory accepted")
	}
}

func TestSessionMCPAndCLIShareViewerState(t *testing.T) {
	testSessionDirectory(t)
	server := testLiveSession(t, "agent")
	left, right := mcp.NewInMemoryTransports()
	ss, err := attachedMCPServer(server.info).Connect(t.Context(), left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "viewer_status", Arguments: map[string]any{"args": map[string]any{}}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	var status controlResponse
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &status); err != nil {
		t.Fatal(err)
	}
	result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "viewer_journey_create", Arguments: map[string]any{
		"request_id": newSessionID(), "generation": status.Generation, "expected_revision": status.Revision,
		"args": map[string]any{"mode": "offline", "stops": []controlStop{{Lat: 51, Lon: 0}, {Lat: 52, Lon: 1}}},
	}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	response, err := sendControl(t.Context(), server.info, controlRequest{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	state := controlTestResult(t, response)
	if len(state["journeys"].([]any)) != 1 {
		t.Fatal("MCP edited a separate workspace", state)
	}
	command, err := parseCLI([]string{"remote", "--session", server.info.ID, "camera", "set", "--zoom", "7", "--json"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := runRemote(t.Context(), command, io.Discard); err != nil {
		t.Fatal(err)
	}
	result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "viewer_status", Arguments: map[string]any{"args": map[string]any{}}})
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &status); err != nil {
		t.Fatal(err)
	}
	if controlTestResult(t, status)["camera"].(map[string]any)["zoom"] != float64(7) {
		t.Fatal("CLI camera not visible through MCP")
	}
	// A stale mutation must use MCP's error signal and the structured contract.
	result, err = cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "viewer_camera_fit", Arguments: map[string]any{
		"request_id": newSessionID(), "generation": status.Generation, "expected_revision": 0, "args": map[string]any{},
	}})
	if err != nil || !result.IsError {
		t.Fatal(result, err)
	}
}

func TestSessionRequestsExpireWithoutHangingViewer(t *testing.T) {
	testSessionDirectory(t)
	server, err := startSession("waiting")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = sendControl(ctx, server.info, controlRequest{Action: "status"})
	if err == nil {
		t.Fatal("unresponsive viewer returned success")
	}
	// close cancels idle clients and pending replies, rather than waiting for deadlines.
	conn, err := net.Dial("unix", filepath.Join(server.dir, server.info.ID+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	server.close()
	if time.Since(start) > time.Second {
		t.Fatal("shutdown waited for socket deadline")
	}
}

func TestSessionConcurrentNameClaimsHaveOneOwner(t *testing.T) {
	testSessionDirectory(t)
	results := make(chan *sessionServer, 6)
	var workers sync.WaitGroup
	for range 6 {
		workers.Go(func() { server, _ := startSession("shared"); results <- server })
	}
	workers.Wait()
	close(results)
	owners := 0
	for server := range results {
		if server != nil {
			owners++
			server.close()
		}
	}
	if owners != 1 {
		t.Fatalf("got %d owners for one name", owners)
	}
}

func TestMCPDiscoversAndControlsMultipleSessions(t *testing.T) {
	testSessionDirectory(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	addViewerTools(server, sessionInfo{})
	left, right := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(t.Context(), right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) controlResponse {
		t.Helper()
		result, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		var response controlResponse
		if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &response); err != nil {
			t.Fatal(err)
		}
		if result.IsError != (response.Error != nil) {
			t.Fatal("inconsistent MCP error", result)
		}
		return response
	}
	empty := call("viewer_sessions", map[string]any{})
	if empty.Error != nil || len(controlTestResult(t, empty)["sessions"].([]any)) != 0 {
		t.Fatal("discovery should work before viewers start", empty)
	}
	first := testLiveSession(t, "first")
	second := testLiveSession(t, "second")
	listed := call("viewer_sessions", map[string]any{})
	if listed.Error != nil || len(controlTestResult(t, listed)["sessions"].([]any)) != 2 {
		t.Fatal("expected both live viewers", listed)
	}
	for i, live := range []*sessionServer{first, second} {
		status := call("viewer_status", map[string]any{"session_id": live.info.ID, "args": map[string]any{}})
		if status.Error != nil || status.SessionID != live.info.ID {
			t.Fatal(status)
		}
		response := call("viewer_camera_set", map[string]any{
			"session_id": live.info.ID, "request_id": newSessionID(),
			"generation": status.Generation, "expected_revision": status.Revision,
			"args": map[string]any{"zoom": 10 + i},
		})
		if response.Error != nil {
			t.Fatal(response)
		}
	}
	for i, live := range []*sessionServer{first, second} {
		status := call("viewer_status", map[string]any{"session_id": live.info.ID, "args": map[string]any{}})
		if controlTestResult(t, status)["camera"].(map[string]any)["zoom"] != float64(10+i) {
			t.Fatal("session controls were not isolated", status)
		}
	}
	alias := call("viewer_status", map[string]any{"session_id": "first", "args": map[string]any{}})
	if alias.Error == nil || alias.Error.Code != "session_mismatch" {
		t.Fatal("must use immutable UUID", alias)
	}
	first.close()
	stale := call("viewer_status", map[string]any{"session_id": first.info.ID, "args": map[string]any{}})
	if stale.Error == nil || stale.Error.Code != "session_not_found" {
		t.Fatal("ended session should report recovery instructions", stale)
	}
}

func TestLargeWorkspaceStatusRemainsControllable(t *testing.T) {
	testSessionDirectory(t)
	server := testLiveSession(t, "large")
	stops := make([]controlStop, 1000)
	for i := range stops {
		stops[i] = controlStop{Lat: 51 + float64(i%10)/100, Lon: float64(i%10) / 100}
	}
	var ref ItemRef
	for range 40 {
		response, err := callRemote(t.Context(), server.info, controlRequest{
			Action: "journey.create", Args: controlJSON(map[string]any{"mode": "offline", "stops": stops}),
		}, false, true)
		if err != nil || response.Error != nil {
			t.Fatal(response, err)
		}
		ref = controlTestRef(t, response)
	}
	response, err := sendControl(t.Context(), server.info, controlRequest{Action: "status"})
	if err != nil || response.Error != nil {
		t.Fatal(response, err)
	}
	state := controlTestResult(t, response)
	if len(state["tracks"].([]any)) != 20 || state["next_offset"] != float64(20) {
		t.Fatal("missing pagination", state["next_offset"])
	}
	if len(controlJSON(response)) > controlResponseLimit {
		t.Fatal("status exceeded wire limit")
	}
	response, err = sendControl(t.Context(), server.info, controlRequest{Action: "journey.inspect",
		Args: controlJSON(map[string]any{"ref": ref})})
	if err != nil || response.Error != nil || len(controlTestResult(t, response)["stops"].([]any)) != 1000 {
		t.Fatal("explicit journey inspection lost stops", response, err)
	}
	response, err = callRemote(t.Context(), server.info, controlRequest{
		Action: "camera.set", Args: controlJSON(map[string]any{"zoom": 12, "animate": false}),
	}, false, true)
	if err != nil || response.Error != nil {
		t.Fatal("large state blocked automatic preconditions", response, err)
	}
}

func TestOversizedControlResponseIsStructured(t *testing.T) {
	testSessionDirectory(t)
	server, err := startSession("oversized")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		request := <-server.inbox
		request.reply <- controlResponse{Version: controlVersion, OutcomeKnown: true, State: "applied",
			Result: controlJSON(strings.Repeat("x", controlResponseLimit))}
	}()
	response, err := sendControl(t.Context(), server.info, controlRequest{Action: "status"})
	<-done
	if err != nil || response.Error == nil || response.Error.Code != "response_too_large" {
		t.Fatal(response, err)
	}
}

func TestSessionDiscoverySkipsVanishedRecords(t *testing.T) {
	testSessionDirectory(t)
	server, err := startSession("survivor")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	// A dangling entry reproduces Open returning ErrNotExist after a successful Glob.
	vanished := filepath.Join(server.dir, newSessionID()+".json")
	if err := os.Symlink(filepath.Join(server.dir, "removed"), vanished); err != nil {
		t.Fatal(err)
	}
	records, err := discoverSessions()
	if err != nil || len(records) != 1 || records[0].ID != server.info.ID {
		t.Fatal(records, err)
	}
}

func TestSessionUsesShortFallbackForLongRuntimePaths(t *testing.T) {
	for _, xdg := range []bool{false, true} {
		t.Run(fmt.Sprint(xdg), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), strings.Repeat("long-runtime-", 12))
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("XDG_RUNTIME_DIR", "")
			t.Setenv("TMPDIR", root)
			if xdg {
				t.Setenv("XDG_RUNTIME_DIR", root)
			}
			server, err := startSession("")
			if err != nil {
				t.Fatal(err)
			}
			defer server.close()
			path := filepath.Join(server.dir, server.info.ID+".sock")
			if len(path) > sessionSocketPathLimit || filepath.Dir(server.dir) != "/tmp" {
				t.Fatal("socket path exceeds portable limit", path)
			}
			records, err := discoverSessions()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, record := range records {
				if record.ID == server.info.ID {
					found = true
				}
			}
			if !found {
				t.Fatal("fallback session not discoverable")
			}
		})
	}
}

func TestPlainRemoteFailuresRemoveTerminalControls(t *testing.T) {
	testSessionDirectory(t)
	server := testLiveSession(t, "plain-output")
	path := filepath.Join(t.TempDir(), "café\x1b[31m\nINJECT.json")
	for _, action := range [][]string{{"load", path}, {"workspace", "open", path, "--discard"}} {
		for _, jsonOutput := range []bool{false, true} {
			args := append([]string{"remote", "--session", server.info.ID}, action...)
			if jsonOutput {
				args = append(args, "--json")
			}
			command, err := parseCLI(args, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := runRemote(t.Context(), command, &out); err == nil {
				t.Fatal("nonexistent path succeeded")
			}
			if strings.ContainsRune(out.String(), '\x1b') {
				t.Fatal("raw terminal escape printed")
			}
			if jsonOutput {
				var response controlResponse
				if err := json.Unmarshal(out.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(response.Message, path) {
					t.Fatal("JSON lost original error path", response.Message)
				}
			} else if strings.Contains(out.String(), "\nINJECT") || !strings.Contains(out.String(), "café") {
				t.Fatal("plain message was not safe printable Unicode", out.String())
			}
		}
	}
}

func TestSessionJourneyStopLimit(t *testing.T) {
	testSessionDirectory(t)
	server := testLiveSession(t, "stop-limit")
	stops := make([]controlStop, 5000)
	for i := range stops {
		stops[i] = controlStop{Lat: 51.12345678901234, Lon: float64(i) / 100000}
	}
	response, err := callRemote(t.Context(), server.info, controlRequest{
		Action: "journey.create", Args: controlJSON(map[string]any{"mode": "offline", "stops": stops}),
	}, false, true)
	if err != nil || response.Error != nil {
		t.Fatal("transport could not create a maximum-size journey", response, err)
	}
	ref := controlTestRef(t, response)
	response, err = callRemote(t.Context(), server.info, controlRequest{
		Action: "journey.stops", Args: controlJSON(map[string]any{"ref": ref, "stops": stops}),
	}, false, true)
	if err != nil || response.Error != nil || len(controlTestResult(t, response)["stops"].([]any)) != 5000 {
		t.Fatal("transport could not replace maximum-size journey stops", response, err)
	}
}

func TestSessionRejectsOversizedRequests(t *testing.T) {
	testSessionDirectory(t)
	server := testLiveSession(t, "request-limit")
	_, err := sendControl(t.Context(), server.info, controlRequest{
		Action: "status", Args: controlJSON(map[string]any{"padding": strings.Repeat("a", controlLimit)}),
	})
	if err == nil {
		t.Fatal("transport accepted an oversized request")
	}
	response, err := sendControl(t.Context(), server.info, controlRequest{Action: "status"})
	if err != nil || response.Error != nil {
		t.Fatal("oversized request blocked the session", response, err)
	}
}
