package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// waitPlainRollback follows a plain rollback's receipt until the previous
// release serves again (ADR-1446). The rollback request alone proves nothing:
// a target that never activates used to look identical to one that did.
func waitPlainRollback(client rollbackOperationClient, slug string, operation api.RollbackOperation, timeout, interval time.Duration) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	if !jsonOutput {
		PrintProgress(osStdout, "Rollback requested to %s; waiting for it to serve", operation.TargetDeploymentID)
	}
	last, err := pollRollbackOperation(ctx, client, slug, operation, true, interval)
	if jsonOutput && last.ID != "" {
		if code := jsonOut(writeJSON(last)); code != 0 {
			return code
		}
	}
	if err == nil {
		if !jsonOutput {
			PrintOK(osStdout, "Rolled back: %s serves again%s", last.TargetDeploymentID, formatRollbackRecovery(last.RecoveryMS))
		}
		return 0
	}
	if signalCtx.Err() != nil {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return printErr("Rollback still in progress", fmt.Errorf("no outcome within %s; resume with: %s rollback status %s --operation %s --wait",
			timeout, recoveryCLI(), slug, operation.ID))
	}
	return printErr("Rollback did not restore the previous release", err)
}

func formatRollbackRecovery(ms *int64) string {
	if ms == nil {
		return ""
	}
	return fmt.Sprintf(" after %.1f s", float64(*ms)/1000)
}
