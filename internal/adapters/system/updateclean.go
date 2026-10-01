package system

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// CleanUpdateArtifacts removes the staged update-worker-<job> binaries and
// update-backups/<job> directories of every job except keep, and returns
// how many went. The caller makes sure no update worker is running.
func CleanUpdateArtifacts(stateDir, keep string, log *slog.Logger) int {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	removed := 0
	remove := func(path string) {
		if err := os.RemoveAll(path); err != nil {
			log.Warn("update artifact not removed", slog.String("path", path), slog.String("err", err.Error()))
			return
		}
		removed++
		log.Info("[FIX] update artifact removed", slog.String("path", path))
	}
	if entries, err := os.ReadDir(stateDir); err == nil {
		for _, e := range entries {
			id, ok := strings.CutPrefix(e.Name(), "update-worker-")
			if !ok || e.IsDir() || strings.TrimSuffix(id, ".exe") == keep {
				continue
			}
			remove(filepath.Join(stateDir, e.Name()))
		}
	}
	backups := filepath.Join(stateDir, "update-backups")
	if entries, err := os.ReadDir(backups); err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == keep {
				continue
			}
			remove(filepath.Join(backups, e.Name()))
		}
	}
	return removed
}
