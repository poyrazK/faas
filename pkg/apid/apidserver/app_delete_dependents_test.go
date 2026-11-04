// Deleting one project service with `gregale app delete` left the services
// bound to it pointing at nothing: their calls failed and their next deploy
// failed the dependency gate. The delete is refused while a sibling depends
// on the service; a reviewed deploy that removes it from Compose is the path.
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

func TestDeletingAProjectServiceOthersDependOnIsRefused(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx, store, acct := context.Background(), (*e.Store()), (*e.Acct())
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(slug, service string, manifest state.AppManifest) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: slug, WorkloadName: service, Type: state.AppTypeApp, Manifest: manifest})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	create("shop-billing", "billing", state.AppManifest{})
	create("shop-api", "api", state.AppManifest{ServiceBindings: []api.AppServiceBinding{{Binding: "BILLING", Service: "billing"}}})
	create("shop-worker", "worker", state.AppManifest{ProjectDependencyConditions: map[string]string{"billing": "service_healthy"}})
	create("shop-web", "web", state.AppManifest{ServiceBindings: []api.AppServiceBinding{{Binding: "API", Service: "api"}}})

	refused := e.Do(t, http.MethodDelete, "/v1/apps/shop-billing", nil, nil)
	var problem api.Problem
	if refused.Code != http.StatusConflict || json.Unmarshal(refused.Body.Bytes(), &problem) != nil || problem.Code != "app_has_dependents" ||
		!strings.Contains(problem.Detail, "shop-api, shop-worker") || strings.Contains(problem.Detail, "shop-web") || !strings.Contains(problem.Hint, "reviewed deploy") {
		t.Fatalf("delete of a depended-on service = %d %s", refused.Code, refused.Body)
	}
	if app, err := store.AppBySlug(ctx, "shop-billing"); err != nil || app.Status == state.AppDeleted {
		t.Fatalf("billing after refused delete = %+v, %v", app, err)
	}
	// Nothing depends on web, so it deletes as before.
	if w := e.Do(t, http.MethodDelete, "/v1/apps/shop-web", nil, nil); w.Code >= 300 {
		t.Fatalf("delete of a leaf service = %d %s", w.Code, w.Body)
	}
}
