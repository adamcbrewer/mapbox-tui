package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type renderingCredential struct {
	token      string
	source     string
	id         string
	guidance   string
	dependency mapboxDependencyStatus
}

type mapboxIdentity struct {
	Account   string `json:"account"`
	ExpiresAt *int64 `json:"expires_at"`
}

type authSection struct {
	State    string `json:"state"`
	Account  string `json:"account,omitempty"`
	Guidance string `json:"guidance,omitempty"`
}

type renderingStatus struct {
	Source   string      `json:"source"`
	Present  bool        `json:"present"`
	Account  string      `json:"account,omitempty"`
	TokenID  string      `json:"token_id,omitempty"`
	Validity string      `json:"validity"`
	Streets  authSection `json:"streets"`
	Guidance string      `json:"guidance,omitempty"`
}

type usagePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type usageReport struct {
	State       string           `json:"state"`
	Account     string           `json:"account,omitempty"`
	TokenID     string           `json:"token_id,omitempty"`
	Period      *usagePeriod     `json:"period_utc,omitempty"`
	GeneratedAt *time.Time       `json:"generated_at,omitempty"`
	Requests    int64            `json:"vector_tile_requests"`
	Daily       map[string]int64 `json:"daily,omitempty"`
	Guidance    string           `json:"guidance,omitempty"`
}

type authUsage struct {
	Account usageReport `json:"account"`
	Token   usageReport `json:"active_token"`
	Note    string      `json:"note"`
}

type authStatus struct {
	Profile        string                 `json:"profile"`
	CredentialFile string                 `json:"credential_file"`
	CLI            mapboxDependencyStatus `json:"cli"`
	CLIVersion     string                 `json:"cli_version,omitempty"`
	CLITelemetry   bool                   `json:"cli_telemetry"`
	Login          authSection            `json:"login"`
	Rendering      renderingStatus        `json:"rendering"`
	Usage          *authUsage             `json:"usage,omitempty"`
}

func mapboxCredentialFile() (string, error) {
	directory := os.Getenv("MAPBOX_CONFIG_DIR")
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot locate the Mapbox credential store: %w", err)
		}
		directory = filepath.Join(home, ".mapbox")
	}
	path, err := filepath.Abs(filepath.Join(directory, "credentials.json"))
	if err != nil {
		return "", fmt.Errorf("cannot locate MAPBOX_CONFIG_DIR: %w", err)
	}
	return path, nil
}

func environmentCredential() (renderingCredential, bool) {
	token, exists := os.LookupEnv("MAPBOX_ACCESS_TOKEN")
	if !exists {
		return renderingCredential{}, false
	}
	c := renderingCredential{token: token, source: "environment"}
	if token == "" {
		c.source = "disabled"
		c.guidance = "Mapbox token missing: MAPBOX_ACCESS_TOKEN is empty; automatic credentials disabled; " + mapboxLoginGuidance
	}
	return c, true
}

func (c *mapboxCLI) publicCredential(ctx context.Context) (renderingCredential, error) {
	var tokens []struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	if err := c.query(ctx, []string{"accounts", "list-tokens", "--default", "--usage", "pk"}, nil, &tokens); err != nil {
		return renderingCredential{}, err
	}
	for _, token := range tokens {
		if strings.HasPrefix(token.Token, "pk.") && len(token.Token) > 3 {
			return renderingCredential{token: token.Token, source: "login", id: safeMapboxID(token.ID)}, nil
		}
	}
	return renderingCredential{}, fmt.Errorf("Mapbox login has no available default public token; check the account's tokens")
}

func resolveCredential(ctx context.Context) renderingCredential {
	if credential, explicit := environmentCredential(); explicit {
		return credential
	}
	cli, dependency := checkMapboxCLI(ctx)
	if cli == nil {
		return renderingCredential{source: "none", guidance: dependency.Guidance, dependency: dependency}
	}
	credential, err := cli.publicCredential(ctx)
	if err != nil {
		return renderingCredential{source: "none", guidance: err.Error(), dependency: dependency}
	}
	credential.dependency = dependency
	return credential
}

func providerForCredential(kind string, credential renderingCredential) *Provider {
	if kind == "" {
		kind = defaultProvider(credential.token)
	}
	p := newProvider(kind)
	p.token, p.guidance = credential.token, credential.guidance
	return p
}

func (c *mapboxCLI) tokenMetadata(ctx context.Context, token string, rendering *renderingStatus) {
	var result struct {
		Code  string `json:"code"`
		Token struct {
			User          string `json:"user"`
			Authorization string `json:"authorization"`
		} `json:"token"`
	}
	if err := c.query(ctx, []string{"accounts", "retrieve-token"}, &token, &result); err != nil {
		rendering.Guidance = err.Error()
		return
	}
	switch result.Code {
	case "TokenValid":
		rendering.Validity = "valid"
		rendering.Account = safeMapboxID(result.Token.User)
		rendering.TokenID = safeMapboxID(result.Token.Authorization)
	case "TokenMalformed", "TokenInvalid", "TokenExpired", "TokenRevoked":
		rendering.Validity = "invalid"
		rendering.Guidance = "Rendering token rejected; update MAPBOX_ACCESS_TOKEN or sign in again"
	default:
		rendering.Guidance = "Mapbox returned an unknown token-validity verdict"
	}
}

func streetsAccess(ctx context.Context, p *Provider) authSection {
	snapshot, err := p.load(ctx, Camera{Center: XY{0.5, 0.5}, Width: 10, Height: 5, Aspect: 2})
	if err != nil {
		return authSection{State: "unavailable", Guidance: "Streets probe cancelled or timed out"}
	}
	if snapshot.Status != "" {
		return authSection{State: "unavailable", Guidance: snapshot.Status}
	}
	return authSection{State: "ready"}
}

func (c *mapboxCLI) usage(ctx context.Context, account, tokenID string, period usagePeriod) usageReport {
	report := usageReport{State: "unavailable", Account: account, TokenID: tokenID}
	args := []string{"usage", "--period-start", period.Start, "--period-end", period.End}
	if tokenID != "" {
		args = append(args, "--token-id", tokenID)
	}
	var result struct {
		GeneratedAt time.Time `json:"generated_at"`
		Data        *struct {
			Period   usagePeriod `json:"period"`
			TokenID  string      `json:"token_id"`
			Products map[string]struct {
				Daily []struct {
					Date  string `json:"date"`
					Usage int64  `json:"usage"`
				} `json:"daily"`
			} `json:"products"`
		} `json:"data"`
	}
	if err := c.query(ctx, args, nil, &result); err != nil {
		report.Guidance = err.Error() + "; statistics access is separate from map access"
		return report
	}
	if result.Data == nil || result.Data.Period != period {
		report.Guidance = "Mapbox usage response is missing the requested UTC period"
		return report
	}
	if tokenID != "" && result.Data.TokenID != tokenID {
		report.Guidance = "Mapbox usage response does not match the active token ID"
		return report
	}
	if result.GeneratedAt.IsZero() {
		report.Guidance = "Mapbox usage response is missing its generation timestamp"
		return report
	}
	report.State, report.Period = "empty", &result.Data.Period
	report.GeneratedAt = &result.GeneratedAt
	report.Daily = map[string]int64{}
	for name, product := range result.Data.Products {
		if !strings.Contains(strings.ToLower(name), "vector tiles") {
			continue
		}
		for _, day := range product.Daily {
			date, err := time.Parse(time.DateOnly, day.Date)
			inPeriod := err == nil && date.Format(time.DateOnly) >= period.Start && day.Date <= period.End
			if !inPeriod || day.Usage < 0 {
				report.State, report.Guidance = "unavailable", "Mapbox usage response has invalid daily figures"
				return report
			}
			report.Daily[day.Date] += day.Usage
			report.Requests += day.Usage
		}
	}
	if report.Requests > 0 {
		report.State = "available"
	}
	return report
}

func collectAuthStatus(ctx context.Context, usage bool) (authStatus, error) {
	return collectAuthStatusWith(ctx, usage, streetsAccess)
}

func collectAuthStatusWith(
	ctx context.Context,
	usage bool,
	probe func(context.Context, *Provider) authSection,
) (authStatus, error) {
	file, err := mapboxCredentialFile()
	if err != nil {
		return authStatus{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	status := authStatus{
		Profile: "default", CredentialFile: file,
		Login:     authSection{State: "unknown"},
		Rendering: renderingStatus{Source: "none", Validity: "unchecked", Streets: authSection{State: "not_checked"}},
	}
	credential, explicit := environmentCredential()
	cli, dependency := checkMapboxCLI(ctx)
	status.CLI = dependency
	if cli == nil {
		status.Login.Guidance = "Saved login could not be checked without a usable Mapbox CLI"
		if !explicit {
			credential = renderingCredential{source: "none", guidance: dependency.Guidance}
		}
	} else {
		status.CLI.State, status.CLIVersion = "ready", cli.version
		status.CLITelemetry = cli.telemetry && !mapboxTelemetryOptOut()
		var identity mapboxIdentity
		if err := cli.query(ctx, []string{"auth", "whoami"}, nil, &identity); err != nil {
			state := "unknown"
			if failure, ok := err.(*mapboxFailure); ok && failure.code == "not_authenticated" {
				state = "logged_out"
			}
			status.Login = authSection{State: state, Guidance: err.Error()}
		} else {
			status.Login = authSection{State: "stored", Account: safeMapboxID(identity.Account)}
			public, err := cli.publicCredential(ctx)
			if err != nil {
				status.Login.Guidance = err.Error()
				if !explicit {
					credential = renderingCredential{source: "none", guidance: err.Error()}
				}
			} else {
				status.Login.State = "usable"
				if !explicit {
					credential = public
				}
			}
		}
	}
	status.Rendering.Source, status.Rendering.Present = credential.source, credential.token != ""
	status.Rendering.Guidance = credential.guidance
	if credential.source == "" {
		status.Rendering.Source = "none"
	}
	if credential.token != "" {
		if cli != nil {
			cli.tokenMetadata(ctx, credential.token, &status.Rendering)
		}
		status.Rendering.Streets = probe(ctx, providerForCredential("mapbox", credential))
	}
	if usage {
		status.Usage = &authUsage{
			Account: usageReport{State: "unavailable", Account: status.Login.Account,
				Guidance: "Usage reporting requires a Mapbox CLI login with statistics access"},
			Token: usageReport{State: "unavailable", TokenID: status.Rendering.TokenID,
				Guidance: "No verified active public token for the logged-in account"},
			Note: "Statistics can lag 24 hours and include other apps using this token; not an invoice or remaining allowance",
		}
		if cli != nil && status.Login.Account != "" {
			end := time.Now().UTC()
			period := usagePeriod{Start: end.AddDate(0, 0, -30).Format(time.DateOnly), End: end.Format(time.DateOnly)}
			status.Usage.Account = cli.usage(ctx, status.Login.Account, "", period)
			sameAccount := status.Rendering.Account == status.Login.Account
			publicToken := strings.HasPrefix(credential.token, "pk.") && status.Rendering.TokenID != ""
			if sameAccount && publicToken && status.Rendering.Validity == "valid" {
				status.Usage.Token = cli.usage(ctx, status.Rendering.Account, status.Rendering.TokenID, period)
			} else {
				status.Usage.Token.Guidance = "Active public token is not verified as belonging to the logged-in account"
			}
		}
	}
	return status, nil
}

func (s authStatus) text() string {
	var out strings.Builder
	if s.CLI.State == "missing" {
		fmt.Fprintf(&out, "Mapbox CLI: not found (checked %s)\n", s.CLI.Checked)
	} else {
		fmt.Fprintf(
			&out,
			"Mapbox CLI: %s %s (checked %s)\n",
			s.CLI.State,
			s.CLIVersion,
			s.CLI.Checked,
		)
	}
	if s.CLI.Path != "" {
		fmt.Fprintf(&out, "Executable: %q\n", s.CLI.Path)
	}
	telemetry := "off"
	if s.CLITelemetry {
		telemetry = "on (opted in)"
	}
	fmt.Fprintf(&out, "Mapbox CLI telemetry: %s\n", telemetry)
	if s.CLI.State != "ready" {
		if s.CLI.State != "missing" || s.CLI.Checked != "PATH" {
			fmt.Fprintln(&out, s.CLI.Guidance)
		}
		fmt.Fprintf(&out, "\nInstall Mapbox CLI using the official instructions:\n  %s\n\nThen run 'shoe auth status' to check your saved login.\n\n",
			s.CLI.InstallURL)
	}
	fmt.Fprintf(&out, "Profile: %s\nCredentials: %s (owned by Mapbox CLI)\n", s.Profile, s.CredentialFile)
	if s.CLI.State != "ready" {
		fmt.Fprintln(&out, "Login: not checked (Mapbox CLI unavailable)")
	} else {
		fmt.Fprintf(&out, "Login: %s %s\n", s.Login.State, s.Login.Account)
	}
	fmt.Fprintf(&out, "Rendering: %s; token %s; account %s\nStreets: %s (status probes make a tile request)\n",
		s.Rendering.Source, s.Rendering.Validity, s.Rendering.Account, s.Rendering.Streets.State)
	seen := map[string]bool{s.CLI.Guidance: true}
	if s.CLI.State != "ready" {
		seen[s.Login.Guidance] = true
	}
	for _, guidance := range []string{s.Login.Guidance, s.Rendering.Guidance, s.Rendering.Streets.Guidance} {
		if guidance != "" && !seen[guidance] {
			fmt.Fprintln(&out, guidance)
			seen[guidance] = true
		}
	}
	if s.Usage != nil {
		for _, entry := range []struct {
			label  string
			report usageReport
		}{
			{label: "Account", report: s.Usage.Account}, {label: "Active token", report: s.Usage.Token},
		} {
			r := entry.report
			fmt.Fprintf(&out, "%s usage: %s; account %s; token ID %s\n", entry.label, r.State, r.Account, r.TokenID)
			if r.State != "unavailable" {
				fmt.Fprintf(&out, "  Vector Tiles requests: %d\n", r.Requests)
			}
			if r.Period != nil {
				fmt.Fprintf(&out, "  UTC period: %s through %s (inclusive)\n", r.Period.Start, r.Period.End)
			}
			if r.GeneratedAt != nil {
				fmt.Fprintf(&out, "  Generated: %s\n", r.GeneratedAt.UTC().Format(time.RFC3339Nano))
			}
			if r.Guidance != "" {
				fmt.Fprintln(&out, " ", r.Guidance)
			}
		}
		fmt.Fprintln(&out, s.Usage.Note)
	}
	return out.String()
}

func runAuth(ctx context.Context, command cliCommand, streams authStreams) error {
	if command.AuthAction == "status" {
		status, err := collectAuthStatus(ctx, command.Usage)
		if err != nil {
			return err
		}
		if command.JSON {
			return json.NewEncoder(streams.out).Encode(status)
		}
		_, err = io.WriteString(streams.out, status.text())
		return err
	}
	file, err := mapboxCredentialFile()
	if err != nil {
		return err
	}
	fmt.Fprintf(streams.out, "Mapbox credentials: %s (shared default profile)\n", file)
	cli, err := findMapboxCLI(ctx)
	if err != nil {
		return err
	}
	if command.AuthAction == "logout" {
		var result struct {
			LoggedOut bool `json:"logged_out"`
		}
		if err := cli.query(ctx, []string{"auth", "logout"}, nil, &result); err != nil {
			return err
		}
		if !result.LoggedOut {
			fmt.Fprintf(streams.out, "No saved Mapbox login at %s\n", file)
			return nil
		}
		fmt.Fprintf(streams.out, "Shared Mapbox login removed from %s; environment tokens and other running apps are unaffected\n", file)
		return nil
	}
	if err := cli.login(ctx, streams); err != nil {
		return err
	}
	fmt.Fprintf(streams.out, "Mapbox login saved by the CLI at %s\n", file)
	credential := resolveCredential(ctx)
	if credential.token == "" {
		fmt.Fprintln(streams.out, "Login succeeded; maps are not ready:", credential.guidance)
		return nil
	}
	access := streetsAccess(ctx, providerForCredential("mapbox", credential))
	fmt.Fprintf(streams.out, "Rendering source: %s; Streets: %s\n", credential.source, access.State)
	if access.Guidance != "" {
		fmt.Fprintln(streams.out, access.Guidance)
	}
	return nil
}
