package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
	"github.com/mgreau/zen/internal/lead"
	"github.com/mgreau/zen/internal/notify"
	"github.com/mgreau/zen/internal/reconciler"
	slackpkg "github.com/mgreau/zen/internal/slack"
	"github.com/spf13/cobra"
)

var (
	slackPostThread string
	slackPostNotify bool
)

var slackPostCmd = &cobra.Command{
	Use:   "post [TEXT | -]",
	Short: "Post to the lead channel (slack.lead_channel), and nowhere else",
	Long: `Posts TEXT, or standard input when TEXT is "-" or missing, to the Slack
channel set as slack.lead_channel. It cannot post anywhere else: there is no
flag or argument that names a channel, so a lead agent session can be
allowed to run it without being allowed to message anyone.

The message starts with slack.lead_prefix (":robot_face: " by default), and
its timestamp is recorded, so the daemon's lead channel watcher does not
report zen's own posts as your messages (zen posts as you).

Prints {"ts": ..., "permalink": ...} as JSON.

--thread TS replies in the thread whose parent message has timestamp TS.
--notify also alerts you as slack.lead_notify says: "command" runs
notify.command as a lead-message notification; "reminder" sets a Slack
reminder due in a minute, which Slackbot delivers as a DM that Slack
notifies you about (needs the reminders:write scope: run zen slack login
again if you signed in before zen asked for it). An alert that fails never
fails the post.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSlackPost,
}

func init() {
	slackPostCmd.Flags().StringVar(&slackPostThread, "thread", "", "Reply in the thread whose parent message has this timestamp")
	slackPostCmd.Flags().BoolVar(&slackPostNotify, "notify", false, "Also alert you as slack.lead_notify says (none, command or reminder)")
	slackCmd.AddCommand(slackPostCmd)
}

// slackAPIBaseURL overrides the Slack Web API root, for tests.
var slackAPIBaseURL string

// maxPostInput caps what zen slack post reads from standard input. Slack
// itself truncates messages past 40,000 characters.
const maxPostInput = 1 << 20

func runSlackPost(cmd *cobra.Command, args []string) error {
	if cfg.Slack.LeadChannel == "" {
		return lead.ErrNoLeadChannel
	}
	text, err := postText(args, cmd.InOrStdin())
	if err != nil {
		return err
	}
	src, _, _, err := slackTokenSource()
	if err != nil {
		if errors.Is(err, slackpkg.ErrNotLoggedIn) {
			return fmt.Errorf("%w (or set ZEN_SLACK_TOKEN)", err)
		}
		return err
	}
	client := slackpkg.NewClientWithSource(src)
	if slackAPIBaseURL != "" {
		client.WithBaseURL(slackAPIBaseURL)
	}
	if err := config.EnsureDirs(); err != nil {
		return err
	}
	poster, err := lead.NewPoster(client, cfg.Slack, lead.StatePath(config.StateDir()))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	posted, err := poster.Post(ctx, text, slackPostThread)
	if err != nil && posted.Ts == "" {
		return fmt.Errorf("posting to the lead channel: %w", err)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	reconciler.Emit(cfg, events.Event{
		Type: events.TypeLeadMessage,
		URL:  posted.Permalink,
		Data: map[string]any{
			"channel":   poster.Channel(),
			"ts":        posted.Ts,
			"thread_ts": slackPostThread,
			"text":      truncateText(cfg.Slack.GetLeadPrefix()+text, lead.MaxText),
		},
	})

	if slackPostNotify {
		notify.Configure(cfg.Notify.Command, cfg.Notify.Events)
		res := poster.Notify(ctx, cfg.Slack.GetLeadNotify(), text, posted.Permalink)
		for _, w := range res.Warnings {
			fmt.Fprintf(os.Stderr, "Warning: --notify: %s\n", w)
		}
	}

	printJSON(posted)
	return nil
}

func truncateText(s string, n int) string {
	s, _ = notify.TruncateUTF8(s, n)
	return s
}

// postText returns the message: the argument, or standard input when it is
// "-" or missing.
func postText(args []string, stdin io.Reader) (string, error) {
	if len(args) == 1 && args[0] != "-" {
		return args[0], nil
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maxPostInput+1))
	if err != nil {
		return "", fmt.Errorf("reading the message from standard input: %w", err)
	}
	if len(data) > maxPostInput {
		return "", fmt.Errorf("message on standard input is over %d bytes", maxPostInput)
	}
	return strings.TrimSpace(string(data)), nil
}
