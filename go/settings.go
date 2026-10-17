package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type appSettings struct {
	MapboxCLITelemetry     bool       `json:"mapbox_cli_telemetry"`
	Traffic                bool       `json:"traffic"`
	BillingAcceptedVersion int        `json:"mapbox_billing_accepted_version"`
	Offline                bool       `json:"offline"`
	Theme                  string     `json:"theme,omitempty"`
	ASCII                  *bool      `json:"ascii,omitempty"`
	CameraAnimation        *bool      `json:"camera_animation,omitempty"`
	Elevation              string     `json:"elevation,omitempty"`
	LastView               *savedView `json:"last_view,omitempty"`
}

// Legacy keys are read-only aliases. Writes use appSettings and emit only the
// current names. Presence matters: an explicit new false/zero beats an old value.
type settingsDocument struct {
	appSettings
	BillingVersion       *int               `json:"mapbox_billing_accepted_version"`
	LegacyBillingVersion *int               `json:"mapbox_billing_accepted"`
	LegacyNoAnimation    *bool              `json:"no_animation"`
	View                 *savedViewDocument `json:"last_view"`
}

type savedViewDocument struct {
	savedView
	LegacyLon *float64 `json:"center_lon"`
	LegacyLat *float64 `json:"center_lat"`
}

func (d settingsDocument) current() appSettings {
	s := d.appSettings
	if d.BillingVersion != nil {
		s.BillingAcceptedVersion = *d.BillingVersion
	} else if d.LegacyBillingVersion != nil {
		s.BillingAcceptedVersion = *d.LegacyBillingVersion
	}
	if s.CameraAnimation == nil && d.LegacyNoAnimation != nil {
		s.CameraAnimation = new(!*d.LegacyNoAnimation)
	}
	if d.View != nil {
		view := d.View.savedView
		if view.Lon == nil {
			view.Lon = d.View.LegacyLon
		}
		if view.Lat == nil {
			view.Lat = d.View.LegacyLat
		}
		s.LastView = &view
	}
	return s
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	// Reject incompatible schemas rather than dropping fields on the next save.
	var decoded *settingsDocument
	if len(data) > 65536 || decoder.Decode(&decoded) != nil || decoded == nil {
		return appSettings{}, fmt.Errorf("invalid or incompatible mapbox-tui settings at %q", path)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return appSettings{}, fmt.Errorf("invalid mapbox-tui settings at %q", path)
	}
	settings = decoded.current()
	if err := settings.validate(); err != nil {
		return appSettings{}, fmt.Errorf("invalid mapbox-tui settings at %q: %w", path, err)
	}
	return settings, nil
}

func (s appSettings) validate() error {
	if s.LastView != nil {
		if err := s.LastView.validate(); err != nil {
			return fmt.Errorf("last_view: %w", err)
		}
	}
	if _, err := parseElevationMode(s.Elevation); err != nil {
		return err
	}
	if len(s.Theme) > 4096 || strings.ContainsAny(s.Theme, "\x00\r\n") {
		return fmt.Errorf("theme must be a built-in ID or a path of at most 4096 bytes")
	}
	return nil
}

// Call through updateAppSettings for mutations; callers must hold the settings lock.
func saveAppSettings(settings appSettings) error {
	if err := settings.validate(); err != nil {
		return err
	}
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
	if writeErr == nil {
		writeErr = file.Sync()
	}
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
