package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/app"
	"github.com/permgps/herdr-telegram-agents/internal/testkit"
)

func TestSharingRegistrationPersistence(t *testing.T) {
	ctx := context.Background()
	store := testkit.NewMemSharingStore()
	s := app.NewSharing(ctx, store, nil)
	now := time.Unix(1000, 0)
	first, err := s.Register(ctx, 7, 7, "name", "", now)
	if !first || err != nil {
		t.Fatalf("first=%v err=%v", first, err)
	}
	st, _ := store.Load(ctx)
	revision := st.Revision
	if len(st.Recipients) != 1 {
		t.Fatal("first contact not durable")
	}
	first, err = s.Register(ctx, 7, 7, "new name", "handle", now.Add(time.Second))
	if first || err != nil {
		t.Fatal("refresh treated as first contact")
	}
	st, _ = store.Load(ctx)
	if st.Revision != revision || st.Recipients[7].Name != "name" {
		t.Fatal("refresh not coalesced")
	}
	if err := s.Flush(ctx, now.Add(31*time.Second), false); err != nil {
		t.Fatal(err)
	}
	st, _ = store.Load(ctx)
	if st.Recipients[7].Name != "new name" {
		t.Fatal("refresh not flushed")
	}
	store.Fail(errors.New("disk full"))
	if first, err := s.Register(ctx, 8, 8, "other", "", now); err == nil || first {
		t.Fatal("failed first contact acknowledged")
	}
	snapshot, _ := s.Snapshot()
	if _, ok := snapshot.Recipients[8]; ok {
		t.Fatal("failed contact retained as registered")
	}
}

func TestSharingCorruptionDisablesGuests(t *testing.T) {
	store := testkit.NewMemSharingStore()
	store.Fail(errors.New("corrupt"))
	s := app.NewSharing(context.Background(), store, nil)
	if _, ok := s.Snapshot(); ok {
		t.Fatal("corrupt policy enabled")
	}
	if _, err := s.Register(context.Background(), 7, 7, "name", "", time.Unix(1000, 0)); !errors.Is(err, app.ErrSharingUnavailable) {
		t.Fatal(err)
	}
}
