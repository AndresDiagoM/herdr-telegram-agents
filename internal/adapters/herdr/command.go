package herdr

import (
	"context"
	"os/exec"
)

// command is exec.CommandContext for every child this package starts (the
// herdr CLI). On Windows the child gets no console window: the daemon runs
// detached without a console, so a console program it starts would
// otherwise open a new visible terminal window. scripts/check-imports.sh
// keeps other exec.Command calls out of this package.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hideConsole(cmd)
	return cmd
}
