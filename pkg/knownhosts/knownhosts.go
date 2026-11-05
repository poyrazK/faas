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
// Wildcard custom domains are left out: a wildcard matches every name under
// it, so preloading one would let random names under it skip the budget.
package knownhosts

import (
	"context"
	"log/slog"
	"strings"
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
}

type hosts struct {
	slugs   map[string]struct{}
	domains map[string]struct{}
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
		return false
	}
	host = strings.TrimSuffix(host, ".")
	if slug, ok := hostidentity.AppSlugFromHost(s.appsSuffix, host); ok {
		_, known := h.slugs[slug]
		return known
	}
	_, known := h.domains[host]
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
	h := &hosts{slugs: make(map[string]struct{}, len(slugs)), domains: make(map[string]struct{}, len(domains))}
	for _, slug := range slugs {
		if slug = strings.ToLower(slug); slug != "" {
			h.slugs[slug] = struct{}{}
		}
	}
	for _, d := range domains {
		d = strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || strings.Contains(d, "*") {
			continue
		}
		h.domains[d] = struct{}{}
	}
	s.hosts.Store(h)
}

// Refresh replaces the known hostnames with src's. On an error the previous
// ones stay.
func (s *Set) Refresh(ctx context.Context, src Source) error {
	slugs, domains, err := src.KnownRouteHosts(ctx, api.KnownRouteHostsMax)
	if err != nil {
		return err
	}
	s.Replace(slugs, domains)
	return nil
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
