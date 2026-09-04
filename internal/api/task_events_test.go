package api_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/starcs/star-launcher-backend/internal/api"
	"github.com/starcs/star-launcher-backend/internal/demo"
	"github.com/starcs/star-launcher-backend/internal/domain"
)

type fakeTaskEventPlayers struct {
	fakePlayers
	serverID string
	events   []domain.TaskProgressEvent
	result   domain.TaskEventBatchResult
	err      error
}

func (players *fakeTaskEventPlayers) RecordTaskEvents(_ context.Context, serverID string, events []domain.TaskProgressEvent) (domain.TaskEventBatchResult, error) {
	players.serverID = serverID
	players.events = append([]domain.TaskProgressEvent(nil), events...)
	return players.result, players.err
}

func newTaskEventHandler(players api.PlayerRepository) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(
		demo.NewStore(),
		players,
		logger,
		nil,
		false,
		api.WithGameAPIKey("game-secret"),
	)
}

func TestTaskEventsAcceptsAuthenticatedBatch(t *testing.T) {
	players := &fakeTaskEventPlayers{result: domain.TaskEventBatchResult{Accepted: 1, ProgressUpdates: 2}}
	handler := newTaskEventHandler(players)
	body := fmt.Sprintf(`{
		"serverId":"zm-01",
		"events":[{
			"eventId":"4d62e911-b553-4181-8a13-d81b38fa35e7",
			"source":"ZombieZeta",
			"steamId":"76561198000000001",
			"metric":"zm.map.completed",
			"value":1,
			"distinctKey":"zm_example",
			"dimensions":{"won":true,"team":"human"},
			"occurredAt":%q
		}]
	}`, time.Now().UTC().Format(time.RFC3339Nano))
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/task-events/batch", strings.NewReader(body))
	request.Header.Set("X-Star-Api-Key", "game-secret")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if players.serverID != "zm-01" || len(players.events) != 1 {
		t.Fatalf("unexpected repository input: server=%q events=%d", players.serverID, len(players.events))
	}
	event := players.events[0]
	if event.SteamID != 76561198000000001 || event.Metric != "zm.map.completed" || event.DistinctKey != "zm_example" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestTaskEventsRequiresGameAPIKey(t *testing.T) {
	handler := newTaskEventHandler(&fakeTaskEventPlayers{})
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/task-events/batch", strings.NewReader(`{"serverId":"zm-01","events":[]}`))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", response.Code, response.Body.String())
	}
}

func TestTaskEventsRejectsInvalidMetricBeforeRepository(t *testing.T) {
	players := &fakeTaskEventPlayers{}
	handler := newTaskEventHandler(players)
	body := fmt.Sprintf(`{
		"serverId":"zm-01",
		"events":[{
			"eventId":"4d62e911-b553-4181-8a13-d81b38fa35e7",
			"source":"ZombieZeta",
			"steamId":"76561198000000001",
			"metric":"ZM Map Completed",
			"value":1,
			"occurredAt":%q
		}]
	}`, time.Now().UTC().Format(time.RFC3339Nano))
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/task-events/batch", strings.NewReader(body))
	request.Header.Set("X-Star-Api-Key", "game-secret")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", response.Code, response.Body.String())
	}
	if len(players.events) != 0 {
		t.Fatal("repository should not receive invalid events")
	}
}
