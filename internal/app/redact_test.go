package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
	"github.com/permgps/herdr-telegram-agents/internal/testkit"
)

const testKey = "sk-abcdefghijklmnopqrstuvwx"

func TestRedactingGatewayMasksEverythingThatLeaves(t *testing.T) {
	fake := testkit.NewFakeTelegram(nil)
	on := true
	tg := newRedactingGateway(fake, domain.NewRedactor(testBotToken), func() bool { return on }, nil)
	ctx := context.Background()

	buttons := []domain.Button{{Text: "1️⃣ use " + testKey, Data: "1"}, {Text: "2️⃣ no", Data: "2"}}
	id, err := tg.Send(ctx, domain.Outgoing{ThreadID: 0, Text: "token " + testBotToken + " and " + testKey, Footer: testBotToken, Buttons: buttons})
	if err != nil {
		t.Fatal(err)
	}
	sent := fake.Sent()
	if len(sent) != 1 || sent[0].Text != "token [redacted] and sk-…uvwx" || sent[0].Footer != "[redacted]" {
		t.Fatalf("Sent = %+v", sent)
	}
	if got := fake.Buttons(id); got[0].Text != "1️⃣ use sk-…uvwx" || got[1].Text != "2️⃣ no" {
		t.Fatalf("Buttons = %+v", got)
	}
	if buttons[0].Text != "1️⃣ use "+testKey {
		t.Fatal("caller's keyboard was rewritten in place")
	}

	if err := tg.SendDocument(ctx, domain.Document{ThreadID: 0, Name: testBotToken + ".txt", Data: []byte("line " + testKey + "\n"), Caption: "cap " + testBotToken}); err != nil {
		t.Fatal(err)
	}
	docs := fake.Documents()
	if len(docs) != 1 || string(docs[0].Data) != "line sk-…uvwx\n" || docs[0].Caption != "cap [redacted]" || docs[0].Name != "[redacted].txt" {
		t.Fatalf("Documents = %+v", docs)
	}

	if err := tg.EditText(ctx, id, "edited "+testKey, false, []domain.Button{{Text: "Bearer 0123456789abcdefghij", Data: "x"}}); err != nil {
		t.Fatal(err)
	}
	if fake.Text(id) != "edited sk-…uvwx" || fake.Buttons(id)[0].Text != "Bearer …ghij" {
		t.Fatalf("EditText left %q / %+v", fake.Text(id), fake.Buttons(id))
	}
	if err := tg.EditButtons(ctx, id, []domain.Button{{Text: "✅ " + testKey, Data: "1"}}); err != nil {
		t.Fatal(err)
	}
	if fake.Buttons(id)[0].Text != "✅ sk-…uvwx" {
		t.Fatalf("EditButtons left %+v", fake.Buttons(id))
	}

	did, err := tg.SendDirect(ctx, 77, domain.Outgoing{Text: "❓ waiting " + testKey, Footer: testBotToken, Notify: true, Buttons: []domain.Button{{Text: "use " + testKey, Data: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if direct := fake.Direct(); len(direct) != 1 || direct[0].Text != "❓ waiting sk-…uvwx" || direct[0].Footer != "[redacted]" || !direct[0].Notify || fake.Buttons(did)[0].Text != "use sk-…uvwx" {
		t.Fatalf("Direct = %+v / %+v", fake.Direct(), fake.Buttons(did))
	}

	on = false
	if _, err := tg.Send(ctx, domain.Outgoing{ThreadID: 0, Text: "raw " + testKey}); err != nil {
		t.Fatal(err)
	}
	if got := fake.Sent(); got[len(got)-1].Text != "raw "+testKey {
		t.Fatalf("option off still redacted: %q", got[len(got)-1].Text)
	}
}

func TestRedactingGatewayPassesRightsAndIcons(t *testing.T) {
	fake := testkit.NewFakeTelegram(nil)
	tg := newRedactingGateway(fake, nil, nil, nil)
	ctx := context.Background()
	if _, err := tg.Rights(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := tg.Send(ctx, domain.Outgoing{Text: "board"})
	if err := tg.ProbeDirect(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tg.Pin(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := tg.Unpin(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := tg.DeleteMessage(ctx, id); err != nil {
		t.Fatal(err)
	}
	if calls := fake.Calls(); len(calls) != 6 || calls[2] != "probe:1" || calls[3] != "pin:1000" || calls[4] != "unpin:1000" || calls[5] != "deletemsg:1000" {
		t.Fatalf("pass-through calls = %v", calls)
	}
	tg.SetStatusIcons(domain.StatusIcons{Working: "🔥"})
	if fake.Icons().Working != "🔥" || len(tg.IconPack()) == 0 {
		t.Fatal("pass-through methods broken")
	}
}

func TestOutboundBlockedPostIsRedacted(t *testing.T) {
	f := newBridgeFixture(t)
	a := f.add(t, "p1", "t1", "reviewer", domain.StatusWorking)
	f.herdr.SetScreen("p1", "\n  Use key "+testKey+"?  \n  1. Yes "+testKey+"  \n  2. No  \n\n")
	f.out.Observe(AgentEvent{Kind: AgentChanged, Agent: f.setStatus(a, domain.StatusBlocked)})
	f.fire(t, 1)
	sent := f.tg.Sent()
	if len(sent) != 1 || sent[0].Text != "  Use key sk-…uvwx?\n  1. Yes sk-…uvwx\n  2. No" || !sent[0].Notify {
		t.Fatalf("Sent = %+v", sent)
	}
	if got := f.tg.Buttons(1000); len(got) != 2 || got[0].Text != "1️⃣ Yes sk-…uvwx" {
		t.Fatalf("Buttons = %+v", got)
	}
	// The duplicate check hashes the raw screen: the same screen again is
	// still skipped.
	f.out.Observe(AgentEvent{Kind: AgentChanged, Agent: f.setStatus(a, domain.StatusWorking)})
	f.out.Observe(AgentEvent{Kind: AgentChanged, Agent: f.setStatus(a, domain.StatusBlocked)})
	f.fire(t, 1)
	if n := len(f.tg.Sent()); n != 1 {
		t.Fatalf("duplicate posted: %d sends", n)
	}
}

func TestOutboundScreenAllDocumentIsRedacted(t *testing.T) {
	f := newBridgeFixture(t)
	a := f.add(t, "w1:p1", "t1", "a", domain.StatusWorking)
	f.capture.Observe(AgentEvent{Kind: AgentChanged, Agent: a})
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, fmt.Sprintf("%03d %s %s", i, testKey, strings.Repeat("ж", 60)))
	}
	f.herdr.SetScreen("w1:p1", strings.Join(lines, "\n"))
	if err := f.out.ScreenAll(f.ctx, a.Key); err != nil {
		t.Fatal(err)
	}
	docs := f.tg.Documents()
	if len(docs) != 1 || strings.Contains(string(docs[0].Data), testKey) || strings.Count(string(docs[0].Data), "sk-…uvwx") != 200 {
		t.Fatalf("document not redacted: %d docs", len(docs))
	}
}

func TestOutboundRedactionOffPostsRaw(t *testing.T) {
	f := newBridgeFixture(t)
	if err := f.opts.Set(f.ctx, domain.OptionRedact, "false", 1); err != nil {
		t.Fatal(err)
	}
	a := f.add(t, "p1", "t1", "reviewer", domain.StatusWorking)
	f.herdr.SetScreen("p1", "key "+testKey)
	if err := f.out.Screen(f.ctx, a.Key, 0); err != nil {
		t.Fatal(err)
	}
	if sent := f.tg.Sent(); len(sent) != 1 || sent[0].Text != "key "+testKey {
		t.Fatalf("Sent = %+v", sent)
	}
}

// TestRedactingGatewayMasksTopicNamesAndToasts: an agent label, a topic
// rename and a button toast are text that leaves the machine too.
func TestRedactingGatewayMasksTopicNamesAndToasts(t *testing.T) {
	fake := testkit.NewFakeTelegram(nil)
	tg := newRedactingGateway(fake, domain.NewRedactor(testBotToken), nil, nil)
	ctx := context.Background()
	topic, err := tg.CreateTopic(ctx, "deploy "+testKey, domain.StatusIdle)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fake.Topic(topic.ThreadID); strings.Contains(got.Name, testKey) || got.Name != "deploy sk-…uvwx" {
		t.Fatalf("created topic name = %q", got.Name)
	}
	name := "work " + testKey
	if err := tg.EditTopic(ctx, topic.ThreadID, domain.TopicPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if got, _ := fake.Topic(topic.ThreadID); strings.Contains(got.Name, testKey) {
		t.Fatalf("edited topic name = %q", got.Name)
	}
	if name != "work "+testKey {
		t.Fatal("caller's patch was rewritten in place")
	}
	if err := tg.AnswerButton(ctx, "cb", "copied "+testKey); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Calls() {
		if strings.Contains(c, testKey) {
			t.Fatalf("secret reached Telegram: %q", c)
		}
	}
}

// TestRedactingGatewayKeepsHTMLValid: masking runs on rendered HTML, so a
// value pattern must never swallow a closing tag.
func TestRedactingGatewayKeepsHTMLValid(t *testing.T) {
	fake := testkit.NewFakeTelegram(nil)
	tg := newRedactingGateway(fake, domain.NewRedactor(), nil, nil)
	ctx := context.Background()
	id, err := tg.Send(ctx, domain.Outgoing{Text: "<pre>token=abcdefgh</pre> and <b>x</b>", HTML: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.Sent()[0].Text; got != "<pre>token=[redacted]</pre> and <b>x</b>" {
		t.Fatalf("Send HTML = %q", got)
	}
	if err := tg.EditText(ctx, id, `<code>password="hunter2secret"</code>`, true, nil); err != nil {
		t.Fatal(err)
	}
	p := PrivateRedactor{DestinationTelegram: fake}
	if _, err := p.SendAt(ctx, domain.TopicAddress{ChatID: 5}, domain.Outgoing{Text: "<pre>token=abcdefgh</pre>", HTML: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := fake.Destination(5).Sent()[0].Text; got != "<pre>token=[redacted]</pre>" {
		t.Fatalf("private SendAt HTML = %q", got)
	}
}
