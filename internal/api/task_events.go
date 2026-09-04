package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/starcs/star-launcher-backend/internal/domain"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
)

const (
	maxTaskEventBatch      = 100
	maxTaskEventValue      = 1_000_000
	maxTaskEventDimensions = 16
)

type taskEventRepository interface {
	RecordTaskEvents(ctx context.Context, serverID string, events []domain.TaskProgressEvent) (domain.TaskEventBatchResult, error)
}

type taskEventBatchRequest struct {
	ServerID string             `json:"serverId"`
	Events   []taskEventRequest `json:"events"`
}

type taskEventRequest struct {
	EventID     string                     `json:"eventId"`
	Source      string                     `json:"source"`
	SteamID     string                     `json:"steamId"`
	Metric      string                     `json:"metric"`
	Value       int64                      `json:"value"`
	DistinctKey string                     `json:"distinctKey"`
	Dimensions  map[string]json.RawMessage `json:"dimensions"`
	OccurredAt  string                     `json:"occurredAt"`
}

func (h *Handler) handleTaskEventsBatch(w http.ResponseWriter, r *http.Request) {
	if !h.requireGameAPIKey(w, r) {
		return
	}
	repository, ok := h.players.(taskEventRepository)
	if !ok {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "任务事件服务尚未配置")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var request taskEventBatchRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "任务事件请求格式无效")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, 4001, "任务事件请求只能包含一个 JSON 对象")
		return
	}
	if !validTaskEventIdentifier(request.ServerID, 64) {
		h.writeError(w, http.StatusBadRequest, 4001, "serverId 格式无效")
		return
	}
	if len(request.Events) == 0 || len(request.Events) > maxTaskEventBatch {
		h.writeError(w, http.StatusBadRequest, 4001, "每批任务事件数量必须为 1 到 100")
		return
	}

	now := time.Now().UTC()
	events := make([]domain.TaskProgressEvent, 0, len(request.Events))
	seen := make(map[string]struct{}, len(request.Events))
	for _, item := range request.Events {
		event, err := validateTaskEvent(item, now)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, 4001, err.Error())
			return
		}
		if _, exists := seen[event.EventID]; exists {
			h.writeError(w, http.StatusBadRequest, 4001, "同一批次不能包含重复 eventId")
			return
		}
		seen[event.EventID] = struct{}{}
		events = append(events, event)
	}

	result, err := repository.RecordTaskEvents(r.Context(), strings.TrimSpace(request.ServerID), events)
	if err != nil {
		if errors.Is(err, mysqlrepo.ErrTaskSchemaUnavailable) {
			h.writeError(w, http.StatusServiceUnavailable, 5003, "任务系统尚未启用")
			return
		}
		h.logger.Error("record task events", "serverId", request.ServerID, "eventCount", len(events), "error", err)
		h.writeError(w, http.StatusServiceUnavailable, 5002, "任务进度暂时无法保存")
		return
	}
	h.writeSuccess(w, result)
}

func validateTaskEvent(request taskEventRequest, now time.Time) (domain.TaskProgressEvent, error) {
	if !validUUID(request.EventID) {
		return domain.TaskProgressEvent{}, errors.New("eventId 必须是 UUID")
	}
	if !validTaskEventIdentifier(request.Source, 64) {
		return domain.TaskProgressEvent{}, errors.New("source 格式无效")
	}
	metric := strings.TrimSpace(request.Metric)
	if !validTaskEventMetric(metric) {
		return domain.TaskProgressEvent{}, errors.New("metric 必须由小写字母、数字、点、下划线或连字符组成")
	}
	steamID, err := parseSteamID(request.SteamID)
	if err != nil {
		return domain.TaskProgressEvent{}, errors.New("steamId 格式无效")
	}
	if request.Value <= 0 || request.Value > maxTaskEventValue {
		return domain.TaskProgressEvent{}, errors.New("value 必须在 1 到 1000000 之间")
	}
	if len([]rune(request.DistinctKey)) > 160 {
		return domain.TaskProgressEvent{}, errors.New("distinctKey 不能超过 160 个字符")
	}
	dimensions, err := validateTaskEventDimensions(request.Dimensions)
	if err != nil {
		return domain.TaskProgressEvent{}, err
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.OccurredAt))
	if err != nil {
		return domain.TaskProgressEvent{}, errors.New("occurredAt 必须是 RFC3339 时间")
	}
	occurredAt = occurredAt.UTC()
	if occurredAt.After(now.Add(5*time.Minute)) || occurredAt.Before(now.Add(-30*24*time.Hour)) {
		return domain.TaskProgressEvent{}, errors.New("occurredAt 超出允许的时间范围")
	}
	return domain.TaskProgressEvent{
		EventID:     strings.ToLower(strings.TrimSpace(request.EventID)),
		Source:      strings.TrimSpace(request.Source),
		SteamID:     steamID,
		Metric:      metric,
		Value:       uint64(request.Value),
		DistinctKey: strings.TrimSpace(request.DistinctKey),
		Dimensions:  dimensions,
		OccurredAt:  occurredAt,
	}, nil
}

func validateTaskEventDimensions(values map[string]json.RawMessage) (json.RawMessage, error) {
	if len(values) > maxTaskEventDimensions {
		return nil, errors.New("dimensions 最多包含 16 项")
	}
	for key, raw := range values {
		if !validTaskEventIdentifier(key, 32) {
			return nil, errors.New("dimensions 包含无效字段名")
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, errors.New("dimensions 包含无效值")
		}
		switch item := value.(type) {
		case string:
			if len([]rune(item)) > 160 {
				return nil, errors.New("dimensions 字符串不能超过 160 个字符")
			}
		case bool, float64, nil:
		default:
			return nil, errors.New("dimensions 仅支持字符串、数字、布尔值或 null")
		}
	}
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, errors.New("dimensions 无法序列化")
	}
	return encoded, nil
}

func validTaskEventIdentifier(value string, maxLength int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '.' && char != '_' && char != '-' && char != ':' {
			return false
		}
	}
	return true
}

func validTaskEventMetric(value string) bool {
	if value == "" || len(value) > 96 || value != strings.ToLower(value) {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func validUUID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}
