package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/starcs/star-launcher-backend/internal/gamews"
)

const (
	defaultCommandTimeoutMs = 5000
	maxCommandTimeoutMs     = 30000
)

var allowedGameCommands = map[string]struct{}{
	"server.ping":             {},
	"server.info":             {},
	"player.reload_inventory": {},
	"player.reload_prefs":     {},
	"player.reload_stardust":  {},
}

type gameServerCommandRequest struct {
	Name      string          `json:"name"`
	Payload   json.RawMessage `json:"payload"`
	TimeoutMs int             `json:"timeoutMs"`
}

func (h *Handler) requireGameAPIKey(w http.ResponseWriter, r *http.Request) bool {
	if h.gameAPIKey == "" {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "游戏服控制面尚未配置")
		return false
	}
	providedKey := r.Header.Get(gameAPIKeyHeader)
	if subtle.ConstantTimeCompare([]byte(providedKey), []byte(h.gameAPIKey)) != 1 {
		h.writeError(w, http.StatusUnauthorized, 4003, "游戏服接口认证失败")
		return false
	}
	return true
}

func (h *Handler) handleListGameServers(w http.ResponseWriter, r *http.Request) {
	if !h.requireGameAPIKey(w, r) {
		return
	}
	if h.gameWS == nil {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "游戏服 WebSocket 控制面尚未配置")
		return
	}
	h.writeSuccess(w, map[string]any{
		"servers": h.gameWS.ListServers(),
	})
}

func (h *Handler) handleGameServerCommand(w http.ResponseWriter, r *http.Request) {
	if !h.requireGameAPIKey(w, r) {
		return
	}
	if h.gameWS == nil || !h.gameWS.Enabled() {
		h.writeError(w, http.StatusServiceUnavailable, 5003, "游戏服 WebSocket 控制面尚未配置")
		return
	}

	serverID := r.PathValue("serverId")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request gameServerCommandRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeError(w, http.StatusBadRequest, 4001, "指令请求格式无效")
		return
	}
	if _, ok := allowedGameCommands[request.Name]; !ok {
		h.writeBusinessError(w, 4001, "不支持的指令名称")
		return
	}

	timeoutMs := request.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = defaultCommandTimeoutMs
	}
	if timeoutMs > maxCommandTimeoutMs {
		timeoutMs = maxCommandTimeoutMs
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	h.logger.Info("game server command dispatching",
		"serverId", serverID,
		"name", request.Name,
		"timeoutMs", timeoutMs,
	)
	result, err := h.gameWS.SendCommand(ctx, serverID, request.Name, request.Payload)
	if err != nil {
		switch {
		case errors.Is(err, gamews.ErrNotConnected):
			h.logger.Warn("game server command failed", "serverId", serverID, "name", request.Name, "reason", "not_connected")
			h.writeError(w, http.StatusNotFound, 4004, "目标游戏服未连接")
		case errors.Is(err, gamews.ErrTimeout):
			h.logger.Warn("game server command failed", "serverId", serverID, "name", request.Name, "reason", "timeout", "timeoutMs", timeoutMs)
			h.writeError(w, http.StatusGatewayTimeout, 5005, "等待游戏服回执超时")
		case errors.Is(err, gamews.ErrClosed):
			h.logger.Warn("game server command failed", "serverId", serverID, "name", request.Name, "reason", "connection_closed")
			h.writeError(w, http.StatusBadGateway, 5002, "游戏服连接已断开")
		default:
			h.logger.Error("game server command failed", "serverId", serverID, "name", request.Name, "error", err)
			h.writeError(w, http.StatusBadGateway, 5002, "下发游戏服指令失败")
		}
		return
	}

	if result.OK {
		h.logger.Info("game server command succeeded",
			"serverId", serverID,
			"name", request.Name,
		)
	} else {
		h.logger.Warn("game server command rejected by game server",
			"serverId", serverID,
			"name", request.Name,
			"error", result.Error,
		)
	}
	h.writeSuccess(w, map[string]any{
		"ok":      result.OK,
		"payload": result.Payload,
		"error":   result.Error,
	})
}
