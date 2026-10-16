package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/permgps/herdr-telegram-agents/internal/domain"
)

type installReader struct{ value domain.PluginInstallation }

func (r installReader) ReadInstallation(context.Context) (domain.PluginInstallation, error) {
	return r.value, nil
}

func TestUpdatePreflightOwnCommitPin(t *testing.T) {
	version, _ := domain.ParseVersion("1.2.0")
	release := domain.Release{Tag: "v1.2.0", Version: version}
	root := t.TempDir()
	commit := strings.Repeat("a", 40)
	base := domain.PluginInstallation{Root: root, ManagedPath: root, ManifestVersion: "1.0.0", BinaryVersion: "1.0.0", SourceKind: "github", Owner: "permgps", Repo: "herdr-telegram-agents", RequestedRef: commit, ResolvedCommit: commit}
	history := domain.UpdateJob{ID: "previous", Phase: "succeeded", TargetTag: "v1.0.0", TargetCommit: commit, NewCommit: commit}
	for _, tc := range []struct {
		name   string
		change func(*domain.PluginInstallation, *domain.UpdateJob)
		want   string
	}{
		{"own commit", func(*domain.PluginInstallation, *domain.UpdateJob) {}, ""},
		{"legacy own tag", func(i *domain.PluginInstallation, _ *domain.UpdateJob) { i.RequestedRef = "v1.0.0" }, ""},
		{"manual pin", func(i *domain.PluginInstallation, _ *domain.UpdateJob) { i.RequestedRef = strings.Repeat("b", 40) }, "pinned"},
		{"changed commit", func(i *domain.PluginInstallation, _ *domain.UpdateJob) { i.ResolvedCommit = strings.Repeat("b", 40) }, "pinned"},
		{"different target", func(_ *domain.PluginInstallation, j *domain.UpdateJob) { j.TargetCommit = strings.Repeat("b", 40) }, "pinned"},
		{"failed update", func(_ *domain.PluginInstallation, j *domain.UpdateJob) { j.Phase = "failed" }, "pinned"},
		{"no history", func(_ *domain.PluginInstallation, j *domain.UpdateJob) { j.ID = "" }, "pinned"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			install, job := base, history
			tc.change(&install, &job)
			p := UpdatePreflight{Installation: installReader{install}, History: &memoryUpdateStore{job: job}}
			got, err := p.Check(context.Background(), release)
			if err != nil || got.Code != tc.want {
				t.Fatalf("code=%q want=%q err=%v", got.Code, tc.want, err)
			}
		})
	}
}

type checkoutReader struct{ value domain.CheckoutState }

func (r checkoutReader) InspectCheckout(context.Context, string, string) (domain.CheckoutState, error) {
	return r.value, nil
}

func TestUpdatePreflightBlockers(t *testing.T) {
	version, _ := domain.ParseVersion("1.2.0")
	release := domain.Release{Tag: "v1.2.0", Version: version}
	managed := t.TempDir()
	root := filepath.Join(managed, "plugin")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	base := domain.PluginInstallation{Root: root, ManifestVersion: "1.0.0", BinaryVersion: "1.0.0", SourceKind: "github", Owner: "permgps", Repo: "herdr-telegram-agents", ManagedPath: managed, ResolvedCommit: "old-commit"}
	local := domain.CheckoutState{Branch: "main", Origin: "https://github.com/permgps/herdr-telegram-agents.git", Commit: "old", TargetCommit: "new", FastForward: true}
	for _, tc := range []struct {
		name   string
		change func(*domain.PluginInstallation, *domain.CheckoutState)
		want   string
	}{
		{"managed", func(*domain.PluginInstallation, *domain.CheckoutState) {}, ""},
		{"pin", func(i *domain.PluginInstallation, _ *domain.CheckoutState) { i.RequestedRef = "v1.0.0" }, "pinned"},
		{"wrong repo", func(i *domain.PluginInstallation, _ *domain.CheckoutState) { i.Repo = "another" }, "wrong_repository"},
		{"local", func(i *domain.PluginInstallation, _ *domain.CheckoutState) { i.SourceKind = "local" }, ""},
		{"dirty", func(i *domain.PluginInstallation, c *domain.CheckoutState) { i.SourceKind = "local"; c.Dirty = true }, "dirty"},
		{"detached", func(i *domain.PluginInstallation, c *domain.CheckoutState) { i.SourceKind = "local"; c.Branch = "" }, "branch"},
		{"non fast forward", func(i *domain.PluginInstallation, c *domain.CheckoutState) {
			i.SourceKind = "local"
			c.FastForward = false
		}, "non_fast_forward"},
		{"disk newer than daemon", func(i *domain.PluginInstallation, _ *domain.CheckoutState) {
			i.BinaryVersion = "1.2.0"
			i.RunningVersion = "1.0.0"
			i.Running = true
		}, "already_installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			install, checkout := base, local
			tc.change(&install, &checkout)
			preflight := UpdatePreflight{Installation: installReader{install}, Checkout: checkoutReader{checkout}}
			result, err := preflight.Check(context.Background(), release)
			if err != nil || result.Code != tc.want {
				t.Fatalf("code=%s want=%s err=%v", result.Code, tc.want, err)
			}
		})
	}
}
