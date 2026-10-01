package system

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCleanUpdateArtifacts: each update stages a worker binary and a
// backup; only the ones of a job that may still need them stay.
func TestCleanUpdateArtifacts(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"update-worker-old", "update-worker-keep.exe", "update-backups/old/herdr-tg", "update-backups/keep/herdr-tg", "daemon.log"} {
		path := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n := CleanUpdateArtifacts(dir, "keep", nil); n != 2 {
		t.Fatalf("removed = %d", n)
	}
	for p, want := range map[string]bool{"update-worker-old": false, "update-backups/old": false, "update-worker-keep.exe": true, "update-backups/keep/herdr-tg": true, "daemon.log": true} {
		_, err := os.Stat(filepath.Join(dir, p))
		if (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", p, err == nil, want)
		}
	}
}
