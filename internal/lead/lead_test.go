package lead

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
	"github.com/mgreau/zen/internal/notify"
	slackpkg "github.com/mgreau/zen/internal/slack"
)

const (
	leadChan = "C0LEAD0001"
	self     = "U0SELF"
	prefix   = config.DefaultLeadPrefix
)

// fakeSlack is a tiny in-memory Slack Web API: one channel's top-level
// messages and threads, plus a log of every request.
type fakeSlack struct {
	mu          sync.Mutex
	top         []slackpkg.Message            // top-level messages
	replies     map[string][]slackpkg.Message // thread ts -> replies
	requests    []string                      // "method channel=..."
	posts       []map[string]any              // chat.postMessage bodies
	reminders   []map[string]any              // reminders.add bodies
	reminderErr string
	nextTS      int64
}

func newFakeSlack(t *testing.T) (*fakeSlack, *slackpkg.Client) {
	t.Helper()
	f := &fakeSlack{replies: make(map[string][]slackpkg.Message), nextTS: 1_800_000_000}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, slackpkg.NewClient("xoxp-test").WithBaseURL(srv.URL + "/")
}

func (f *fakeSlack) addTop(user, ts, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.top = append(f.top, slackpkg.Message{User: user, Ts: ts, Text: text})
}

func (f *fakeSlack) addReply(parent, user, ts, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[parent] = append(f.replies[parent], slackpkg.Message{User: user, Ts: ts, Text: text, ThreadTS: parent})
}

func (f *fakeSlack) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	method := strings.TrimPrefix(r.URL.Path, "/")
	q := r.URL.Query()
	var body map[string]any
	if r.Method == http.MethodPost {
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
	}
	f.requests = append(f.requests, method+" channel="+q.Get("channel"))
	reply := func(v map[string]any) {
		v["ok"] = true
		json.NewEncoder(w).Encode(v)
	}
	switch method {
	case "conversations.history":
		oldest := q.Get("oldest")
		var out []slackpkg.Message
		for _, m := range f.top {
			if oldest != "" && slackpkg.CompareTS(m.Ts, oldest) <= 0 {
				continue
			}
			if rs := f.replies[m.Ts]; len(rs) > 0 {
				m.ThreadTS = m.Ts
				m.ReplyCount = len(rs)
				m.LatestReply = rs[len(rs)-1].Ts
			}
			out = append(out, m)
		}
		sort.Slice(out, func(i, j int) bool { return slackpkg.CompareTS(out[i].Ts, out[j].Ts) > 0 })
		reply(map[string]any{"messages": out})
	case "conversations.replies":
		ts := q.Get("ts")
		var parent slackpkg.Message
		for _, m := range f.top {
			if m.Ts == ts {
				parent = m
			}
		}
		parent.ThreadTS = ts
		out := []slackpkg.Message{parent}
		for _, m := range f.replies[ts] {
			if o := q.Get("oldest"); o == "" || slackpkg.CompareTS(m.Ts, o) > 0 {
				out = append(out, m)
			}
		}
		reply(map[string]any{"messages": out})
	case "chat.getPermalink":
		reply(map[string]any{"permalink": "https://x.slack.com/archives/" + q.Get("channel") + "/p" + strings.ReplaceAll(q.Get("message_ts"), ".", "")})
	case "chat.postMessage":
		f.requests[len(f.requests)-1] = method + fmt.Sprintf(" channel=%v", body["channel"])
		f.posts = append(f.posts, body)
		f.nextTS++
		ts := fmt.Sprintf("%d.000100", f.nextTS)
		m := slackpkg.Message{User: self, Ts: ts, Text: fmt.Sprint(body["text"])}
		if th, ok := body["thread_ts"].(string); ok && th != "" {
			m.ThreadTS = th
			f.replies[th] = append(f.replies[th], m)
		} else {
			f.top = append(f.top, m)
		}
		reply(map[string]any{"ts": ts, "channel": body["channel"]})
	case "reminders.add":
		f.reminders = append(f.reminders, body)
		if f.reminderErr != "" {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": f.reminderErr})
			return
		}
		reply(map[string]any{})
	default:
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "unknown_method"})
	}
}

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func ts(t time.Time) string { return slackpkg.FormatTS(t) }

func newWatcher(t *testing.T, client *slackpkg.Client, c *clock) *Watcher {
	t.Helper()
	return &Watcher{Client: client, StatePath: filepath.Join(t.TempDir(), StateFileName), Now: c.now}
}

func texts(evs []events.Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Data["text"].(string))
	}
	return out
}

func poll(t *testing.T, w *Watcher) []events.Event {
	t.Helper()
	evs, err := w.Poll(context.Background(), leadChan, self, prefix)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return evs
}

func TestPollFirstEnableStartsAtNow(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	f.addTop(self, ts(c.t.Add(-time.Hour)), "old request")

	w := newWatcher(t, client, c)
	if evs := poll(t, w); len(evs) != 0 {
		t.Fatalf("first poll reported %v, want nothing (no replay)", texts(evs))
	}
	if n := len(f.requests); n != 0 {
		t.Errorf("first poll made %d Slack calls, want 0: %v", n, f.requests)
	}
	c.t = c.t.Add(time.Minute)
	if evs := poll(t, w); len(evs) != 0 {
		t.Errorf("second poll reported history %v", texts(evs))
	}
}

func TestPollReportsOnlyUsersOwnNonZenMessages(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	w := newWatcher(t, client, c)
	poll(t, w) // start

	base := c.t
	f.addTop(self, ts(base.Add(1*time.Second)), "please review PR 12")
	f.addTop("U0OTHER", ts(base.Add(2*time.Second)), "someone else")
	f.addTop(self, ts(base.Add(3*time.Second)), prefix+"status: all green")
	f.addTop(self, ts(base.Add(4*time.Second)), ":robot_face: trimmed prefix")
	f.mu.Lock()
	f.top = append(f.top, slackpkg.Message{User: self, Ts: ts(base.Add(5 * time.Second)), Text: "joined", Subtype: "channel_join"})
	f.mu.Unlock()
	c.t = base.Add(time.Minute)

	evs := poll(t, w)
	if got := texts(evs); len(got) != 1 || got[0] != "please review PR 12" {
		t.Fatalf("Poll reported %q, want only the user's own request", got)
	}
	e := evs[0]
	if e.Type != events.TypeSlackMessage {
		t.Errorf("type = %q", e.Type)
	}
	if e.Data["channel"] != leadChan || e.Data["ts"] != ts(base.Add(time.Second)) || e.Data["thread_ts"] != "" || e.Data["user"] != self {
		t.Errorf("data = %v", e.Data)
	}
	if !strings.HasPrefix(e.URL, "https://x.slack.com/archives/"+leadChan) {
		t.Errorf("url = %q, want a permalink", e.URL)
	}

	if evs := poll(t, w); len(evs) != 0 {
		t.Errorf("repeat poll reported %v again", texts(evs))
	}
}

func TestPollPicksUpThreadReplies(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	w := newWatcher(t, client, c)
	poll(t, w)

	base := c.t
	parent := ts(base.Add(time.Second))
	f.addTop(self, parent, prefix+"Shall I merge PR 7?")
	c.t = base.Add(time.Minute)
	if evs := poll(t, w); len(evs) != 0 {
		t.Fatalf("zen's own post reported: %v", texts(evs))
	}

	f.addReply(parent, self, ts(base.Add(2*time.Minute)), "yes, merge it")
	f.addReply(parent, "U0OTHER", ts(base.Add(3*time.Minute)), "+1")
	c.t = base.Add(4 * time.Minute)
	evs := poll(t, w)
	if got := texts(evs); len(got) != 1 || got[0] != "yes, merge it" {
		t.Fatalf("Poll reported %q, want the user's reply", got)
	}
	if evs[0].Data["thread_ts"] != parent {
		t.Errorf("thread_ts = %v, want %s", evs[0].Data["thread_ts"], parent)
	}

	// A later reply in the same thread is new; the earlier one is not.
	f.addReply(parent, self, ts(base.Add(5*time.Minute)), "and close the issue")
	c.t = base.Add(6 * time.Minute)
	if got := texts(poll(t, w)); len(got) != 1 || got[0] != "and close the issue" {
		t.Fatalf("Poll reported %q, want only the newest reply", got)
	}
	// With no new replies, the thread is not read again.
	f.mu.Lock()
	f.requests = nil
	f.mu.Unlock()
	poll(t, w)
	for _, r := range f.requests {
		if strings.HasPrefix(r, "conversations.replies") {
			t.Errorf("thread re-read with no new replies: %v", f.requests)
		}
	}
}

func TestPollSkipsRecordedPostsWithoutPrefix(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	w := newWatcher(t, client, c)
	poll(t, w)

	// A post recorded by zen slack post is skipped even if Slack shows its
	// text without the prefix.
	posted := ts(c.t.Add(time.Second))
	if err := Update(context.Background(), w.StatePath, func(s *State) error {
		s.Posted = map[string]int64{posted: c.t.Unix()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.addTop(self, posted, "no prefix here")
	c.t = c.t.Add(time.Minute)
	if evs := poll(t, w); len(evs) != 0 {
		t.Errorf("recorded post reported: %v", texts(evs))
	}
}

func TestPollCursorsPersist(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	w := newWatcher(t, client, c)
	poll(t, w)
	base := c.t
	parent := ts(base.Add(time.Second))
	f.addTop(self, parent, "first")
	f.addReply(parent, self, ts(base.Add(2*time.Second)), "reply")
	c.t = base.Add(time.Minute)
	if got := texts(poll(t, w)); len(got) != 2 {
		t.Fatalf("Poll reported %q, want the message and its reply", got)
	}

	st, err := Load(w.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Channel != leadChan || st.Cursor != parent || st.Threads[parent] != ts(base.Add(2*time.Second)) {
		t.Errorf("saved state = %+v", st)
	}
	if st.Seen[parent] == 0 {
		t.Errorf("seen not saved: %+v", st.Seen)
	}

	// A new watcher (daemon restart) on the same file reports nothing old.
	w2 := &Watcher{Client: client, StatePath: w.StatePath, Now: c.now}
	if evs := poll(t, w2); len(evs) != 0 {
		t.Errorf("after restart, reported %v again", texts(evs))
	}

	// A different lead channel starts over from now.
	if _, err := w2.Poll(context.Background(), "C0OTHER001", self, prefix); err != nil {
		t.Fatal(err)
	}
	st, _ = Load(w.StatePath)
	if st.Channel != "C0OTHER001" || len(st.Threads) != 0 {
		t.Errorf("channel change kept old cursors: %+v", st)
	}
}

func TestPollCapsThreadsPerPoll(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	w := newWatcher(t, client, c)
	poll(t, w)
	base := c.t
	n := MaxThreadsPerPoll + 3
	for i := range n {
		parent := ts(base.Add(time.Duration(i+1) * time.Second))
		f.addTop(self, parent, prefix+fmt.Sprintf("q%d", i))
		f.addReply(parent, self, ts(base.Add(time.Minute+time.Duration(i)*time.Second)), fmt.Sprintf("a%d", i))
	}
	c.t = base.Add(2 * time.Minute)
	if got := poll(t, w); len(got) != MaxThreadsPerPoll {
		t.Fatalf("first poll reported %d replies, want the cap %d", len(got), MaxThreadsPerPoll)
	}
	if got := poll(t, w); len(got) != 3 {
		t.Fatalf("second poll reported %d replies, want the 3 left over", len(got))
	}
}

func TestPollTruncatesText(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	w := newWatcher(t, client, c)
	poll(t, w)
	f.addTop(self, ts(c.t.Add(time.Second)), strings.Repeat("é", MaxText))
	c.t = c.t.Add(time.Minute)
	evs := poll(t, w)
	if len(evs) != 1 || len(evs[0].Data["text"].(string)) > MaxText || evs[0].Data["truncated"] != true {
		t.Fatalf("text not cut to %d bytes", MaxText)
	}
}

func newPoster(t *testing.T, client *slackpkg.Client, sc config.SlackConfig) *Poster {
	t.Helper()
	p, err := NewPoster(client, sc, filepath.Join(t.TempDir(), StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPostOnlyToLeadChannel(t *testing.T) {
	f, client := newFakeSlack(t)
	if _, err := NewPoster(client, config.SlackConfig{}, "x"); err != ErrNoLeadChannel {
		t.Errorf("NewPoster with no lead_channel = %v, want ErrNoLeadChannel", err)
	}

	p := newPoster(t, client, config.SlackConfig{LeadChannel: leadChan})
	ctx := context.Background()
	// Text that looks like a channel, and a thread ts, change nothing.
	got, err := p.Post(ctx, "<#C0OTHER001> hello @here", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ts == "" || !strings.Contains(got.Permalink, leadChan) {
		t.Errorf("Post = %+v", got)
	}
	if _, err := p.Post(ctx, "in thread", got.Ts); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Post(ctx, "x", "C0OTHER001"); err == nil {
		t.Error("Post accepted a channel ID as --thread")
	}
	for _, r := range f.requests {
		if !strings.HasSuffix(r, "channel="+leadChan) {
			t.Errorf("request outside the lead channel: %s", r)
		}
	}
	if len(f.posts) != 2 {
		t.Fatalf("posts = %v", f.posts)
	}
	if f.posts[0]["text"] != prefix+"<#C0OTHER001> hello @here" {
		t.Errorf("text = %q, want the prefix added", f.posts[0]["text"])
	}
	if _, ok := f.posts[0]["thread_ts"]; ok {
		t.Error("top-level post sent a thread_ts")
	}
	if f.posts[1]["thread_ts"] != got.Ts {
		t.Errorf("thread reply thread_ts = %v, want %s", f.posts[1]["thread_ts"], got.Ts)
	}
	if _, err := p.Post(ctx, "   ", ""); err == nil {
		t.Error("Post accepted an empty message")
	}
}

func TestPostIsSkippedByWatcher(t *testing.T) {
	f, client := newFakeSlack(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	sc := config.SlackConfig{LeadChannel: leadChan, LeadPrefix: "[lead] "}
	statePath := filepath.Join(t.TempDir(), StateFileName)
	w := &Watcher{Client: client, StatePath: statePath, Now: c.now}
	if _, err := w.Poll(context.Background(), leadChan, self, sc.GetLeadPrefix()); err != nil {
		t.Fatal(err)
	}
	p, _ := NewPoster(client, sc, statePath)
	f.nextTS = c.t.Unix()
	posted, err := p.Post(context.Background(), "status", "")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := Load(statePath)
	if st.Posted[posted.Ts] == 0 {
		t.Fatalf("post not recorded: %+v", st)
	}
	f.addReply(posted.Ts, self, ts(c.t.Add(30*time.Second)), "ok")
	c.t = c.t.Add(time.Minute)
	evs, err := w.Poll(context.Background(), leadChan, self, sc.GetLeadPrefix())
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(evs); len(got) != 1 || got[0] != "ok" {
		t.Errorf("Poll reported %q, want only the user's reply to zen's post", got)
	}
}

func setPush(t *testing.T, events []string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	notify.Configure(`printf '%s|%s|%s|%s\n' "$ZEN_EVENT" "$ZEN_TITLE" "$ZEN_MESSAGE" "$ZEN_URL" >> `+out, events)
	t.Cleanup(func() { notify.Configure("", nil) })
	return out
}

func readOut(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func TestNotifyModes(t *testing.T) {
	ctx := context.Background()
	text := "PR 7 is ready\nmore detail"
	link := "https://x.slack.com/p1"

	t.Run("none", func(t *testing.T) {
		f, client := newFakeSlack(t)
		out := setPush(t, nil)
		res := newPoster(t, client, config.SlackConfig{LeadChannel: leadChan}).Notify(ctx, config.LeadNotifyNone, text, link)
		if res.Via != "" || readOut(out) != "" || len(f.reminders) != 0 {
			t.Errorf("none sent an alert: %+v %q %v", res, readOut(out), f.reminders)
		}
	})

	t.Run("command", func(t *testing.T) {
		_, client := newFakeSlack(t)
		out := setPush(t, []string{notify.EventLeadMessage})
		res := newPoster(t, client, config.SlackConfig{LeadChannel: leadChan}).Notify(ctx, config.LeadNotifyCommand, text, link)
		if res.Via != config.LeadNotifyCommand {
			t.Errorf("Via = %q, warnings %v", res.Via, res.Warnings)
		}
		if want := "lead-message|Lead|PR 7 is ready|" + link + "\n"; readOut(out) != want {
			t.Errorf("command saw %q, want %q", readOut(out), want)
		}
	})

	t.Run("command not configured", func(t *testing.T) {
		_, client := newFakeSlack(t)
		notify.Configure("", nil)
		res := newPoster(t, client, config.SlackConfig{LeadChannel: leadChan}).Notify(ctx, config.LeadNotifyCommand, text, link)
		if res.Via != "" || len(res.Warnings) == 0 {
			t.Errorf("got %+v, want a warning and no alert", res)
		}
	})

	t.Run("reminder", func(t *testing.T) {
		f, client := newFakeSlack(t)
		out := setPush(t, nil)
		before := time.Now()
		res := newPoster(t, client, config.SlackConfig{LeadChannel: leadChan}).Notify(ctx, config.LeadNotifyReminder, text, link)
		if res.Via != config.LeadNotifyReminder || len(f.reminders) != 1 {
			t.Fatalf("got %+v, reminders %v", res, f.reminders)
		}
		r := f.reminders[0]
		if r["text"] != "Lead: PR 7 is ready "+link {
			t.Errorf("reminder text = %q", r["text"])
		}
		due := int64(r["time"].(float64))
		if due < before.Add(50*time.Second).Unix() || due > time.Now().Add(70*time.Second).Unix() {
			t.Errorf("reminder due %d, want about a minute from now", due)
		}
		if readOut(out) != "" {
			t.Error("reminder also ran the command")
		}
	})

	t.Run("reminder missing scope falls back to command", func(t *testing.T) {
		f, client := newFakeSlack(t)
		f.reminderErr = "missing_scope"
		out := setPush(t, nil)
		res := newPoster(t, client, config.SlackConfig{LeadChannel: leadChan}).Notify(ctx, config.LeadNotifyReminder, text, link)
		if res.Via != config.LeadNotifyCommand {
			t.Errorf("Via = %q, want the command fallback", res.Via)
		}
		if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "reminders:write") {
			t.Errorf("warnings = %v, want the missing scope named", res.Warnings)
		}
		if !strings.HasPrefix(readOut(out), "lead-message|Lead|") {
			t.Errorf("command saw %q", readOut(out))
		}
	})

	t.Run("reminder missing scope without command only warns", func(t *testing.T) {
		f, client := newFakeSlack(t)
		f.reminderErr = "missing_scope"
		notify.Configure("", nil)
		res := newPoster(t, client, config.SlackConfig{LeadChannel: leadChan}).Notify(ctx, config.LeadNotifyReminder, text, link)
		if res.Via != "" || len(res.Warnings) < 2 {
			t.Errorf("got %+v, want warnings and no alert", res)
		}
	})
}
