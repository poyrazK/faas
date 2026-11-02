package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// routeLabelPinStore is the only control-plane read the pin cache needs.
type routeLabelPinStore interface {
	RouteLabelPins(ctx context.Context, appID string) ([]string, error)
}

// routeLabelPins caches the route labels each app's route health guard or
// production monitor selects, so the handler keeps their telemetry labels in
// a full route set (ADR-20261028110044 amendment 4). A request only reads the
// cache: an app's first request and each expiry start one background read, and
// requests keep the last known pins meanwhile (amendment 5). Pins only protect
// evidence and never change request handling, so a failed read keeps the last
// known pins and retries shortly.
type routeLabelPins struct {
	store      routeLabelPinStore
	ttl, retry time.Duration
	timeout    time.Duration
	now        func() time.Time
	log        *slog.Logger

	mu      sync.RWMutex
	entries map[string]routeLabelPinEntry
	loading map[string]bool
	// reads tracks background reads so tests can wait for them.
	reads sync.WaitGroup
}

type routeLabelPinEntry struct {
	labels  map[string]struct{}
	expires time.Time
}

func newRouteLabelPins(store routeLabelPinStore, log *slog.Logger) *routeLabelPins {
	return &routeLabelPins{store: store, ttl: time.Minute, retry: 10 * time.Second, timeout: 2 * time.Second,
		now: time.Now, log: log, entries: make(map[string]routeLabelPinEntry), loading: make(map[string]bool)}
}

func (p *routeLabelPins) RouteLabelPinned(_ context.Context, app gateway.App, label string) bool {
	if p == nil || p.store == nil || app.ID == "" {
		return false
	}
	now := p.now()
	p.mu.RLock()
	entry, ok := p.entries[app.ID]
	p.mu.RUnlock()
	if !ok || !now.Before(entry.expires) {
		p.refresh(app.ID, now)
	}
	_, pinned := entry.labels[label]
	return pinned
}

// refresh starts one background read for appID unless one is running or a
// concurrent read already renewed it.
func (p *routeLabelPins) refresh(appID string, now time.Time) {
	p.mu.Lock()
	entry, ok := p.entries[appID]
	if p.loading[appID] || ok && now.Before(entry.expires) {
		p.mu.Unlock()
		return
	}
	p.loading[appID] = true
	p.mu.Unlock()
	p.reads.Add(1)
	go func() {
		defer p.reads.Done()
		p.load(appID)
	}()
}

func (p *routeLabelPins) load(appID string) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	labels, err := p.store.RouteLabelPins(ctx, appID)
	if err != nil && p.log != nil {
		p.log.Warn("gateway: route label pins unavailable", "app_id", appID, "err", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.loading, appID)
	next := routeLabelPinEntry{labels: p.entries[appID].labels, expires: p.now().Add(p.retry)}
	if err == nil {
		next.labels = make(map[string]struct{}, len(labels))
		for _, label := range labels {
			next.labels[label] = struct{}{}
		}
		next.expires = p.now().Add(p.ttl)
	}
	p.entries[appID] = next
}
