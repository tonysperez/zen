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
		"-P", "-F", "#{window_id}", "-n", "mono-fix-x", "-c", "/w/mono-fix-x",
		"--", "/bin/bash", "-l", "-c", `claude; exec "${SHELL:-/bin/sh}"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("sessionArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

func TestSessionWindowArgs(t *testing.T) {
	got := sessionWindowArgs("workers", "/w/mono-fix-x", "claude", "/bin/bash")
	want := []string{
		"new-window", "-d", "-t", "=workers:",
		"-P", "-F", "#{window_id}", "-n", "mono-fix-x", "-c", "/w/mono-fix-x",
		"--", "/bin/bash", "-l", "-c", `claude; exec "${SHELL:-/bin/sh}"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("sessionWindowArgs() =\n  %q\nwant\n  %q", got, want)
	}
}

// TestOpenWindowInNamedSession drives a real tmux server from outside tmux,
// through a private socket directory: the named session is created with the
// first worktree's window, the second worktree gets a window in it, and
// opening the first again reuses its window.
func TestOpenWindowInNamedSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("", "zt") // socket paths must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("SHELL", "/bin/sh")
	// Start the server with no config, so the user's tmux.conf (and any zen
	// hooks in it) stays out of the test.
	if out, err := exec.Command("tmux", "-f", "/dev/null", "new-session", "-d", "-s", "placeholder").CombinedOutput(); err != nil {
		t.Fatalf("starting tmux server: %v: %s", err, out)
	}
	t.Cleanup(func() { exec.Command("tmux", "kill-server").Run() })

	first, second := t.TempDir(), t.TempDir()
	if got, err := OpenWindowIn("zen-workers", first, "true"); err != nil || got != NewWindow {
		t.Fatalf("OpenWindowIn() creating the session = %d, %v", got, err)
	}
	if got, err := OpenWindowIn("zen-workers", second, "sleep 30"); err != nil || got != NewWindow {
		t.Fatalf("OpenWindowIn() into the existing session = %d, %v", got, err)
	}

	out, err := exec.Command("tmux", "list-windows", "-t", "=zen-workers", "-F", "#{window_name}").Output()
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	if names, want := strings.Fields(string(out)), []string{filepath.Base(first), filepath.Base(second)}; !slices.Equal(names, want) {
		t.Errorf("windows = %v, want %v", names, want)
	}
	// Both windows are tagged, so they are found again.
	for _, wd := range []string{first, second} {
		if _, ok := findWindow(tmuxWindows(t), wd); !ok {
			t.Errorf("no tagged window for %s", wd)
		}
	}
	// The second still runs its command, so it is not started again.
	if got, err := OpenWindowIn("zen-workers", second, "touch ran"); err != nil || got != AlreadyRunning {
		t.Errorf("OpenWindowIn() on a busy window = %d, %v, want AlreadyRunning", got, err)
	}
}

func TestOpenWindowDetachedStillNeedsTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	if _, err := OpenWindowIn("", t.TempDir(), "true"); err != ErrNotInTmux {
		t.Errorf("OpenWindowIn(\"\") outside tmux = %v, want ErrNotInTmux", err)
	}
}
