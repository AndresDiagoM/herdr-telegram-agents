package transcript

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// openCodeTuple is the session Herdr reports for an opencode pane.
var openCodeTuple = domain.SessionTuple{Source: "herdr:opencode", Agent: "opencode", Kind: "id", Value: "ses_abc"}

// openCodeTurn is a trimmed "opencode export" of one session: an older
// turn, then the last prompt answered in two steps, the first a tool call
// with an edit and no text, the second the reply.
const openCodeTurn = `{
  "info": {"id": "ses_abc"},
  "messages": [
    {"info": {"role": "user", "time": {"created": 500}}, "parts": [{"type": "text", "text": "old prompt"}]},
    {"info": {"role": "assistant", "modelID": "old-model", "tokens": {"output": 7}, "time": {"created": 600, "completed": 700}},
     "parts": [{"type": "text", "text": "old reply"}]},
    {"info": {"role": "user", "time": {"created": 1000}}, "parts": [{"type": "text", "text": "what's next"}]},
    {"info": {"role": "assistant", "modelID": "codigo-3", "tokens": {"output": 40}, "time": {"created": 1001, "completed": 1002}},
     "parts": [
       {"type": "step-start"},
       {"type": "reasoning", "text": "thinking about it"},
       {"type": "tool", "tool": "edit", "state": {"input": {"filePath": "docs/plan.md"}}},
       {"type": "tool", "tool": "read", "state": {"input": {"filePath": "README.md"}}},
       {"type": "step-finish"}
     ]},
    {"info": {"role": "assistant", "modelID": "codigo-3", "tokens": {"output": 60}, "time": {"created": 1003, "completed": 61004}},
     "parts": [
       {"type": "tool", "tool": "edit", "state": {"input": {"filePath": "docs/plan.md"}}},
       {"type": "text", "text": "Endpoint is **live** now."},
       {"type": "text", "text": "Click refresh."}
     ]}
  ]
}`

// openCodeFixture builds a reader over scripted Herdr and export answers
// and records whether the export ran.
type openCodeFixture struct {
	tuple    domain.SessionTuple
	lookErr  error
	out      string
	exportFn func(id string) ([]byte, error)
	exported []string
	logBuf   bytes.Buffer
}

func (f *openCodeFixture) reader() *OpenCodeReader {
	session := func(context.Context, string) (domain.SessionTuple, error) { return f.tuple, f.lookErr }
	export := func(_ context.Context, id string) ([]byte, error) {
		f.exported = append(f.exported, id)
		if f.exportFn != nil {
			return f.exportFn(id)
		}
		return []byte(f.out), nil
	}
	r := NewOpenCodeReader(session, export, slog.New(slog.NewJSONHandler(&f.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	r.now = func() time.Time { return time.UnixMilli(61004).Add(5 * time.Second) }
	return r
}

func openCodeAgent(digest string) domain.Agent {
	return domain.Agent{Key: domain.Key{PaneID: "p1", TerminalID: "t1", SessionDigest: digest}, Kind: "opencode"}
}

func TestOpenCodeLastReply(t *testing.T) {
	f := &openCodeFixture{tuple: openCodeTuple, out: openCodeTurn}
	reply, err := f.reader().LastReply(context.Background(), openCodeAgent(openCodeTuple.Digest()))
	if err != nil {
		t.Fatalf("LastReply: %v", err)
	}
	if want := "Endpoint is **live** now.\n\nClick refresh."; reply.Text != want {
		t.Fatalf("Text = %q, want %q", reply.Text, want)
	}
	if len(f.exported) != 1 || f.exported[0] != "ses_abc" {
		t.Fatalf("exported %v, want the session id once", f.exported)
	}
	m := reply.Meta
	if m.Model != "codigo-3" || m.OutputTokens != 100 || !m.Started.Equal(time.UnixMilli(1000)) || !m.Ended.Equal(time.UnixMilli(61004)) {
		t.Fatalf("Meta = %+v", m)
	}
	if len(m.Files) != 1 || m.Files[0] != "docs/plan.md" {
		t.Fatalf("Files = %v, want the one edited file once", m.Files)
	}
	if d, ok := m.Duration(); !ok || d != time.Minute+4*time.Millisecond {
		t.Fatalf("Duration = %v, %v", d, ok)
	}
	if !reply.Written.Equal(m.Ended) || reply.Age != 5*time.Second {
		t.Fatalf("Written = %v, Age = %v", reply.Written, reply.Age)
	}
	// The session value is transient: never in the Reply or the log.
	if strings.Contains(reply.Source, "ses_abc") || strings.Contains(f.logBuf.String(), "ses_abc") {
		t.Fatalf("session value leaked: source %q, log %s", reply.Source, f.logBuf.String())
	}
}

// TestOpenCodeLastReplySessionlessKey covers a mapping entry that predates
// session digests: there is nothing to compare, so the lookup proceeds.
func TestOpenCodeLastReplySessionlessKey(t *testing.T) {
	f := &openCodeFixture{tuple: openCodeTuple, out: openCodeTurn}
	if _, err := f.reader().LastReply(context.Background(), openCodeAgent("")); err != nil {
		t.Fatalf("LastReply: %v", err)
	}
}

func TestOpenCodeLastReplyNoReply(t *testing.T) {
	other := openCodeTuple
	other.Value = "ses_other"
	cases := map[string]struct {
		fixture    openCodeFixture
		agent      domain.Agent
		wantExport bool
	}{
		"unsupported kind": {openCodeFixture{tuple: openCodeTuple, out: openCodeTurn},
			domain.Agent{Key: domain.Key{PaneID: "p1"}, Kind: "claude"}, false},
		"lookup fails": {openCodeFixture{lookErr: errors.New("herdr down")},
			openCodeAgent(openCodeTuple.Digest()), false},
		"no session": {openCodeFixture{},
			openCodeAgent(""), false},
		"not an id session": {openCodeFixture{tuple: domain.SessionTuple{Source: "herdr:opencode", Agent: "opencode", Kind: "path", Value: "/x"}},
			openCodeAgent(""), false},
		"incomplete tuple": {openCodeFixture{tuple: domain.SessionTuple{Agent: "opencode", Kind: "id", Value: "ses_abc"}},
			openCodeAgent(""), false},
		"pane runs another session": {openCodeFixture{tuple: other, out: openCodeTurn},
			openCodeAgent(openCodeTuple.Digest()), false},
		"export fails": {openCodeFixture{tuple: openCodeTuple, exportFn: func(string) ([]byte, error) { return nil, errors.New("exit status 1") }},
			openCodeAgent(openCodeTuple.Digest()), true},
		"not json": {openCodeFixture{tuple: openCodeTuple, out: "Exporting session..."},
			openCodeAgent(openCodeTuple.Digest()), true},
		"another session exported": {openCodeFixture{tuple: openCodeTuple, out: `{"info":{"id":"ses_zzz"},"messages":[]}`},
			openCodeAgent(openCodeTuple.Digest()), true},
		"no text after the prompt": {openCodeFixture{tuple: openCodeTuple, out: `{"info":{"id":"ses_abc"},"messages":[
			{"info":{"role":"user","time":{"created":1}},"parts":[{"type":"text","text":"run it"}]},
			{"info":{"role":"assistant","time":{"created":2}},"parts":[{"type":"tool","tool":"bash"}]}]}`},
			openCodeAgent(openCodeTuple.Digest()), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := tc.fixture
			_, err := f.reader().LastReply(context.Background(), tc.agent)
			if !errors.Is(err, domain.ErrNoReply) {
				t.Fatalf("err = %v, want ErrNoReply", err)
			}
			if got := len(f.exported) > 0; got != tc.wantExport {
				t.Fatalf("export ran = %v, want %v", got, tc.wantExport)
			}
		})
	}
}

func TestOpenCodeLastReplyCancelled(t *testing.T) {
	f := &openCodeFixture{tuple: openCodeTuple, out: openCodeTurn}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.reader().LastReply(ctx, openCodeAgent("")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
