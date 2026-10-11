package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const environmentQualificationRecoveryInterval = time.Minute

type environmentQualificationRecoveryEngine interface {
	RecoverEnvironmentQualificationExecutions(context.Context, string, string, int) (sched.EnvironmentQualificationRecoveryPage, error)
}

type environmentQualificationRecoveryCursor struct{ after string }

func (r *environmentQualificationRecoveryCursor) recoverPage(ctx context.Context, engine environmentQualificationRecoveryEngine, nodeID string, log *slog.Logger) {
	page, err := engine.RecoverEnvironmentQualificationExecutions(ctx, nodeID, r.after, api.EnvironmentGitOpsQualificationRecoveryBatchMax)
	if page.NextCursor == r.after && r.after != "" {
		// A malformed or stale cursor must not pin this worker forever.
		r.after = ""
		err = errors.Join(err, fmt.Errorf("qualification recovery cursor did not advance: %w", state.ErrConflict))
	} else {
		r.after = page.NextCursor
	}
	if err != nil && ctx.Err() == nil {
		log.Warn("schedd: environment qualification recovery page failed", "node_id", nodeID,
			"examined", page.Examined, "retired", page.Retired, "skipped", page.Skipped, "err", err)
	} else if page.Examined > 0 {
		log.Info("schedd: environment qualification recovery page completed", "node_id", nodeID,
			"examined", page.Examined, "retired", page.Retired, "skipped", page.Skipped)
	}
}

// startEnvironmentQualificationRecovery continuously retries retirement of
// abandoned qualification executions. This is cleanup-only: the recovery API
// uses the original execution frame and cannot boot, qualify, or activate a
// candidate. A cursor is retained between ticks so one unavailable VM cannot
// starve later cleanup records.
type environmentQualificationRecoveryNodeStore interface {
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
}

func startEnvironmentQualificationRecovery(ctx context.Context, engine environmentQualificationRecoveryEngine,
	store environmentQualificationRecoveryNodeStore, ownerNodeID string, log *slog.Logger) {
	if engine == nil || store == nil || log == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(environmentQualificationRecoveryInterval)
		defer ticker.Stop()
		cursor := &environmentQualificationRecoveryCursor{}
		nodeID := ""
		recoverPage := func() {
			resolved, err := environmentQualificationRecoveryNodeID(ctx, store, ownerNodeID)
			if err != nil {
				log.Warn("schedd: environment qualification recovery node lookup failed", "err", err)
				return
			}
			if nodeID != "" && nodeID != resolved {
				cursor.after = ""
			}
			nodeID = resolved
			cursor.recoverPage(ctx, engine, nodeID, log)
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

// A legacy single-box schedd has no configured owner ID, but qualification
// execution frames still use the UUID of the synthetic default-local node.
// Resolve that exact row so recovery is routed only to the host that owns it.
func environmentQualificationRecoveryNodeID(ctx context.Context, store environmentQualificationRecoveryNodeStore, ownerNodeID string) (string, error) {
	if ownerNodeID != "" {
		return ownerNodeID, nil
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		return "", err
	}
	if node.ID == "" || node.Name != state.DefaultLocalNodeName {
		return "", state.ErrConflict
	}
	return node.ID, nil
}
