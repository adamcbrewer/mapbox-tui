package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type appCredentials struct {
	Method string `json:"method"`
	Token  string `json:"public_token,omitempty"`
}

func appCredentialsPath() (string, error) {
	settings, err := appSettingsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(settings), "credentials.json"), nil
}

func loadAppCredentials() (appCredentials, error) {
	credentials := appCredentials{Method: "browser"}
	path, err := appCredentialsPath()
	if err != nil {
		return credentials, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return credentials, nil
	}
	if err != nil {
		return credentials, fmt.Errorf("cannot read saved Mapbox authentication")
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return credentials, fmt.Errorf("invalid Mapbox TUI credential file; run 'mapbox-tui auth login' to replace it")
	}
	file, err := os.Open(path)
	if err != nil {
		return credentials, fmt.Errorf("cannot read saved Mapbox authentication")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return credentials, fmt.Errorf("cannot read saved Mapbox authentication")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var saved appCredentials
	if len(data) > 8192 || decoder.Decode(&saved) != nil || decoder.Decode(new(any)) != io.EOF {
		return credentials, fmt.Errorf("invalid Mapbox TUI credential file; run 'mapbox-tui auth login' to replace it")
	}
	if err := saved.validate(); err != nil {
		return credentials, err
	}
	return saved, nil
}

func (c appCredentials) validate() error {
	switch c.Method {
	case "token":
		return checkPublicTokenFormat(c.Token)
	case "browser", "none":
		if c.Token == "" {
			return nil
		}
	}
	return fmt.Errorf("invalid saved Mapbox authentication method; run 'mapbox-tui auth login'")
}

func saveAppCredentials(credentials appCredentials) error {
	if err := credentials.validate(); err != nil {
		return err
	}
	path, err := appCredentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create Mapbox authentication directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.json")
	if err != nil {
		return fmt.Errorf("create Mapbox authentication file: %w", err)
	}
	defer os.Remove(file.Name())
	writeErr := json.NewEncoder(file).Encode(credentials)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("save Mapbox authentication: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace Mapbox authentication: %w", err)
	}
	return nil
}

func configuredCredential() (renderingCredential, bool) {
	if credential, explicit := environmentCredential(); explicit {
		return credential, true
	}
	credentials, err := loadAppCredentials()
	if err != nil {
		return renderingCredential{source: "saved_token", guidance: err.Error()}, true
	}
	switch credentials.Method {
	case "token":
		return renderingCredential{source: "saved_token", token: credentials.Token}, true
	case "none":
		return renderingCredential{source: "none", guidance: "Signed out; run 'mapbox-tui auth login' to connect"}, true
	}
	return renderingCredential{}, false
}

func checkPublicTokenFormat(token string) error {
	if !strings.HasPrefix(token, "pk.") || len(token) <= 3 || len(token) > 4096 {
		return fmt.Errorf("enter a public Mapbox token beginning with 'pk.'")
	}
	if strings.ContainsFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("public Mapbox token must not contain whitespace or control characters")
	}
	return nil
}

var mapboxTokenClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func validatePublicToken(ctx context.Context, token string) (renderingStatus, error) {
	status := renderingStatus{Source: "saved_token", Present: token != "", Validity: "unchecked"}
	if err := checkPublicTokenFormat(token); err != nil {
		status.Validity = "invalid"
		return status, err
	}
	endpoint := "https://api.mapbox.com/tokens/v2?" + url.Values{"access_token": {token}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return status, fmt.Errorf("cannot prepare Mapbox token validation")
	}
	response, err := mapboxTokenClient.Do(req)
	if err != nil {
		return status, fmt.Errorf("cannot validate Mapbox token; check connectivity and try again")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return status, fmt.Errorf("Mapbox token validation returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Code  string `json:"code"`
		Token struct {
			User          string `json:"user"`
			Authorization string `json:"authorization"`
		} `json:"token"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&result) != nil {
		return status, fmt.Errorf("Mapbox returned an invalid token validation response")
	}
	switch result.Code {
	case "TokenValid":
		status.Validity = "valid"
		status.Account, status.TokenID = safeMapboxID(result.Token.User), safeMapboxID(result.Token.Authorization)
		return status, nil
	case "TokenMalformed", "TokenInvalid", "TokenExpired", "TokenRevoked":
		status.Validity = "invalid"
		return status, fmt.Errorf("Mapbox rejected the public token; replace it using 'mapbox-tui auth login'")
	}
	return status, fmt.Errorf("Mapbox returned an unknown token validation verdict")
}
