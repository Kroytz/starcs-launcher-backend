package mysqlrepo

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicCatalogEntryCachesSuccessfulLoad(t *testing.T) {
	var loads atomic.Int32
	var entry publicCatalogEntry[string]

	first, err := entry.getOrLoad(time.Minute, func() (string, error) {
		loads.Add(1)
		return "ok", nil
	})
	if err != nil || first != "ok" {
		t.Fatalf("first load: value=%q err=%v", first, err)
	}
	second, err := entry.getOrLoad(time.Minute, func() (string, error) {
		loads.Add(1)
		return "again", nil
	})
	if err != nil || second != "ok" {
		t.Fatalf("cached load: value=%q err=%v", second, err)
	}
	if loads.Load() != 1 {
		t.Fatalf("expected one load, got %d", loads.Load())
	}
}

func TestPublicCatalogEntrySingleflightUnderConcurrency(t *testing.T) {
	var loads atomic.Int32
	var entry publicCatalogEntry[int]
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := entry.getOrLoad(time.Minute, func() (int, error) {
				loads.Add(1)
				startedOnce.Do(func() { close(started) })
				<-release
				return 7, nil
			})
			if err != nil {
				t.Errorf("load failed: %v", err)
			}
			if value != 7 {
				t.Errorf("unexpected value %d", value)
			}
		}()
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("load did not start")
	}
	close(release)
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("expected singleflight one load, got %d", loads.Load())
	}
}

func TestCollectAndResolveAnnouncementImageIDs(t *testing.T) {
	raw := `{"sections":[{"blocks":[{"kind":"image","imageId":12},{"kind":"image","imageId":12},{"kind":"paragraph","text":"hi"}]}]}`
	ids := collectAnnouncementImageIDs(raw)
	if len(ids) != 1 || ids[0] != 12 {
		t.Fatalf("unexpected ids: %+v", ids)
	}
	resolved, err := resolveAnnouncementPayloadWithFiles(raw, map[uint64]string{12: "images/a.png"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resolved), `"imageUrl":"https://static.starcs.cn/images/a.png"`) {
		t.Fatalf("missing resolved image url: %s", resolved)
	}
}
