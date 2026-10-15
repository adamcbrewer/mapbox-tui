package release

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RealAdapter reuses the repository's Make targets and GitHub release workflow.
// Mode is fixed at construction; a request cannot upgrade rehearsal privileges.
type RealAdapter struct {
	Mode      Mode
	Root      string
	Runner    CommandRunner
	Journal   Journal
	Resume    bool
	temporary []string
}

func (r *RealAdapter) Cleanup() {
	for _, path := range r.temporary {
		os.RemoveAll(path)
	}
}

func (r *RealAdapter) run(ctx context.Context, c Command, emit func(string)) (string, error) {
	c.Dir = r.Root
	return r.Runner.Run(ctx, c, emit)
}
func (r *RealAdapter) git(ctx context.Context, args ...string) (string, error) {
	return r.run(ctx, Command{Name: "git", Args: args}, nil)
}
func (r *RealAdapter) gh(ctx context.Context, state State, args ...string) (string, error) {
	return r.run(ctx, Command{Name: "gh", Args: append(args, "--repo", state.Host+"/"+state.Repository)}, nil)
}
func (r *RealAdapter) api(ctx context.Context, state State, path string, extra ...string) (string, error) {
	args := append([]string{"api", "--hostname", state.Host, "repos/" + state.Repository + path}, extra...)
	return r.run(ctx, Command{Name: "gh", Args: args}, nil)
}

func (r *RealAdapter) Execute(ctx context.Context, request Request, emit func(string)) (Result, error) {
	if r.Mode != Live && r.Mode != Rehearsal {
		return Result{}, fmt.Errorf("real adapter requires release or rehearsal mode")
	}
	if request.Operation != Inspect && request.State.Mode != r.Mode {
		return Result{}, fmt.Errorf("execution mode mismatch")
	}
	if request.Operation != Inspect {
		state := request.State
		if !stableVersion.MatchString(state.Version) || !commitPattern.MatchString(state.Commit) ||
			state.Root != r.Root || !repoPattern.MatchString(state.Repository) || !hostPattern.MatchString(state.Host) {
			return Result{}, fmt.Errorf("invalid release identity; inspect the repository again")
		}
	}
	if r.Mode != Live && (request.Operation == PushTag || request.Operation == PreparePublication || request.Operation == PublishRelease) {
		return Result{}, fmt.Errorf("rehearsal cannot create tags or publish releases")
	}
	result, err := r.execute(ctx, request, emit)
	if err != nil {
		return result, err
	}
	if !result.Pending && result.State.Version != "" {
		if err = r.Journal.Save(result.State); err != nil {
			return result, fmt.Errorf("save recovery state: %w", err)
		}
	}
	return result, nil
}

func (r *RealAdapter) execute(ctx context.Context, q Request, emit func(string)) (Result, error) {
	state := q.State
	result := Result{State: state}
	var err error
	switch q.Operation {
	case Inspect:
		result.State, err = r.inspect(ctx)
		result.Message = "Repository, source and release history loaded"
	case Tools:
		err = r.preflight(ctx, state)
		result.Message = "Toolchain, terminal dependencies and gh authentication ready"
	case Source:
		err = r.checkSource(ctx, state)
		if err == nil {
			args := []string{"log", "--oneline", "-20", state.Commit}
			if state.Policy.Latest != "" {
				args = append([]string{"log", "--oneline", "-50"}, state.Policy.Latest+".."+state.Commit)
			}
			var changes string
			changes, err = r.git(ctx, args...)
			if err == nil {
				emit("Changes since the previous release:\n" + changes)
			}
		}
		result.Message = "Clean source selected: " + state.Commit
	case CI:
		if state.Mode == Rehearsal {
			result.Message = "Rehearsal: remote CI is not a publication gate; local checks follow"
		} else {
			return r.waitCI(ctx, q)
		}
	case Check, Interactions, Build:
		if err = r.checkSource(ctx, state); err != nil {
			break
		}
		args := []string{"check"}
		if q.Operation == Interactions {
			args = []string{"interactions"}
		}
		if q.Operation == Build {
			args = []string{"package", "VERSION=" + state.Version, "COMMIT=" + shortCommit(state.Commit)}
		}
		command := Command{Name: "make", Args: args, Timeout: 45 * time.Minute, Env: map[string]string{"MAPBOX_ACCESS_TOKEN": ""}}
		if q.Operation == Interactions {
			command.Unset = []string{"NO_COLOR"}
		}
		_, err = r.run(ctx, command, emit)
		result.Message = map[Operation]string{Check: "Formatting, tests, vet and race checks passed", Interactions: "Terminal interactions passed", Build: "Linux amd64 archive prepared"}[q.Operation]
	case LocalArtifact:
		result.State.LocalDir, err = r.freshDirectory("mapbox-release-local-")
		if err == nil {
			err = copyArtifacts(filepath.Join(r.Root, "dist"), result.State.LocalDir, state.Version)
		}
		if err == nil {
			result.State.Binary, err = verifyArtifact(ctx, result.State.LocalDir, state.Version)
		}
		if err == nil {
			result.State.LocalArtifactDigest, err = artifactDigest(ctx, result.State.LocalDir, state.Version)
		}
		result.Message = "Local checksum and archive contents verified"
	case LocalSmoke:
		err = r.smoke(ctx, state, emit)
		result.Message = "Packaged version, commit and offline rendering verified"
	case PushTag:
		result.State, err = r.pushTag(ctx, state)
		result.Message = "Annotated tag verified on origin; only the selected tag was pushed"
	case Discover:
		return r.discover(ctx, q)
	case Jobs:
		return r.waitJobs(ctx, q, emit)
	case FetchArtifact:
		result.State, err = r.download(ctx, state)
		result.Message = "Release attachments downloaded into a fresh private directory"
	case RemoteArtifact:
		result.State.Binary, err = verifyArtifact(ctx, state.DownloadDir, state.Version)
		if err == nil {
			var digest string
			digest, err = artifactDigest(ctx, state.DownloadDir, state.Version)
			if err == nil && (state.LocalArtifactDigest == "" || digest != state.LocalArtifactDigest) {
				err = fmt.Errorf("downloaded archive differs from the verified local build; it will not be executed or published. Check source, Go toolchain and build settings")
			}
		}
		if err == nil {
			err = r.smoke(ctx, result.State, emit)
		}
		if err == nil {
			result.State.AssetIdentity = state.DownloadIdentity
		}
		result.Message = "Downloaded checksum, version, commit and offline rendering verified"
	case OpenViewer:
		// The UI owns terminal handoff via tea.ExecProcess; this operation completes
		// only after the child exits. Headless tests use an injected adapter instead.
		return result, fmt.Errorf("viewer requires interactive terminal handoff")
	case ReadNotes:
		var release *githubRelease
		release, err = r.requireRelease(ctx, state)
		if err == nil && state.PublishIntent && !release.Draft && release.Body != state.Notes {
			err = fmt.Errorf("public release notes differ from the reviewed notes; inspect the release before continuing")
		}
		if err == nil {
			result.State.Notes = release.Body
		}
		result.Message = "Release notes loaded; review or edit before publication"
	case PreparePublication, PublishRelease:
		return r.publish(ctx, q)
	default:
		err = fmt.Errorf("unknown release operation %q", q.Operation)
	}
	return result, err
}

func shortCommit(commit string) string { return commit[:min(12, len(commit))] }

func (r *RealAdapter) inspect(ctx context.Context) (State, error) {
	state := State{Mode: r.Mode, Root: r.Root}
	saved, exists, err := r.Journal.Load()
	if err != nil {
		return state, err
	}
	if !r.Resume && exists && saved.TagIntent && !saved.Published {
		return state, fmt.Errorf("unfinished release %s exists; use --resume to reconcile it", saved.Version)
	}
	origin, err := r.git(ctx, "remote", "get-url", "origin")
	if err != nil {
		return state, fmt.Errorf("read origin: %w", err)
	}
	state.Host, state.Repository, err = parseOrigin(origin)
	if err != nil {
		return state, err
	}
	branch, err := r.git(ctx, "branch", "--show-current")
	if err != nil {
		return state, err
	}
	if r.Mode == Live && branch != "main" {
		return state, fmt.Errorf("release from main; switch to main and run again")
	}
	if err = r.clean(ctx); err != nil {
		return state, err
	}
	if _, err = r.git(ctx, "fetch", "origin", "main", "--tags"); err != nil {
		return state, fmt.Errorf("fetch origin without overwriting tags: %w", err)
	}
	before, err := r.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return state, err
	}
	if r.Mode == Live && !r.Resume {
		if _, err = r.git(ctx, "merge", "--ff-only", "origin/main"); err != nil {
			return state, fmt.Errorf("main diverged; reconcile it before releasing: %w", err)
		}
	}
	state.Commit, err = r.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return state, err
	}
	if state.Commit != before {
		return state, fmt.Errorf("main was fast-forwarded; rerun make release to load the updated release tool")
	}
	state.Policy, err = r.policy(ctx, state)
	if err != nil {
		return state, err
	}
	if r.Resume {
		if !exists {
			return state, fmt.Errorf("no release journal exists; start without --resume")
		}
		sameSource := saved.Root == state.Root && saved.Repository == state.Repository && saved.Host == state.Host
		if !sameSource || saved.Commit != state.Commit || saved.Mode != r.Mode {
			return state, fmt.Errorf("saved release belongs to different source, repository or mode; use a fresh version after reconciling the old release")
		}
		state.Version = saved.Version
		state.TagIntent, state.PublishIntent, state.Published = saved.TagIntent, saved.PublishIntent, saved.Published
		state.RunID, state.URL = saved.RunID, saved.URL
		state.Notes = saved.Notes
		if state.TagIntent {
			if err = r.checkRemoteTag(ctx, state, true); err != nil {
				return state, err
			}
		}
	}
	return state, nil
}

var commitPattern = regexp.MustCompile(`^([a-f0-9]{40}|[a-f0-9]{64})$`)
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func parseOrigin(origin string) (string, string, error) {
	if strings.HasPrefix(origin, "git@") {
		host, path, ok := strings.Cut(strings.TrimPrefix(origin, "git@"), ":")
		if ok {
			origin = "ssh://git@" + host + "/" + path
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || !hostPattern.MatchString(parsed.Hostname()) || (parsed.Scheme != "ssh" && parsed.Scheme != "https") {
		return "", "", fmt.Errorf("origin must be an SSH or HTTPS GitHub repository URL")
	}
	repo := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	if !repoPattern.MatchString(repo) {
		return "", "", fmt.Errorf("unsupported origin repository path")
	}
	return parsed.Hostname(), repo, nil
}

func (r *RealAdapter) clean(ctx context.Context) error {
	status, err := r.git(ctx, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("working tree is not clean; commit or move changes before continuing")
	}
	return nil
}
func (r *RealAdapter) checkSource(ctx context.Context, state State) error {
	if err := r.clean(ctx); err != nil {
		return err
	}
	commit, err := r.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if commit != state.Commit {
		return fmt.Errorf("source changed since selection; prepare a new release version")
	}
	if state.Mode == Live {
		branch, err := r.git(ctx, "branch", "--show-current")
		if err != nil {
			return err
		}
		if branch != "main" {
			return fmt.Errorf("release branch changed; expected main")
		}
		remote, err := r.git(ctx, "rev-parse", "origin/main")
		if err != nil {
			return err
		}
		if remote != state.Commit {
			if r.Resume && state.TagIntent {
				_, err = r.git(ctx, "merge-base", "--is-ancestor", state.Commit, "origin/main")
				if err != nil {
					return fmt.Errorf("saved release source is no longer on main: %w", err)
				}
				return r.checkRemoteTag(ctx, state, true)
			}
			return fmt.Errorf("selected commit does not match origin/main; update main before releasing")
		}
	}
	return nil
}

func (r *RealAdapter) preflight(ctx context.Context, state State) error {
	raw, err := r.run(ctx, Command{Name: "go", Args: []string{"env", "-json", "GOVERSION", "GOOS", "GOARCH"}}, nil)
	if err != nil {
		return fmt.Errorf("Go is unavailable; check mise trust and mise install: %w", err)
	}
	var env struct{ GOVERSION, GOOS, GOARCH string }
	if err = json.Unmarshal([]byte(raw), &env); err != nil {
		return fmt.Errorf("read Go environment: %w", err)
	}
	mod, err := os.ReadFile(filepath.Join(r.Root, "go.mod"))
	if err != nil {
		return err
	}
	required := ""
	for _, line := range strings.Split(string(mod), "\n") {
		if strings.HasPrefix(line, "go ") {
			required = "go" + strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	if env.GOVERSION != required || env.GOOS != "linux" || env.GOARCH != "amd64" {
		return fmt.Errorf("release requires %s on linux/amd64; got %s on %s/%s (check mise trust/install)", required, env.GOVERSION, env.GOOS, env.GOARCH)
	}
	for _, command := range []Command{
		{Name: "make", Args: []string{"--version"}}, {Name: "python3", Args: []string{"-c", "import sys; sys.exit(0 if sys.version_info >= (3, 11) else 'Python 3.11 or newer is required')"}},
		{Name: "tmux", Args: []string{"-V"}}, {Name: "rsvg-convert", Args: []string{"--version"}},
	} {
		_, err := r.run(ctx, command, nil)
		if err != nil {
			return fmt.Errorf("missing terminal-check dependency %s; see docs/DEVELOPMENT.md: %w", command.Name, err)
		}

	}
	_, err = r.run(ctx, Command{Name: "gh", Args: []string{"auth", "status", "--hostname", state.Host}}, nil)
	if err != nil {
		return fmt.Errorf("authenticate with gh auth login --hostname %s: %w", state.Host, err)
	}
	if state.Mode == Live {
		raw, err := r.api(ctx, state, "")
		if err != nil {
			return fmt.Errorf("check GitHub repository write permission: %w", err)
		}
		var repository struct {
			Permissions struct{ Push bool } `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(raw), &repository); err != nil {
			return fmt.Errorf("read GitHub repository permissions: %w", err)
		}
		if !repository.Permissions.Push {
			return fmt.Errorf("active gh account needs write access to %s to update release notes; select a writable account before pushing the tag", state.Repository)
		}
	}
	return nil
}

type githubRelease struct {
	ID         int64  `json:"id"`
	Tag        string `json:"tag_name"`
	Name       string `json:"name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Body       string `json:"body"`
	URL        string `json:"html_url"`
	Assets     []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r *RealAdapter) releases(ctx context.Context, state State) ([]githubRelease, error) {
	filter := "map({id,tag_name,draft,prerelease} + (if .tag_name == " +
		strconv.Quote(state.Version) + " then {name,body,html_url,assets} else {} end))"
	raw, err := r.api(ctx, state, "/releases?per_page=100", "--paginate", "--jq", filter)
	if err != nil {
		return nil, fmt.Errorf("read GitHub releases (check authentication): %w", err)
	}
	var all []githubRelease
	decoder := json.NewDecoder(strings.NewReader(raw))
	for {
		var page []githubRelease
		if err := decoder.Decode(&page); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode releases: %w", err)
		}
		all = append(all, page...)
	}
	return all, nil
}
func (r *RealAdapter) policy(ctx context.Context, state State) (VersionPolicy, error) {
	policy := VersionPolicy{}
	local, err := r.git(ctx, "tag", "--list")
	if err != nil {
		return policy, err
	}
	policy.Tags = strings.Fields(local)
	remote, err := r.git(ctx, "ls-remote", "--tags", "origin")
	if err != nil {
		return policy, err
	}
	for _, line := range strings.Split(remote, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			policy.Tags = append(policy.Tags, strings.TrimSuffix(strings.TrimPrefix(fields[1], "refs/tags/"), "^{}"))
		}
	}
	releases, err := r.releases(ctx, state)
	if err != nil {
		return policy, err
	}
	for _, release := range releases {
		policy.Tags = append(policy.Tags, release.Tag)
		if !release.Draft && !release.Prerelease && stableVersion.MatchString(release.Tag) {
			if policy.Latest == "" || compareVersions(release.Tag, policy.Latest) > 0 {
				policy.Latest = release.Tag
			}
		}
	}
	slices.Sort(policy.Tags)
	policy.Tags = slices.Compact(policy.Tags)
	return policy, nil
}

func (r *RealAdapter) freshDirectory(prefix string) (string, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err == nil {
		r.temporary = append(r.temporary, dir)
	}
	return dir, err
}
func copyArtifacts(from, to, version string) error {
	for _, name := range []string{archiveName(version), archiveName(version) + ".sha256"} {
		input, err := os.Open(filepath.Join(from, name))
		if err != nil {
			return err
		}
		output, err := os.OpenFile(filepath.Join(to, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := output.ReadFrom(input)
		inputErr, outputErr := input.Close(), output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		if outputErr != nil {
			return outputErr
		}
	}
	return nil
}
func (r *RealAdapter) smoke(ctx context.Context, state State, emit func(string)) error {
	if state.Binary == "" {
		return fmt.Errorf("no verified binary available")
	}
	output, err := r.run(ctx, Command{Name: state.Binary, Args: []string{"--version"}, Env: map[string]string{"MAPBOX_ACCESS_TOKEN": ""}}, nil)
	if err != nil {
		return err
	}
	expected := "mapbox-tui " + state.Version + " (" + shortCommit(state.Commit) + ")"
	if output != expected {
		return fmt.Errorf("embedded version/commit mismatch: got %q, expected %q", output, expected)
	}
	_, err = r.run(ctx, Command{Name: state.Binary, Args: []string{"render", filepath.Join(r.Root, "fixtures/example.gpx"), "--no-provider", "--ascii", "--width", "60", "--height", "20"}, Env: map[string]string{"MAPBOX_ACCESS_TOKEN": ""}}, emit)
	return err
}

// ViewerCommand is used only with Bubble Tea's terminal-restoring ExecProcess.
func (r *RealAdapter) ViewerCommand(ctx context.Context, state State) (*exec.Cmd, error) {
	if state.Binary == "" {
		return nil, fmt.Errorf("no verified binary to launch")
	}
	if _, err := os.Stat(state.Binary); err != nil {
		return nil, fmt.Errorf("verified binary is unavailable: %w", err)
	}
	command := exec.CommandContext(ctx, state.Binary, filepath.Join(r.Root, "fixtures/example.gpx"), "--no-provider")
	command.Dir = r.Root
	command.Env = commandEnv(Command{Env: map[string]string{"MAPBOX_ACCESS_TOKEN": ""}})
	return command, nil
}

// Annotated tags are never moved. The journal records intent before the first
// mutation so retry can reconcile a push whose response was lost.
func (r *RealAdapter) pushTag(ctx context.Context, state State) (State, error) {
	if _, err := r.git(ctx, "fetch", "origin", "main"); err != nil {
		return state, err
	}
	if err := r.checkSource(ctx, state); err != nil {
		return state, err
	}
	policy, err := r.policy(ctx, state)
	if err != nil {
		return state, err
	}
	if !state.TagIntent {
		if err = policy.Validate(state.Version); err != nil {
			return state, err
		}
		state.TagIntent = true
	}
	if err = r.Journal.Save(state); err != nil {
		return state, err
	}
	local, err := r.git(ctx, "tag", "--list", state.Version)
	if err != nil {
		return state, err
	}
	if local != "" {
		commit, err := r.git(ctx, "rev-parse", state.Version+"^{commit}")
		if err != nil {
			return state, err
		}
		kind, err := r.git(ctx, "cat-file", "-t", "refs/tags/"+state.Version)
		if err != nil {
			return state, err
		}
		if commit != state.Commit || kind != "tag" {
			return state, fmt.Errorf("existing tag differs from the prepared annotated tag; never overwrite it")
		}
	} else if _, err = r.git(ctx, "tag", "-a", state.Version, state.Commit, "-m", "Release "+state.Version); err != nil {
		return state, err
	}
	if err = r.checkRemoteTag(ctx, state, true); err != nil {
		return state, err
	}
	remote, err := r.git(ctx, "ls-remote", "--tags", "origin", "refs/tags/"+state.Version)
	if err != nil {
		return state, err
	}
	if remote == "" {
		if _, err = r.git(ctx, "push", "--no-follow-tags", "origin", "refs/tags/"+state.Version+":refs/tags/"+state.Version); err != nil {
			return state, err
		}
	}
	if err = r.checkRemoteTag(ctx, state, false); err != nil {
		return state, err
	}
	return state, nil
}

func (r *RealAdapter) checkRemoteTag(ctx context.Context, state State, allowMissing bool) error {
	raw, err := r.git(ctx, "ls-remote", "--tags", "origin", "refs/tags/"+state.Version, "refs/tags/"+state.Version+"^{}")
	if err != nil {
		return err
	}
	if raw == "" && allowMissing {
		return nil
	}
	local, err := r.git(ctx, "rev-parse", "refs/tags/"+state.Version)
	if err != nil {
		return err
	}
	var object, commit string
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		switch parts[1] {
		case "refs/tags/" + state.Version:
			object = parts[0]
		case "refs/tags/" + state.Version + "^{}":
			commit = parts[0]
		}
	}
	if object != local || commit != state.Commit {
		return fmt.Errorf("remote annotated tag %s differs from the local tag object or selected commit %s", state.Version, state.Commit)
	}
	return nil
}

type workflowRun struct {
	ID         int64                                       `json:"databaseId"`
	Commit     string                                      `json:"headSha"`
	Branch     string                                      `json:"headBranch"`
	Status     string                                      `json:"status"`
	Conclusion string                                      `json:"conclusion"`
	URL        string                                      `json:"url"`
	Jobs       []struct{ Name, Status, Conclusion string } `json:"jobs"`
}

func (r *RealAdapter) runs(ctx context.Context, state State, workflow, branch string) ([]workflowRun, error) {
	raw, err := r.gh(ctx, state, "run", "list", "--workflow", workflow, "--branch", branch, "--commit", state.Commit, "--event", "push", "--limit", "100", "--json", "databaseId,headSha,headBranch,status,conclusion,url")
	if err != nil {
		return nil, err
	}
	var runs []workflowRun
	if err = json.Unmarshal([]byte(raw), &runs); err != nil {
		return nil, err
	}
	return runs, nil
}
func pending(q Request, message string, limit int) (Result, error) {
	if q.Attempt >= limit {
		return Result{State: q.State}, fmt.Errorf("%s; timed out after %d polls. Inspect GitHub and retry or resume; do not move the tag", message, limit)
	}
	return Result{State: q.State, Message: message, Pending: true}, nil
}
func (r *RealAdapter) waitCI(ctx context.Context, q Request) (Result, error) {
	runs, err := r.runs(ctx, q.State, "ci.yml", "main")
	if err != nil {
		return Result{}, err
	}
	var selected workflowRun
	for _, run := range runs {
		if run.Commit == q.State.Commit && run.Branch == "main" && run.ID > selected.ID {
			selected = run
		}
	}
	if selected.ID == 0 {
		return pending(q, "Waiting for CI for the exact selected main commit", 40)
	}
	if selected.Status != "completed" {
		return pending(q, "CI "+selected.Status+": "+selected.URL, 80)
	}
	if selected.Conclusion != "success" {
		return Result{}, fmt.Errorf("selected commit's CI finished with %s: %s", selected.Conclusion, selected.URL)
	}
	return Result{State: q.State, Message: "CI passed for " + q.State.Commit}, nil
}
func (r *RealAdapter) discover(ctx context.Context, q Request) (Result, error) {
	runs, err := r.runs(ctx, q.State, "release.yml", q.State.Version)
	if err != nil {
		return Result{}, err
	}
	var selected workflowRun
	for _, run := range runs {
		if run.Commit == q.State.Commit && run.Branch == q.State.Version && run.ID > selected.ID {
			selected = run
		}
	}
	if selected.ID == 0 {
		return pending(q, "Waiting for GitHub to start the matching release workflow", 40)
	}
	state := q.State
	state.RunID = selected.ID
	return Result{State: state, Message: "Matched release workflow: " + selected.URL}, nil
}
func (r *RealAdapter) waitJobs(ctx context.Context, q Request, emit func(string)) (Result, error) {
	if q.State.RunID == 0 {
		return Result{}, fmt.Errorf("no matching workflow was selected")
	}
	raw, err := r.gh(ctx, q.State, "run", "view", strconv.FormatInt(q.State.RunID, 10), "--json", "databaseId,headSha,headBranch,status,conclusion,url,jobs")
	if err != nil {
		return Result{}, err
	}
	var run workflowRun
	if err = json.Unmarshal([]byte(raw), &run); err != nil {
		return Result{}, err
	}
	if run.ID != q.State.RunID || run.Commit != q.State.Commit || run.Branch != q.State.Version {
		return Result{}, fmt.Errorf("workflow source changed; refusing unrelated run")
	}
	for _, job := range run.Jobs {
		emit(job.Name + ": " + job.Status + " " + job.Conclusion)
	}
	if run.Status != "completed" {
		return pending(q, "Release workflow "+run.Status+": "+run.URL, 120)
	}
	if run.Conclusion != "success" {
		if r.Resume && q.State.TagIntent {
			release, err := r.requireRelease(ctx, q.State)
			if err != nil {
				return Result{}, fmt.Errorf("release workflow %s; existing release cannot be reconciled: %w", run.Conclusion, err)
			}
			state := q.State
			state.URL = release.URL
			return Result{State: state, Message: "Workflow rerun failed; matching release found. Continuing with full attachment verification: " + release.URL}, nil
		}
		return Result{}, fmt.Errorf("release workflow %s: %s; rerun failed jobs for unchanged source or use a new version for source fixes", run.Conclusion, run.URL)
	}
	return Result{State: q.State, Message: "Release workflow succeeded; inspect draft next"}, nil
}

func (r *RealAdapter) requireRelease(ctx context.Context, state State) (*githubRelease, error) {
	if err := r.checkRemoteTag(ctx, state, false); err != nil {
		return nil, err
	}
	releases, err := r.releases(ctx, state)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		if release.Tag != state.Version {
			continue
		}
		if !release.Draft && !state.PublishIntent {
			return nil, fmt.Errorf("release is already public; published assets and tags must remain unchanged")
		}
		if release.Name != state.Version {
			return nil, fmt.Errorf("release title %q differs from expected version %s", release.Name, state.Version)
		}
		if release.Prerelease {
			return nil, fmt.Errorf("expected a stable release, found prerelease")
		}
		if len(release.Assets) != 2 {
			return nil, fmt.Errorf("release must contain only the expected archive and checksum; found %d attachments", len(release.Assets))
		}
		for _, name := range []string{archiveName(state.Version), archiveName(state.Version) + ".sha256"} {
			limit := int64(256 << 20)
			if strings.HasSuffix(name, ".sha256") {
				limit = 4 << 10
			}
			count := 0
			for _, asset := range release.Assets {
				if asset.Name == name && asset.Size > 0 {
					if asset.Size > limit {
						return nil, fmt.Errorf("release attachment %s exceeds the %d-byte download limit", name, limit)
					}
					count++
				}
			}
			if count != 1 {
				return nil, fmt.Errorf("release must have exactly one nonempty attachment named %s", name)
			}
		}
		return &release, nil
	}
	return nil, fmt.Errorf("release draft is not available yet; inspect the workflow and retry")
}
func (r *RealAdapter) download(ctx context.Context, state State) (State, error) {
	release, err := r.requireRelease(ctx, state)
	if err != nil {
		return state, err
	}
	state.URL = release.URL
	state.DownloadIdentity = assetIdentity(release)
	state.AssetIdentity = ""
	state.DownloadDir, err = r.freshDirectory("mapbox-release-download-")
	if err != nil {
		return state, err
	}
	_, err = r.gh(ctx, state, "release", "download", state.Version, "--dir", state.DownloadDir, "--pattern", archiveName(state.Version), "--pattern", archiveName(state.Version)+".sha256")
	return state, err
}
func (r *RealAdapter) publish(ctx context.Context, q Request) (Result, error) {
	state := q.State
	if q.Operation == PublishRelease {
		state.PublishIntent = true
	}
	result := Result{State: state}
	release, err := r.requireRelease(ctx, state)
	if err != nil {
		return result, err
	}
	if state.AssetIdentity == "" || state.AssetIdentity != assetIdentity(release) {
		return result, fmt.Errorf("release attachments changed or were not verified; restart with --resume to download and review them again")
	}
	policy, err := r.policy(ctx, state)
	if err != nil {
		return result, err
	}
	if release.Draft && policy.Latest != "" && compareVersions(policy.Latest, state.Version) > 0 {
		return result, fmt.Errorf("a newer version %s is already published; refusing to replace latest", policy.Latest)
	}
	if strings.TrimSpace(state.Notes) == "" {
		return result, fmt.Errorf("release notes must not be empty")
	}
	if release.Draft && q.Operation == PublishRelease {
		return result, fmt.Errorf("release is still a draft; open %s and click Publish release, then retry", release.URL)
	}
	if q.Operation == PreparePublication && release.Draft && release.Body != state.Notes {
		state.PublishIntent = true
		if err = r.Journal.Save(state); err != nil {
			return result, err
		}
		result.State = state
		dir, err := r.freshDirectory("mapbox-release-notes-")
		if err != nil {
			return result, err
		}
		path := filepath.Join(dir, "notes.md")
		if err = os.WriteFile(path, []byte(state.Notes), 0o600); err != nil {
			return result, err
		}
		// Save reviewed notes without publishing. The maintainer publishes in the web UI.
		if _, err = r.api(ctx, state, "/releases/"+strconv.FormatInt(release.ID, 10),
			"--method", "PATCH", "-F", "body=@"+path); err != nil {
			return result, err
		}
	}
	if q.Operation == PreparePublication {
		state.PublishIntent = true
		state.URL = release.URL
		if !release.Draft && release.Body != state.Notes {
			return result, fmt.Errorf("public release notes differ from the reviewed notes; inspect %s", release.URL)
		}
		return Result{State: state, Message: "Review and publish the release on GitHub: " + state.URL}, nil
	}
	releases, err := r.releases(ctx, state)
	if err != nil {
		return result, err
	}
	public := false
	for _, item := range releases {
		if item.Tag == state.Version && item.ID == release.ID && !item.Draft {
			if item.Prerelease {
				return result, fmt.Errorf("release is public but marked as a prerelease; inspect %s before continuing", item.URL)
			}
			if assetIdentity(&item) != state.AssetIdentity {
				return result, fmt.Errorf("release is public but its attachments changed during publication; inspect %s before continuing", item.URL)
			}
			if item.Name != state.Version {
				return result, fmt.Errorf("release is public but its title differs from the selected version; inspect %s before continuing", item.URL)
			}
			if item.Body != state.Notes {
				return result, fmt.Errorf("release is public but its notes differ from the reviewed notes; inspect %s before continuing", item.URL)
			}
			public = true
			state.URL = item.URL
		}
	}
	if !public {
		q.State = state
		return pending(q, "Waiting for GitHub to confirm publication", 10)
	}
	raw, err := r.api(ctx, state, "/releases/latest")
	if err != nil {
		return result, err
	}
	var latest githubRelease
	if err = json.Unmarshal([]byte(raw), &latest); err != nil {
		return result, err
	}
	expectedLatest := release
	for i := range releases {
		item := &releases[i]
		if !item.Draft && !item.Prerelease && stableVersion.MatchString(item.Tag) &&
			compareVersions(item.Tag, expectedLatest.Tag) > 0 {
			expectedLatest = item
		}
	}
	if latest.ID != expectedLatest.ID || latest.Tag != expectedLatest.Tag {
		q.State = state
		return pending(q, "Waiting for GitHub to confirm latest release", 10)
	}
	state.Published = true
	message := "Publication and latest status verified: " + state.URL
	if latest.Tag != state.Version {
		message += "; newer release " + latest.Tag + " remains latest"
	}
	return Result{State: state, Message: message}, nil
}

// GitHub asset IDs change when an attachment is replaced. Bind publication to
// the exact release and attachments that were downloaded and reviewed.
func assetIdentity(release *githubRelease) string {
	var assets []string
	for _, asset := range release.Assets {
		assets = append(assets, fmt.Sprintf("%d:%s:%d", asset.ID, asset.Name, asset.Size))
	}
	slices.Sort(assets)
	return fmt.Sprintf("%d:%x", release.ID, sha256.Sum256([]byte(strings.Join(assets, "\n"))))
}
