package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestWorkspacePreviewMetadataAndValidatedImports(t *testing.T) {
	checkpoint := &workspaceCheckpoint{
		path: filepath.Join(t.TempDir(), "workspace.json"),
		manifest: savedWorkspace{Name: "Playback pauses. is a workspace name", Created: time.Now(), Modified: time.Now(),
			Imports: []savedImport{{ID: "import-one", Path: "recordings/café.gpx"}}},
	}
	metadata := previewWorkspace(checkpoint, nil, false)
	if metadata.imports[0].path != filepath.Join(filepath.Dir(checkpoint.path), "recordings/café.gpx") {
		t.Fatal("relative preview path was not resolved beside manifest")
	}
	picker := metadata.pickerDetails(45, true)
	if !strings.Contains(picker, "checked when opening") || strings.Contains(picker, "missing/changed") {
		t.Fatal("metadata preview misrepresented validation", picker)
	}
	validated := previewWorkspace(checkpoint, checkpoint.manifest.Imports, true)
	confirmation := validated.confirmation(45, true)
	if !strings.Contains(confirmation, checkpoint.manifest.Name) || !strings.Contains(confirmation, "café.gpx (missing/changed)") {
		t.Fatal("structured confirmation lost name or availability", confirmation)
	}
	for _, body := range []string{picker, confirmation} {
		for _, line := range strings.Split(body, "\n") {
			if ansi.StringWidth(line) > 45 {
				t.Fatal("preview exceeded width", line)
			}
		}
	}
}

func TestWorkspacePreviewShortensFilenamesWithoutStoragePaths(t *testing.T) {
	for _, test := range []struct {
		name  string
		ascii bool
	}{
		{name: "unicode"},
		{name: "ascii", ascii: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := "/very/long/folder/" + strings.Repeat("旅行", 40) + ".gpx"
			short := workspaceFilename(path, 24, test.ascii)
			if ansi.StringWidth(short) > 24 || !strings.HasSuffix(short, ".gpx") {
				t.Fatal("filename lost extension or exceeded its width", short)
			}
			p := workspacePreview{path: "/settings/workspaces/opaque-workspace-id.json",
				imports: []workspaceImportPreview{{path: path}}}
			body := p.pickerDetails(45, test.ascii)
			if strings.Contains(body, "opaque-workspace-id") || strings.Contains(body, "/very/long/folder") {
				t.Fatal("picker still shows long storage paths", body)
			}
		})
	}
}
