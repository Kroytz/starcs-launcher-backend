package mysqlrepo

import (
	"strings"
	"testing"
	"time"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

func TestTaskPeriodKey(t *testing.T) {
	now := time.Date(2026, time.September, 3, 1, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		policy   string
		timezone string
		campaign string
		expected string
	}{
		{name: "once", policy: "once", timezone: "Asia/Shanghai", campaign: "new-player", expected: "once"},
		{name: "daily uses campaign timezone", policy: "daily", timezone: "Asia/Shanghai", campaign: "daily", expected: "2026-09-03"},
		{name: "weekly uses ISO week", policy: "weekly", timezone: "Asia/Shanghai", campaign: "weekly", expected: "2026-W36"},
		{name: "campaign", policy: "campaign", timezone: "Asia/Shanghai", campaign: "autumn-2026", expected: "campaign:autumn-2026"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := taskPeriodKey(now, test.campaign, test.policy, test.timezone)
			if err != nil {
				t.Fatal(err)
			}
			if actual != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, actual)
			}
		})
	}
	if _, err := taskPeriodKey(now, "bad", "hourly", "Asia/Shanghai"); err == nil {
		t.Fatal("expected unsupported policy error")
	}
	if _, err := taskPeriodKey(now, "bad", "daily", "Mars/Olympus"); err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestLegacyTaskProgress(t *testing.T) {
	pass := domain.SeasonPassOverview{
		DailyGames:       3,
		DailyQuestStatus: map[string]int{"3": 2},
	}
	current, status := legacyTaskProgress(pass, "daily", "3", "daily_games", 3, 2)
	if current != 3 || status != "claimed" {
		t.Fatalf("expected claimed 3/3, got status=%q progress=%d", status, current)
	}
	current, status = legacyTaskProgress(pass, "daily", "4", "daily_games", 3, 2)
	if current != 3 || status != "completed" {
		t.Fatalf("expected completed 3/3, got status=%q progress=%d", status, current)
	}
	current, status = legacyTaskProgress(pass, "daily", "4", "daily_games", 5, 2)
	if current != 3 || status != "in_progress" {
		t.Fatalf("expected in_progress 3/5, got status=%q progress=%d", status, current)
	}
}

func TestFinalizeSequentialTaskGroup(t *testing.T) {
	group := domain.TaskGroup{
		UnlockPolicy:   "sequential",
		CompletionRule: "all",
		Tasks: []domain.TaskItem{
			{ID: "done", Status: "claimed", ClaimPolicy: "manual"},
			{ID: "current", Status: "in_progress", ClaimPolicy: "manual"},
			{ID: "later", Status: "in_progress", ClaimPolicy: "manual"},
		},
	}
	finalizeTaskGroup(&group, taskGroupStateRecord{})
	if group.CurrentTaskID != "current" {
		t.Fatalf("unexpected current task %q", group.CurrentTaskID)
	}
	if group.Tasks[1].Locked || !group.Tasks[2].Locked {
		t.Fatal("sequential lock state is incorrect")
	}
	if group.CompletedCount != 1 || group.State != "in_progress" {
		t.Fatalf("unexpected group summary: %+v", group)
	}
}

func TestTaskPeriodQueryUsesPlaceholders(t *testing.T) {
	query, args := taskPeriodQuery("launcher_player_task_progress", "task_id", 76561198000000001,
		map[uint64]string{1: "once", 2: "2026-09-03", 3: "2026-09-03"},
		"task_id, period_key")
	if strings.Contains(query, "76561198000000001") || strings.Contains(query, "2026-09-03") {
		t.Fatalf("query interpolated values instead of placeholders: %s", query)
	}
	if len(args) != 6 {
		t.Fatalf("expected 6 arguments, got %d", len(args))
	}
}
