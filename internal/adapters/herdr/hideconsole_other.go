//go:build !windows

package herdr

import "os/exec"

func hideConsole(*exec.Cmd) {}
