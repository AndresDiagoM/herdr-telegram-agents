package testkit

import (
	"context"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// Destination gives each chat its own ID namespace, deliberately starting
// IDs at the same values to expose accidental cross-chat routing.
func (f *FakeTelegram) Destination(chat int64) *FakeTelegram {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.destinations == nil {
		f.destinations = map[int64]*FakeTelegram{}
	}
	d := f.destinations[chat]
	if d == nil {
		d = NewFakeTelegram(f.log)
		f.destinations[chat] = d
	}
	return d
}
func fakeGuard(ctx context.Context, g domain.DispatchGuard) (context.Context, context.CancelFunc, error) {
	if g == nil {
		return ctx, func() {}, ctx.Err()
	}
	return g(ctx)
}
func (f *FakeTelegram) CreateTopicAt(ctx context.Context, chat int64, name string, status domain.Status, g domain.DispatchGuard) (domain.Topic, error) {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return domain.Topic{}, err
	}
	defer cancel()
	return f.Destination(chat).CreateTopic(ctx, name, status)
}
func (f *FakeTelegram) EditTopicAt(ctx context.Context, a domain.TopicAddress, p domain.TopicPatch, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).EditTopic(ctx, a.ThreadID, p)
}
func (f *FakeTelegram) DeleteTopicAt(ctx context.Context, a domain.TopicAddress, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).DeleteTopic(ctx, a.ThreadID)
}
func (f *FakeTelegram) SendAt(ctx context.Context, a domain.TopicAddress, o domain.Outgoing, g domain.DispatchGuard) (int, error) {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return 0, err
	}
	defer cancel()
	o.ThreadID = a.ThreadID
	return f.Destination(a.ChatID).Send(ctx, o)
}
func (f *FakeTelegram) DocumentAt(ctx context.Context, a domain.TopicAddress, d domain.Document, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	d.ThreadID = a.ThreadID
	return f.Destination(a.ChatID).SendDocument(ctx, d)
}
func (f *FakeTelegram) EditTextAt(ctx context.Context, a domain.MessageAddress, text string, html bool, b []domain.Button, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).EditText(ctx, a.MessageID, text, html, b)
}
func (f *FakeTelegram) EditButtonsAt(ctx context.Context, a domain.MessageAddress, b []domain.Button, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).EditButtons(ctx, a.MessageID, b)
}
func (f *FakeTelegram) ReactAt(ctx context.Context, a domain.MessageAddress, emoji string, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).React(ctx, 0, a.MessageID, emoji)
}
func (f *FakeTelegram) PinAt(ctx context.Context, a domain.MessageAddress, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).Pin(ctx, a.MessageID)
}
func (f *FakeTelegram) UnpinAt(ctx context.Context, a domain.MessageAddress, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).Unpin(ctx, a.MessageID)
}
func (f *FakeTelegram) DeleteMessageAt(ctx context.Context, a domain.MessageAddress, g domain.DispatchGuard) error {
	ctx, cancel, err := fakeGuard(ctx, g)
	if err != nil {
		return err
	}
	defer cancel()
	return f.Destination(a.ChatID).DeleteMessage(ctx, a.MessageID)
}

var _ domain.DestinationTelegram = (*FakeTelegram)(nil)
