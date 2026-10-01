package state_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/adapters/state"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

func TestInboxSaveCreatesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	in := state.NewInbox(dir, nil)
	ctx := context.Background()
	path, err := in.Save(ctx, "20260906-120000-42-photo.jpg", []byte("jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || filepath.Dir(path) != filepath.Join(dir, state.InboxDirName) {
		t.Fatalf("path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "jpeg" {
		t.Fatalf("content = %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %o", info.Mode().Perm())
		}
		if info, _ := os.Stat(in.Dir()); info.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %o", info.Mode().Perm())
		}
	}
	if entries, _ := os.ReadDir(in.Dir()); len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestInboxSaveUniqueNames(t *testing.T) {
	in := state.NewInbox(t.TempDir(), nil)
	ctx := context.Background()
	first, _ := in.Save(ctx, "a.txt", []byte("1"))
	second, err := in.Save(ctx, "a.txt", []byte("2"))
	if err != nil {
		t.Fatal(err)
	}
	third, _ := in.Save(ctx, "a.txt", []byte("3"))
	if filepath.Base(first) != "a.txt" || filepath.Base(second) != "a-2.txt" || filepath.Base(third) != "a-3.txt" {
		t.Fatalf("names = %q %q %q", first, second, third)
	}
	if data, _ := os.ReadFile(first); string(data) != "1" {
		t.Fatalf("first file overwritten: %q", data)
	}
}

func TestInboxSaveRefusesUnsafeName(t *testing.T) {
	in := state.NewInbox(t.TempDir(), nil)
	for _, name := range []string{"", "..", "../x", "a/b", `a\b`, "/etc/passwd"} {
		if _, err := in.Save(context.Background(), name, []byte("x")); err == nil {
			t.Errorf("Save(%q) accepted", name)
		}
	}
}

func TestInboxSweepByAge(t *testing.T) {
	dir := t.TempDir()
	in := state.NewInbox(dir, nil)
	ctx := context.Background()
	old1, _ := in.Save(ctx, "old1.txt", []byte("x"))
	old2, _ := in.Save(ctx, "old2.txt", []byte("x"))
	fresh, _ := in.Save(ctx, "fresh.txt", []byte("x"))
	past := time.Now().Add(-10 * 24 * time.Hour)
	for _, p := range []string{old1, old2} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(in.Dir(), "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	n, err := in.Sweep(ctx, 7*24*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh file deleted")
	}
	if _, err := os.Stat(old1); !os.IsNotExist(err) {
		t.Fatal("old file kept")
	}
	if _, err := os.Stat(filepath.Join(in.Dir(), "sub")); err != nil {
		t.Fatal("subdirectory removed")
	}
}

func TestInboxSweepEmptyDir(t *testing.T) {
	in := state.NewInbox(t.TempDir(), nil)
	n, err := in.Sweep(context.Background(), time.Hour)
	if err != nil || n != 0 {
		t.Fatalf("Sweep on a missing inbox = %d, %v", n, err)
	}
}

// TestInboxTotalQuota: attachments stay within a total size. When a new
// file would go over, the oldest files go first; a file bigger than the
// whole quota is refused.
func TestInboxTotalQuota(t *testing.T) {
	ctx := context.Background()
	in := state.NewInbox(t.TempDir(), nil)
	in.MaxTotal = func() int64 { return 25 }
	old, err := in.Save(ctx, "old.bin", make([]byte, 10))
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	mid, err := in.Save(ctx, "mid.bin", make([]byte, 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Save(ctx, "new.bin", make([]byte, 10)); err != nil {
		t.Fatalf("save over quota: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("oldest file kept: %v", err)
	}
	if _, err := os.Stat(mid); err != nil {
		t.Fatalf("newer file removed: %v", err)
	}
	if _, err := in.Save(ctx, "huge.bin", make([]byte, 26)); !errors.Is(err, domain.ErrFileTooBig) {
		t.Fatalf("file over the whole quota = %v", err)
	}
}
