package events

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordedRun struct {
	command string
	stdin   []byte
	env     []string
}

// recordRuns swaps in a runner that records each call.
func recordRuns(t *testing.T) (get func() []recordedRun) {
	t.Helper()
	var mu sync.Mutex
	var runs []recordedRun
	restore := SetCommandRunner(func(_ context.Context, command string, stdin []byte, env []string) error {
		mu.Lock()
		defer mu.Unlock()
		runs = append(runs, recordedRun{command, stdin, env})
		return nil
	})
	t.Cleanup(restore)
	return func() []recordedRun {
		Wait()
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRun(nil), runs...)
	}
}

func TestEmitRunsCommandForListedTypes(t *testing.T) {
	runs := recordRuns(t)
	dir := t.TempDir()
	opts := Options{
		StateDir:     dir,
		Command:      "notify-lead",
		CommandTypes: []string{TypeSessionWaiting, TypeSlackTask},
	}

	Emit(opts, Event{Type: TypeSessionWaiting, Worktree: "/w/mono-x", Session: "s1"})
	Emit(opts, Event{Type: TypePRUpdated, PR: 3}) // not listed

	got := runs()
	if len(got) != 1 {
		t.Fatalf("command ran %d times, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.command != "notify-lead" {
		t.Errorf("command = %q", r.command)
	}
	var e Event
	if err := json.Unmarshal(r.stdin, &e); err != nil {
		t.Fatalf("stdin is not event JSON: %q", r.stdin)
	}
	if e.Type != TypeSessionWaiting || e.Session != "s1" || e.Time == "" {
		t.Errorf("stdin event = %+v", e)
	}
	if len(r.env) != 1 || r.env[0] != "ZEN_EVENT_TYPE=session_waiting" {
		t.Errorf("env = %v", r.env)
	}
	// The command works without the event log.
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Error("Emit wrote the log with Log: false")
	}
}

func TestEmitNoCommand(t *testing.T) {
	runs := recordRuns(t)
	Emit(Options{CommandTypes: []string{TypeSlackTask}}, Event{Type: TypeSlackTask})
	if got := runs(); len(got) != 0 {
		t.Errorf("command ran with no command configured: %+v", got)
	}
}

func TestEmitDoesNotBlockOnSlowCommand(t *testing.T) {
	release := make(chan struct{})
	var started atomic.Int32
	restore := SetCommandRunner(func(ctx context.Context, _ string, _ []byte, _ []string) error {
		started.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	defer restore()

	opts := Options{Command: "slow", CommandTypes: []string{TypeSlackTask}}
	start := time.Now()
	for i := 0; i < maxConcurrent+2; i++ {
		Emit(opts, Event{Type: TypeSlackTask})
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Emit blocked for %s behind a slow command", d)
	}
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() < maxConcurrent && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := started.Load(); n != maxConcurrent {
		t.Errorf("%d runs started at once, want the limit %d", n, maxConcurrent)
	}
	close(release)
	Wait()
	if n := started.Load(); n != maxConcurrent+2 {
		t.Errorf("%d runs in total, want %d", n, maxConcurrent+2)
	}
}

func TestRunShell(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	err := runShell(context.Background(), `cat > "$OUT"; printf '%s' "$ZEN_EVENT_TYPE" >> "$OUT"`, []byte(`{"type":"slack_task"}`), []string{"ZEN_EVENT_TYPE=slack_task", "OUT=" + out})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != `{"type":"slack_task"}slack_task` {
		t.Errorf("command saw %q", got)
	}
}

func TestRunShellFailureCarriesOutput(t *testing.T) {
	err := runShell(context.Background(), "echo boom >&2; exit 3", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("runShell error = %v, want one mentioning the output", err)
	}
}

func TestRunShellTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := runShell(ctx, "sleep 30", nil, nil)
	if err == nil {
		t.Fatal("runShell should fail when the deadline passes")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("runShell took %s past a 100ms deadline", d)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("ctx.Err() = %v", ctx.Err())
	}
}
