package entity

import (
	"encoding/json"
	"time"
)

// Known settings keys.
const (
	SettingSendingPaused = "sending_paused"
)

// Setting mirrors the settings table — small runtime state such as the
// reputation circuit breaker's pause flag.
type Setting struct {
	Key       string
	Value     json.RawMessage
	UpdatedAt time.Time
}
