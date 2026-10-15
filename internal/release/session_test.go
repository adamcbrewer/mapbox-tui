package release

import (
	"strings"
	"testing"
)

func TestVersionValidation(t *testing.T) {
	for _, version := range []string{"v0.2.0", "v1.0.0", "0.2.0", "v01.2.0", "v1.2.3-rc1", "v1.2.3+build", "v1.2", "v1.2.3\n"} {
		t.Run(version, func(t *testing.T) {
			_, err := NewSession(version, "success")
			valid := version == "v0.2.0" || version == "v1.0.0"
			if (err == nil) != valid {
				t.Fatalf("version %q: %v", version, err)
			}
		})
	}
	if _, err := NewSession("v1.0.0", "unknown"); err == nil {
		t.Fatal("accepted unknown scenario")
	}
}

func TestWorkflowScenarios(t *testing.T) {
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			s, err := NewSession("v0.2.0", scenario)
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			confirmations, failures := 0, 0
			for range 40 {
				switch s.Status {
				case Running:
					s.Advance()
				case Confirmation:
					before := s.Step
					s.Advance()
					s.Confirm("no")
					if s.Step != before {
						t.Fatal("advanced without explicit confirmation")
					}
					s.Confirm("yes")
					confirmations++
				case Failed:
					failures++
					before := s.Step
					s.Advance()
					if s.Step != before {
						t.Fatal("failure advanced automatically")
					}
					s.Retry()
				}
				if s.Status == Done {
					break
				}
			}
			if s.Status != Done || confirmations != 4 {
				t.Fatalf("incomplete flow: %+v", s)
			}
			wantFailure := scenario == "failed-checks" || scenario == "auth-error" || scenario == "checksum-mismatch"
			if (failures == 1) != wantFailure {
				t.Fatalf("unexpected failures: %d", failures)
			}
			if scenario == "existing-draft" && !strings.Contains(strings.Join(s.Logs, "\n"), "without creating another draft") {
				t.Fatal("missing existing draft reconciliation")
			}
		})
	}
}

func TestCancellationStopsEveryStage(t *testing.T) {
	for step := Preflight; step < Complete; step++ {
		t.Run(step.String(), func(t *testing.T) {
			s, _ := NewSession("v0.2.0", "success")
			s.Step = step
			s.enter()
			s.Cancel()
			s.Start()
			s.Advance()
			s.Confirm("yes")
			s.Confirm("publish")
			s.Retry()
			if s.Status != Cancelled || s.Step != step {
				t.Fatalf("cancelled session advanced: %+v", s)
			}
		})
	}
}

func TestOperationsProgressIndividually(t *testing.T) {
	s, _ := NewSession("", "success")
	s.Start()
	if s.Activity[Preflight][0].Status != ActivityRunning {
		t.Fatal("no active operation on start")
	}
	s.Advance()
	entries := s.Activity[Preflight]
	if s.Step != Preflight || entries[0].Status != ActivityComplete || entries[1].Status != ActivityRunning {
		t.Fatalf("operations did not progress separately: %+v", entries)
	}
	s.Cancel()
	if s.Activity[Preflight][1].Status != ActivityCancelled {
		t.Fatal("cancelled leaf still running")
	}
}

func TestWaitingUpdatesOneLeaf(t *testing.T) {
	s, _ := NewSession("", "delayed-workflow")
	s.Step = Workflow
	s.enter()
	for range 4 {
		s.Advance()
	}
	entries := s.Activity[Workflow]
	if len(entries) != 1 || entries[0].Status != ActivityRunning || !strings.Contains(entries[0].Text, "attempt 4/5") {
		t.Fatalf("waiting should update the active leaf: %+v", entries)
	}
	s.Advance()
	if s.Activity[Workflow][0].Status != ActivityComplete || s.Activity[Workflow][1].Status != ActivityRunning {
		t.Fatal("workflow discovery did not hand off to job monitoring")
	}
}

func TestRetryKeepsCompletedLeaves(t *testing.T) {
	s, _ := NewSession("", "checksum-mismatch")
	s.Step = Download
	s.enter()
	s.Advance()
	s.Advance()
	if s.Activity[Download][0].Status != ActivityComplete || s.Activity[Download][1].Status != ActivityFailed {
		t.Fatal("download and failed verification not distinguished")
	}
	s.Retry()
	s.Advance()
	if s.Step != Viewer {
		t.Fatal("retry repeated the download instead of verification")
	}
}
