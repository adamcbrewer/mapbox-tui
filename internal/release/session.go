// Package release contains the maintainer release demo. It has no command or
// GitHub adapter: all results are deterministic, in-memory fixtures.
package release

import (
	"fmt"
	"slices"
)

type Step int

const (
	Preflight Step = iota
	Checks
	Package
	Tag
	Workflow
	Download
	Viewer
	Notes
	Publish
	Complete
)

var stepNames = []string{
	"Preflight & exact-commit CI", "Checks & terminal interactions", "Package & verify locally",
	"Confirm tag push", "Wait for release workflow", "Verify draft attachments",
	"Confirm offline viewer", "Review release notes", "Confirm publication",
}

func (s Step) String() string {
	if s == Complete {
		return "Demo complete"
	}
	return stepNames[s]
}

type Status int

const (
	Ready Status = iota
	Running
	Confirmation
	Failed
	Done
	Cancelled
)

const Scenarios = "success, slow-checks, delayed-workflow, failed-checks, auth-error, checksum-mismatch, existing-draft, existing-tag"

var scenarios = []string{
	"success", "slow-checks", "delayed-workflow", "failed-checks", "auth-error", "checksum-mismatch", "existing-draft", "existing-tag",
}

type ActivityStatus int

const (
	ActivityNote ActivityStatus = iota
	ActivityRunning
	ActivityComplete
	ActivityFailed
	ActivityCancelled
)

type ActivityEntry struct {
	Text   string
	Status ActivityStatus
}

type demoOperation struct{ waiting, result string }

// Session is independent of terminal rendering, wall-clock time and external IO.
// Advance represents one simulated unit of work, never an actual release action.
type Session struct {
	Policy    VersionPolicy
	Activity  [Complete + 1][]ActivityEntry
	Version   string
	Scenario  string
	Step      Step
	Status    Status
	Logs      []string
	Detail    string
	operation int
	ticks     int
	retried   bool
}

func NewSession(version, scenario string) (Session, error) {
	if !slices.Contains(scenarios, scenario) {
		return Session{}, fmt.Errorf("unknown scenario %q; choose %s", scenario, Scenarios)
	}
	policy := VersionPolicy{Latest: "v0.1.0", Tags: []string{"v0.1.0"}}
	if scenario == "existing-tag" {
		policy.Tags = append(policy.Tags, "v0.1.1")
	}
	if version == "" {
		var err error
		version, err = policy.Suggested()
		if err != nil {
			return Session{}, err
		}
	}
	if err := policy.Validate(version); err != nil {
		return Session{}, err
	}
	return Session{Version: version, Policy: policy, Scenario: scenario, Status: Ready}, nil
}

func (s *Session) Start() {
	if s.Status != Ready {
		return
	}
	if err := s.Policy.Validate(s.Version); err != nil {
		s.Detail = err.Error()
		return
	}
	s.enter()
}

func (s *Session) enter() {
	s.ticks = 0
	s.Status = Running
	s.Detail = "Simulating " + s.Step.String()
	switch s.Step {
	case Tag, Viewer, Notes, Publish:
		s.Status = Confirmation
		s.Detail = s.Prompt()
	case Complete:
		s.Status = Done
		s.Detail = "Simulated publication verified. No real release was created."
	}
	if s.Status == Running {
		s.startOperation()
		return
	}
	s.log(s.Detail)
}

func (s *Session) startOperation() {
	s.ticks = 0
	s.Detail = demoOperations[s.Step][s.operation].waiting
	s.Activity[s.Step] = append(s.Activity[s.Step], ActivityEntry{Text: s.Detail, Status: ActivityRunning})
	s.Logs = append(s.Logs, s.Detail)
}

func (s *Session) updateOperation(text string, status ActivityStatus) {
	entries := s.Activity[s.Step]
	entries[len(entries)-1] = ActivityEntry{Text: text, Status: status}
	s.Detail = text
}

func (s Session) Prompt() string {
	switch s.Step {
	case Tag:
		return "Push annotated tag " + s.Version + " for example/mapbox-tui at 0123456789ab? (simulated)"
	case Viewer:
		return "Offline viewer smoke test (simulated): confirm it started and quit correctly."
	case Notes:
		return "Review notes: Linux x86-64; docs/INSTALL.md link; breaking changes; limitations. Demo notes only."
	case Publish:
		return "Publish " + s.Version + " as latest? Type yes to confirm simulated publication."
	}
	return ""
}

func (s *Session) Confirm(answer string) {
	if s.Status != Confirmation {
		return
	}
	if answer != "yes" {
		return
	}
	if s.Step == Tag {
		if err := s.Policy.Validate(s.Version); err != nil {
			s.Status, s.Detail = Failed, err.Error()
			s.log(s.Detail)
			return
		}
	}
	s.log("Confirmed: " + s.Step.String())
	s.next()
}

func (s *Session) Advance() {
	if s.Status != Running {
		return
	}
	s.ticks++
	if s.Scenario == "slow-checks" && s.Step == Checks && s.operation == 1 && s.ticks < 8 {
		s.updateOperation("Running terminal interactions; waiting for the process to finish", ActivityRunning)
		return
	}
	if s.Scenario == "delayed-workflow" && s.Step == Workflow && s.operation == 0 && s.ticks < 5 {
		s.Detail = fmt.Sprintf("Waiting for GitHub to start the matching workflow (attempt %d/5).", s.ticks)
		s.updateOperation(s.Detail, ActivityRunning)
		return
	}
	if !s.retried {
		failure := ""
		switch {
		case s.Scenario == "auth-error" && s.Step == Preflight:
			failure = "Authentication failed. In a real release, run gh auth login, then retry."
		case s.Scenario == "failed-checks" && s.Step == Checks && s.operation == 1:
			failure = "Terminal checks failed. Inspect logs; source fixes require a new release preparation."
		case s.Scenario == "checksum-mismatch" && s.Step == Download && s.operation == 1:
			failure = "Checksum mismatch. Stop before executing the download; inspect the draft assets."
		}
		if failure != "" {
			s.Status, s.Detail = Failed, failure
			s.updateOperation(failure, ActivityFailed)
			s.Logs = append(s.Logs, failure)
			return
		}
	}
	result := demoOperations[s.Step][s.operation].result
	s.updateOperation(result, ActivityComplete)
	s.Logs = append(s.Logs, result)
	s.operation++
	if s.operation < len(demoOperations[s.Step]) {
		s.startOperation()
		return
	}

	detail := "Simulated success: " + s.Step.String()
	if s.Scenario == "existing-draft" && s.Step == Download {
		detail = "Existing draft matches tag and commit; inspect its assets without creating another draft. (simulated)"
	}
	s.log(detail)
	s.next()
}

func (s *Session) next() { s.Step++; s.operation = 0; s.enter() }

// Retry models a repaired fixture, not permission to retry real release mutations.
func (s *Session) Retry() {
	if s.Status != Failed {
		return
	}
	s.retried = true
	s.log("Demo: simulating an external repair, then retrying this step.")
	s.enter()
}

func (s *Session) Cancel() {
	if s.Status == Done || s.Status == Cancelled {
		return
	}
	if s.Status == Running {
		s.updateOperation(s.Detail, ActivityCancelled)
	}
	s.Status = Cancelled
	s.Detail = "Demo cancelled. No commands ran; no remote workflow is running."
	s.log(s.Detail)
}

func (s *Session) log(message string) {
	s.Logs = append(s.Logs, message)
	s.Activity[s.Step] = append(s.Activity[s.Step], ActivityEntry{Text: message, Status: ActivityNote})
}

// These messages describe fixture results, never observations from the machine.
var demoOperations = map[Step][]demoOperation{
	Preflight: {
		{waiting: "Checking toolchain and waiting for gh authentication", result: "Toolchain and gh authentication ready"},
		{waiting: "Inspecting the working tree and selecting main", result: "Clean main selected: 0123456789ab"},
		{waiting: "Waiting for GitHub CI status for 0123456789ab", result: "CI passed for the selected commit"},
	},
	Checks: {
		{waiting: "Running formatting, tests, vet and race checks", result: "Formatting, tests, vet and race checks passed"},
		{waiting: "Running terminal interactions with NO_COLOR unset", result: "Terminal interactions passed with NO_COLOR unset"},
	},
	Package: {
		{waiting: "Building the Linux amd64 archive", result: "Linux amd64 archive prepared"},
		{waiting: "Verifying the local archive checksum", result: "SHA-256 checksum matched"},
		{waiting: "Checking the packaged version and commit", result: "Embedded version and commit matched selection"},
	},
	Workflow: {
		{waiting: "Waiting for GitHub to start the matching workflow", result: "Matched release workflow by tag and commit"},
		{waiting: "Waiting for GitHub checks, packaging and draft creation", result: "Checks and packaging jobs succeeded; draft ready"},
	},
	Download: {
		{waiting: "Downloading draft attachments from GitHub", result: "Draft attachments downloaded to a fresh directory"},
		{waiting: "Verifying downloaded checksum, version and commit", result: "Checksum, embedded version and commit verified"},
	},
}
