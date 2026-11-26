package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CommandTimeout bounds one run of notify.command.
const CommandTimeout = 10 * time.Second

const (
	// maxConcurrent is how many notify.command processes run at once.
	maxConcurrent = 4
	// maxPending caps runs that are queued or running. Past it, an event's
	// command run is dropped and logged instead of piling up goroutines
	// behind a command that keeps hanging.
	maxPending = 64
)

// Options says where Emit sends an event.
type Options struct {
	// Log appends the event to the log in StateDir (events.enabled).
	Log      bool
	StateDir string
	// Command is run through `sh -c` for events whose type is in
	// CommandTypes (notify.command and notify.events). Empty: no command.
	Command      string
	CommandTypes []string
}

// CommandRunner runs command with stdin and extra environment, honouring
// ctx's deadline.
type CommandRunner func(ctx context.Context, command string, stdin []byte, env []string) error

var (
	runnerMu sync.RWMutex
	runner   CommandRunner = runShell

	sem     = make(chan struct{}, maxConcurrent)
	pending atomic.Int32
	running sync.WaitGroup
)

// SetCommandRunner replaces how notify.command is run, for tests. It returns
// a function that restores the previous runner.
func SetCommandRunner(r CommandRunner) (restore func()) {
	runnerMu.Lock()
	prev := runner
	runner = r
	runnerMu.Unlock()
	return func() {
		runnerMu.Lock()
		runner = prev
		runnerMu.Unlock()
	}
}

func currentRunner() CommandRunner {
	runnerMu.RLock()
	defer runnerMu.RUnlock()
	return runner
}

// Emit records e as opts says: appended to the log, and handed to the notify
// command in the background. Neither can fail the caller; errors are logged.
// Emit does not wait for the command; Wait does.
func Emit(opts Options, e Event) {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if opts.Log {
		if err := Append(Path(opts.StateDir), e); err != nil {
			logf("Event log: failed to append %s: %v", e.Type, err)
		}
	}
	if opts.Command != "" && wants(opts.CommandTypes, e.Type) {
		runCommand(opts.Command, e)
	}
}

// Wait blocks until every notify command started by Emit has finished.
func Wait() {
	running.Wait()
}

func wants(types []string, t string) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

// runCommand starts command for e on a goroutine, at most maxConcurrent at a
// time, so a slow command never holds up the daemon loop that emitted e.
func runCommand(command string, e Event) {
	payload, err := json.Marshal(e)
	if err != nil {
		logf("Notify command: encoding %s: %v", e.Type, err)
		return
	}
	if pending.Add(1) > maxPending {
		pending.Add(-1)
		logf("Notify command: %d runs already pending, dropping %s", maxPending, e.Type)
		return
	}
	run := currentRunner()
	running.Add(1)
	go func() {
		defer running.Done()
		defer pending.Add(-1)
		sem <- struct{}{}
		defer func() { <-sem }()

		ctx, cancel := context.WithTimeout(context.Background(), CommandTimeout)
		defer cancel()
		if err := run(ctx, command, payload, []string{"ZEN_EVENT_TYPE=" + e.Type}); err != nil {
			logf("Notify command failed for %s: %v", e.Type, err)
		}
	}()
}

// runShell runs command through `sh -c` with the event JSON on stdin.
func runShell(ctx context.Context, command string, stdin []byte, env []string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
	// A background child that inherits the output pipe must not keep the
	// run alive past the timeout.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, truncate(msg, 500))
		}
		return err
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func logf(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
