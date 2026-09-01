package gamews

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	"github.com/gorilla/websocket"
)

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.Enabled() {
		http.Error(w, "game websocket is disabled", http.StatusServiceUnavailable)
		return
	}
	if !h.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	serverID := normalizeServerID(r.URL.Query().Get("serverId"))
	if serverID == "" {
		http.Error(w, "serverId is required", http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	hostname := strings.TrimSpace(r.URL.Query().Get("hostname"))

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("game websocket upgrade failed", "serverId", serverID, "error", err)
		return
	}

	conn := newConn(h, ws, serverID, mode, hostname)
	previous, accepted := h.register(conn)
	if !accepted {
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "too many connections"))
		_ = ws.Close()
		return
	}
	if previous != nil {
		h.logger.Info("game websocket replaced existing connection", "serverId", serverID)
		previous.close()
	}

	hello, err := NewEvent("server.accepted", map[string]any{"serverId": serverID})
	if err == nil {
		_ = conn.enqueue(hello)
	}
	h.logger.Info("game websocket connected", "serverId", serverID, "mode", mode, "hostname", hostname)
	conn.start()
}

func (h *Hub) authorize(r *http.Request) bool {
	provided := r.Header.Get(apiKeyHeader)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.apiKey)) == 1
}

func normalizeServerID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' || char == '-' || char == '.' {
			continue
		}
		return ""
	}
	return value
}

func DecodePayload[T any](raw json.RawMessage, out *T) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return json.Unmarshal(raw, out)
}
