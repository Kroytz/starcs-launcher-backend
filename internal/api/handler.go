package api

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/starcs/star-launcher-backend/internal/demo"
	"github.com/starcs/star-launcher-backend/internal/domain"
	"github.com/starcs/star-launcher-backend/internal/gamews"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
	"github.com/starcs/star-launcher-backend/internal/passwordauth"
)

const (
	successCode            = 2000
	reauthHeader           = "X-StarCS-Reauth"
	gameAPIKeyHeader       = "X-Star-Api-Key"
	sessionExpiredCode     = 4010
	credentialsStaleCode   = 4011
	invalidCredentialsCode = 4012
	authBusyCode           = 5004
)

type envelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

type PlayerRepository interface {
	Authenticate(ctx context.Context, steamID uint64, password string) error
	GamePasswordHash(ctx context.Context, steamID uint64) (string, error)
	UpdateGamePasswordHash(ctx context.Context, steamID uint64, encoded string) error
	Inventory(ctx context.Context, steamID uint64) ([]domain.InventoryItem, error)
	PurchaseHistory(ctx context.Context, steamID uint64) ([]domain.PurchaseHistoryItem, error)
	PurchaseStarlight(ctx context.Context, steamID uint64, pricingID int64) (int64, error)
	PurchaseStardust(ctx context.Context, steamID uint64, itemType, uniqueID string) (int64, error)
	StardustEquipments(ctx context.Context, steamID uint64) ([]domain.StardustEquipment, error)
	EquipStardust(ctx context.Context, steamID uint64, itemType, uniqueID string) error
	UnequipStardust(ctx context.Context, steamID uint64, itemType, uniqueID string) error
	Announcements(ctx context.Context) ([]domain.Announcement, error)
	StoreItems(ctx context.Context) ([]domain.StoreItem, error)
	StoreItemsForPlayer(ctx context.Context, steamID uint64) ([]domain.StoreItem, error)
	Maps(ctx context.Context) ([]domain.MapResource, error)
	WorkshopPacks(ctx context.Context, mode string) ([]domain.WorkshopPack, error)
	LatestLauncherRelease(ctx context.Context) (domain.LauncherRelease, error)
	PlayerReadModel(ctx context.Context, steamID uint64) (domain.PlayerReadModel, error)
}

type EquipmentService interface {
	Load(ctx context.Context, steamID uint64) (domain.EquipmentProfile, error)
	Apply(ctx context.Context, steamID uint64, mutation domain.EquipmentMutation) (domain.EquipmentProfile, error)
}

type loginRequest struct {
	SteamID  string `json:"steamId"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
	domain.PlayerReadModel
}

type verifyPasswordRequest struct {
	Password string `json:"password"`
}

type gamePasswordRequest struct {
	SteamID           string `json:"steamId"`
	NewPassword       string `json:"newPassword"`
	IdentityValidated bool   `json:"identityValidated"`
}

type equipmentMutationRequest struct {
	ProductID int64    `json:"productId"`
	Modes     []string `json:"modes"`
	Team      string   `json:"team"`
}

type stardustEquipmentRequest struct {
	ItemType string `json:"itemType"`
	UniqueID string `json:"uniqueId"`
}

type starlightPurchaseRequest struct {
	PricingID int64 `json:"pricingId"`
}

type stardustPurchaseRequest struct {
	ItemType string `json:"itemType"`
	UniqueID string `json:"uniqueId"`
}

type session struct {
	steamID   uint64
	expiresAt time.Time
}

type Handler struct {
	store            demo.Store
	players          PlayerRepository
	logger           *slog.Logger
	allowedOrigins   map[string]struct{}
	skipPasswordAuth bool
	gameAPIKey       string
	equipment        EquipmentService
	gameWS           *gamews.Hub
	authLimiter      *authRateLimiter
	sessionMu        sync.Mutex
	sessions         map[string]session
}

type HandlerOption func(*Handler)

func WithGameAPIKey(key string) HandlerOption {
	return func(handler *Handler) {
		handler.gameAPIKey = strings.TrimSpace(key)
	}
}

func WithEquipmentService(service EquipmentService) HandlerOption {
	return func(handler *Handler) {
		handler.equipment = service
	}
}

func WithGameWS(hub *gamews.Hub) HandlerOption {
	return func(handler *Handler) {
		handler.gameWS = hub
	}
}

func NewHandler(store demo.Store, players PlayerRepository, logger *slog.Logger, allowedOrigins []string, skipPasswordAuth bool, options ...HandlerOption) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	h := &Handler{
		store:            store,
		players:          players,
		logger:           logger,
		allowedOrigins:   make(map[string]struct{}, len(allowedOrigins)),
		skipPasswordAuth: skipPasswordAuth,
		authLimiter:      newAuthRateLimiter(),
		sessions:         make(map[string]session),
	}
	for _, option := range options {
		option(h)
	}
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			h.allowedOrigins[origin] = struct{}{}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleIndex)
	mux.HandleFunc("/healthz", h.handleHealth)
	mux.HandleFunc("/api/v1/bootstrap", h.handleBootstrap)
	mux.HandleFunc("/api/v1/store/items", h.handleStoreItems)
	mux.HandleFunc("/api/v1/maps", h.handleMaps)
	mux.HandleFunc("/api/v1/workshop-packs", h.handleWorkshopPacks)
	mux.HandleFunc("/api/v1/launcher/update-policy", h.handleLauncherUpdatePolicy)
	mux.HandleFunc("/api/v1/launcher/manifest", h.handleLauncherUpdateManifest)
	mux.HandleFunc("/api/v1/auth/login", h.handleLogin)
	mux.HandleFunc("/api/v1/auth/verify", h.handleVerifyPassword)
	mux.HandleFunc("/api/v1/me/inventory", h.handleInventory)
	mux.HandleFunc("/api/v1/me/equipment", h.handleEquipment)
	mux.HandleFunc("/api/v1/me/equipment/equip", h.handleEquip)
	mux.HandleFunc("/api/v1/me/equipment/unequip", h.handleUnequip)
	mux.HandleFunc("/api/v1/me/stardust/equip", h.handleStardustEquip)
	mux.HandleFunc("/api/v1/me/stardust/unequip", h.handleStardustUnequip)
	mux.HandleFunc("/api/v1/me/store/purchase", h.handleStarlightPurchase)
	mux.HandleFunc("/api/v1/me/stardust/purchase", h.handleStardustPurchase)
	mux.HandleFunc("/internal/v1/game-password", h.handleGamePassword)
	if h.gameWS != nil {
		mux.Handle("/internal/v1/ws/game", h.gameWS)
		mux.HandleFunc("POST /internal/v1/servers/{serverId}/commands", h.handleGameServerCommand)
		mux.HandleFunc("GET /internal/v1/servers", h.handleListGameServers)
	}

	return h.withLogging(h.withCORS(mux))
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		h.writeError(w, http.StatusNotFound, 4004, "接口不存在")
		return
	}
	if !h.requireGET(w, r) {
		return
	}

	h.writeSuccess(w, map[string]any{
		"service":   "star-launcher-backend",
		"version":   "0.1.0",
		"health":    "/healthz",
		"bootstrap": "/api/v1/bootstrap",
	})
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	h.writeSuccess(w, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	bootstrap := h.store.Bootstrap()
	if h.players != nil {
		announcements, err := h.players.Announcements(r.Context())
		if err != nil {
			h.writeRepositoryError(w, "query announcements", err)
			return
		}
		storeItems, err := h.players.StoreItems(r.Context())
		if err != nil {
			h.writeRepositoryError(w, "query store items", err)
			return
		}
		maps, err := h.players.Maps(r.Context())
		if err != nil {
			h.writeRepositoryError(w, "query maps", err)
			return
		}
		bootstrap.Announcements = announcements
		bootstrap.StoreItems = storeItems
		bootstrap.Maps = maps
	}
	h.writeSuccess(w, bootstrap)
}

func (h *Handler) handleStoreItems(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}

	currency := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("currency")))
	if currency != "" && currency != "starlight" && currency != "stardust" && currency != "afdian" {
		h.writeError(w, http.StatusBadRequest, 4001, "currency 仅支持 starlight、stardust 或 afdian")
		return
	}
	if h.players == nil {
		h.writeSuccess(w, h.store.StoreItems(currency))
		return
	}
	items, err := h.players.StoreItems(r.Context())
	if err != nil {
		h.writeRepositoryError(w, "query store items", err)
		return
	}
	filtered := items[:0]
	for _, item := range items {
		if currency == "" || item.Currency == currency {
			filtered = append(filtered, item)
		}
	}
	h.writeSuccess(w, filtered)
}

func (h *Handler) handleMaps(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	if h.players == nil {
		h.writeSuccess(w, []domain.MapResource{})
		return
	}
	maps, err := h.players.Maps(r.Context())
	if err != nil {
		h.writeRepositoryError(w, "query maps", err)
		return
	}
	h.writeSuccess(w, maps)
}

func (h *Handler) handleWorkshopPacks(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	mode := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("mode")))
	if !validModeCode(mode) {
		h.writeError(w, http.StatusBadRequest, 4001, "mode 必须是 1-16 位字母、数字、下划线或连字符")
		return
	}
	if h.players == nil {
		h.writeSuccess(w, []domain.WorkshopPack{})
		return
	}
	packs, err := h.players.WorkshopPacks(r.Context(), mode)
	if err != nil {
		h.writeRepositoryError(w, "query workshop packs", err)
		return
	}
	h.writeSuccess(w, packs)
}

func validModeCode(mode string) bool {
	if len(mode) == 0 || len(mode) > 16 {
		return false
	}
	for _, char := range mode {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

// handleLauncherUpdatePolicy 返回最新发布的更新策略（信封格式，供启动器 Rust 命令消费，
// 用于决定普通/强制更新弹窗以及更新完成后的 changelog 展示）。
func (h *Handler) handleLauncherUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	if h.players == nil {
		h.writeError(w, http.StatusNotFound, 4004, "暂无发布版本")
		return
	}
	release, err := h.players.LatestLauncherRelease(r.Context())
	if err != nil {
		h.writeRepositoryError(w, "query latest launcher release", err)
		return
	}
	if release.Version == "" {
		h.writeError(w, http.StatusNotFound, 4004, "暂无发布版本")
		return
	}
	h.writeSuccess(w, map[string]any{
		"version":   release.Version,
		"mandatory": release.Mandatory,
		"changelog": release.Changelog,
		"pubDate":   release.PubDate.UTC().Format(time.RFC3339),
	})
}

// handleLauncherUpdateManifest 按 Tauri updater 静态清单格式输出裸 JSON（不能套业务信封），
// 当前版本已是最新（或更高）时返回 204 No Content。
func (h *Handler) handleLauncherUpdateManifest(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	if h.players == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	release, err := h.players.LatestLauncherRelease(r.Context())
	if err != nil || release.Version == "" {
		if err != nil {
			h.logger.Error("query latest launcher release", "error", err)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	current := strings.TrimSpace(r.URL.Query().Get("current_version"))
	if compareVersions(normalizeVersion(current), normalizeVersion(release.Version)) >= 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	target := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("target")))
	if target == "" {
		target = "windows"
	}
	arch := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("arch")))
	if arch == "" {
		arch = "x86_64"
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{
		"version":  release.Version,
		"notes":    release.Changelog,
		"pub_date": release.PubDate.UTC().Format(time.RFC3339),
		"platforms": map[string]any{
			target + "-" + arch: map[string]string{
				"signature": release.Signature,
				"url":       release.ArtifactURL,
			},
		},
	}); err != nil {
		h.logger.Error("write launcher update manifest", "error", err)
	}
}

func (h *Handler) handleInventory(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}
	items, err := h.players.Inventory(r.Context(), steamID)
	if err != nil {
		h.logger.Error("query player inventory", "error", err)
		h.writeError(w, http.StatusInternalServerError, 5001, "读取真实库存失败")
		return
	}
	h.writeSuccess(w, items)
}

func (h *Handler) handleEquipment(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}
	if h.equipment == nil {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "装备配置服务尚未配置")
		return
	}
	profile, err := h.equipment.Load(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load player equipment", "error", err)
		h.writeError(w, http.StatusBadGateway, 5002, "读取游戏内装备配置失败")
		return
	}
	h.writeSuccess(w, profile)
}

func (h *Handler) handleEquip(w http.ResponseWriter, r *http.Request) {
	h.handleEquipmentMutation(w, r, true)
}

func (h *Handler) handleUnequip(w http.ResponseWriter, r *http.Request) {
	h.handleEquipmentMutation(w, r, false)
}

func (h *Handler) handleEquipmentMutation(w http.ResponseWriter, r *http.Request, equip bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}
	if h.equipment == nil {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "装备配置服务尚未配置")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var request equipmentMutationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "装备请求格式无效")
		return
	}
	modes, err := validateEquipmentModes(request.Modes)
	if err != nil {
		h.logger.Warn("equipment mutation rejected", "equip", equip, "product_id", request.ProductID, "reason", err.Error())
		h.writeError(w, http.StatusBadRequest, 4001, err.Error())
		return
	}
	team := strings.ToLower(strings.TrimSpace(request.Team))
	if team != "all" && team != "ct" && team != "t" {
		h.logger.Warn("equipment mutation rejected", "equip", equip, "product_id", request.ProductID, "reason", "invalid team")
		h.writeError(w, http.StatusBadRequest, 4001, "阵营仅支持 all、ct 或 t")
		return
	}

	items, err := h.players.Inventory(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load inventory before equipment mutation", "error", err)
		h.writeError(w, http.StatusServiceUnavailable, 5002, "校验玩家库存失败")
		return
	}
	item, ok := findInventoryItem(items, request.ProductID)
	if !ok || item.Source != "starlight" {
		h.writeError(w, http.StatusForbidden, 4003, "该物品不在当前玩家的有效星光库存中")
		return
	}
	mutation, err := buildEquipmentMutation(item, modes, team, equip)
	if err != nil {
		h.logger.Warn("equipment mutation rejected", "equip", equip, "product_id", request.ProductID, "modes", modes, "item_mode", item.Mode, "item_type", item.Type, "reason", err.Error())
		h.writeError(w, http.StatusBadRequest, 4001, err.Error())
		return
	}
	profile, err := h.equipment.Apply(r.Context(), steamID, mutation)
	if err != nil {
		h.logger.Error("apply player equipment", "equip", equip, "product_id", request.ProductID, "error", err)
		h.writeError(w, http.StatusBadGateway, 5002, "同步游戏内装备配置失败，原配置已尽力恢复")
		return
	}
	h.notifyGameServers(steamID, "player.reload_prefs", mutation.Modes)
	h.writeSuccess(w, profile)
}

func (h *Handler) handleStardustEquip(w http.ResponseWriter, r *http.Request) {
	h.handleStardustEquipment(w, r, true)
}

func (h *Handler) handleStardustUnequip(w http.ResponseWriter, r *http.Request) {
	h.handleStardustEquipment(w, r, false)
}

func (h *Handler) handleStarlightPurchase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request starlightPurchaseRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.PricingID <= 0 {
		h.writeError(w, http.StatusBadRequest, 4001, "购买请求格式无效")
		return
	}

	starlight, err := h.players.PurchaseStarlight(r.Context(), steamID, request.PricingID)
	if err != nil {
		switch {
		case errors.Is(err, mysqlrepo.ErrPricingNotFound):
			h.writeBusinessError(w, 4004, "该商品不可用或已下架")
		case errors.Is(err, mysqlrepo.ErrInsufficientStarlight):
			h.writeBusinessError(w, 4002, "星光余额不足")
		case errors.Is(err, mysqlrepo.ErrProductAlreadyOwned):
			h.writeBusinessError(w, 4002, "已拥有该物品，无需重复购买")
		case errors.Is(err, mysqlrepo.ErrPermanentVersionOwned):
			h.writeBusinessError(w, 4002, "已拥有永久版本，无需购买期限档位")
		case errors.Is(err, mysqlrepo.ErrPlayerNotFound):
			h.writeBusinessError(w, 4004, "玩家账号不存在")
		default:
			h.logger.Error("purchase starlight product", "pricing_id", request.PricingID, "error", err)
			h.writeError(w, http.StatusBadGateway, 5002, "购买失败，请稍后重试")
		}
		return
	}

	// 事务已提交：后续刷新失败也必须返回购买成功，避免客户端重试导致重复扣款发货。
	result := domain.StarlightPurchaseResult{
		Starlight:       starlight,
		Inventory:       []domain.InventoryItem{},
		PurchaseHistory: []domain.PurchaseHistoryItem{},
		StoreItems:      []domain.StoreItem{},
		RefreshComplete: true,
	}
	inventory, err := h.players.Inventory(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load inventory after purchase", "error", err)
		result.RefreshComplete = false
	} else {
		result.Inventory = inventory
	}
	history, err := h.players.PurchaseHistory(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load purchase history after purchase", "error", err)
		result.RefreshComplete = false
	} else {
		result.PurchaseHistory = history
	}
	storeItems, err := h.players.StoreItemsForPlayer(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load store items after purchase", "error", err)
		result.RefreshComplete = false
	} else {
		result.StoreItems = storeItems
	}
	if result.RefreshComplete {
		h.writeSuccess(w, result)
		return
	}
	h.writeJSON(w, http.StatusOK, envelope{
		Code: successCode,
		Msg:  "购买成功，部分展示数据刷新失败，请稍后重新打开库存或商城",
		Data: result,
	})
}

func (h *Handler) handleStardustPurchase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request stardustPurchaseRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "购买请求格式无效")
		return
	}
	request.ItemType = strings.TrimSpace(request.ItemType)
	request.UniqueID = strings.TrimSpace(request.UniqueID)
	if request.ItemType == "" || request.UniqueID == "" {
		h.writeError(w, http.StatusBadRequest, 4001, "购买请求缺少物品标识")
		return
	}

	stardust, err := h.players.PurchaseStardust(r.Context(), steamID, request.ItemType, request.UniqueID)
	if err != nil {
		switch {
		case errors.Is(err, mysqlrepo.ErrPricingNotFound):
			h.writeBusinessError(w, 4004, "该商品不可用或已下架")
		case errors.Is(err, mysqlrepo.ErrInsufficientStardust):
			h.writeBusinessError(w, 4002, "星尘余额不足")
		case errors.Is(err, mysqlrepo.ErrProductAlreadyOwned):
			h.writeBusinessError(w, 4002, "已拥有该物品，无需重复购买")
		default:
			h.logger.Error("purchase stardust item", "type", request.ItemType, "unique_id", request.UniqueID, "error", err)
			h.writeError(w, http.StatusBadGateway, 5002, "购买失败，请稍后重试")
		}
		return
	}

	result := domain.StardustPurchaseResult{
		Stardust:        stardust,
		Inventory:       []domain.InventoryItem{},
		StoreItems:      []domain.StoreItem{},
		RefreshComplete: true,
	}
	inventory, err := h.players.Inventory(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load inventory after stardust purchase", "error", err)
		result.RefreshComplete = false
	} else {
		result.Inventory = inventory
	}
	storeItems, err := h.players.StoreItemsForPlayer(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load store items after stardust purchase", "error", err)
		result.RefreshComplete = false
	} else {
		result.StoreItems = storeItems
	}
	if result.RefreshComplete {
		h.writeSuccess(w, result)
		return
	}
	h.writeJSON(w, http.StatusOK, envelope{
		Code: successCode,
		Msg:  "购买成功，部分展示数据刷新失败，请稍后重新打开库存或商城",
		Data: result,
	})
}

func (h *Handler) handleStardustEquipment(w http.ResponseWriter, r *http.Request, equip bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request stardustEquipmentRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "星尘装备请求格式无效")
		return
	}
	request.ItemType = strings.TrimSpace(request.ItemType)
	request.UniqueID = strings.TrimSpace(request.UniqueID)
	if request.ItemType == "" || request.UniqueID == "" {
		h.writeError(w, http.StatusBadRequest, 4001, "星尘装备请求缺少物品标识")
		return
	}

	var err error
	if equip {
		err = h.players.EquipStardust(r.Context(), steamID, request.ItemType, request.UniqueID)
	} else {
		err = h.players.UnequipStardust(r.Context(), steamID, request.ItemType, request.UniqueID)
	}
	if err != nil {
		h.logger.Error("apply stardust equipment", "equip", equip, "type", request.ItemType, "unique_id", request.UniqueID, "error", err)
		switch {
		case errors.Is(err, mysqlrepo.ErrStardustItemNotOwned):
			h.writeError(w, http.StatusForbidden, 4003, "该物品不在当前玩家的有效星尘库存中")
		case errors.Is(err, mysqlrepo.ErrChallengeDBUnavailable):
			h.writeError(w, http.StatusServiceUnavailable, 5003, "星尘装备服务尚未配置")
		default:
			h.writeError(w, http.StatusBadGateway, 5002, "同步星尘装备配置失败")
		}
		return
	}

	equipments, err := h.players.StardustEquipments(r.Context(), steamID)
	if err != nil {
		h.logger.Error("load stardust equipments", "error", err)
		h.writeError(w, http.StatusBadGateway, 5002, "读取星尘装备配置失败")
		return
	}
	h.notifyGameServers(steamID, "player.reload_stardust", nil)
	h.writeSuccess(w, equipments)
}

// notifyGameServers best-effort asks connected game servers to reload player state.
// modes filters by reported WS mode; empty/nil modes targets every connected server.
// Failures are logged only — ClientPrefs / DB writes already succeeded.
func (h *Handler) notifyGameServers(steamID uint64, command string, modes []string) {
	if h.gameWS == nil || !h.gameWS.Enabled() {
		h.logger.Info("skip game command notify", "command", command, "steamId", steamID, "reason", "hub_disabled")
		return
	}

	targets := make([]gamews.ServerInfo, 0)
	seen := make(map[string]struct{})
	add := func(servers []gamews.ServerInfo) {
		for _, server := range servers {
			if _, ok := seen[server.ServerID]; ok {
				continue
			}
			seen[server.ServerID] = struct{}{}
			targets = append(targets, server)
		}
	}
	if len(modes) == 0 {
		add(h.gameWS.ListServers())
	} else {
		for _, mode := range modes {
			matched := h.gameWS.ListServersByMode(mode)
			if len(matched) == 0 {
				h.logger.Info("no connected game server for mode",
					"command", command,
					"steamId", steamID,
					"mode", mode,
				)
			}
			add(matched)
		}
	}
	if len(targets) == 0 {
		h.logger.Info("skip game command notify",
			"command", command,
			"steamId", steamID,
			"modes", modes,
			"reason", "no_matching_connected_servers",
		)
		return
	}

	payload := map[string]string{"steamId": strconv.FormatUint(steamID, 10)}
	for _, server := range targets {
		server := server
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			h.logger.Info("game command notify dispatching",
				"command", command,
				"steamId", steamID,
				"serverId", server.ServerID,
				"mode", server.Mode,
			)
			result, err := h.gameWS.SendCommand(ctx, server.ServerID, command, payload)
			if err != nil {
				h.logger.Warn("game command notify failed",
					"command", command,
					"steamId", steamID,
					"serverId", server.ServerID,
					"mode", server.Mode,
					"error", err,
				)
				return
			}
			if !result.OK {
				h.logger.Warn("game command notify rejected",
					"command", command,
					"steamId", steamID,
					"serverId", server.ServerID,
					"mode", server.Mode,
					"error", result.Error,
				)
				return
			}
			h.logger.Info("game command notify succeeded",
				"command", command,
				"steamId", steamID,
				"serverId", server.ServerID,
				"mode", server.Mode,
			)
		}()
	}
}

func validateEquipmentModes(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > len(domain.EquipmentModes) {
		return nil, errors.New("至少选择一个有效服务器模式")
	}
	allowed := make(map[string]struct{}, len(domain.EquipmentModes))
	for _, mode := range domain.EquipmentModes {
		allowed[mode] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	modes := make([]string, 0, len(values))
	for _, value := range values {
		mode := strings.ToUpper(strings.TrimSpace(value))
		if _, ok := allowed[mode]; !ok {
			return nil, fmt.Errorf("不支持服务器模式 %q", value)
		}
		if _, duplicate := seen[mode]; duplicate {
			continue
		}
		seen[mode] = struct{}{}
		modes = append(modes, mode)
	}
	return modes, nil
}

func findInventoryItem(items []domain.InventoryItem, productID int64) (domain.InventoryItem, bool) {
	for _, item := range items {
		if item.ProductID == productID {
			return item, true
		}
	}
	return domain.InventoryItem{}, false
}

func buildEquipmentMutation(item domain.InventoryItem, modes []string, team string, equip bool) (domain.EquipmentMutation, error) {
	if item.UseLimit == 0 {
		return domain.EquipmentMutation{}, errors.New("该物品当前已被禁用")
	}
	for _, mode := range modes {
		if !productModeIsAllowed(item.Mode, mode) {
			return domain.EquipmentMutation{}, fmt.Errorf("该物品不能用于 %s 模式", mode)
		}
	}
	mutation := domain.EquipmentMutation{
		ProductID: item.ProductID,
		Modes:     modes,
		Team:      team,
		Equip:     equip,
	}
	switch item.Type {
	case "角色外观", "玩家外观":
		mutation.Slot = "player"
	case "武器外观":
		if item.WeaponType == "" || item.WeaponPrefab == "" {
			return domain.EquipmentMutation{}, errors.New("该武器外观缺少 weapon_type 或 prefab 配置")
		}
		mutation.Slot = "weapon"
		mutation.Team = "all"
		mutation.WeaponType = item.WeaponType
		mutation.WeaponPrefab = item.WeaponPrefab
		if item.UseLimit == 7 {
			ownerID, err := strconv.ParseInt(strings.TrimSpace(item.UseLimitInfo), 10, 64)
			if err != nil || ownerID <= 0 {
				return domain.EquipmentMutation{}, errors.New("该专属武器缺少关联角色商品 ID")
			}
			mutation.ExclusiveFor = strconv.FormatInt(ownerID, 10)
		}
	default:
		return domain.EquipmentMutation{}, errors.New("该物品不是可装备外观")
	}
	return mutation, nil
}

func productModeIsAllowed(expression, mode string) bool {
	// 空 mode 与 COALESCE(..., 'ALL') / 启动器前端 (mode || "ALL") 对齐，视为全模式可用。
	expression = strings.TrimSpace(expression)
	if expression == "" {
		expression = "ALL"
	}
	parts := strings.SplitN(expression, "#", 2)
	allowed := strings.TrimSpace(parts[0])
	disallowed := ""
	if len(parts) == 2 {
		disallowed = strings.TrimSpace(parts[1])
	}
	if disallowed == "" {
		return strings.Contains(allowed, "ALL") || strings.Contains(allowed, mode)
	}
	return !strings.Contains(disallowed, mode)
}

func (h *Handler) handleVerifyPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
		return
	}
	steamID, token, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request verifyPasswordRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "密码复验请求格式无效")
		return
	}
	r.Header.Set(reauthHeader, request.Password)
	if !h.verifyOperationPassword(w, r, steamID, token) {
		return
	}
	h.writeSuccess(w, map[string]bool{"valid": true})
}

func (h *Handler) handleGamePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
		return
	}
	if h.players == nil {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "玩家数据库尚未配置")
		return
	}
	providedKey := r.Header.Get(gameAPIKeyHeader)
	if h.gameAPIKey == "" {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "游戏服密码接口尚未配置")
		return
	}
	if subtle.ConstantTimeCompare([]byte(providedKey), []byte(h.gameAPIKey)) != 1 {
		h.writeError(w, http.StatusUnauthorized, 4003, "游戏服接口认证失败")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var request gamePasswordRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "改密请求格式无效")
		return
	}
	steamID, err := parseSteamID(request.SteamID)
	if err != nil {
		h.writeBusinessError(w, 4001, "Steam64 格式无效")
		return
	}
	if err := passwordauth.Validate(request.NewPassword); err != nil {
		h.writeBusinessError(w, 4001, "新密码须为 8 至 128 个 UTF-8 字节")
		return
	}
	if !request.IdentityValidated {
		h.writeBusinessError(w, 4003, "游戏内身份尚未完成校验")
		return
	}

	_, err = h.players.GamePasswordHash(r.Context(), steamID)
	if err != nil {
		if errors.Is(err, mysqlrepo.ErrPlayerNotFound) {
			h.writeBusinessError(w, 4004, "玩家账号不存在")
			return
		}
		h.writeRepositoryError(w, "query current game password", err)
		return
	}
	encoded, err := passwordauth.Hash(r.Context(), request.NewPassword)
	if err != nil {
		if errors.Is(err, passwordauth.ErrBusy) {
			h.writeError(w, http.StatusServiceUnavailable, authBusyCode, "认证服务繁忙，请稍后重试")
			return
		}
		h.logger.Error("hash new game password", "error", err)
		h.writeError(w, http.StatusInternalServerError, 5000, "生成密码摘要失败")
		return
	}
	if err := h.players.UpdateGamePasswordHash(r.Context(), steamID, encoded); err != nil {
		if errors.Is(err, mysqlrepo.ErrPlayerNotFound) {
			h.writeBusinessError(w, 4004, "玩家账号不存在")
			return
		}
		h.writeRepositoryError(w, "update game password", err)
		return
	}
	h.writeSuccess(w, map[string]bool{"updated": true})
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
		return
	}
	if h.players == nil {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "真实库存数据库尚未配置")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var request loginRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "登录请求格式无效")
		return
	}
	steamID, err := parseSteamID(request.SteamID)
	if err != nil || (!h.skipPasswordAuth && strings.TrimSpace(request.Password) == "") {
		h.writeError(w, http.StatusBadRequest, 4001, "Steam64 或密码格式无效")
		return
	}

	if !h.skipPasswordAuth {
		if !h.authenticateWithLimits(w, r, steamID, request.Password, func() {
			h.writeError(w, http.StatusUnauthorized, invalidCredentialsCode, "Steam64 或游戏内密码错误")
		}) {
			return
		}
	}
	readModel, err := h.players.PlayerReadModel(r.Context(), steamID)
	if err != nil {
		h.logger.Error("query player read model after login", "error", err)
		h.writeError(w, http.StatusInternalServerError, 5001, "读取真实玩家数据失败")
		return
	}

	token, err := newSessionToken()
	if err != nil {
		h.logger.Error("create login session", "error", err)
		h.writeError(w, http.StatusInternalServerError, 5000, "创建登录会话失败")
		return
	}
	expiresAt := time.Now().Add(24 * time.Hour)
	h.sessionMu.Lock()
	h.sessions[token] = session{steamID: steamID, expiresAt: expiresAt}
	h.sessionMu.Unlock()
	h.writeSuccess(w, loginResponse{Token: token, ExpiresAt: expiresAt.UTC().Format(time.RFC3339), PlayerReadModel: readModel})
}

func (h *Handler) writeRepositoryError(w http.ResponseWriter, operation string, err error) {
	h.logger.Error(operation, "error", err)
	h.writeError(w, http.StatusServiceUnavailable, 5002, "只读数据服务暂时不可用")
}

func (h *Handler) requireSession(w http.ResponseWriter, r *http.Request) (uint64, string, bool) {
	if h.players == nil {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "真实库存数据库尚未配置")
		return 0, "", false
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		h.writeError(w, http.StatusUnauthorized, sessionExpiredCode, "请先登录")
		return 0, "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	h.sessionMu.Lock()
	current, exists := h.sessions[token]
	if exists && time.Now().After(current.expiresAt) {
		delete(h.sessions, token)
		exists = false
	}
	h.sessionMu.Unlock()
	if !exists {
		h.writeError(w, http.StatusUnauthorized, sessionExpiredCode, "登录会话无效或已过期")
		return 0, "", false
	}
	return current.steamID, token, true
}

func (h *Handler) verifyOperationPassword(w http.ResponseWriter, r *http.Request, steamID uint64, token string) bool {
	if h.skipPasswordAuth {
		return true
	}
	password := r.Header.Get(reauthHeader)
	if password == "" {
		h.writeError(w, http.StatusBadRequest, 4001, "此操作需要再次校验游戏内密码")
		return false
	}
	return h.authenticateWithLimits(w, r, steamID, password, func() {
		h.sessionMu.Lock()
		delete(h.sessions, token)
		h.sessionMu.Unlock()
		h.writeError(w, http.StatusUnauthorized, credentialsStaleCode, "密码不正确或已变更，请重新登录")
	})
}

func (h *Handler) authenticateWithLimits(w http.ResponseWriter, r *http.Request, steamID uint64, password string, onInvalidCredentials func()) bool {
	ip := clientIP(r)
	now := time.Now()
	if !h.authLimiter.allow(ip, steamID, now) {
		h.writeError(w, http.StatusTooManyRequests, authRateLimitedCode, "登录尝试过于频繁，请稍后再试")
		return false
	}
	h.authLimiter.recordAttempt(ip, now)

	if err := h.players.Authenticate(r.Context(), steamID, password); err != nil {
		if errors.Is(err, mysqlrepo.ErrInvalidCredentials) {
			h.authLimiter.recordFailure(ip, steamID, time.Now())
			onInvalidCredentials()
			return false
		}
		if errors.Is(err, passwordauth.ErrBusy) {
			h.writeError(w, http.StatusServiceUnavailable, authBusyCode, "认证服务繁忙，请稍后重试")
			return false
		}
		h.logger.Error("authenticate player", "error", err)
		h.writeError(w, http.StatusServiceUnavailable, 5002, "账号数据库暂时不可用")
		return false
	}
	return true
}

func parseSteamID(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if len(value) != 17 || !strings.HasPrefix(value, "7656119") {
		return 0, errors.New("invalid Steam64")
	}
	return strconv.ParseUint(value, 10, 64)
}

func newSessionToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (h *Handler) requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	h.writeError(w, http.StatusMethodNotAllowed, 4005, "请求方法不支持")
	return false
}

func (h *Handler) writeSuccess(w http.ResponseWriter, data any) {
	h.writeJSON(w, http.StatusOK, envelope{Code: successCode, Msg: "success", Data: data})
}

func (h *Handler) writeError(w http.ResponseWriter, status, code int, message string) {
	h.writeJSON(w, status, envelope{Code: code, Msg: message, Data: nil})
}

func (h *Handler) writeBusinessError(w http.ResponseWriter, code int, message string) {
	h.writeError(w, http.StatusOK, code, message)
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		h.logger.Error("write json response", "error", err)
	}
}

func (h *Handler) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if _, ok := h.allowedOrigins[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, "+reauthHeader)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response does not implement http.Hijacker")
	}
	return hijacker.Hijack()
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (h *Handler) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		h.logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration", time.Since(started),
		)
	})
}
