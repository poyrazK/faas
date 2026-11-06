package apidserver

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/daemonenv"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/datapipeline/alertdelivery"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/role"
)

func loadPipelineAlertConfig(path string) (datapipeline.Config, error) {
	current, err := LoadConfig(path)
	if err != nil || os.Getenv("FAAS_ENVIRONMENT") != "staging" || current == nil || !current.DataPipelines.Enabled {
		return datapipeline.Config{}, datapipeline.ErrUnavailable
	}
	if err = role.Require("apid", current.Role, role.RoleControlPlane); err != nil {
		return datapipeline.Config{}, datapipeline.ErrUnavailable
	}
	return current.DataPipelines, nil
}

func runDataPipelineAlertWorker(ctx context.Context, log *slog.Logger) error {
	if _, err := daemonenv.Load("apid"); err != nil {
		return err
	}
	path := apidConfigPath(flag.Lookup)
	cfg, err := LoadConfig(path)
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
	pool, err := db.OpenWithAppName(db.WithBootWait(ctx), cfg.DBURL, "faas-apid-pipeline-alert-worker")
	if err != nil {
		return errors.New("data pipeline alerts: catalog unavailable")
	}
	defer pool.Close()
	cache := &pipelineAlertConfigCache{
		now: time.Now, loadedAt: time.Now(), value: cfg.DataPipelines,
		load: func() (datapipeline.Config, error) { return loadPipelineAlertConfig(path) },
	}
	// Source allowlists, managed-database credentials, and capacity observations
	// are deliberately not needed for monitoring. Keys are loaded per delivery:
	// a missing source-worker identity cannot prevent recording an incident.
	worker := &datapipeline.AlertWorker{Store: datapipeline.NewPostgresStore(pool), Log: log, Sender: alertdelivery.New(nil, log),
		AccountsSource: func() ([]string, error) {
			current, err := cache.current()
			return current.Accounts, err
		},
		Identities: func() ([]*age.X25519Identity, error) {
			current, err := cache.current()
			if err != nil {
				return nil, err
			}
			return current.LoadIdentities()
		},
	}
	return worker.Run(ctx)
}
