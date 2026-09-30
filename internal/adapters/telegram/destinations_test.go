package telegram_test

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

func TestDestinationsIsolateCollidingIDs(t *testing.T) {
	h := newHarness(t)
	for _, method := range []string{"sendMessage", "editMessageText", "sendDocument"} {
		h.api.on(method, func(url.Values) apiReply { return okReply(map[string]any{"message_id": 9}) })
	}
	for _, chat := range []int64{101, 202} {
		a := domain.TopicAddress{ChatID: chat, ThreadID: 7}
		m := domain.MessageAddress{ChatID: chat, MessageID: 9}
		if _, err := h.gw.SendAt(h.ctx, a, domain.Outgoing{Text: "hello"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.gw.ReactAt(h.ctx, m, "👍", nil); err != nil {
			t.Fatal(err)
		}
		if err := h.gw.EditTextAt(h.ctx, m, "new", false, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.gw.DeleteMessageAt(h.ctx, m, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.gw.PinAt(h.ctx, m, nil); err != nil {
			t.Fatal(err)
		}
		if err := h.gw.DocumentAt(h.ctx, a, domain.Document{Name: "screen.txt", Data: []byte("text")}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, method := range []string{"sendMessage", "setMessageReaction", "editMessageText", "deleteMessage", "pinChatMessage", "sendDocument"} {
		calls := h.api.callsOf(method)
		if len(calls) != 2 {
			t.Fatalf("%s calls=%d", method, len(calls))
		}
		for i, chat := range []int64{101, 202} {
			if calls[i].form.Get("chat_id") != strconv.FormatInt(chat, 10) {
				t.Fatalf("%s routed to wrong chat", method)
			}
		}
	}
	if len(h.api.callsOf("closeForumTopic"))+len(h.api.callsOf("reopenForumTopic")) != 0 {
		t.Fatal("private group lifecycle call")
	}
}

func TestPrivateMultipartRechecksGuard(t *testing.T) {
	h := newHarness(t)
	var revoked atomic.Bool
	denied := errors.New("revoked")
	h.api.on("sendMessage", func(url.Values) apiReply { revoked.Store(true); return okReply(map[string]any{"message_id": 9}) })
	guard := func(ctx context.Context) (context.Context, context.CancelFunc, error) {
		if revoked.Load() {
			return nil, nil, denied
		}
		return ctx, func() {}, nil
	}
	_, err := h.gw.SendAt(h.ctx, domain.TopicAddress{ChatID: 101, ThreadID: 7}, domain.Outgoing{Text: strings.Repeat("x", 5000)}, guard)
	if !errors.Is(err, denied) || len(h.api.callsOf("sendMessage")) != 1 {
		t.Fatalf("revocation ignored: %v", err)
	}
}

func TestPrivateFailureIsNotReplayed(t *testing.T) {
	h := newHarness(t)
	var revoked atomic.Bool
	denied := errors.New("revoked")
	h.api.on("sendMessage", func(url.Values) apiReply { revoked.Store(true); return errReply(500, "Internal Server Error") })
	guard := func(ctx context.Context) (context.Context, context.CancelFunc, error) {
		if revoked.Load() {
			return nil, nil, denied
		}
		return ctx, func() {}, nil
	}
	_, err := h.gw.SendAt(h.ctx, domain.TopicAddress{ChatID: 101, ThreadID: 7}, domain.Outgoing{Text: "hello"}, guard)
	if err == nil || len(h.api.callsOf("sendMessage")) != 1 {
		t.Fatalf("ambiguous private call was replayed: %v", err)
	}
}

func TestSlowPrivateCallDoesNotHoldOwnerQueue(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.api.on("sendMessage", func(f url.Values) apiReply {
		if f.Get("chat_id") == "101" {
			close(entered)
			<-release
		}
		return okReply(map[string]any{"message_id": 9})
	})
	completed := make(chan error, 1)
	go func() {
		_, err := h.gw.SendAt(h.ctx, domain.TopicAddress{ChatID: 101, ThreadID: 7}, domain.Outgoing{Text: "private"}, nil)
		completed <- err
	}()
	<-entered
	_, err := h.gw.Send(h.ctx, domain.Outgoing{Text: "owner"})
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
}
