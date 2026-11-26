package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSessionArgs(t *testing.T) {
	got := sessionArgs("workers", "/w/mono-fix-x", "claude", "/bin/bash")
	want := []string{
		"new-session", "-d", "-s", "workers",
		"-n", "mono-fix-x", "-c", "/w/mono-fix-x",
		"--", "/bin/bash", "-l", "-c", `claude; exec "${SHELL:-/bin/sh}"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("sessionArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

func TestSessionWindowArgs(t *testing.T) {
	got := sessionWindowArgs("workers", "/w/mono-fix-x", "claude", "/bin/bash")
	want := []string{
		"new-window", "-t", "=workers:",
		"-n", "mono-fix-x", "-c", "/w/mono-fix-x",
		"--", "/bin/bash", "-l", "-c", `claude; exec "${SHELL:-/bin/sh}"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("sessionWindowArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

func TestOpenWindowInEmptySessionIsOpenWindow(t *testing.T) {
	t.Setenv("TMUX", "")
	if err := OpenWindowIn("", t.TempDir(), "true"); err != ErrNotInTmux {
		t.Errorf("OpenWindowIn(\"\") outside tmux = %v, want ErrNotInTmux", err)
	}
}

// TestOpenWindowInNamedSession drives a real tmux server on a private socket
// directory: outside tmux, the named session is created, then reused.
func TestOpenWindowInNamedSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock, err := os.MkdirTemp("/tmp", "zen-tmux-") // socket paths must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sock) })
	t.Setenv("TMUX_TMPDIR", sock)
	t.Setenv("TMUX", "")
	t.Setenv("SHELL", "/bin/sh")
	t.Cleanup(func() { exec.Command("tmux", "kill-server").Run() })

	first, second := t.TempDir(), t.TempDir()
	if err := OpenWindowIn("zen-test-workers", first, "sleep 30"); err != nil {
		t.Fatalf("OpenWindowIn() creating the session: %v", err)
	}
	if err := OpenWindowIn("zen-test-workers", second, "sleep 30"); err != nil {
		t.Fatalf("OpenWindowIn() into the existing session: %v", err)
	}

	out, err := exec.Command("tmux", "list-windows", "-t", "=zen-test-workers", "-F", "#{window_name}").Output()
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	names := strings.Fields(string(out))
	want := []string{filepath.Base(first), filepath.Base(second)}
	if !slices.Equal(names, want) {
		t.Errorf("windows = %v, want %v", names, want)
	}
}
