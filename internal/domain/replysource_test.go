package domain_test

import (
	"context"
	"errors"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// stubReplySource returns a scripted reply or error, and records whether
// it was called.
type stubReplySource struct {
	reply  domain.Reply
	err    error
	called bool
}

func (s *stubReplySource) LastReply(context.Context, domain.Agent) (domain.Reply, error) {
	s.called = true
	return s.reply, s.err
}

func TestMultiReplySourceFirstSucceeds(t *testing.T) {
	first := &stubReplySource{reply: domain.Reply{Text: "from first"}}
	second := &stubReplySource{reply: domain.Reply{Text: "from second"}}
	m := domain.MultiReplySource{first, second}

	r, err := m.LastReply(context.Background(), domain.Agent{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if r.Text != "from first" {
		t.Fatalf("text = %q, want %q", r.Text, "from first")
	}
	if second.called {
		t.Fatal("second source was called though the first succeeded")
	}
}

func TestMultiReplySourceFallsThrough(t *testing.T) {
	first := &stubReplySource{err: domain.ErrNoReply}
	second := &stubReplySource{reply: domain.Reply{Text: "from second"}}
	m := domain.MultiReplySource{first, second}

	r, err := m.LastReply(context.Background(), domain.Agent{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if r.Text != "from second" {
		t.Fatalf("text = %q, want %q", r.Text, "from second")
	}
	if !first.called || !second.called {
		t.Fatal("both sources should have been called")
	}
}

func TestMultiReplySourceAllFail(t *testing.T) {
	first := &stubReplySource{err: errors.New("unsupported agent kind")}
	second := &stubReplySource{err: domain.ErrNoReply}
	m := domain.MultiReplySource{first, second}

	_, err := m.LastReply(context.Background(), domain.Agent{})
	if !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
}

func TestMultiReplySourceEmpty(t *testing.T) {
	m := domain.MultiReplySource{}
	_, err := m.LastReply(context.Background(), domain.Agent{})
	if !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
}
