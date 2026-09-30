package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/app"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
	"github.com/permgps/herdr-telegram-agents/internal/testkit"
)

func TestPrivateMirrorIntentAndLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	store := testkit.NewMemSharingStore()
	sharing := app.NewSharing(ctx, store, nil)
	sharing.BotID = 42
	sharing.Now = func() time.Time { return now }
	a := domain.Agent{Key: domain.Key{PaneID: "p", TerminalID: "t", SessionDigest: "digest"}, Name: "Agent", Status: domain.StatusIdle}
	sharing.Agent = func(k domain.Key) (domain.Agent, bool) { return a, k == a.Key }
	if _, err := sharing.Register(ctx, 10, 10, "name", "", now); err != nil {
		t.Fatal(err)
	}
	g, err := sharing.Grant(ctx, app.GrantRequest{RecipientID: 10, Key: a.Key, Role: domain.ShareRead, Bot: domain.BotIdentity{ID: 42, HasTopicsEnabled: true}}, now)
	if err != nil {
		t.Fatal(err)
	}
	tg := testkit.NewFakeTelegram(nil)
	r := &app.PrivateReconciler{Sharing: sharing, Telegram: tg, Agent: sharing.Agent, Now: sharing.Now}
	if err := r.Grant(ctx, g); err != nil {
		t.Fatal(err)
	}
	st, _ := sharing.Snapshot()
	g = st.Grants[g.ID]
	thread := st.Mirrors[g.ID].Address.ThreadID
	if err := r.Grant(ctx, g); err != nil {
		t.Fatal(err)
	}
	creates := 0
	for _, call := range tg.Destination(10).Calls() {
		if len(call) > 7 && call[:7] == "create:" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("created %d mirrors", creates)
	}
	posts := tg.Destination(10).Sent()
	if len(posts) == 0 || posts[0].Code {
		t.Fatal("missing neutral initial card")
	}
	if err := r.Observe(ctx, app.AgentEvent{Kind: app.AgentGone, Agent: a}); err != nil {
		t.Fatal(err)
	}
	st, _ = sharing.Snapshot()
	if st.Grants[g.ID].State != domain.GrantSuspended {
		t.Fatal("exit still active")
	}
	a.TerminalID = "resumed"
	if err := r.Observe(ctx, app.AgentEvent{Kind: app.AgentAppeared, Agent: a}); err != nil {
		t.Fatal(err)
	}
	st, _ = sharing.Snapshot()
	if st.Grants[g.ID].State != domain.GrantActive || st.Mirrors[g.ID].Address.ThreadID != thread {
		t.Fatal("same session not reused")
	}
	a.SessionDigest = "replacement"
	if err := r.Observe(ctx, app.AgentEvent{Kind: app.AgentAppeared, Agent: a}); err != nil {
		t.Fatal(err)
	}
	st, _ = sharing.Snapshot()
	if st.Grants[g.ID].State != domain.GrantSuspended {
		t.Fatal("replacement kept access")
	}
}

func TestPrivateMirrorAmbiguousCreateNotRetried(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	s := app.NewSharing(ctx, testkit.NewMemSharingStore(), nil)
	s.BotID = 42
	s.Now = func() time.Time { return now }
	a := domain.Agent{Key: domain.Key{PaneID: "p", TerminalID: "t", SessionDigest: "digest"}}
	s.Agent = func(domain.Key) (domain.Agent, bool) { return a, true }
	_, _ = s.Register(ctx, 10, 10, "name", "", now)
	g, err := s.Grant(ctx, app.GrantRequest{RecipientID: 10, Key: a.Key, Role: domain.ShareRead, Bot: domain.BotIdentity{ID: 42, HasTopicsEnabled: true}}, now)
	if err != nil {
		t.Fatal(err)
	}
	tg := testkit.NewFakeTelegram(nil)
	tg.Destination(10).FailNext("create", errors.New("unknown network outcome"))
	r := &app.PrivateReconciler{Sharing: s, Telegram: tg, Agent: s.Agent, Now: s.Now}
	if err := r.Grant(ctx, g); err == nil {
		t.Fatal("ambiguous create accepted")
	}
	st, _ := s.Snapshot()
	if st.Grants[g.ID].State != domain.GrantNeedsRepair {
		t.Fatal("repair state missing")
	}
	if err := r.Grant(ctx, g); err == nil {
		t.Fatal("stale create retried")
	}
}
