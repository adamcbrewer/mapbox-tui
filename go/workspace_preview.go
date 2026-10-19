package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Preview data contains only display metadata, never imported geometry or stops.
// Imports are unchecked until a candidate workspace has actually been loaded.
type workspacePreview struct {
	name, path        string
	created, modified time.Time
	journeys          int
	imports           []workspaceImportPreview
	checked           bool
}

type workspaceImportPreview struct {
	path        string
	unavailable bool
}

func previewWorkspace(checkpoint *workspaceCheckpoint, unresolved []savedImport, checked bool) workspacePreview {
	saved := checkpoint.manifest
	preview := workspacePreview{
		name: saved.Name, path: checkpoint.path, created: saved.Created, modified: saved.Modified,
		journeys: len(saved.Journeys), checked: checked,
	}
	missing := make(map[string]bool, len(unresolved))
	for _, entry := range unresolved {
		missing[entry.ID] = true
	}
	for _, entry := range saved.Imports {
		path := entry.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(checkpoint.path), path)
		}
		preview.imports = append(preview.imports, workspaceImportPreview{path: path, unavailable: missing[entry.ID]})
	}
	return preview
}

func (p workspacePreview) detailLines(width int, ascii bool) []string {
	lines := []string{
		"Created:  " + p.created.Local().Format("02 Jan 2006 15:04 MST"),
		"Modified: " + p.modified.Local().Format("02 Jan 2006 15:04 MST"),
		fmt.Sprintf("Imports: %d | Journeys: %d", len(p.imports), p.journeys),
	}
	if len(p.imports) == 0 {
		lines = append(lines, "No imported files.")
	}
	for _, entry := range p.imports {
		name := cleanPlaceText(filepath.Base(entry.path))
		if entry.unavailable {
			name += " (missing/changed)"
		}
		lines = append(lines, name, "  "+abbreviatedWorkspacePath(entry.path, max(1, width-2), ascii))
	}
	return lines
}

func (p workspacePreview) pickerDetails(width int, ascii bool) string {
	lines := []string{
		"Modified: " + p.modified.Local().Format("02 Jan 2006 15:04"),
		"Created:  " + p.created.Local().Format("02 Jan 2006"),
		fmt.Sprintf("Imports: %d | Journeys: %d", len(p.imports), p.journeys),
	}
	for _, entry := range p.imports {
		lines = append(lines, "  "+workspaceFilename(entry.path, max(1, min(36, width-2)), ascii))
	}
	if !p.checked && len(p.imports) > 0 {
		lines = append(lines, "Files checked when opening.")
	}
	return ansi.Hardwrap(strings.Join(lines, "\n"), max(1, width), true)
}

func workspaceFilename(path string, width int, ascii bool) string {
	name := cleanPlaceText(filepath.Base(path))
	if ansi.StringWidth(name) <= width {
		return name
	}
	ending := "…"
	if ascii {
		ending = "..."
	}
	extension := filepath.Ext(name)
	room := width - ansi.StringWidth(extension)
	if room <= ansi.StringWidth(ending) {
		return ansi.Truncate(name, width, ending)
	}
	return ansi.Truncate(name, room, ending) + extension
}

func (p workspacePreview) confirmation(width int, ascii bool) string {
	lines := append([]string{p.name}, p.detailLines(width, ascii)...)
	lines = append(lines, "", "Playback pauses. Road routes recalculate when online.")
	return ansi.Hardwrap(strings.Join(lines, "\n"), max(1, width), true)
}
