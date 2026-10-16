package release

import (
	"context"
	"fmt"
)

type Mode string

const (
	TestMode  Mode = "test"
	Rehearsal Mode = "rehearsal"
	Live      Mode = "release"
)

type Operation string

const (
	Inspect            Operation = "inspect"
	Tools              Operation = "tools"
	Source             Operation = "source"
	CI                 Operation = "ci"
	Check              Operation = "check"
	Interactions       Operation = "interactions"
	Build              Operation = "build"
	LocalArtifact      Operation = "local-artifact"
	LocalSmoke         Operation = "local-smoke"
	PushTag            Operation = "push-tag"
	Discover           Operation = "discover"
	Jobs               Operation = "jobs"
	FetchArtifact      Operation = "fetch-artifact"
	RemoteArtifact     Operation = "remote-artifact"
	OpenViewer         Operation = "open-viewer"
	ReadNotes          Operation = "read-notes"
	PreparePublication Operation = "prepare-publication"
	PublishRelease     Operation = "publish"
)

type operationSpec struct {
	id      Operation
	waiting string
}

// This is the sole execution plan for interactive tests, rehearsal and real releases.
var operations = map[Step][]operationSpec{
	Preflight: {
		{id: Tools, waiting: "Checking toolchain and GitHub authentication"},
		{id: Source, waiting: "Checking selected source and release history"},
		{id: CI, waiting: "Waiting for CI on the selected commit"},
	},
	Checks: {
		{id: Check, waiting: "Running formatting, tests, vet and race checks"},
		{id: Interactions, waiting: "Running terminal interactions with NO_COLOR unset"},
	},
	Package: {
		{id: Build, waiting: "Building the Linux amd64 archive"},
		{id: LocalArtifact, waiting: "Verifying local archive checksum and contents"},
		{id: LocalSmoke, waiting: "Checking packaged version, commit and offline rendering"},
	},
	Tag: {
		{id: PushTag, waiting: "Rechecking source, creating and pushing the annotated tag"},
	},
	Workflow: {
		{id: Discover, waiting: "Waiting for GitHub to start the matching workflow"},
		{id: Jobs, waiting: "Waiting for GitHub checks, packaging and draft creation"},
	},
	Download: {
		{id: FetchArtifact, waiting: "Inspecting the draft and downloading attachments"},
		{id: RemoteArtifact, waiting: "Verifying downloaded checksum, version and offline rendering"},
	},
	Viewer: {
		{id: OpenViewer, waiting: "Opening the verified binary offline; quit the viewer to return"},
	},
	Notes: {
		{id: ReadNotes, waiting: "Loading release notes for review"},
	},
	Publish: {
		{id: PreparePublication, waiting: "Saving reviewed notes to the GitHub draft"},
		{id: PublishRelease, waiting: "Verifying publication on GitHub"},
	},
}

// State is passed by value between the UI and adapters. Credentials are never stored.
type State struct {
	Mode                Mode
	Root                string
	Repository          string
	Host                string
	Commit              string
	Version             string
	Policy              VersionPolicy
	RunID               int64
	URL                 string
	LocalDir            string
	LocalArtifactDigest string
	DownloadDir         string
	AssetIdentity       string
	DownloadIdentity    string
	Binary              string
	Notes               string
	NotesPublic         bool
	TagIntent           bool
	PublishIntent       bool
	Published           bool
}

type Request struct {
	State     State
	Operation Operation
	Attempt   int
	Retry     bool
}

type Result struct {
	State   State
	Message string
	Pending bool
}

// Adapter performs a single operation. Pending results leave the current leaf
// running; the UI schedules the next bounded poll without blocking input.
type Adapter interface {
	Execute(context.Context, Request, func(string)) (Result, error)
}

// A public notes change can be accepted by reviewing the current GitHub text.
// It never authorizes skipping the other publication checks.
type publicNotesChangedError struct {
	URL string
}

func (e *publicNotesChangedError) Error() string {
	return fmt.Sprintf("release is public but its notes differ from the reviewed notes; review the current notes at %s", e.URL)
}
