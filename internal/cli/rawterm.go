package cli

import (
	"io"
	"log/slog"
	"os"
	"sync"

	"golang.org/x/term"
)

// rawPane switches an overlay's terminal to raw input so a bare Esc or q
// reaches watchQuitKeys without Enter (issue #48). Raw mode also stops the
// terminal turning "\n" into "\r\n" on output, so the returned context
// writes, and logs, through crlfWriter. Outside a terminal, or when raw mode
// cannot be entered, rc comes back unchanged and the keys need Enter as
// before. restore is safe to call more than once.
func rawPane(rc *runContext) (pane *runContext, restore func()) {
	f, ok := rc.stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return rc, func() {}
	}
	fd := int(f.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		rc.log.Warn("pane input stays line-buffered", slog.Any("err", err))
		return rc, func() {}
	}
	stderr := &crlfWriter{w: rc.stderr}
	pane = &runContext{
		version: rc.version,
		stdin:   rc.stdin,
		stdout:  &crlfWriter{w: rc.stdout},
		stderr:  stderr,
		log:     newLogger(stderr),
	}
	pane.log.Debug("pane input raw")
	var once sync.Once
	return pane, func() {
		once.Do(func() {
			if err := term.Restore(fd, old); err != nil {
				rc.log.Warn("pane input restore failed", slog.Any("err", err))
				return
			}
			rc.log.Debug("pane input restored")
		})
	}
}

// crlfWriter writes every "\n" not already preceded by "\r" as "\r\n", the
// output translation a raw terminal no longer does.
type crlfWriter struct {
	w      io.Writer
	lastCR bool
}

func (c *crlfWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p)+8)
	for _, b := range p {
		if b == '\n' && !c.lastCR {
			out = append(out, '\r')
		}
		out = append(out, b)
		c.lastCR = b == '\r'
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
