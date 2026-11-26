package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/lead"
	"github.com/spf13/pflag"
)

// TestSlackPostHasNoChannelFlag pins the command's surface: nothing a caller
// passes may choose where the message goes.
func TestSlackPostHasNoChannelFlag(t *testing.T) {
	var names []string
	slackPostCmd.LocalFlags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	if strings.Join(names, ",") != "notify,thread" {
		t.Errorf("zen slack post flags = %v, want only notify and thread", names)
	}
	if err := slackPostCmd.Args(slackPostCmd, []string{"C0OTHER001", "text"}); err == nil {
		t.Error("zen slack post accepted a second argument")
	}
}

type postServer struct {
	mu     sync.Mutex
	bodies []map[string]any
	paths  []string
}

func newPostServer(t *testing.T) *postServer {
	t.Helper()
	ps := &postServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		defer ps.mu.Unlock()
		ps.paths = append(ps.paths, r.URL.Path+"?"+r.URL.RawQuery)
		var body map[string]any
		if r.Method == http.MethodPost {
			data, _ := io.ReadAll(r.Body)
			json.Unmarshal(data, &body)
			ps.bodies = append(ps.bodies, body)
		}
		switch r.URL.Path {
		case "/chat.postMessage":
			w.Write([]byte(`{"ok":true,"ts":"1800000000.000100"}`))
		case "/chat.getPermalink":
			w.Write([]byte(`{"ok":true,"permalink":"https://x.slack.com/archives/` + r.URL.Query().Get("channel") + `/p1"}`))
		default:
			w.Write([]byte(`{"ok":false,"error":"unknown_method"}`))
		}
	}))
	t.Cleanup(srv.Close)
	old := slackAPIBaseURL
	slackAPIBaseURL = srv.URL + "/"
	t.Cleanup(func() { slackAPIBaseURL = old })
	return ps
}

func withSlackCfg(t *testing.T, sc config.SlackConfig) {
	t.Helper()
	t.Setenv("ZEN_HOME", t.TempDir())
	t.Setenv("ZEN_SLACK_TOKEN", "xoxp-test")
	old := cfg
	cfg = &config.Config{Slack: sc}
	t.Cleanup(func() { cfg = old })
	slackPostCmd.SetContext(context.Background())
	oldThread, oldNotify := slackPostThread, slackPostNotify
	t.Cleanup(func() { slackPostThread, slackPostNotify = oldThread, oldNotify })
}

func TestRunSlackPost(t *testing.T) {
	ps := newPostServer(t)
	withSlackCfg(t, config.SlackConfig{LeadChannel: "C0LEAD0001"})
	slackPostThread = "1700000000.000200"

	if err := runSlackPost(slackPostCmd, []string{"ship it?"}); err != nil {
		t.Fatal(err)
	}
	if len(ps.bodies) != 1 {
		t.Fatalf("requests = %v", ps.paths)
	}
	b := ps.bodies[0]
	if b["channel"] != "C0LEAD0001" || b["text"] != ":robot_face: ship it?" || b["thread_ts"] != "1700000000.000200" {
		t.Errorf("chat.postMessage body = %v", b)
	}
	for _, p := range ps.paths {
		if strings.Contains(p, "channel=") && !strings.Contains(p, "channel=C0LEAD0001") {
			t.Errorf("request outside the lead channel: %s", p)
		}
	}
	st, err := lead.Load(lead.StatePath(config.StateDir()))
	if err != nil || st.Posted["1800000000.000100"] == 0 {
		t.Errorf("post not recorded for the watcher: %+v, %v", st, err)
	}
}

func TestRunSlackPostErrors(t *testing.T) {
	newPostServer(t)

	withSlackCfg(t, config.SlackConfig{})
	if err := runSlackPost(slackPostCmd, []string{"x"}); !errors.Is(err, lead.ErrNoLeadChannel) {
		t.Errorf("no lead_channel: err = %v", err)
	}

	withSlackCfg(t, config.SlackConfig{LeadChannel: "C0LEAD0001"})
	t.Setenv("ZEN_SLACK_TOKEN", "")
	err := runSlackPost(slackPostCmd, []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "zen slack login") {
		t.Errorf("not signed in: err = %v, want a pointer to zen slack login", err)
	}
}

func TestPostText(t *testing.T) {
	if got, _ := postText([]string{"hello"}, strings.NewReader("ignored")); got != "hello" {
		t.Errorf("arg: %q", got)
	}
	if got, _ := postText([]string{"-"}, strings.NewReader("  from stdin\n")); got != "from stdin" {
		t.Errorf("dash: %q", got)
	}
	if got, _ := postText(nil, strings.NewReader("piped")); got != "piped" {
		t.Errorf("no arg: %q", got)
	}
	if _, err := postText(nil, strings.NewReader(strings.Repeat("x", maxPostInput+1))); err == nil {
		t.Error("oversized stdin accepted")
	}
}
