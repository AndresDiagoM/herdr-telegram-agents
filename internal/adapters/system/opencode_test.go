package system

import (
	"context"
	"errors"
	"os"
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

func TestOpenCodeExporterFailureCarriesStderr(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	e.bin = fakeOpenCode(t, "echo 'Session not found' >&2\nexit 1\n")
	if _, err := e.Export(context.Background(), "ses_abc"); err == nil || !strings.Contains(err.Error(), "Session not found") {
		t.Fatalf("err = %v", err)
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

func TestOpenCodeExporterTimeout(t *testing.T) {
	e := NewOpenCodeExporter(nil)
	e.bin = fakeOpenCode(t, "sleep 5\necho done\n")
	e.timeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := e.Export(context.Background(), "ses_abc"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout did not stop the process: %v", time.Since(start))
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
