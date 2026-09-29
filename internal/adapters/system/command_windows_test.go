//go:build windows

package system

import (
	"context"
	"strings"
	"syscall"
	"testing"
)

func TestCommandHidesConsoleWindow(t *testing.T) {
	cmd := command(context.Background(), "cmd.exe", "/d", "/c", "echo hidden-ok")
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("SysProcAttr = %+v, want CREATE_NO_WINDOW", cmd.SysProcAttr)
	}
	// A hidden console child still writes to the pipes the caller set up.
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "hidden-ok" {
		t.Fatalf("Output = %q, %v", out, err)
	}
}

func TestHideConsoleKeepsExistingFlags(t *testing.T) {
	cmd := command(context.Background(), "cmd.exe")
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	hideConsole(cmd)
	if want := uint32(createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP); cmd.SysProcAttr.CreationFlags != want {
		t.Fatalf("CreationFlags = %#x, want %#x", cmd.SysProcAttr.CreationFlags, want)
	}
}
