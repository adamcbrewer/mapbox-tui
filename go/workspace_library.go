package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type workspaceListing struct {
	path       string
	name       string
	modified   time.Time
	problem    string
	preview    workspacePreview
	checkpoint *workspaceCheckpoint
}

// Listing reads manifests only; imported geometry is loaded only when opening a save.
func (s workspaceStore) list() ([]workspaceListing, error) {
	files, err := os.ReadDir(s.directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []workspaceListing
	for _, file := range files {
		if file.IsDir() || !strings.EqualFold(filepath.Ext(file.Name()), ".json") {
			continue
		}
		entry := workspaceListing{path: filepath.Join(s.directory, file.Name()), name: cleanPlaceText(file.Name())}
		checkpoint, err := readWorkspace(entry.path)
		if err != nil {
			entry.problem = err.Error()
		} else {
			entry.checkpoint = checkpoint
			entry.name, entry.modified = checkpoint.manifest.Name, checkpoint.manifest.Modified
			entry.preview = previewWorkspace(checkpoint, nil, false)
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b workspaceListing) int {
		if order := b.modified.Compare(a.modified); order != 0 {
			return order
		}
		if order := strings.Compare(a.name, b.name); order != 0 {
			return order
		}
		return strings.Compare(a.path, b.path)
	})
	return entries, nil
}
