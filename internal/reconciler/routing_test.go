package reconciler

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
	"github.com/mgreau/zen/internal/notify"
)

// captureDesktop replaces the desktop notifier and returns the titles sent.
func captureDesktop(t *testing.T) (titles func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	prev := notify.Sender
	notify.Sender = func(title, _, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, title)
		return nil
	}
	t.Cleanup(func() { notify.Sender = prev })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// writeWaitingSnapshot leaves a fresh snapshot with one waiting session, so
// SendDigest has something to report.
func writeWaitingSnapshot(t *testing.T) {
	t.Helper()
	t.Setenv("ZEN_HOME", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := WriteSessionSnapshot([]SessionState{{WorktreePath: "/w/x", Status: "waiting", UpdatedAt: time.Now().Unix()}}); err != nil {
		t.Fatal(err)
	}
}

func TestSendDigest_Desktop(t *testing.T) {
	writeWaitingSnapshot(t)
	titles := captureDesktop(t)

	SendDigest(&config.Config{})
	if got := titles(); len(got) != 1 || got[0] != "zen digest" {
		t.Errorf("desktop notifications = %v, want one digest", got)
	}
}

func TestSendDigest_DesktopOff(t *testing.T) {
	writeWaitingSnapshot(t)
	titles := captureDesktop(t)

	off := false
	SendDigest(&config.Config{Notify: config.NotifyConfig{Desktop: &off}})
	if got := titles(); len(got) != 0 {
		t.Errorf("notify.desktop: false still sent %v", got)
	}
}

func TestEmit_NotifyCommandWithoutEventLog(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	var mu sync.Mutex
	var types []string
	restore := events.SetCommandRunner(func(_ context.Context, _ string, _ []byte, env []string) error {
		mu.Lock()
		defer mu.Unlock()
		types = append(types, env...)
		return nil
	})
	defer restore()

	cfg := &config.Config{Notify: config.NotifyConfig{Command: "lead"}}
	Emit(cfg, events.Event{Type: events.TypeSessionWaiting})
	Emit(cfg, events.Event{Type: events.TypeReviewCleaned}) // not in the default notify.events
	events.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(types) != 1 || types[0] != "ZEN_EVENT_TYPE=session_waiting" {
		t.Errorf("command ran with %v, want once for session_waiting", types)
	}
	if _, err := os.Stat(config.EventsPath()); !os.IsNotExist(err) {
		t.Error("notify.command alone wrote the event log")
	}
}

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
