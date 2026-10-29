package apidserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reserving an app in one environment (?environment=) writes only that
// environment's workload settings, which the reaper applies to its
// deployments. Such an app counts toward the plan's reserved-tier cap
// (issue #475) once, as an app whose own row is reserved does, however many
// of its environments reserve it.
func TestEnvironmentReservationsCountTowardTheReservedCap(t *testing.T) {
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	ram, concurrency := 256, 1
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram, MaxConcurrency: &concurrency}); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"staging", "qa"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	for _, slug := range []string{"solo", "solo-2"} {
		if _, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive,
			RAMMB: ram, MaxConcurrency: concurrency, IdleTimeoutS: 60}); err != nil {
			t.Fatal(err)
		}
	}
	// Pro reserves two apps.
	for _, step := range []struct {
		slug, environment string
		refused           bool
	}{
		{app.Slug, "staging", false},
		{"solo", "", false},
		// The api app already holds a reservation through staging.
		{app.Slug, "qa", false},
		{"solo-2", "", true},
	} {
		path := "/v1/apps/" + step.slug
		if step.environment != "" {
			path += "?environment=" + step.environment
		}
		req, rec := apidserver.ProjectRequest(http.MethodPatch, path, step.slug, []byte(`{"eviction_priority":"reserved"}`))
		if step.environment != "" {
			req.Header.Set("If-Workload-Revision", "0")
		}
		srv.UpdateApp(rec, req, account)
		refused := rec.Code == http.StatusUnprocessableEntity && strings.Contains(rec.Body.String(), api.CodePlanEvictionPriorityReservedQuota)
		if refused != step.refused || !step.refused && rec.Code != http.StatusOK {
			t.Fatalf("reserve %s: %d %s, want refused=%v", path, rec.Code, rec.Body, step.refused)
		}
	}
}

// A preview scales to zero and is never protected from eviction: it does
// not inherit its parent's reserved tier, which only a PATCH admits against
// the plan's cap, as GitHub pull-request previews do not.
func TestCreatePreviewDoesNotInheritTheReservedTier(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	if _, err := store.CreateAppIfUnderQuota(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: "acme",
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, Status: state.AppActive,
		EvictionPriority: string(api.EvictionPriorityReserved)}, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPost, "/v1/apps/acme/previews", api.CreatePreviewRequest{PRNumber: 7}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create preview = %d %s", rec.Code, rec.Body)
	}
	preview, err := store.AppBySlug(context.Background(), "pr-7-acme")
	if err != nil {
		t.Fatal(err)
	}
	if preview.EvictionPriority != string(api.EvictionPriorityBestEffort) {
		t.Fatalf("preview eviction priority = %q, want best_effort", preview.EvictionPriority)
	}
}
