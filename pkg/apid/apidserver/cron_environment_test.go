package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261104120000: a cron is created in one environment of a project app.
// A stage takes HTTP and command crons; an unknown environment and an app
// outside a project are refused with a stable problem.
func TestCreateCronInEnvironment(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	store := *e.Store()
	acct := *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "cron-env"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "cron-env-api",
		Type: state.AppTypeApp, Status: state.AppActive, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	loose := apidserver.MustSeedApp(t, e, "cron-env-loose")
	command := []string{"bin/report"}

	for _, tc := range []struct {
		name   string
		req    api.CreateCronRequest
		status int
		env    string
	}{
		{"production command", api.CreateCronRequest{AppID: app.ID, Schedule: "0 9 * * *", Command: command}, http.StatusCreated, ""},
		{"staging command", api.CreateCronRequest{AppID: app.ID, Schedule: "0 9 * * *", Command: command, Environment: "staging"}, http.StatusCreated, "staging"},
		{"staging HTTP", api.CreateCronRequest{AppID: app.ID, Schedule: "0 9 * * *", Path: "/tick", Environment: "staging"}, http.StatusCreated, "staging"},
		{"unknown environment", api.CreateCronRequest{AppID: app.ID, Schedule: "0 9 * * *", Command: command, Environment: "qa"}, http.StatusNotFound, ""},
		{"app outside a project", api.CreateCronRequest{AppID: loose, Schedule: "0 9 * * *", Command: command, Environment: "staging"}, http.StatusBadRequest, ""},
		{"production by name", api.CreateCronRequest{AppID: loose, Schedule: "0 10 * * *", Command: command, Environment: "production"}, http.StatusCreated, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.Do(t, http.MethodPost, "/v1/crons", tc.req, nil)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status == http.StatusBadRequest {
				wantCronProblem(t, rec, http.StatusBadRequest, api.CodeCronInvalid)
			}
			if tc.status != http.StatusCreated {
				return
			}
			var got api.CronResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Environment != tc.env {
				t.Fatalf("environment = %q, want %q", got.Environment, tc.env)
			}
		})
	}
}
