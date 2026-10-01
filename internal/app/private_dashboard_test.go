package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/app"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

func TestPrivateOverviewScopedAndRevoked(t *testing.T) {
	f := newPrivateFixture(t, domain.ShareRead)
	ctx := context.Background()
	r := &app.PrivateReconciler{Sharing: f.s, Telegram: f.tg, Agent: f.s.Agent, Now: f.s.Now}
	d := app.NewPrivateDashboard(f.p, r, "example_bot", nil)
	f.p.Dashboard = d
	f.p.Overview = d.Handle
	if err := d.Refresh(ctx, 10, true); err != nil {
		t.Fatal(err)
	}
	st, _ := f.s.Snapshot()
	message := st.Dashboards[10]
	text := f.tg.Destination(10).Text(message.MessageID)
	if !strings.Contains(text, "agent") || strings.Contains(text, "t.me/c/") {
		t.Fatalf("overview: %s", text)
	}
	if err := f.s.ChangeState(ctx, f.g.ID, f.g.Revision, domain.GrantRevoked, f.now); err != nil {
		t.Fatal(err)
	}
	if err := d.Refresh(ctx, 10, false); err != nil {
		t.Fatal(err)
	}
	text = f.tg.Destination(10).Text(message.MessageID)
	if strings.Contains(text, "mirror_") || !strings.Contains(text, "No active grants") {
		t.Fatal("revoked agent still listed")
	}
	if len(f.tg.Destination(11).Sent()) != 0 {
		t.Fatal("overview crossed recipients")
	}
}

func TestPrivateAliasDoesNotRenameOwner(t *testing.T) {
	f := newPrivateFixture(t, domain.ShareRead)
	ctx := context.Background()
	r := &app.PrivateReconciler{Sharing: f.s, Telegram: f.tg, Agent: f.s.Agent, Now: f.s.Now}
	d := app.NewPrivateDashboard(f.p, r, "bot", nil)
	handled, err := d.Local(ctx, domain.PrivateMessage{Contact: domain.PrivateContact{ActorID: 10}, Address: f.origin.Address, Text: "/alias My agent"})
	if !handled || err != nil {
		t.Fatal(err)
	}
	st, _ := f.s.Snapshot()
	if st.Mirrors[f.g.ID].Preferences.Alias != "My agent" {
		t.Fatal("alias not saved")
	}
	if len(f.h.Renames()) != 0 {
		t.Fatal("private alias renamed owner agent")
	}
}

type countingMenus struct{ calls int }

func (m *countingMenus) RegisterPrivateCommands(context.Context, int64, []string) error {
	m.calls++
	return nil
}

// TestPrivateDashboardRegistersMenuOnce: every private message refreshes the
// overview; the chat menu (setMyCommands) is published only when it changes.
func TestPrivateDashboardRegistersMenuOnce(t *testing.T) {
	f := newPrivateFixture(t, domain.ShareRead)
	r := &app.PrivateReconciler{Sharing: f.s, Telegram: f.tg, Agent: f.s.Agent, Now: f.s.Now}
	menus := &countingMenus{}
	d := app.NewPrivateDashboard(f.p, r, "bot", menus)
	for i := 0; i < 3; i++ {
		if err := d.Refresh(context.Background(), 10, true); err != nil {
			t.Fatal(err)
		}
	}
	if menus.calls != 1 {
		t.Fatalf("setMyCommands calls = %d", menus.calls)
	}
}

func TestSharingGrantee(t *testing.T) {
	f := newPrivateFixture(t, domain.ShareRead)
	if !f.s.Grantee(10) || f.s.Grantee(11) {
		t.Fatal("grantee lookup wrong")
	}
	if err := f.s.ChangeState(context.Background(), f.g.ID, f.g.Revision, domain.GrantRevoked, f.now); err != nil {
		t.Fatal(err)
	}
	if f.s.Grantee(10) {
		t.Fatal("revoked grant still trusted")
	}
}
