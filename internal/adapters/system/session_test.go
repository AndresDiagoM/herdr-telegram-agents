package system_test

import (
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/adapters/system"
)

func TestGraphicalSession(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"wayland", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"local x11", map[string]string{"DISPLAY": ":0"}, true},
		{"session type wayland", map[string]string{"XDG_SESSION_TYPE": "wayland"}, true},
		{"session type x11", map[string]string{"XDG_SESSION_TYPE": "X11"}, true},
		{"empty", map[string]string{}, false},
		{"empty values", map[string]string{"WAYLAND_DISPLAY": "", "DISPLAY": "", "XDG_SESSION_TYPE": ""}, false},
		{"ssh login", map[string]string{"XDG_SESSION_TYPE": "tty"}, false},
		{"unspecified", map[string]string{"XDG_SESSION_TYPE": "unspecified"}, false},
		{"x forwarding", map[string]string{"DISPLAY": "localhost:10.0", "XDG_SESSION_TYPE": "tty"}, false},
	}
	for _, tc := range cases {
		if got := system.GraphicalSession(lookupFrom(tc.env)); got != tc.want {
			t.Errorf("%s: GraphicalSession = %v, want %v", tc.name, got, tc.want)
		}
	}
}
