package apidserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/conditions"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppStatus is GET /v1/apps/{slug}/status (ADR-20261030151810): the app's
// conditions, derived from the same health assessment as /health. Nothing is
// stored and nothing is woken.
func (s *server) getAppStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, assessment, ok := s.collectAppHealth(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, conditions.App(app.Slug, assessment, time.Now()))
}

// getDeploymentStatus is GET /v1/deployments/{id}/status: the deployment's
// conditions, mapped from the response GET /v1/deployments/{id} returns.
func (s *server) getDeploymentStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, err := s.store.DeploymentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, conditions.Deployment(s.deploymentResponse(r.Context(), d, app), time.Now()))
}

// getDatabaseStatus is GET /v1/postgres/databases/{id}/status: the managed
// database's conditions, mapped from the response GET
// /v1/postgres/databases/{id} returns, cached health included.
func (s *server) getDatabaseStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	database, err := s.managedPostgres.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, conditions.Database(managedPostgresView(database), time.Now()))
}

// getDomainStatus is GET /v1/domains/{domain}/status: the domain's
// conditions from its durable row. Unlike GET /v1/domains/{domain} it never
// dials the domain, so a wait can poll it.
func (s *server) getDomainStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, ok := s.loadDomain(w, r, acct, strings.ToLower(r.PathValue("domain")))
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, conditions.Domain(domainResponse(d), time.Now()))
}

// getCronStatus is GET /v1/crons/{id}/status: the cron's conditions from its
// row and its newest run, read as GET /v1/crons/{id}/runs reads them.
func (s *server) getCronStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	c, err := s.store.CronByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(r.Context(), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	last, err := s.newestCronRun(r.Context(), c)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("read cron runs"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, conditions.Cron(cronResponse(c), last, time.Now()))
}

func (s *server) newestCronRun(ctx context.Context, c state.Cron) (*api.CronRun, error) {
	if len(c.Command) > 0 {
		tasks, err := s.store.ListCronAppTaskRuns(ctx, c.ID, 1, "")
		if err != nil || len(tasks) == 0 {
			return nil, err
		}
		run := cronRunFromAppTask(tasks[0])
		return &run, nil
	}
	invocations, err := s.store.ListCronRunsForCron(ctx, c.ID, 1, "")
	if err != nil || len(invocations) == 0 {
		return nil, err
	}
	run := cronRunFromInvocation(invocations[0])
	return &run, nil
}

// getJobRunStatus is GET /v1/jobs/{name}/runs/{id}/status: the run's
// conditions, mapped from the response GET /v1/jobs/{name}/runs/{id} returns.
func (s *server) getJobRunStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("id")
	job, run, ok, err := s.resolveJobRun(r.Context(), runID, acct)
	if err != nil {
		s.log.Error("get job run status failed", "id", runID, "account", acct.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not get run"))
		return
	}
	if !ok || job.Name != r.PathValue("name") {
		s.notFound(w, "no such run")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, conditions.JobRun(jobRunResponse(run, job), time.Now()))
}
