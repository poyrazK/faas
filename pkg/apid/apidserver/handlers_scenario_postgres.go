package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configureRehearsalPostgres(ctx context.Context, getenv func(string) string) error {
	dsn := getenv("FAAS_REHEARSAL_POSTGRES_ADMIN_URL")
	s.rehearsalPostgresEnabled = getenv("FAAS_REHEARSAL_POSTGRES_FIXTURES") == "1"
	if dsn == "" {
		if s.rehearsalPostgresEnabled {
			return errors.New("rehearsal PostgreSQL requires a dedicated fixture cluster")
		}
		return nil
	}
	catalog, ok := s.store.(rehearsalpg.Catalog)
	if !ok {
		return errors.New("rehearsal PostgreSQL durable catalog is unavailable")
	}
	backend, e := pgruntime.NewPostgres(ctx, dsn)
	if e != nil {
		return errors.New("rehearsal PostgreSQL fixture cluster is not qualified or isolated")
	}
	s.rehearsalPostgres = pgruntime.NewService(catalog, backend)
	go s.rehearsalPostgres.Run(ctx)
	return nil
}

func (s *server) createScenarioPostgres(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.rehearsalPostgresEnabled || s.rehearsalPostgres == nil || !s.rehearsalCommandsEnabled || !s.rehearsalNetworkEnabled || !s.rehearsalSimulationEnabled {
		api.WriteProblem(w, api.ErrRehearsalNotEnabled("Isolated PostgreSQL fixtures"))
		return
	}
	var req rehearsalpg.CreateRequest
	if e := decodeJSON(r, &req); e != nil || req.Spec.Validate() != nil {
		api.WriteProblem(w, api.ErrValidation("invalid PostgreSQL fixture contract"))
		return
	}
	app, e := s.store.AppBySlug(r.Context(), req.AppSlug)
	if e != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such test workload")
		return
	}
	member, e := s.store.ScenarioTestMemberByApp(r.Context(), app.ID)
	if e != nil || member.AccountID != acct.ID || member.RunID != r.PathValue("run_id") || member.NetworkMode != "contained" || member.SimulationJSON == "" || !eligibleScenarioPostgresApp(app) {
		s.notFound(w, "no such contained test workload")
		return
	}
	limits, _ := api.LimitsFor(acct.Plan)
	if problem := s.checkSecretQuota(r.Context(), acct, app, "default", "DATABASE_URL", limits); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	receipt, connection, e := s.rehearsalPostgres.Create(r.Context(), acct.ID, member.RunID, app.ID, member.Workload+".svc.gregale", req)
	if e != nil {
		api.WriteProblem(w, api.ErrCapacity("create isolated PostgreSQL fixture"))
		return
	}
	if problem := s.sealAndPersist(r.Context(), acct, app, "default", "DATABASE_URL", connection, api.SecretClassEphemeral, limits); problem != nil {
		_ = s.rehearsalPostgres.Delete(context.WithoutCancel(r.Context()), acct.ID, member.RunID)
		api.WriteProblem(w, problem)
		return
	}
	if e := s.bindScenarioPostgresParticipants(r.Context(), acct, member); e != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), scenarioObjectCleanupTimeout)
		defer cancel()
		_ = s.rehearsalPostgres.Delete(cleanup, acct.ID, member.RunID)
		api.WriteProblem(w, api.ErrCapacity("PostgreSQL participant binding failed; fixture cleanup is required"))
		return
	}
	writeJSON(w, http.StatusCreated, receipt)
}

func eligibleScenarioPostgresApp(app state.App) bool {
	return app.Status != state.AppDeleted && app.PreviewOfSlug != "" && app.PreviewPrNumber == 0
}

func (s *server) sealScenarioPostgres(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.rehearsalPostgres == nil {
		api.WriteProblem(w, api.ErrCapacity("isolated PostgreSQL collector is unavailable"))
		return
	}
	var req rehearsalpg.SealRequest
	if decodeJSON(r, &req) != nil {
		api.WriteProblem(w, api.ErrValidation("deployment_id is required"))
		return
	}
	resource, e := s.rehearsalPostgres.Catalog.GetScenarioPostgres(r.Context(), acct.ID, r.PathValue("run_id"))
	if e != nil {
		s.notFound(w, "no such PostgreSQL fixture")
		return
	}
	dep, e := s.store.DeploymentByID(r.Context(), req.DeploymentID)
	if e != nil || dep.AppID != resource.AppID || resource.Evidence != nil && resource.Evidence.DeploymentID != req.DeploymentID {
		api.WriteProblem(w, api.ErrValidation("deployment does not own this fixture"))
		return
	}
	actions, ok := s.store.(state.ScenarioActionSnapshotStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sealed action evidence unavailable"))
		return
	}
	snapshot, e := actions.SealScenarioTestActions(r.Context(), acct.ID, resource.RunID)
	if e != nil || snapshot.Status != "complete" {
		api.WriteProblem(w, api.ErrValidation("complete action evidence is required before database sealing"))
		return
	}
	evidence, e := s.rehearsalPostgres.Seal(r.Context(), acct.ID, resource.RunID, dep.ID)
	if e != nil {
		api.WriteProblem(w, api.ErrCapacity("PostgreSQL evidence is incomplete; fixture cleanup is required"))
		return
	}
	writeJSON(w, http.StatusOK, evidence)
}

func (s *server) deleteScenarioPostgres(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.rehearsalPostgres == nil {
		api.WriteProblem(w, api.ErrCapacity("isolated PostgreSQL cleanup is unavailable"))
		return
	}
	if _, err := s.rehearsalPostgres.Catalog.GetScenarioPostgres(r.Context(), acct.ID, r.PathValue("run_id")); errors.Is(err, state.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if e := s.rehearsalPostgres.Delete(r.Context(), acct.ID, r.PathValue("run_id")); e != nil {
		api.WriteProblem(w, api.ErrCapacity("PostgreSQL fixture cleanup was not confirmed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
