package system

import (
	"os"
	"strings"
)

// InGraphicalSession reports whether the daemon runs inside a local desktop
// session; see GraphicalSession.
func InGraphicalSession() bool {
	return GraphicalSession(os.LookupEnv)
}

// GraphicalSession reports whether the environment belongs to a local
// desktop session: WAYLAND_DISPLAY set, a local DISPLAY (":0", not the
// "localhost:10.0" of X forwarding over SSH), or XDG_SESSION_TYPE wayland or
// x11 ("tty" and "unspecified" are what SSH and console logins get). Presence
// assumes the operator sits at the desk only here when no idle source
// answers; a headless server keeps quiet off.
func GraphicalSession(lookup Lookup) bool {
	if v, ok := lookup("WAYLAND_DISPLAY"); ok && v != "" {
		return true
	}
	if v, ok := lookup("DISPLAY"); ok && strings.HasPrefix(v, ":") {
		return true
	}
	v, _ := lookup("XDG_SESSION_TYPE")
	switch strings.ToLower(v) {
	case "wayland", "x11":
		return true
	}
	return false
}
