// Package events is zen's append-only event log: one JSON object per line in
// <state dir>/events.jsonl. The watch daemon appends to it when something a
// supervisor might act on happens (a review request, a prepared worktree, a
// session waiting for input), so a watcher can tail one file instead of
// polling the inbox and worktrees.
//
// Event types share the names of the daemon's notifications (see the notify
// package) where there is one, so a type means the same thing in
// events.jsonl, in notify.events and in $ZEN_EVENT.
package events

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mgreau/zen/internal/notify"
)

// Event types.
const (
	TypeReviewRequested = notify.EventPRReview      // first sighting of a review request with no local worktree
	TypeReviewPrepared  = notify.EventWorktreeReady // the daemon created a review worktree
	TypePRUpdated       = notify.EventPRUpdated     // a review worktree moved to a new GitHub head
	TypeSessionWaiting  = notify.EventWaiting       // an agent session went from running to waiting for input
	TypeSlackTask       = notify.EventSlackTask     // the Slack watcher picked up a flagged message

	// Log-only: no notification is sent for these.
	TypeSessionStopped = "session-stopped" // an agent session's process exited
	TypeReviewCleaned  = "review-cleaned"  // a merged review worktree was removed
)

// Types lists every event type, sorted.
func Types() []string {
	t := []string{
		TypeReviewRequested,
		TypeReviewPrepared,
		TypePRUpdated,
		TypeSessionWaiting,
		TypeSessionStopped,
		TypeReviewCleaned,
		TypeSlackTask,
	}
	sort.Strings(t)
	return t
}

// IsType reports whether name is a known event type.
func IsType(name string) bool {
	for _, t := range Types() {
		if t == name {
			return true
		}
	}
	return false
}

// Event is one line of events.jsonl. Fields that do not apply to an event
// type are omitted.
type Event struct {
	Time     string         `json:"time"` // RFC3339
	Type     string         `json:"type"`
	Repo     string         `json:"repo,omitempty"` // short repo name from config
	PR       int            `json:"pr,omitempty"`
	Worktree string         `json:"worktree,omitempty"` // worktree path
	Session  string         `json:"session,omitempty"`  // agent session ID
	SHA      string         `json:"sha,omitempty"`
	URL      string         `json:"url,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// MaxSize is the size at which Append rotates events.jsonl to events.jsonl.1,
// matching watch.log. A variable so tests can rotate small files.
var MaxSize int64 = 10 * 1024 * 1024

// FileName is the event log's name inside the state directory.
const FileName = "events.jsonl"

// Path returns the event log path inside stateDir.
func Path(stateDir string) string {
	return filepath.Join(stateDir, FileName)
}

var appendMu sync.Mutex

// Append writes e as one line to the log at path, filling in Time when it is
// empty. The file is opened with O_APPEND and the line is written in a single
// write, so a reader never sees a partial line from a concurrent writer in
// another process. When the log has reached MaxSize, it is first renamed to
// path+".1", replacing any previous backup.
func Append(path string, e Event) error {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding event: %w", err)
	}
	line = append(line, '\n')

	appendMu.Lock()
	defer appendMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= MaxSize {
		if err := os.Rename(path, path+".1"); err != nil {
			return fmt.Errorf("rotating %s: %w", path, err)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Filter selects events by time and type. The zero Filter matches every
// event.
type Filter struct {
	Since time.Time       // zero: no lower bound
	Types map[string]bool // empty: every type
}

// Match reports whether a raw log line passes the filter. Lines that are not
// valid events never match.
func (f Filter) Match(line []byte) bool {
	var e struct {
		Time string `json:"time"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &e); err != nil || e.Type == "" {
		return false
	}
	if len(f.Types) > 0 && !f.Types[e.Type] {
		return false
	}
	if !f.Since.IsZero() {
		t, err := time.Parse(time.RFC3339, e.Time)
		if err != nil || t.Before(f.Since) {
			return false
		}
	}
	return true
}

// ParseSince parses a --since value: an RFC3339 time, or a Go duration such
// as "2h" meaning that long before now.
func ParseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			d = -d
		}
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q: want an RFC3339 time or a duration like 2h", s)
}

// ParseTypes parses a comma-separated --type value. Unknown names are an
// error, so a typo does not silently match nothing.
func ParseTypes(s string) (map[string]bool, error) {
	types := make(map[string]bool)
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !IsType(t) {
			return nil, fmt.Errorf("unknown event type %q (known: %s)", t, strings.Join(Types(), ", "))
		}
		types[t] = true
	}
	return types, nil
}

// Read returns the lines of the rotated backup and then the current log that
// pass f, oldest first, and the size of the current log, which is where
// Follow should start. A missing file is not an error.
func Read(path string, f Filter) (lines [][]byte, offset int64, err error) {
	for _, p := range []string{path + ".1", path} {
		file, err := os.Open(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		n, err := scanLines(file, func(line []byte) {
			if f.Match(line) {
				lines = append(lines, append([]byte(nil), line...))
			}
		})
		file.Close()
		if err != nil {
			return nil, 0, err
		}
		if p == path {
			offset = n
		}
	}
	return lines, offset, nil
}

// scanLines calls fn for each complete line in r and returns the number of
// bytes consumed, which stops before a trailing partial line.
func scanLines(r io.Reader, fn func([]byte)) (int64, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var n int64
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			n += int64(len(line))
			if l := bytes.TrimSpace(line); len(l) > 0 {
				fn(l)
			}
		}
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}
