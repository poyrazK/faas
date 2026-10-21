package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
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
// durable candidate graphs. Each pass first checks open, the
// environment_gitops_execution kill switch (ADR-20261021080847), because
// qualification creates native VMs. Account admission stays with apid, which
// queues graphs only for admitted accounts. Every pass is bounded by the
// engine's graph page limit and each candidate is revalidated under its
// durable lease before any VM effect.
func startEnvironmentQualificationDispatch(ctx context.Context, engine environmentQualificationGraphDispatchEngine,
	store environmentQualificationRecoveryNodeStore, ownerNodeID string, open func() bool, log *slog.Logger) {
	if open == nil || engine == nil || store == nil || log == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(environmentQualificationDispatchInterval)
		defer ticker.Stop()
		cursor := &environmentQualificationDispatchCursor{}
		nodeID := ""
		wasOpen := false
		dispatchPage := func() {
			if !open() {
				if wasOpen {
					log.Info("schedd: environment GitOps qualification dispatch stopped by the environment_gitops_execution kill switch")
				}
				wasOpen, cursor.after = false, ""
				return
			}
			if !wasOpen {
				log.Info("schedd: environment GitOps qualification dispatch enabled by the environment_gitops_execution kill switch")
				wasOpen = true
			}
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

// environmentQualificationDispatchBootstrap seeds the flag's kill switch from
// FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH until an operator writes it.
// It admits no account: only apid decides accounts.
func environmentQualificationDispatchBootstrap(value string) platformflags.Definition {
	return platformflags.Definition{Enabled: environmentQualificationDispatchEnabled(value)}
}

// environmentQualificationDispatchGate is nil, so dispatch never starts, when
// admission ledger seeding failed; otherwise it reads the live kill switch.
func environmentQualificationDispatchGate(flags *platformflags.Set, seedLedgerReady bool) func() bool {
	if flags == nil || !seedLedgerReady {
		return nil
	}
	return func() bool { return flags.Snapshot().GloballyEnabled(platformflags.EnvironmentGitOpsExecution) }
}
