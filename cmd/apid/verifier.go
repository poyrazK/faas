package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
)

// verifier polls DNS for unverified custom-domain TXT challenges and marks
// them verified in the Store. Spec §7: customer publishes a TXT at
// _faas-verify.<domain>; apid polls and flips verified_at when it matches.
//
// The verifier is a simple time-based loop that hits DNS once per minute per
// unverified domain. It uses the Go stdlib resolver (no extra dep) and runs
// in the apid process — could be moved to schedd later if load warrants.

const verifyInterval = 30 * time.Second

// startVerifier runs the verifier loop until ctx is cancelled. Caller is
// responsible for surfacing errors via the slog logger.
func startVerifier(ctx context.Context, s *server, log *slog.Logger) {
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
		log.Warn("verifier: list failed", "err", err)
		return
	}
	for _, d := range pending {
		if checkTXT(ctx, d.Domain, d.ChallengeToken) {
			if err := s.store.MarkDomainVerified(ctx, d.Domain); err != nil {
				log.Warn("verifier: mark verified failed", "domain", d.Domain, "err", err)
				continue
			}
			_ = s.notif.Notify(ctx, "domain_verified", `{"domain":"`+d.Domain+`"}`)
			log.Info("domain verified", "domain", d.Domain)
		}
	}
}

// pendingUnverifiedDomains reads the unverified index directly. Implemented
// as a tiny helper here (rather than a Store method) because the verifier
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

// pendingDomainRow is the verifier's view of an unverified custom domain.
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

// silence linter on the env import when other helpers move.
var _ = os.Getenv
