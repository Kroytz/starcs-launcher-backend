package domain

import "encoding/json"

// TaskCenterOverview is the complete read model consumed by the launcher's
// task page. Database IDs are kept out of JSON; public IDs remain opaque.
type TaskCenterOverview struct {
	Available   bool                `json:"available"`
	GeneratedAt string              `json:"generatedAt"`
	Campaigns   []TaskCampaign      `json:"campaigns"`
	SeasonPass  *SeasonPassOverview `json:"seasonPass,omitempty"`
}

type TaskCampaign struct {
	DatabaseID  uint64          `json:"-"`
	ID          string          `json:"id"`
	Code        string          `json:"code"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	StartsAt    string          `json:"startsAt,omitempty"`
	EndsAt      string          `json:"endsAt,omitempty"`
	Timezone    string          `json:"timezone"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Groups      []TaskGroup     `json:"groups"`
}

type TaskGroup struct {
	DatabaseID        uint64          `json:"-"`
	ID                string          `json:"id"`
	Code              string          `json:"code"`
	Category          string          `json:"category"`
	Title             string          `json:"title"`
	Description       string          `json:"description"`
	RepeatPolicy      string          `json:"repeatPolicy"`
	CompletionRule    string          `json:"completionRule"`
	RequiredTaskCount *int            `json:"requiredTaskCount,omitempty"`
	UnlockPolicy      string          `json:"unlockPolicy"`
	PeriodKey         string          `json:"periodKey"`
	State             string          `json:"state"`
	CompletedCount    int             `json:"completedCount"`
	ClaimableCount    int             `json:"claimableCount"`
	CurrentTaskID     string          `json:"currentTaskId,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	Tasks             []TaskItem      `json:"tasks"`
	Rewards           []TaskReward    `json:"rewards"`
}

type TaskItem struct {
	DatabaseID  uint64       `json:"-"`
	ID          string       `json:"id"`
	Code        string       `json:"code"`
	Revision    uint         `json:"revision"`
	SeriesCode  string       `json:"seriesCode,omitempty"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Source      string       `json:"source"`
	Current     uint64       `json:"current"`
	Target      uint64       `json:"target"`
	Unit        string       `json:"unit"`
	Status      string       `json:"status"`
	ClaimPolicy string       `json:"claimPolicy"`
	Locked      bool         `json:"locked"`
	UpdatedAt   string       `json:"updatedAt,omitempty"`
	Rewards     []TaskReward `json:"rewards"`
}

type TaskReward struct {
	Type     string          `json:"type"`
	Ref      string          `json:"ref,omitempty"`
	Amount   uint64          `json:"amount"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}
