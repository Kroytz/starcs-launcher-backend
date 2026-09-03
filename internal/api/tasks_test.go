package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starcs/star-launcher-backend/internal/api"
	"github.com/starcs/star-launcher-backend/internal/demo"
	"github.com/starcs/star-launcher-backend/internal/domain"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
)

type fakeTaskPlayers struct {
	fakePlayers
	overview domain.TaskCenterOverview
	err      error
}

func (players fakeTaskPlayers) Tasks(_ context.Context, _ uint64) (domain.TaskCenterOverview, error) {
	return players.overview, players.err
}

func newTaskHandler(players api.PlayerRepository) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(demo.NewStore(), players, logger, []string{"http://localhost:1420"}, false)
}

func TestTasksReturnsUnifiedReadModel(t *testing.T) {
	expected := domain.TaskCenterOverview{
		Available:   true,
		GeneratedAt: "2026-09-03T00:00:00Z",
		Campaigns: []domain.TaskCampaign{{
			ID:   "campaign:onboarding",
			Code: "onboarding",
			Kind: "onboarding",
			Groups: []domain.TaskGroup{{
				ID:           "group:onboarding:basics",
				Code:         "basics",
				UnlockPolicy: "sequential",
				Tasks:        []domain.TaskItem{},
				Rewards:      []domain.TaskReward{},
			}},
		}},
	}
	handler := newTaskHandler(fakeTaskPlayers{overview: expected})
	token := authenticateTestPlayer(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/tasks", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-StarCS-Reauth", "valid-password")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var actual domain.TaskCenterOverview
	if err := json.Unmarshal(body.Data, &actual); err != nil {
		t.Fatal(err)
	}
	if !actual.Available || len(actual.Campaigns) != 1 || actual.Campaigns[0].Code != "onboarding" {
		t.Fatalf("unexpected task response: %+v", actual)
	}
}

func TestTasksRequiresPasswordReauthentication(t *testing.T) {
	handler := newTaskHandler(fakeTaskPlayers{overview: domain.TaskCenterOverview{Available: true}})
	token := authenticateTestPlayer(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/tasks", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestTasksReportsMissingSchema(t *testing.T) {
	handler := newTaskHandler(fakeTaskPlayers{err: fmt.Errorf("read tasks: %w", mysqlrepo.ErrTaskSchemaUnavailable)})
	token := authenticateTestPlayer(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/tasks", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-StarCS-Reauth", "valid-password")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", response.Code, response.Body.String())
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 5003 {
		t.Fatalf("expected task unavailable code 5003, got %d", body.Code)
	}
}
