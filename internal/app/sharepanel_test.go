package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/app"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
	"github.com/permgps/herdr-telegram-agents/internal/testkit"
)

func TestSharePanelExplicitConfirmationAndActorBinding(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1000, 0)
	store := testkit.NewMemSharingStore()
	sharing := app.NewSharing(ctx, store, nil)
	for _, id := range []int64{10, 11} {
		if _, err := sharing.Register(ctx, id, id, "Same <name>", "", now); err != nil {
			t.Fatal(err)
		}
	}
	tg := testkit.NewFakeTelegram(nil)
	topic, err := tg.CreateTopic(ctx, "agent", domain.StatusIdle)
	if err != nil {
		t.Fatal(err)
	}
	thread := topic.ThreadID
	insp := testkit.NewFakeInspector()
	insp.SetIdentity(domain.BotIdentity{ID: 42, HasTopicsEnabled: true}, nil)
	key := domain.Key{PaneID: "p", TerminalID: "t", SessionDigest: "digest"}
	current := key
	p := &app.SharePanel{Sharing: sharing, Capability: &app.PrivateCapability{Source: insp}, Telegram: tg, Private: tg, Config: domain.Config{ChatID: -100, OperatorIDs: []int64{7, 8}}, Agent: func(k domain.Key) (domain.Agent, bool) {
		return domain.Agent{Key: current, Name: "Agent"}, k == current
	}, KeyForThread: func(int) (domain.Key, bool) { return current, true }, Now: func() time.Time { return now }}
	if err := p.Open(ctx, 7, -100, thread, 99, "/share"); err != nil {
		t.Fatal(err)
	}
	posts := tg.Sent()
	if len(posts) != 1 || !strings.Contains(posts[0].Text, "Same &lt;name&gt;") {
		t.Fatal("directory not escaped")
	}
	message := 1000
	buttons := tg.Buttons(message)
	if len(buttons) < 2 || buttons[0].Text != "Select 10" || buttons[1].Text != "Select 11" {
		t.Fatalf("stable ID choices: %+v", buttons)
	}
	press := func(actor int64, data string) {
		t.Helper()
		if err := p.Press(ctx, domain.ButtonPressed{ChatID: -100, ThreadID: thread, MessageID: message, FromID: actor, CallbackID: "callback", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	press(8, buttons[0].Data)
	if !strings.Contains(tg.Text(message), "Recipients") {
		t.Fatal("another operator used panel")
	}
	press(7, buttons[0].Data)
	if !strings.Contains(tg.Text(message), "<b>read</b>") {
		t.Fatal("default not read")
	}
	choose := func(label string) {
		t.Helper()
		for _, b := range tg.Buttons(message) {
			if b.Text == label {
				if len(b.Data) > 64 {
					t.Fatal("callback too long")
				}
				press(7, b.Data)
				return
			}
		}
		t.Fatalf("missing button %s", label)
	}
	choose("Review grant")
	st, _ := sharing.Snapshot()
	if len(st.Grants) != 0 {
		t.Fatal("grant created before confirmation")
	}
	choose("Confirm grant")
	st, _ = sharing.Snapshot()
	if len(st.Grants) != 1 {
		t.Fatal("grant not persisted")
	}
	for _, g := range st.Grants {
		if g.RecipientID != 10 || g.Role != domain.ShareRead || !g.ExpiresAt.IsZero() {
			t.Fatalf("wrong grant: %+v", g)
		}
	}
	if err := p.Open(ctx, 7, 7, 3, 99, "/share"); err == nil {
		t.Fatal("operator private chat became admin")
	}
}

func TestOwnerRevokePublishesBeforeBridgeRendering(t *testing.T) {
	f := newPrivateFixture(t, domain.ShareControl)
	ctx := context.Background()
	topic, err := f.tg.CreateTopic(ctx, "owner", domain.StatusIdle)
	if err != nil {
		t.Fatal(err)
	}
	panel := &app.SharePanel{Sharing: f.s, Telegram: f.tg, Private: f.tg, Config: domain.Config{ChatID: -100, OperatorIDs: []int64{7}}, Agent: f.s.Agent, KeyForThread: func(int) (domain.Key, bool) { return f.g.Key, true }, Now: f.s.Now}
	if err := panel.Open(ctx, 7, -100, topic.ThreadID, 0, "/shares"); err != nil {
		t.Fatal(err)
	}
	var manage string
	for _, b := range f.tg.Buttons(1000) {
		if strings.HasPrefix(b.Text, "Manage ") {
			manage = b.Data
		}
	}
	e := domain.ButtonPressed{ChatID: -100, ThreadID: topic.ThreadID, MessageID: 1000, FromID: 7, CallbackID: "cb", Data: manage}
	if err := panel.Press(ctx, e); err != nil {
		t.Fatal(err)
	}
	for _, b := range f.tg.Buttons(1000) {
		if b.Text == "revoke" {
			e.Data = b.Data
		}
	}
	if e.Data == manage {
		t.Fatal("revoke button absent")
	}
	wrong := e
	wrong.ChatID = 7
	panel.DenyImmediately(ctx, wrong)
	_, release, decision := f.s.Begin(ctx, f.origin, domain.SharePrompt)
	if !decision.Allowed {
		t.Fatal("private surface administered grant")
	}
	release()
	panel.DenyImmediately(ctx, e)
	if _, _, decision := f.s.Begin(ctx, f.origin, domain.SharePrompt); decision.Allowed {
		t.Fatal("revoke waits for queued UI rendering")
	}
	persisted, err := f.store.Load(ctx)
	if err != nil || persisted.Grants[f.g.ID].State != domain.GrantRevoked {
		t.Fatal("revocation not durable")
	}
	if err := panel.Press(ctx, e); err != nil {
		t.Fatal(err)
	}
}
