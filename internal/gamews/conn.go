package gamews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Conn struct {
	hub        *Hub
	ws         *websocket.Conn
	serverID   string
	mode       string
	hostname   string
	connected  time.Time
	send       chan Envelope
	pendingMu  sync.Mutex
	pending    map[string]chan CommandResult
	closeOnce  sync.Once
	closed     chan struct{}
}

func newConn(hub *Hub, ws *websocket.Conn, serverID, mode, hostname string) *Conn {
	return &Conn{
		hub:       hub,
		ws:        ws,
		serverID:  serverID,
		mode:      mode,
		hostname:  hostname,
		connected: time.Now().UTC(),
		send:      make(chan Envelope, sendQueueSize),
		pending:   make(map[string]chan CommandResult),
		closed:    make(chan struct{}),
	}
}

func (c *Conn) info() ServerInfo {
	return ServerInfo{
		ServerID:  c.serverID,
		Mode:      c.mode,
		Hostname:  c.hostname,
		Connected: c.connected,
	}
}

func (c *Conn) start() {
	go c.writePump()
	go c.readPump()
}

func (c *Conn) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.hub.unregister(c)
		c.failAllPending(ErrClosed)
		_ = c.ws.Close()
	})
}

func (c *Conn) failAllPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, waiter := range c.pending {
		waiter <- CommandResult{OK: false, Error: err.Error()}
		close(waiter)
		delete(c.pending, id)
	}
}

func (c *Conn) sendCommand(ctx context.Context, name string, payload any) (CommandResult, error) {
	id, err := newCommandID()
	if err != nil {
		return CommandResult{}, err
	}
	envelope, err := NewCommand(id, name, payload)
	if err != nil {
		return CommandResult{}, err
	}

	waiter := make(chan CommandResult, 1)
	c.pendingMu.Lock()
	if len(c.pending) >= maxPendingCmds {
		c.pendingMu.Unlock()
		return CommandResult{}, fmt.Errorf("too many pending commands for server %s", c.serverID)
	}
	c.pending[id] = waiter
	c.pendingMu.Unlock()

	c.hub.logger.Info("game websocket command queued",
		"serverId", c.serverID,
		"commandId", id,
		"name", name,
	)
	if err := c.enqueue(envelope); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		c.hub.logger.Warn("game websocket command enqueue failed",
			"serverId", c.serverID,
			"commandId", id,
			"name", name,
			"error", err,
		)
		return CommandResult{}, err
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.hub.logger.Warn("game websocket command timed out",
				"serverId", c.serverID,
				"commandId", id,
				"name", name,
			)
			return CommandResult{}, ErrTimeout
		}
		return CommandResult{}, ctx.Err()
	case <-c.closed:
		c.hub.logger.Warn("game websocket command aborted: connection closed",
			"serverId", c.serverID,
			"commandId", id,
			"name", name,
		)
		return CommandResult{}, ErrClosed
	case result := <-waiter:
		if result.OK {
			c.hub.logger.Info("game websocket command result",
				"serverId", c.serverID,
				"commandId", id,
				"name", name,
				"ok", true,
			)
		} else {
			c.hub.logger.Warn("game websocket command result",
				"serverId", c.serverID,
				"commandId", id,
				"name", name,
				"ok", false,
				"error", result.Error,
			)
		}
		return result, nil
	}
}

func (c *Conn) enqueue(envelope Envelope) error {
	select {
	case <-c.closed:
		return ErrClosed
	case c.send <- envelope:
		return nil
	default:
		return fmt.Errorf("send queue full for server %s", c.serverID)
	}
}

func (c *Conn) readPump() {
	defer c.close()
	c.ws.SetReadLimit(maxMessageBytes)
	_ = c.ws.SetReadDeadline(time.Now().Add(defaultReadIdle))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(defaultReadIdle))
	})

	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.hub.logger.Info("game websocket read ended", "serverId", c.serverID, "error", err)
			}
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(defaultReadIdle))

		var envelope Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			c.hub.logger.Warn("invalid game websocket frame", "serverId", c.serverID, "error", err)
			continue
		}
		c.handleIncoming(envelope)
	}
}

func (c *Conn) handleIncoming(envelope Envelope) {
	switch envelope.Type {
	case TypePing:
		_ = c.enqueue(NewPong())
	case TypePong:
		// application-level keepalive ack
	case TypeResult:
		c.resolvePending(envelope)
	case TypeEvent:
		c.hub.logger.Info("game websocket event",
			"serverId", c.serverID,
			"name", envelope.Name,
		)
	default:
		c.hub.logger.Warn("unknown game websocket message type",
			"serverId", c.serverID,
			"type", envelope.Type,
		)
	}
}

func (c *Conn) resolvePending(envelope Envelope) {
	if envelope.ID == "" {
		return
	}
	c.pendingMu.Lock()
	waiter, ok := c.pending[envelope.ID]
	if ok {
		delete(c.pending, envelope.ID)
	}
	c.pendingMu.Unlock()
	if !ok {
		return
	}
	okFlag := envelope.OK != nil && *envelope.OK
	waiter <- CommandResult{
		OK:      okFlag,
		Payload: envelope.Payload,
		Error:   envelope.Error,
	}
	close(waiter)
}

func (c *Conn) writePump() {
	defer c.close()
	for {
		select {
		case <-c.closed:
			return
		case envelope, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(defaultWriteWait))
			if err := c.ws.WriteJSON(envelope); err != nil {
				c.hub.logger.Warn("game websocket write failed",
					"serverId", c.serverID,
					"type", envelope.Type,
					"name", envelope.Name,
					"commandId", envelope.ID,
					"error", err,
				)
				return
			}
			if envelope.Type == TypeCommand {
				c.hub.logger.Info("game websocket command sent",
					"serverId", c.serverID,
					"commandId", envelope.ID,
					"name", envelope.Name,
				)
			}
		}
	}
}
