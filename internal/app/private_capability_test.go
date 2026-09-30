package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/app"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
	"github.com/permgps/herdr-telegram-agents/internal/testkit"
)

func TestPrivateCapabilityRefresh(t *testing.T) {
	insp := testkit.NewFakeInspector()
	p := &app.PrivateCapability{Source: insp}
	ctx := context.Background()
	if _, err := p.Check(ctx); !errors.Is(err, app.ErrPrivateTopicsDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	insp.SetIdentity(domain.BotIdentity{ID: 42, HasTopicsEnabled: true}, nil)
	if _, err := p.Check(ctx); err != nil {
		t.Fatal(err)
	}
	insp.SetIdentity(domain.BotIdentity{}, errors.New("offline"))
	if id, err := p.Check(ctx); err == nil || id.PrivateTopicsReady() {
		t.Fatal("failed probe authorized sharing")
	}
}
