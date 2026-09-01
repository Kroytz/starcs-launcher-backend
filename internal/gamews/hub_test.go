package gamews

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNormalizeServerID(t *testing.T) {
	if got := normalizeServerID(" zm-01 "); got != "zm-01" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeServerID("bad id"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestHubSendCommandRoundTrip(t *testing.T) {
	hub := NewHub(nil, "secret-key")
	server := httptest.NewServer(hub)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/?serverId=test-server&mode=ZM"
	header := http.Header{}
	header.Set(apiKeyHeader, "secret-key")
	client, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = client.Close() })

	// Drain accepted event.
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	var accepted Envelope
	if err := client.ReadJSON(&accepted); err != nil {
		t.Fatalf("read accepted: %v", err)
	}
	if accepted.Type != TypeEvent || accepted.Name != "server.accepted" {
		t.Fatalf("unexpected hello: %+v", accepted)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var envelope Envelope
			if err := client.ReadJSON(&envelope); err != nil {
				return
			}
			if envelope.Type != TypeCommand {
				continue
			}
			ok := true
			payload, _ := json.Marshal(map[string]any{"pong": true, "echo": envelope.Name})
			_ = client.WriteJSON(Envelope{
				V:       ProtocolVersion,
				Type:    TypeResult,
				ID:      envelope.ID,
				OK:      &ok,
				Payload: payload,
			})
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := hub.SendCommand(ctx, "test-server", "server.ping", map[string]any{})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok result: %+v", result)
	}

	_ = client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func TestHubSendCommandTimeout(t *testing.T) {
	hub := NewHub(nil, "secret-key")
	server := httptest.NewServer(hub)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/?serverId=slow-server"
	header := http.Header{}
	header.Set(apiKeyHeader, "secret-key")
	client, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = client.Close() })
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	var accepted Envelope
	_ = client.ReadJSON(&accepted)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = hub.SendCommand(ctx, "slow-server", "server.ping", nil)
	if err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestHubRejectsBadAPIKey(t *testing.T) {
	hub := NewHub(nil, "secret-key")
	server := httptest.NewServer(hub)
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/?serverId=test-server"
	header := http.Header{}
	header.Set(apiKeyHeader, "wrong")
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatal("expected dial failure")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
