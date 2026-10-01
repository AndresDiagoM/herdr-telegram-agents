package system

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateLockContentionAndStaleOwner(t *testing.T) {
	dir := t.TempDir()
	first := NewUpdateLock(dir, func(int) bool { return true }, nil)
	if err := first.Acquire(); err != nil {
		t.Fatal(err)
	}
	second := NewUpdateLock(dir, func(int) bool { return true }, nil)
	if err := second.Acquire(); !errors.Is(err, ErrUpdateLocked) {
		t.Fatalf("contention=%v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "update.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update.lock", "owner.json"), []byte(`{"pid":999999,"nonce":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := NewUpdateLock(dir, func(int) bool { return false }, nil)
	if err := stale.Acquire(); err != nil {
		t.Fatalf("stale recovery=%v", err)
	}
	if err := stale.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateLockConcurrentStaleRecovery: two workers find the same stale
// lock. The one that recovers second must see the first one's fresh lock,
// not move it away and take the lock as well.
func TestUpdateLockConcurrentStaleRecovery(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "update.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update.lock", "owner.json"), []byte(`{"pid":999999,"nonce":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	alive := func(pid int) bool { return pid == os.Getpid() }
	a := NewUpdateLock(dir, alive, nil)
	b := NewUpdateLock(dir, alive, nil)
	// B has read the stale owner; before it recovers, A recovers and wins.
	b.beforeRecover = func() {
		b.beforeRecover = nil
		if err := a.Acquire(); err != nil {
			t.Fatalf("A acquire: %v", err)
		}
	}
	if err := b.Acquire(); !errors.Is(err, ErrUpdateLocked) {
		t.Fatalf("B acquire over A's fresh lock = %v", err)
	}
	if err := a.Release(); err != nil {
		t.Fatalf("A lost its lock: %v", err)
	}
}
