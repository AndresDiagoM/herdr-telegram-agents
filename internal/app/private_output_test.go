package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/app"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

func TestPrivateFanoutAndReadKeyboardIsolation(t *testing.T) {
	f := newPrivateFixture(t, domain.ShareControl)
	ctx := context.Background()
	_, _ = f.s.Register(ctx, 11, 11, "reader", "", f.now)
	g, err := f.s.Grant(ctx, app.GrantRequest{RecipientID: 11, Key: f.g.Key, Role: domain.ShareRead, Bot: domain.BotIdentity{ID: 42, HasTopicsEnabled: true}}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	r := &app.PrivateReconciler{Sharing: f.s, Telegram: f.tg, Agent: f.s.Agent, Now: f.s.Now}
	if err := r.Grant(ctx, g); err != nil {
		t.Fatal(err)
	}
	f.h.SetScreen("p", "1. Allow\n2. Deny")
	p := &app.PrivateOutput{Control: f.p, Automatic: func() bool { return true }}
	f.p.Output = p
	a, _ := f.s.Agent(f.g.Key)
	a.Status = domain.StatusBlocked
	p.Observe(app.AgentEvent{Kind: app.AgentChanged, Agent: a})
	f.now = f.now.Add(3 * time.Second)
	if err := p.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	control, read := f.tg.Destination(10).Sent(), f.tg.Destination(11).Sent()
	if len(control) != 2 || len(read) != 2 {
		t.Fatalf("fanout posts %d/%d", len(control), len(read))
	}
	if len(control[1].Buttons) != 2 || len(read[1].Buttons) != 0 {
		t.Fatal("reader received action keyboard")
	}
	if len(f.h.Reads()) != 1 {
		t.Fatal("capture multiplied by mirror count")
	}
	if !control[1].Notify || !read[1].Notify {
		t.Fatal("blocked alert silent by default")
	}
	if strings.Contains(read[1].Text, "transcript") {
		t.Fatal("unverified source")
	}
	p.Observe(app.AgentEvent{Kind: app.AgentChanged, Agent: a})
	f.now = f.now.Add(3 * time.Second)
	_ = p.Tick(ctx)
	if len(f.tg.Destination(10).Sent()) != 2 {
		t.Fatal("dedup failed")
	}
}

func TestTwoControllersCannotSubmitSameDialog(t *testing.T) {
	f := newPrivateFixture(t, domain.ShareControl)
	ctx := context.Background()
	_, _ = f.s.Register(ctx, 11, 11, "controller", "", f.now)
	g, err := f.s.Grant(ctx, app.GrantRequest{RecipientID: 11, Key: f.g.Key, Role: domain.ShareControl, Bot: domain.BotIdentity{ID: 42, HasTopicsEnabled: true}}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	r := &app.PrivateReconciler{Sharing: f.s, Telegram: f.tg, Agent: f.s.Agent, Now: f.s.Now}
	if err := r.Grant(ctx, g); err != nil {
		t.Fatal(err)
	}
	f.h.SetScreen("p", "1. Allow\n2. Deny")
	output := &app.PrivateOutput{Control: f.p}
	f.p.Output = output
	a, _ := f.s.Agent(f.g.Key)
	a.Status = domain.StatusBlocked
	output.Observe(app.AgentEvent{Kind: app.AgentChanged, Agent: a})
	f.now = f.now.Add(3 * time.Second)
	if err := output.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	first := f.tg.Destination(10).Sent()[1].Buttons[0]
	second := f.tg.Destination(11).Sent()[1].Buttons[0]
	other, _ := f.s.Origin(g.ID)
	for _, e := range []domain.PrivateMessage{
		{Contact: domain.PrivateContact{ActorID: 10}, Address: f.origin.Address, MessageID: 1001, CallbackID: "first", CallbackData: first.Data},
		{Contact: domain.PrivateContact{ActorID: 11}, Address: other.Address, MessageID: 1001, CallbackID: "second", CallbackData: second.Data},
	} {
		if err := f.p.Handle(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.h.Keys()) != 1 {
		t.Fatal("two controllers submitted the same dialog")
	}
}
