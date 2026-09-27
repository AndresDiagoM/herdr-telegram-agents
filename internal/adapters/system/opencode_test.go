package system

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeOpenCode writes a shell stand-in for the opencode binary.
func fakeOpenCode(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in is Unix only")
	}
	script := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestOpenCodeExporterRunsExport(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	e.bin = fakeOpenCode(t, `[ "$1" = export ] && [ "$2" = ses_abc ] && [ $# -eq 2 ] || exit 3
echo '{"messages":[]}'
`)
	out, err := e.Export(context.Background(), "ses_abc")
	if err != nil || strings.TrimSpace(string(out)) != `{"messages":[]}` {
		t.Fatalf("Export = %q, %v", out, err)
	}
}

func TestOpenCodeExporterDiscardsStderr(t *testing.T) {
	for _, exit := range []string{"0", "1"} {
		t.Run(exit, func(t *testing.T) {
			var logs bytes.Buffer
			e := NewOpenCodeExporter(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			e.bin = fakeOpenCode(t, "printf '{\"messages\":[]}'\nprintf 'ses_private secret_private' >&2\nhead -c 1048577 /dev/zero >&2\nexit "+exit+"\n")
			out, err := e.Export(context.Background(), "ses_abc")
			if exit == "0" && (err != nil || string(out) != `{"messages":[]}`) {
				t.Fatalf("success = %q, %v", out, err)
			}
			if exit == "1" && (err == nil || !strings.Contains(err.Error(), "exit code 1") || out != nil) {
				t.Fatalf("failure = %q, %v", out, err)
			}
			for _, secret := range []string{"ses_private", "secret_private"} {
				if strings.Contains(logs.String(), secret) || err != nil && strings.Contains(err.Error(), secret) {
					t.Fatalf("secret leaked: %s", secret)
				}
			}
		})
	}
}

func TestOpenCodeExporterCapIsAnError(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	e.bin = fakeOpenCode(t, "i=0; while [ $i -lt 100 ]; do echo 'padding padding padding'; i=$((i+1)); done\n")
	e.maxBytes = 64
	if _, err := e.Export(context.Background(), "ses_abc"); err == nil || !strings.Contains(err.Error(), "output over") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenCodeExporterExactCap(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	e.bin = fakeOpenCode(t, "printf 12345678\n")
	e.maxBytes = 8
	if out, err := e.Export(context.Background(), "ses_abc"); err != nil || string(out) != "12345678" {
		t.Fatalf("at cap = %q, %v", out, err)
	}
	e.bin = fakeOpenCode(t, "printf 123456789\n")
	if out, err := e.Export(context.Background(), "ses_abc"); err == nil || out != nil {
		t.Fatalf("over cap = %q, %v", out, err)
	}
}

func TestOpenCodeExporterContext(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Export(ctx, "ses_abc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled err = %v", err)
	}
	e.bin = fakeOpenCode(t, "exit 0\n")
	e.run = func(*exec.Cmd) error { cancel(); return context.Canceled }
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if _, err := e.Export(ctx, "ses_abc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("during-run err = %v", err)
	}
	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	if _, err := e.Export(deadlineCtx, "ses_abc"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline err = %v", err)
	}
}

func TestOpenCodeExporterMissingBinary(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	e.bin = "opencode-definitely-missing-binary"
	if _, err := e.Export(context.Background(), "ses_abc"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenCodeExporterRejectsBadSessionID(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	e.bin = "opencode-definitely-missing-binary" // never reached
	for _, id := range []string{"", "--help", "-x"} {
		if _, err := e.Export(context.Background(), id); err == nil || !strings.Contains(err.Error(), "invalid session id") {
			t.Fatalf("Export(%q) err = %v", id, err)
		}
	}
}
