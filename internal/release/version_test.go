package release

import (
	"slices"
	"testing"
)

func TestVersionPolicy(t *testing.T) {
	policy := VersionPolicy{Latest: "v1.2.3", Tags: []string{"v1.2.3", "v1.3.0"}}
	for _, tc := range []struct {
		name, version string
		valid         bool
	}{
		{name: "patch", version: "v1.2.4", valid: true},
		{name: "major", version: "v2.0.0", valid: true},
		{name: "reserved minor", version: "v1.3.0"},
		{name: "existing baseline", version: "v1.2.3"},
		{name: "skip patch", version: "v1.2.5"},
		{name: "skip minor", version: "v1.4.0"},
		{name: "skip major", version: "v3.0.0"},
		{name: "minor must reset patch", version: "v1.3.1"},
		{name: "major must reset minor", version: "v2.1.0"},
		{name: "downgrade", version: "v1.1.9"},
		{name: "prerelease", version: "v1.2.4-rc1"},
		{name: "leading zero", version: "v1.02.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := policy.Validate(tc.version)
			if (err == nil) != tc.valid {
				t.Fatalf("Validate(%q) = %v", tc.version, err)
			}
		})
	}
	policy.Tags = nil
	if err := policy.Validate("v1.3.0"); err != nil {
		t.Fatal(err)
	}
}

func TestSuggestedVersion(t *testing.T) {
	p := VersionPolicy{Latest: "v1.2.3"}
	for _, want := range []string{"v1.2.4", "v1.3.0", "v2.0.0"} {
		got, err := p.Suggested()
		if err != nil || got != want {
			t.Fatalf("suggestion %q, %v; want %q", got, err, want)
		}
		p.Tags = append(p.Tags, got)
	}
	if _, err := p.Suggested(); err == nil {
		t.Fatal("all candidates reserved but suggested a tag")
	}
	p.Latest = "not-a-version"
	if _, err := p.Suggested(); err == nil {
		t.Fatal("accepted invalid baseline")
	}
}

func TestVersionIncrementDoesNotOverflow(t *testing.T) {
	p := VersionPolicy{Latest: "v18446744073709551615.0.0"}
	got, err := p.Candidates()
	if err != nil || !slices.Contains(got, "v18446744073709551616.0.0") {
		t.Fatalf("increment overflow: %v, %v", got, err)
	}
}

func TestVersionSelectionAndTagRecheck(t *testing.T) {
	s, err := NewSession("", "success")
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != "v0.1.1" {
		t.Fatalf("unexpected suggestion: %s", s.Version)
	}
	if err := s.SelectVersion("v0.3.0"); err == nil {
		t.Fatal("accepted skipped version")
	}
	if s.Version != "v0.1.1" {
		t.Fatal("invalid selection changed version")
	}
	if err := s.SelectVersion("v0.2.0"); err != nil {
		t.Fatal(err)
	}
	s.Start()
	if err := s.SelectVersion("v1.0.0"); err == nil {
		t.Fatal("changed version after start")
	}
	for s.Step != Tag {
		s.Advance()
	}
	s.Policy.Tags = append(s.Policy.Tags, s.Version)
	s.Confirm("yes")
	if s.Status != Failed || s.Step != Tag {
		t.Fatal("tag collision did not block confirmation")
	}
	s.Retry()
	s.Confirm("yes")
	if s.Status != Failed || s.Step != Tag {
		t.Fatal("retry bypassed tag collision")
	}
}
