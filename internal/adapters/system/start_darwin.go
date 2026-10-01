//go:build darwin

package system

import (
	"time"

	"golang.org/x/sys/unix"
)

// StartTime reports when pid started, from the kernel's process table.
func (p *Process) StartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid != int32(pid) {
		return time.Time{}, false
	}
	tv := kp.Proc.P_starttime
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), true
}
