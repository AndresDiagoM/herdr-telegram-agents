package state

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// PidFileName is the daemon's single-instance lock under the state dir.
const PidFileName = "daemon.pid"

// pidStartSlack absorbs clock granularity between a process start time and
// the pid file's modification time (Linux reports boot time in seconds).
const pidStartSlack = 5 * time.Second

// errReusedPid means the recorded pid now belongs to a process that started
// after the pid file was written.
var errReusedPid = errors.New("pid reused by another process")

// PidFile implements domain.PidFile over STATE_DIR/daemon.pid. The file is
// created with O_EXCL; when it already exists the recorded pid is checked
// with alive and a stale file is removed and the acquire retried once.
//
// The file keeps its plain "<pid>\n" format so older builds (a rollback)
// still read it. Identity comes from its modification time instead: the
// daemon writes the file after it started, so a live process with that pid
// that started later than the file is an unrelated process.
type PidFile struct {
	path    string
	alive   func(int) bool
	started func(int) (time.Time, bool)
	log     *slog.Logger

	mu    sync.Mutex
	owned int
}

var _ domain.PidFile = (*PidFile)(nil)

// NewPidFile returns a pid file inside dir. alive decides whether a pid found
// in an existing file still runs; nil treats every existing file as live.
func NewPidFile(dir string, alive func(int) bool, log *slog.Logger) *PidFile {
	if alive == nil {
		alive = func(int) bool { return true }
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &PidFile{path: filepath.Join(dir, PidFileName), alive: alive, log: log}
}

// CheckStart enables the identity check: started reports when a process
// started, false when the platform cannot tell.
func (p *PidFile) CheckStart(started func(int) (time.Time, bool)) *PidFile {
	p.started = started
	return p
}

// Path returns the pid file location.
func (p *PidFile) Path() string { return p.path }

// Acquire records pid. It returns ErrAlreadyRunning (wrapping the live pid)
// when another daemon owns the file.
func (p *PidFile) Acquire(pid int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return fmt.Errorf("mkdir for pid file: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		err := p.create(pid)
		if err == nil {
			p.owned = pid
			p.log.Debug("pid file acquired", slog.String("path", p.path), slog.Int("pid", pid))
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create pid file: %w", err)
		}
		info, readErr := p.read()
		if errors.Is(readErr, errReusedPid) {
			p.log.Warn("[FIX] pid file names a reused pid, removing", slog.String("path", p.path), slog.String("err", readErr.Error()))
		} else if readErr != nil {
			// Unreadable or empty: treat as stale and replace it.
			p.log.Warn("pid file unreadable, removing", slog.String("path", p.path), slog.String("err", readErr.Error()))
		} else if p.alive(info.PID) {
			return fmt.Errorf("%w: pid %d", domain.ErrAlreadyRunning, info.PID)
		} else {
			p.log.Warn("stale pid file removed", slog.String("path", p.path), slog.Int("pid", info.PID))
		}
		if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale pid file: %w", err)
		}
	}
	return fmt.Errorf("%w: pid file keeps reappearing", domain.ErrAlreadyRunning)
}

// create writes the pid to a temporary file and links it into place, which
// fails when the file exists. A reader never sees the file empty, so a
// concurrent Acquire cannot mistake a starting daemon's file for a stale
// one. Filesystems without hard links fall back to O_EXCL.
func (p *PidFile) create(pid int) error {
	tmp, err := os.CreateTemp(filepath.Dir(p.path), ".daemon.pid.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := fmt.Fprintf(tmp, "%d\n", pid); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	err = os.Link(tmp.Name(), p.path)
	if err == nil || errors.Is(err, os.ErrExist) {
		return err
	}
	p.log.Debug("[FIX] pid file link failed, using O_EXCL", slog.String("err", err.Error()))
	f, err := os.OpenFile(p.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		_ = f.Close()
		_ = os.Remove(p.path)
		return err
	}
	return f.Close()
}

// Read returns the recorded pid and the file's modification time, or
// ErrNotRunning when there is no file or its pid now belongs to a process
// that started after the file was written.
func (p *PidFile) Read() (domain.PidInfo, error) {
	info, err := p.read()
	if errors.Is(err, os.ErrNotExist) {
		return domain.PidInfo{}, domain.ErrNotRunning
	}
	if errors.Is(err, errReusedPid) {
		p.log.Debug("[FIX] pid file names a reused pid", slog.String("err", err.Error()))
		return domain.PidInfo{}, fmt.Errorf("%w: %w", domain.ErrNotRunning, err)
	}
	return info, err
}

func (p *PidFile) read() (domain.PidInfo, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return domain.PidInfo{}, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return domain.PidInfo{}, fmt.Errorf("pid file %s: bad content %q", p.path, strings.TrimSpace(string(data)))
	}
	st, err := os.Stat(p.path)
	if err != nil {
		return domain.PidInfo{}, err
	}
	info := domain.PidInfo{PID: pid, Since: st.ModTime()}
	if p.started == nil {
		return info, nil
	}
	start, ok := p.started(pid)
	if !ok {
		return info, nil
	}
	if start.After(info.Since.Add(pidStartSlack)) {
		return info, fmt.Errorf("%w: pid %d started %s, pid file written %s", errReusedPid, pid, start.Format(time.RFC3339), info.Since.Format(time.RFC3339))
	}
	info.Verified = true
	return info, nil
}

// Release removes the file only when it still holds the pid this process
// acquired, so a newer daemon's file is never deleted by an old one.
func (p *PidFile) Release() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owned == 0 {
		return nil
	}
	info, err := p.read()
	if errors.Is(err, os.ErrNotExist) {
		p.owned = 0
		return nil
	}
	if err == nil && info.PID != p.owned {
		p.log.Warn("pid file owned by another process, not removed",
			slog.String("path", p.path), slog.Int("pid", info.PID), slog.Int("own", p.owned))
		p.owned = 0
		return nil
	}
	if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove pid file: %w", err)
	}
	p.log.Debug("pid file released", slog.String("path", p.path), slog.Int("pid", p.owned))
	p.owned = 0
	return nil
}
