package main

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

//go:embed themes/*.toml
var themeFiles embed.FS

var mapRoleNames = [...]string{
	"none",
	"water",
	"road",
	"major_road",
	"place",
	"route",
	"marker",
	"cursor",
	"active_route",
	"active_marker",
	"traffic_low",
	"traffic_moderate",
	"traffic_heavy",
	"traffic_severe",
	"traffic_closed",
}
var themeIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var themeColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var builtinThemeOrder = map[string]int{
	"catppuccin": 0,
	"nord":       1,
	"synthwave":  2,
	"noir":       3,
	"paper":      4,
}

func decodeTheme(r io.Reader) (presentationTheme, error) {
	var theme presentationTheme
	if err := toml.NewDecoder(r).DisallowUnknownFields().Decode(&theme); err != nil {
		return theme, fmt.Errorf("invalid theme TOML: %w", err)
	}
	if !themeIDPattern.MatchString(theme.ID) {
		return theme, fmt.Errorf("theme id must use lowercase words or numbers separated by hyphens")
	}
	if strings.TrimSpace(theme.Name) == "" || clean(theme.Name) != theme.Name {
		return theme, fmt.Errorf("theme name must be non-empty printable ASCII")
	}
	if len(theme.Activities) != activityColorCount {
		return theme, fmt.Errorf("theme activities must contain exactly %d colours", activityColorCount)
	}
	if theme.ActivityText == nil {
		theme.ActivityText = slices.Clone(theme.Activities)
	}
	if len(theme.ActivityText) != activityColorCount {
		return theme, fmt.Errorf("theme activity_text must contain exactly %d colours when specified", activityColorCount)
	}
	if theme.Map == nil {
		theme.Map = map[string]string{}
	}
	for i, colour := range []string{"#40a02b", "#df8e1d", "#d20f39", "#a3153a", "#d20f39"} {
		role := mapRoleNames[roleTrafficLow+mapRole(i)]
		if _, present := theme.Map[role]; !present {
			theme.Map[role] = colour
		}
	}
	colors := map[string]string{
		"background":     theme.Background,
		"surface":        theme.Surface,
		"selected":       theme.Selected,
		"text":           theme.Text,
		"muted":          theme.Muted,
		"accent":         theme.Accent,
		"highlight":      theme.Highlight,
		"border":         theme.Border,
		"dimmed":         theme.Dimmed,
		"progress_empty": theme.ProgressEmpty,
	}
	for _, role := range mapRoleNames {
		colors["map."+role] = theme.Map[role]
	}
	for role := range theme.Map {
		if !slices.Contains(mapRoleNames[:], role) {
			return theme, fmt.Errorf("unknown theme map role %q", role)
		}
	}
	for i, color := range theme.Activities {
		colors[fmt.Sprintf("activities[%d]", i)] = color
	}
	for i, color := range theme.ActivityText {
		colors[fmt.Sprintf("activity_text[%d]", i)] = color
	}
	if theme.BorderGradient != nil && len(theme.BorderGradient) < 2 {
		return theme, fmt.Errorf("theme border_gradient must contain at least two colours when specified")
	}
	for i, color := range theme.BorderGradient {
		colors[fmt.Sprintf("border_gradient[%d]", i)] = color
	}
	for role, color := range colors {
		if !themeColorPattern.MatchString(color) {
			return theme, fmt.Errorf("theme %s must be a #RRGGBB colour", role)
		}
	}
	return theme, nil
}

func loadThemes(choice string) ([]presentationTheme, int, error) {
	entries, err := themeFiles.ReadDir("themes")
	if err != nil {
		return nil, 0, err
	}
	themes := make([]presentationTheme, 0, len(entries))
	for _, entry := range entries {
		data, err := themeFiles.ReadFile("themes/" + entry.Name())
		if err != nil {
			return nil, 0, err
		}
		theme, err := decodeTheme(bytes.NewReader(data))
		if err != nil {
			return nil, 0, fmt.Errorf("theme %s: %w", entry.Name(), err)
		}
		for _, existing := range themes {
			if existing.ID == theme.ID {
				return nil, 0, fmt.Errorf("duplicate theme id %q", theme.ID)
			}
		}
		themes = append(themes, theme)
	}
	slices.SortFunc(themes, func(a, b presentationTheme) int {
		aOrder, aBuiltIn := builtinThemeOrder[a.ID]
		bOrder, bBuiltIn := builtinThemeOrder[b.ID]
		if aBuiltIn && bBuiltIn {
			return aOrder - bOrder
		}
		if aBuiltIn {
			return -1
		}
		if bBuiltIn {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	if choice == "" {
		choice = "catppuccin"
	}
	for i, theme := range themes {
		if theme.ID == choice {
			return themes, i, nil
		}
	}
	if !strings.EqualFold(filepath.Ext(choice), ".toml") && !strings.ContainsAny(choice, `/\`) {
		return nil, 0, fmt.Errorf("unknown theme %q; choose a built-in id or a TOML file path", choice)
	}
	file, err := os.Open(choice)
	if err != nil {
		return nil, 0, fmt.Errorf("open theme %q: %w", choice, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return nil, 0, fmt.Errorf("read theme %q: %w", choice, err)
	}
	if len(data) > 65536 {
		return nil, 0, fmt.Errorf("theme %q exceeds 64 KiB", choice)
	}
	theme, err := decodeTheme(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("theme %q: %w", choice, err)
	}
	for _, existing := range themes {
		if existing.ID == theme.ID {
			return nil, 0, fmt.Errorf("custom theme id %q matches a built-in; choose a unique id", theme.ID)
		}
	}
	theme.Path, err = filepath.Abs(choice)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve theme path: %w", err)
	}
	return append(themes, theme), len(themes), nil
}
