package system

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFetchTagUsesRemoteTag: the update target is the tag as published on
// origin. A fresh clone has no local tag, and a local tag of the same name
// that points elsewhere must not be trusted.
func TestFetchTagUsesRemoteTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	base := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1", "HOME="+base)
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	upstream := filepath.Join(base, "upstream")
	if err := os.Mkdir(upstream, 0o700); err != nil {
		t.Fatal(err)
	}
	git(upstream, "init", "-q", "-b", "main")
	git(upstream, "commit", "-q", "--allow-empty", "-m", "one")
	clone := filepath.Join(base, "clone")
	git(base, "clone", "-q", "--no-tags", upstream, clone)
	git(upstream, "commit", "-q", "--allow-empty", "-m", "two")
	git(upstream, "tag", "v1.2.0")
	want := git(upstream, "rev-parse", "HEAD")

	got, err := fetchTag(context.Background(), clone, "v1.2.0")
	if err != nil || got != want {
		t.Fatalf("fresh clone: fetchTag = %q, %v; want %q", got, err, want)
	}

	// A local tag that disagrees with origin fails closed.
	other := filepath.Join(base, "other")
	git(base, "clone", "-q", "--no-tags", upstream, other)
	git(other, "tag", "v1.2.0", "HEAD~1")
	if got, err := fetchTag(context.Background(), other, "v1.2.0"); err == nil {
		t.Fatalf("diverged local tag trusted: %q", got)
	}
}
