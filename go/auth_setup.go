package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
)

func authTerminal(input io.Reader) (*os.File, bool) {
	file, ok := input.(*os.File)
	return file, ok && term.IsTerminal(file.Fd())
}

func readAuthInput(ctx context.Context, streams authStreams, hidden bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if hidden {
		file, terminal := authTerminal(streams.in)
		if !terminal {
			return "", fmt.Errorf("token entry needs a terminal; use --token-stdin to pipe a public token")
		}
		state, err := term.MakeRaw(file.Fd())
		if err != nil {
			return "", fmt.Errorf("prepare private token input: %w", err)
		}
		defer func() {
			term.Restore(file.Fd(), state)
			fmt.Fprintln(streams.out)
		}()
	}
	inputReader := streams.in
	var inputFile *os.File
	if file, ok := streams.in.(*os.File); ok {
		info, err := file.Stat()
		regular := err == nil && info.Mode().IsRegular()
		windowsPipe := runtime.GOOS == "windows" && err == nil && info.Mode()&os.ModeNamedPipe != 0
		if regular || windowsPipe {
			// cancelreader otherwise opens CONIN$ for redirected Windows stdin.
			inputReader = io.LimitReader(file, 4097)
		}
		if windowsPipe {
			inputFile = file
		}
	}
	reader, err := cancelreader.NewReader(inputReader)
	if err != nil {
		return "", fmt.Errorf("prepare authentication input: %w", err)
	}
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		reader.Cancel()
		if inputFile != nil {
			inputFile.Close()
		}
		close(cancelled)
	})
	defer func() {
		if !stop() {
			<-cancelled
		}
		reader.Close()
	}()
	input := bufio.NewReader(reader)
	var value []byte
	for {
		b, err := input.ReadByte()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, io.EOF) && len(value) != 0 {
			return strings.TrimSpace(string(value)), nil
		}
		if err != nil {
			return "", fmt.Errorf("authentication input closed or unavailable")
		}
		switch b {
		case '\r', '\n':
			return strings.TrimSpace(string(value)), nil
		case 3, 4, 27:
			return "", &mapboxFailure{code: "cancelled"}
		case 8, 127:
			if len(value) != 0 {
				value = value[:len(value)-1]
			}
		default:
			value = append(value, b)
			if len(value) > 4096 {
				return "", fmt.Errorf("authentication input is too long")
			}
		}
	}
}

func performMapboxLogin(ctx context.Context, command cliCommand, streams authStreams, cli *mapboxCLI) error {
	if err := ctx.Err(); err != nil {
		return &mapboxFailure{code: "cancelled"}
	}
	method := command.AuthMethod
	if method == "" {
		method = "browser"
		if _, terminal := authTerminal(streams.in); terminal {
			choice, err := chooseAuthMethod(ctx, streams)
			if err != nil {
				return err
			}
			method = choice
		}
	}
	if method == "token" {
		if command.TokenStdin {
			if _, terminal := authTerminal(streams.in); terminal {
				return fmt.Errorf("pipe a public token to --token-stdin, or use interactive login for hidden entry")
			}
		} else {
			fmt.Fprint(streams.out, "Public API token (hidden): ")
		}
		token, err := readAuthInput(ctx, streams, !command.TokenStdin)
		if err != nil {
			return err
		}
		if _, err := validatePublicToken(ctx, token); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := saveAppCredentials(appCredentials{Method: "token", Token: token}); err != nil {
			return err
		}
		path, _ := appCredentialsPath()
		fmt.Fprintf(streams.out, "Public token saved at %s; Mapbox CLI login is unchanged.\n", path)
		return nil
	}
	file, err := mapboxCredentialFile()
	if err != nil {
		return err
	}
	fmt.Fprintf(streams.out, "Mapbox credentials: %s (shared default profile)\n", file)
	if cli == nil {
		cli, err = findMapboxCLI(ctx)
		if err != nil {
			return err
		}
	}
	if err := cli.login(ctx, streams); err != nil {
		return err
	}
	if err := saveAppCredentials(appCredentials{Method: "browser"}); err != nil {
		return fmt.Errorf("CLI login succeeded but its selection could not be saved: %w", err)
	}
	fmt.Fprintf(streams.out, "Mapbox login saved by the CLI at %s\n", file)
	return nil
}

func logoutMapbox(ctx context.Context, streams authStreams) error {
	credentials, err := loadAppCredentials()
	if err != nil {
		return err
	}
	message := "Saved public token removed; the shared CLI login is unchanged. Environment tokens are unaffected."
	if credentials.Method == "browser" {
		file, err := mapboxCredentialFile()
		if err != nil {
			return err
		}
		fmt.Fprintf(streams.out, "Mapbox credentials: %s (shared default profile)\n", file)
		cli, err := findMapboxCLI(ctx)
		if err != nil {
			return err
		}
		var result struct {
			LoggedOut bool `json:"logged_out"`
		}
		if err := cli.query(ctx, []string{"auth", "logout"}, nil, &result); err != nil {
			return err
		}
		message = fmt.Sprintf("Shared Mapbox login removed from %s; environment tokens and other running apps are unaffected", file)
		if !result.LoggedOut {
			message = "No saved Mapbox login at " + file
		}
	}
	if err := saveAppCredentials(appCredentials{Method: "none"}); err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.out, message)
	return err
}
