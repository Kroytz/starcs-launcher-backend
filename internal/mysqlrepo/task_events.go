package mysqlrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

type taskEventDefinition struct {
	taskID       uint64
	campaignCode string
	timezone     string
	repeatPolicy string
	aggregation  string
	target       uint64
	criteria     []byte
}

func (r *Repository) RecordTaskEvents(ctx context.Context, serverID string, events []domain.TaskProgressEvent) (domain.TaskEventBatchResult, error) {
	result := domain.TaskEventBatchResult{}
	for _, event := range events {
		duplicate, updates, err := r.recordTaskEvent(ctx, serverID, event)
		if err != nil {
			return domain.TaskEventBatchResult{}, wrapTaskSchemaError("record task event", err)
		}
		if duplicate {
			result.Duplicates++
			continue
		}
		result.Accepted++
		result.ProgressUpdates += updates
	}
	return result, nil
}

func (r *Repository) recordTaskEvent(ctx context.Context, serverID string, event domain.TaskProgressEvent) (bool, int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()

	dimensions, err := enrichTaskEventDimensions(event.Dimensions, serverID, event.Source)
	if err != nil {
		return false, 0, err
	}
	insert, err := tx.ExecContext(ctx, `
		INSERT IGNORE INTO launcher_task_event_inbox
			(event_id, steam_id64, metric_key, delta_value, distinct_key, dimensions_json, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, event.EventID, event.SteamID, event.Metric, event.Value, event.DistinctKey, dimensions, event.OccurredAt)
	if err != nil {
		return false, 0, err
	}
	affected, err := insert.RowsAffected()
	if err != nil {
		return false, 0, err
	}
	if affected == 0 {
		if err := tx.Commit(); err != nil {
			return false, 0, err
		}
		return true, 0, nil
	}

	definitions, err := loadMatchingTaskDefinitions(ctx, tx, event)
	if err != nil {
		return false, 0, err
	}
	updates := 0
	for _, definition := range definitions {
		matches, err := taskCriteriaMatches(definition.criteria, dimensions)
		if err != nil {
			return false, 0, fmt.Errorf("match criteria for task %d: %w", definition.taskID, err)
		}
		if !matches {
			continue
		}
		periodKey, err := taskPeriodKey(event.OccurredAt, definition.campaignCode, definition.repeatPolicy, definition.timezone)
		if err != nil {
			return false, 0, err
		}
		updated, err := applyTaskProgressEvent(ctx, tx, event, definition, periodKey)
		if err != nil {
			return false, 0, err
		}
		if updated {
			updates++
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE launcher_task_event_inbox
		SET processed_at = CURRENT_TIMESTAMP(6), processing_error = ''
		WHERE event_id = ?
	`, event.EventID); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return false, updates, nil
}

func loadMatchingTaskDefinitions(ctx context.Context, tx *sql.Tx, event domain.TaskProgressEvent) ([]taskEventDefinition, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT d.id, c.code, c.timezone, g.repeat_policy, d.aggregation, d.target_value, d.criteria_json
		FROM launcher_task_definition AS d
		INNER JOIN launcher_task_group AS g ON g.id = d.group_id
		INNER JOIN launcher_task_campaign AS c ON c.id = g.campaign_id
		WHERE d.progress_source = 'native'
		  AND d.metric_key = ?
		  AND d.enabled = 1
		  AND g.enabled = 1
		  AND c.status = 'published'
		  AND (c.starts_at IS NULL OR c.starts_at <= ?)
		  AND (c.ends_at IS NULL OR c.ends_at > ?)
		  AND d.published_at IS NOT NULL
		  AND d.published_at <= ?
		  AND (d.retired_at IS NULL OR d.retired_at > ?)
		  AND d.revision = (
		      SELECT MAX(d2.revision)
		      FROM launcher_task_definition AS d2
		      WHERE d2.group_id = d.group_id
		        AND d2.code = d.code
		        AND d2.enabled = 1
		        AND d2.published_at IS NOT NULL
		        AND d2.published_at <= ?
		        AND (d2.retired_at IS NULL OR d2.retired_at > ?)
		  )
		ORDER BY d.id
	`, event.Metric, event.OccurredAt, event.OccurredAt, event.OccurredAt, event.OccurredAt, event.OccurredAt, event.OccurredAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	definitions := make([]taskEventDefinition, 0)
	for rows.Next() {
		var definition taskEventDefinition
		if err := rows.Scan(
			&definition.taskID,
			&definition.campaignCode,
			&definition.timezone,
			&definition.repeatPolicy,
			&definition.aggregation,
			&definition.target,
			&definition.criteria,
		); err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, rows.Err()
}

func applyTaskProgressEvent(ctx context.Context, tx *sql.Tx, event domain.TaskProgressEvent, definition taskEventDefinition, periodKey string) (bool, error) {
	var current uint64
	var state string
	err := tx.QueryRowContext(ctx, `
		SELECT progress_value, state
		FROM launcher_player_task_progress
		WHERE steam_id64 = ? AND task_id = ? AND period_key = ?
		FOR UPDATE
	`, event.SteamID, definition.taskID, periodKey).Scan(&current, &state)
	isNewProgress := errors.Is(err, sql.ErrNoRows)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if state == "claimed" || state == "completed" || current >= definition.target {
		return false, nil
	}

	next, err := nextTaskProgress(ctx, tx, event, definition, periodKey, current)
	if err != nil || next == current {
		return false, err
	}
	if next > definition.target {
		next = definition.target
	}
	nextState := "in_progress"
	var completedAt any
	if next >= definition.target {
		nextState = "completed"
		completedAt = event.OccurredAt
	}

	if isNewProgress {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO launcher_player_task_progress
				(steam_id64, task_id, period_key, progress_value, state, version, started_at, completed_at)
			VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		`, event.SteamID, definition.taskID, periodKey, next, nextState, event.OccurredAt, completedAt)
		return err == nil, err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE launcher_player_task_progress
		SET progress_value = ?, state = ?, version = version + 1,
		    completed_at = COALESCE(completed_at, ?), updated_at = CURRENT_TIMESTAMP(6)
		WHERE steam_id64 = ? AND task_id = ? AND period_key = ?
	`, next, nextState, completedAt, event.SteamID, definition.taskID, periodKey)
	return err == nil, err
}

func nextTaskProgress(ctx context.Context, tx *sql.Tx, event domain.TaskProgressEvent, definition taskEventDefinition, periodKey string, current uint64) (uint64, error) {
	switch definition.aggregation {
	case "sum", "count":
		if ^uint64(0)-current < event.Value {
			return definition.target, nil
		}
		return current + event.Value, nil
	case "max":
		if event.Value > current {
			return event.Value, nil
		}
		return current, nil
	case "boolean":
		return 1, nil
	case "distinct":
		if event.DistinctKey == "" {
			return current, fmt.Errorf("task %d requires distinctKey", definition.taskID)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT IGNORE INTO launcher_player_task_distinct_value
				(steam_id64, task_id, period_key, distinct_key, first_seen_at)
			VALUES (?, ?, ?, ?, ?)
		`, event.SteamID, definition.taskID, periodKey, event.DistinctKey, event.OccurredAt)
		if err != nil {
			return current, err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected == 0 {
			return current, err
		}
		return current + 1, nil
	default:
		return current, fmt.Errorf("unsupported aggregation %q", definition.aggregation)
	}
}

func enrichTaskEventDimensions(raw json.RawMessage, serverID, source string) (json.RawMessage, error) {
	dimensions := make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &dimensions); err != nil {
			return nil, err
		}
	}
	dimensions["serverId"] = serverID
	dimensions["source"] = source
	return json.Marshal(dimensions)
}

func taskCriteriaMatches(criteria, dimensions json.RawMessage) (bool, error) {
	if len(criteria) == 0 || string(criteria) == "null" || string(criteria) == "{}" {
		return true, nil
	}
	var expected any
	if err := json.Unmarshal(criteria, &expected); err != nil {
		return false, err
	}
	var actual any
	if err := json.Unmarshal(dimensions, &actual); err != nil {
		return false, err
	}
	return jsonContains(actual, expected), nil
}

func jsonContains(actual, expected any) bool {
	switch wanted := expected.(type) {
	case map[string]any:
		available, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range wanted {
			candidate, exists := available[key]
			if !exists || !jsonContains(candidate, value) {
				return false
			}
		}
		return true
	case []any:
		available, ok := actual.([]any)
		if !ok {
			return false
		}
		for _, value := range wanted {
			matched := false
			for _, candidate := range available {
				if jsonContains(candidate, value) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(actual, expected)
	}
}
