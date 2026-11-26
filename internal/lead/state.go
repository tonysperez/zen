// Package lead is zen's Slack lead channel: one Slack conversation
// (slack.lead_channel) that a lead agent session and the user talk through.
// The lead posts with zen slack post, which can only reach that channel;
// the watch daemon reads it with Watcher and turns each message the user
// writes there into a slack-message event.
//
// zen signs in to Slack with a user token, so its posts appear as the user.
// Watcher therefore skips a message whose timestamp zen slack post recorded,
// or whose text starts with the post prefix (slack.lead_prefix), so the
// lead never reads its own posts back as requests.
package lead

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	slackpkg "github.com/mgreau/zen/internal/slack"
)

// StateFileName is the lead channel's state file inside the state directory.
const StateFileName = "slack_lead.json"

// StatePath returns the state file path inside stateDir.
func StatePath(stateDir string) string {
	return filepath.Join(stateDir, StateFileName)
}

// State is what slack_lead.json holds. The daemon owns everything except
// Posted, which zen slack post adds to; both change it only through Update.
type State struct {
	// Channel is the channel the cursors below belong to. A different
	// slack.lead_channel starts over from now.
	Channel string `json:"channel,omitempty"`
	// Since is the Slack timestamp watching started at. Nothing older is
	// reported, so turning the lead channel on does not replay history.
	Since string `json:"since,omitempty"`
	// Cursor is the newest top-level message handled.
	Cursor string `json:"cursor,omitempty"`
	// Threads maps a thread's parent ts to the newest reply handled, for
	// threads started in the last ActiveWindow.
	Threads map[string]string `json:"threads,omitempty"`
	// Seen holds the messages reported as slack-message events, with the
	// Unix time each was reported, pruned after RetainFor.
	Seen map[string]int64 `json:"seen,omitempty"`
	// Posted holds the messages zen slack post sent, with the Unix time each
	// was sent, pruned after RetainFor.
	Posted map[string]int64 `json:"posted,omitempty"`
}

const (
	// ActiveWindow is how long after a message is posted the watcher keeps
	// looking for new replies in its thread.
	ActiveWindow = 7 * 24 * time.Hour
	// RetainFor is how long Seen and Posted entries are kept: past
	// ActiveWindow, so a reply can never be reported twice.
	RetainFor = ActiveWindow + 24*time.Hour
)

// Load reads the state file. A missing file is an empty state.
func Load(path string) (*State, error) {
	st := &State{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return st, nil
}

// Update loads the state file under an exclusive lock, lets fn change it,
// and writes it back atomically. The daemon and zen slack post both write
// it, so neither may overwrite what the other just added.
func Update(ctx context.Context, path string, fn func(*State) error) error {
	unlock, err := slackpkg.LockTokenFile(ctx, path)
	if err != nil {
		return fmt.Errorf("locking %s: %w", path, err)
	}
	defer unlock()
	st, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return save(path, st)
}

func save(path string, st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".slack_lead-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// prune drops Seen and Posted entries recorded before cutoff.
func prune(m map[string]int64, cutoff time.Time) {
	for ts, at := range m {
		if at < cutoff.Unix() {
			delete(m, ts)
		}
	}
}
