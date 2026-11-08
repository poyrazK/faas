// adr: 20261107090000
package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Removing a service's published ports makes its existing app internal on the
// next production deploy; it used to stay public.
func TestProjectDeployMakesUnpublishedServiceInternal(t *testing.T) {
	spool := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	e, _ := apidserver.NewTestServerWithCapturingNotifier(t, api.PlanPro)
	published := "services:\n  admin:\n    image: nginx:1.27\n    ports: [\"8080:80\"]\n"
	first := applyEnvironmentProject(t, *e.H(), *e.Key(), "", published)
	if len(first.Builds) != 1 {
		t.Fatalf("builds = %+v", first.Builds)
	}
	store := *e.Store()
	app, err := store.AppByID(t.Context(), first.Builds[0].AppID)
	if err != nil || api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
		t.Fatalf("admin starts internal: %+v, %v", app.Visibility, err)
	}
	unpublished := "services:\n  admin:\n    image: nginx:1.28\n    expose: [\"80\"]\n"
	applyEnvironmentProject(t, *e.H(), *e.Key(), "", unpublished)
	app, err = store.AppByID(t.Context(), app.ID)
	if err != nil || api.NormalizeAppVisibility(app.Visibility) != api.AppVisibilityInternal {
		t.Fatalf("admin visibility = %q, %v; want internal once Compose stops publishing it", app.Visibility, err)
	}
}
