package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// PrivateCapability checks getMe on each owner activation attempt. Cached
// observations serve diagnostics only and never authorize a grant.
type PrivateCapability struct {
	Source domain.BotIdentitySource
	Log    *slog.Logger
	mu     sync.Mutex
	last   domain.BotIdentity
}

var ErrPrivateTopicsDisabled = errors.New("enable Topics for this bot in BotFather before sharing")

func (p *PrivateCapability) Check(ctx context.Context) (domain.BotIdentity, error) {
	id, err := p.Source.Identity(ctx)
	log := p.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	p.mu.Lock()
	previous := p.last
	p.last = id
	p.mu.Unlock()
	if err != nil {
		log.Warn("private sharing capability unavailable")
		return domain.BotIdentity{}, err
	}
	if id != previous {
		log.Info("private sharing capability changed", slog.Int64("bot_id", id.ID), slog.Bool("enabled", id.HasTopicsEnabled))
	}
	if !id.PrivateTopicsReady() {
		return id, ErrPrivateTopicsDisabled
	}
	return id, nil
}
