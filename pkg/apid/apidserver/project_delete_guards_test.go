// Deleting a project detaches its services, so calls between them by service
// name stop resolving at once (ADR-20261104221217). Only the CLI refused that;
// the API now refuses it for every client until the caller confirms with
// detach_services=true (apps/projects review 2026-11-10, P5).
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeletingAProjectWhoseServicesCallEachOtherIsRefused(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx, store, acct := context.Background(), (*e.Store()), (*e.Acct())
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "calls"})
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{AccountID: acct.ID, ProjectID: project.ID, Slug: "calls-billing", WorkloadName: "billing", Type: state.AppTypeApp},
		{AccountID: acct.ID, ProjectID: project.ID, Slug: "calls-api", WorkloadName: "api", Type: state.AppTypeApp,
			Manifest: state.AppManifest{ServiceBindings: []api.AppServiceBinding{{Binding: "BILLING", Service: "billing"}}}},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}

	refused := e.Do(t, http.MethodDelete, "/v1/projects/calls", nil, nil)
	var problem api.Problem
	if refused.Code != http.StatusConflict || json.Unmarshal(refused.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodeProjectServicesCallEachOther || !strings.Contains(problem.Detail, "api -> billing") {
		t.Fatalf("delete of a project whose services call each other = %d %s", refused.Code, refused.Body)
	}
	if _, err := store.ProjectBySlug(ctx, acct.ID, "calls"); err != nil {
		t.Fatalf("project after refused delete: %v", err)
	}
	if w := e.Do(t, http.MethodDelete, "/v1/projects/calls?detach_services=true", nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("confirmed delete = %d %s", w.Code, w.Body)
	}
	if app, err := store.AppBySlug(ctx, "calls-api"); err != nil || app.ProjectID != "" {
		t.Fatalf("api after project delete = %+v, %v; want a live standalone app", app, err)
	}
}
