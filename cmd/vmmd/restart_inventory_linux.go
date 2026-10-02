//go:build linux

// adr: 398
package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

func recoverRestartResources(ctx context.Context, mgr *fcvm.Manager, jailRoot string, log *slog.Logger) error {
	rep, err := mgr.RecoverRestartQuarantine(ctx, jailRoot)
	if err != nil {
		return err
	}
	log.Info("vmmd: restart resource quarantine", "slots", rep.Slots,
		"instances", rep.Instances, "processes", rep.Processes,
		"ownership_reconciliation_required", rep.Instances > 0 || rep.Slots > 0)
	return nil
}
