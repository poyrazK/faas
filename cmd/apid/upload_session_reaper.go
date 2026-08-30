// cmd/apid/upload_session_reaper.go — in-process reaper for
// upload_sessions rows whose expires_at has passed.
//
// Why in-process in apid (not in meterd or schedd): the reaper
// touches two surfaces apid owns:
//   1. upload_sessions.status (open → expired)
//   2. /var/spool/faas/builds/<id>.part files
// schedd owns the state machine (§6) but doesn't read or write
// the spool; meterd owns different retention (pkg/meter/retention.go).
//
// Pattern: mirror rekeyRunner (cmd/apid/main.go:571-573 +
// cmd/apid/server.go:208-224) and startDNSPoller /
// startDebugRegressionCron at cmd/apid/main.go:583-589.
//
// Cadence: 5 minutes. The 24h TTL means a session can sit
// expired for up to 5 minutes before the reaper sweeps it —
// fine for the trust boundary (the session row's status='open'
// predicate blocks any new PATCH after expires_at; see
// AppendUploadBytes in pkg/state/queries.sql).
//
// Idempotency: ReapExpiredUploadSessions returns at most 100
// rows per call (bounded scan). After ExpireUploadSession +
// os.Remove, the goroutine returns to its 5-minute wait. If a
// second tick finds the same row (because the UPDATE failed
// mid-flight), it re-tries — but the row was already flipped
// to 'expired' last tick, so the partial index
// upload_sessions_expires_idx WHERE status='open' excludes it.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// reaperTick is the sweep cadence. 5 minutes keeps the worst-case
// staleness (a session in 'open' status past expires_at) at <5m.
// Matches the dns_poller / debug_regression_cron cadences.
const reaperTick = 5 * time.Minute

// runUploadSessionReaper blocks until ctx is cancelled. The
// ticker fires the first sweep at t=0 (so an apid boot catches
// up on any backlog from a previous instance crash) and then
// every reaperTick.
//
// Mirrors the rekeyRunner.Run pattern (cmd/apid/rekey_runner.go).
func runUploadSessionReaper(ctx context.Context, s *server, log *slog.Logger) error {
	tick := time.NewTicker(reaperTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			sweepUploadSessions(ctx, s, log)
		}
	}
}

// sweepUploadSessions is a single iteration of the reaper.
// Exposed as its own function so the unit tests
// (cmd/apid/handlers_upload_session_test.go) can call it
// directly without spinning a ticker.
func sweepUploadSessions(ctx context.Context, s *server, log *slog.Logger) {
	rows, err := s.store.ReapExpiredUploadSessions(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Error("upload session reaper: scan failed", "err", err)
			uploadSessionReaperFailedTotal().Inc()
		}
		return
	}
	// PR-1 fixup #5: sweep .part files for terminal-row sessions
	// (status IN committed/cancelled/expired, last_patched_at
	// < now() - 1h). The commit handler leaves .part in place
	// for builderd to consume; the cancel handler removes its
	// .part at the same time it flips status='cancelled'; but
	// neither has a 1h cleanup guarantee for committed rows.
	// Stale part sweep runs separately from the open-expired
	// sweep so a partial failure in one does not block the
	// other.
	stale, staleErr := s.store.ReapStaleUploadPartFiles(ctx)
	if staleErr != nil && !errors.Is(staleErr, context.Canceled) {
		log.Warn("upload session reaper: stale scan failed", "err", staleErr)
		uploadSessionReaperFailedTotal().Inc()
	}
	deleted := 0
	for _, row := range rows {
		// Race order matters: do .part removal FIRST, then flip
		// status. The reverse ordering leaves a leaked .part if
		// os.Remove fails (the WHERE status='open' partial index
		// would then exclude the row from the next sweep,
		// bounding nothing). On os.Remove failure we leave the
		// row at status='open' so the next tick re-attempts the
		// removal — but the 24h TTL bounds the leak (worst case:
		// one sweep cycle of staleness).
		if row.PartPath != "" {
			if rmErr := removeUploadPart(row.PartPath); rmErr != nil && !os.IsNotExist(rmErr) {
				log.Warn("upload session reaper: .part remove failed, will retry next sweep",
					"upload_id", row.ID, "path", row.PartPath, "err", rmErr)
				uploadSessionReaperFailedTotal().Inc()
				continue
			}
		}
		if err := s.store.ExpireUploadSession(ctx, row.ID); err != nil {
			if errors.Is(err, state.ErrConflict) {
				// Already expired by a racing reaper (or by
				// an admin tool). Skip silently — the partial
				// index will exclude it next sweep.
				continue
			}
			log.Warn("upload session reaper: expire failed",
				"upload_id", row.ID, "err", err)
			uploadSessionReaperFailedTotal().Inc()
			continue
		}
		deleted++
		uploadSessionExpiredTotal().Inc()
	}
	if deleted > 0 {
		// Increment by the batch count, not 1 — matches the
		// Prometheus pattern of one observation per sweep
		// outcome. The metric is a counter, so the
		// observation unit is "rows deleted in this sweep".
		for i := 0; i < deleted; i++ {
			uploadSessionReaperRowsDeletedTotal().Inc()
		}
		log.Info("upload session reaper: open-expired sweep complete", "deleted", deleted)
	}
	// Stale part sweep (#5): remove .part files for terminal
	// rows that builderd never consumed (e.g. apid crash between
	// commit + builderd pickup, or a builderd crash mid-read).
	// No DB UPDATE — status is already terminal.
	staleDeleted := 0
	for _, row := range stale {
		if row.PartPath == "" {
			continue
		}
		if rmErr := removeUploadPart(row.PartPath); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Warn("upload session reaper: stale .part remove failed",
				"upload_id", row.ID, "path", row.PartPath, "err", rmErr)
			uploadSessionReaperFailedTotal().Inc()
			continue
		}
		staleDeleted++
	}
	if staleDeleted > 0 {
		for i := 0; i < staleDeleted; i++ {
			uploadSessionReaperRowsDeletedTotal().Inc()
		}
		log.Info("upload session reaper: stale-part sweep complete", "deleted", staleDeleted)
	}
}

// removeUploadPart is os.Remove wrapped so tests can swap it
// without monkey-patching the global os package.
var removeUploadPart = func(path string) error { return os.Remove(path) }