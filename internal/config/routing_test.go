package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const minimalRepos = `repos:
  mono:
    full_name: chainguard-dev/mono
    base_path: /tmp/mono
`

// loadYAML writes body as $ZEN_HOME/config.yaml and loads it.
func loadYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ZEN_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load()
}

func TestEventsDefaults(t *testing.T) {
	cfg, err := loadYAML(t, minimalRepos)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Events.Enabled {
		t.Error("events.enabled should default to false")
	}
	if opts := cfg.EventOptions(); opts.Log {
		t.Errorf("EventOptions() = %+v, want no log by default", opts)
	}
}

func TestNotifyDefaults(t *testing.T) {
	cfg, err := loadYAML(t, minimalRepos)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Notify.DesktopEnabled() {
		t.Error("notify.desktop should default to true")
	}
	if cfg.Notify.Command != "" {
		t.Errorf("notify.command default = %q, want empty", cfg.Notify.Command)
	}
	if got := cfg.Notify.GetEvents(); !slices.Equal(got, []string{"review_requested", "session_waiting", "slack_task"}) {
		t.Errorf("notify.events default = %v", got)
	}
	if !cfg.Slack.CompletionDMEnabled() {
		t.Error("slack.completion_dm should default to true")
	}
	if opts := cfg.EventOptions(); opts.Command != "" {
		t.Errorf("EventOptions() = %+v, want no command by default", opts)
	}
}

func TestNotifyCustom(t *testing.T) {
	cfg, err := loadYAML(t, minimalRepos+`notify:
  desktop: false
  command: "lead-notify --quiet"
  events: [pr_updated, review_cleaned]
slack:
  completion_dm: false
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notify.DesktopEnabled() {
		t.Error("notify.desktop: false not honoured")
	}
	if cfg.Slack.CompletionDMEnabled() {
		t.Error("slack.completion_dm: false not honoured")
	}
	opts := cfg.EventOptions()
	if opts.Log {
		t.Error("notify.command must not turn on the event log")
	}
	if opts.Command != "lead-notify --quiet" || !slices.Equal(opts.CommandTypes, []string{"pr_updated", "review_cleaned"}) {
		t.Errorf("EventOptions() = %+v", opts)
	}
}

func TestNotifyEventsExplicitEmpty(t *testing.T) {
	cfg, err := loadYAML(t, minimalRepos+"notify:\n  command: x\n  events: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Notify.GetEvents(); len(got) != 0 {
		t.Errorf("notify.events: [] = %v, want none", got)
	}
}

func TestNotifyEventsRejectsUnknown(t *testing.T) {
	_, err := loadYAML(t, minimalRepos+"notify:\n  events: [session_waitng]\n")
	if err == nil || !strings.Contains(err.Error(), "session_waitng") {
		t.Errorf("Load() error = %v, want one naming the bad event type", err)
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
		{"event with log", "slack:\n  enabled: true\n  route: event\nevents:\n  enabled: true\n", SlackRouteEvent, ""},
		{"event with command", "slack:\n  enabled: true\n  route: event\nnotify:\n  command: lead\n", SlackRouteEvent, ""},
		{"event, watcher off", "slack:\n  route: event\n", SlackRouteEvent, ""},
		{"event with nowhere to go", "slack:\n  enabled: true\n  route: event\n", "", "needs events.enabled"},
		{"event, command skips slack_task", "slack:\n  enabled: true\n  route: event\nnotify:\n  command: lead\n  events: [session_waiting]\n", "", "needs events.enabled"},
		{"unknown", "slack:\n  route: lead\n", "", `invalid slack.route "lead"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadYAML(t, minimalRepos+tt.yaml)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want one containing %q", err, tt.wantErr)
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

func TestTmuxSession(t *testing.T) {
	t.Run("default terminal outside tmux becomes tmux", func(t *testing.T) {
		t.Setenv("TMUX", "")
		cfg, err := loadYAML(t, minimalRepos+"tmux:\n  session: workers\n")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tmux.Session != "workers" || cfg.GetTerminal() != "tmux" {
			t.Errorf("tmux.session = %q, terminal = %q", cfg.Tmux.Session, cfg.GetTerminal())
		}
	})
	t.Run("explicit terminal wins", func(t *testing.T) {
		t.Setenv("TMUX", "")
		cfg, err := loadYAML(t, minimalRepos+"terminal: kitty\ntmux:\n  session: workers\n")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.GetTerminal() != "kitty" {
			t.Errorf("terminal = %q, want kitty", cfg.GetTerminal())
		}
	})
	t.Run("unset keeps iterm outside tmux", func(t *testing.T) {
		t.Setenv("TMUX", "")
		cfg, err := loadYAML(t, minimalRepos)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tmux.Session != "" || cfg.GetTerminal() != "iterm" {
			t.Errorf("tmux.session = %q, terminal = %q", cfg.Tmux.Session, cfg.GetTerminal())
		}
	})
	for _, bad := range []string{"work:ers", "work.ers", `" "`} {
		t.Run("rejects "+bad, func(t *testing.T) {
			if _, err := loadYAML(t, minimalRepos+"tmux:\n  session: "+bad+"\n"); err == nil || !strings.Contains(err.Error(), "tmux.session") {
				t.Errorf("Load() error = %v, want a tmux.session error", err)
			}
		})
	}
}

func TestEventsEnabled(t *testing.T) {
	cfg, err := loadYAML(t, minimalRepos+"events:\n  enabled: true\n")
	if err != nil {
		t.Fatal(err)
	}
	opts := cfg.EventOptions()
	if !opts.Log || opts.StateDir != StateDir() {
		t.Errorf("EventOptions() = %+v", opts)
	}
	if want := filepath.Join(os.Getenv("ZEN_HOME"), "state", "events.jsonl"); EventsPath() != want {
		t.Errorf("EventsPath() = %q, want %q", EventsPath(), want)
	}
}
