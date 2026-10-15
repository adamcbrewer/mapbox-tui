package release

import (
	"context"
	"fmt"
	"slices"
)

// FixtureAdapter cannot execute commands: it has no runner or network client.
type FixtureAdapter struct{ Scenario string }

func fixtureState(scenario string) State {
	policy := VersionPolicy{Latest: "v0.1.0", Tags: []string{"v0.1.0"}}
	if scenario == "existing-tag" {
		policy.Tags = append(policy.Tags, "v0.1.1")
	}
	return State{Mode: TestMode, Repository: "example/mapbox-tui", Host: "github.com", Commit: "0123456789ab", Policy: policy}
}

func (d FixtureAdapter) Execute(ctx context.Context, request Request, emit func(string)) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	state := request.State
	if request.Operation == Inspect {
		if !slices.Contains(scenarios, d.Scenario) {
			return Result{}, fmt.Errorf("unknown test scenario %q", d.Scenario)
		}
		return Result{State: fixtureState(d.Scenario)}, nil
	}
	result := Result{State: state, Message: fixtureResults[request.Operation]}
	if d.Scenario == "slow-checks" && request.Operation == Interactions && request.Attempt < 8 {
		result.Pending, result.Message = true, "Running terminal interactions; waiting for the process to finish"
		return result, nil
	}
	if d.Scenario == "delayed-workflow" && request.Operation == Discover && request.Attempt < 5 {
		result.Pending = true
		result.Message = fmt.Sprintf("Waiting for GitHub to start the matching workflow (attempt %d/5).", request.Attempt)
		return result, nil
	}
	if !request.Retry {
		switch {
		case d.Scenario == "auth-error" && request.Operation == Tools:
			return result, fmt.Errorf("authentication failed; run gh auth login, then retry")
		case d.Scenario == "failed-checks" && request.Operation == Interactions:
			return result, fmt.Errorf("terminal checks failed; inspect the output before retrying")
		case d.Scenario == "checksum-mismatch" && request.Operation == RemoteArtifact:
			return result, fmt.Errorf("checksum mismatch; do not execute the download")
		}
	}
	switch request.Operation {
	case PushTag:
		result.State.TagIntent = true
	case Discover:
		result.State.RunID = 42
	case ReadNotes:
		result.State.Notes = "Linux x86-64. Install: docs/INSTALL.md\n\nNo breaking changes. Known limitations: Linux only."
		emit(result.State.Notes)
	case PreparePublication:
		result.State.PublishIntent = true
		result.State.URL = "https://example.invalid/releases/" + state.Version
	case PublishRelease:
		result.State.Published = true
		result.State.URL = "https://example.invalid/releases/" + state.Version
	case FetchArtifact:
		if d.Scenario == "existing-draft" {
			result.Message = "Existing draft matches tag and commit; inspected without creating another draft"
		}
	}
	if result.Message == "" {
		return result, fmt.Errorf("unsupported test operation %q", request.Operation)
	}
	return result, nil
}

var fixtureResults = map[Operation]string{
	Tools:              "Toolchain and gh authentication ready",
	Source:             "Clean main selected: 0123456789ab",
	CI:                 "CI passed for the selected commit",
	Check:              "Formatting, tests, vet and race checks passed",
	Interactions:       "Terminal interactions passed with NO_COLOR unset",
	Build:              "Linux amd64 archive prepared",
	LocalArtifact:      "SHA-256 checksum matched",
	LocalSmoke:         "Embedded version and commit matched selection; offline rendering passed",
	PushTag:            "Annotated tag pushed; only the selected tag was sent",
	Discover:           "Matched release workflow by tag and commit",
	Jobs:               "Checks and packaging jobs succeeded; draft ready",
	FetchArtifact:      "Draft attachments downloaded to a fresh directory",
	RemoteArtifact:     "Checksum, embedded version and commit verified; offline rendering passed",
	OpenViewer:         "Offline viewer exited; confirm the interaction below",
	ReadNotes:          "Release notes loaded for review",
	PreparePublication: "Reviewed notes saved to the draft; publish it on GitHub",
	PublishRelease:     "Publication and latest release verified",
}
