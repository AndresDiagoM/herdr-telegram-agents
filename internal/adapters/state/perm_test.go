package state_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/adapters/state"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// TestStateFilesArePrivate: the mapping holds working directories and topic
// names, the pid file the daemon's pid; the state directory is created for
// the user alone.
func TestStateFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "state")
	m := state.NewMappingStore(dir, nil)
	if err := m.Save(context.Background(), domain.NewMapping(-100)); err != nil {
		t.Fatal(err)
	}
	p := state.NewPidFile(filepath.Join(t.TempDir(), "pid"), nil, nil)
	if err := p.Acquire(42); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		dir:                                0o700,
		filepath.Join(dir, "mapping.json"): 0o600,
		p.Path():                           0o600,
		filepath.Dir(p.Path()):             0o700,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want)
		}
	}
}
