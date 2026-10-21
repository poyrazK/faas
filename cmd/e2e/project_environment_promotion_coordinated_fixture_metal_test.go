//go:build promotionacceptance && metal

package e2e_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func promotionCoordinatedNativeFiles(binary []byte, version string) fstest.MapFS {
	generation := version
	if version == "repair" {
		generation = "tested"
	}
	files := promotionServiceGraphNativeFiles(binary, generation)
	worker := environmentPromotionNativeFiles(binary, generation)
	files["Dockerfile.processor"] = worker["Dockerfile.processor"]
	files["docker-compose.yml"].Data = append(files["docker-compose.yml"].Data, []byte("  processor:\n    build: { context: ., dockerfile: Dockerfile.processor }\n")...)
	files["Dockerfile.backend"].Data = append(files["Dockerfile.backend"].Data, []byte("COPY projectworker /bin/sh\n")...)
	if version == "repair" {
		for _, role := range []string{"frontend", "backend", "processor"} {
			files["Dockerfile."+role].Data = append(files["Dockerfile."+role].Data, []byte("ENV REPAIR_ARTIFACT=1\n")...)
		}
	}
	if version == "tested" {
		files["Procfile"] = &fstest.MapFile{Data: []byte("release: /projectworker migration-promotion\n")}
	}
	return files
}

func TestProjectEnvironmentPromotionCoordinatedFixtureMeta(t *testing.T) {
	for _, version := range []string{"stable", "baseline", "tested", "repair"} {
		scan, err := reposcan.Scan(promotionCoordinatedNativeFiles([]byte("fixture"), version))
		mustProjectWorkerNative(t, err)
		store := state.NewMemStore()
		account, err := store.CreateAccount(t.Context(), "coordinated@example.test", api.PlanScale)
		mustProjectWorkerNative(t, err)
		project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "coordinated"})
		mustProjectWorkerNative(t, err)
		plan, err := reconcile.NewService(store, nil, nil).Plan(t.Context(), project, scan, "", "", nil)
		mustProjectWorkerNative(t, err)
		if len(plan.Added) != 3 {
			t.Fatal("coordinated fixture needs frontend, API and worker")
		}
		for _, app := range plan.Added {
			if app.WorkloadName == "processor" && app.WorkloadClass != state.WorkloadClassWorker {
				t.Fatal("processor lost worker classification")
			}
		}
		for _, w := range scan.Workloads {
			want := version == "tested" && w.Name == "backend"
			if (len(w.ReleaseCommand) > 0) != want || want && !strings.Contains(strings.Join(w.ReleaseCommand, " "), "/projectworker migration-promotion") {
				t.Fatal("migration must belong only to the tested API")
			}
			if w.Name == "frontend" && len(w.DependsOn) != 1 {
				t.Fatal("frontend lost API dependency")
			}
		}
	}
}
