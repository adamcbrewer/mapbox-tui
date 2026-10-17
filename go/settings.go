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
	Traffic            bool `json:"traffic"`
	BillingAccepted    int  `json:"mapbox_billing_accepted"`
	Offline            bool `json:"offline"`
}

func appSettingsPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate mapbox-tui settings: %w", err)
	}
	return filepath.Join(directory, "mapbox-tui", "settings.json"), nil
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
		return settings, fmt.Errorf("read mapbox-tui settings: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 65537))
	closeErr := file.Close()
	if readErr != nil {
		return settings, fmt.Errorf("read mapbox-tui settings: %w", readErr)
	}
	if closeErr != nil {
		return settings, fmt.Errorf("close mapbox-tui settings: %w", closeErr)
	}
	if len(data) > 65536 || json.Unmarshal(data, &settings) != nil {
		return settings, fmt.Errorf("invalid mapbox-tui settings at %q", path)
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
		return fmt.Errorf("encode mapbox-tui settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create mapbox-tui settings directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return fmt.Errorf("create mapbox-tui settings file: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("save mapbox-tui settings: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close mapbox-tui settings: %w", closeErr)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace mapbox-tui settings: %w", err)
	}
	return nil
}

// Reload before changing a preference so a long-lived viewer does not overwrite
// unrelated choices saved by a later CLI invocation or another viewer.
func updateAppSettings(change func(*appSettings)) (appSettings, error) {
	path, err := appSettingsPath()
	if err != nil {
		return appSettings{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return appSettings{}, fmt.Errorf("create mapbox-tui settings directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return appSettings{}, fmt.Errorf("open mapbox-tui settings lock: %w", err)
	}
	defer lock.Close()
	if err := lockLocalFile(lock); err != nil {
		return appSettings{}, fmt.Errorf("mapbox-tui settings are busy; try again")
	}
	defer unlockLocalFile(lock)
	settings, err := loadAppSettings()
	if err != nil {
		return settings, err
	}
	change(&settings)
	return settings, saveAppSettings(settings)
}

func mapboxTelemetryOptOut() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("MAPBOX_CLI_NO_TELEMETRY")))
	switch value {
	case "", "0", "f", "false", "n", "no", "off":
		return false
	}
	return true
}
