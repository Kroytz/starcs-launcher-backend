package domain

import (
	"encoding/json"
	"time"
)

// TaskProgressEvent is a game fact reported by a trusted game server.
// The backend, not the game plugin, decides which task definitions it advances.
type TaskProgressEvent struct {
	EventID     string
	Source      string
	SteamID     uint64
	Metric      string
	Value       uint64
	DistinctKey string
	Dimensions  json.RawMessage
	OccurredAt  time.Time
}

type TaskEventBatchResult struct {
	Accepted        int `json:"accepted"`
	Duplicates      int `json:"duplicates"`
	ProgressUpdates int `json:"progressUpdates"`
}
