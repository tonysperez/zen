package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
	"github.com/mgreau/zen/internal/notify"
	slackpkg "github.com/mgreau/zen/internal/slack"
)

func TestSlackReconcile_RouteEvent(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	notify.Configure(`printf '%s' "$ZEN_EVENT" > `+filepath.Join(out, "event")+
		`; printf '%s' "$ZEN_URL" > `+filepath.Join(out, "url")+
		`; printf '%s' "$ZEN_EVENT_JSON" > `+filepath.Join(out, "json"), nil)
	notify.ConfigureDesktop(false)
	t.Cleanup(func() { notify.Configure("", nil); notify.ConfigureDesktop(true) })

	base := t.TempDir()
	cfg := &config.Config{
		Repos:  map[string]config.RepoConfig{"mono": {FullName: "chainguard-dev/mono", BasePath: base}},
		Slack:  config.SlackConfig{Enabled: true, Route: config.SlackRouteEvent}, // no default_repo: not needed
		Events: config.EventsConfig{Enabled: true},
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

	if len(fetched) != 1 || fetched[0] != "C1:1700000000.1" {
		t.Errorf("thread fetches = %v, want one", fetched)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
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
	for k, want := range map[string]string{"channel": "C1", "ts": "1700000000.1", "user": "U2", "flagged_by": "U1", "route": "event"} {
		if e.Data[k] != want {
			t.Errorf("data[%q] = %v, want %q", k, e.Data[k], want)
		}
	}
	if text, _ := e.Data["text"].(string); !contains(text, "U2: the nightly build is red again") || !contains(text, "U3: since the go bump") {
		t.Errorf("data.text = %q", text)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("push command did not run: %v", err)
		}
		return string(b)
	}
	if read("event") != "slack-task" || read("url") != "https://example.slack.com/archives/C1/p1700000000" {
		t.Errorf("push saw ZEN_EVENT=%q ZEN_URL=%q", read("event"), read("url"))
	}
	var pushed events.Event
	if err := json.Unmarshal([]byte(read("json")), &pushed); err != nil || pushed.Type != "slack-task" || pushed.Time != e.Time {
		t.Errorf("ZEN_EVENT_JSON = %q (%v), want the logged event", read("json"), err)
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
	if err == nil || workqueue.GetNonRetriableDetails(err) != nil {
		t.Errorf("Reconcile() = %v, want a retriable error", err)
	}
	if _, err := os.Stat(config.EventsPath()); !os.IsNotExist(err) {
		t.Error("an event was logged for a thread that was never fetched")
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
