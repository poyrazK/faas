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
// a full route set (ADR-20261028110044 amendment 4). Pins only protect
// evidence and never change request handling, so a failed read keeps the last
// known pins and retries shortly instead of failing the request.
type routeLabelPins struct {
	store      routeLabelPinStore
	ttl, retry time.Duration
	timeout    time.Duration
	now        func() time.Time
	log        *slog.Logger

	mu      sync.Mutex
	entries map[string]routeLabelPinEntry
}

type routeLabelPinEntry struct {
	labels  map[string]struct{}
	expires time.Time
}

func newRouteLabelPins(store routeLabelPinStore, log *slog.Logger) *routeLabelPins {
	return &routeLabelPins{store: store, ttl: time.Minute, retry: 10 * time.Second, timeout: 500 * time.Millisecond,
		now: time.Now, log: log, entries: make(map[string]routeLabelPinEntry)}
}

func (p *routeLabelPins) RouteLabelPinned(ctx context.Context, app gateway.App, label string) bool {
	if p == nil || p.store == nil || app.ID == "" {
		return false
	}
	now := p.now()
	p.mu.Lock()
	entry, ok := p.entries[app.ID]
	refresh := !ok || !now.Before(entry.expires)
	if refresh {
		// Claim the refresh: concurrent requests use the last known pins
		// rather than all reading the store.
		p.entries[app.ID] = routeLabelPinEntry{labels: entry.labels, expires: now.Add(p.retry)}
	}
	p.mu.Unlock()
	if refresh {
		entry = p.load(ctx, app.ID, entry.labels)
	}
	_, pinned := entry.labels[label]
	return pinned
}

func (p *routeLabelPins) load(ctx context.Context, appID string, previous map[string]struct{}) routeLabelPinEntry {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	labels, err := p.store.RouteLabelPins(ctx, appID)
	next := routeLabelPinEntry{labels: previous, expires: p.now().Add(p.retry)}
	if err != nil {
		if p.log != nil {
			p.log.Warn("gateway: route label pins unavailable", "app_id", appID, "err", err)
		}
	} else {
		next.labels = make(map[string]struct{}, len(labels))
		for _, label := range labels {
			next.labels[label] = struct{}{}
		}
		next.expires = p.now().Add(p.ttl)
	}
	p.mu.Lock()
	p.entries[appID] = next
	p.mu.Unlock()
	return next
}
