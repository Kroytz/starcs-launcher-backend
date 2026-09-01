package gamews

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrNotConnected = errors.New("game server is not connected")
	ErrTimeout      = errors.New("game server command timed out")
	ErrClosed       = errors.New("game server connection closed")
	ErrHubDisabled  = errors.New("game websocket hub is disabled")
)

const (
	defaultReadIdle  = 60 * time.Second
	defaultWriteWait = 10 * time.Second
	maxMessageBytes  = 64 << 10
	maxPendingCmds   = 64
	maxConnections   = 256
	sendQueueSize    = 32
	apiKeyHeader     = "X-Star-Api-Key"
)

type ServerInfo struct {
	ServerID  string    `json:"serverId"`
	Mode      string    `json:"mode,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	Connected time.Time `json:"connectedAt"`
}

type CommandResult struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type Hub struct {
	logger   *slog.Logger
	apiKey   string
	mu       sync.Mutex
	servers  map[string]*Conn
	upgrader websocket.Upgrader
}

func NewHub(logger *slog.Logger, apiKey string) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		logger:  logger,
		apiKey:  apiKey,
		servers: make(map[string]*Conn),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}
}

func (h *Hub) Enabled() bool {
	return h != nil && h.apiKey != ""
}

func (h *Hub) ListServers() []ServerInfo {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ServerInfo, 0, len(h.servers))
	for _, conn := range h.servers {
		out = append(out, conn.info())
	}
	return out
}

// ListServersByMode returns currently connected servers whose reported mode matches
// (case-insensitive). Servers that connected without a mode are never matched.
func (h *Hub) ListServersByMode(mode string) []ServerInfo {
	if h == nil {
		return nil
	}
	mode = strings.ToUpper(strings.TrimSpace(mode))
	if mode == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ServerInfo, 0)
	for _, conn := range h.servers {
		if strings.EqualFold(strings.TrimSpace(conn.mode), mode) {
			out = append(out, conn.info())
		}
	}
	return out
}

func (h *Hub) SendCommand(ctx context.Context, serverID, name string, payload any) (CommandResult, error) {
	if !h.Enabled() {
		return CommandResult{}, ErrHubDisabled
	}
	serverID = normalizeServerID(serverID)
	if serverID == "" {
		return CommandResult{}, fmt.Errorf("serverId is required")
	}
	if name == "" {
		return CommandResult{}, fmt.Errorf("command name is required")
	}

	h.mu.Lock()
	conn := h.servers[serverID]
	connected := make([]string, 0, len(h.servers))
	for id := range h.servers {
		connected = append(connected, id)
	}
	h.mu.Unlock()
	if conn == nil {
		h.logger.Warn("game websocket command skipped: server not connected",
			"serverId", serverID,
			"name", name,
			"connectedServers", connected,
		)
		return CommandResult{}, ErrNotConnected
	}
	return conn.sendCommand(ctx, name, payload)
}

func (h *Hub) register(conn *Conn) (previous *Conn, accepted bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous = h.servers[conn.serverID]
	if previous == nil && len(h.servers) >= maxConnections {
		return nil, false
	}
	h.servers[conn.serverID] = conn
	return previous, true
}

func (h *Hub) unregister(conn *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	current, ok := h.servers[conn.serverID]
	if ok && current == conn {
		delete(h.servers, conn.serverID)
	}
}

func newCommandID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
