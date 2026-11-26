package reconciler

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mgreau/zen/internal/config"
)

func TestNotifyReady_CompletionDMOff(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	worktreePath := filepath.Join(t.TempDir(), "mono-slack-1700000000")
	if err := RecordSlackOrigin(SlackOrigin{ChannelID: "C1", MessageTS: "1700000000.1", WorktreePath: worktreePath, WorktreeName: "mono-slack-1700000000"}); err != nil {
		t.Fatal(err)
	}

	posted := false
	fake := &fakeSlackAPI{postMessageFn: func(context.Context, string, string) error {
		posted = true
		return nil
	}}
	off := false
	rec := NewSlackReconciler(&config.Config{Slack: config.SlackConfig{CompletionDM: &off}}, fake, "U1")
	rec.NotifyReady(worktreePath, "mono-slack-1700000000", "zen work resume mono-slack-1700000000")
	if posted {
		t.Error("slack.completion_dm: false still sent the DM")
	}
}
