package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// This worker resumes existing intents only. It cannot create a drain request,
// fence an app, change credentials, or activate a customer cutover.
func startManagedPostgresDrainRecovery(ctx context.Context, engine *sched.Engine, store environmentQualificationRecoveryNodeStore, ownerNodeID string, log *slog.Logger) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		after := ""
		recoverPage := func() {
			owner, err := environmentQualificationRecoveryNodeID(ctx, store, ownerNodeID)
			if err != nil {
				log.Warn("schedd: cutover drain owner lookup failed", "err", err)
				return
			}
			page, err := engine.RecoverManagedPostgresDrains(ctx, owner, after, api.ManagedPostgresCutoverDrainBatchMax)
			if page.NextCursor == after && after != "" {
				after = ""
				err = errors.Join(err, state.ErrConflict)
			} else {
				after = page.NextCursor
			}
			if err != nil && ctx.Err() == nil {
				log.Warn("schedd: cutover drain recovery failed", "examined", page.Examined, "retired", page.Retired, "err", err)
			}
		}
		recoverPage()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				recoverPage()
			}
		}
	}()
}
