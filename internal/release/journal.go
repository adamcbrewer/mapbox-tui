package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Journal interface {
	Load() (State, bool, error)
	Save(State) error
}

type FileJournal struct {
	Path string
	lock *os.File
}

func OpenJournal(path string) (*FileJournal, error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another release tool holds the repository lock: %w", err)
	}
	return &FileJournal{Path: path, lock: file}, nil
}
func (j *FileJournal) Close() error { return j.lock.Close() }
func (j *FileJournal) Load() (State, bool, error) {
	data, err := os.ReadFile(j.Path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var state State
	if err = json.Unmarshal(data, &state); err != nil {
		return State{}, false, fmt.Errorf("read release journal: %w", err)
	}
	return state, true, nil
}
func (j *FileJournal) Save(state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(j.Path), "release-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), j.Path); err != nil {
		return fmt.Errorf("replace release journal: %w", err)
	}
	// Sync the directory entry as well as the file: tag/publication intent must
	// survive a crash before the remote mutation starts.
	dir, err := os.Open(filepath.Dir(j.Path))
	if err != nil {
		return fmt.Errorf("open release journal directory: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync release journal directory: %w", err)
	}
	return nil
}
