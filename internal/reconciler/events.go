package reconciler

import (
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/events"
)

// Emit sends a daemon event where cfg routes it (see config.EventOptions).
// With no events or notify settings it does nothing.
func Emit(cfg *config.Config, e events.Event) {
	if cfg == nil {
		return
	}
	events.Emit(cfg.EventOptions(), e)
}
