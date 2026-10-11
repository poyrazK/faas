package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const environmentQualificationDispatchInterval = 30 * time.Second

type environmentQualificationGraphDispatchEngine interface {
	DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth(context.Context, string, string, string, int) (sched.EnvironmentQualificationGraphDispatchPage, error)
}

type environmentQualificationDispatchCursor struct{ after string }

func (c *environmentQualificationDispatchCursor) dispatchPage(ctx context.Context, engine environmentQualificationGraphDispatchEngine,
	nodeID string, log *slog.Logger) {
	workerID := "schedd-environment-qualification/" + nodeID
	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth(ctx, nodeID, workerID, c.after,
		api.EnvironmentGitOpsQualificationDispatchBatchMax)
	if page.NextCursor != "" && page.NextCursor == c.after {
		// A malformed cursor must not pin this worker to one durable page.
		c.after = ""
		err = errors.Join(err, fmt.Errorf("qualification dispatch cursor did not advance: %w", state.ErrConflict))
	} else {
		c.after = page.NextCursor
	}
	if err != nil && ctx.Err() == nil {
		log.Warn("schedd: environment qualification dispatch page failed", "node_id", nodeID,
			"examined", page.Examined, "claimed", page.Claimed, "executed", page.Executed, "skipped", page.Skipped, "err", err)
	} else if page.Examined > 0 {
		log.Info("schedd: environment qualification dispatch page completed", "node_id", nodeID,
			"examined", page.Examined, "claimed", page.Claimed, "executed", page.Executed, "skipped", page.Skipped)
	}
}

// startEnvironmentQualificationDispatch is the production recovery poller for
// durable candidate graphs. It is opt-in because qualification can create
// native VMs and remains gated on dedicated Linux capture/restore acceptance.
// Every pass is bounded by the engine's graph page limit and each candidate is
// revalidated under its durable lease before any VM effect.
func startEnvironmentQualificationDispatch(ctx context.Context, engine environmentQualificationGraphDispatchEngine,
	store environmentQualificationRecoveryNodeStore, ownerNodeID string, enabled bool, log *slog.Logger) {
	if !enabled || engine == nil || store == nil || log == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(environmentQualificationDispatchInterval)
		defer ticker.Stop()
		cursor := &environmentQualificationDispatchCursor{}
		nodeID := ""
		dispatchPage := func() {
			resolved, err := environmentQualificationRecoveryNodeID(ctx, store, ownerNodeID)
			if err != nil {
				log.Warn("schedd: environment qualification dispatch node lookup failed", "err", err)
				return
			}
			if nodeID != "" && nodeID != resolved {
				cursor.after = ""
			}
			nodeID = resolved
			cursor.dispatchPage(ctx, engine, nodeID, log)
		}
		dispatchPage()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dispatchPage()
			}
		}
	}()
}

func environmentQualificationDispatchEnabled(value string) bool {
	return strings.TrimSpace(value) == "1"
}

func environmentQualificationDispatchAllowed(value string, seedLedgerReady bool) bool {
	return seedLedgerReady && environmentQualificationDispatchEnabled(value)
}
