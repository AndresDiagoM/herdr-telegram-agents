package domain_test

import (
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

func TestSharePermissionMatrix(t *testing.T) {
	read := domain.ShareGrant{Role: domain.ShareRead}
	control := domain.ShareGrant{Role: domain.ShareControl}
	for _, c := range []struct {
		text          string
		read, control bool
	}{
		{"/help", true, true}, {"/status", true, true}, {"/screen", true, true}, {"/screen 20", true, true}, {"/screen all", true, true},
		{"hello", false, true}, {"/keys enter", false, true}, {"/stop", false, true}, {"/interrupt", false, true},
		{"/clear", false, true}, {"/compact", false, true}, {"/usage", false, true}, {"/model", false, true},
		{"/git status", false, true}, {"/git diff", false, true}, {"/git log", false, true},
		{"/close", false, false}, {"/focus", false, false}, {"/new", false, false}, {"/options", false, false},
		{"/observers", false, false}, {"/away", false, false}, {"/here", false, false}, {"/invented", false, false},
	} {
		t.Run(c.text, func(t *testing.T) {
			a, ok := domain.ShareCommandAction(domain.ParseCommand(c.text, ""))
			if got := ok && domain.SharePermits(read, a); got != c.read {
				t.Errorf("read=%v", got)
			}
			if got := ok && domain.SharePermits(control, a); got != c.control {
				t.Errorf("control=%v", got)
			}
		})
	}
	for _, a := range []domain.ShareAction{domain.ShareAttachment, domain.ShareDialog, domain.ShareKeys, domain.ShareForward} {
		if domain.SharePermits(read, a) || !domain.SharePermits(control, a) {
			t.Errorf("callback/attachment %s", a)
		}
	}
	read.RepositoryRead = true
	read.CloseAgent = true
	read.LocalFocus = true
	if !domain.SharePermits(read, domain.ShareGit) || domain.SharePermits(read, domain.ShareClose) || domain.SharePermits(read, domain.ShareFocus) {
		t.Fatal("reader capability scope")
	}
	control.CloseAgent = true
	control.LocalFocus = true
	if !domain.SharePermits(control, domain.ShareClose) || !domain.SharePermits(control, domain.ShareFocus) || domain.SharePermits(control, "unknown") {
		t.Fatal("controller capability scope")
	}
}

func TestShareAuthorizationBindings(t *testing.T) {
	now := time.Unix(1000, 0)
	key := domain.Key{PaneID: "p", TerminalID: "t", SessionDigest: "digest"}
	base := domain.ShareGrant{ID: "g", RecipientID: 10, Key: key, Revision: 2, State: domain.GrantActive, Role: domain.ShareRead, ExpiresAt: now.Add(time.Hour), HistoryCursor: 12}
	mirror := domain.MirrorBinding{GrantID: "g", Address: domain.TopicAddress{ChatID: 10, ThreadID: 7}}
	origin := domain.ShareOrigin{ActorID: 10, Address: mirror.Address, GrantID: "g", Revision: 2, Key: key}
	for _, tc := range []struct {
		name   string
		change func(*domain.ShareGrant, *domain.MirrorBinding, *domain.ShareOrigin, *domain.Key)
		reason domain.AccessReason
	}{
		{"allowed", func(*domain.ShareGrant, *domain.MirrorBinding, *domain.ShareOrigin, *domain.Key) {}, domain.AccessAllowed},
		{"wrong actor", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) { o.ActorID++ }, domain.AccessActor},
		{"chat collision", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			o.Address.ChatID++
		}, domain.AccessDestination},
		{"topic", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			o.Address.ThreadID++
		}, domain.AccessDestination},
		{"revision", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			o.Revision--
		}, domain.AccessRevision},
		{"replacement", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			k.SessionDigest = "new"
		}, domain.AccessSession},
		{"expiry boundary", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			g.ExpiresAt = now
		}, domain.AccessExpired},
		{"unknown state", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			g.State = "future"
		}, domain.AccessInactive},
		{"unknown role", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			g.Role = "admin"
		}, domain.AccessInvalid},
		{"revoked", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			g.State = domain.GrantRevoked
		}, domain.AccessInactive},
		{"ambiguous create", func(g *domain.ShareGrant, m *domain.MirrorBinding, o *domain.ShareOrigin, k *domain.Key) {
			m.Creating = true
		}, domain.AccessDestination},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, m, o, k := base, mirror, origin, key
			tc.change(&g, &m, &o, &k)
			d := domain.AuthorizeShare(g, m, o, k, domain.ShareScreen, now)
			if d.Reason != tc.reason || d.Allowed != (tc.reason == domain.AccessAllowed) {
				t.Fatalf("decision=%+v", d)
			}
		})
	}
	base.Key.SessionDigest = ""
	origin.Key = base.Key
	if d := domain.AuthorizeShare(base, mirror, origin, base.Key, domain.ShareScreen, now); d.Allowed {
		t.Fatal("incomplete identity survived restart")
	}
	base.ProcessID = "process"
	origin.ProcessID = "process"
	if d := domain.AuthorizeShare(base, mirror, origin, base.Key, domain.ShareScreen, now); !d.Allowed {
		t.Fatal(d)
	}
}

func TestShareGrantRevision(t *testing.T) {
	g := domain.ShareGrant{State: domain.GrantPending, Revision: 1}
	for _, state := range []domain.GrantState{domain.GrantActive, domain.GrantSuspended, domain.GrantActive, domain.GrantRevoked} {
		old := g.Revision
		var err error
		g, err = g.Transition(state)
		if err != nil || g.Revision != old+1 {
			t.Fatalf("transition %s: %+v %v", state, g, err)
		}
	}
	if _, err := g.Transition(domain.GrantActive); err == nil {
		t.Fatal("revoked grant activated without reapproval")
	}
}
