package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// SharePanel is confined to the bridge worker. Callback references carry no
// authority: every press checks actor, owner chat, message and current session.
type SharePanel struct {
	Sharing      *Sharing
	Capability   *PrivateCapability
	Telegram     domain.TelegramGateway
	Private      domain.DestinationTelegram
	Config       domain.Config
	Agent        func(domain.Key) (domain.Agent, bool)
	KeyForThread func(int) (domain.Key, bool)
	Now          func() time.Time
	OnGrant      func(context.Context, domain.ShareGrant) error
	refsMu       sync.Mutex
	refs         map[string]shareSelection
}

type shareSelection struct {
	actor           int64
	thread, message int
	key             domain.Key
	action, query   string
	page            int
	recipient       int64
	request         GrantRequest
	grant           string
	revision        uint64
	expires         time.Time
}

type shareChoice struct {
	label     string
	selection shareSelection
}

func (p *SharePanel) Open(ctx context.Context, actor, chat int64, thread, message int, text string) error {
	if chat != p.Config.ChatID || !p.Config.IsOperator(actor) {
		return errors.New("sharing administration requires owner group operator")
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	query := ""
	if len(fields) > 1 {
		query = strings.Join(fields[1:], " ")
	}
	v := shareSelection{actor: actor, thread: thread, query: query}
	if thread != 0 {
		var ok bool
		v.key, ok = p.KeyForThread(thread)
		if !ok {
			return errors.New("agent topic not found")
		}
	}
	if strings.HasPrefix(fields[0], "/shares") {
		return p.grants(ctx, v)
	}
	if thread == 0 {
		return p.directory(ctx, v, true)
	}
	if _, err := p.Capability.Check(ctx); err != nil {
		return p.show(ctx, v, "Sharing unavailable. Enable Topics in BotFather, then run /share again.", nil)
	}
	return p.directory(ctx, v, false)
}

func (p *SharePanel) show(ctx context.Context, v shareSelection, text string, choices []shareChoice) error {
	p.refsMu.Lock()
	if p.refs == nil {
		p.refs = map[string]shareSelection{}
	}
	for ref, old := range p.refs {
		if !p.Now().Before(old.expires) || (old.message == v.message && old.thread == v.thread && old.actor == v.actor) {
			delete(p.refs, ref)
		}
	}
	if len(p.refs)+len(choices) > 2048 {
		p.refsMu.Unlock()
		return errors.New("too many sharing panels; retry after older panels expire")
	}
	buttons := make([]domain.Button, 0, len(choices))
	refs := make([]string, 0, len(choices))
	for _, choice := range choices {
		ref := "sh:" + rand.Text()
		s := choice.selection
		s.actor = v.actor
		s.thread = v.thread
		s.message = v.message
		s.expires = p.Now().Add(10 * time.Minute)
		p.refs[ref] = s
		refs = append(refs, ref)
		buttons = append(buttons, domain.Button{Text: choice.label, Data: ref})
	}
	p.refsMu.Unlock()
	if v.message != 0 {
		return p.Telegram.EditText(ctx, v.message, text, true, buttons)
	}
	id, err := p.Telegram.Send(ctx, domain.Outgoing{ThreadID: v.thread, Text: text, HTML: true, Buttons: buttons})
	p.refsMu.Lock()
	defer p.refsMu.Unlock()
	for _, ref := range refs {
		s := p.refs[ref]
		s.message = id
		p.refs[ref] = s
		if err != nil {
			delete(p.refs, ref)
		}
	}
	return err
}

func (p *SharePanel) directory(ctx context.Context, v shareSelection, manage bool) error {
	st, ok := p.Sharing.Snapshot()
	if !ok {
		return ErrSharingUnavailable
	}
	var recipients []domain.Recipient
	query := strings.ToLower(v.query)
	hidden := strings.HasPrefix(query, "hidden")
	if hidden {
		query = strings.TrimSpace(strings.TrimPrefix(query, "hidden"))
	}
	for _, r := range st.Recipients {
		if r.Hidden != hidden {
			continue
		}
		hay := strings.ToLower(r.Name + " " + r.Username + " " + strconv.FormatInt(r.ID, 10))
		if query == "" || strings.Contains(hay, query) {
			recipients = append(recipients, r)
		}
	}
	sort.Slice(recipients, func(i, j int) bool {
		if recipients[i].LastSeen.Equal(recipients[j].LastSeen) {
			return recipients[i].ID < recipients[j].ID
		}
		return recipients[i].LastSeen.After(recipients[j].LastSeen)
	})
	pages := max(1, (len(recipients)+9)/10)
	v.page = min(max(v.page, 0), pages-1)
	body := fmt.Sprintf("Recipients · page %d/%d\nSearch: /share &lt;name, username or ID&gt;\n", v.page+1, pages)
	var choices []shareChoice
	for _, r := range recipients[min(v.page*10, len(recipients)):min((v.page+1)*10, len(recipients))] {
		status := "reachable"
		if r.Unavailable {
			status = "unavailable"
		}
		if r.Hidden {
			status += " · hidden"
		}
		for _, g := range st.Grants {
			if g.RecipientID == r.ID && g.Key == v.key && g.State == domain.GrantActive {
				status += " · " + string(g.Role)
			}
		}
		body += fmt.Sprintf("\n%s @%s · <code>%d</code> · %s", html.EscapeString(r.Name), html.EscapeString(r.Username), r.ID, status)
		next := v
		next.recipient = r.ID
		next.action = "select"
		label := fmt.Sprintf("Select %d", r.ID)
		if manage {
			next.action = "hide"
			if r.Hidden {
				next.action = "restore"
			}
			label = fmt.Sprintf("%s %d", next.action, r.ID)
		}
		choices = append(choices, shareChoice{label, next})
	}
	for _, delta := range []int{-1, 1} {
		if v.page+delta >= 0 && v.page+delta < pages {
			next := v
			next.page += delta
			next.action = "directory"
			choices = append(choices, shareChoice{fmt.Sprintf("Page %d", next.page+1), next})
		}
	}
	refresh := v
	refresh.action = "directory"
	choices = append(choices, shareChoice{"Refresh", refresh})
	if manage {
		other := refresh
		if hidden {
			other.query = ""
		} else {
			other.query = "hidden"
		}
		choices = append(choices, shareChoice{"Visible / hidden recipients", other})
	}
	return p.show(ctx, v, body, choices)
}

func (p *SharePanel) permissions(ctx context.Context, v shareSelection) error {
	r := v.request
	body := fmt.Sprintf("Access for recipient <code>%d</code>\nRights: <b>%s</b>\nExpiry: %s\nRepository read: %t · Close agent: %t · Local focus: %t\n\nControl allows use of the agent's existing filesystem and tools. Owners retain control. All new agent output is shared, including results of owner prompts. No history is replayed; screen requests may reveal older visible content.", r.RecipientID, r.Role, expiryText(r.ExpiresAt), r.RepositoryRead, r.CloseAgent, r.LocalFocus)
	var choices []shareChoice
	for _, role := range []domain.ShareRole{domain.ShareRead, domain.ShareControl} {
		n := v
		n.action = "permissions"
		n.request.Role = role
		n.request.RepositoryRead = false
		n.request.CloseAgent = false
		n.request.LocalFocus = false
		choices = append(choices, shareChoice{string(role), n})
	}
	for _, duration := range []time.Duration{time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 0} {
		n := v
		n.action = "permissions"
		n.request.ExpiresAt = time.Time{}
		label := "No expiry"
		if duration > 0 {
			n.request.ExpiresAt = p.Now().Add(duration)
			label = duration.String()
		}
		choices = append(choices, shareChoice{label, n})
	}
	for _, capability := range []string{"repository", "close", "focus"} {
		if (capability == "repository") != (r.Role == domain.ShareRead) {
			continue
		}
		n := v
		n.action = "permissions"
		switch capability {
		case "repository":
			n.request.RepositoryRead = !r.RepositoryRead
		case "close":
			n.request.CloseAgent = !r.CloseAgent
		case "focus":
			n.request.LocalFocus = !r.LocalFocus
		}
		choices = append(choices, shareChoice{"Toggle " + capability, n})
	}
	n := v
	n.action = "confirm"
	choices = append(choices, shareChoice{"Review grant", n})
	return p.show(ctx, v, body, choices)
}

func expiryText(t time.Time) string {
	if t.IsZero() {
		return "no expiry"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func (p *SharePanel) Press(ctx context.Context, e domain.ButtonPressed) error {
	p.refsMu.Lock()
	v, ok := p.refs[e.Data]
	p.refsMu.Unlock()
	if !ok || e.ChatID != p.Config.ChatID || !p.Config.IsOperator(e.FromID) || e.FromID != v.actor || e.MessageID != v.message || e.ThreadID != v.thread || !p.Now().Before(v.expires) {
		return p.Telegram.AnswerButton(ctx, e.CallbackID, "This sharing panel is stale or belongs to another operator.")
	}
	if v.key.PaneID != "" {
		a, live := p.Agent(v.key)
		k, mapped := p.KeyForThread(v.thread)
		if !live || a.Key != v.key || (v.thread != 0 && (!mapped || k != v.key)) {
			return p.Telegram.AnswerButton(ctx, e.CallbackID, "Agent session changed. Run /share again.")
		}
	}
	_ = p.Telegram.AnswerButton(ctx, e.CallbackID, "")
	p.refsMu.Lock()
	delete(p.refs, e.Data)
	p.refsMu.Unlock()
	switch v.action {
	case "denial-failed":
		return p.show(ctx, v, "Durable permission change failed. Access is denied locally. Retry saving before restarting the daemon.", nil)
	case "directory":
		return p.directory(ctx, v, v.thread == 0)
	case "hide", "restore":
		if err := p.Sharing.HideRecipient(ctx, v.recipient, v.action == "hide", p.Now()); err != nil {
			return err
		}
		return p.directory(ctx, v, true)
	case "select":
		v.request = GrantRequest{RecipientID: v.recipient, Key: v.key, Role: domain.ShareRead}
		st, _ := p.Sharing.Snapshot()
		var revision uint64
		for _, g := range st.Grants {
			if g.RecipientID == v.recipient && g.Key == v.key {
				revision = g.Revision
			}
		}
		v.request.ExpectedRevision = &revision
		return p.permissions(ctx, v)
	case "permissions":
		return p.permissions(ctx, v)
	case "confirm":
		a, _ := p.Agent(v.key)
		n := v
		n.action = "activate"
		return p.show(ctx, v, fmt.Sprintf("Confirm access to <b>%s</b>\nSession: <code>%s</code>\nRecipient and private destination: <code>%d</code>\nRights: %s · expires %s\nRepository read: %t · Close: %t · Focus: %t\n\nControl shares this session and its existing tool permissions. Automatic output goes to every authorized mirror.", html.EscapeString(a.Label()), html.EscapeString(v.key.SessionDigest), v.request.RecipientID, v.request.Role, expiryText(v.request.ExpiresAt), v.request.RepositoryRead, v.request.CloseAgent, v.request.LocalFocus), []shareChoice{{"Confirm grant", n}})
	case "activate":
		id, err := p.Capability.Check(ctx)
		if err != nil {
			return err
		}
		v.request.Bot = id
		g, err := p.Sharing.Grant(ctx, v.request, p.Now())
		if err != nil {
			return err
		}
		if p.OnGrant != nil {
			if err := p.OnGrant(ctx, g); err != nil {
				return err
			}
		}
		return p.grants(ctx, v)
	case "grants":
		return p.grants(ctx, v)
	case "grant":
		return p.grantDetails(ctx, v)
	case "rights":
		st, _ := p.Sharing.Snapshot()
		g := st.Grants[v.grant]
		if g.Revision != v.revision {
			return errors.New("grant changed; refresh")
		}
		v.key = g.Key
		v.request = GrantRequest{ExpectedRevision: &g.Revision, RecipientID: g.RecipientID, Key: g.Key, Role: g.Role, RepositoryRead: g.RepositoryRead, CloseAgent: g.CloseAgent, LocalFocus: g.LocalFocus, ExpiresAt: g.ExpiresAt}
		return p.permissions(ctx, v)
	case "revoke", "suspend", "resume":
		next := domain.GrantRevoked
		if v.action == "suspend" {
			next = domain.GrantSuspended
		}
		if v.action == "resume" {
			next = domain.GrantActive
		}
		if err := p.Sharing.ChangeState(ctx, v.grant, v.revision, next, p.Now()); err != nil {
			return err
		}
		return p.grants(ctx, v)
	case "revoke-all-confirm":
		n := v
		n.action = "revoke-all"
		return p.show(ctx, v, "Revoke all grants listed in this scope? Already dispatched work cannot be recalled. History is retained.", []shareChoice{{"Confirm revoke all", n}})
	case "revoke-all":
		st, _ := p.Sharing.Snapshot()
		for _, g := range st.Grants {
			if v.key.PaneID == "" || g.Key == v.key {
				if err := p.Sharing.ChangeState(ctx, g.ID, g.Revision, domain.GrantRevoked, p.Now()); err != nil {
					return err
				}
			}
		}
		return p.grants(ctx, v)
	case "service-repair-confirm":
		n := v
		n.action = "service-repair"
		return p.show(ctx, v, "Retry overview topic creation? A topic from an earlier uncertain response may remain. Check the private chat first.", []shareChoice{{"Retry overview", n}})
	case "service-repair":
		st, _ := p.Sharing.Snapshot()
		g := st.Grants[v.grant]
		if g.Revision != v.revision {
			return errors.New("grant changed; refresh")
		}
		if err := p.Sharing.RepairService(ctx, g.RecipientID, p.Now()); err != nil {
			return err
		}
		return p.grants(ctx, v)
	case "repair-confirm":
		n := v
		n.action = "repair"
		return p.show(ctx, v, "The previous topic creation may have succeeded. Telegram cannot enumerate or recover its ID. Check your private chat before retrying; an orphan topic may remain.", []shareChoice{{"Retry topic creation", n}})
	case "repair":
		g, err := p.Sharing.RepairMirror(ctx, v.grant, v.revision, p.Now())
		if err != nil {
			return err
		}
		if p.OnGrant != nil {
			if err := p.OnGrant(ctx, g); err != nil {
				return err
			}
		}
		return p.grants(ctx, v)
	case "delete-confirm":
		n := v
		n.action = "delete"
		return p.show(ctx, v, "Delete this private mirror and all its history? This cannot be undone. Access is revoked first.", []shareChoice{{"Delete mirror and history", n}})
	case "delete":
		if err := p.Sharing.ChangeState(ctx, v.grant, v.revision, domain.GrantRevoked, p.Now()); err != nil {
			return err
		}
		st, _ := p.Sharing.Snapshot()
		m := st.Mirrors[v.grant]
		if m.Address.ThreadID > 0 {
			g := st.Grants[v.grant]
			if err := p.Private.DeleteTopicAt(ctx, m.Address, p.Sharing.BindingGuard(g.ID, g.Revision)); err != nil {
				return err
			}
			if err := p.Sharing.ForgetMirror(ctx, g.ID, g.Revision, p.Now()); err != nil {
				return err
			}
		}
		return p.grants(ctx, v)
	}
	return errors.New("unknown sharing action")
}

func (p *SharePanel) grants(ctx context.Context, v shareSelection) error {
	st, ok := p.Sharing.Snapshot()
	if !ok {
		return ErrSharingUnavailable
	}
	var grants []domain.ShareGrant
	for _, g := range st.Grants {
		if v.key.PaneID == "" || g.Key == v.key {
			grants = append(grants, g)
		}
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].ID < grants[j].ID })
	pages := max(1, (len(grants)+9)/10)
	v.page = min(max(0, v.page), pages-1)
	body := fmt.Sprintf("Shared access · %d grants · page %d/%d\nRevocation stops new dispatches. Running work cannot be recalled. History is retained.", len(grants), v.page+1, pages)
	var choices []shareChoice
	for _, g := range grants[min(v.page*10, len(grants)):min((v.page+1)*10, len(grants))] {
		body += fmt.Sprintf("\n%d · %s · %s", g.RecipientID, g.Role, g.State)
		n := v
		n.action = "grant"
		n.grant = g.ID
		n.revision = g.Revision
		choices = append(choices, shareChoice{fmt.Sprintf("Manage %d · %s", g.RecipientID, g.Role), n})
	}
	for _, delta := range []int{-1, 1} {
		if v.page+delta >= 0 && v.page+delta < pages {
			n := v
			n.page += delta
			n.action = "grants"
			choices = append(choices, shareChoice{fmt.Sprintf("Page %d", n.page+1), n})
		}
	}
	n := v
	n.action = "revoke-all-confirm"
	choices = append(choices, shareChoice{"Revoke all", n})
	n.action = "directory"
	choices = append(choices, shareChoice{"Recipients", n})
	return p.show(ctx, v, body, choices)
}

func (p *SharePanel) grantDetails(ctx context.Context, v shareSelection) error {
	st, _ := p.Sharing.Snapshot()
	g, ok := st.Grants[v.grant]
	if !ok || g.Revision != v.revision {
		return errors.New("grant changed; refresh")
	}
	var choices []shareChoice
	for _, action := range []string{"rights", "suspend", "resume", "revoke", "repair-confirm", "service-repair-confirm", "delete-confirm"} {
		n := v
		n.action = action
		choices = append(choices, shareChoice{action, n})
	}
	return p.show(ctx, v, fmt.Sprintf("Recipient %d · %s · %s\nExpires: %s\nChange rights also changes expiry.", g.RecipientID, g.Role, g.State, expiryText(g.ExpiresAt)), choices)
}

// DenyImmediately runs on the daemon loop, ahead of the bridge's network work.
// Only denial actions are admitted here; every reference still proves the
// configured owner route, actor, panel and revision. Rendering remains queued.
func (p *SharePanel) DenyImmediately(ctx context.Context, e domain.ButtonPressed) {
	p.refsMu.Lock()
	defer p.refsMu.Unlock()
	v, ok := p.refs[e.Data]
	if !ok || e.ChatID != p.Config.ChatID || !p.Config.IsOperator(e.FromID) || e.FromID != v.actor || e.MessageID != v.message || e.ThreadID != v.thread || !p.Now().Before(v.expires) {
		return
	}
	if v.action != "revoke" && v.action != "suspend" && v.action != "revoke-all" {
		return
	}
	next := domain.GrantRevoked
	if v.action == "suspend" {
		next = domain.GrantSuspended
	}
	var err error
	if v.action == "revoke-all" {
		st, _ := p.Sharing.Snapshot()
		for _, g := range st.Grants {
			if (v.key.PaneID == "" || g.Key == v.key) && g.State != domain.GrantRevoked {
				err = errors.Join(err, p.Sharing.ChangeState(ctx, g.ID, g.Revision, next, p.Now()))
			}
		}
	} else {
		err = p.Sharing.ChangeState(ctx, v.grant, v.revision, next, p.Now())
	}
	v.action = "grants"
	if err != nil {
		v.action = "denial-failed"
	}
	p.refs[e.Data] = v
}
