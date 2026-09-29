//go:build windows

package herdr

import (
	"context"
	"strings"
	"testing"
)

func TestCommandHidesConsoleWindow(t *testing.T) {
	cmd := command(context.Background(), "cmd.exe", "/d", "/c", "echo hidden-ok")
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("SysProcAttr = %+v, want CREATE_NO_WINDOW", cmd.SysProcAttr)
	}
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "hidden-ok" {
		t.Fatalf("Output = %q, %v", out, err)
	}
}
