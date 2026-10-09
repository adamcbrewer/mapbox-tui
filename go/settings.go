package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type appSettings struct {
	MapboxCLITelemetry bool `json:"mapbox_cli_telemetry"`
}

func appSettingsPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate shoe settings: %w", err)
	}
	return filepath.Join(directory, "shoe", "settings.json"), nil
}

func loadAppSettings() (appSettings, error) {
	var settings appSettings
	path, err := appSettingsPath()
	if err != nil {
		return settings, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("read shoe settings: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 65537))
	closeErr := file.Close()
	if readErr != nil {
		return settings, fmt.Errorf("read shoe settings: %w", readErr)
	}
	if closeErr != nil {
		return settings, fmt.Errorf("close shoe settings: %w", closeErr)
	}
	if len(data) > 65536 || json.Unmarshal(data, &settings) != nil {
		return settings, fmt.Errorf("invalid shoe settings at %q", path)
	}
	return settings, nil
}

func saveAppSettings(settings appSettings) error {
	path, err := appSettingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode shoe settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create shoe settings directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return fmt.Errorf("create shoe settings file: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("save shoe settings: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close shoe settings: %w", closeErr)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace shoe settings: %w", err)
	}
	return nil
}

func mapboxTelemetryOptOut() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("MAPBOX_CLI_NO_TELEMETRY")))
	switch value {
	case "", "0", "f", "false", "n", "no", "off":
		return false
	}
	return true
}
