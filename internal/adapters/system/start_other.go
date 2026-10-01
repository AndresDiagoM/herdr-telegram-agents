//go:build !darwin && !linux && !windows

package system

import "time"

// StartTime is unknown on this platform; the pid file stays unverified.
func (p *Process) StartTime(int) (time.Time, bool) { return time.Time{}, false }
