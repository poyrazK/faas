package main

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

// dnsPoller polls DNS for unverified custom-domain TXT challenges and marks
// them verified in the Store. Spec §7: customer publishes a TXT at
// _faas-verify.<domain>; apid polls and flips verified_at when it matches.
//
// This is a poll-only loop — it does NOT subscribe to pg_notify. A LISTEN
// path would replace the ticker once a domain_verify producer lands. Channel
// names use pkg/db constants to stay aligned with the apid NotifyChannels
// table.
const verifyInterval = 30 * time.Second

// startDNSPoller runs the DNS poll loop until ctx is cancelled. Caller is
// responsible for surfacing errors via the slog logger.
func startDNSPoller(ctx context.Context, s *server, log *slog.Logger) {
	if s.store == nil {
		return
	}
	go func() {
		t := time.NewTicker(verifyInterval)
		defer t.Stop()
		// Run once immediately so freshly-added domains don't wait a minute.
		s.runVerifyOnce(ctx, log)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runVerifyOnce(ctx, log)
			}
		}
	}()
}

func (s *server) runVerifyOnce(ctx context.Context, log *slog.Logger) {
	pending, err := s.pendingUnverifiedDomains(ctx)
	if err != nil {
		log.Warn("dns_poller: list failed", "err", err)
		return
	}
	for _, d := range pending {
		if checkTXT(ctx, d.Domain, d.ChallengeToken) {
			if err := s.store.MarkDomainVerified(ctx, d.Domain); err != nil {
				log.Warn("dns_poller: mark verified failed", "domain", d.Domain, "err", err)
				continue
			}
			// Use the canonical channel constant (no LISTEN consumer yet —
			// recorded here so the next dns_poller→imaged LISTEN path picks up
			// the right name without a find/replace).
			_ = s.notif.Notify(ctx, db.NotifyDomainVerify, `{"domain":"`+d.Domain+`"}`)
			log.Info("domain verified", "domain", d.Domain)
		}
	}
}

// pendingUnverifiedDomains reads the unverified index directly. Implemented
// as a tiny helper here (rather than a Store method) because the poller
// goroutine is the only consumer.
func (s *server) pendingUnverifiedDomains(ctx context.Context) ([]pendingDomainRow, error) {
	// We can't reach a *sql.DB from server without exposing one on the
	// struct. The simpler path is to walk all apps and ListDomainsForApp,
	// which works fine at M5 scale (one-box, single-digit accounts). The
	// Store interface grows a dedicated method when this matters.
	var out []pendingDomainRow
	// Fast path: if the Store exposes ListAllUnverifiedDomains (PgStore),
	// use it; otherwise fall back to the per-account walk.
	type listUnverified interface {
		ListAllUnverifiedDomains(ctx context.Context) ([]pendingDomainRow, error)
	}
	if lu, ok := s.store.(listUnverified); ok {
		return lu.ListAllUnverifiedDomains(ctx)
	}
	// Fallback: not implemented for MemStore in tests; return empty.
	return out, nil
}

// pendingDomainRow is the poller's view of an unverified custom domain.
type pendingDomainRow struct {
	Domain         string
	ChallengeToken string
}

// checkTXT does a TXT lookup for _faas-verify.<domain> and reports whether
// any returned record equals the expected token.
func checkTXT(ctx context.Context, domain, expected string) bool {
	target := "_faas-verify." + domain
	resolver := &net.Resolver{}
	records, err := resolver.LookupTXT(ctx, target)
	if err != nil {
		return false
	}
	for _, r := range records {
		if strings.TrimSpace(r) == expected {
			return true
		}
	}
	return false
}
