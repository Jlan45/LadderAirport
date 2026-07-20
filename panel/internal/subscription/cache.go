package subscription

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ladderairport/panel/internal/store"
)

const (
	DefaultRefreshInterval = 24 * time.Hour
	MinRefreshInterval     = time.Minute
	MaxRefreshInterval     = 24 * time.Hour
	StaleGrace             = 24 * time.Hour
	BackgroundTick         = time.Minute
	BackgroundConcurrency  = 3
)

// SourceStore is the persistence surface Aggregator needs.
type SourceStore interface {
	GetExternalSource(id string) (*store.ExternalSource, error)
	ListExternalSources() ([]store.ExternalSource, error)
	SaveExternalSourceCache(id, body, contentType string, proxyCount int, fetchUnix, successUnix int64, lastErr string) error
}

// Aggregator fetches, caches, and parses external subscription sources.
type Aggregator struct {
	Store SourceStore

	mu    sync.Mutex
	inflight map[string]*flight
}

type flight struct {
	done chan struct{}
	err  error
}

func NewAggregator(st SourceStore) *Aggregator {
	return &Aggregator{
		Store:    st,
		inflight: map[string]*flight{},
	}
}

// EndpointsForSources returns prefixed endpoints for the given sources.
// Failures are soft: warnings are returned and the source is skipped.
// Uses stale-while-revalidate: fresh cache is used as-is; stale cache is returned
// while a background refresh is kicked; never-fetched sources are fetched sync.
func (a *Aggregator) EndpointsForSources(ctx context.Context, sources []store.ExternalSource) (eps []ProxyEndpoint, warnings []string) {
	if a == nil {
		return nil, nil
	}
	now := time.Now().Unix()
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		// Refresh snapshot from DB when possible (may include newer cache).
		if a.Store != nil {
			if fresh, err := a.Store.GetExternalSource(src.ID); err == nil && fresh != nil {
				src = *fresh
			}
		}
		ttl := effectiveTTL(src.RefreshIntervalSec)
		body, kind, warn := a.bodyForSource(ctx, src, now, ttl)
		if warn != "" {
			warnings = append(warnings, fmt.Sprintf("%s: %s", src.Name, warn))
		}
		if len(body) == 0 {
			continue
		}
		parsed, detected, err := DetectAndParse(body)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: parse: %v", src.Name, err))
			continue
		}
		if kind == "" {
			kind = detected
		}
		_ = kind
		for _, ep := range parsed {
			ep.Name = prefixExternalName(src.Name, ep.Name)
			ep.SourceID = src.ID
			eps = append(eps, ep)
		}
	}
	return eps, warnings
}

func (a *Aggregator) bodyForSource(ctx context.Context, src store.ExternalSource, now int64, ttl time.Duration) (body []byte, kind string, warn string) {
	fresh := src.CachedBody != "" && src.LastSuccessUnix > 0 && now-src.LastSuccessUnix < int64(ttl.Seconds())
	staleOK := src.CachedBody != "" && src.LastSuccessUnix > 0 && now-src.LastSuccessUnix < int64((ttl+StaleGrace).Seconds())

	if fresh {
		return []byte(src.CachedBody), src.ContentType, ""
	}
	if src.CachedBody != "" && staleOK {
		// Serve stale; refresh async.
		go func() {
			_ = a.RefreshSource(context.Background(), src.ID)
		}()
		return []byte(src.CachedBody), src.ContentType, ""
	}
	// Never fetched or beyond grace — sync refresh.
	if err := a.RefreshSource(ctx, src.ID); err != nil {
		if src.CachedBody != "" {
			return []byte(src.CachedBody), src.ContentType, fmt.Sprintf("refresh failed, using stale: %v", err)
		}
		return nil, "", fmt.Sprintf("refresh failed: %v", err)
	}
	// Re-read after refresh.
	if a.Store != nil {
		if updated, err := a.Store.GetExternalSource(src.ID); err == nil && updated != nil && updated.CachedBody != "" {
			return []byte(updated.CachedBody), updated.ContentType, ""
		}
	}
	return nil, "", "refresh produced empty cache"
}

// RefreshSource fetches and parses a source, updating the cache.
// Concurrent calls for the same id coalesce.
func (a *Aggregator) RefreshSource(ctx context.Context, id string) error {
	if a == nil || a.Store == nil {
		return fmt.Errorf("aggregator not configured")
	}
	// singleflight
	a.mu.Lock()
	if f, ok := a.inflight[id]; ok {
		a.mu.Unlock()
		select {
		case <-f.done:
			return f.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	a.inflight[id] = f
	a.mu.Unlock()

	err := a.doRefresh(ctx, id)

	a.mu.Lock()
	f.err = err
	close(f.done)
	delete(a.inflight, id)
	a.mu.Unlock()
	return err
}

func (a *Aggregator) doRefresh(ctx context.Context, id string) error {
	src, err := a.Store.GetExternalSource(id)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	body, err := FetchURL(ctx, src.URL, src.Headers)
	if err != nil {
		// Keep previous good body; record error.
		_ = a.Store.SaveExternalSourceCache(id, src.CachedBody, src.ContentType, src.CachedProxyCount, now, src.LastSuccessUnix, err.Error())
		return err
	}
	eps, kind, err := DetectAndParse(body)
	if err != nil {
		_ = a.Store.SaveExternalSourceCache(id, src.CachedBody, src.ContentType, src.CachedProxyCount, now, src.LastSuccessUnix, err.Error())
		return err
	}
	if len(eps) == 0 {
		err = fmt.Errorf("parsed 0 proxies")
		_ = a.Store.SaveExternalSourceCache(id, src.CachedBody, src.ContentType, src.CachedProxyCount, now, src.LastSuccessUnix, err.Error())
		return err
	}
	return a.Store.SaveExternalSourceCache(id, string(body), kind, len(eps), now, now, "")
}

// RunBackground periodically refreshes due sources until ctx is done.
func (a *Aggregator) RunBackground(ctx context.Context, every time.Duration) {
	if a == nil {
		return
	}
	if every <= 0 {
		every = BackgroundTick
	}
	t := time.NewTicker(every)
	defer t.Stop()
	// Immediate pass once.
	a.refreshDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.refreshDue(ctx)
		}
	}
}

func (a *Aggregator) refreshDue(ctx context.Context) {
	if a.Store == nil {
		return
	}
	list, err := a.Store.ListExternalSources()
	if err != nil {
		log.Printf("external-source refresh: list: %v", err)
		return
	}
	now := time.Now().Unix()
	sem := make(chan struct{}, BackgroundConcurrency)
	var wg sync.WaitGroup
	for _, src := range list {
		if !src.Enabled {
			continue
		}
		ttl := effectiveTTL(src.RefreshIntervalSec)
		due := src.LastSuccessUnix == 0 || now-src.LastSuccessUnix >= int64(ttl.Seconds())
		if !due {
			continue
		}
		// ListExternalSources omits body; refresh still works via Get in doRefresh.
		srcID := src.ID
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := a.RefreshSource(ctx, srcID); err != nil {
				log.Printf("external-source refresh %s: %v", srcID, err)
			}
		}()
	}
	wg.Wait()
}

func effectiveTTL(sec int) time.Duration {
	if sec <= 0 {
		return DefaultRefreshInterval
	}
	d := time.Duration(sec) * time.Second
	if d < MinRefreshInterval {
		return MinRefreshInterval
	}
	if d > MaxRefreshInterval {
		return MaxRefreshInterval
	}
	return d
}
