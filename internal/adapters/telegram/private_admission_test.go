package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

type admissionTestClient struct{ body string }

func (c admissionTestClient) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(c.body))}, nil
}

func TestAdmissionCompletesBeforePollingResponse(t *testing.T) {
	body := `{"ok":true,"result":[{"update_id":7,"message":{"message_id":9,"date":100,"chat":{"id":11,"type":"private"},"from":{"id":11,"is_bot":false,"first_name":"name"},"text":"/start"}}]}`
	admitted := false
	client := &admissionClient{client: admissionTestClient{body}, admit: func(context.Context, *models.Update) error { admitted = true; return nil }}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "http://example.invalid/botredacted/getUpdates", nil)
	res, err := client.Do(req)
	if err != nil || !admitted {
		t.Fatalf("admission not completed: %v", err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Equal(got, []byte(body)) {
		t.Fatal("response altered")
	}
	client.admit = func(context.Context, *models.Update) error { return errors.New("disk full") }
	if res, err := client.Do(req); err == nil || res != nil {
		t.Fatal("failed admission released update for acknowledgement")
	}
}

func TestPrivateContactKinds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*models.Message)
		want   bool
	}{
		{"text", func(m *models.Message) { m.Text = "hello" }, true},
		{"command", func(m *models.Message) { m.Text = "/start" }, true},
		{"photo", func(m *models.Message) { m.Photo = []models.PhotoSize{{FileID: "x"}} }, true},
		{"voice", func(m *models.Message) { m.Voice = &models.Voice{FileID: "x"} }, true},
		{"sticker", func(m *models.Message) { m.Sticker = &models.Sticker{FileID: "x"} }, true},
		{"no username", func(m *models.Message) { m.Text = "hello"; m.From.Username = "" }, true},
		{"forward", func(m *models.Message) { m.Text = "forward"; m.ForwardOrigin = &models.MessageOrigin{} }, true},
		{"bot", func(m *models.Message) { m.Text = "hello"; m.From.IsBot = true }, false},
		{"group", func(m *models.Message) { m.Text = "hello"; m.Chat.Type = models.ChatTypeSupergroup }, false},
		{"anonymous", func(m *models.Message) { m.Text = "hello"; m.From = nil }, false},
		{"service", func(m *models.Message) { m.ForumTopicEdited = &models.ForumTopicEdited{Name: "new"} }, false},
		{"other service", func(m *models.Message) { m.WriteAccessAllowed = &models.WriteAccessAllowed{} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &models.Message{Chat: models.Chat{ID: 11, Type: models.ChatTypePrivate}, From: &models.User{ID: 11, FirstName: "name"}}
			tc.mutate(m)
			c, ok := privateContact(&models.Update{ID: 7, Message: m}, time.Unix(100, 0))
			if ok != tc.want {
				t.Fatalf("accepted=%v", ok)
			}
			if ok && c.ActorID != 11 {
				t.Fatal("sender replaced")
			}
		})
	}
}

func TestPrivateIngressRateLimitReservesOtherRecipients(t *testing.T) {
	now := time.Unix(100, 0)
	g := &Gateway{queue: NewQueue(nil, QueueConfig{Now: func() time.Time { return now }})}
	for i := 0; i < 8; i++ {
		if !g.admitPrivateEvent(1) {
			t.Fatal("burst refused early")
		}
	}
	if g.admitPrivateEvent(1) {
		t.Fatal("per-recipient flood admitted")
	}
	if !g.admitPrivateEvent(2) {
		t.Fatal("one recipient exhausted all admission")
	}
	for id := int64(3); id < 26; id++ {
		if !g.admitPrivateEvent(id) {
			t.Fatal("global burst refused early")
		}
	}
	if g.admitPrivateEvent(26) {
		t.Fatal("global limit exceeded")
	}
	now = now.Add(10 * time.Second)
	if !g.admitPrivateEvent(1) {
		t.Fatal("limiter did not recover")
	}
}
