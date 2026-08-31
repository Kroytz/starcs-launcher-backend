package api_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
	"github.com/starcs/star-launcher-backend/internal/passwordauth"
)

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func newHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(demo.NewStore(), nil, logger, []string{"http://localhost:1420"}, false)
}

type fakePlayers struct{}

func (fakePlayers) Authenticate(_ context.Context, steamID uint64, password string) error {
	if steamID == 76561198000000001 && password == "valid-password" {
		return nil
	}
	return mysqlrepo.ErrInvalidCredentials
}

func (fakePlayers) GamePasswordHash(_ context.Context, _ uint64) (string, error) {
	return "", nil
}

func (fakePlayers) UpdateGamePasswordHash(_ context.Context, _ uint64, _ string) error {
	return nil
}

func (fakePlayers) Inventory(_ context.Context, steamID uint64) ([]domain.InventoryItem, error) {
	return []domain.InventoryItem{{
		ProductID:    42,
		ID:           "product-42",
		Source:       "starlight",
		Name:         "真实库存测试武器",
		Type:         "武器外观",
		Rarity:       "SR",
		Quantity:     1,
		Mode:         "ALL",
		UseLimit:     1,
		WeaponPrefab: "weapon_ak47",
		WeaponType:   "CommonRifle",
	}}, nil
}

func (fakePlayers) StardustEquipments(_ context.Context, _ uint64) ([]domain.StardustEquipment, error) {
	return []domain.StardustEquipment{}, nil
}

func (fakePlayers) PurchaseHistory(_ context.Context, _ uint64) ([]domain.PurchaseHistoryItem, error) {
	return []domain.PurchaseHistoryItem{}, nil
}

func (fakePlayers) PurchaseStarlight(_ context.Context, _ uint64, _ int64) (int64, error) {
	return 0, nil
}

func (fakePlayers) PurchaseStardust(_ context.Context, _ uint64, _, _ string) (int64, error) {
	return 0, nil
}

func (fakePlayers) EquipStardust(_ context.Context, _ uint64, _, _ string) error {
	return nil
}

func (fakePlayers) UnequipStardust(_ context.Context, _ uint64, _, _ string) error {
	return nil
}

func (fakePlayers) Announcements(_ context.Context) ([]domain.Announcement, error) {
	return []domain.Announcement{{ID: "real-announcement", Title: "真实公告"}}, nil
}

func (fakePlayers) StoreItems(_ context.Context) ([]domain.StoreItem, error) {
	return []domain.StoreItem{{ID: "real-product", Currency: "starlight", Title: "真实商品", Enabled: true}}, nil
}

func (repository fakePlayers) StoreItemsForPlayer(ctx context.Context, _ uint64) ([]domain.StoreItem, error) {
	return repository.StoreItems(ctx)
}

func (fakePlayers) Maps(_ context.Context) ([]domain.MapResource, error) {
	return []domain.MapResource{{ID: 1, Name: "ze_test", WorkshopID: "123"}}, nil
}

func (fakePlayers) WorkshopPacks(_ context.Context, mode string) ([]domain.WorkshopPack, error) {
	return []domain.WorkshopPack{
		{ID: 1, Kind: "base", Mode: "ALL", Title: "基础资源包", WorkshopID: "3711721516"},
		{ID: 2, Kind: "mode", Mode: mode, Title: mode + " 资源包", WorkshopID: "3652674769"},
	}, nil
}

func (fakePlayers) LatestLauncherRelease(_ context.Context) (domain.LauncherRelease, error) {
	return domain.LauncherRelease{
		ID:          1,
		Version:     "0.2.0",
		Mandatory:   true,
		Changelog:   "## 更新内容\n- 测试条目",
		ArtifactURL: "https://static.starcs.cn/launcher/STAR Launcher_0.2.0_x64-setup.exe",
		Signature:   "untrusted comment: test signature",
		PubDate:     time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}, nil
}

func (repository fakePlayers) PlayerReadModel(ctx context.Context, steamID uint64) (domain.PlayerReadModel, error) {
	inventory, err := repository.Inventory(ctx, steamID)
	return domain.PlayerReadModel{Inventory: inventory}, err
}

func newAuthenticatedHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(demo.NewStore(), fakePlayers{}, logger, []string{"http://localhost:1420"}, false)
}

func newPasswordlessHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(demo.NewStore(), fakePlayers{}, logger, []string{"http://localhost:1420"}, true)
}

type fakeEquipmentService struct {
	mutation domain.EquipmentMutation
}

func (service *fakeEquipmentService) Load(_ context.Context, _ uint64) (domain.EquipmentProfile, error) {
	profile := domain.NewEquipmentProfile()
	for _, mode := range domain.EquipmentModes {
		profile.Modes[mode] = domain.NewModeEquipment()
	}
	return profile, nil
}

func (service *fakeEquipmentService) Apply(ctx context.Context, steamID uint64, mutation domain.EquipmentMutation) (domain.EquipmentProfile, error) {
	service.mutation = mutation
	return service.Load(ctx, steamID)
}

func newEquipmentHandler(service api.EquipmentService) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.NewHandler(demo.NewStore(), fakePlayers{}, logger, nil, false, api.WithEquipmentService(service))
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

func TestWorkshopPacksReturnsBaseAndRequestedMode(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workshop-packs?mode=zm", nil)
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var packs []domain.WorkshopPack
	if err := json.Unmarshal(body.Data, &packs); err != nil {
		t.Fatalf("decode workshop packs: %v", err)
	}
	if len(packs) != 2 || packs[0].Kind != "base" || packs[1].Mode != "ZM" {
		t.Fatalf("unexpected workshop packs: %#v", packs)
	}
}

func TestWorkshopPacksRejectsInvalidMode(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workshop-packs?mode=zm%20drop", nil)
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", response.Code)
	}
}

func TestLauncherUpdatePolicy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/launcher/update-policy", nil)
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)

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
		Version   string `json:"version"`
		Mandatory bool   `json:"mandatory"`
		Changelog string `json:"changelog"`
		PubDate   string `json:"pubDate"`
	}
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatalf("decode policy data: %v", err)
	}
	if data.Version != "0.2.0" || !data.Mandatory || data.Changelog == "" {
		t.Fatalf("unexpected policy payload: %#v", data)
	}
	if _, err := time.Parse(time.RFC3339, data.PubDate); err != nil {
		t.Fatalf("pubDate is not RFC3339: %q", data.PubDate)
	}
}

func TestLauncherUpdatePolicyWithoutRelease(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/launcher/update-policy", nil)
	response := httptest.NewRecorder()
	newHandler().ServeHTTP(response, request) // players == nil → 无发布

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", response.Code)
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 4004 {
		t.Fatalf("expected application code 4004, got %d", body.Code)
	}
}

func TestLauncherUpdateManifest(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{name: "older version gets manifest", query: "target=windows&arch=x86_64&current_version=0.1.0", wantStatus: http.StatusOK},
		{name: "v-prefixed older version gets manifest", query: "current_version=v0.1.0", wantStatus: http.StatusOK},
		{name: "missing current version gets manifest", query: "", wantStatus: http.StatusOK},
		{name: "same version gets 204", query: "current_version=0.2.0", wantStatus: http.StatusNoContent},
		{name: "newer version gets 204", query: "current_version=0.3.0", wantStatus: http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/launcher/manifest?"+tc.query, nil)
			response := httptest.NewRecorder()
			newAuthenticatedHandler().ServeHTTP(response, request)

			if response.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, response.Code)
			}
			if tc.wantStatus != http.StatusOK {
				return
			}

			// 裸 JSON（无业务信封），符合 Tauri updater 静态清单格式
			var manifest struct {
				Version  string `json:"version"`
				Notes    string `json:"notes"`
				PubDate  string `json:"pub_date"`
				Code     int    `json:"code"`
				Platform map[string]struct {
					Signature string `json:"signature"`
					URL       string `json:"url"`
				} `json:"platforms"`
			}
			if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}
			if manifest.Code != 0 {
				t.Fatal("manifest must not be wrapped in the business envelope")
			}
			if manifest.Version != "0.2.0" || manifest.Notes == "" {
				t.Fatalf("unexpected manifest: %#v", manifest)
			}
			platform, ok := manifest.Platform["windows-x86_64"]
			if !ok || platform.Signature == "" || platform.URL == "" {
				t.Fatalf("manifest missing windows-x86_64 platform: %#v", manifest.Platform)
			}
			if _, err := time.Parse(time.RFC3339, manifest.PubDate); err != nil {
				t.Fatalf("pub_date is not RFC3339: %q", manifest.PubDate)
			}
		})
	}
}

func TestLauncherUpdateManifestWithoutRelease(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/launcher/manifest?current_version=0.1.0", nil)
	response := httptest.NewRecorder()
	newHandler().ServeHTTP(response, request) // players == nil → 无发布

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", response.Code)
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

func TestStoreItemsAfdianFilter(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/store/items?currency=afdian", nil)
	response := httptest.NewRecorder()
	newHandler().ServeHTTP(response, request)

	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var items []domain.StoreItem
	if err := json.Unmarshal(body.Data, &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if len(items) != 1 || items[0].Currency != "afdian" || items[0].PurchaseURL == "" {
		t.Fatalf("unexpected afdian items: %+v", items)
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
	inventoryRequest.Header.Set("X-StarCS-Reauth", "valid-password")
	inventoryResponse := httptest.NewRecorder()
	handler.ServeHTTP(inventoryResponse, inventoryRequest)
	if inventoryResponse.Code != http.StatusOK {
		t.Fatalf("expected inventory status 200, got %d", inventoryResponse.Code)
	}
}

func TestWrongOperationPasswordRevokesSession(t *testing.T) {
	handler := newAuthenticatedHandler()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"steamId":"76561198000000001","password":"valid-password"}`))
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, loginRequest)
	var body envelope
	if err := json.NewDecoder(loginResponse.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body.Data, &login); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"password":"changed-password"}`))
	request.Header.Set("Authorization", "Bearer "+login.Token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 4011 {
		t.Fatalf("expected stale credentials code 4011, got %d", body.Code)
	}

	retry := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"password":"valid-password"}`))
	retry.Header.Set("Authorization", "Bearer "+login.Token)
	retryResponse := httptest.NewRecorder()
	handler.ServeHTTP(retryResponse, retry)
	if retryResponse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token should stay unauthorized, got %d", retryResponse.Code)
	}
}

func TestEquipmentEndpointsReadAndMutateServerPreferences(t *testing.T) {
	service := &fakeEquipmentService{}
	handler := newEquipmentHandler(service)
	token := authenticateTestPlayer(t, handler)

	readRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me/equipment", nil)
	readRequest.Header.Set("Authorization", "Bearer "+token)
	readRequest.Header.Set("X-StarCS-Reauth", "valid-password")
	readResponse := httptest.NewRecorder()
	handler.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("equipment read failed: %d %s", readResponse.Code, readResponse.Body.String())
	}

	equipRequest := httptest.NewRequest(http.MethodPost, "/api/v1/me/equipment/equip", strings.NewReader(`{"productId":42,"modes":["ZM","SCP"],"team":"all"}`))
	equipRequest.Header.Set("Authorization", "Bearer "+token)
	equipRequest.Header.Set("X-StarCS-Reauth", "valid-password")
	equipResponse := httptest.NewRecorder()
	handler.ServeHTTP(equipResponse, equipRequest)
	if equipResponse.Code != http.StatusOK {
		t.Fatalf("equipment mutation failed: %d %s", equipResponse.Code, equipResponse.Body.String())
	}
	if !service.mutation.Equip || service.mutation.ProductID != 42 || service.mutation.Slot != "weapon" {
		t.Fatalf("unexpected equipment mutation: %+v", service.mutation)
	}
	if service.mutation.WeaponType != "CommonRifle" || service.mutation.WeaponPrefab != "weapon_ak47" {
		t.Fatalf("weapon metadata must come from owned inventory: %+v", service.mutation)
	}
	if strings.Join(service.mutation.Modes, ",") != "ZM,SCP" {
		t.Fatalf("unexpected modes: %+v", service.mutation.Modes)
	}

	unequipRequest := httptest.NewRequest(http.MethodPost, "/api/v1/me/equipment/unequip", strings.NewReader(`{"productId":42,"modes":["ZM"],"team":"all"}`))
	unequipRequest.Header.Set("Authorization", "Bearer "+token)
	unequipRequest.Header.Set("X-StarCS-Reauth", "valid-password")
	unequipResponse := httptest.NewRecorder()
	handler.ServeHTTP(unequipResponse, unequipRequest)
	if unequipResponse.Code != http.StatusOK || service.mutation.Equip {
		t.Fatalf("unequip failed: status=%d mutation=%+v body=%s", unequipResponse.Code, service.mutation, unequipResponse.Body.String())
	}
}

func TestEquipmentMutationRejectsUnownedProduct(t *testing.T) {
	service := &fakeEquipmentService{}
	handler := newEquipmentHandler(service)
	token := authenticateTestPlayer(t, handler)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/equipment/equip", strings.NewReader(`{"productId":999,"modes":["ZM"],"team":"all"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-StarCS-Reauth", "valid-password")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected unowned item rejection, got %d: %s", response.Code, response.Body.String())
	}
}

func authenticateTestPlayer(t *testing.T, handler http.Handler) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"steamId":"76561198000000001","password":"valid-password"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body.Data, &login); err != nil {
		t.Fatal(err)
	}
	return login.Token
}

type mutablePasswordPlayers struct {
	fakePlayers
	hash string
}

func (players *mutablePasswordPlayers) Authenticate(_ context.Context, steamID uint64, password string) error {
	if steamID != 76561198000000001 || players.hash == "" {
		return mysqlrepo.ErrInvalidCredentials
	}
	valid, err := passwordauth.Verify(password, players.hash)
	if err != nil {
		return err
	}
	if !valid {
		return mysqlrepo.ErrInvalidCredentials
	}
	return nil
}

func (players *mutablePasswordPlayers) GamePasswordHash(_ context.Context, steamID uint64) (string, error) {
	if steamID != 76561198000000001 {
		return "", mysqlrepo.ErrPlayerNotFound
	}
	return players.hash, nil
}

func (players *mutablePasswordPlayers) UpdateGamePasswordHash(_ context.Context, steamID uint64, encoded string) error {
	if steamID != 76561198000000001 {
		return mysqlrepo.ErrPlayerNotFound
	}
	players.hash = encoded
	return nil
}

func TestGamePasswordEndpointSetsAndChangesPassword(t *testing.T) {
	players := &mutablePasswordPlayers{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := api.NewHandler(demo.NewStore(), players, logger, nil, false, api.WithGameAPIKey("game-secret"))

	unvalidatedRequest := httptest.NewRequest(http.MethodPost, "/internal/v1/game-password", strings.NewReader(`{"steamId":"76561198000000001","newPassword":"first-password"}`))
	unvalidatedRequest.Header.Set("X-Star-Api-Key", "game-secret")
	unvalidatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unvalidatedResponse, unvalidatedRequest)
	if unvalidatedResponse.Code != http.StatusOK {
		t.Fatalf("identity rejection must use the standard HTTP 200 envelope, got %d", unvalidatedResponse.Code)
	}
	var unvalidatedBody envelope
	if err := json.NewDecoder(unvalidatedResponse.Body).Decode(&unvalidatedBody); err != nil {
		t.Fatal(err)
	}
	if unvalidatedBody.Code != 4003 || players.hash != "" {
		t.Fatalf("unvalidated identity must not set a password: body=%+v hash=%q", unvalidatedBody, players.hash)
	}

	setRequest := httptest.NewRequest(http.MethodPost, "/internal/v1/game-password", strings.NewReader(`{"steamId":"76561198000000001","newPassword":"first-password","identityValidated":true}`))
	setRequest.Header.Set("X-Star-Api-Key", "game-secret")
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusOK {
		t.Fatalf("initial password set failed: %d %s", setResponse.Code, setResponse.Body.String())
	}
	valid, err := passwordauth.Verify("first-password", players.hash)
	if err != nil || !valid {
		t.Fatalf("stored initial hash is invalid: valid=%v err=%v", valid, err)
	}

	changeRequest := httptest.NewRequest(http.MethodPost, "/internal/v1/game-password", strings.NewReader(`{"steamId":"76561198000000001","newPassword":"second-password","identityValidated":true}`))
	changeRequest.Header.Set("X-Star-Api-Key", "game-secret")
	changeResponse := httptest.NewRecorder()
	handler.ServeHTTP(changeResponse, changeRequest)
	if changeResponse.Code != http.StatusOK {
		t.Fatalf("password change failed: %d %s", changeResponse.Code, changeResponse.Body.String())
	}
	valid, err = passwordauth.Verify("second-password", players.hash)
	if err != nil || !valid {
		t.Fatalf("stored changed hash is invalid: valid=%v err=%v", valid, err)
	}
}

func TestLoginAllowsSteamIDOnlyWhenPasswordAuthIsDisabled(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"steamId":"76561198000000001"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	newPasswordlessHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestLoginStillRequiresPasswordByDefault(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"steamId":"76561198000000001"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", response.Code, response.Body.String())
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

type stardustTrackingPlayers struct {
	fakePlayers
	equipped map[string]string
}

func newStardustTrackingPlayers() *stardustTrackingPlayers {
	return &stardustTrackingPlayers{equipped: make(map[string]string)}
}

func (players *stardustTrackingPlayers) StardustEquipments(_ context.Context, _ uint64) ([]domain.StardustEquipment, error) {
	equipments := make([]domain.StardustEquipment, 0, len(players.equipped))
	for itemType, uniqueID := range players.equipped {
		equipments = append(equipments, domain.StardustEquipment{Type: itemType, UniqueID: uniqueID, Slot: 1})
	}
	return equipments, nil
}

func (players *stardustTrackingPlayers) EquipStardust(_ context.Context, _ uint64, itemType, uniqueID string) error {
	if uniqueID == "not-owned" {
		return errors.New("该物品不在当前玩家的有效星尘库存中")
	}
	players.equipped[itemType] = uniqueID
	return nil
}

func (players *stardustTrackingPlayers) UnequipStardust(_ context.Context, _ uint64, itemType, uniqueID string) error {
	if players.equipped[itemType] == uniqueID {
		delete(players.equipped, itemType)
	}
	return nil
}

func TestStardustEquipmentEquipUnequipAndTypeMutex(t *testing.T) {
	players := newStardustTrackingPlayers()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := api.NewHandler(demo.NewStore(), players, logger, nil, false)
	token := authenticateTestPlayer(t, handler)

	call := func(path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-StarCS-Reauth", "valid-password")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	if response := call("/api/v1/me/stardust/equip", `{"itemType":"chatcolor","uniqueId":"red"}`); response.Code != http.StatusOK {
		t.Fatalf("equip failed: %d %s", response.Code, response.Body.String())
	}
	if players.equipped["chatcolor"] != "red" {
		t.Fatalf("expected chatcolor=red, got %+v", players.equipped)
	}

	// 同 Type 装备新物品应顶掉旧物品
	if response := call("/api/v1/me/stardust/equip", `{"itemType":"chatcolor","uniqueId":"blue"}`); response.Code != http.StatusOK {
		t.Fatalf("re-equip failed: %d %s", response.Code, response.Body.String())
	}
	if players.equipped["chatcolor"] != "blue" {
		t.Fatalf("expected chatcolor=blue after mutex replace, got %+v", players.equipped)
	}

	// 卸下
	if response := call("/api/v1/me/stardust/unequip", `{"itemType":"chatcolor","uniqueId":"blue"}`); response.Code != http.StatusOK {
		t.Fatalf("unequip failed: %d %s", response.Code, response.Body.String())
	}
	if _, exists := players.equipped["chatcolor"]; exists {
		t.Fatalf("expected chatcolor removed, got %+v", players.equipped)
	}

	// 未拥有的物品应被拒绝
	if response := call("/api/v1/me/stardust/equip", `{"itemType":"chatcolor","uniqueId":"not-owned"}`); response.Code == http.StatusOK {
		t.Fatalf("expected unowned stardust item rejection, got %d", response.Code)
	}
}

func TestStardustEquipmentRequiresLogin(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/stardust/equip", strings.NewReader(`{"itemType":"chatcolor","uniqueId":"red"}`))
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
}

type purchaseTrackingPlayers struct {
	fakePlayers
	balance int64
}

func (players *purchaseTrackingPlayers) PurchaseStarlight(_ context.Context, _ uint64, pricingID int64) (int64, error) {
	if pricingID == 999 {
		return 0, mysqlrepo.ErrPricingNotFound
	}
	if players.balance < 100 {
		return 0, mysqlrepo.ErrInsufficientStarlight
	}
	players.balance -= 100
	return players.balance, nil
}

func TestStarlightPurchase(t *testing.T) {
	players := &purchaseTrackingPlayers{balance: 250}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := api.NewHandler(demo.NewStore(), players, logger, nil, false)
	token := authenticateTestPlayer(t, handler)

	call := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/me/store/purchase", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-StarCS-Reauth", "valid-password")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := call(`{"pricingId":1}`)
	if response.Code != http.StatusOK {
		t.Fatalf("purchase failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Starlight       int64                        `json:"starlight"`
			Inventory       []domain.InventoryItem       `json:"inventory"`
			PurchaseHistory []domain.PurchaseHistoryItem `json:"purchaseHistory"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode purchase response: %v", err)
	}
	if payload.Data.Starlight != 150 {
		t.Fatalf("expected starlight 150, got %d", payload.Data.Starlight)
	}
	if len(payload.Data.Inventory) == 0 {
		t.Fatalf("expected refreshed inventory in purchase response")
	}

	// 余额不足：250 - 100 - 100 = 50 < 100
	_ = call(`{"pricingId":1}`)
	response = call(`{"pricingId":1}`)
	var insufficient struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &insufficient); err != nil {
		t.Fatalf("decode insufficient response: %v", err)
	}
	if insufficient.Code == 2000 || insufficient.Msg != "星光余额不足" {
		t.Fatalf("expected insufficient balance error, got %+v", insufficient)
	}

	// 已下架的价格档位
	response = call(`{"pricingId":999}`)
	var notFound struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &notFound); err != nil {
		t.Fatalf("decode not-found response: %v", err)
	}
	if notFound.Code == 2000 || notFound.Msg != "该商品不可用或已下架" {
		t.Fatalf("expected pricing not found error, got %+v", notFound)
	}
}

func TestStarlightPurchaseRequiresLogin(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/store/purchase", strings.NewReader(`{"pricingId":1}`))
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
}

func (players *purchaseTrackingPlayers) PurchaseStardust(_ context.Context, _ uint64, itemType, uniqueID string) (int64, error) {
	if uniqueID == "not-for-sale" {
		return 0, mysqlrepo.ErrPricingNotFound
	}
	if players.balance < 50 {
		return 0, mysqlrepo.ErrInsufficientStardust
	}
	players.balance -= 50
	return players.balance, nil
}

func TestStardustPurchase(t *testing.T) {
	players := &purchaseTrackingPlayers{balance: 120}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := api.NewHandler(demo.NewStore(), players, logger, nil, false)
	token := authenticateTestPlayer(t, handler)

	call := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/me/stardust/purchase", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-StarCS-Reauth", "valid-password")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	response := call(`{"itemType":"chatcolor","uniqueId":"red"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("stardust purchase failed: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Stardust   int64              `json:"stardust"`
			Inventory  []domain.InventoryItem `json:"inventory"`
			StoreItems []domain.StoreItem `json:"storeItems"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode stardust purchase response: %v", err)
	}
	if payload.Data.Stardust != 70 {
		t.Fatalf("expected stardust 70, got %d", payload.Data.Stardust)
	}
	if len(payload.Data.StoreItems) == 0 {
		t.Fatalf("expected refreshed store items in stardust purchase response")
	}

	// 余额不足：120 - 50 - 50 = 20 < 50
	_ = call(`{"itemType":"chatcolor","uniqueId":"blue"}`)
	response = call(`{"itemType":"chatcolor","uniqueId":"green"}`)
	var insufficient struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &insufficient); err != nil {
		t.Fatalf("decode insufficient response: %v", err)
	}
	if insufficient.Code == 2000 || insufficient.Msg != "星尘余额不足" {
		t.Fatalf("expected insufficient stardust error, got %+v", insufficient)
	}

	// 未上架的物品
	response = call(`{"itemType":"chatcolor","uniqueId":"not-for-sale"}`)
	var notFound struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &notFound); err != nil {
		t.Fatalf("decode not-found response: %v", err)
	}
	if notFound.Code == 2000 || notFound.Msg != "该商品不可用或已下架" {
		t.Fatalf("expected pricing not found error, got %+v", notFound)
	}
}

func TestStardustPurchaseRequiresLogin(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/stardust/purchase", strings.NewReader(`{"itemType":"chatcolor","uniqueId":"red"}`))
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
}


func TestBootstrapUsesReadOnlyRepositoryData(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	response := httptest.NewRecorder()
	newAuthenticatedHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var data struct {
		Announcements []domain.Announcement `json:"announcements"`
		StoreItems    []domain.StoreItem    `json:"storeItems"`
		Maps          []domain.MapResource  `json:"maps"`
	}
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if len(data.Announcements) != 1 || data.Announcements[0].ID != "real-announcement" {
		t.Fatalf("unexpected announcements: %+v", data.Announcements)
	}
	if len(data.StoreItems) != 1 || data.StoreItems[0].ID != "real-product" {
		t.Fatalf("unexpected store items: %+v", data.StoreItems)
	}
	if len(data.Maps) != 1 || data.Maps[0].WorkshopID != "123" {
		t.Fatalf("unexpected maps: %+v", data.Maps)
	}
}
