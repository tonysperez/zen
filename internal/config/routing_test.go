package config

import (
	"os"
	"path/filepath"
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

func TestNotifyEventsAcceptsSlackTask(t *testing.T) {
	cfg := mustParse(t, "notify:\n  events: [slack-task, session-waiting]\n")
	if len(cfg.Notify.Events) != 2 {
		t.Errorf("notify.events = %v", cfg.Notify.Events)
	}
}
