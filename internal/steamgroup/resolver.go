package steamgroup

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL  = "https://steamcommunity.com/gid/"
	maxResponseSize = 4 << 20
	maxPages        = 100
)

type groupSnapshot struct {
	fetchedAt   time.Time
	memberCount int
	members     map[uint64]struct{}
	complete    bool
}

type memberListXML struct {
	GroupDetails struct {
		MemberCount int `xml:"memberCount"`
	} `xml:"groupDetails"`
	Members struct {
		SteamIDs []string `xml:"steamID64"`
	} `xml:"members"`
}

// Resolver caches Steam Community group membership server-side. A stale
// snapshot remains usable when Steam Community is temporarily unavailable so
// an optional external dependency cannot break the whole launcher inventory.
type Resolver struct {
	client  *http.Client
	logger  *slog.Logger
	ttl     time.Duration
	baseURL string
	now     func() time.Time

	mu         sync.RWMutex
	cache      map[uint64]groupSnapshot
	groupLocks map[uint64]*sync.Mutex
}

func New(logger *slog.Logger, ttl time.Duration) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if request.URL.Scheme != "https" || !strings.EqualFold(request.URL.Hostname(), "steamcommunity.com") {
				return fmt.Errorf("refusing Steam group redirect to %s", request.URL.Redacted())
			}
			return nil
		},
	}
	return newResolver(client, logger, ttl, defaultBaseURL, time.Now)
}

func newResolver(client *http.Client, logger *slog.Logger, ttl time.Duration, baseURL string, now func() time.Time) *Resolver {
	return &Resolver{
		client:     client,
		logger:     logger,
		ttl:        ttl,
		baseURL:    baseURL,
		now:        now,
		cache:      make(map[uint64]groupSnapshot),
		groupLocks: make(map[uint64]*sync.Mutex),
	}
}

// IsMember reports whether steamID belongs to groupID and the group remains
// within the product's configured member limit.
func (r *Resolver) IsMember(ctx context.Context, groupID, steamID uint64, maxMembers int) bool {
	if groupID == 0 || steamID == 0 || maxMembers <= 0 {
		return false
	}
	if snapshot, ok := r.cached(groupID); ok && r.usable(snapshot, maxMembers) {
		return snapshotAllows(snapshot, steamID, maxMembers)
	}

	lock := r.lockFor(groupID)
	lock.Lock()
	defer lock.Unlock()

	if snapshot, ok := r.cached(groupID); ok && r.usable(snapshot, maxMembers) {
		return snapshotAllows(snapshot, steamID, maxMembers)
	}

	stale, hasStale := r.snapshot(groupID)
	fresh, err := r.fetch(ctx, groupID, maxMembers)
	if err != nil {
		r.logger.Warn("refresh Steam group membership failed", "groupID", groupID, "error", err)
		if hasStale {
			return snapshotAllows(stale, steamID, maxMembers)
		}
		return false
	}
	r.mu.Lock()
	r.cache[groupID] = fresh
	r.mu.Unlock()
	return snapshotAllows(fresh, steamID, maxMembers)
}

func (r *Resolver) cached(groupID uint64) (groupSnapshot, bool) {
	snapshot, ok := r.snapshot(groupID)
	if !ok || r.now().Sub(snapshot.fetchedAt) >= r.ttl {
		return groupSnapshot{}, false
	}
	return snapshot, true
}

func (r *Resolver) snapshot(groupID uint64) (groupSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot, ok := r.cache[groupID]
	return snapshot, ok
}

func (r *Resolver) usable(snapshot groupSnapshot, maxMembers int) bool {
	return snapshot.complete || snapshot.memberCount > maxMembers
}

func (r *Resolver) lockFor(groupID uint64) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock := r.groupLocks[groupID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.groupLocks[groupID] = lock
	}
	return lock
}

func (r *Resolver) fetch(ctx context.Context, groupID uint64, maxMembers int) (groupSnapshot, error) {
	snapshot := groupSnapshot{
		fetchedAt: r.now(),
		members:   make(map[uint64]struct{}),
	}
	for page := 1; page <= maxPages; page++ {
		payload, err := r.fetchPage(ctx, groupID, page)
		if err != nil {
			return groupSnapshot{}, err
		}
		if page == 1 {
			snapshot.memberCount = payload.GroupDetails.MemberCount
			if snapshot.memberCount < 0 {
				return groupSnapshot{}, fmt.Errorf("Steam returned a negative member count")
			}
		}
		before := len(snapshot.members)
		for _, raw := range payload.Members.SteamIDs {
			steamID, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
			if err == nil && steamID != 0 {
				snapshot.members[steamID] = struct{}{}
			}
		}
		if snapshot.memberCount > maxMembers {
			return snapshot, nil
		}
		if len(snapshot.members) >= snapshot.memberCount {
			snapshot.complete = true
			return snapshot, nil
		}
		if len(snapshot.members) == before {
			break
		}
	}
	return groupSnapshot{}, fmt.Errorf("Steam group member list is incomplete: got %d of %d", len(snapshot.members), snapshot.memberCount)
}

func (r *Resolver) fetchPage(ctx context.Context, groupID uint64, page int) (memberListXML, error) {
	endpoint, err := url.Parse(r.baseURL + strconv.FormatUint(groupID, 10) + "/memberslistxml")
	if err != nil {
		return memberListXML{}, fmt.Errorf("build Steam group URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("xml", "1")
	if page > 1 {
		query.Set("p", strconv.Itoa(page))
	}
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return memberListXML{}, fmt.Errorf("build Steam group request: %w", err)
	}
	request.Header.Set("Accept", "application/xml, text/xml")
	response, err := r.client.Do(request)
	if err != nil {
		return memberListXML{}, fmt.Errorf("request Steam group page %d: %w", page, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return memberListXML{}, fmt.Errorf("Steam group page %d returned HTTP %d", page, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return memberListXML{}, fmt.Errorf("read Steam group page %d: %w", page, err)
	}
	if len(body) > maxResponseSize {
		return memberListXML{}, fmt.Errorf("Steam group page %d exceeds %d bytes", page, maxResponseSize)
	}
	var payload memberListXML
	if err := xml.Unmarshal(body, &payload); err != nil {
		return memberListXML{}, fmt.Errorf("decode Steam group page %d: %w", page, err)
	}
	return payload, nil
}

func snapshotAllows(snapshot groupSnapshot, steamID uint64, maxMembers int) bool {
	if snapshot.memberCount > maxMembers {
		return false
	}
	_, ok := snapshot.members[steamID]
	return ok
}
