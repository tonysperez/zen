package reconciler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
	"github.com/mgreau/zen/internal/notify"
	slackpkg "github.com/mgreau/zen/internal/slack"
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

func TestSlackReconcile_RouteEvent(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var commandStdin [][]byte
	restore := events.SetCommandRunner(func(_ context.Context, _ string, stdin []byte, _ []string) error {
		mu.Lock()
		defer mu.Unlock()
		commandStdin = append(commandStdin, stdin)
		return nil
	})
	defer restore()

	base := t.TempDir()
	cfg := &config.Config{
		Repos:  map[string]config.RepoConfig{"mono": {FullName: "chainguard-dev/mono", BasePath: base}},
		Slack:  config.SlackConfig{Enabled: true, Route: config.SlackRouteEvent}, // no default_repo: not needed
		Events: config.EventsConfig{Enabled: true},
		Notify: config.NotifyConfig{Command: "lead"},
	}
	var fetched []string
	fake := &fakeSlackAPI{
		repliesFn: func(_ context.Context, channel, ts string) ([]slackpkg.Message, error) {
			fetched = append(fetched, channel+":"+ts)
			return []slackpkg.Message{
				{User: "U2", Text: "the nightly build is red again", Ts: ts},
				{User: "U3", Text: "since the go bump", Ts: "1700000001.2"},
			}, nil
		},
		permalinkFn: func(context.Context, string, string) (string, error) {
			return "https://example.slack.com/archives/C1/p1700000000", nil
		},
		postMessageFn: func(context.Context, string, string) error {
			t.Error("route event must not post to Slack")
			return nil
		},
	}
	rec := NewSlackReconciler(cfg, fake, "U1")

	if err := rec.Reconcile(context.Background(), "C1:1700000000.1", workqueue.Options{}); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}
	events.Wait()

	if len(fetched) != 1 || fetched[0] != "C1:1700000000.1" {
		t.Errorf("thread fetches = %v, want one for C1:1700000000.1", fetched)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("route event created %v in the repo base path", entries)
	}
	if len(LoadSlackOrigins()) != 0 {
		t.Error("route event recorded a Slack origin; no completion DM should follow")
	}

	got := readEventLog(t)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	e := got[0]
	if e.Type != events.TypeSlackTask || e.URL != "https://example.slack.com/archives/C1/p1700000000" || e.Worktree != "" {
		t.Errorf("event = %+v", e)
	}
	for k, want := range map[string]string{
		"channel":    "C1",
		"ts":         "1700000000.1",
		"permalink":  "https://example.slack.com/archives/C1/p1700000000",
		"user":       "U2",
		"flagged_by": "U1",
		"route":      "event",
	} {
		if e.Data[k] != want {
			t.Errorf("data[%q] = %v, want %q", k, e.Data[k], want)
		}
	}
	if text, _ := e.Data["text"].(string); !contains(text, "U2: the nightly build is red again") || !contains(text, "U3: since the go bump") {
		t.Errorf("data.text = %q", text)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(commandStdin) != 1 || !contains(string(commandStdin[0]), `"type":"slack_task"`) {
		t.Errorf("notify.command stdin = %q, want one slack_task event", commandStdin)
	}
}

func TestSlackReconcile_RouteEventFetchErrorRetries(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	cfg := &config.Config{
		Slack:  config.SlackConfig{Route: config.SlackRouteEvent},
		Events: config.EventsConfig{Enabled: true},
	}
	fake := &fakeSlackAPI{repliesFn: func(context.Context, string, string) ([]slackpkg.Message, error) {
		return nil, errors.New("ratelimited")
	}}
	err := NewSlackReconciler(cfg, fake, "U1").Reconcile(context.Background(), "C1:1700000000.1", workqueue.Options{})
	if err == nil {
		t.Fatal("expected an error when the thread cannot be fetched")
	}
	if workqueue.GetNonRetriableDetails(err) != nil {
		t.Error("a failed thread fetch should be retried")
	}
	if _, err := os.Stat(config.EventsPath()); !os.IsNotExist(err) {
		t.Error("an event was emitted for a thread that was never fetched")
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
