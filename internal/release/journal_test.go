package release

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestJournalLocksAndPersistsIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	journal, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if other, err := OpenJournal(path); err == nil {
		other.Close()
		t.Fatal("second journal acquired repository lock")
	}
	if _, exists, err := journal.Load(); err != nil || exists {
		t.Fatalf("initial journal exists=%v: %v", exists, err)
	}
	state := State{Version: "v0.2.0", TagIntent: true, Notes: "reviewed notes", Policy: VersionPolicy{Latest: "v0.1.0", Tags: []string{"v0.1.0"}}}
	if err := journal.Save(state); err != nil {
		t.Fatal(err)
	}
	state.PublishIntent = true
	if err := journal.Save(state); err != nil {
		t.Fatal(err)
	}
	got, exists, err := journal.Load()
	if err != nil || !exists || !reflect.DeepEqual(got, state) {
		t.Fatalf("load = %#v, %v, %v", got, exists, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal permissions = %o", info.Mode().Perm())
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, exists, err = reopened.Load()
	if err != nil || !exists || !reflect.DeepEqual(got, state) {
		t.Fatalf("reopened journal lost state: %#v, %v", got, err)
	}
}

func TestJournalRejectsCorruptState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	journal, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := os.WriteFile(path, []byte(`{"Version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := journal.Load(); err == nil {
		t.Fatal("corrupt journal accepted")
	}
}
