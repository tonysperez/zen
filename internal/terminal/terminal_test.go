package terminal

import (
	"testing"

	"github.com/mgreau/zen/internal/config"
)

func TestNewTerminal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantErr  bool
	}{
		{"iterm explicit", "iterm", "iTerm2", false},
		{"ghostty", "ghostty", "Ghostty", false},
		{"kitty", "kitty", "kitty", false},
		{"macos", "macos", "Terminal", false},
		{"tmux", "tmux", "tmux", false},
		{"empty is invalid", "", "", true},
		{"invalid terminal", "invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			term, err := NewTerminal(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTerminal(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if got := term.Name(); got != tt.wantName {
				t.Errorf("NewTerminal(%q).Name() = %q, want %q", tt.input, got, tt.wantName)
			}
		})
	}
}

func TestITermTerminalName(t *testing.T) {
	term := &ITermTerminal{}
	if got := term.Name(); got != "iTerm2" {
		t.Errorf("ITermTerminal.Name() = %q, want %q", got, "iTerm2")
	}
}

func TestGhosttyTerminalName(t *testing.T) {
	term := &GhosttyTerminal{}
	if got := term.Name(); got != "Ghostty" {
		t.Errorf("GhosttyTerminal.Name() = %q, want %q", got, "Ghostty")
	}
}

func TestKittyTerminalName(t *testing.T) {
	term := &KittyTerminal{}
	if got := term.Name(); got != "kitty" {
		t.Errorf("KittyTerminal.Name() = %q, want %q", got, "kitty")
	}
}

func TestMacOSTerminalName(t *testing.T) {
	term := &MacOSTerminal{}
	if got := term.Name(); got != "Terminal" {
		t.Errorf("MacOSTerminal.Name() = %q, want %q", got, "Terminal")
	}
}

func TestFromConfig(t *testing.T) {
	term, err := FromConfig(&config.Config{Terminal: "tmux", Tmux: config.TmuxConfig{Session: "workers"}})
	if err != nil {
		t.Fatal(err)
	}
	if tm, ok := term.(*TmuxTerminal); !ok || tm.Session != "workers" {
		t.Errorf("FromConfig() = %#v, want a tmux terminal for session workers", term)
	}

	term, err = FromConfig(&config.Config{Terminal: "kitty", Tmux: config.TmuxConfig{Session: "workers"}})
	if err != nil {
		t.Fatal(err)
	}
	if term.Name() != "kitty" {
		t.Errorf("FromConfig() = %q, want kitty", term.Name())
	}

	if _, err := FromConfig(&config.Config{Terminal: "bogus"}); err == nil {
		t.Error("FromConfig() should reject an unknown terminal")
	}
}
