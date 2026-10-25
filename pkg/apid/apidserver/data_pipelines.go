package apidserver

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	pipelineapi "github.com/onebox-faas/faas/pkg/apid/datapipelines"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
	"github.com/onebox-faas/faas/pkg/daemonenv"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configureDataPipelines(cfg Config, pool *pgxpool.Pool) error {
	if _, err := cfg.DataPipelines.Policy(); err != nil {
		return errors.New("data pipelines: invalid operator policy")
	}
	if cfg.DataPipelines.Enabled && os.Getenv("FAAS_ENVIRONMENT") != "staging" {
		return errors.New("data pipelines: qualification is staging-only")
	}
	if pool == nil {
		return nil
	}
	service := &datapipeline.Service{CanCreate: func(ctx context.Context, account string) bool {
		acct, err := s.store.AccountByID(ctx, account)
		return err == nil && acct.MayDeploy()
	}, Store: datapipeline.NewPostgresStore(pool), Allowed: func(ctx context.Context, account string) bool {
		if !cfg.DataPipelines.Allowed(account) {
			return false
		}
		acct, err := s.store.AccountByID(ctx, account)
		return err == nil && acct.Plan != api.PlanFree && acct.Active()
	}, ValidateTarget: func(ctx context.Context, account, id string) error {
		if s.managedPostgres == nil {
			return datapipeline.ErrUnavailable
		}
		if err := s.managedPostgres.ValidatePipelineTarget(ctx, account, id); err != nil {
			return datapipeline.ErrConflict
		}
		return nil
	}}
	if cfg.DataPipelines.RecipientPath != "" {
		recipient, err := secretbox.LoadRecipient(cfg.DataPipelines.RecipientPath)
		if err != nil {
			return errors.New("data pipelines: recipient unavailable")
		}
		service.Recipient = recipient
	}
	s.configurePipelineSetup(service)
	s.dataPipelines = &pipelineapi.Handler{Service: service}
	service.CapacityStatus = func(ctx context.Context, account, id string) (*api.DataPipelineCapacity, error) {
		return datapipeline.PipelineCapacityStatus(ctx, connectioncapacity.NewStore(pool), account, id)
	}
	return nil
}

// New domain handlers receive account identity only from the authenticated
// apid boundary. The domain never trusts account fields in request JSON.
func (s *server) pipelineConnections(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.Connections(w, r, a.ID)
}
func (s *server) pipelineCreateConnection(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.CreateConnection(w, r, a.ID)
}
func (s *server) pipelineRotateConnection(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.RotateConnection(w, r, a.ID)
}
func (s *server) pipelineDeleteConnection(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.DeleteConnection(w, r, a.ID)
}
func (s *server) pipelineRequestCheck(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.RequestCheck(w, r, a.ID)
}
func (s *server) pipelineGetCheck(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.GetCheck(w, r, a.ID)
}
func (s *server) pipelineCreate(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.Create(w, r, a.ID)
}
func (s *server) pipelineList(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.List(w, r, a.ID)
}
func (s *server) pipelineGet(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.Get(w, r, a.ID)
}
func (s *server) pipelineControl(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.dataPipelines.Control(w, r, a.ID)
}

func runDataPipelineWorker(ctx context.Context, log *slog.Logger) error {
	if _, err := daemonenv.Load("apid"); err != nil {
		return err
	}
	cfg, err := LoadConfig(apidConfigPath(flag.Lookup))
	if err != nil {
		return err
	}
	if os.Getenv("FAAS_ENVIRONMENT") != "staging" {
		return errors.New("data pipelines: qualification is staging-only")
	}
	if err = role.Require("apid", cfg.Role, role.RoleControlPlane); err != nil {
		return err
	}
	if err = runtimecheck.MustCheckOnBoot(capsDecl, log, nil); err != nil {
		return err
	}
	policy, err := cfg.DataPipelines.Policy()
	if err != nil {
		return err
	}
	identities, err := cfg.DataPipelines.LoadIdentities()
	if err != nil {
		return errors.New("data pipelines: private identity unavailable")
	}
	pool, err := db.OpenWithAppName(ctx, cfg.DBURL, "faas-apid-pipeline-worker")
	if err != nil {
		return errors.New("data pipelines: catalog unavailable")
	}
	defer pool.Close()
	service, reconciler, bindings, bindingReconciler, usage, health, err := loadManagedPostgres(pool, os.Getenv, log, nil)
	if err != nil {
		return err
	}
	srv := newServer(state.NewPgStore(pool), log, cfg.GetAppsDomain(os.Getenv), pgNotifier{pool: pool, log: log})
	srv.WithManagedPostgres(service, reconciler, bindings, bindingReconciler, usage, health)
	if err = srv.configureDataPipelines(*cfg, pool); err != nil {
		return err
	}
	if service == nil {
		return managedpostgres.ErrUnavailable
	}
	store := datapipeline.NewPostgresStore(pool)
	capacity := &datapipeline.CapacityObserver{Store: connectioncapacity.NewStore(pool), Qualifications: cfg.DataPipelines.Capacity, Target: service.PipelineCapacityTarget}
	worker := &datapipeline.Worker{SetupTick: srv.dataPipelines.Service.SetupTick, Store: store, Identities: identities, Allowed: srv.dataPipelines.Service.Allowed, Engine: &datapipeline.Engine{RefreshSchema: store.RefreshSchema, Policy: policy, Target: service.WithPipelineSQL, Capacity: capacity, Admit: store.AdmitCapacity}}
	return worker.Run(ctx)
}
