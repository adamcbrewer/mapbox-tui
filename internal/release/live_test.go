package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// These tests execute the production adapter and session. Only the process
// boundary is replaced, so unexpected commands and flags fail the test.
type releaseCommands struct {
	t                        *testing.T
	root                     string
	local, remote, published bool
	losePush, loseNotes      bool
	calls                    []Command
	notes                    string
}
type memoryJournal struct {
	state  State
	exists bool
}

func (j *memoryJournal) Load() (State, bool, error) { return j.state, j.exists, nil }
func (j *memoryJournal) Save(s State) error         { j.state, j.exists = s, true; return nil }

const testCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func newReleaseHarness(t *testing.T, mode Mode) (*RealAdapter, *releaseCommands, State) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	commands := &releaseCommands{t: t, root: root}
	adapter := &RealAdapter{Mode: mode, Root: root, Runner: commands, Journal: &memoryJournal{}}
	t.Cleanup(adapter.Cleanup)
	result, err := adapter.Execute(t.Context(), Request{Operation: Inspect}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	result.State.Version = "v0.1.1"
	local := t.TempDir()
	writeReleaseArchive(t, local)
	result.State.LocalArtifactDigest, err = artifactDigest(t.Context(), local, result.State.Version)
	if err != nil {
		t.Fatal(err)
	}
	return adapter, commands, result.State
}
func jsonValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func (f *releaseCommands) releases() []map[string]any {
	all := []map[string]any{{"id": 1, "tag_name": "v0.1.0", "draft": false}}
	body := "Linux x86-64 release. See docs/INSTALL.md."
	if f.notes != "" {
		body = f.notes
	}
	if f.remote {
		all = append(all, map[string]any{"id": 2, "tag_name": "v0.1.1", "name": "v0.1.1", "draft": !f.published, "body": body, "html_url": "https://github.com/example/mapbox-tui/releases/tag/v0.1.1", "assets": []map[string]any{{"id": 11, "name": archiveName("v0.1.1"), "size": 100}, {"id": 12, "name": archiveName("v0.1.1") + ".sha256", "size": 100}}})
	}
	return all
}
func (f *releaseCommands) Run(ctx context.Context, c Command, emit func(string)) (string, error) {
	f.t.Helper()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.calls = append(f.calls, c)
	if c.Dir != f.root {
		f.t.Fatalf("wrong command root: %+v", c)
	}
	args := strings.Join(c.Args, " ")
	if c.Name == "git" {
		switch args {
		case "remote get-url origin":
			return "git@github.com:example/mapbox-tui.git", nil
		case "branch --show-current":
			return "main", nil
		case "status --porcelain --untracked-files=normal", "fetch origin main --tags", "fetch origin main", "merge --ff-only origin/main":
			return "", nil
		case "rev-parse HEAD", "rev-parse origin/main", "rev-parse v0.1.1^{commit}":
			return testCommit, nil
		case "rev-parse refs/tags/v0.1.1":
			return strings.Repeat("b", 40), nil
		case "cat-file -t refs/tags/v0.1.1":
			return "tag", nil
		case "tag --list":
			if f.local {
				return "v0.1.0\nv0.1.1", nil
			}
			return "v0.1.0", nil
		case "tag --list v0.1.1":
			if f.local {
				return "v0.1.1", nil
			}
			return "", nil
		case "tag -a v0.1.1 " + testCommit + " -m Release v0.1.1":
			f.local = true
			return "", nil
		case "push --no-follow-tags origin refs/tags/v0.1.1:refs/tags/v0.1.1":
			f.remote = true
			if f.losePush {
				f.losePush = false
				return "", fmt.Errorf("response lost after push")
			}
			return "", nil
		case "ls-remote --tags origin", "ls-remote --tags origin refs/tags/v0.1.1", "ls-remote --tags origin refs/tags/v0.1.1 refs/tags/v0.1.1^{}":
			if f.remote {
				return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\trefs/tags/v0.1.1\n" + testCommit + "\trefs/tags/v0.1.1^{}", nil
			}
			return "", nil
		case "log --oneline -50 v0.1.0.." + testCommit:
			return "aaaaaaa Change ready for release", nil
		}
	}
	if c.Name == "go" && args == "env -json GOVERSION GOOS GOARCH" {
		return `{"GOVERSION":"go1.27.1","GOOS":"linux","GOARCH":"amd64"}`, nil
	}
	if (c.Name == "make" || c.Name == "rsvg-convert") && args == "--version" {
		return "3.11.0", nil
	}
	if c.Name == "python3" && slices.Equal(c.Args, []string{"-c", "import sys; sys.exit(0 if sys.version_info >= (3, 11) else 'Python 3.11 or newer is required')"}) {
		return "", nil
	}
	if c.Name == "tmux" && args == "-V" {
		return "tmux 3.6", nil
	}
	if c.Name == "make" {
		switch args {
		case "check", "interactions":
			if c.Env["MAPBOX_ACCESS_TOKEN"] != "" {
				f.t.Fatal("checks must be offline")
			}
			if args == "interactions" && !slices.Contains(c.Unset, "NO_COLOR") {
				f.t.Fatal("interactions must enable colour")
			}
			emit("local checks completed")
			return "", nil
		case "package VERSION=v0.1.1 COMMIT=aaaaaaaaaaaa":
			writeReleaseArchive(f.t, filepath.Join(f.root, "dist"))
			return "", nil
		}
	}
	if c.Name == "gh" {
		if len(c.Args) == 7 && c.Args[3] == "repos/example/mapbox-tui/releases?per_page=100" &&
			c.Args[4] == "--paginate" && c.Args[5] == "--jq" {
			if !strings.Contains(c.Args[6], "then {name,body,html_url,assets} else {} end") {
				f.t.Fatal("release history must omit unrelated bodies and assets")
			}
			return jsonValue(f.releases()), nil
		}
		switch args {
		case "api --hostname github.com repos/example/mapbox-tui":
			return `{"permissions":{"push":true}}`, nil
		case "auth status --hostname github.com":
			return "authenticated", nil
		case "api --hostname github.com repos/example/mapbox-tui/releases/latest":
			return jsonValue(f.releases()[len(f.releases())-1]), nil
		case "run list --workflow ci.yml --branch main --commit " + testCommit + " --event push --limit 100 --json databaseId,headSha,headBranch,status,conclusion,url --repo github.com/example/mapbox-tui":
			return `[{"databaseId":4,"headSha":"` + testCommit + `","headBranch":"main","status":"completed","conclusion":"success"}]`, nil
		case "run list --workflow release.yml --branch v0.1.1 --commit " + testCommit + " --event push --limit 100 --json databaseId,headSha,headBranch,status,conclusion,url --repo github.com/example/mapbox-tui":
			return `[{"databaseId":5,"headSha":"` + testCommit + `","headBranch":"v0.1.1","status":"completed","conclusion":"success"}]`, nil
		case "run view 5 --json databaseId,headSha,headBranch,status,conclusion,url,jobs --repo github.com/example/mapbox-tui":
			return `{"databaseId":5,"headSha":"` + testCommit + `","headBranch":"v0.1.1","status":"completed","conclusion":"success","jobs":[{"name":"checks","status":"completed","conclusion":"success"}]}`, nil
		}
		if len(c.Args) == 11 && slices.Equal(c.Args[:4], []string{"release", "download", "v0.1.1", "--dir"}) {
			want := []string{"--pattern", archiveName("v0.1.1"), "--pattern", archiveName("v0.1.1") + ".sha256", "--repo", "github.com/example/mapbox-tui"}
			if !slices.Equal(c.Args[5:], want) {
				f.t.Fatalf("download flags: %v", c.Args)
			}
			writeReleaseArchive(f.t, c.Args[4])
			return "", nil
		}
		if len(c.Args) == 8 && slices.Equal(c.Args[:6], []string{"api", "--hostname", "github.com", "repos/example/mapbox-tui/releases/2", "--method", "PATCH"}) {
			if c.Args[6] != "-F" || !strings.HasPrefix(c.Args[7], "body=@") {
				f.t.Fatalf("publication flags: %v", c.Args)
			}
			b, err := os.ReadFile(strings.TrimPrefix(c.Args[7], "body=@"))
			if err != nil {
				f.t.Fatal(err)
			}
			f.notes = string(b)
			if f.loseNotes {
				f.loseNotes = false
				return "", fmt.Errorf("response lost after saving notes")
			}
			return "", nil
		}
	}
	if filepath.Base(c.Name) == "mapbox-tui" {
		if c.Env["MAPBOX_ACCESS_TOKEN"] != "" {
			f.t.Fatal("smoke must be offline")
		}
		if args == "--version" {
			return "mapbox-tui v0.1.1 (aaaaaaaaaaaa)", nil
		}
		if slices.Equal(c.Args, []string{"render", filepath.Join(f.root, "fixtures/example.gpx"), "--no-provider", "--ascii", "--width", "60", "--height", "20"}) {
			emit("offline rendering passed")
			return "map", nil
		}
	}
	f.t.Fatalf("unexpected command: %s %v", c.Name, c.Args)
	return "", fmt.Errorf("unexpected command")
}
func writeReleaseArchive(t *testing.T, dir string) {
	t.Helper()
	writeReleaseArchiveWithBinary(t, dir, "fixture mapbox-tui")
}
func writeReleaseArchiveWithBinary(t *testing.T, dir, binary string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, archiveName("v0.1.1"))
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"mapbox-tui", "INSTALL.md", "LICENSE", "THIRD_PARTY_NOTICES.txt"} {
		body := []byte("fixture " + name)
		if name == "mapbox-tui" {
			body = []byte(binary)
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{tw.Close(), gz.Close(), file.Close()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), filepath.Base(path))
	if err := os.WriteFile(path+".sha256", []byte(checksum), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRealWorkflowCommandsAndConfirmations(t *testing.T) {
	for _, mode := range []Mode{Live, Rehearsal} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			adapter, commands, state := newReleaseHarness(t, mode)
			session, err := NewSessionWithState(state, "")
			if err != nil {
				t.Fatal(err)
			}
			session.Start()
			var gates []Step
			var executed []Operation
			for range 60 {
				if session.Status == Done {
					break
				}
				if session.Status == Confirmation {
					gates = append(gates, session.Step)
					if session.Step == Tag && commands.remote {
						t.Fatal("pushed before confirmation")
					}
					if session.Step == Publish && commands.published {
						t.Fatal("published before confirmation")
					}
					session.Confirm("no")
					if session.Status != Confirmation {
						t.Fatal("accepted no")
					}
					if session.Step == Publish {
						if commands.releases()[1]["body"] != session.Notes || session.Notes == "" {
							t.Fatal("reviewed notes were not saved before web handoff")
						}
						commands.notes = session.Notes
						commands.published = true // Maintainer clicks Publish release in GitHub.
					}
					session.Confirm("yes")
					continue
				}
				q := session.Request()
				executed = append(executed, q.Operation)
				var result Result
				if q.Operation == OpenViewer {
					// Verify the real terminal handoff command; never run the fixture archive.
					cmd, err := adapter.ViewerCommand(t.Context(), q.State)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(cmd.Args, []string{q.State.Binary, filepath.Join(commands.root, "fixtures/example.gpx"), "--no-provider"}) {
						t.Fatalf("viewer command: %v", cmd.Args)
					}
					result = Result{State: q.State, Message: "Offline viewer exited"}
				} else {
					result, err = adapter.Execute(t.Context(), q, func(string) {})
				}
				if err != nil {
					t.Fatalf("%s: %v", q.Operation, err)
				}
				session.Accept(result, nil)
			}
			if session.Status != Done {
				t.Fatalf("not complete: %+v", session)
			}
			wantGates := []Step{Tag, Viewer, Notes, Publish}
			if mode == Rehearsal {
				wantGates = []Step{Viewer}
			}
			if !slices.Equal(gates, wantGates) {
				t.Fatalf("gates %v; want %v", gates, wantGates)
			}
			if mode == Live && (!commands.remote || !commands.published || commands.notes != session.Notes) {
				t.Fatal("release or reviewed notes not published")
			}
			if mode == Rehearsal && (commands.local || commands.remote || commands.published) {
				t.Fatal("rehearsal mutated tags/releases")
			}
			if mode == Live && !slices.Equal(executed, []Operation{Tools, Source, CI, Check, Interactions, Build, LocalArtifact, LocalSmoke, PushTag, Discover, Jobs, FetchArtifact, RemoteArtifact, OpenViewer, ReadNotes, PreparePublication, PublishRelease}) {
				t.Fatalf("missing/reordered operations: %v", executed)
			}
		})
	}
}

func TestLostMutationResponsesAreReconciled(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	commands.losePush = true
	result, err := adapter.Execute(t.Context(), Request{State: state, Operation: PushTag}, func(string) {})
	if err == nil || !result.State.TagIntent || !commands.remote {
		t.Fatal("expected uncertain push with saved intent")
	}
	adapter.Resume = true
	inspected, err := adapter.Execute(t.Context(), Request{Operation: Inspect}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	result, err = adapter.Execute(t.Context(), Request{State: inspected.State, Operation: PushTag, Retry: true}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	state = result.State
	for _, op := range []Operation{Build, LocalArtifact} {
		result, err = adapter.Execute(t.Context(), Request{State: state, Operation: op}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		state = result.State
	}
	downloaded, err := adapter.Execute(t.Context(), Request{State: state, Operation: FetchArtifact}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := adapter.Execute(t.Context(), Request{State: downloaded.State, Operation: RemoteArtifact}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	state = verified.State
	state.Notes = "Reviewed release notes"
	commands.loseNotes = true
	result, err = adapter.Execute(t.Context(), Request{State: state, Operation: PreparePublication}, func(string) {})
	if err == nil || !result.State.PublishIntent || commands.published {
		t.Fatal("expected uncertain notes save without publication")
	}
	result, err = adapter.Execute(t.Context(), Request{State: result.State, Operation: PreparePublication, Retry: true}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	state = result.State
	result, err = adapter.Execute(t.Context(), Request{State: state, Operation: PublishRelease}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "still a draft") || commands.published {
		t.Fatal("tool must wait for manual publication")
	}
	commands.published = true
	result, err = adapter.Execute(t.Context(), Request{State: state, Operation: PublishRelease, Retry: true}, func(string) {})
	if err != nil || !result.State.Published {
		t.Fatalf("reconcile publication: %v", err)
	}
	pushes, edits := 0, 0
	for _, c := range commands.calls {
		if c.Name == "git" && c.Args[0] == "push" {
			pushes++
		}
		if c.Name == "gh" && slices.Contains(c.Args, "PATCH") {
			edits++
		}
	}
	if pushes != 1 || edits != 1 {
		t.Fatalf("replayed mutations: pushes %d edits %d", pushes, edits)
	}
}

func TestRealAdapterRejectsUnsafeRequests(t *testing.T) {
	for _, name := range []string{"rehearsal tag", "rehearsal prepare", "rehearsal publish", "version injection", "wrong mode", "wrong source", "duplicate tag"} {
		t.Run(name, func(t *testing.T) {
			mode := Live
			if strings.HasPrefix(name, "rehearsal") {
				mode = Rehearsal
			}
			adapter, commands, state := newReleaseHarness(t, mode)
			op := PushTag
			switch name {
			case "rehearsal prepare":
				op = PreparePublication
			case "rehearsal publish":
				op = PublishRelease
			case "version injection":
				state.Version = "v0.1.1$(shell touch bad)"
				op = Build
			case "wrong mode":
				state.Mode = TestMode
			case "wrong source":
				state.Commit = "bad"
				op = Build
			case "duplicate tag":
				commands.local = true
			}
			_, err := adapter.Execute(t.Context(), Request{State: state, Operation: op}, func(string) {})
			if err == nil {
				t.Fatal("unsafe operation accepted")
			}
			if commands.remote || commands.published {
				t.Fatal("remote mutation")
			}
		})
	}
}

type commandOverride struct {
	base     CommandRunner
	override func(Command) (string, error, bool)
}

func (r commandOverride) Run(ctx context.Context, c Command, emit func(string)) (string, error) {
	if output, err, ok := r.override(c); ok {
		return output, err
	}
	return r.base.Run(ctx, c, emit)
}

func TestWorkflowWaitsForMatchingRunAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		op        Operation
		payload   string
		attempt   int
		pending   bool
		wantError bool
	}{
		{name: "unrelated CI", op: CI, payload: `[{"databaseId":9,"headSha":"other","headBranch":"main","status":"completed","conclusion":"success"}]`, attempt: 1, pending: true},
		{name: "CI not started", op: CI, payload: `[]`, attempt: 40, wantError: true},
		{name: "CI queued", op: CI, payload: `[{"databaseId":9,"headSha":"` + testCommit + `","headBranch":"main","status":"queued"}]`, attempt: 1, pending: true},
		{name: "CI failed", op: CI, payload: `[{"databaseId":9,"headSha":"` + testCommit + `","headBranch":"main","status":"completed","conclusion":"failure"}]`, wantError: true},
		{name: "release not started", op: Discover, payload: `[]`, attempt: 1, pending: true},
		{name: "release timed out", op: Discover, payload: `[]`, attempt: 40, wantError: true},
		{name: "release wrong tag", op: Discover, payload: `[{"databaseId":9,"headSha":"` + testCommit + `","headBranch":"v0.1.2"}]`, attempt: 1, pending: true},
		{name: "jobs running", op: Jobs, payload: `{"databaseId":5,"headSha":"` + testCommit + `","headBranch":"v0.1.1","status":"in_progress"}`, attempt: 1, pending: true},
		{name: "wrong run ID", op: Jobs, payload: `{"databaseId":6,"headSha":"` + testCommit + `","headBranch":"v0.1.1","status":"completed","conclusion":"success"}`, wantError: true},
		{name: "jobs failed", op: Jobs, payload: `{"databaseId":5,"headSha":"` + testCommit + `","headBranch":"v0.1.1","status":"completed","conclusion":"failure"}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			adapter, commands, state := newReleaseHarness(t, Live)
			state.RunID = 5
			adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) { return tc.payload, nil, c.Name == "gh" && c.Args[0] == "run" }}
			result, err := adapter.Execute(t.Context(), Request{State: state, Operation: tc.op, Attempt: tc.attempt}, func(string) {})
			if (err != nil) != tc.wantError || result.Pending != tc.pending {
				t.Fatalf("pending=%v err=%v", result.Pending, err)
			}
		})
	}
}

func TestResumeAllowsNewerMainButRequiresSameTaggedSource(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	result, err := adapter.Execute(t.Context(), Request{State: state, Operation: PushTag}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	adapter.Resume = true
	ancestorChecked := false
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		switch strings.Join(c.Args, " ") {
		case "rev-parse origin/main":
			return strings.Repeat("c", 40), nil, true
		case "merge-base --is-ancestor " + testCommit + " origin/main":
			ancestorChecked = true
			return "", nil, true
		}
		return "", nil, false
	}}
	_, err = adapter.Execute(t.Context(), Request{State: result.State, Operation: Source}, func(string) {})
	if err != nil || !ancestorChecked {
		t.Fatalf("valid recovery blocked: %v", err)
	}
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		return strings.Repeat("c", 40), nil, strings.Join(c.Args, " ") == "rev-parse HEAD"
	}}
	if _, err = adapter.Execute(t.Context(), Request{Operation: Inspect}, func(string) {}); err == nil {
		t.Fatal("resumed changed source")
	}
}

func TestPublicationRejectsReplacedAttachments(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	commands.local, commands.remote = true, true
	result, err := adapter.Execute(t.Context(), Request{State: state, Operation: FetchArtifact}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := adapter.Execute(t.Context(), Request{State: result.State, Operation: RemoteArtifact}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	state = verified.State
	state.Notes = "Reviewed notes"
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		if c.Name == "gh" && c.Args[0] == "api" {
			data := commands.releases()
			data[1]["assets"].([]map[string]any)[0]["id"] = 99
			return jsonValue(data), nil, true
		}
		return "", nil, false
	}}
	if _, err = adapter.Execute(t.Context(), Request{State: state, Operation: PublishRelease}, func(string) {}); err == nil {
		t.Fatal("published replacement assets")
	}
	if commands.published {
		t.Fatal("publication command ran")
	}
}

type failingJournal struct{ memoryJournal }

func (j *failingJournal) Save(State) error { return fmt.Errorf("disk full") }

func TestTagIntentMustBeDurableBeforeEveryMutationAttempt(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	adapter.Journal = &failingJournal{}
	for range 2 {
		result, err := adapter.Execute(t.Context(), Request{State: state, Operation: PushTag, Retry: true}, func(string) {})
		if err == nil {
			t.Fatal("ignored journal error")
		}
		state = result.State
		if commands.local || commands.remote {
			t.Fatal("mutated a tag without durable intent")
		}
	}
}

func TestPublicationCannotReplaceNewerLatest(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	commands.local, commands.remote = true, true
	for _, op := range []Operation{FetchArtifact, RemoteArtifact} {
		result, err := adapter.Execute(t.Context(), Request{State: state, Operation: op}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		state = result.State
	}
	state.Notes = "Reviewed notes"
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		if c.Name == "gh" && c.Args[0] == "api" {
			data := commands.releases()
			data = append(data, map[string]any{"id": 3, "tag_name": "v0.2.0", "draft": false})
			return jsonValue(data), nil, true
		}
		return "", nil, false
	}}
	_, err := adapter.Execute(t.Context(), Request{State: state, Operation: PublishRelease}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("newer latest ignored: %v", err)
	}
	if commands.published {
		t.Fatal("publication command ran")
	}
}

func TestDraftRejectsUnexpectedAttachments(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	commands.remote = true
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		if c.Name != "gh" || c.Args[0] != "api" {
			return "", nil, false
		}
		data := commands.releases()
		assets := data[1]["assets"].([]map[string]any)
		data[1]["assets"] = append(assets, map[string]any{"id": 13, "name": "unexpected.exe", "size": 100})
		return jsonValue(data), nil, true
	}}
	_, err := adapter.Execute(t.Context(), Request{State: state, Operation: FetchArtifact}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "only the expected archive") {
		t.Fatalf("unexpected attachment accepted: %v", err)
	}
	for _, c := range commands.calls {
		if slices.Contains(c.Args, "download") {
			t.Fatal("download started before attachment validation")
		}
	}
}

func TestPreflightChecksPythonMinimum(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Rehearsal)
	checked := false
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		if c.Name != "python3" {
			return "", nil, false
		}
		checked = true
		if !strings.Contains(strings.Join(c.Args, " "), "sys.version_info >= (3, 11)") {
			t.Fatal("Python minimum not checked")
		}
		return "", fmt.Errorf("Python 3.11 or newer is required"), true
	}}
	_, err := adapter.Execute(t.Context(), Request{State: state, Operation: Tools}, func(string) {})
	if !checked || err == nil || !strings.Contains(err.Error(), "Python 3.11") {
		t.Fatalf("preflight did not explain Python requirement: %v", err)
	}
}

func TestInspectRequiresRestartAfterFastForward(t *testing.T) {
	adapter, commands, _ := newReleaseHarness(t, Live)
	merged := false
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		args := strings.Join(c.Args, " ")
		if args == "merge --ff-only origin/main" {
			merged = true
			return "", nil, true
		}
		if args == "rev-parse HEAD" && merged {
			return strings.Repeat("c", 40), nil, true
		}
		if merged && c.Name == "gh" {
			t.Fatal("continued with stale tool after fast-forward")
		}
		return "", nil, false
	}}
	_, err := adapter.Execute(t.Context(), Request{Operation: Inspect}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "rerun make release") {
		t.Fatalf("did not require restart: %v", err)
	}
}

func TestConcurrentHigherReleaseRemainsLatest(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	commands.local, commands.remote = true, true
	for _, op := range []Operation{FetchArtifact, RemoteArtifact} {
		result, err := adapter.Execute(t.Context(), Request{State: state, Operation: op}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		state = result.State
	}
	state.Notes = "Reviewed notes"
	commands.published, commands.notes = true, state.Notes
	newer := map[string]any{"id": 3, "tag_name": "v0.2.0", "draft": false}
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		// The maintainer has published, but a newer release is now latest.
		// Verification must not change either release's visibility or latest status.
		if !commands.published || c.Name != "gh" || c.Args[0] != "api" {
			return "", nil, false
		}
		if strings.HasSuffix(c.Args[3], "/latest") {
			return jsonValue(newer), nil, true
		}
		data := append(commands.releases(), newer)
		return jsonValue(data), nil, true
	}}
	result, err := adapter.Execute(t.Context(), Request{State: state, Operation: PublishRelease}, func(string) {})
	if err != nil || !result.State.Published || !strings.Contains(result.Message, "v0.2.0 remains latest") {
		t.Fatalf("concurrent latest not reconciled: %+v %v", result, err)
	}
	// Reconciliation after a lost response must also accept a newer latest.
	result, err = adapter.Execute(t.Context(), Request{State: result.State, Operation: PublishRelease, Retry: true}, func(string) {})
	if err != nil || !result.State.Published {
		t.Fatalf("publication recovery blocked: %v", err)
	}
}

func TestUnfinishedReleasePreventsCheckoutChanges(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	state.TagIntent = true
	if err := adapter.Journal.Save(state); err != nil {
		t.Fatal(err)
	}
	commands.calls = nil
	_, err := adapter.Execute(t.Context(), Request{Operation: Inspect}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "use --resume") {
		t.Fatalf("unfinished release not rejected: %v", err)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("commands ran before recovery guard: %+v", commands.calls)
	}
}

func TestResumePushesMissingTagAfterMainAdvances(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	state.TagIntent = true
	if err := adapter.Journal.Save(state); err != nil {
		t.Fatal(err)
	}
	adapter.Resume = true
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		switch strings.Join(c.Args, " ") {
		case "rev-parse origin/main":
			return strings.Repeat("c", 40), nil, true
		case "merge-base --is-ancestor " + testCommit + " origin/main":
			return "", nil, true
		}
		return "", nil, false
	}}
	for _, op := range []Operation{Inspect, Source, PushTag} {
		result, err := adapter.Execute(t.Context(), Request{State: state, Operation: op}, func(string) {})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		state = result.State
	}
	if !commands.remote {
		t.Fatal("missing tag was not pushed")
	}
}

func TestPublicationVerifiesPublicAssetsAndReviewedNotes(t *testing.T) {
	for _, name := range []string{"replaced attachment", "added attachment", "changed notes", "changed title", "changed prerelease", "already public with different notes"} {
		t.Run(name, func(t *testing.T) {
			adapter, commands, state := newReleaseHarness(t, Live)
			commands.local, commands.remote = true, true
			for _, op := range []Operation{FetchArtifact, RemoteArtifact} {
				result, err := adapter.Execute(t.Context(), Request{State: state, Operation: op}, func(string) {})
				if err != nil {
					t.Fatal(err)
				}
				state = result.State
			}
			state.Notes = "Reviewed notes"
			state.PublishIntent = true
			commands.published, commands.notes = true, state.Notes
			if name == "already public with different notes" {
				commands.notes = "Someone else's notes"
			}
			reads := 0
			adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
				if c.Name != "gh" || !slices.Contains(c.Args, "repos/example/mapbox-tui/releases?per_page=100") {
					return "", nil, false
				}
				reads++
				if reads == 1 {
					return "", nil, false
				}
				data := commands.releases()
				assets := data[1]["assets"].([]map[string]any)
				switch name {
				case "replaced attachment":
					assets[0]["id"] = 99
				case "added attachment":
					data[1]["assets"] = append(assets, map[string]any{"id": 99, "name": "extra.exe", "size": 100})
				case "changed prerelease":
					data[1]["prerelease"] = true
				case "changed title":
					data[1]["name"] = "Unexpected title"
				case "changed notes":
					data[1]["body"] = "Unexpected notes"
				}
				return jsonValue(data), nil, true
			}}
			result, err := adapter.Execute(t.Context(), Request{State: state, Operation: PublishRelease}, func(string) {})
			if err == nil || result.State.Published {
				t.Fatalf("unverified publication accepted: %+v %v", result, err)
			}
			if !strings.Contains(err.Error(), "release is public") {
				t.Fatalf("failure did not report public state: %v", err)
			}
		})
	}
}

func TestRemoteTagRequiresMatchingObjectAndCommit(t *testing.T) {
	for _, name := range []string{"different annotation", "different commit", "lightweight tag"} {
		t.Run(name, func(t *testing.T) {
			adapter, commands, state := newReleaseHarness(t, Live)
			commands.local, commands.remote = true, true
			state.TagIntent = true
			adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
				if c.Name != "git" || !slices.Contains(c.Args, "ls-remote") {
					return "", nil, false
				}
				object, commit := strings.Repeat("b", 40), testCommit
				if name == "different annotation" {
					object = strings.Repeat("c", 40)
				}
				if name == "different commit" {
					commit = strings.Repeat("d", 40)
				}
				raw := object + "\trefs/tags/v0.1.1"
				if name != "lightweight tag" {
					raw += "\n" + commit + "\trefs/tags/v0.1.1^{}"
				}
				return raw, nil, true
			}}
			_, err := adapter.Execute(t.Context(), Request{State: state, Operation: PushTag}, func(string) {})
			if err == nil || !strings.Contains(err.Error(), "remote annotated tag") {
				t.Fatalf("accepted mismatched tag: %v", err)
			}
		})
	}
}

func TestPublicationRejectsChangedDraftTitle(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	commands.local, commands.remote = true, true
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		if c.Name != "gh" || !slices.Contains(c.Args, "repos/example/mapbox-tui/releases?per_page=100") {
			return "", nil, false
		}
		data := commands.releases()
		data[1]["name"] = "Unexpected title"
		return jsonValue(data), nil, true
	}}
	_, err := adapter.Execute(t.Context(), Request{State: state, Operation: PublishRelease}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "release title") || commands.published {
		t.Fatalf("renamed draft was accepted: %v", err)
	}
}

func TestDownloadedBinaryMustMatchLocalBuildBeforeExecution(t *testing.T) {
	for _, name := range []string{"replaced binary and checksum", "missing local verification"} {
		t.Run(name, func(t *testing.T) {
			adapter, commands, state := newReleaseHarness(t, Live)
			state.DownloadDir = t.TempDir()
			if name == "missing local verification" {
				state.LocalArtifactDigest = ""
				writeReleaseArchive(t, state.DownloadDir)
			} else {
				writeReleaseArchiveWithBinary(t, state.DownloadDir, "untrusted executable")
			}
			before := len(commands.calls)
			result, err := adapter.Execute(t.Context(), Request{State: state, Operation: RemoteArtifact}, func(string) {})
			if err == nil || !strings.Contains(err.Error(), "will not be executed") || result.State.AssetIdentity != "" {
				t.Fatalf("unauthenticated executable accepted: %+v, %v", result, err)
			}
			if len(commands.calls) != before {
				t.Fatal("downloaded binary was executed before authentication")
			}
		})
	}
}

func TestDownloadRejectsOversizedAssets(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"archive", "checksum"}[index], func(t *testing.T) {
			adapter, commands, state := newReleaseHarness(t, Live)
			commands.local, commands.remote = true, true
			adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
				if c.Name != "gh" || !slices.Contains(c.Args, "repos/example/mapbox-tui/releases?per_page=100") {
					return "", nil, false
				}
				data := commands.releases()
				limit := int64(256 << 20)
				if index == 1 {
					limit = 4 << 10
				}
				data[1]["assets"].([]map[string]any)[index]["size"] = limit + 1
				return jsonValue(data), nil, true
			}}
			_, err := adapter.Execute(t.Context(), Request{State: state, Operation: FetchArtifact}, func(string) {})
			if err == nil || !strings.Contains(err.Error(), "download limit") {
				t.Fatalf("oversized asset accepted: %v", err)
			}
			for _, c := range commands.calls {
				if c.Name == "gh" && slices.Contains(c.Args, "download") {
					t.Fatal("oversized asset was downloaded")
				}
			}
		})
	}
}

func TestPreflightRequiresRepositoryWriteAccess(t *testing.T) {
	for _, response := range []string{`{"permissions":{"push":false}}`, `{}`} {
		t.Run(response, func(t *testing.T) {
			adapter, commands, state := newReleaseHarness(t, Live)
			adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
				return response, nil, c.Name == "gh" && slices.Contains(c.Args, "repos/example/mapbox-tui")
			}}
			_, err := adapter.Execute(t.Context(), Request{State: state, Operation: Tools}, func(string) {})
			if err == nil || !strings.Contains(err.Error(), "write access") {
				t.Fatalf("read-only GitHub account accepted: %v", err)
			}
			if commands.local || commands.remote {
				t.Fatal("preflight mutated a tag")
			}
		})
	}
}

func TestResumeReconcilesDraftAfterFailedWorkflowRerun(t *testing.T) {
	for _, name := range []string{"matching draft", "no draft", "wrong title", "wrong tag commit"} {
		t.Run(name, func(t *testing.T) {
			adapter, commands, state := newReleaseHarness(t, Live)
			adapter.Resume = true
			commands.local, commands.remote = true, true
			state.RunID, state.TagIntent = 5, true
			adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
				if c.Name == "gh" && c.Args[0] == "run" {
					return `{"databaseId":5,"headSha":"` + testCommit + `","headBranch":"v0.1.1","status":"completed","conclusion":"failure"}`, nil, true
				}
				if name == "wrong tag commit" && c.Name == "git" && c.Args[0] == "ls-remote" {
					return strings.Repeat("b", 40) + "\trefs/tags/v0.1.1\n" +
						strings.Repeat("c", 40) + "\trefs/tags/v0.1.1^{}", nil, true
				}
				if c.Name == "gh" && slices.Contains(c.Args, "repos/example/mapbox-tui/releases?per_page=100") {
					data := commands.releases()
					if name == "no draft" {
						data = data[:1]
					}
					if name == "wrong title" {
						data[1]["name"] = "Wrong title"
					}
					return jsonValue(data), nil, true
				}
				return "", nil, false
			}}
			result, err := adapter.Execute(t.Context(), Request{State: state, Operation: Jobs}, func(string) {})
			if name != "matching draft" {
				if err == nil {
					t.Fatal("invalid recovery accepted")
				}
				return
			}
			if err != nil || result.Pending || result.State.AssetIdentity != "" {
				t.Fatalf("matching draft not reconciled for further verification: %+v %v", result, err)
			}
			for _, op := range []Operation{FetchArtifact, RemoteArtifact} {
				result, err = adapter.Execute(t.Context(), Request{State: result.State, Operation: op}, func(string) {})
				if err != nil {
					t.Fatal(err)
				}
			}
			if result.State.AssetIdentity == "" {
				t.Fatal("reconciled draft attachments were not verified")
			}
		})
	}
}

func TestReleaseHistoryDecodesFilteredPages(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	adapter.Runner = commandOverride{base: commands, override: func(c Command) (string, error, bool) {
		if c.Name != "gh" || !slices.Contains(c.Args, "repos/example/mapbox-tui/releases?per_page=100") {
			return "", nil, false
		}
		if !slices.Contains(c.Args, "--jq") || slices.Contains(c.Args, "--slurp") ||
			!strings.Contains(c.Args[len(c.Args)-1], strconv.Quote(state.Version)) {
			t.Fatalf("missing selected-release history filter: %v", c.Args)
		}
		return `[{"id":1,"tag_name":"v0.1.0","draft":false,"prerelease":false}]
[{"id":2,"tag_name":"v0.1.1","draft":true,"body":"Selected notes"}]`, nil, true
	}}
	releases, err := adapter.releases(t.Context(), state)
	if err != nil || len(releases) != 2 || releases[0].Body != "" || releases[1].Body != "Selected notes" {
		t.Fatalf("filtered release pages: %+v %v", releases, err)
	}
}

func TestPublicationAcceptsEquivalentLineEndings(t *testing.T) {
	for _, notes := range []struct {
		name, reviewed, published string
	}{
		{name: "web editor", reviewed: "# Release\n\nChanges  \nNext\n", published: "# Release\r\n\r\nChanges  \r\nNext\r\n"},
		{name: "reverse", reviewed: "Changes\r\nNext", published: "Changes\nNext"},
		{name: "mixed", reviewed: "Changes\r\nNext\nEnd", published: "Changes\nNext\r\nEnd"},
	} {
		t.Run(notes.name, func(t *testing.T) {
			for _, op := range []Operation{ReadNotes, PreparePublication, PublishRelease} {
				t.Run(string(op), func(t *testing.T) {
					adapter, commands, state := newReleaseHarness(t, Live)
					commands.remote, commands.published, commands.notes = true, true, notes.published
					state.Notes, state.PublishIntent = notes.reviewed, true
					release, err := adapter.requireRelease(t.Context(), state)
					if err != nil {
						t.Fatal(err)
					}
					state.AssetIdentity = assetIdentity(release)
					result, err := adapter.Execute(t.Context(), Request{State: state, Operation: op}, func(string) {})
					if err != nil {
						t.Fatal(err)
					}
					if op == PublishRelease && !result.State.Published {
						t.Fatal("publication not verified")
					}
					if op == ReadNotes && (!result.State.NotesPublic || result.State.Notes != notes.published) {
						t.Fatal("published notes were not loaded for review")
					}
				})
			}
		})
	}
}

func TestChangedPublicNotesRequireNewConfirmation(t *testing.T) {
	for _, operation := range []int{0, 1} {
		for _, published := range []string{"Changed release notes", "Reviewed notes  \nNext", "Reviewed notes\n\nNext"} {
			t.Run(fmt.Sprintf("%d/%q", operation, published), func(t *testing.T) {
				adapter, commands, state := newReleaseHarness(t, Live)
				commands.remote, commands.published, commands.notes = true, true, published
				state.Notes, state.PublishIntent = "Reviewed notes\nNext", true
				release, err := adapter.requireRelease(t.Context(), state)
				if err != nil {
					t.Fatal(err)
				}
				state.AssetIdentity = assetIdentity(release)
				session := Session{State: state, Step: Publish, operation: operation, approved: true}
				session.enter()
				advance := func() {
					t.Helper()
					result, err := adapter.Execute(t.Context(), session.Request(), func(string) {})
					session.Accept(result, err)
				}
				advance()
				if session.Step != Notes || session.Status != Running || session.Published {
					t.Fatalf("changed notes did not return to review: %+v", session)
				}
				advance()
				if session.Status != Confirmation || session.Notes != published || !session.NotesPublic {
					t.Fatalf("current public notes not presented for approval: %+v", session)
				}
				for _, answer := range []string{"", "n"} {
					session.Confirm(answer)
					if session.Step != Notes || session.Status != Confirmation {
						t.Fatal("notes accepted without explicit approval")
					}
				}
				session.Confirm("y")
				advance()
				if session.Step != Publish || session.Status != Confirmation {
					t.Fatal("publication confirmation was skipped")
				}
				session.Confirm("y")
				advance()
				if session.Status != Done || !session.Published {
					t.Fatalf("accepted notes did not complete publication: %+v", session)
				}
				for _, call := range commands.calls {
					if slices.Contains(call.Args, "PATCH") {
						t.Fatal("public notes were overwritten")
					}
				}
			})
		}
	}
}

func TestPreparationPreservesIntentForPublishedNotes(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	commands.local, commands.remote = true, true
	state.TagIntent = true
	state.Notes = "Reviewed draft notes"
	release, err := adapter.requireRelease(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	state.AssetIdentity = assetIdentity(release)
	// The maintainer publishes in GitHub before preparation first runs.
	commands.published, commands.notes = true, "Changed published notes"
	result, err := adapter.Execute(t.Context(), Request{State: state, Operation: PreparePublication}, func(string) {})
	if _, changed := errors.AsType[*publicNotesChangedError](err); !changed || !result.State.PublishIntent {
		t.Fatalf("preparation did not preserve intent for notes recovery: %+v %v", result, err)
	}
	saved, exists, err := adapter.Journal.Load()
	if err != nil || !exists || !saved.PublishIntent || saved.Notes != state.Notes {
		t.Fatalf("intent was not saved before returning for review: %+v %v", saved, err)
	}
	// A restart before the notes reload must still reconcile the public release.
	adapter.Resume = true
	result, err = adapter.Execute(t.Context(), Request{Operation: Inspect}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	result, err = adapter.Execute(t.Context(), Request{State: result.State, Operation: ReadNotes}, func(string) {})
	if err != nil || !result.State.NotesPublic || result.State.Notes != commands.notes {
		t.Fatalf("resume failed to reload public notes: %+v %v", result, err)
	}
}

func TestPreparationStopsIfIntentCannotBeSaved(t *testing.T) {
	adapter, commands, state := newReleaseHarness(t, Live)
	adapter.Journal = &failingJournal{}
	commands.calls = nil
	_, err := adapter.Execute(t.Context(), Request{State: state, Operation: PreparePublication}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("journal failure was not reported: %v", err)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("commands ran before publication intent was saved: %+v", commands.calls)
	}
}
