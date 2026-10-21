package deployreadiness

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReadinessServiceAdditionsAreReadOnlyAndQualified(t *testing.T) {
	store := state.NewMemStore()
	account, project, app := seedReadiness(t, store)
	app.Manifest.RevisionPinTTLSeconds = 1800
	_, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest})
	must(t, err)
	seedReadinessGraph(t, store, account, project, app, "production")
	request := readinessRequest()
	request.Atomic, request.AddServices = true, true
	request.Workloads = append(request.Workloads, api.ProjectDeploymentCheckWorkload{Workload: api.PlanWorkload{Name: "metrics", RootDir: "metrics", Class: "http"}})
	blocked := Check(t.Context(), store, account, project.Slug, request, Config{})
	if countCode(blocked, "service_additions_unqualified") != 1 {
		t.Fatal("qualification gate omitted", blocked)
	}
	ready := Check(t.Context(), store, account, project.Slug, request, Config{ServiceAdditionsEnabled: true})
	if ready.Status != "ready" {
		t.Fatalf("supported source addition did not pass: %+v", ready)
	}
	apps, err := store.AppsForProject(t.Context(), account.ID, project.ID)
	must(t, err)
	if len(apps) != 1 {
		t.Fatal("readiness reserved an app")
	}
	request.Workloads = request.Workloads[1:]
	if removed := Check(t.Context(), store, account, project.Slug, request, Config{ServiceAdditionsEnabled: true}); countCode(removed, "atomic_source_incompatible") != 1 {
		t.Fatal("removal passed as addition", removed)
	}
}
