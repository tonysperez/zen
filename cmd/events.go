package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
	"github.com/spf13/cobra"
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Print the daemon's event log",
	Long: `Print the watch daemon's event log (events.jsonl in the zen state
directory), one JSON object per line, oldest first. The daemon writes it when
events.enabled is true in config.yaml.

Event types: ` + fmt.Sprint(events.Types()) + `

With --json, prints one JSON array instead of lines (except with --follow,
which always streams lines).`,
	Example: `  zen events --since 2h
  zen events --type review_requested,session_waiting
  zen events --since 2026-01-02T15:04:05Z --follow`,
	Args: cobra.NoArgs,
	RunE: runEvents,
}

var (
	eventsSince  string
	eventsTypes  string
	eventsFollow bool
)

func init() {
	eventsCmd.Flags().StringVar(&eventsSince, "since", "", "Only events at or after this RFC3339 time, or this long ago (e.g. 2h)")
	eventsCmd.Flags().StringVar(&eventsTypes, "type", "", "Only these comma-separated event types")
	eventsCmd.Flags().BoolVarP(&eventsFollow, "follow", "f", false, "Keep printing new events as they are written")
	rootCmd.AddCommand(eventsCmd)
}

func runEvents(cmd *cobra.Command, args []string) error {
	since, err := events.ParseSince(eventsSince, time.Now())
	if err != nil {
		return err
	}
	types, err := events.ParseTypes(eventsTypes)
	if err != nil {
		return err
	}
	filter := events.Filter{Since: since, Types: types}
	path := config.EventsPath()

	if !cfg.Events.Enabled {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "The event log is off: set events.enabled: true in config.yaml and restart the daemon.")
		}
	}

	lines, offset, err := events.Read(path, filter)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	if jsonFlag && !eventsFollow {
		out := make([]json.RawMessage, 0, len(lines))
		for _, l := range lines {
			out = append(out, json.RawMessage(l))
		}
		printJSON(out)
		return nil
	}

	w := cmd.OutOrStdout()
	for _, l := range lines {
		fmt.Fprintf(w, "%s\n", l)
	}
	if !eventsFollow {
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return events.Follow(ctx, path, offset, 500*time.Millisecond, func(line []byte) {
		if filter.Match(line) {
			fmt.Fprintf(w, "%s\n", line)
		}
	})
}
