package release

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
)

// VersionPolicy uses a release baseline and a complete snapshot of reserved tags.
// A real adapter must reconcile local and remote tags before supplying this data.
type VersionPolicy struct {
	Latest string
	Tags   []string
}

func (p VersionPolicy) Candidates() ([]string, error) {
	if !stableVersion.MatchString(p.Latest) {
		return nil, fmt.Errorf("invalid latest stable release %q", p.Latest)
	}
	parts := strings.Split(strings.TrimPrefix(p.Latest, "v"), ".")
	increment := func(value string) string {
		n, _ := new(big.Int).SetString(value, 10)
		return n.Add(n, big.NewInt(1)).String()
	}
	return []string{
		"v" + parts[0] + "." + parts[1] + "." + increment(parts[2]),
		"v" + parts[0] + "." + increment(parts[1]) + ".0",
		"v" + increment(parts[0]) + ".0.0",
	}, nil
}

func (p VersionPolicy) Suggested() (string, error) {
	candidates, err := p.Candidates()
	if err != nil {
		return "", err
	}
	for _, version := range candidates {
		if !slices.Contains(p.Tags, version) {
			return version, nil
		}
	}
	return "", fmt.Errorf("all next versions already have tags; reconcile release history before continuing")
}

func (p VersionPolicy) Validate(version string) error {
	if !stableVersion.MatchString(version) {
		return fmt.Errorf("use stable vMAJOR.MINOR.PATCH, for example v0.1.1")
	}
	if slices.Contains(p.Tags, version) {
		return fmt.Errorf("tag %s already exists; existing tags cannot be overwritten", version)
	}
	candidates, err := p.Candidates()
	if err != nil {
		return err
	}
	if !slices.Contains(candidates, version) {
		return fmt.Errorf("choose one increment after %s: %s", p.Latest, strings.Join(candidates, ", "))
	}
	return nil
}

func (s *Session) SelectVersion(version string) error {
	if s.Status != Ready {
		return fmt.Errorf("version is locked once preparation starts")
	}
	if err := s.Policy.Validate(version); err != nil {
		return err
	}
	s.Version = version
	return nil
}
