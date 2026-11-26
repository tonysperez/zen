package events

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("line %q is not an event: %v", l, err)
		}
		out = append(out, e)
	}
	return out
}

func TestAppend(t *testing.T) {
	path := Path(t.TempDir() + "/state") // directory created on demand
	if err := Append(path, Event{Type: TypeReviewRequested, Repo: "mono", PR: 42}); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, Event{Time: "2026-01-02T03:04:05Z", Type: TypeSessionWaiting, Session: "abc"}); err != nil {
		t.Fatal(err)
	}

	got := readEvents(t, path)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Type != TypeReviewRequested || got[0].Repo != "mono" || got[0].PR != 42 {
		t.Errorf("first event = %+v", got[0])
	}
	if _, err := time.Parse(time.RFC3339, got[0].Time); err != nil {
		t.Errorf("Append should fill an RFC3339 time, got %q", got[0].Time)
	}
	if got[1].Time != "2026-01-02T03:04:05Z" {
		t.Errorf("Append replaced an explicit time: %q", got[1].Time)
	}

	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), `"worktree"`) || strings.Contains(string(raw), `"data"`) {
		t.Errorf("empty fields should be omitted: %s", raw)
	}
}

func TestAppendConcurrentLinesStayWhole(t *testing.T) {
	path := Path(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Append(path, Event{Type: TypePRUpdated, PR: i, Data: map[string]any{"pad": strings.Repeat("x", 512)}}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if got := readEvents(t, path); len(got) != 50 {
		t.Errorf("got %d events, want 50", len(got))
	}
}

func TestAppendRotates(t *testing.T) {
	old := MaxSize
	MaxSize = 200
	defer func() { MaxSize = old }()

	path := Path(t.TempDir())
	for i := 1; i <= 6; i++ {
		if err := Append(path, Event{Type: TypePRUpdated, PR: i, Data: map[string]any{"pad": "0123456789012345678901234567890123456789"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= 2*MaxSize {
		t.Errorf("current log is %d bytes, rotation did not happen", info.Size())
	}

	// Read sees the backup first, then the current file, with no loss of
	// the events in either.
	lines, offset, err := Read(path, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if offset != info.Size() {
		t.Errorf("offset = %d, want current size %d", offset, info.Size())
	}
	backup := readEvents(t, path+".1")
	current := readEvents(t, path)
	if len(lines) != len(backup)+len(current) {
		t.Fatalf("Read returned %d lines, want %d", len(lines), len(backup)+len(current))
	}
	var first Event
	json.Unmarshal(lines[0], &first)
	if first.PR != backup[0].PR {
		t.Errorf("Read should start with the backup, got PR %d", first.PR)
	}
}

func TestFilter(t *testing.T) {
	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	line := func(typ string, at time.Time) []byte {
		b, _ := json.Marshal(Event{Type: typ, Time: at.Format(time.RFC3339)})
		return b
	}
	tests := []struct {
		name   string
		filter Filter
		line   []byte
		want   bool
	}{
		{"zero filter", Filter{}, line(TypeSlackTask, base), true},
		{"type match", Filter{Types: map[string]bool{TypeSlackTask: true}}, line(TypeSlackTask, base), true},
		{"type miss", Filter{Types: map[string]bool{TypeSlackTask: true}}, line(TypePRUpdated, base), false},
		{"since equal", Filter{Since: base}, line(TypePRUpdated, base), true},
		{"since before", Filter{Since: base}, line(TypePRUpdated, base.Add(-time.Second)), false},
		{"not json", Filter{}, []byte("garbage"), false},
		{"no type", Filter{}, []byte(`{"time":"2026-01-02T12:00:00Z"}`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Match(tt.line); got != tt.want {
				t.Errorf("Match(%s) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	if got, err := ParseSince("2h", now); err != nil || !got.Equal(now.Add(-2*time.Hour)) {
		t.Errorf("ParseSince(2h) = %v, %v", got, err)
	}
	if got, err := ParseSince("2026-01-01T00:00:00Z", now); err != nil || !got.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("ParseSince(RFC3339) = %v, %v", got, err)
	}
	if got, err := ParseSince("", now); err != nil || !got.IsZero() {
		t.Errorf("ParseSince(\"\") = %v, %v", got, err)
	}
	if _, err := ParseSince("yesterday", now); err == nil {
		t.Error("ParseSince(yesterday) should fail")
	}
}

func TestParseTypes(t *testing.T) {
	got, err := ParseTypes("pr-review, slack-task,")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[TypeReviewRequested] || !got[TypeSlackTask] {
		t.Errorf("ParseTypes = %v", got)
	}
	if _, err := ParseTypes("pr-reveiw"); err == nil {
		t.Error("ParseTypes should reject an unknown type")
	}
}

func TestReadMissing(t *testing.T) {
	lines, offset, err := Read(Path(t.TempDir()), Filter{})
	if err != nil || len(lines) != 0 || offset != 0 {
		t.Errorf("Read(missing) = %v, %d, %v", lines, offset, err)
	}
}

// collector gathers Follow output safely across goroutines.
type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) add(l []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, string(l))
}

func (c *collector) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		if len(c.lines) >= n {
			out := append([]string(nil), c.lines...)
			c.mu.Unlock()
			return out
		}
		c.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("got %d lines, want %d: %v", len(c.lines), n, c.lines)
	return nil
}

func TestFollowSurvivesRotation(t *testing.T) {
	old := MaxSize
	MaxSize = 150
	defer func() { MaxSize = old }()

	path := Path(t.TempDir())
	if err := Append(path, Event{Type: TypePRUpdated, PR: 1}); err != nil {
		t.Fatal(err)
	}
	_, offset, err := Read(path, Filter{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var c collector
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, offset, 10*time.Millisecond, c.add) }()

	// PR 1 was before offset and must not be repeated. Enough appends to
	// rotate more than once.
	for i := 2; i <= 8; i++ {
		if err := Append(path, Event{Type: TypePRUpdated, PR: i, Data: map[string]any{"pad": "01234567890123456789012345678901234567890123456789"}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	got := c.waitFor(t, 7)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Follow() error: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("test did not rotate the log")
	}
	for i, l := range got {
		var e Event
		json.Unmarshal([]byte(l), &e)
		if e.PR != i+2 {
			t.Errorf("line %d has PR %d, want %d (lines: %v)", i, e.PR, i+2, got)
		}
	}
}

func TestFollowWaitsForFile(t *testing.T) {
	path := Path(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var c collector
	go Follow(ctx, path, 0, 10*time.Millisecond, c.add)

	time.Sleep(30 * time.Millisecond)
	if err := Append(path, Event{Type: TypeSlackTask}); err != nil {
		t.Fatal(err)
	}
	if got := c.waitFor(t, 1); !strings.Contains(got[0], TypeSlackTask) {
		t.Errorf("got %v", got)
	}
}

func TestEmitLogOnlyWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	Emit(Options{Log: false, StateDir: dir}, Event{Type: TypeSlackTask})
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatal("Emit wrote the log while it was off")
	}
	Emit(Options{Log: true, StateDir: dir}, Event{Type: TypeSlackTask})
	if got := readEvents(t, Path(dir)); len(got) != 1 || got[0].Time == "" {
		t.Errorf("Emit with Log: got %+v", got)
	}
}
