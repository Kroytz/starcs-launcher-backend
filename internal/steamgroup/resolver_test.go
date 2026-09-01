package steamgroup

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolverPaginatesCachesAndFallsBackToStaleMembership(t *testing.T) {
	const member = uint64(76561198000000003)
	var requests atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if fail.Load() {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("p"))
		if page <= 1 {
			_, _ = io.WriteString(w, `<memberList><groupDetails><memberCount>3</memberCount></groupDetails><members><steamID64>76561198000000001</steamID64><steamID64>76561198000000002</steamID64></members></memberList>`)
			return
		}
		_, _ = io.WriteString(w, `<memberList><groupDetails><memberCount>3</memberCount></groupDetails><members><steamID64>76561198000000003</steamID64></members></memberList>`)
	}))
	defer server.Close()

	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	resolver := newResolver(server.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute, server.URL+"/gid/", func() time.Time { return now })
	if !resolver.IsMember(context.Background(), 42, member, 10) {
		t.Fatal("expected member from the second page to be allowed")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected two paginated requests, got %d", got)
	}
	if !resolver.IsMember(context.Background(), 42, member, 10) {
		t.Fatal("expected cached membership to remain allowed")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("fresh cache should avoid HTTP requests, got %d", got)
	}

	now = now.Add(2 * time.Minute)
	fail.Store(true)
	if !resolver.IsMember(context.Background(), 42, member, 10) {
		t.Fatal("expected stale membership to survive a temporary Steam failure")
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("expired cache should attempt one refresh, got %d requests", got)
	}
}

func TestResolverRejectsGroupAboveConfiguredMemberLimit(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `<memberList><groupDetails><memberCount>20</memberCount></groupDetails><members><steamID64>76561198000000001</steamID64></members></memberList>`)
	}))
	defer server.Close()

	resolver := newResolver(server.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute, server.URL+"/gid/", time.Now)
	if resolver.IsMember(context.Background(), 42, 76561198000000001, 10) {
		t.Fatal("group above the product member limit must be rejected")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("member-limit rejection should not fetch more pages, got %d requests", got)
	}
}
