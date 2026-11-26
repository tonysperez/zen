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
