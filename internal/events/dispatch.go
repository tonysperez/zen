package events

import (
	"fmt"
	"time"
)

// Options says where Emit sends an event.
type Options struct {
	// Log appends the event to the log in StateDir (events.enabled).
	Log      bool
	StateDir string
}

// Emit records e as opts says. It cannot fail the caller; errors are logged.
func Emit(opts Options, e Event) {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if opts.Log {
		if err := Append(Path(opts.StateDir), e); err != nil {
			logf("Event log: failed to append %s: %v", e.Type, err)
		}
	}
}

func logf(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
