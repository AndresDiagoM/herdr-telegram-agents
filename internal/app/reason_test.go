package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

// TestFailureReasonHidesLocalPaths: replies to the group never carry the
// machine's absolute paths (user name, project layout).
func TestFailureReasonHidesLocalPaths(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: /Users/alex/Projects/secret", domain.ErrNotRepository), "not a git repository"},
		{fmt.Errorf("open /Users/alex/Library/state/inbox/a.jpg: permission denied"), "open …/a.jpg: permission denied"},
		{fmt.Errorf(`save C:\Users\alex\AppData\x.json: disk full`), "save …/x.json: disk full"},
		{fmt.Errorf("git diff: %w", context.DeadlineExceeded), "timed out"},
		{fmt.Errorf("prompt: %w", domain.ErrAgentGone), "agent is gone"},
		{fmt.Errorf("plain failure\nsecond line"), "plain failure"},
		{fmt.Errorf("Get https://api.github.com/repos/x: EOF"), "Get https://api.github.com/repos/x: EOF"},
		{fmt.Errorf("build internal/app/x.go failed"), "build internal/app/x.go failed"},
	}
	for _, tc := range cases {
		if got := failureReason(tc.err); got != tc.want {
			t.Errorf("failureReason(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if got := gitFailure("status", fmt.Errorf("%w: /Users/alex/x", domain.ErrNotRepository)); strings.Contains(got, "/Users") {
		t.Errorf("gitFailure = %q", got)
	}
}
