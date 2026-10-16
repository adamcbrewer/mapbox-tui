// Package release implements the shared maintainer release workflow.
package release

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	"Push release tag", "Wait for release workflow", "Verify draft attachments",
	"Test offline viewer", "Review release notes", "Publish on GitHub",
}

func (s Step) String() string {
	if s == Complete {
		return "Test complete"
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

const Scenarios = "success, slow-checks, delayed-workflow, failed-checks, auth-error, checksum-mismatch, existing-draft, existing-tag, changed-public-notes"

var scenarios = []string{
	"success", "slow-checks", "delayed-workflow", "failed-checks", "auth-error", "checksum-mismatch", "existing-draft", "existing-tag", "changed-public-notes",
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
	Output []string
}

// Session owns transitions and confirmations, never subprocesses or network IO.
type Session struct {
	State
	Activity  [Complete + 1][]ActivityEntry
	Skipped   [Complete]bool
	Scenario  string
	Step      Step
	Status    Status
	Logs      []string
	Detail    string
	operation int
	ticks     int
	retried   bool
	approved  bool
}

func NewSession(version, scenario string) (Session, error) {
	if !slices.Contains(scenarios, scenario) {
		return Session{}, fmt.Errorf("unknown scenario %q; choose %s", scenario, Scenarios)
	}
	state := fixtureState(scenario)
	state.Version = version
	return NewSessionWithState(state, scenario)
}

func NewSessionWithState(state State, scenario string) (Session, error) {
	if state.Version == "" {
		version, err := state.Policy.Suggested()
		if err != nil {
			return Session{}, err
		}
		state.Version = version
	}
	if !state.TagIntent {
		if err := state.Policy.Validate(state.Version); err != nil {
			return Session{}, err
		}
	}
	return Session{State: state, Scenario: scenario, Status: Ready}, nil
}

func (s *Session) Start() {
	if s.Status != Ready {
		return
	}
	if !s.TagIntent {
		if err := s.Policy.Validate(s.Version); err != nil {
			s.Detail = err.Error()
			return
		}
	}
	s.enter()
}

func (s *Session) enter() {
	s.ticks = 0
	s.Status = Running
	if s.Step == Complete {
		s.Status = Done
		s.Detail = s.Summary()
		s.log(s.Detail)
		return
	}
	if (s.Step == Tag || (s.Step == Publish && s.operation == 1)) && !s.approved {
		s.Status = Confirmation
		s.Detail = s.Prompt()
		s.log(s.Detail)
		return
	}
	s.Detail = operations[s.Step][s.operation].waiting
	s.Activity[s.Step] = append(s.Activity[s.Step], ActivityEntry{Text: s.Detail, Status: ActivityRunning})
}

func (s Session) Prompt() string {
	suffix := ""
	if s.Mode == TestMode {
		suffix = " (simulated)"
	}
	switch s.Step {
	case Tag:
		return fmt.Sprintf("Would you like to push tag %s now?%s\n%s @ %s", s.Version, suffix, s.Repository, s.Commit)
	case Viewer:
		return "Did the offline viewer open and quit correctly?" + suffix
	case Notes:
		if s.NotesPublic {
			return "Release is already public. Accept the published notes shown above?\n" +
				"Edit on GitHub, then press r to refresh: " + s.URL
		}
		return "Review notes: check installation instructions, breaking changes and limitations. Press e to edit." + suffix
	case Publish:
		if s.NotesPublic {
			return "Verify the published release with these accepted notes?\n" + s.URL
		}
		return fmt.Sprintf("Have you published %s on GitHub?%s\nOpen the draft, review it, then click Publish release:\n%s", s.Version, suffix, s.URL)
	}
	return ""
}

func (s *Session) Confirm(answer string) {
	answer = strings.ToLower(strings.TrimSpace(answer))
	if s.Status != Confirmation || (answer != "y" && answer != "yes") {
		return
	}
	if s.Step == Notes && strings.TrimSpace(s.Notes) == "" {
		s.Detail = s.emptyNotesMessage()
		return
	}
	if s.Step == Tag && !s.TagIntent {
		if err := s.Policy.Validate(s.Version); err != nil {
			s.Status, s.Detail = Failed, err.Error()
			s.log(s.Detail)
			return
		}
	}
	s.log("Confirmed: " + s.Step.String())
	if s.Step == Tag || s.Step == Publish {
		s.approved = true
		s.enter()
		return
	}
	s.next()
}

func (s *Session) Request() Request {
	s.ticks++
	return Request{State: s.State, Operation: operations[s.Step][s.operation].id, Attempt: s.ticks, Retry: s.retried}
}

func (s *Session) Accept(result Result, err error) {
	if s.Status != Running {
		return
	}
	if err != nil {
		if result.State.Version == s.Version {
			s.State = result.State
		}
		s.updateOperation(err.Error(), ActivityFailed)
		s.Logs = append(s.Logs, err.Error())
		if _, changed := errors.AsType[*publicNotesChangedError](err); changed && s.Step == Publish {
			s.reloadNotes()
			return
		}
		s.Status = Failed
		return
	}
	s.State = result.State
	if result.Pending {
		s.updateOperation(result.Message, ActivityRunning)
		return
	}
	s.updateOperation(result.Message, ActivityComplete)
	s.Logs = append(s.Logs, result.Message)
	s.operation++
	if s.operation < len(operations[s.Step]) {
		s.enter()
		return
	}
	if s.Step == Viewer || s.Step == Notes {
		s.Status = Confirmation
		s.Detail = s.Prompt()
		return
	}
	s.next()
}

// Advance drives the same adapter/result protocol synchronously for deterministic tests.
func (s *Session) Advance() {
	if s.Status != Running {
		return
	}
	result, err := (FixtureAdapter{Scenario: s.Scenario}).Execute(context.Background(), s.Request(), func(string) {})
	s.Accept(result, err)
}

func (s *Session) next() {
	if s.Mode == Rehearsal && s.Step == Package {
		for _, step := range []Step{Tag, Workflow, Download, Notes, Publish} {
			s.Skipped[step] = true
		}
		s.Step = Viewer
	} else if s.Mode == Rehearsal && s.Step == Viewer {
		s.Step = Complete
	} else {
		s.Step++
	}
	s.operation, s.approved = 0, false
	s.enter()
}

func (s *Session) Retry() {
	if s.Status != Failed {
		return
	}
	s.retried = true
	s.log("Retrying this operation; existing release state will be reconciled.")
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
	s.Detail = s.Summary()
	s.log(s.Detail)
}

func (s Session) Summary() string {
	if s.Mode == TestMode {
		if s.Status == Done {
			return "Test complete. No real release was created."
		}
		return "Test cancelled. No commands ran; no remote workflow is running."
	}
	if s.Status == Done {
		if s.Mode == Rehearsal {
			return "Rehearsal complete. Local artifacts verified; tag, workflow, draft and publication stages were not exercised."
		}
		return "Publication verified: " + s.URL
	}
	return "Stopped locally. A pushed tag may still have a remote workflow running. Resume with --resume to reconcile git and GitHub."
}

func (s *Session) updateOperation(text string, status ActivityStatus) {
	entries := s.Activity[s.Step]
	entries[len(entries)-1].Text = text
	entries[len(entries)-1].Status = status
	s.Detail = text
}

func (s *Session) log(message string) {
	s.Logs = append(s.Logs, message)
	s.Activity[s.Step] = append(s.Activity[s.Step], ActivityEntry{Text: message, Status: ActivityNote})
}

func (s *Session) reloadNotes() {
	s.Step, s.operation, s.approved = Notes, 0, false
	s.enter()
}

func (s Session) emptyNotesMessage() string {
	if s.NotesPublic {
		return "Release notes cannot be empty. Edit on GitHub, then press r to refresh."
	}
	return "Release notes cannot be empty. Press e to edit."
}
