package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — consumer routes take an environment: a stage's
// consumers are listed and created there, production's list never shows
// them, and only production consumers have trials.
func TestAPIConsumersInEnvironment(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx := context.Background()
	store, account := *e.Store(), *e.Acct()
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "consumer-env"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "env-api",
		Type: state.AppTypeApp, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/apps/env-api/consumers"

	production := tenantJSON[api.APIConsumerResponse](t, e, http.MethodPost, base,
		api.CreateAPIConsumerRequest{ExternalRef: "customer-1", Name: "Acme"}, http.StatusCreated)
	staged := tenantJSON[api.APIConsumerResponse](t, e, http.MethodPost, base+"?environment=staging",
		api.CreateAPIConsumerRequest{ExternalRef: "customer-stage", Name: "Acme staging"}, http.StatusCreated)
	if production.EnvironmentID != "" || staged.EnvironmentID != stage.ID {
		t.Fatalf("environments = %q, %q; want production and %s", production.EnvironmentID, staged.EnvironmentID, stage.ID)
	}

	for _, tc := range []struct {
		path string
		want string
	}{
		{base, production.ID},
		{base + "?environment=production", production.ID},
		{base + "?environment=staging", staged.ID},
	} {
		list := tenantJSON[api.APIConsumerListResponse](t, e, http.MethodGet, tc.path, nil, http.StatusOK)
		if len(list.Consumers) != 1 || list.Consumers[0].ID != tc.want {
			t.Fatalf("GET %s = %+v, want only %s", tc.path, list.Consumers, tc.want)
		}
	}
	tenantJSON[api.Problem](t, e, http.MethodGet, base+"?environment=missing", nil, http.StatusNotFound)
	tenantJSON[api.Problem](t, e, http.MethodPost, base+"?environment=missing",
		api.CreateAPIConsumerRequest{ExternalRef: "customer-2", Name: "Missing"}, http.StatusNotFound)
	tenantJSON[api.Problem](t, e, http.MethodPost, base+"?environment=staging",
		api.CreateAPIConsumerRequest{ExternalRef: "customer-3", Name: "Trial", TrialDays: 7}, http.StatusUnprocessableEntity)
}
