package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// captureDesktop replaces the desktop notifier and returns what it was asked
// to show.
func captureDesktop(t *testing.T) *[]note {
	t.Helper()
	var shown []note
	prev := showDesktop
	showDesktop = func(n note) error {
		shown = append(shown, n)
		return nil
	}
	t.Cleanup(func() { showDesktop = prev })
	return &shown
}

func TestDesktopOff(t *testing.T) {
	shown := captureDesktop(t)
	out := filepath.Join(t.TempDir(), "out")
	Configure(`echo "$ZEN_EVENT" >> `+out, nil)
	t.Cleanup(func() { Configure("", nil); ConfigureDesktop(true) })

	ConfigureDesktop(false)
	if DesktopEnabled() {
		t.Fatal("DesktopEnabled() after ConfigureDesktop(false)")
	}
	if err := SessionWaiting("mono-fix", "opus", "zen work resume mono-fix"); err != nil {
		t.Fatal(err)
	}
	if len(*shown) != 0 {
		t.Errorf("desktop off still showed %+v", *shown)
	}
	if got, _ := os.ReadFile(out); string(got) != EventWaiting+"\n" {
		t.Errorf("push command saw %q, want it to run with the desktop off", got)
	}

	ConfigureDesktop(true)
	if err := PRReview(7, "add feature", "alice", "app"); err != nil {
		t.Fatal(err)
	}
	if len(*shown) != 1 || (*shown)[0].event != EventPRReview {
		t.Errorf("desktop on showed %+v, want the review request", *shown)
	}
}

func TestDesktopOffNoCommandIsQuiet(t *testing.T) {
	shown := captureDesktop(t)
	Configure("", nil)
	ConfigureDesktop(false)
	t.Cleanup(func() { ConfigureDesktop(true) })
	if err := Digest(1, 2, 0); err != nil {
		t.Errorf("Digest with everything off = %v, want nil", err)
	}
	if len(*shown) != 0 {
		t.Errorf("showed %+v", *shown)
	}
}

func TestSlackTaskPush(t *testing.T) {
	captureDesktop(t)
	dir := t.TempDir()
	Configure(`printf '%s|%s|%s|%s' "$ZEN_EVENT" "$ZEN_TITLE" "$ZEN_MESSAGE" "$ZEN_SUBTITLE" > `+filepath.Join(dir, "head")+
		`; printf '%s' "$ZEN_URL" > `+filepath.Join(dir, "url")+
		`; printf '%s' "$ZEN_TEXT" > `+filepath.Join(dir, "text")+
		`; printf '%s' "$ZEN_EVENT_JSON" > `+filepath.Join(dir, "json"), []string{EventSlackTask})
	t.Cleanup(func() { Configure("", nil) })

	text := "U2: the nightly build is red $(touch pwned)\n\nU3: " + strings.Repeat("é", MaxSlackTaskText)
	event := []byte(`{"type":"slack-task","url":"https://x.slack.com/p1"}`)
	if err := SlackTask("U2", "https://x.slack.com/p1", text, event); err != nil {
		t.Fatal(err)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got, want := read("head"), "slack-task|Slack task|U2: the nightly build is red $(touch pwned)|from U2"; got != want {
		t.Errorf("push saw %q, want %q", got, want)
	}
	if got := read("url"); got != "https://x.slack.com/p1" {
		t.Errorf("ZEN_URL = %q", got)
	}
	if got := read("text"); len(got) > MaxSlackTaskText || !utf8.ValidString(got) || !strings.HasPrefix(got, "U2: the nightly") {
		t.Errorf("ZEN_TEXT is %d bytes (valid UTF-8: %v), want a prefix of at most %d", len(got), utf8.ValidString(got), MaxSlackTaskText)
	}
	if got := read("json"); got != string(event) {
		t.Errorf("ZEN_EVENT_JSON = %q", got)
	}
	if _, err := os.Stat("pwned"); err == nil {
		os.Remove("pwned")
		t.Error("thread text was run as shell")
	}
}

func TestTruncateUTF8(t *testing.T) {
	if got, cut := TruncateUTF8("héllo", 2); got != "h" || !cut {
		t.Errorf("TruncateUTF8 = %q, %v", got, cut)
	}
	if got, cut := TruncateUTF8("abc", 5); got != "abc" || cut {
		t.Errorf("TruncateUTF8 = %q, %v", got, cut)
	}
}
