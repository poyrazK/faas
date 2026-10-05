package main

// adr: 627
import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/sched"
)

func seedSchedulerLedger(ctx context.Context, engine interface{ SeedLedger(context.Context) error }, log *slog.Logger) error {
	err := engine.SeedLedger(ctx)
	if errors.Is(err, sched.ErrRegionalRuntimeRecoveryUnresolved) {
		return fmt.Errorf("schedd: rebuild regional serving capacity before startup: %w", err)
	}
	if err != nil {
		log.Warn("seed ledger", "err", err)
	}
	return nil
}
