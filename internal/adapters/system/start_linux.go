//go:build linux

package system

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is USER_HZ, the unit of /proc/<pid>/stat start times. It is
// 100 on every architecture Go builds for; reading it needs cgo.
const clockTicks = 100

// StartTime reports when pid started: field 22 of /proc/<pid>/stat (ticks
// since boot) plus the boot time from /proc/stat.
func (p *Process) StartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, false
	}
	// The command name may contain spaces and parentheses; fields resume
	// after the last ')'. Field 3 (state) is the first one after it.
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return time.Time{}, false
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 20 {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	boot, ok := bootTime()
	if !ok {
		return time.Time{}, false
	}
	return boot.Add(time.Duration(ticks) * time.Second / clockTicks), true
}

func bootTime() (time.Time, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(sec, 0), true
		}
	}
	return time.Time{}, false
}
