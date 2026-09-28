package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// Both ids are made up. They are UUIDv7-shaped (version nibble 7) so the
// day-directory shortcut is exercised, and share a prefix like a real
// thread and its guardian helper do.
const (
	codexTestID     = "01a0de3a-aaaa-7aaa-8aaa-aaaaaaaaaaaa"
	codexHelperID   = "01a0de3a-bbbb-7bbb-8bbb-bbbbbbbbbbbb"
	codexTestSecret = "PRIVATE-TOOL-OUTPUT"
)

var codexTestTuple = domain.SessionTuple{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: codexTestID}

const (
	codexT0 = int64(1790000000) // turn started (unix seconds)
	codexT1 = int64(1790000090) // turn completed
)

// codexLine renders one rollout record.
func codexLine(typ string, payload map[string]any) string {
	b, err := json.Marshal(map[string]any{"ordinal": 1, "timestamp": time.Unix(codexT1, 0).UTC().Format(time.RFC3339Nano), "type": typ, "payload": payload})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func codexStarted(turn string) string {
	return codexLine("event_msg", map[string]any{"type": "task_started", "turn_id": turn, "started_at": codexT0})
}

func codexContext(turn, model string) string {
	return codexLine("turn_context", map[string]any{"turn_id": turn, "model": model, "effort": "high"})
}

func codexUsage(turn string, out int) string {
	return codexLine("token_usage_record", map[string]any{"turn_id": turn, "turn_token_usage": map[string]any{"output_tokens": out, "input_tokens": 999999}})
}

func codexToolOutput(size int) string {
	return codexLine("response_item", map[string]any{"type": "custom_tool_call_output", "output": codexTestSecret + strings.Repeat("x", size)})
}

func codexComplete(turn, text string) string {
	return codexLine("event_msg", map[string]any{"type": "task_complete", "turn_id": turn, "last_agent_message": text, "started_at": codexT0, "completed_at": codexT1, "duration_ms": 90000})
}

// codexTurn is a finished turn: start, context, chatter, usage, answer.
func codexTurn(turn, model, answer string, out int) []string {
	return []string{
		codexStarted(turn), codexContext(turn, model),
		codexLine("response_item", map[string]any{"type": "message", "role": "assistant", "phase": "commentary", "content": []any{map[string]any{"type": "output_text", "text": "working on it"}}}),
		codexToolOutput(64), codexUsage(turn, out),
		codexComplete(turn, answer),
	}
}

type codexFixture struct {
	t      *testing.T
	home   string
	tuple  domain.SessionTuple
	digest string
	logBuf bytes.Buffer
	homed  int
}

func newCodexFixture(t *testing.T) *codexFixture {
	t.Helper()
	return &codexFixture{t: t, home: t.TempDir(), tuple: codexTestTuple, digest: codexTestTuple.Digest()}
}

// rolloutDir is the day directory Codex would use for id.
func (f *codexFixture) rolloutDir(id string) string {
	dirs := codexDayDirs(filepath.Join(f.home, ".codex", "sessions"), id)
	if len(dirs) == 0 {
		f.t.Fatalf("no day directory for %s", id)
	}
	return dirs[0]
}

// write puts a rollout for id under its day directory.
func (f *codexFixture) write(id string, lines ...string) string {
	f.t.Helper()
	return f.writeIn(f.rolloutDir(id), id, lines...)
}

func (f *codexFixture) writeIn(dir, id string, lines ...string) string {
	f.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-09-26T09-59-58-"+id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *codexFixture) reader() *CodexReader {
	session := func(context.Context, string) (domain.SessionTuple, error) { return f.tuple, nil }
	home := func() (string, error) { f.homed++; return f.home, nil }
	r := newCodexReader(session, home, func() time.Time { return time.Unix(codexT1+5, 0) },
		slog.New(slog.NewJSONHandler(&f.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return r
}

func (f *codexFixture) agent() domain.Agent {
	return domain.Agent{Key: domain.Key{PaneID: "p1", TerminalID: "t1", SessionDigest: f.digest}, Kind: "codex"}
}

// noLeak fails if text carries the thread id, the helper id or a path.
func noLeak(t *testing.T, what, text string) {
	t.Helper()
	for _, secret := range []string{codexTestID, codexHelperID, strings.ToUpper(codexTestID), "rollout-", ".jsonl", "sessions", codexTestSecret} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaks %q: %s", what, secret, text)
		}
	}
}

func TestCodexLastReply(t *testing.T) {
	f := newCodexFixture(t)
	f.write(codexTestID, append(codexTurn("turn-old", "old-model", "old answer", 7), codexTurn("turn-new", "gpt-test", "  the **final** answer\n", 1234)...)...)
	reply, err := f.reader().LastReply(context.Background(), f.agent())
	if err != nil {
		t.Fatalf("LastReply: %v", err)
	}
	if reply.Text != "the **final** answer" {
		t.Fatalf("Text = %q, want the newest turn's answer, trimmed", reply.Text)
	}
	m := reply.Meta
	if m.Model != "gpt-test" || m.OutputTokens != 1234 || !m.Started.Equal(time.Unix(codexT0, 0)) || !m.Ended.Equal(time.Unix(codexT1, 0)) {
		t.Fatalf("Meta = %+v", m)
	}
	if d, ok := m.Duration(); !ok || d != 90*time.Second {
		t.Fatalf("Duration = %v, %v", d, ok)
	}
	if len(m.Files) != 0 {
		t.Fatalf("Files = %v, want none: Codex rollouts do not name edited files reliably", m.Files)
	}
	if !reply.Written.Equal(time.Unix(codexT1, 0)) || reply.Age != 5*time.Second {
		t.Fatalf("Written = %v, Age = %v", reply.Written, reply.Age)
	}
	noLeak(t, "Reply.Source", reply.Source)
	noLeak(t, "log", f.logBuf.String())
}

func TestCodexIgnoresOtherTurnsUsage(t *testing.T) {
	f := newCodexFixture(t)
	f.write(codexTestID, codexStarted("turn-a"), codexContext("turn-a", "model-a"), codexUsage("turn-a", 42),
		codexContext("turn-b", "model-b"), codexUsage("turn-b", 555), codexComplete("turn-a", "answer"))
	reply, err := f.reader().LastReply(context.Background(), f.agent())
	if err != nil || reply.Meta.OutputTokens != 42 || reply.Meta.Model != "model-a" {
		t.Fatalf("reply = %+v, err = %v; want tokens and model of the answered turn only", reply.Meta, err)
	}
}

// TestCodexNestedTurnStartDoesNotEndTheWalk covers another turn's start
// inside the answered turn: only the answered turn's own start ends the
// walk, so the model recorded at the top of the turn is still found.
func TestCodexNestedTurnStartDoesNotEndTheWalk(t *testing.T) {
	f := newCodexFixture(t)
	f.write(codexTestID, codexStarted("turn-a"), codexContext("turn-a", "model-a"), codexStarted("turn-nested"),
		codexUsage("turn-a", 42), codexComplete("turn-a", "answer"))
	reply, err := f.reader().LastReply(context.Background(), f.agent())
	if err != nil || reply.Meta.Model != "model-a" || reply.Text != "answer" {
		t.Fatalf("reply = %q, meta = %+v, err = %v", reply.Text, reply.Meta, err)
	}
}

func TestCodexNoReply(t *testing.T) {
	interrupted := codexLine("event_msg", map[string]any{"type": "turn_aborted", "turn_id": "turn-b", "reason": "interrupted", "started_at": codexT0, "completed_at": codexT1})
	cases := map[string][]string{
		"turn still running":               append(codexTurn("turn-a", "m", "old answer", 1), codexStarted("turn-b"), codexContext("turn-b", "m")),
		"turn interrupted":                 append(codexTurn("turn-a", "m", "old answer", 1), codexStarted("turn-b"), interrupted),
		"interrupt without its turn start": append(codexTurn("turn-a", "m", "OLD ANSWER", 1), interrupted),
		"empty final answer":               {codexStarted("turn-a"), codexComplete("turn-a", "   ")},
		"null final answer":                {codexStarted("turn-a"), codexLine("event_msg", map[string]any{"type": "task_complete", "turn_id": "turn-a", "last_agent_message": nil, "completed_at": codexT1})},
		"no turn boundary at all":          {codexLine("response_item", map[string]any{"type": "message", "role": "user"})},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCodexFixture(t)
			f.write(codexTestID, lines...)
			_, err := f.reader().LastReply(context.Background(), f.agent())
			if !errors.Is(err, domain.ErrNoReply) {
				t.Fatalf("err = %v, want ErrNoReply", err)
			}
			noLeak(t, "error", err.Error())
		})
	}
}

func TestCodexWrongThread(t *testing.T) {
	// Only a helper thread that shares the id prefix exists: it must never
	// be read for the pane's session.
	f := newCodexFixture(t)
	f.write(codexHelperID, codexTurn("turn-h", "m", "HELPER ANSWER", 1)...)
	_, err := f.reader().LastReply(context.Background(), f.agent())
	if !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
	// With both present, the exact thread's file wins.
	f.write(codexTestID, codexTurn("turn-a", "m", "THE RIGHT ANSWER", 1)...)
	reply, err := f.reader().LastReply(context.Background(), f.agent())
	if err != nil || reply.Text != "THE RIGHT ANSWER" {
		t.Fatalf("reply = %q, err = %v", reply.Text, err)
	}
}

func TestCodexSessionChecks(t *testing.T) {
	other := codexTestTuple
	other.Value = "01a0de3a-cccc-7ccc-8ccc-cccccccccccc"
	cases := map[string]func(*codexFixture, *domain.Agent){
		"unsupported kind":      func(_ *codexFixture, a *domain.Agent) { a.Kind = "claude" },
		"no session":            func(f *codexFixture, _ *domain.Agent) { f.tuple = domain.SessionTuple{} },
		"not an id session":     func(f *codexFixture, _ *domain.Agent) { f.tuple.Kind = "path" },
		"another agent's tuple": func(f *codexFixture, _ *domain.Agent) { f.tuple.Agent = "opencode" },
		"incomplete tuple":      func(f *codexFixture, _ *domain.Agent) { f.tuple.Source = "" },
		"pane runs another one": func(f *codexFixture, _ *domain.Agent) { f.tuple = other },
		"sessionless topic key": func(_ *codexFixture, a *domain.Agent) { a.SessionDigest = "" },
		"traversal in the value": func(f *codexFixture, a *domain.Agent) {
			f.tuple.Value = "../../../etc/passwd"
			a.SessionDigest = f.tuple.Digest()
		},
		"path separators": func(f *codexFixture, a *domain.Agent) { f.tuple.Value = `a\b/c`; a.SessionDigest = f.tuple.Digest() },
		"not a uuid": func(f *codexFixture, a *domain.Agent) {
			f.tuple.Value = "session-1"
			a.SessionDigest = f.tuple.Digest()
		},
		"wildcard": func(f *codexFixture, a *domain.Agent) { f.tuple.Value = "*"; a.SessionDigest = f.tuple.Digest() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCodexFixture(t)
			f.write(codexTestID, codexTurn("turn-a", "m", "answer", 1)...)
			agent := f.agent()
			mutate(f, &agent)
			_, err := f.reader().LastReply(context.Background(), agent)
			if !errors.Is(err, domain.ErrNoReply) {
				t.Fatalf("err = %v, want ErrNoReply", err)
			}
			if f.homed != 0 {
				t.Fatalf("the file system was consulted (home looked up %d times) before the session was accepted", f.homed)
			}
		})
	}
}

func TestCodexUpperCaseIDIsAccepted(t *testing.T) {
	f := newCodexFixture(t)
	f.write(codexTestID, codexTurn("turn-a", "m", "answer", 1)...)
	f.tuple.Value = strings.ToUpper(codexTestID)
	agent := f.agent()
	agent.SessionDigest = f.tuple.Digest()
	if _, err := f.reader().LastReply(context.Background(), agent); err != nil {
		t.Fatalf("LastReply: %v", err)
	}
}

func TestCodexLookupFailureAndCancellation(t *testing.T) {
	f := newCodexFixture(t)
	r := newCodexReader(func(context.Context, string) (domain.SessionTuple, error) {
		return domain.SessionTuple{}, errors.New("herdr down")
	},
		func() (string, error) { return f.home, nil }, time.Now, nil)
	if _, err := r.LastReply(context.Background(), f.agent()); !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("lookup failure err = %v, want ErrNoReply", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.reader().LastReply(ctx, f.agent()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err = %v, want context.Canceled", err)
	}
	if _, err := f.reader().LastReply(context.Background(), f.agent()); !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("no rollout at all: err = %v, want ErrNoReply", err)
	}
}

func TestCodexFindsRolloutOutsideTheDayDirectories(t *testing.T) {
	f := newCodexFixture(t)
	f.writeIn(filepath.Join(f.home, ".codex", "sessions", "2020", "01", "01"), codexTestID, codexTurn("turn-a", "m", "found by the walk", 1)...)
	reply, err := f.reader().LastReply(context.Background(), f.agent())
	if err != nil || reply.Text != "found by the walk" {
		t.Fatalf("reply = %q, err = %v", reply.Text, err)
	}
}

func TestCodexWalkIsBounded(t *testing.T) {
	f := newCodexFixture(t)
	root := filepath.Join(f.home, ".codex", "sessions", "2020", "01", "01")
	for i := 0; i < 12; i++ {
		f.writeIn(root, fmt.Sprintf("01a0de3a-0000-7000-8000-%012d", i), codexTurn("t", "m", "x", 1)...)
	}
	f.writeIn(filepath.Join(f.home, ".codex", "sessions", "2021", "01", "01"), codexTestID, codexTurn("turn-a", "m", "too far", 1)...)
	old := codexWalkLimit
	codexWalkLimit = 5
	defer func() { codexWalkLimit = old }()
	_, err := f.reader().LastReply(context.Background(), f.agent())
	if !errors.Is(err, domain.ErrNoReply) || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("err = %v, want the walk limit", err)
	}
}

func TestCodexSymlinkedRolloutIsRefused(t *testing.T) {
	f := newCodexFixture(t)
	real := f.writeIn(filepath.Join(f.home, "elsewhere"), codexTestID, codexTurn("turn-a", "m", "SYMLINK TARGET", 1)...)
	dir := f.rolloutDir(codexTestID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "rollout-2026-09-26T09-59-58-"+codexTestID+".jsonl")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if _, err := f.reader().LastReply(context.Background(), f.agent()); !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply for a symlink", err)
	}
}

func TestCodexHugeLinesAreNotParsed(t *testing.T) {
	// A 3 MiB tool output that is not even JSON, and no marker in it: it is
	// walked over, never decoded and never counted as a skipped record.
	f := newCodexFixture(t)
	f.write(codexTestID, codexStarted("turn-a"), codexContext("turn-a", "m"), strings.Repeat("{not json ", 300000), codexComplete("turn-a", "answer"))
	r := f.reader()
	r.maxScan = 8 << 20
	reply, err := r.LastReply(context.Background(), f.agent())
	if err != nil || reply.Text != "answer" || reply.Meta.Model != "m" {
		t.Fatalf("reply = %+v, err = %v", reply, err)
	}
	if !strings.Contains(f.logBuf.String(), `"skipped_json":0`) {
		t.Fatalf("log = %s", f.logBuf.String())
	}
	// A broken line that does carry a marker is counted and skipped.
	f.write(codexTestID, codexStarted("turn-a"), `{"type":"event_msg","payload":{"type":"task_started" BROKEN`, codexComplete("turn-a", "answer"))
	f.logBuf.Reset()
	if _, err := f.reader().LastReply(context.Background(), f.agent()); err != nil || !strings.Contains(f.logBuf.String(), `"skipped_json":1`) {
		t.Fatalf("err = %v, log = %s", err, f.logBuf.String())
	}
}

func TestCodexAnswerSurvivesABudgetCutBeforeTheTurnStart(t *testing.T) {
	f := newCodexFixture(t)
	f.write(codexTestID, codexStarted("turn-a"), codexContext("turn-a", "m"), codexToolOutput(200000), codexUsage("turn-a", 9), codexComplete("turn-a", "still here"))
	r := f.reader()
	r.maxScan = 100 << 10
	reply, err := r.LastReply(context.Background(), f.agent())
	if err != nil || reply.Text != "still here" {
		t.Fatalf("reply = %q, err = %v", reply.Text, err)
	}
	if reply.Meta.Model != "" || reply.Meta.OutputTokens != 9 || !reply.Meta.Started.Equal(time.Unix(codexT0, 0)) {
		t.Fatalf("Meta = %+v, want partial meta: no model, tokens and times from the answer record", reply.Meta)
	}
	// No turn boundary within the budget: nothing to answer with.
	f.write(codexTestID, codexToolOutput(200000), codexToolOutput(200000))
	if _, err := r.LastReply(context.Background(), f.agent()); !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
}

// codexLeakyReader fails every read with an error that names the rollout,
// as an *fs.PathError does.
type codexLeakyReader struct{}

func (codexLeakyReader) ReadAt([]byte, int64) (int, error) {
	return 0, &os.PathError{Op: "read", Path: `C:\Users\x\.codex\sessions\2026\09\26\rollout-2026-09-26T09-59-58-` + codexTestID + `.jsonl`, Err: errors.New("device error")}
}

func TestCodexReadFailureNamesNothing(t *testing.T) {
	_, _, _, err := codexLastReplyFrom(codexLeakyReader{}, 1<<20, 1<<20)
	if !errors.Is(err, domain.ErrNoReply) {
		t.Fatalf("err = %v, want ErrNoReply", err)
	}
	noLeak(t, "read error", err.Error())
	if strings.Contains(err.Error(), "device error") || strings.Contains(err.Error(), `C:\`) {
		t.Fatalf("read error leaks its cause: %v", err)
	}
}

func TestCodexDayDirs(t *testing.T) {
	if dirs := codexDayDirs("root", "01a0de3a-aaaa-4aaa-8aaa-aaaaaaaaaaaa"); dirs != nil {
		t.Fatalf("a version 4 uuid must not guess day directories: %v", dirs)
	}
	dirs := codexDayDirs("root", codexTestID)
	if len(dirs) < 3 {
		t.Fatalf("dirs = %v, want the creation day and its neighbours", dirs)
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			t.Fatalf("duplicate day directory %s", d)
		}
		seen[d] = true
	}
}
