package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// PrivateReconciler creates only after durable intent and never closes private
// topics. Unknown creation results require an explicit owner repair.
type PrivateReconciler struct {
	pending   map[domain.Key]AgentEvent
	after     map[domain.Key]string
	Automatic func() bool
	Sharing   *Sharing
	Telegram  domain.DestinationTelegram
	Agent     func(domain.Key) (domain.Agent, bool)
	Now       func() time.Time
	Log       *slog.Logger
	last      map[string]string
}

func (r *PrivateReconciler) Grant(ctx context.Context, g domain.ShareGrant) error {
	a, ok := r.Agent(g.Key)
	if !ok || a.Key != g.Key || a.Status == domain.StatusExited {
		return errors.New("agent session no longer live")
	}
	if g.State == domain.GrantActive {
		return r.card(ctx, g.ID)
	}
	m, err := r.Sharing.PrepareMirror(ctx, g.ID, g.Revision, r.Now())
	if err != nil {
		return err
	}
	topic := domain.Topic{ThreadID: m.Address.ThreadID}
	if topic.ThreadID == 0 {
		topic, err = r.Telegram.CreateTopicAt(ctx, m.Address.ChatID, a.Label(), a.Status, r.Sharing.BindingGuard(g.ID, g.Revision))
		if err != nil {
			_ = r.Sharing.ChangeState(ctx, g.ID, g.Revision, domain.GrantNeedsRepair, r.Now())
			if r.Log != nil {
				r.Log.Warn("private topic creation needs repair", slog.String("grant_id", g.ID))
			}
			return errors.New("private topic creation failed or outcome unknown; repair required")
		}
	}
	if err := r.Sharing.ActivateMirror(ctx, g.ID, g.Revision, topic, r.Now()); err != nil {
		return err
	}
	if r.Log != nil {
		r.Log.Info("private mirror activated", slog.String("grant_id", g.ID), slog.Int64("recipient_id", g.RecipientID))
	}
	return r.card(ctx, g.ID)
}

func (r *PrivateReconciler) card(ctx context.Context, id string) error {
	st, ok := r.Sharing.Snapshot()
	if !ok {
		return ErrSharingUnavailable
	}
	g := st.Grants[id]
	o, ok := r.Sharing.Origin(id)
	if !ok {
		return ErrSharingUnavailable
	}
	text := fmt.Sprintf("Shared agent access: %s. Expires: %s.\nAll new output from this session is shared, including results of owner prompts. Existing history is not replayed. Screen requests may reveal older content still visible in this session.\nControl permits use of this agent's existing filesystem and tools. The owner retains control. Revocation cannot recall running work or dispatched messages.", g.Role, expiryText(g.ExpiresAt))
	_, err := r.Telegram.SendAt(ctx, o.Address, domain.Outgoing{Text: text}, r.Sharing.Guard(o, domain.ShareOutput))
	return err
}

func (r *PrivateReconciler) Observe(ctx context.Context, e AgentEvent) error {
	if r.pending == nil {
		r.pending = map[domain.Key]AgentEvent{}
		r.after = map[domain.Key]string{}
	}
	r.pending[e.Agent.Key] = e
	delete(r.after, e.Agent.Key)
	return r.flushEvent(ctx, e)
}

func (r *PrivateReconciler) Flush(ctx context.Context) error {
	for _, e := range r.pending {
		return r.flushEvent(ctx, e)
	}
	return nil
}

func (r *PrivateReconciler) flushEvent(ctx context.Context, e AgentEvent) error {
	if err := r.Sharing.Lifecycle(ctx, e, r.Now()); err != nil {
		return err
	}
	st, ok := r.Sharing.Snapshot()
	if !ok || !st.MatchesBot(domain.BotIdentity{ID: r.Sharing.BotID}) || (r.Automatic != nil && !r.Automatic()) {
		delete(r.pending, e.Agent.Key)
		delete(r.after, e.Agent.Key)
		return nil
	}
	ids := make([]string, 0, len(st.Grants))
	for id := range st.Grants {
		if id > r.after[e.Agent.Key] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	calls := 0
	for _, id := range ids {
		if calls >= 4 {
			return nil
		}
		r.after[e.Agent.Key] = id
		g := st.Grants[id]
		if g.Key.PaneID != e.Agent.PaneID || (g.State != domain.GrantActive && g.SuspendReason != "exited") {
			continue
		}
		m := st.Mirrors[g.ID]
		if e.Agent.Key != g.Key || e.Kind == AgentGone {

			status := domain.StatusExited
			if !m.Preferences.Paused {
				calls++
				_ = r.Telegram.EditTopicAt(ctx, m.Address, domain.TopicPatch{Status: &status}, r.Sharing.BindingGuard(g.ID, g.Revision))
			}
			continue
		}
		if m.Preferences.Paused {
			continue
		}
		name := e.Agent.Label()
		if m.Preferences.Alias != "" {
			name = m.Preferences.Alias
		}
		if r.last == nil {
			r.last = map[string]string{}
		}
		signature := fmt.Sprintf("%d:%s:%s", g.Revision, name, e.Agent.Status)
		if r.last[g.ID] == signature {
			continue
		}
		o, _ := r.Sharing.Origin(g.ID)
		o.Revision, o.Key = g.Revision, g.Key
		calls++
		if err := r.Telegram.EditTopicAt(ctx, m.Address, domain.TopicPatch{Name: &name, Status: &e.Agent.Status}, r.Sharing.Guard(o, domain.ShareOutput)); err != nil {
			if errors.Is(err, domain.ErrTopicGone) {
				next, saveErr := r.Sharing.MissingMirror(ctx, g.ID, g.Revision, r.Now())
				if saveErr != nil {
					return saveErr
				}
				return r.Grant(ctx, next)
			}
			if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrRecipientUnavailable) {
				_ = r.Sharing.Reachability(ctx, g.RecipientID, true, r.Now())
			}
			continue
		}
		r.last[g.ID] = signature
	}
	delete(r.pending, e.Agent.Key)
	delete(r.after, e.Agent.Key)
	return nil
}

// Sweep applies the existing exited-topic retention option without group
// administrator checks. Grants remain as tombstones after topic deletion.
func (r *PrivateReconciler) Sweep(ctx context.Context, maxAge time.Duration) error {
	if maxAge <= 0 || (r.Automatic != nil && !r.Automatic()) {
		return nil
	}
	st, ok := r.Sharing.Snapshot()
	if !ok {
		return nil
	}
	count := 0
	for id, g := range st.Grants {
		if (g.SuspendReason != "exited" && g.SuspendReason != "retention") || g.ExitedAt.IsZero() || r.Now().Sub(g.ExitedAt) < maxAge {
			continue
		}
		m, exists := st.Mirrors[id]
		if !exists || m.Address.ThreadID == 0 {
			continue
		}
		if count >= 4 {
			break
		}
		count++
		if err := r.Sharing.RetireMirror(ctx, id, g.Revision, r.Now()); err != nil {
			return err
		}
		current, _ := r.Sharing.Snapshot()
		g = current.Grants[id]
		err := r.Telegram.DeleteTopicAt(ctx, m.Address, r.Sharing.BindingGuard(id, g.Revision))
		if err == nil || errors.Is(err, domain.ErrTopicGone) {
			if err := r.Sharing.ForgetMirror(ctx, id, g.Revision, r.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}
