// Package knownhosts keeps the hostnames that name a real app, so the
// gateway's and the edge's new-host lookup budgets are spent only on names
// that do not (ADR-20261103163954 §4).
//
// Both budgets are one bucket per process for hosts the process has not yet
// seen route. They started empty on every restart, so after a release one
// client sending random names under the apps wildcard faster than the budget
// refills kept every app the node had not yet served answering 503. A Set
// holds every app slug and verified custom domain, refreshed from the store,
// and a host it knows skips the budget. Knowing a name grants nothing else:
// routing still reads and checks the row.
//
// Previously admitted concrete wildcard hosts may be preloaded. Verified
// wildcard patterns qualify only for a separate, bounded recovery budget.
package knownhosts

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// Source lists every app slug and verified custom domain, at most limit of
// each. state.KnownRouteHostStore implements it.
type Source interface {
	KnownRouteHosts(ctx context.Context, limit int) (slugs, domains []string, err error)
}

// Set is the known hostnames. The zero value and a nil Set know nothing.
type Set struct {
	appsSuffix string // ".<apps domain>"
	hosts      atomic.Pointer[hosts]
	recent     atomic.Pointer[recentHosts]
	mu         sync.Mutex
	reader     recentSource
	loading    *recentLoad
	next       time.Time
	waiters    int
}

type recentSource interface {
	ListRecentCustomDomainTLSHosts(context.Context, int) ([]string, error)
}

type recentHosts struct{ names map[string]struct{} }
type recentLoad struct {
	done    chan struct{}
	started time.Time
}

type hosts struct {
	slugs     map[string]struct{}
	domains   map[string]struct{}
	wildcards map[string]struct{}
}

// New returns an empty Set for the apps domain (with or without its leading
// dot); an empty apps domain knows custom domains only.
func New(appsDomain string) *Set {
	appsDomain = strings.Trim(strings.ToLower(appsDomain), ".")
	s := &Set{}
	if appsDomain != "" {
		s.appsSuffix = "." + appsDomain
	}
	return s
}

// Has reports whether host (lowercase, without a port) names a known app
// slug under the apps domain or a known verified custom domain.
func (s *Set) Has(host string) bool {
	if s == nil {
		return false
	}
	h := s.hosts.Load()
	if h == nil {
		if recent := s.recent.Load(); recent != nil {
			_, known := recent.names[strings.TrimSuffix(host, ".")]
			return known
		}
		return false
	}
	host = strings.TrimSuffix(host, ".")
	if slug, ok := hostidentity.AppSlugFromHost(s.appsSuffix, host); ok {
		_, known := h.slugs[slug]
		return known
	}
	_, known := h.domains[host]
	if known {
		return true
	}
	if recent := s.recent.Load(); recent != nil {
		_, known = recent.names[host]
	}
	return known
}

// Len is the number of known slugs and domains.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	h := s.hosts.Load()
	if h == nil {
		return 0
	}
	return len(h.slugs) + len(h.domains)
}

// Replace makes slugs and domains the known hostnames.
func (s *Set) Replace(slugs, domains []string) {
	h := &hosts{slugs: make(map[string]struct{}, len(slugs)), domains: make(map[string]struct{}, len(domains)), wildcards: make(map[string]struct{})}
	for _, slug := range slugs {
		if slug = strings.ToLower(slug); slug != "" {
			h.slugs[slug] = struct{}{}
		}
	}
	for _, d := range domains {
		d = strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
		if strings.HasPrefix(d, "*.") && !strings.Contains(d[2:], "*") {
			h.wildcards[d[2:]] = struct{}{}
			continue
		}
		if d == "" || strings.Contains(d, "*") {
			continue
		}
		h.domains[d] = struct{}{}
	}
	s.hosts.Store(h)
}

// WildcardCandidate identifies a possible wildcard route. It grants no free
// lookup: callers must spend their bounded wildcard recovery budget and then
// authorize the hostname against the live store.
func (s *Set) WildcardCandidate(host string) bool {
	if s == nil || strings.Contains(host, "*") {
		return false
	}
	h := s.hosts.Load()
	if h == nil {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for i := strings.IndexByte(host, '.'); i >= 0; i = strings.IndexByte(host, '.') {
		host = host[i+1:]
		if _, ok := h.wildcards[host]; ok {
			return true
		}
	}
	return false
}

// Refresh replaces the known hostnames with src's. On an error the previous
// ones stay.
func (s *Set) Refresh(ctx context.Context, src Source) error {
	s.mu.Lock()
	s.reader, _ = src.(recentSource)
	s.mu.Unlock()
	slugs, domains, err := src.KnownRouteHosts(ctx, api.KnownRouteHostsMax)
	if err != nil {
		return err
	}
	s.Replace(slugs, domains)
	return nil
}

// Recover shares a bounded, rate-limited read of recently admitted or
// verified concrete names after a lookup budget is exhausted. A match only
// bypasses that budget; the caller still performs its authoritative lookup.
func (s *Set) Recover(ctx context.Context, host string) bool {
	if s == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, api.KnownRouteRecentHostTimeout)
	defer cancel()
	arrived := time.Now()
	s.mu.Lock()
	if s.reader == nil || s.waiters >= api.OnDemandTLSAskRecentHostWaiters {
		s.mu.Unlock()
		return false
	}
	s.waiters++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.waiters--
		s.mu.Unlock()
	}()
	for range 2 {
		if s.Has(host) {
			return true
		}
		s.mu.Lock()
		load := s.loading
		if load == nil {
			load = &recentLoad{done: make(chan struct{})}
			s.loading = load
			go s.loadRecent(context.WithoutCancel(ctx), s.reader, load)
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-load.done:
		}
		if s.Has(host) {
			return true
		}
		if !load.started.Before(arrived) {
			return false
		}
	}
	return false
}

func (s *Set) loadRecent(ctx context.Context, reader recentSource, load *recentLoad) {
	ctx, cancel := context.WithTimeout(ctx, api.KnownRouteRecentHostTimeout)
	defer cancel()
	s.mu.Lock()
	delay := time.Until(s.next)
	s.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.loading = nil
			close(load.done)
			s.mu.Unlock()
			return
		case <-timer.C:
		}
	}
	s.mu.Lock()
	load.started = time.Now()
	s.next = load.started.Add(api.OnDemandTLSAskRecentHostRetry)
	s.mu.Unlock()
	names, err := reader.ListRecentCustomDomainTLSHosts(ctx, api.OnDemandTLSAskRecentHostRows)
	if err == nil {
		recent := &recentHosts{names: make(map[string]struct{}, len(names))}
		for _, name := range names {
			if name = strings.ToLower(strings.TrimSuffix(name, ".")); name != "" && !strings.Contains(name, "*") {
				recent.names[name] = struct{}{}
			}
		}
		s.recent.Store(recent)
	}
	s.mu.Lock()
	s.loading = nil
	close(load.done)
	s.mu.Unlock()
}

// Run refreshes s from src now and every api.KnownRouteHostsRefreshInterval
// until ctx ends. A failed refresh is logged and keeps the previous hosts.
func Run(ctx context.Context, s *Set, src Source, log *slog.Logger) {
	if s == nil || src == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	refresh := func() {
		readCtx, cancel := context.WithTimeout(ctx, api.KnownRouteHostsRefreshTimeout)
		defer cancel()
		if err := s.Refresh(readCtx, src); err != nil && ctx.Err() == nil {
			log.Warn("known hosts refresh failed; keeping the previous list", "err", err, "known", s.Len())
		}
	}
	refresh()
	ticker := time.NewTicker(api.KnownRouteHostsRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}
