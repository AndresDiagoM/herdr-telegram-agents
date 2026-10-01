package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// PrivateRedactor applies redaction regardless of the owner's display option.
// Lifecycle names and aliases pass through the same filter as agent output.
//
// It is also the single choke point for private-chat failures: a 403 from a
// private chat means one recipient blocked the bot, so it is reported as
// domain.ErrRecipientUnavailable (which no longer matches ErrForbidden) and
// never reaches the daemon's fatal path.
type PrivateRedactor struct {
	domain.DestinationTelegram
	Redactor *domain.Redactor
	// Unavailable marks the recipient behind the private chat unreachable.
	Unavailable func(ctx context.Context, chat int64)
	Log         *slog.Logger
}

func (p PrivateRedactor) text(s string) string {
	r := p.Redactor
	if r == nil {
		r = domain.NewRedactor()
	}
	text, _ := r.Redact(s)
	return text
}
func (p PrivateRedactor) markup(s string, html bool) string {
	if !html {
		return p.text(s)
	}
	return redactHTML(s, p.text)
}
func (p PrivateRedactor) buttons(in []domain.Button) []domain.Button {
	out := append([]domain.Button(nil), in...)
	for i := range out {
		out[i].Text = p.text(out[i].Text)
	}
	return out
}

// recipient translates a private-chat 403 into ErrRecipientUnavailable.
func (p PrivateRedactor) recipient(ctx context.Context, chat int64, op string, err error) error {
	if !errors.Is(err, domain.ErrForbidden) {
		return err
	}
	if p.Log != nil {
		p.Log.Warn("[FIX] private recipient unavailable", slog.Int64("chat_id", chat), slog.String("op", op), slog.String("err", err.Error()))
	}
	if p.Unavailable != nil {
		p.Unavailable(ctx, chat)
	}
	return fmt.Errorf("%w: %s: %v", domain.ErrRecipientUnavailable, op, err)
}

func (p PrivateRedactor) SendAt(ctx context.Context, a domain.TopicAddress, o domain.Outgoing, g domain.DispatchGuard) (int, error) {
	o.Text = p.markup(o.Text, o.HTML)
	o.Footer = p.text(o.Footer)
	o.Buttons = p.buttons(o.Buttons)
	id, err := p.DestinationTelegram.SendAt(ctx, a, o, g)
	return id, p.recipient(ctx, a.ChatID, "send", err)
}
func (p PrivateRedactor) DocumentAt(ctx context.Context, a domain.TopicAddress, d domain.Document, g domain.DispatchGuard) error {
	d.Data = []byte(p.text(string(d.Data)))
	d.Name = p.text(d.Name)
	d.Caption = p.text(d.Caption)
	return p.recipient(ctx, a.ChatID, "document", p.DestinationTelegram.DocumentAt(ctx, a, d, g))
}
func (p PrivateRedactor) EditTextAt(ctx context.Context, a domain.MessageAddress, text string, html bool, b []domain.Button, g domain.DispatchGuard) error {
	return p.recipient(ctx, a.ChatID, "edit_text", p.DestinationTelegram.EditTextAt(ctx, a, p.markup(text, html), html, p.buttons(b), g))
}
func (p PrivateRedactor) EditButtonsAt(ctx context.Context, a domain.MessageAddress, b []domain.Button, g domain.DispatchGuard) error {
	return p.recipient(ctx, a.ChatID, "edit_buttons", p.DestinationTelegram.EditButtonsAt(ctx, a, p.buttons(b), g))
}
func (p PrivateRedactor) CreateTopicAt(ctx context.Context, chat int64, name string, status domain.Status, g domain.DispatchGuard) (domain.Topic, error) {
	t, err := p.DestinationTelegram.CreateTopicAt(ctx, chat, p.text(name), status, g)
	return t, p.recipient(ctx, chat, "create_topic", err)
}
func (p PrivateRedactor) EditTopicAt(ctx context.Context, a domain.TopicAddress, patch domain.TopicPatch, g domain.DispatchGuard) error {
	if patch.Name != nil {
		name := p.text(*patch.Name)
		patch.Name = &name
	}
	return p.recipient(ctx, a.ChatID, "edit_topic", p.DestinationTelegram.EditTopicAt(ctx, a, patch, g))
}
func (p PrivateRedactor) DeleteTopicAt(ctx context.Context, a domain.TopicAddress, g domain.DispatchGuard) error {
	return p.recipient(ctx, a.ChatID, "delete_topic", p.DestinationTelegram.DeleteTopicAt(ctx, a, g))
}
func (p PrivateRedactor) ReactAt(ctx context.Context, a domain.MessageAddress, emoji string, g domain.DispatchGuard) error {
	return p.recipient(ctx, a.ChatID, "react", p.DestinationTelegram.ReactAt(ctx, a, emoji, g))
}
func (p PrivateRedactor) PinAt(ctx context.Context, a domain.MessageAddress, g domain.DispatchGuard) error {
	return p.recipient(ctx, a.ChatID, "pin", p.DestinationTelegram.PinAt(ctx, a, g))
}
func (p PrivateRedactor) UnpinAt(ctx context.Context, a domain.MessageAddress, g domain.DispatchGuard) error {
	return p.recipient(ctx, a.ChatID, "unpin", p.DestinationTelegram.UnpinAt(ctx, a, g))
}
func (p PrivateRedactor) DeleteMessageAt(ctx context.Context, a domain.MessageAddress, g domain.DispatchGuard) error {
	return p.recipient(ctx, a.ChatID, "delete_message", p.DestinationTelegram.DeleteMessageAt(ctx, a, g))
}
