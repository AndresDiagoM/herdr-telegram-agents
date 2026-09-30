package transcript

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

const (
	// kindCodex is the Herdr agent kind CodexReader understands.
	kindCodex = "codex"
	// codexWalkLimitDefault is the value of codexWalkLimit.
	codexWalkLimitDefault = 50000
)

// codexWalkLimit caps the directory entries visited by the fallback search
// for a rollout file, so a huge sessions tree costs a screen post and not
// a long walk. A variable so a test can lower it.
var codexWalkLimit = codexWalkLimitDefault

// codexHomeDir is Codex's data directory, relative to the user's home.
// CODEX_HOME is not honoured: environment access stays out of this
// package (see scripts/check-imports.sh).
var codexHomeDir = ".codex"

// codexThreadID is the only shape of session id used in a file name: Codex
// thread ids are lower-case UUIDs. Anything else, a path separator or ".."
// included, is refused before it can reach the file system.
var codexThreadID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Markers a rollout line must contain before it is decoded. Tool outputs
// make single lines megabytes long; a line without one of these is never
// parsed.
var (
	codexMarkComplete = []byte(`"task_complete"`)
	codexMarkStarted  = []byte(`"task_started"`)
	codexMarkAborted  = []byte(`"turn_aborted"`)
	codexMarkRollback = []byte(`"thread_rolled_back"`)
	codexMarkContext  = []byte(`"turn_context"`)
	codexMarkUsage    = []byte(`"token_usage_record"`)
)

// CodexReader implements domain.ReplySource for Codex. Codex writes every
// thread to ~/.codex/sessions/YYYY/MM/DD/rollout-<time>-<thread id>.jsonl,
// and Herdr reports the thread id as the pane's agent_session, so the
// reader opens exactly that file: no guessing from the working directory.
// The reply is the turn's task_complete record, which carries the final
// answer as last_agent_message.
//
// Like OpenCodeReader, the session tuple comes from Herdr at read time and
// is used for that one lookup: it is never stored or logged, and neither is
// the rollout path, which contains the thread id. A tuple whose digest
// differs from the topic's key means the pane now runs another session, so
// the reader answers ErrNoReply rather than post that session's reply into
// this topic.
type CodexReader struct {
	session func(ctx context.Context, paneID string) (domain.SessionTuple, error)
	home    func() (string, error)
	now     func() time.Time
	log     *slog.Logger
	maxScan int64
}

// NewCodexReader wires the reader over Herdr's session lookup and the
// current user's home directory.
func NewCodexReader(session func(context.Context, string) (domain.SessionTuple, error), log *slog.Logger) *CodexReader {
	return newCodexReader(session, os.UserHomeDir, time.Now, log)
}

// newCodexReader takes the home and clock sources so tests can point the
// reader at a temporary directory.
func newCodexReader(session func(context.Context, string) (domain.SessionTuple, error),
	home func() (string, error), now func() time.Time, log *slog.Logger) *CodexReader {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &CodexReader{session: session, home: home, now: now, log: log, maxScan: defaultMaxScan}
}

// LastReply returns the final answer of the pane's last completed Codex
// turn. Every failure is domain.ErrNoReply wrapped with a reason that
// carries no session value and no path; the caller falls back to the
// screen. A turn that is still running or was interrupted has no answer.
func (r *CodexReader) LastReply(ctx context.Context, agent domain.Agent) (domain.Reply, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reply{}, err
	}
	if agent.Kind != kindCodex {
		return domain.Reply{}, fmt.Errorf("%w: unsupported agent %q", domain.ErrNoReply, agent.Kind)
	}
	tuple, err := r.session(ctx, agent.PaneID)
	if err != nil {
		return domain.Reply{}, classifyCodexError(ctx, err, "session lookup failed")
	}
	if err := ctx.Err(); err != nil {
		return domain.Reply{}, err
	}
	if tuple.Agent != kindCodex || tuple.Kind != "id" || tuple.Value == "" {
		return domain.Reply{}, fmt.Errorf("%w: herdr reports no codex session id for the pane", domain.ErrNoReply)
	}
	digest := tuple.Digest()
	if digest == "" {
		return domain.Reply{}, fmt.Errorf("%w: herdr reports an incomplete session for the pane", domain.ErrNoReply)
	}
	if agent.SessionDigest == "" || digest != agent.SessionDigest {
		return domain.Reply{}, fmt.Errorf("%w: the pane now runs another session", domain.ErrNoReply)
	}
	id := strings.ToLower(tuple.Value)
	if !codexThreadID.MatchString(id) {
		return domain.Reply{}, fmt.Errorf("%w: the codex session id is not a thread uuid", domain.ErrNoReply)
	}
	home, err := r.home()
	if err != nil {
		return domain.Reply{}, fmt.Errorf("%w: no home directory", domain.ErrNoReply)
	}
	path, err := findCodexRollout(ctx, filepath.Join(home, codexHomeDir, "sessions"), id)
	if err != nil {
		return domain.Reply{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return domain.Reply{}, fmt.Errorf("%w: rollout could not be opened", domain.ErrNoReply)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return domain.Reply{}, fmt.Errorf("%w: rollout could not be read", domain.ErrNoReply)
	}
	text, meta, stats, err := codexLastReplyFrom(f, info.Size(), r.maxScan)
	if err != nil {
		return domain.Reply{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Reply{}, err
	}
	written := meta.Ended
	if written.IsZero() {
		written = info.ModTime()
	}
	age := r.now().Sub(written)
	r.log.Debug("codex reply found", slog.String("pane", agent.PaneID),
		slog.Int("lines", stats.lines), slog.Int64("bytes", stats.bytes), slog.Int("skipped_json", stats.skipped),
		slog.Int("chars", len(text)), slog.Int64("age_ms", age.Milliseconds()),
		slog.String("model", meta.Model), slog.Int("output_tokens", meta.OutputTokens))
	return domain.Reply{Text: text, Source: "codex rollout", Age: age, Written: written, Meta: meta}, nil
}

func classifyCodexError(ctx context.Context, err error, category string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %s", domain.ErrNoReply, category)
}

// findCodexRollout returns the rollout file of thread id under root. Codex
// names it rollout-<local time>-<id>.jsonl inside the day directory of the
// thread's creation, and thread ids are UUIDv7, whose first 48 bits are
// that creation time in milliseconds: the days around it (in UTC and in
// local time, the file name's zone) are tried first, then the whole tree.
// Only a regular file whose name ends in exactly "-<id>.jsonl" matches, so
// a helper thread that shares an id prefix, or a symlink, never does.
func findCodexRollout(ctx context.Context, root, id string) (string, error) {
	suffix := "-" + id + ".jsonl"
	matches := func(e fs.DirEntry) bool {
		name := e.Name()
		return e.Type().IsRegular() && strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, suffix)
	}
	for _, dir := range codexDayDirs(root, id) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if matches(e) {
				return filepath.Join(dir, e.Name()), nil
			}
		}
	}
	visited := 0
	found := ""
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if visited++; visited > codexWalkLimit {
			return errCodexWalkLimit
		}
		if visited%256 == 0 {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
		}
		if matches(e) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	switch {
	case found != "":
		return found, nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "", err
	case errors.Is(err, errCodexWalkLimit):
		return "", fmt.Errorf("%w: too many session files to search", domain.ErrNoReply)
	}
	return "", fmt.Errorf("%w: no rollout file for the codex session", domain.ErrNoReply)
}

var errCodexWalkLimit = errors.New("codex sessions walk limit")

// codexDayDirs lists the session day directories worth trying first for
// thread id: the days around the UUIDv7 creation time, UTC and local.
// A UUID that is not version 7 yields none.
func codexDayDirs(root, id string) []string {
	raw, err := hex.DecodeString(strings.ReplaceAll(id[:13], "-", ""))
	if err != nil || len(raw) != 6 || id[14] != '7' {
		return nil
	}
	var ms int64
	for _, b := range raw {
		ms = ms<<8 | int64(b)
	}
	created := time.UnixMilli(ms)
	seen := map[string]bool{}
	var dirs []string
	for _, loc := range []*time.Location{time.UTC, time.Local} {
		for _, shift := range []int{0, -1, 1} {
			d := created.In(loc).AddDate(0, 0, shift)
			dir := filepath.Join(root, d.Format("2006"), d.Format("01"), d.Format("02"))
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs
}

// codexRecord is the slice of a rollout line the reader decodes. Codex
// writes many record types; only the turn boundaries, the turn context and
// the token usage are read.
type codexRecord struct {
	Type      string       `json:"type"`
	Timestamp string       `json:"timestamp"`
	Payload   codexPayload `json:"payload"`
}

type codexPayload struct {
	Type             string  `json:"type"`
	TurnID           string  `json:"turn_id"`
	LastAgentMessage *string `json:"last_agent_message"`
	StartedAt        int64   `json:"started_at"`
	NumTurns         *int64  `json:"num_turns"`
	CompletedAt      int64   `json:"completed_at"`
	Model            string  `json:"model"`
	TurnTokenUsage   struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"turn_token_usage"`
}

// codexLastReplyFrom walks a rollout of size bytes backwards. The first
// turn boundary it meets decides: a task_started means the turn is still
// running, a turn_aborted that it was interrupted and a thread_rolled_back
// that the newest turns were removed, none has an answer; a task_complete
// carries the answer. The walk then goes on to that turn's task_started for the model and the output tokens; when the
// budget or the file runs out first, the answer found is returned with the
// partial meta rather than an error.
func codexLastReplyFrom(f io.ReaderAt, size, budget int64) (string, domain.TurnMeta, scanStats, error) {
	f = codexReadAt{f}
	var stats scanStats
	var meta domain.TurnMeta
	// A record Codex is still writing has no newline yet: the newest turn
	// boundary may be that half line, and walking past it would answer with
	// the previous turn's reply.
	if size > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], size-1); err != nil && !errors.Is(err, io.EOF) {
			return "", domain.TurnMeta{}, stats, fmt.Errorf("%w: the rollout could not be read", domain.ErrNoReply)
		}
		if last[0] != '\n' {
			return "", domain.TurnMeta{}, stats, fmt.Errorf("%w: the rollout ends mid-record", domain.ErrNoReply)
		}
	}
	var text, turnID string
	var haveOutcome, haveTokens bool
	visit := func(line []byte) error {
		stats.lines++
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return nil
		}
		if !haveOutcome {
			if !bytes.Contains(line, codexMarkComplete) && !bytes.Contains(line, codexMarkStarted) && !bytes.Contains(line, codexMarkAborted) && !bytes.Contains(line, codexMarkRollback) {
				return nil
			}
		} else if !bytes.Contains(line, codexMarkUsage) && !bytes.Contains(line, codexMarkContext) && !bytes.Contains(line, codexMarkStarted) {
			return nil
		}
		var rec codexRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			if !haveOutcome {
				// The newest turn boundary cannot be read (a changed field
				// type, say): skipping it would answer with an older turn.
				return fmt.Errorf("%w: the newest codex turn record is unreadable", domain.ErrNoReply)
			}
			stats.skipped++
			return nil
		}
		p := rec.Payload
		if !haveOutcome {
			if rec.Type != "event_msg" {
				return nil
			}
			switch p.Type {
			case "task_started":
				return fmt.Errorf("%w: the codex turn is still running", domain.ErrNoReply)
			case "turn_aborted":
				return fmt.Errorf("%w: the last codex turn was interrupted", domain.ErrNoReply)
			case "thread_rolled_back":
				// Codex drops the newest user turns when it replays a
				// rollback, so a rollback newer than the last task_complete
				// removed that turn: its answer is no longer part of the
				// conversation. Only an explicit zero is a no-op.
				if p.NumTurns != nil && *p.NumTurns == 0 {
					return nil
				}
				return fmt.Errorf("%w: the last codex turn was rolled back", domain.ErrNoReply)
			case "task_complete":
				if p.LastAgentMessage == nil || strings.TrimSpace(*p.LastAgentMessage) == "" {
					return fmt.Errorf("%w: the last codex turn has no final answer", domain.ErrNoReply)
				}
				haveOutcome = true
				text, turnID = strings.TrimSpace(*p.LastAgentMessage), p.TurnID
				meta.Started = codexUnix(p.StartedAt)
				meta.Ended = codexUnix(p.CompletedAt)
				if meta.Ended.IsZero() {
					meta.Ended = parseStamp(rec.Timestamp)
				}
			}
			return nil
		}
		switch {
		case rec.Type == "token_usage_record" && p.TurnID == turnID && !haveTokens:
			meta.OutputTokens, haveTokens = p.TurnTokenUsage.OutputTokens, true
		case rec.Type == "turn_context" && p.TurnID == turnID && p.Model != "" && meta.Model == "":
			meta.Model = p.Model
		case rec.Type == "event_msg" && p.Type == "task_started" && p.TurnID == turnID:
			if meta.Started.IsZero() {
				meta.Started = codexUnix(p.StartedAt)
			}
			return errStop
		}
		return nil
	}
	bytesRead, err := walkBack(f, size, budget, visit)
	stats.bytes = bytesRead
	switch {
	case errors.Is(err, errStop):
		return text, meta, stats, nil
	case haveOutcome:
		// The turn start is beyond the budget, the file start or a read
		// failure: the answer found is worth more than the missing meta.
		stats.readErr = err
		return text, meta, stats, nil
	case err != nil:
		return "", domain.TurnMeta{}, stats, err
	case bytesRead >= budget && size > budget:
		return "", domain.TurnMeta{}, stats, fmt.Errorf("%w: no turn boundary within the last %d bytes", domain.ErrNoReply, budget)
	}
	return "", domain.TurnMeta{}, stats, fmt.Errorf("%w: the rollout has no completed turn", domain.ErrNoReply)
}

// codexReadAt hides the reason of a failed read: a *fs.PathError carries the
// rollout path, which contains the thread id, and walkBack copies the error
// text into its own. Callers see only that the read failed.
type codexReadAt struct{ io.ReaderAt }

func (r codexReadAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, errors.New("read failed")
	}
	return n, err
}

// codexUnix converts Codex's second timestamps; zero stays zero.
func codexUnix(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
