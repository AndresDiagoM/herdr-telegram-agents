package app

import (
	"context"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// PrivateRedactor applies redaction regardless of the owner's display option.
// Lifecycle names and aliases pass through the same filter as agent output.
type PrivateRedactor struct {
	domain.DestinationTelegram
	Redactor *domain.Redactor
}

func (p PrivateRedactor) text(s string) string {
	r := p.Redactor
	if r == nil {
		r = domain.NewRedactor()
	}
	text, _ := r.Redact(s)
	return text
}
func (p PrivateRedactor) buttons(in []domain.Button) []domain.Button {
	out := append([]domain.Button(nil), in...)
	for i := range out {
		out[i].Text = p.text(out[i].Text)
	}
	return out
}
func (p PrivateRedactor) SendAt(ctx context.Context, a domain.TopicAddress, o domain.Outgoing, g domain.DispatchGuard) (int, error) {
	o.Text = p.text(o.Text)
	o.Footer = p.text(o.Footer)
	o.Buttons = p.buttons(o.Buttons)
	return p.DestinationTelegram.SendAt(ctx, a, o, g)
}
func (p PrivateRedactor) DocumentAt(ctx context.Context, a domain.TopicAddress, d domain.Document, g domain.DispatchGuard) error {
	d.Data = []byte(p.text(string(d.Data)))
	d.Name = p.text(d.Name)
	d.Caption = p.text(d.Caption)
	return p.DestinationTelegram.DocumentAt(ctx, a, d, g)
}
func (p PrivateRedactor) EditTextAt(ctx context.Context, a domain.MessageAddress, text string, html bool, b []domain.Button, g domain.DispatchGuard) error {
	return p.DestinationTelegram.EditTextAt(ctx, a, p.text(text), html, p.buttons(b), g)
}
func (p PrivateRedactor) EditButtonsAt(ctx context.Context, a domain.MessageAddress, b []domain.Button, g domain.DispatchGuard) error {
	return p.DestinationTelegram.EditButtonsAt(ctx, a, p.buttons(b), g)
}
func (p PrivateRedactor) CreateTopicAt(ctx context.Context, chat int64, name string, status domain.Status, g domain.DispatchGuard) (domain.Topic, error) {
	return p.DestinationTelegram.CreateTopicAt(ctx, chat, p.text(name), status, g)
}
func (p PrivateRedactor) EditTopicAt(ctx context.Context, a domain.TopicAddress, patch domain.TopicPatch, g domain.DispatchGuard) error {
	if patch.Name != nil {
		name := p.text(*patch.Name)
		patch.Name = &name
	}
	return p.DestinationTelegram.EditTopicAt(ctx, a, patch, g)
}
