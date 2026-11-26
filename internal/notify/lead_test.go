package notify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlackMessageAndLeadMessagePushOnly(t *testing.T) {
	shown := 0
	oldShow := showDesktop
	showDesktop = func(note) error { shown++; return nil }
	t.Cleanup(func() { showDesktop = oldShow })

	out := filepath.Join(t.TempDir(), "out")
	Configure(`printf '%s|%s|%s|%s|%s\n' "$ZEN_EVENT" "$ZEN_TITLE" "$ZEN_MESSAGE" "$ZEN_URL" "$ZEN_EVENT_JSON" >> `+out, []string{EventSlackMessage, EventLeadMessage})
	t.Cleanup(func() { Configure("", nil) })

	if ran, err := SlackMessage("https://x/p1", "do the thing\nplease", []byte(`{"type":"slack-message"}`)); !ran || err != nil {
		t.Fatalf("SlackMessage = %v, %v", ran, err)
	}
	if ran, err := LeadMessage("\n  PR 7 merged\nmore", "https://x/p2"); !ran || err != nil {
		t.Fatalf("LeadMessage = %v, %v", ran, err)
	}
	got, _ := os.ReadFile(out)
	want := "slack-message|Slack message|do the thing|https://x/p1|{\"type\":\"slack-message\"}\n" +
		"lead-message|Lead|PR 7 merged|https://x/p2|\n"
	if string(got) != want {
		t.Errorf("command saw %q, want %q", got, want)
	}
	if shown != 0 {
		t.Errorf("shown on the desktop %d times, want push only", shown)
	}

	Configure(`true`, []string{EventDigest})
	if PushWants(EventLeadMessage) {
		t.Error("PushWants(lead-message) with events [digest] = true")
	}
	if ran, _ := LeadMessage("x", ""); ran {
		t.Error("LeadMessage ran a command that leaves out lead-message")
	}
}
