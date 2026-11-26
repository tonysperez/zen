package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalRepos = `repos:
  mono:
    full_name: chainguard-dev/mono
    base_path: /tmp/mono
`

func mustParse(t *testing.T, body string) *Config {
	t.Helper()
	cfg, err := Parse([]byte(minimalRepos + body))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	return cfg
}

func TestNotifyDesktopAndCompletionDMDefaults(t *testing.T) {
	cfg := mustParse(t, "")
	if !cfg.Notify.DesktopEnabled() {
		t.Error("notify.desktop should default to true")
	}
	if !cfg.Slack.CompletionDMEnabled() {
		t.Error("slack.completion_dm should default to true")
	}
}

func TestNotifyDesktopAndCompletionDMOff(t *testing.T) {
	cfg := mustParse(t, "notify:\n  desktop: false\n  command: tell-lead\nslack:\n  completion_dm: false\n")
	if cfg.Notify.DesktopEnabled() {
		t.Error("notify.desktop: false not honoured")
	}
	if cfg.Slack.CompletionDMEnabled() {
		t.Error("slack.completion_dm: false not honoured")
	}
	if cfg.Notify.Command != "tell-lead" {
		t.Errorf("notify.command = %q", cfg.Notify.Command)
	}
}

func TestEventsConfig(t *testing.T) {
	t.Setenv("ZEN_HOME", t.TempDir())
	if cfg := mustParse(t, ""); cfg.Events.Enabled || cfg.EventOptions().Log {
		t.Error("events.enabled should default to false")
	}
	cfg := mustParse(t, "events:\n  enabled: true\n")
	if opts := cfg.EventOptions(); !opts.Log || opts.StateDir != StateDir() {
		t.Errorf("EventOptions() = %+v", opts)
	}
	if want := filepath.Join(os.Getenv("ZEN_HOME"), "state", "events.jsonl"); EventsPath() != want {
		t.Errorf("EventsPath() = %q, want %q", EventsPath(), want)
	}
}

func TestSlackRoute(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    string
		wantErr string
	}{
		{"default", "", SlackRouteAgent, ""},
		{"agent", "slack:\n  route: agent\n", SlackRouteAgent, ""},
		{"event, desktop on by default", "slack:\n  enabled: true\n  route: event\n", SlackRouteEvent, ""},
		{"event with log", "slack:\n  enabled: true\n  route: event\nnotify:\n  desktop: false\nevents:\n  enabled: true\n", SlackRouteEvent, ""},
		{"event with command for all events", "slack:\n  enabled: true\n  route: event\nnotify:\n  desktop: false\n  command: lead\n", SlackRouteEvent, ""},
		{"event with command for slack-task", "slack:\n  enabled: true\n  route: event\nnotify:\n  desktop: false\n  command: lead\n  events: [slack-task]\n", SlackRouteEvent, ""},
		{"event, watcher off", "slack:\n  route: event\nnotify:\n  desktop: false\n", SlackRouteEvent, ""},
		{"event with nowhere to go", "slack:\n  enabled: true\n  route: event\nnotify:\n  desktop: false\n", "", "nowhere to send"},
		{"event, command skips slack-task", "slack:\n  enabled: true\n  route: event\nnotify:\n  desktop: false\n  command: lead\n  events: [session-waiting]\n", "", "nowhere to send"},
		{"unknown", "slack:\n  route: lead\n", "", `invalid slack.route "lead"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(minimalRepos + tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse() error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Slack.GetRoute(); got != tt.want {
				t.Errorf("GetRoute() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNotifyEventsAcceptsSlackTask(t *testing.T) {
	cfg := mustParse(t, "notify:\n  events: [slack-task, session-waiting]\n")
	if len(cfg.Notify.Events) != 2 {
		t.Errorf("notify.events = %v", cfg.Notify.Events)
	}
}
