package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
	"github.com/permgps/herdr-telegram-agents/internal/testkit"
)

// TestPrivateForbiddenIsNotFatal: a contact who blocks the bot answers every
// private reply with 403. That is one recipient's problem, never a reason to
// stop the daemon.
func TestPrivateForbiddenIsNotFatal(t *testing.T) {
	ctx := context.Background()
	sharing := NewSharing(ctx, testkit.NewMemSharingStore(), nil)
	r := newRunningBridge(t, func(f *bridgeFixture, b *Bridge) {
		sharing.Now = f.clock.Now
		if _, err := sharing.Register(ctx, 10, 10, "stranger", "", f.clock.Now()); err != nil {
			t.Fatal(err)
		}
		private := PrivateRedactor{DestinationTelegram: f.tg, Redactor: domain.NewRedactor(), Unavailable: func(ctx context.Context, chat int64) {
			_ = sharing.Reachability(ctx, chat, true, f.clock.Now())
		}}
		b.SetPrivateControl(&PrivateControl{Sharing: sharing, Telegram: private, Transport: f.tg, Herdr: f.herdr, Agent: func(domain.Key) (domain.Agent, bool) { return domain.Agent{}, false }, Now: f.clock.Now})
	})
	r.tg.Destination(10).FailNext("send", domain.ErrForbidden)
	r.bridge.Submit(domain.PrivateMessage{Contact: domain.PrivateContact{ActorID: 10, ChatID: 10}, Address: domain.TopicAddress{ChatID: 10, ThreadID: 5}, MessageID: 1, Text: "hello"})
	waitUntil(t, "private message handled", func() bool { return r.bridge.Handled() == 1 })
	select {
	case err := <-r.bridge.Fatal():
		t.Fatalf("private 403 reported fatal: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	st, _ := sharing.Snapshot()
	if !st.Recipients[10].Unavailable {
		t.Fatal("recipient not marked unavailable")
	}
}

// TestPrivateRedactorMapsForbidden covers every private operation, including
// documents (/git output), edits and deletes.
func TestPrivateRedactorMapsForbidden(t *testing.T) {
	ctx := context.Background()
	tg := testkit.NewFakeTelegram(nil)
	var marked []int64
	p := PrivateRedactor{DestinationTelegram: tg, Unavailable: func(_ context.Context, chat int64) { marked = append(marked, chat) }}
	a := domain.TopicAddress{ChatID: 10, ThreadID: 3}
	m := domain.MessageAddress{ChatID: 10, MessageID: 1}
	calls := map[string]func() error{
		"document":  func() error { return p.DocumentAt(ctx, a, domain.Document{Name: "git.txt", Data: []byte("x")}, nil) },
		"edittext":  func() error { return p.EditTextAt(ctx, m, "x", false, nil, nil) },
		"deletemsg": func() error { return p.DeleteMessageAt(ctx, m, nil) },
		"pin":       func() error { return p.PinAt(ctx, m, nil) },
	}
	for method, call := range calls {
		tg.Destination(10).FailNext(method, domain.ErrForbidden)
		err := call()
		if !errors.Is(err, domain.ErrRecipientUnavailable) || errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("%s: err = %v", method, err)
		}
		if isFatal(err) {
			t.Fatalf("%s: fatal", method)
		}
	}
	if len(marked) != len(calls) {
		t.Fatalf("marked = %v", marked)
	}
}
