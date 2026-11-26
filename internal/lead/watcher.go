package lead

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/events"
	"github.com/mgreau/zen/internal/notify"
	slackpkg "github.com/mgreau/zen/internal/slack"
)

// API is the part of the Slack client the watcher uses.
type API interface {
	ConversationsHistory(ctx context.Context, channel, oldest string, maxPages int) ([]slackpkg.Message, error)
	ThreadRepliesSince(ctx context.Context, channel, threadTS, oldest string) ([]slackpkg.Message, error)
	Permalink(ctx context.Context, channel, ts string) (string, error)
}

const (
	// MaxText caps data.text in a slack-message event.
	MaxText = 20000
	// historyPages caps conversations.history pages (200 messages each)
	// read per poll. Threads whose parent falls past them are not checked.
	historyPages = 3
	// MaxThreadsPerPoll caps conversations.replies calls per poll. Threads
	// left over keep their cursor and are read on the next poll.
	MaxThreadsPerPoll = 10
)

// Watcher reads the lead channel and reports the signed-in user's new
// messages, top-level and in threads, as slack-message events.
type Watcher struct {
	Client    API
	StatePath string
	// Now defaults to time.Now.
	Now func() time.Time
}

func (w *Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// pendingThread is a thread with replies newer than its cursor.
type pendingThread struct {
	ts, cursor, latest string
}

// Poll reads channel once and returns a slack-message event, oldest first,
// for each new message by selfUserID that zen did not post: neither a
// recorded zen slack post nor text starting with prefix. On the first poll
// for a channel it only records the starting point and returns nothing.
// The cursors are saved before Poll returns, so each message is reported
// once.
func (w *Watcher) Poll(ctx context.Context, channel, selfUserID, prefix string) ([]events.Event, error) {
	now := w.now()
	st, err := Load(w.StatePath)
	if err != nil {
		return nil, err
	}
	if st.Channel != channel || st.Since == "" {
		start := slackpkg.FormatTS(now)
		return nil, Update(ctx, w.StatePath, func(s *State) error {
			s.Channel, s.Since, s.Cursor = channel, start, start
			s.Threads, s.Seen = nil, nil
			return nil
		})
	}

	windowStart := slackpkg.FormatTS(now.Add(-ActiveWindow))
	if slackpkg.CompareTS(st.Since, windowStart) > 0 {
		windowStart = st.Since
	}
	history, err := w.Client.ConversationsHistory(ctx, channel, windowStart, historyPages)
	if err != nil {
		return nil, fmt.Errorf("reading lead channel: %w", err)
	}

	cursor := st.Cursor
	var candidates []slackpkg.Message
	var pending []pendingThread
	threads := make(map[string]string)
	for _, m := range history {
		if m.ThreadTS != "" && m.ThreadTS != m.Ts {
			// A reply also sent to the channel: read with its thread.
			continue
		}
		if slackpkg.CompareTS(m.Ts, st.Cursor) > 0 {
			candidates = append(candidates, slackpkg.Message{User: m.User, Text: m.Text, Ts: m.Ts, Subtype: m.Subtype, BotID: m.BotID})
			if slackpkg.CompareTS(m.Ts, cursor) > 0 {
				cursor = m.Ts
			}
		}
		tc, known := st.Threads[m.Ts]
		if !known {
			tc = m.Ts
			if slackpkg.CompareTS(st.Since, tc) > 0 {
				tc = st.Since
			}
		}
		if m.ReplyCount == 0 || m.LatestReply == "" {
			if known {
				threads[m.Ts] = tc
			}
			continue
		}
		threads[m.Ts] = tc
		if slackpkg.CompareTS(m.LatestReply, tc) > 0 {
			pending = append(pending, pendingThread{ts: m.Ts, cursor: tc, latest: m.LatestReply})
		}
	}

	// Most recent activity first; the rest wait for the next poll.
	sort.Slice(pending, func(i, j int) bool { return slackpkg.CompareTS(pending[i].latest, pending[j].latest) > 0 })
	if len(pending) > MaxThreadsPerPoll {
		pending = pending[:MaxThreadsPerPoll]
	}
	var errs []error
	for _, p := range pending {
		replies, err := w.Client.ThreadRepliesSince(ctx, channel, p.ts, p.cursor)
		if err != nil {
			errs = append(errs, fmt.Errorf("reading thread %s: %w", p.ts, err))
			continue
		}
		newest := p.latest
		for _, r := range replies {
			if r.Ts == p.ts || slackpkg.CompareTS(r.Ts, p.cursor) <= 0 {
				continue
			}
			r.ThreadTS = p.ts
			candidates = append(candidates, r)
			if slackpkg.CompareTS(r.Ts, newest) > 0 {
				newest = r.Ts
			}
		}
		threads[p.ts] = newest
	}

	sort.Slice(candidates, func(i, j int) bool { return slackpkg.CompareTS(candidates[i].Ts, candidates[j].Ts) < 0 })

	var reported []slackpkg.Message
	err = Update(ctx, w.StatePath, func(s *State) error {
		if s.Channel != channel {
			// Changed under us (a reset); start over next poll.
			return nil
		}
		for _, m := range candidates {
			if s.Seen[m.Ts] != 0 || !fromUser(m, selfUserID) || s.Posted[m.Ts] != 0 || isZenPost(m.Text, prefix) {
				continue
			}
			if s.Seen == nil {
				s.Seen = make(map[string]int64)
			}
			s.Seen[m.Ts] = now.Unix()
			reported = append(reported, m)
		}
		s.Cursor = cursor
		s.Threads = threads
		prune(s.Seen, now.Add(-RetainFor))
		prune(s.Posted, now.Add(-RetainFor))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("saving lead channel state: %w", err)
	}

	evs := make([]events.Event, 0, len(reported))
	for _, m := range reported {
		evs = append(evs, w.event(ctx, channel, m))
	}
	return evs, errors.Join(errs...)
}

// fromUser reports whether m is an ordinary message by userID: not a bot
// message, join notice or other system subtype.
func fromUser(m slackpkg.Message, userID string) bool {
	if m.User == "" || m.User != userID || m.BotID != "" {
		return false
	}
	switch m.Subtype {
	case "", "thread_broadcast", "file_share", "me_message":
		return true
	}
	return false
}

// isZenPost reports whether text starts with zen slack post's prefix. Slack
// trims trailing spaces from a message, so the prefix is compared without
// them.
func isZenPost(text, prefix string) bool {
	p := strings.TrimSpace(prefix)
	return p != "" && strings.HasPrefix(strings.TrimSpace(text), p)
}

func (w *Watcher) event(ctx context.Context, channel string, m slackpkg.Message) events.Event {
	permalink, err := w.Client.Permalink(ctx, channel, m.Ts)
	if err != nil {
		permalink = ""
	}
	text, truncated := notify.TruncateUTF8(m.Text, MaxText)
	data := map[string]any{
		"channel":   channel,
		"ts":        m.Ts,
		"thread_ts": m.ThreadTS,
		"text":      text,
		"user":      m.User,
	}
	if truncated {
		data["truncated"] = true
	}
	return events.Event{
		Time: time.Now().UTC().Format(time.RFC3339),
		Type: events.TypeSlackMessage,
		URL:  permalink,
		Data: data,
	}
}
