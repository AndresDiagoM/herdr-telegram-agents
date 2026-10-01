package system

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestProcessStartTimeOfSelf(t *testing.T) {
	p := NewProcess(t.TempDir(), nil)
	start, ok := p.StartTime(os.Getpid())
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		if ok {
			t.Fatal("start time on an unsupported platform")
		}
		return
	}
	if !ok || start.After(time.Now()) || time.Since(start) > time.Hour {
		t.Fatalf("StartTime(self) = %v, %v", start, ok)
	}
	if _, ok := p.StartTime(0); ok {
		t.Fatal("StartTime(0) known")
	}
}
