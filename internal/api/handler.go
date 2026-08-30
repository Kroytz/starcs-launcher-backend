package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/starcs/star-launcher-backend/internal/demo"
	"github.com/starcs/star-launcher-backend/internal/domain"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
	"github.com/starcs/star-launcher-backend/internal/passwordauth"
)

const (
	successCode          = 2000
	reauthHeader         = "X-StarCS-Reauth"
	gameAPIKeyHeader     = "X-Star-Api-Key"
	credentialsStaleCode = 4011
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
	Announcements(ctx context.Context) ([]domain.Announcement, error)
	StoreItems(ctx context.Context) ([]domain.StoreItem, error)
	Maps(ctx context.Context) ([]domain.MapResource, error)
	WorkshopPacks(ctx context.Context, mode string) ([]domain.WorkshopPack, error)
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
	mux.HandleFunc("/api/v1/announcements", h.handleAnnouncements)
	mux.HandleFunc("/api/v1/store/items", h.handleStoreItems)
	mux.HandleFunc("/api/v1/maps", h.handleMaps)
	mux.HandleFunc("/api/v1/workshop-packs", h.handleWorkshopPacks)
	mux.HandleFunc("/api/v1/auth/login", h.handleLogin)
	mux.HandleFunc("/api/v1/auth/verify", h.handleVerifyPassword)
	mux.HandleFunc("/api/v1/me", h.handleAccount)
	mux.HandleFunc("/api/v1/me/inventory", h.handleInventory)
	mux.HandleFunc("/api/v1/me/equipment", h.handleEquipment)
	mux.HandleFunc("/api/v1/me/equipment/equip", h.handleEquip)
	mux.HandleFunc("/api/v1/me/equipment/unequip", h.handleUnequip)
	mux.HandleFunc("/internal/v1/game-password", h.handleGamePassword)

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

func (h *Handler) handleAnnouncements(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	h.writeSuccess(w, h.store.Announcements())
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

func (h *Handler) handleAccount(w http.ResponseWriter, r *http.Request) {
	if !h.requireGET(w, r) {
		return
	}
	h.writeSuccess(w, h.store.Account())
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
		h.writeError(w, http.StatusBadRequest, 4001, err.Error())
		return
	}
	team := strings.ToLower(strings.TrimSpace(request.Team))
	if team != "all" && team != "ct" && team != "t" {
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
		h.writeError(w, http.StatusBadRequest, 4001, err.Error())
		return
	}
	profile, err := h.equipment.Apply(r.Context(), steamID, mutation)
	if err != nil {
		h.logger.Error("apply player equipment", "equip", equip, "product_id", request.ProductID, "error", err)
		h.writeError(w, http.StatusBadGateway, 5002, "同步游戏内装备配置失败，原配置已尽力恢复")
		return
	}
	h.writeSuccess(w, profile)
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
	parts := strings.SplitN(expression, "#", 2)
	allowed := parts[0]
	disallowed := ""
	if len(parts) == 2 {
		disallowed = parts[1]
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
	encoded, err := passwordauth.Hash(request.NewPassword)
	if err != nil {
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
		if err := h.players.Authenticate(r.Context(), steamID, request.Password); err != nil {
			if errors.Is(err, mysqlrepo.ErrInvalidCredentials) {
				h.writeError(w, http.StatusUnauthorized, 4003, "Steam64 或游戏内密码错误")
				return
			}
			h.logger.Error("authenticate player", "error", err)
			h.writeError(w, http.StatusServiceUnavailable, 5002, "账号数据库暂时不可用")
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
		h.writeError(w, http.StatusUnauthorized, 4003, "请先登录")
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
		h.writeError(w, http.StatusUnauthorized, 4003, "登录会话无效或已过期")
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
	if err := h.players.Authenticate(r.Context(), steamID, password); err != nil {
		if errors.Is(err, mysqlrepo.ErrInvalidCredentials) {
			h.sessionMu.Lock()
			delete(h.sessions, token)
			h.sessionMu.Unlock()
			h.writeError(w, http.StatusUnauthorized, credentialsStaleCode, "游戏内密码已变更，请重新登录")
			return false
		}
		h.logger.Error("reauthenticate player operation", "error", err)
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
