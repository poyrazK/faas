package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/daemonenv"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
)

// Private opt-in process only. Normal apid startup never runs this worker;
// no service unit or customer admission is shipped before native acceptance.
// It has no listeners, VM clients, provider loops or source reservation route.
func runRuntimeUpgradeWorker(ctx context.Context, log *slog.Logger) error {
	if _, err := daemonenv.Load("apid"); err != nil {
		return err
	}
	if err := runtimecheck.MustCheckOnBoot(capsDecl, log, nil); err != nil {
		return err
	}
	cfg, err := LoadConfig(apidConfigPath(flag.Lookup))
	if err != nil {
		return fmt.Errorf("runtime upgrade worker: load config: %w", err)
	}
	if err := role.Require("apid", cfg.Role, role.RoleControlPlane); err != nil {
		return err
	}
	if err := rejectProductionDevEnvironment(cfg.Role, os.Getenv); err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, cfg.DBURL, "faas-apid-runtime-upgrade-worker")
	if err != nil {
		return errors.New("runtime upgrade worker: control-plane database unavailable")
	}
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		// PostgreSQL constraint diagnostics may include persisted row values.
		return errors.New("runtime upgrade worker: migration unavailable")
	}
	return runtimeupgrade.RunWorker(ctx, log, state.NewPgStore(pool))
}
