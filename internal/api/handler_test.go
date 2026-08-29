package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/starcs/star-launcher-backend/internal/api"
	"github.com/starcs/star-launcher-backend/internal/demo"
	"github.com/starcs/star-launcher-backend/internal/domain"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
)

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func newHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(demo.NewStore(), nil, logger, []string{"http://localhost:1420"})
}

type fakePlayers struct{}

func (fakePlayers) Authenticate(_ context.Context, steamID uint64, password string) error {
	if steamID == 76561198000000001 && password == "valid-password" {
		return nil
	}
	return mysqlrepo.ErrInvalidCredentials
}

func (fakePlayers) Inventory(_ context.Context, steamID uint64) ([]domain.InventoryItem, error) {
	return []domain.InventoryItem{{ProductID: 42, ID: "product-42", Name: "真实库存测试物品", Type: "物品", Rarity: "SR", Quantity: 1}}, nil
}

func newAuthenticatedHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(demo.NewStore(), fakePlayers{}, logger, []string{"http://localhost:1420"})
}

func TestBootstrap(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	response := httptest.NewRecorder()
	newHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}

	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 2000 {
		t.Fatalf("expected application code 2000, got %d", body.Code)
	}

	var data struct {
		Announcements []any `json:"announcements"`
		StoreItems    []any `json:"storeItems"`
		Inventory     []any `json:"inventory"`
	}
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatalf("decode bootstrap data: %v", err)
	}
	if len(data.Announcements) == 0 || len(data.StoreItems) == 0 || len(data.Inventory) == 0 {
		t.Fatal("bootstrap response should contain demo display data")
	}
}

func TestStoreItemsCurrencyFilter(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/store/items?currency=stardust", nil)
	response := httptest.NewRecorder()
	newHandler().ServeHTTP(response, request)

	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var items []struct {
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(body.Data, &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 stardust items, got %d", len(items))
	}
	for _, item := range items {
		if item.Currency != "stardust" {
			t.Fatalf("expected stardust item, got %q", item.Currency)
		}
	}
}

func TestStoreItemsRejectsUnknownCurrency(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/store/items?currency=coin", nil)
	response := httptest.NewRecorder()
	newHandler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", response.Code)
	}
}

func TestCORSForLauncherDevOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("Origin", "http://localhost:1420")
	response := httptest.NewRecorder()
	newHandler().ServeHTTP(response, request)

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:1420" {
		t.Fatalf("unexpected allow origin %q", got)
	}
}

func TestLoginReturnsAuthenticatedInventory(t *testing.T) {
	handler := newAuthenticatedHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"steamId":"76561198000000001","password":"valid-password"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var login struct {
		Token     string                 `json:"token"`
		Inventory []domain.InventoryItem `json:"inventory"`
	}
	if err := json.Unmarshal(body.Data, &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if login.Token == "" || len(login.Inventory) != 1 || login.Inventory[0].ProductID != 42 {
		t.Fatalf("unexpected login response: %+v", login)
	}

	inventoryRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me/inventory", nil)
	inventoryRequest.Header.Set("Authorization", "Bearer "+login.Token)
	inventoryResponse := httptest.NewRecorder()
	handler.ServeHTTP(inventoryResponse, inventoryRequest)
	if inventoryResponse.Code != http.StatusOK {
		t.Fatalf("expected inventory status 200, got %d", inventoryResponse.Code)
	}
}

func TestInventoryRequiresLogin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/inventory", nil)
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
}
