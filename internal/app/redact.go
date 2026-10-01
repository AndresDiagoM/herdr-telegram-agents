package app

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// redactingGateway wraps a TelegramGateway so every text that leaves the
// daemon (screen posts, pager messages, documents, button labels, panel
// edits, notices) passes the domain.Redactor first. It is the single
// insertion point for the privacy.redact option: the call sites that build
// posts never need to know about it. Everything else (pins, deletes, the
// private-chat probe) is passed through untouched.
type redactingGateway struct {
	domain.TelegramGateway
	red     *domain.Redactor
	enabled func() bool
	log     *slog.Logger
}

// newRedactingGateway returns tg wrapped; enabled is read on every call so
// a change of the option applies to the next post. A nil enabled means
// always on.
func newRedactingGateway(tg domain.TelegramGateway, red *domain.Redactor, enabled func() bool, log *slog.Logger) domain.TelegramGateway {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if enabled == nil {
		enabled = func() bool { return true }
	}
	if red == nil {
		red = domain.NewRedactor()
	}
	return &redactingGateway{TelegramGateway: tg, red: red, enabled: enabled, log: log}
}

// NewRedactingGateway wraps tg for callers outside the bridge and daemon
// (the reconciler, the share panel), so topic names and toasts follow the
// same privacy.redact option.
func NewRedactingGateway(tg domain.TelegramGateway, botToken string, enabled func() bool, log *slog.Logger) domain.TelegramGateway {
	return newRedactingGateway(tg, domain.NewRedactor(botToken), enabled, log)
}

func (g *redactingGateway) Send(ctx context.Context, out domain.Outgoing) (int, error) {
	if !g.enabled() {
		return g.TelegramGateway.Send(ctx, out)
	}
	stats := domain.RedactionStats{}
	out.Text = g.redactMarkup(out.Text, out.HTML, stats)
	out.Footer = g.redact(out.Footer, stats)
	out.Buttons = g.redactButtons(out.Buttons, stats)
	g.report(out.ThreadID, "send", stats)
	return g.TelegramGateway.Send(ctx, out)
}

func (g *redactingGateway) SendDirect(ctx context.Context, userID int64, out domain.Outgoing) (int, error) {
	if !g.enabled() {
		return g.TelegramGateway.SendDirect(ctx, userID, out)
	}
	stats := domain.RedactionStats{}
	out.Text = g.redact(out.Text, stats)
	out.Footer = g.redact(out.Footer, stats)
	out.Buttons = g.redactButtons(out.Buttons, stats)
	g.report(0, "direct", stats)
	return g.TelegramGateway.SendDirect(ctx, userID, out)
}

func (g *redactingGateway) SendDocument(ctx context.Context, doc domain.Document) error {
	if !g.enabled() {
		return g.TelegramGateway.SendDocument(ctx, doc)
	}
	stats := domain.RedactionStats{}
	doc.Data = []byte(g.redact(string(doc.Data), stats))
	doc.Caption = g.redact(doc.Caption, stats)
	doc.Name = g.redact(doc.Name, stats)
	g.report(doc.ThreadID, "document", stats)
	return g.TelegramGateway.SendDocument(ctx, doc)
}

func (g *redactingGateway) EditText(ctx context.Context, messageID int, text string, html bool, buttons []domain.Button) error {
	if !g.enabled() {
		return g.TelegramGateway.EditText(ctx, messageID, text, html, buttons)
	}
	stats := domain.RedactionStats{}
	text = g.redactMarkup(text, html, stats)
	buttons = g.redactButtons(buttons, stats)
	g.report(0, "edittext", stats)
	return g.TelegramGateway.EditText(ctx, messageID, text, html, buttons)
}

func (g *redactingGateway) EditButtons(ctx context.Context, messageID int, buttons []domain.Button) error {
	if !g.enabled() {
		return g.TelegramGateway.EditButtons(ctx, messageID, buttons)
	}
	stats := domain.RedactionStats{}
	buttons = g.redactButtons(buttons, stats)
	g.report(0, "buttons", stats)
	return g.TelegramGateway.EditButtons(ctx, messageID, buttons)
}

// CreateTopic masks the topic name: it is built from the agent label.
func (g *redactingGateway) CreateTopic(ctx context.Context, name string, status domain.Status) (domain.Topic, error) {
	if !g.enabled() {
		return g.TelegramGateway.CreateTopic(ctx, name, status)
	}
	stats := domain.RedactionStats{}
	name = domain.DisplayName(g.redact(name, stats))
	g.report(0, "create_topic", stats)
	return g.TelegramGateway.CreateTopic(ctx, name, status)
}

// EditTopic masks a renamed topic; the caller's patch is not modified.
func (g *redactingGateway) EditTopic(ctx context.Context, threadID int, patch domain.TopicPatch) error {
	if !g.enabled() || patch.Name == nil {
		return g.TelegramGateway.EditTopic(ctx, threadID, patch)
	}
	stats := domain.RedactionStats{}
	name := domain.DisplayName(g.redact(*patch.Name, stats))
	patch.Name = &name
	g.report(threadID, "edit_topic", stats)
	return g.TelegramGateway.EditTopic(ctx, threadID, patch)
}

// AnswerButton masks the toast text.
func (g *redactingGateway) AnswerButton(ctx context.Context, callbackID, text string) error {
	if !g.enabled() {
		return g.TelegramGateway.AnswerButton(ctx, callbackID, text)
	}
	stats := domain.RedactionStats{}
	text = g.redact(text, stats)
	g.report(0, "answer", stats)
	return g.TelegramGateway.AnswerButton(ctx, callbackID, text)
}

func (g *redactingGateway) redactMarkup(text string, html bool, stats domain.RedactionStats) string {
	if !html {
		return g.redact(text, stats)
	}
	return redactHTML(text, func(s string) string { return g.redact(s, stats) })
}

// htmlTag matches one tag of Telegram's HTML subset.
var htmlTag = regexp.MustCompile(`<[^<>]*>`)

// redactHTML applies redact to the text between tags only, so a value
// pattern can never consume a closing tag and break the message. A secret
// split by a tag is missed; the renderers never put tags inside a word.
func redactHTML(text string, redact func(string) string) string {
	tags := htmlTag.FindAllStringIndex(text, -1)
	if len(tags) == 0 {
		return redact(text)
	}
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, t := range tags {
		b.WriteString(redact(text[last:t[0]]))
		b.WriteString(text[t[0]:t[1]])
		last = t[1]
	}
	b.WriteString(redact(text[last:]))
	return b.String()
}

func (g *redactingGateway) redact(text string, stats domain.RedactionStats) string {
	out, s := g.red.Redact(text)
	for k, n := range s {
		stats[k] += n
	}
	return out
}

// redactButtons copies the slice so the caller's keyboard (kept by
// outbound for later edits) is never rewritten in place.
func (g *redactingGateway) redactButtons(buttons []domain.Button, stats domain.RedactionStats) []domain.Button {
	if len(buttons) == 0 {
		return buttons
	}
	out := make([]domain.Button, len(buttons))
	copy(out, buttons)
	for i := range out {
		out[i].Text = g.redact(out[i].Text, stats)
	}
	return out
}

// report logs the kinds and counts of what was masked; the values never
// reach the log.
func (g *redactingGateway) report(threadID int, via string, stats domain.RedactionStats) {
	if stats.Total() == 0 {
		return
	}
	g.log.Info("secrets redacted", slog.Int("thread_id", threadID), slog.String("via", via), slog.String("kinds", stats.String()))
}
