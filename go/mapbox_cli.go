package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const mapboxInstallURL = "https://github.com/mapbox/mapbox-cli#install-a-released-binary"
const mapboxSignupURL = "https://docs.mapbox.com/accounts/guides/signup/"
const mapboxLoginGuidance = "Run 'mapbox-tui auth login' to sign in or create a Mapbox account"
const mapboxCLIUnavailable = "Mapbox CLI not found; install the optional CLI: " + mapboxInstallURL +
	"; then run 'mapbox-tui auth login'. Offline features work without it; MAPBOX_ACCESS_TOKEN can supply a public token"

type mapboxDependencyStatus struct {
	State      string `json:"state"`
	Checked    string `json:"checked"`
	Path       string `json:"path,omitempty"`
	Guidance   string `json:"guidance,omitempty"`
	InstallURL string `json:"install_url,omitempty"`
}

type mapboxDependencyError struct{ status mapboxDependencyStatus }

func (e *mapboxDependencyError) Error() string { return e.status.Guidance }

var mapboxIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var mapboxVersion = regexp.MustCompile(`^mapbox ([0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?)$`)

type mapboxCLI struct {
	path      string
	version   string
	telemetry bool
}

type mapboxFailure struct{ code string }

func (e *mapboxFailure) Error() string {
	switch e.code {
	case "not_authenticated", "unauthorized":
		return "Mapbox login unavailable or rejected; " + mapboxLoginGuidance
	case "forbidden":
		return "Mapbox permission/account access unavailable; sign in again or check account access with Mapbox"
	case "rate_limited":
		return "Mapbox rate limited the request; wait before trying again"
	case "network_error":
		return "Mapbox network request failed; check connectivity and try again"
	case "timeout":
		return "Mapbox operation timed out; try again (complete signup/email verification before restarting login)"
	case "cancelled":
		return "Mapbox operation cancelled"
	default:
		return "Mapbox operation failed; try 'mapbox-tui auth login' or check the optional CLI installation"
	}
}

type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	const limit = 2 * 1024 * 1024
	if b.Len()+len(p) > limit {
		b.exceeded = true
		return 0, fmt.Errorf("Mapbox output exceeds 2 MiB")
	}
	return b.Buffer.Write(p)
}

func mapboxEnvironment(token *string, telemetry bool) []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "MAPBOX_ACCESS_TOKEN", "MapboxAccessToken", "MAPBOX_USERNAME", "MAPBOX_DEBUG",
			"MAPBOX_OUTPUT", "MAPBOX_TIMEOUT", "MAPBOX_CLI_EXTRA_QUERY", "MAPBOX_NO_UPDATE_CHECK",
			"MAPBOX_CLI_NO_TELEMETRY":
			continue
		}
		env = append(env, entry)
	}
	optOut := "1"
	if telemetry && !mapboxTelemetryOptOut() {
		optOut = "0"
	}
	env = append(env, "MAPBOX_NO_UPDATE_CHECK=1", "MAPBOX_CLI_NO_TELEMETRY="+optOut)
	if token != nil {
		env = append(env, "MAPBOX_ACCESS_TOKEN="+*token)
	}
	return env
}

func findMapboxCLI(ctx context.Context) (*mapboxCLI, error) {
	return findMapboxCLIWithTelemetry(ctx, nil)
}

func findMapboxCLIWithTelemetry(ctx context.Context, telemetry *bool) (*mapboxCLI, error) {
	cli, status := checkMapboxCLIWithTelemetry(ctx, telemetry)
	if cli == nil {
		return nil, &mapboxDependencyError{status: status}
	}
	return cli, nil
}

func checkMapboxCLI(ctx context.Context) (*mapboxCLI, mapboxDependencyStatus) {
	return checkMapboxCLIWithTelemetry(ctx, nil)
}

func checkMapboxCLIWithTelemetry(ctx context.Context, telemetry *bool) (*mapboxCLI, mapboxDependencyStatus) {
	status := mapboxDependencyStatus{State: "missing", Checked: "PATH", InstallURL: mapboxInstallURL}
	path := os.Getenv("MAPBOX_TUI_CLI")
	if path == "" {
		var err error
		path, err = exec.LookPath("mapbox")
		if err != nil {
			status.Guidance = "Mapbox CLI was not found on PATH; install it: " + mapboxInstallURL
			return nil, status
		}
	} else {
		status.Checked = "MAPBOX_TUI_CLI"
	}
	path, err := filepath.Abs(path)
	if err != nil {
		status.State, status.Guidance = "unavailable", "Cannot resolve Mapbox CLI; use an absolute MAPBOX_TUI_CLI path"
		return nil, status
	}
	status.Path = path
	if _, err := exec.LookPath(path); err != nil {
		status.Guidance = "Mapbox CLI executable is missing; fix MAPBOX_TUI_CLI or install it: " + mapboxInstallURL
		if !errors.Is(err, os.ErrNotExist) {
			status.State, status.Guidance = "unavailable", "Mapbox CLI is not executable; fix its installation or MAPBOX_TUI_CLI"
		}
		return nil, status
	}
	var enabled bool
	if telemetry != nil {
		enabled = *telemetry
	} else if settings, err := loadAppSettings(); err == nil {
		enabled = settings.MapboxCLITelemetry
	}
	cli := &mapboxCLI{path: path, telemetry: enabled}
	if err := cli.check(ctx); err != nil {
		status.State, status.Guidance = "unavailable", err.Error()
		return nil, status
	}
	status.State, status.InstallURL = "ready", ""
	return cli, status
}

func (c *mapboxCLI) command(ctx context.Context, args []string, token *string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Env = mapboxEnvironment(token, c.telemetry)
	cmd.WaitDelay = time.Second
	return cmd
}

func (c *mapboxCLI) output(ctx context.Context, args []string, token *string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := c.command(ctx, args, token)
	var stdout, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, &mapboxFailure{code: "cancelled"}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, &mapboxFailure{code: "timeout"}
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("Mapbox CLI output exceeded its limit; update/check the CLI installation")
	}
	if err == nil {
		return stdout.Bytes(), nil
	}
	if _, ok := errors.AsType[*exec.Error](err); ok {
		return nil, fmt.Errorf("%s", mapboxCLIUnavailable)
	}
	if _, ok := errors.AsType[*os.PathError](err); ok {
		return nil, fmt.Errorf("cannot run Mapbox CLI; check MAPBOX_TUI_CLI or install it: %s", mapboxInstallURL)
	}
	for _, line := range slices.Backward(strings.Split(stderr.String(), "\n")) {
		var failure struct {
			Code   string `json:"code"`
			Status int    `json:"status"`
		}
		if json.Unmarshal([]byte(line), &failure) != nil || failure.Code == "" {
			continue
		}
		code := failure.Code
		switch failure.Status {
		case 401:
			code = "unauthorized"
		case 403:
			code = "forbidden"
		case 429:
			code = "rate_limited"
		}
		return nil, &mapboxFailure{code: code}
	}
	return nil, &mapboxFailure{}
}

func (c *mapboxCLI) query(ctx context.Context, args []string, token *string, result any) error {
	options := []string{"--profile", "default", "--output", "json", "--timeout", "8"}
	if token == nil {
		options = append(options, "--use-login")
	}
	data, err := c.output(ctx, append(options, args...), token)
	if err != nil {
		return err
	}
	if json.Unmarshal(data, result) != nil {
		return fmt.Errorf("Mapbox CLI returned unexpected JSON; update/check its installation")
	}
	return nil
}

func (c *mapboxCLI) check(ctx context.Context) error {
	version, err := c.output(ctx, []string{"--version"}, nil)
	if err != nil {
		return err
	}
	match := mapboxVersion.FindStringSubmatch(strings.TrimSpace(string(version)))
	if len(match) != 2 {
		return fmt.Errorf("Mapbox CLI version is unrecognised; install a compatible release: %s", mapboxInstallURL)
	}
	data, err := c.output(ctx, []string{"--schema"}, nil)
	if err != nil {
		return err
	}
	var schema struct {
		Version int `json:"schema_version"`
		Globals []struct {
			Flag   string   `json:"flag"`
			Values []string `json:"values"`
		} `json:"global_options"`
		Commands []struct {
			Command   string `json:"command"`
			Arguments []struct {
				Flag   string   `json:"flag"`
				Values []string `json:"values"`
			} `json:"arguments"`
		} `json:"commands"`
	}
	if json.Unmarshal(data, &schema) != nil || schema.Version != 1 {
		return fmt.Errorf("Mapbox CLI schema is incompatible; update it: %s", mapboxInstallURL)
	}
	globals := map[string]bool{}
	for _, option := range schema.Globals {
		globals[option.Flag] = option.Flag != "--output" || slices.Contains(option.Values, "json")
	}
	for _, flag := range []string{"--profile", "--use-login", "--output", "--timeout"} {
		if !globals[flag] {
			return fmt.Errorf("Mapbox CLI lacks required JSON/login options; update it: %s", mapboxInstallURL)
		}
	}
	required := map[string][]string{
		"mapbox auth login":              nil,
		"mapbox auth logout":             nil,
		"mapbox auth whoami":             nil,
		"mapbox accounts list-tokens":    {"--default", "--usage"},
		"mapbox accounts retrieve-token": nil,
		"mapbox usage":                   {"--token-id", "--period-start", "--period-end"},
	}
	for _, command := range schema.Commands {
		flags, needed := required[command.Command]
		if !needed {
			continue
		}
		for _, argument := range command.Arguments {
			publicTokenFilter := command.Command == "mapbox accounts list-tokens" && argument.Flag == "--usage"
			if publicTokenFilter && !slices.Contains(argument.Values, "pk") {
				return fmt.Errorf("Mapbox CLI lacks the public-token filter; update it: %s", mapboxInstallURL)
			}
			flags = slices.DeleteFunc(flags, func(flag string) bool { return flag == argument.Flag })
		}
		if len(flags) == 0 {
			delete(required, command.Command)
		}
	}
	if len(required) != 0 {
		return fmt.Errorf("Mapbox CLI lacks required auth/token/usage commands; update it: %s", mapboxInstallURL)
	}
	c.version = match[1]
	return nil
}

func (c *mapboxCLI) loginCommand(ctx context.Context) *exec.Cmd {
	return c.command(ctx, []string{"--profile", "default", "--output", "text", "--timeout", "10", "auth", "login"}, nil)
}

type authStreams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

func (c *mapboxCLI) login(ctx context.Context, streams authStreams) error {
	ctx, cancel := context.WithTimeout(ctx, 7*time.Minute)
	defer cancel()
	cmd := c.loginCommand(ctx)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.in, streams.out, streams.err
	if _, terminal := authTerminal(streams.in); terminal {
		stop, err := watchMapboxCallbacks(ctx, streams)
		if err != nil {
			return err
		}
		defer stop()
	}
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &mapboxFailure{code: "timeout"}
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return &mapboxFailure{code: "cancelled"}
		}
		return &mapboxFailure{}
	}
	return nil
}

func safeMapboxID(value string) string {
	if mapboxIdentifier.MatchString(value) {
		return value
	}
	return ""
}
