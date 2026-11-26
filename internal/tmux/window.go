// Package tmux opens windows in the tmux session zen runs in, or in a named
// session (tmux.session in config). It suits hosts reached over ssh, such as
// a cloud workstation, where there is no terminal application to drive.
package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ErrNotInTmux is returned when zen is not running inside a tmux session.
var ErrNotInTmux = errors.New("not running inside tmux: start zen from a tmux session, or pass --no-terminal")

// OpenWindow opens a new window in the current tmux session, named after the
// base name of workDir, and runs command there through the user's login shell.
// When command exits, the window keeps an interactive shell in workDir, so a
// finished or failed agent leaves a prompt instead of closing the window.
func OpenWindow(workDir, command string) error {
	if os.Getenv("TMUX") == "" {
		return ErrNotInTmux
	}
	out, err := exec.Command("tmux", windowArgs(workDir, command, userShell())...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-window: %w: %s", err, out)
	}
	return nil
}

// OpenWindowIn opens the window in the named tmux session instead of the
// current one, creating that session detached when it does not exist. It
// works outside tmux too, as long as a tmux binary is installed. An empty
// session behaves like OpenWindow.
func OpenWindowIn(session, workDir, command string) error {
	if session == "" {
		return OpenWindow(workDir, command)
	}
	shell := userShell()
	if exec.Command("tmux", "has-session", "-t", "="+session).Run() != nil {
		out, err := exec.Command("tmux", sessionArgs(session, workDir, command, shell)...).CombinedOutput()
		if err == nil {
			return nil
		}
		// Another zen may have created it in the meantime; fall through to
		// new-window, and report the original error if that fails too.
		if _, werr := exec.Command("tmux", sessionWindowArgs(session, workDir, command, shell)...).CombinedOutput(); werr != nil {
			return fmt.Errorf("tmux new-session -s %s: %w: %s", session, err, out)
		}
		return nil
	}
	out, err := exec.Command("tmux", sessionWindowArgs(session, workDir, command, shell)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-window -t %s: %w: %s", session, err, out)
	}
	return nil
}

// sessionArgs builds the tmux new-session arguments that create session
// detached, with the worktree window as its first window.
func sessionArgs(session, workDir, command, shell string) []string {
	return append([]string{"new-session", "-d", "-s", session}, windowSpec(workDir, command, shell)...)
}

// sessionWindowArgs builds the new-window arguments that add the worktree
// window to session. "=" makes tmux match the session name exactly rather
// than as a prefix, and the trailing ":" picks the next free window index.
func sessionWindowArgs(session, workDir, command, shell string) []string {
	return append([]string{"new-window", "-t", "=" + session + ":"}, windowSpec(workDir, command, shell)...)
}

func userShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// windowArgs builds the tmux new-window arguments. tmux runs the shell and its
// arguments directly, so command reaches the shell as one argument and tmux
// never parses it. A login shell loads the same profile as an ssh session.
func windowArgs(workDir, command, shell string) []string {
	return append([]string{"new-window"}, windowSpec(workDir, command, shell)...)
}

// windowSpec is the window name, directory and command shared by
// new-window and new-session.
func windowSpec(workDir, command, shell string) []string {
	return []string{
		"-n", filepath.Base(workDir), "-c", workDir,
		"--", shell, "-l", "-c", command + `; exec "${SHELL:-/bin/sh}"`,
	}
}
