//go:build windows

package system

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW from the Win32 API: a console child
// runs without a console window.
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
