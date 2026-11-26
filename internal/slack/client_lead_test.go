package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestConversationsHistory_Paginates(t *testing.T) {
	page := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		if r.URL.Path != "/conversations.history" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("channel") != "C1" || q.Get("oldest") != "100.000000" {
			t.Errorf("query = %v", q)
		}
		page++
		if page == 1 {
			w.Write([]byte(`{"ok":true,"messages":[{"user":"U1","ts":"300.1","text":"b","thread_ts":"300.1","reply_count":2,"latest_reply":"301.5"}],"response_metadata":{"next_cursor":"n"}}`))
			return
		}
		if q.Get("cursor") != "n" {
			t.Errorf("cursor = %q", q.Get("cursor"))
		}
		w.Write([]byte(`{"ok":true,"messages":[{"user":"U1","ts":"200.1","text":"a","subtype":"channel_join"}]}`))
	})
	msgs, err := client.ConversationsHistory(context.Background(), "C1", "100.000000", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].ReplyCount != 2 || msgs[0].LatestReply != "301.5" || msgs[0].ThreadTS != "300.1" || msgs[1].Subtype != "channel_join" {
		t.Errorf("messages = %+v", msgs)
	}
	if page != 2 {
		t.Errorf("made %d requests, want 2", page)
	}
}

func TestThreadRepliesSince_DropsParent(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("oldest") != "300.2" {
			t.Errorf("oldest = %q", r.URL.Query().Get("oldest"))
		}
		w.Write([]byte(`{"ok":true,"messages":[{"ts":"300.1","text":"parent"},{"ts":"300.2","text":"old"},{"ts":"300.3","text":"new","thread_ts":"300.1"}]}`))
	})
	msgs, err := client.ThreadRepliesSince(context.Background(), "C1", "300.1", "300.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "new" {
		t.Errorf("replies = %+v, want only the reply after oldest", msgs)
	}
}

func TestPost_ThreadAndTS(t *testing.T) {
	var got map[string]string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = nil
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"ok":true,"ts":"555.000100"}`))
	})
	ts, err := client.Post(context.Background(), "C1", "hi", "")
	if err != nil || ts != "555.000100" {
		t.Fatalf("Post = %q, %v", ts, err)
	}
	if _, ok := got["thread_ts"]; ok || got["channel"] != "C1" || got["text"] != "hi" {
		t.Errorf("body = %v", got)
	}
	if _, err := client.Post(context.Background(), "C1", "re", "444.1"); err != nil {
		t.Fatal(err)
	}
	if got["thread_ts"] != "444.1" {
		t.Errorf("thread body = %v", got)
	}
}

func TestAddReminder(t *testing.T) {
	var got map[string]any
	scope := ""
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reminders.add" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		if scope != "" {
			w.Write([]byte(`{"ok":false,"error":"` + scope + `"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	due := time.Unix(1_700_000_060, 0)
	if err := client.AddReminder(context.Background(), "Lead: x", due); err != nil {
		t.Fatal(err)
	}
	if got["text"] != "Lead: x" || got["time"] != float64(1_700_000_060) {
		t.Errorf("body = %v", got)
	}
	scope = "missing_scope"
	err := client.AddReminder(context.Background(), "Lead: x", due)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "missing_scope" {
		t.Errorf("err = %v, want missing_scope", err)
	}
}

func TestCompareTS(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"1.1", "1.1", 0},
		{"1.100000", "1.1", 0},
		{"9.9", "10.0", -1},
		{"1700000000.000002", "1700000000.000010", -1},
		{"1700000001.0", "1700000000.999999", 1},
	} {
		if got := CompareTS(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareTS(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
	tm := time.Unix(1_700_000_000, 123_456_000)
	if s := FormatTS(tm); s != "1700000000.123456" {
		t.Errorf("FormatTS = %q", s)
	}
}
