package reconciler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
	slackpkg "github.com/mgreau/zen/internal/slack"
)

// readEventLog parses the event log under the test's ZEN_HOME.
func readEventLog(t *testing.T) []events.Event {
	t.Helper()
	lines, _, err := events.Read(config.EventsPath(), events.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]events.Event, 0, len(lines))
	for _, l := range lines {
		var e events.Event
		if err := json.Unmarshal(l, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestCleanupReconcile_EmitsReviewCleaned(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	cfg, worktreePath, prHead := cleanupFixture(t)
	cfg.Events.Enabled = true

	rec := NewCleanupReconciler(cfg)
	rec.prMergeInfo = mergedInfo(prHead, 2*time.Hour)
	if err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{}); err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}

	got := readEventLog(t)
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	e := got[0]
	if e.Type != "review-cleaned" || e.Repo != "testrepo" || e.PR != 999 || e.Worktree != worktreePath || e.SHA != prHead {
		t.Errorf("event = %+v", e)
	}
	if _, ok := e.Data["merged_at"].(string); !ok {
		t.Errorf("event data missing merged_at: %+v", e.Data)
	}
}

func TestCleanupReconcile_EventLogOffByDefault(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	cfg, _, prHead := cleanupFixture(t)

	rec := NewCleanupReconciler(cfg)
	rec.prMergeInfo = mergedInfo(prHead, 2*time.Hour)
	if err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.EventsPath()); !os.IsNotExist(err) {
		t.Error("the event log was written without events.enabled")
	}
}

func TestCleanupReconcile_SkipEmitsNothing(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	cfg, worktreePath, prHead := cleanupFixture(t)
	cfg.Events.Enabled = true
	if err := os.WriteFile(filepath.Join(worktreePath, "notes.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := NewCleanupReconciler(cfg)
	rec.prMergeInfo = mergedInfo(prHead, 2*time.Hour)
	if err := rec.Reconcile(context.Background(), "testrepo:999", workqueue.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.EventsPath()); !os.IsNotExist(err) {
		t.Error("a skipped cleanup wrote an event")
	}
}

func TestSlackTaskEvent(t *testing.T) {
	messages := []slackpkg.Message{
		{User: "U2", Text: "can someone bump the base image?", Ts: "1.1"},
		{User: "U3", Text: "+1", Ts: "1.2"},
	}
	e := slackTaskEvent("C1", "1.1", "https://example.slack.com/archives/C1/p1", "U1", messages)
	if e.Type != "slack-task" || e.URL != "https://example.slack.com/archives/C1/p1" {
		t.Errorf("event = %+v", e)
	}
	if e.Data["channel"] != "C1" || e.Data["ts"] != "1.1" || e.Data["user"] != "U2" || e.Data["flagged_by"] != "U1" {
		t.Errorf("event data = %+v", e.Data)
	}
	text, _ := e.Data["text"].(string)
	if !contains(text, "U2: can someone bump the base image?") || !contains(text, "U3: +1") {
		t.Errorf("event text = %q", text)
	}
	if _, ok := e.Data["truncated"]; ok {
		t.Error("short thread marked truncated")
	}
}

func TestSlackTaskEventTruncates(t *testing.T) {
	long := strings.Repeat("é", maxSlackTaskText) // 2 bytes per rune
	e := slackTaskEvent("C1", "1.1", "", "U1", []slackpkg.Message{{User: "U2", Text: long}})
	text := e.Data["text"].(string)
	if len(text) > maxSlackTaskText || !utf8.ValidString(text) {
		t.Errorf("text is %d bytes (valid UTF-8: %v), want <= %d", len(text), utf8.ValidString(text), maxSlackTaskText)
	}
	if e.Data["truncated"] != true {
		t.Error("long thread not marked truncated")
	}
}
