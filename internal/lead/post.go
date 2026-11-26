package lead

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/notify"
	slackpkg "github.com/mgreau/zen/internal/slack"
)

// PostAPI is the part of the Slack client zen slack post uses.
type PostAPI interface {
	Post(ctx context.Context, channel, text, threadTS string) (string, error)
	Permalink(ctx context.Context, channel, ts string) (string, error)
	AddReminder(ctx context.Context, text string, due time.Time) error
}

// Poster sends the lead's messages to the lead channel. The channel is
// fixed when the Poster is made: nothing a caller passes to Post can send
// to another one.
type Poster struct {
	client    PostAPI
	channel   string
	prefix    string
	statePath string
}

// ErrNoLeadChannel is returned when slack.lead_channel is not set.
var ErrNoLeadChannel = errors.New("slack.lead_channel is not set in config.yaml; zen slack post only posts there")

// NewPoster returns a Poster for the lead channel in cfg.
func NewPoster(client PostAPI, cfg config.SlackConfig, statePath string) (*Poster, error) {
	if cfg.LeadChannel == "" {
		return nil, ErrNoLeadChannel
	}
	return &Poster{client: client, channel: cfg.LeadChannel, prefix: cfg.GetLeadPrefix(), statePath: statePath}, nil
}

// Channel returns the lead channel.
func (p *Poster) Channel() string { return p.channel }

// slackTS is what a message timestamp looks like.
var slackTS = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

// Posted is a message zen slack post sent.
type Posted struct {
	Ts        string `json:"ts"`
	Permalink string `json:"permalink"`
}

// Post sends text, prefixed, to the lead channel, as a reply in the thread
// threadTS when that is set, and records its timestamp so the watcher never
// reports it as the user's.
func (p *Poster) Post(ctx context.Context, text, threadTS string) (Posted, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Posted{}, errors.New("nothing to post: the message is empty")
	}
	if threadTS != "" && !slackTS.MatchString(threadTS) {
		return Posted{}, fmt.Errorf("invalid --thread %q: want a message timestamp such as 1700000000.123456", threadTS)
	}
	ts, err := p.client.Post(ctx, p.channel, p.prefix+text, threadTS)
	if err != nil {
		return Posted{}, err
	}
	// Recorded even if the permalink lookup fails: the message is sent.
	rerr := Update(ctx, p.statePath, func(s *State) error {
		if s.Posted == nil {
			s.Posted = make(map[string]int64)
		}
		s.Posted[ts] = time.Now().Unix()
		return nil
	})
	permalink, err := p.client.Permalink(ctx, p.channel, ts)
	if err != nil {
		permalink = ""
	}
	if rerr != nil {
		// The prefix still keeps the watcher from reporting it.
		return Posted{Ts: ts, Permalink: permalink}, fmt.Errorf("posted, but recording it failed: %w", rerr)
	}
	return Posted{Ts: ts, Permalink: permalink}, nil
}

// NotifyResult says how a --notify alert went out, if at all.
type NotifyResult struct {
	Via      string // config.LeadNotifyReminder, config.LeadNotifyCommand, or "" for none
	Warnings []string
}

// reminderDelay is how far ahead a reminder is set. Slack rejects a time
// that has already passed.
const reminderDelay = time.Minute

// Notify alerts the user to a post as mode (slack.lead_notify) says. It never
// fails: a reminder that cannot be set (for example because the token lacks
// reminders:write) falls back to notify.command when that takes
// lead-message, and is otherwise only a warning.
func (p *Poster) Notify(ctx context.Context, mode, text, permalink string) NotifyResult {
	var res NotifyResult
	line := firstLine(text)
	switch mode {
	case config.LeadNotifyReminder:
		msg := strings.TrimSpace("Lead: " + line + " " + permalink)
		err := p.client.AddReminder(ctx, msg, time.Now().Add(reminderDelay))
		if err == nil {
			res.Via = config.LeadNotifyReminder
			return res
		}
		var apiErr *slackpkg.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "missing_scope" {
			res.Warnings = append(res.Warnings, "the Slack sign-in lacks the reminders:write scope; run zen slack login again to add it")
		} else {
			res.Warnings = append(res.Warnings, fmt.Sprintf("setting a Slack reminder failed: %v", err))
		}
		if !notify.PushWants(notify.EventLeadMessage) {
			res.Warnings = append(res.Warnings, "no notify.command takes lead-message to fall back to; no alert sent")
			return res
		}
		fallthrough
	case config.LeadNotifyCommand:
		ran, err := notify.LeadMessage(text, permalink)
		switch {
		case err != nil:
			res.Warnings = append(res.Warnings, err.Error())
		case !ran:
			res.Warnings = append(res.Warnings, "no notify.command takes lead-message; no alert sent")
		default:
			res.Via = config.LeadNotifyCommand
		}
	}
	return res
}

func firstLine(text string) string {
	line := strings.TrimSpace(text)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	line, _ = notify.TruncateUTF8(line, 200)
	return line
}
