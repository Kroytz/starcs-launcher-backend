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
	"server.ping": {},
	"server.info": {},
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

	result, err := h.gameWS.SendCommand(ctx, serverID, request.Name, request.Payload)
	if err != nil {
		switch {
		case errors.Is(err, gamews.ErrNotConnected):
			h.writeError(w, http.StatusNotFound, 4004, "目标游戏服未连接")
		case errors.Is(err, gamews.ErrTimeout):
			h.writeError(w, http.StatusGatewayTimeout, 5005, "等待游戏服回执超时")
		case errors.Is(err, gamews.ErrClosed):
			h.writeError(w, http.StatusBadGateway, 5002, "游戏服连接已断开")
		default:
			h.logger.Error("dispatch game server command", "serverId", serverID, "name", request.Name, "error", err)
			h.writeError(w, http.StatusBadGateway, 5002, "下发游戏服指令失败")
		}
		return
	}

	h.logger.Info("game server command completed",
		"serverId", serverID,
		"name", request.Name,
		"ok", result.OK,
	)
	h.writeSuccess(w, map[string]any{
		"ok":      result.OK,
		"payload": result.Payload,
		"error":   result.Error,
	})
}
