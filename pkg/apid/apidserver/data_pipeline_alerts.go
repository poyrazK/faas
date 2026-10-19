package apidserver

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"

	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/daemonenv"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/datapipeline/alertdelivery"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/role"
)

func runDataPipelineAlertWorker(ctx context.Context, log *slog.Logger) error {
	if _, err := daemonenv.Load("apid"); err != nil {
		return err
	}
	cfg, err := LoadConfig(apidConfigPath(flag.Lookup))
	if err != nil {
		return err
	}
	if os.Getenv("FAAS_ENVIRONMENT") != "staging" || !cfg.DataPipelines.Enabled {
		return errors.New("data pipeline alerts: enabled staging pilot required")
	}
	if err = role.Require("apid", cfg.Role, role.RoleControlPlane); err != nil {
		return err
	}
	if err = runtimecheck.MustCheckOnBoot(capsDecl, log, nil); err != nil {
		return err
	}
	pool, err := db.OpenWithAppName(ctx, cfg.DBURL, "faas-apid-pipeline-alert-worker")
	if err != nil {
		return errors.New("data pipeline alerts: catalog unavailable")
	}
	defer pool.Close()
	// Source allowlists, managed-database credentials, and capacity observations
	// are deliberately not needed for monitoring. Keys are loaded per delivery:
	// a missing source-worker identity cannot prevent recording an incident.
	worker := &datapipeline.AlertWorker{Store: datapipeline.NewPostgresStore(pool), Accounts: cfg.DataPipelines.Accounts, Identities: cfg.DataPipelines.LoadIdentities, Log: log, Sender: alertdelivery.New(nil, log)}
	return worker.Run(ctx)
}
