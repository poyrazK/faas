// Package credentialkeys runs apid's backfill of keyed credential hashes
// (ADR 20261029202403).
package credentialkeys

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/credhash"
)

// Rekeyer rewrites plain credential hashes as keyed digests;
// *state.PgStore implements it.
type Rekeyer interface {
	RekeyCredentials(ctx context.Context, batch int) (int, error)
}

const (
	// rekeyBatch is how many rows of each column one pass rewrites.
	rekeyBatch = 500
	// rekeyInterval is how long the backfill waits once a pass finds
	// nothing left.
	rekeyInterval = 5 * time.Minute
)

// RunRekey backfills the long-lived credentials while a family's keyed writes
// are on (decision 4.2): it runs passes back to back while they rewrite
// anything, then checks again every rekeyInterval for rows written plain
// meanwhile. A store that cannot rekey, or keyed writes off, makes it
// return at once. Every apid may run it: each rewrite is guarded by the
// value it replaces.
func RunRekey(ctx context.Context, store any, log *slog.Logger) {
	r, ok := store.(Rekeyer)
	if !ok || !credhash.AnyKeyedWrites() {
		return
	}
	ticker := time.NewTicker(rekeyInterval)
	defer ticker.Stop()
	for {
		for ctx.Err() == nil {
			n, err := r.RekeyCredentials(ctx, rekeyBatch)
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("credential hashes: rekey failed", "err", err)
				}
				break
			}
			if n == 0 {
				break
			}
			log.Info("credential hashes: rekeyed", "rows", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
