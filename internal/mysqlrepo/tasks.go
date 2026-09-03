package mysqlrepo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"github.com/starcs/star-launcher-backend/internal/domain"
)

var ErrTaskSchemaUnavailable = errors.New("task schema unavailable")

const maxTaskCatalogRows = 2000

type taskCatalogRow struct {
	campaignID          uint64
	campaignCode        string
	campaignKind        string
	campaignTitle       string
	campaignDescription string
	campaignStartsAt    sql.NullTime
	campaignEndsAt      sql.NullTime
	campaignTimezone    string
	campaignMetadata    []byte
	groupID             uint64
	groupCode           string
	groupCategory       string
	groupTitle          string
	groupDescription    string
	groupRepeatPolicy   string
	groupCompletionRule string
	groupRequiredCount  sql.NullInt64
	groupUnlockPolicy   string
	groupMetadata       []byte
	taskID              uint64
	taskCode            string
	taskRevision        uint
	taskSeriesCode      string
	taskTitle           string
	taskDescription     string
	taskSource          string
	taskMetricKey       string
	taskAggregation     string
	taskTarget          uint64
	taskUnit            string
	taskClaimPolicy     string
	legacyProvider      sql.NullString
	legacySeasonID      sql.NullInt64
	legacyScope         sql.NullString
	legacyQuestKey      sql.NullString
	legacyMetric        sql.NullString
	legacyClaimedMin    sql.NullInt64
}

type taskProgressRecord struct {
	value     uint64
	state     string
	updatedAt time.Time
}

type taskGroupStateRecord struct {
	state string
}

type taskCampaignBuild struct {
	campaign   domain.TaskCampaign
	groupIndex map[uint64]int
}

func (r *Repository) Tasks(ctx context.Context, steamID uint64) (domain.TaskCenterOverview, error) {
	now := time.Now()
	result := domain.TaskCenterOverview{
		Available:   true,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Campaigns:   []domain.TaskCampaign{},
	}

	catalog, err := r.loadTaskCatalog(ctx)
	if err != nil {
		return domain.TaskCenterOverview{}, err
	}
	if len(catalog) == 0 {
		return result, nil
	}

	hasLegacy := false
	for _, row := range catalog {
		if row.taskSource == "legacy" {
			hasLegacy = true
			break
		}
	}

	var seasonPass domain.SeasonPassOverview
	if hasLegacy {
		seasonPass, err = r.SeasonPass(ctx, steamID)
		if err != nil {
			return domain.TaskCenterOverview{}, fmt.Errorf("load legacy season pass for tasks: %w", err)
		}
		if seasonPass.Available {
			copy := seasonPass
			result.SeasonPass = &copy
		}
	}

	filtered := make([]taskCatalogRow, 0, len(catalog))
	taskPeriods := make(map[uint64]string)
	groupPeriods := make(map[uint64]string)
	for _, row := range catalog {
		if row.taskSource == "legacy" {
			if !seasonPass.Available || !row.legacyProvider.Valid || row.legacyProvider.String != "season_pass" {
				continue
			}
			if row.legacySeasonID.Valid && row.legacySeasonID.Int64 > 0 && int(row.legacySeasonID.Int64) != seasonPass.SeasonID {
				continue
			}
		}
		periodKey, err := taskPeriodKey(now, row.campaignCode, row.groupRepeatPolicy, row.campaignTimezone)
		if err != nil {
			return domain.TaskCenterOverview{}, fmt.Errorf("resolve task period for %s/%s: %w", row.campaignCode, row.groupCode, err)
		}
		filtered = append(filtered, row)
		groupPeriods[row.groupID] = periodKey
		if row.taskSource == "native" {
			taskPeriods[row.taskID] = periodKey
		}
	}

	progress, err := r.loadTaskProgress(ctx, steamID, taskPeriods)
	if err != nil {
		return domain.TaskCenterOverview{}, err
	}
	groupStates, err := r.loadTaskGroupStates(ctx, steamID, groupPeriods)
	if err != nil {
		return domain.TaskCenterOverview{}, err
	}
	taskRewards, groupRewards, err := r.loadTaskRewards(ctx, filtered)
	if err != nil {
		return domain.TaskCenterOverview{}, err
	}

	builders := make([]taskCampaignBuild, 0)
	campaignIndex := make(map[uint64]int)
	for _, row := range filtered {
		campaignPosition, exists := campaignIndex[row.campaignID]
		if !exists {
			campaignPosition = len(builders)
			campaignIndex[row.campaignID] = campaignPosition
			builders = append(builders, taskCampaignBuild{
				campaign: domain.TaskCampaign{
					DatabaseID:  row.campaignID,
					ID:          "campaign:" + row.campaignCode,
					Code:        row.campaignCode,
					Kind:        row.campaignKind,
					Title:       row.campaignTitle,
					Description: row.campaignDescription,
					StartsAt:    formatTaskTime(row.campaignStartsAt),
					EndsAt:      formatTaskTime(row.campaignEndsAt),
					Timezone:    row.campaignTimezone,
					Metadata:    taskRawJSON(row.campaignMetadata),
					Groups:      []domain.TaskGroup{},
				},
				groupIndex: make(map[uint64]int),
			})
		}

		builder := &builders[campaignPosition]
		groupPosition, exists := builder.groupIndex[row.groupID]
		if !exists {
			groupPosition = len(builder.campaign.Groups)
			builder.groupIndex[row.groupID] = groupPosition
			var requiredCount *int
			if row.groupRequiredCount.Valid {
				value := int(row.groupRequiredCount.Int64)
				requiredCount = &value
			}
			builder.campaign.Groups = append(builder.campaign.Groups, domain.TaskGroup{
				DatabaseID:        row.groupID,
				ID:                fmt.Sprintf("group:%s:%s", row.campaignCode, row.groupCode),
				Code:              row.groupCode,
				Category:          row.groupCategory,
				Title:             row.groupTitle,
				Description:       row.groupDescription,
				RepeatPolicy:      row.groupRepeatPolicy,
				CompletionRule:    row.groupCompletionRule,
				RequiredTaskCount: requiredCount,
				UnlockPolicy:      row.groupUnlockPolicy,
				PeriodKey:         groupPeriods[row.groupID],
				Metadata:          taskRawJSON(row.groupMetadata),
				State:             "in_progress",
				Tasks:             []domain.TaskItem{},
				Rewards:           rewardList(groupRewards[row.groupID]),
			})
		}

		group := &builder.campaign.Groups[groupPosition]
		current := uint64(0)
		status := "in_progress"
		updatedAt := ""
		taskID := fmt.Sprintf("task:%d:%s", row.taskID, group.PeriodKey)
		if row.taskSource == "legacy" {
			current, status = legacyTaskProgress(seasonPass, row.legacyScope.String, row.legacyQuestKey.String, row.legacyMetric.String, row.taskTarget, uint64(row.legacyClaimedMin.Int64))
			taskID = fmt.Sprintf("legacy:season_pass:%d:%s:%s", seasonPass.SeasonID, row.legacyScope.String, row.legacyQuestKey.String)
			updatedAt = seasonPass.UpdatedAt
		} else if saved, ok := progress[taskProgressKey(row.taskID, group.PeriodKey)]; ok {
			current = saved.value
			status = normalizeTaskStatus(saved.state, current, row.taskTarget)
			updatedAt = saved.updatedAt.UTC().Format(time.RFC3339)
		} else {
			status = normalizeTaskStatus(status, current, row.taskTarget)
		}
		group.Tasks = append(group.Tasks, domain.TaskItem{
			DatabaseID:  row.taskID,
			ID:          taskID,
			Code:        row.taskCode,
			Revision:    row.taskRevision,
			SeriesCode:  row.taskSeriesCode,
			Title:       row.taskTitle,
			Description: row.taskDescription,
			Source:      row.taskSource,
			Current:     current,
			Target:      row.taskTarget,
			Unit:        row.taskUnit,
			Status:      status,
			ClaimPolicy: row.taskClaimPolicy,
			UpdatedAt:   updatedAt,
			Rewards:     rewardList(taskRewards[row.taskID]),
		})
	}

	for campaignIndex := range builders {
		for groupIndex := range builders[campaignIndex].campaign.Groups {
			group := &builders[campaignIndex].campaign.Groups[groupIndex]
			finalizeTaskGroup(group, groupStates[taskProgressKey(group.DatabaseID, group.PeriodKey)])
		}
		result.Campaigns = append(result.Campaigns, builders[campaignIndex].campaign)
	}
	return result, nil
}

func (r *Repository) loadTaskCatalog(ctx context.Context) ([]taskCatalogRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.code, c.kind, c.title, c.description, c.starts_at, c.ends_at, c.timezone, c.metadata_json,
		       g.id, g.code, g.category, g.title, g.description, g.repeat_policy,
		       g.completion_rule, g.required_task_count, g.unlock_policy, g.metadata_json,
		       d.id, d.code, d.revision, d.series_code, d.title, d.description,
		       d.progress_source, d.metric_key, d.aggregation, d.target_value, d.unit, d.claim_policy,
		       lb.provider, lb.season_id, lb.legacy_scope, lb.legacy_quest_key,
		       lb.progress_metric, lb.claimed_status_min
		FROM launcher_task_campaign c
		JOIN launcher_task_group g ON g.campaign_id = c.id AND g.enabled = 1
		JOIN launcher_task_definition d ON d.group_id = g.id AND d.enabled = 1
		LEFT JOIN launcher_task_legacy_binding lb ON lb.task_id = d.id
		WHERE c.status = 'published'
		  AND (c.starts_at IS NULL OR c.starts_at <= CURRENT_TIMESTAMP(6))
		  AND (c.ends_at IS NULL OR c.ends_at > CURRENT_TIMESTAMP(6))
		  AND d.published_at IS NOT NULL
		  AND d.published_at <= CURRENT_TIMESTAMP(6)
		  AND (d.retired_at IS NULL OR d.retired_at > CURRENT_TIMESTAMP(6))
		  AND d.revision = (
		      SELECT MAX(d2.revision)
		      FROM launcher_task_definition d2
		      WHERE d2.group_id = d.group_id AND d2.code = d.code AND d2.enabled = 1
		        AND d2.published_at IS NOT NULL AND d2.published_at <= CURRENT_TIMESTAMP(6)
		        AND (d2.retired_at IS NULL OR d2.retired_at > CURRENT_TIMESTAMP(6))
		  )
		ORDER BY c.sort_order, c.id, g.sort_order, g.id, d.sort_order, d.id
	`)
	if err != nil {
		return nil, wrapTaskSchemaError("query task catalog", err)
	}
	defer rows.Close()

	items := make([]taskCatalogRow, 0)
	for rows.Next() {
		if len(items) >= maxTaskCatalogRows {
			return nil, fmt.Errorf("task catalog exceeds %d active definitions", maxTaskCatalogRows)
		}
		var item taskCatalogRow
		if err := rows.Scan(
			&item.campaignID, &item.campaignCode, &item.campaignKind, &item.campaignTitle, &item.campaignDescription,
			&item.campaignStartsAt, &item.campaignEndsAt, &item.campaignTimezone, &item.campaignMetadata,
			&item.groupID, &item.groupCode, &item.groupCategory, &item.groupTitle, &item.groupDescription,
			&item.groupRepeatPolicy, &item.groupCompletionRule, &item.groupRequiredCount, &item.groupUnlockPolicy, &item.groupMetadata,
			&item.taskID, &item.taskCode, &item.taskRevision, &item.taskSeriesCode, &item.taskTitle, &item.taskDescription,
			&item.taskSource, &item.taskMetricKey, &item.taskAggregation, &item.taskTarget, &item.taskUnit, &item.taskClaimPolicy,
			&item.legacyProvider, &item.legacySeasonID, &item.legacyScope, &item.legacyQuestKey,
			&item.legacyMetric, &item.legacyClaimedMin,
		); err != nil {
			return nil, fmt.Errorf("scan task catalog: %w", err)
		}
		if item.taskSource == "legacy" && (!item.legacyScope.Valid || !item.legacyQuestKey.Valid || !item.legacyMetric.Valid || !item.legacyClaimedMin.Valid) {
			return nil, fmt.Errorf("legacy task %d has no complete legacy binding", item.taskID)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapTaskSchemaError("iterate task catalog", err)
	}
	return items, nil
}

func (r *Repository) loadTaskProgress(ctx context.Context, steamID uint64, periods map[uint64]string) (map[string]taskProgressRecord, error) {
	result := make(map[string]taskProgressRecord)
	query, args := taskPeriodQuery("launcher_player_task_progress", "task_id", steamID, periods,
		"task_id, period_key, progress_value, state, updated_at")
	if query == "" {
		return result, nil
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapTaskSchemaError("query player task progress", err)
	}
	defer rows.Close()
	for rows.Next() {
		var taskID uint64
		var periodKey string
		var record taskProgressRecord
		if err := rows.Scan(&taskID, &periodKey, &record.value, &record.state, &record.updatedAt); err != nil {
			return nil, fmt.Errorf("scan player task progress: %w", err)
		}
		result[taskProgressKey(taskID, periodKey)] = record
	}
	return result, rows.Err()
}

func (r *Repository) loadTaskGroupStates(ctx context.Context, steamID uint64, periods map[uint64]string) (map[string]taskGroupStateRecord, error) {
	result := make(map[string]taskGroupStateRecord)
	query, args := taskPeriodQuery("launcher_player_task_group_state", "group_id", steamID, periods,
		"group_id, period_key, state")
	if query == "" {
		return result, nil
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapTaskSchemaError("query player task group state", err)
	}
	defer rows.Close()
	for rows.Next() {
		var groupID uint64
		var periodKey string
		var record taskGroupStateRecord
		if err := rows.Scan(&groupID, &periodKey, &record.state); err != nil {
			return nil, fmt.Errorf("scan player task group state: %w", err)
		}
		result[taskProgressKey(groupID, periodKey)] = record
	}
	return result, rows.Err()
}

func (r *Repository) loadTaskRewards(ctx context.Context, catalog []taskCatalogRow) (map[uint64][]domain.TaskReward, map[uint64][]domain.TaskReward, error) {
	taskIDs := make(map[uint64]struct{})
	groupIDs := make(map[uint64]struct{})
	for _, row := range catalog {
		taskIDs[row.taskID] = struct{}{}
		groupIDs[row.groupID] = struct{}{}
	}
	if len(taskIDs) == 0 && len(groupIDs) == 0 {
		return map[uint64][]domain.TaskReward{}, map[uint64][]domain.TaskReward{}, nil
	}

	conditions := make([]string, 0, 2)
	args := make([]any, 0, len(taskIDs)+len(groupIDs))
	if len(taskIDs) > 0 {
		ids := sortedTaskIDs(taskIDs)
		conditions = append(conditions, "task_id IN ("+placeholders(len(ids))+")")
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if len(groupIDs) > 0 {
		ids := sortedTaskIDs(groupIDs)
		conditions = append(conditions, "group_id IN ("+placeholders(len(ids))+")")
		for _, id := range ids {
			args = append(args, id)
		}
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, group_id, reward_type, reward_ref, amount, metadata_json
		FROM launcher_task_reward
		WHERE `+strings.Join(conditions, " OR ")+`
		ORDER BY sort_order, id
	`, args...)
	if err != nil {
		return nil, nil, wrapTaskSchemaError("query task rewards", err)
	}
	defer rows.Close()

	taskRewards := make(map[uint64][]domain.TaskReward)
	groupRewards := make(map[uint64][]domain.TaskReward)
	for rows.Next() {
		var taskID, groupID sql.NullInt64
		var reward domain.TaskReward
		var metadata []byte
		if err := rows.Scan(&taskID, &groupID, &reward.Type, &reward.Ref, &reward.Amount, &metadata); err != nil {
			return nil, nil, fmt.Errorf("scan task reward: %w", err)
		}
		reward.Metadata = taskRawJSON(metadata)
		if taskID.Valid {
			taskRewards[uint64(taskID.Int64)] = append(taskRewards[uint64(taskID.Int64)], reward)
		} else if groupID.Valid {
			groupRewards[uint64(groupID.Int64)] = append(groupRewards[uint64(groupID.Int64)], reward)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return taskRewards, groupRewards, nil
}

func taskPeriodQuery(table, idColumn string, steamID uint64, periods map[uint64]string, columns string) (string, []any) {
	if len(periods) == 0 {
		return "", nil
	}
	byPeriod := make(map[string][]uint64)
	for id, period := range periods {
		byPeriod[period] = append(byPeriod[period], id)
	}
	periodKeys := make([]string, 0, len(byPeriod))
	for period := range byPeriod {
		periodKeys = append(periodKeys, period)
	}
	sort.Strings(periodKeys)
	conditions := make([]string, 0, len(periodKeys))
	args := []any{steamID}
	for _, period := range periodKeys {
		ids := byPeriod[period]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		conditions = append(conditions, fmt.Sprintf("(period_key = ? AND %s IN (%s))", idColumn, placeholders(len(ids))))
		args = append(args, period)
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE steam_id64 = ? AND (%s)", columns, table, strings.Join(conditions, " OR "))
	return query, args
}

func taskPeriodKey(now time.Time, campaignCode, repeatPolicy, timezone string) (string, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}
	local := now.In(location)
	switch repeatPolicy {
	case "once":
		return "once", nil
	case "daily":
		return local.Format("2006-01-02"), nil
	case "weekly":
		year, week := local.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week), nil
	case "campaign":
		return "campaign:" + campaignCode, nil
	default:
		return "", fmt.Errorf("unsupported repeat policy %q", repeatPolicy)
	}
}

func legacyTaskProgress(pass domain.SeasonPassOverview, scope, questKey, metric string, target, claimedMin uint64) (uint64, string) {
	current := legacyProgressValue(pass, metric)
	statusValue := 0
	if scope == "daily" {
		statusValue = pass.DailyQuestStatus[questKey]
	} else if scope == "weekly" {
		statusValue = pass.WeeklyQuestStatus[questKey]
	}
	if claimedMin > 0 && uint64(maxInt(statusValue, 0)) >= claimedMin {
		return current, "claimed"
	}
	return current, normalizeTaskStatus("in_progress", current, target)
}

func legacyProgressValue(pass domain.SeasonPassOverview, metric string) uint64 {
	switch metric {
	case "daily_logged_in":
		if pass.DailyLoggedIn {
			return 1
		}
	case "daily_games":
		return uint64(maxInt(pass.DailyGames, 0))
	case "daily_online_minutes":
		return uint64(maxInt(pass.DailyOnlineMinutes, 0))
	case "weekly_logged_in":
		if pass.WeeklyLoggedIn {
			return 1
		}
	case "weekly_games":
		return uint64(maxInt(pass.WeeklyGames, 0))
	case "weekly_completed_modes":
		return uint64(maxInt(pass.WeeklyCompletedModes, 0))
	case "season_level":
		return uint64(maxInt(pass.Level, 0))
	case "season_experience":
		return uint64(maxInt(pass.Experience, 0))
	}
	return 0
}

func normalizeTaskStatus(state string, current, target uint64) string {
	switch state {
	case "claimed":
		return "claimed"
	case "completed":
		return "completed"
	default:
		if current >= target {
			return "completed"
		}
		return "in_progress"
	}
}

func finalizeTaskGroup(group *domain.TaskGroup, saved taskGroupStateRecord) {
	firstIncomplete := -1
	for index := range group.Tasks {
		task := &group.Tasks[index]
		if task.Status == "completed" || task.Status == "claimed" {
			group.CompletedCount++
		} else if firstIncomplete < 0 {
			firstIncomplete = index
		}
		if task.Status == "completed" && task.ClaimPolicy == "manual" {
			group.ClaimableCount++
		}
	}
	if group.UnlockPolicy == "sequential" && firstIncomplete >= 0 {
		group.CurrentTaskID = group.Tasks[firstIncomplete].ID
		for index := firstIncomplete + 1; index < len(group.Tasks); index++ {
			if group.Tasks[index].Status == "in_progress" {
				group.Tasks[index].Locked = true
			}
		}
	}

	required := len(group.Tasks)
	switch group.CompletionRule {
	case "any":
		required = 1
	case "count":
		if group.RequiredTaskCount != nil {
			required = *group.RequiredTaskCount
		}
	}
	group.State = "in_progress"
	if required > 0 && group.CompletedCount >= required {
		group.State = "completed"
	}
	if saved.state == "completed" || saved.state == "claimed" {
		group.State = saved.state
	}
}

func taskProgressKey(id uint64, period string) string {
	return fmt.Sprintf("%d\x00%s", id, period)
}

func formatTaskTime(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339)
}

func rewardList(items []domain.TaskReward) []domain.TaskReward {
	if items == nil {
		return []domain.TaskReward{}
	}
	return items
}

func taskRawJSON(value []byte) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return json.RawMessage(append([]byte(nil), value...))
}

func sortedTaskIDs(values map[uint64]struct{}) []uint64 {
	result := make([]uint64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func wrapTaskSchemaError(operation string, err error) error {
	var mysqlError *mysqlDriver.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1146 {
		return fmt.Errorf("%s: %w: %v", operation, ErrTaskSchemaUnavailable, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
