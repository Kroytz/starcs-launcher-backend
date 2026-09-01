package mysqlrepo

import (
	"sync"
	"time"
)

// publicCatalogTTL bounds how long bootstrap/store/maps share one DB snapshot.
// Short enough for ops updates to appear quickly; long enough to stop stampeding
// the 10-connection primary pool under launcher refresh traffic.
const publicCatalogTTL = 30 * time.Second

// negativeCacheTTL avoids a thundering herd against a failing database.
const negativeCacheTTL = time.Second

type publicCatalogEntry[T any] struct {
	mu        sync.Mutex
	value     T
	err       error
	expiresAt time.Time
	loading   chan struct{}
}

func (e *publicCatalogEntry[T]) getOrLoad(ttl time.Duration, load func() (T, error)) (T, error) {
	for {
		e.mu.Lock()
		if time.Now().Before(e.expiresAt) {
			value, err := e.value, e.err
			e.mu.Unlock()
			return value, err
		}
		if e.loading != nil {
			wait := e.loading
			e.mu.Unlock()
			<-wait
			continue
		}
		wait := make(chan struct{})
		e.loading = wait
		e.mu.Unlock()

		value, err := load()

		e.mu.Lock()
		e.value = value
		e.err = err
		if err == nil {
			e.expiresAt = time.Now().Add(ttl)
		} else {
			e.expiresAt = time.Now().Add(negativeCacheTTL)
		}
		e.loading = nil
		close(wait)
		e.mu.Unlock()
		return value, err
	}
}
