package system

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

const (
	// openCodeExportTimeout bounds one "opencode export" run; a real run
	// takes about 1.5 s, mostly runtime startup.
	openCodeExportTimeout = 10 * time.Second
	// openCodeExportMaxOutput caps the JSON a run may produce. A session
	// export is a few MB in normal use, and truncated JSON cannot be parsed,
	// so a capped run is a failure rather than a partial result.
	openCodeExportMaxOutput = 16 << 20
	// openCodeWaitDelay bounds how long a run waits for its pipes once the
	// timeout killed opencode.
	openCodeWaitDelay = time.Second
)

// OpenCodeExporter runs "opencode export <sessionID>" from the opencode
// binary on PATH and returns its JSON. The argv is fixed: the only input is
// the session id Herdr reported for the pane, passed as one argument and
// never through a shell.
type OpenCodeExporter struct {
	bin      string
	timeout  time.Duration
	maxBytes int
	log      *slog.Logger
}

// NewOpenCodeExporter returns an exporter for "opencode" on PATH.
func NewOpenCodeExporter(log *slog.Logger) *OpenCodeExporter {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &OpenCodeExporter{bin: "opencode", timeout: openCodeExportTimeout, maxBytes: openCodeExportMaxOutput, log: log}
}

// Export runs the export for sessionID. A missing binary, a timeout, a
// non-zero exit and output over the cap are all errors.
func (e *OpenCodeExporter) Export(ctx context.Context, sessionID string) ([]byte, error) {
	if sessionID == "" || strings.HasPrefix(sessionID, "-") {
		return nil, fmt.Errorf("opencode export: invalid session id")
	}
	bin, err := exec.LookPath(e.bin)
	if err != nil {
		return nil, fmt.Errorf("opencode binary not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "export", sessionID)
	cmd.WaitDelay = openCodeWaitDelay
	stdout := &limitedWriter{max: e.maxBytes}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	e.log.Debug("opencode export", slog.Int64("dur_ms", time.Since(start).Milliseconds()),
		slog.Int("bytes", stdout.buf.Len()), slog.Bool("capped", stdout.capped), slog.Any("err", runErr))
	switch {
	case runErr == nil && !stdout.capped:
		return stdout.buf.Bytes(), nil
	case stdout.capped:
		return nil, fmt.Errorf("opencode export: output over %d bytes", e.maxBytes)
	case ctx.Err() != nil:
		return nil, fmt.Errorf("opencode export: %w", context.DeadlineExceeded)
	}
	if msg := firstLine(stderr.String()); msg != "" {
		return nil, fmt.Errorf("opencode export: %w: %s", runErr, msg)
	}
	return nil, fmt.Errorf("opencode export: %w", runErr)
}
