//go:build metal

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Validate fixture admission before spending native builder/guest time. This
// test does not qualify VM execution; the release scenario does that separately.
func TestProjectWorkerReleaseFixtureMeta(t *testing.T) {
	t.Run("migration_contract", qualifyProjectMigrationContractFixtureMeta)
	t.Run("service_graph", func(t *testing.T) {
		store := state.NewMemStore()
		account, err := store.CreateAccount(t.Context(), "service-fixture@example.test", api.PlanPro)
		mustProjectWorkerNative(t, err)
		project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "service-fixture"})
		mustProjectWorkerNative(t, err)
		scan, err := reposcan.Scan(projectServiceGraphNativeFiles([]byte("fixture"), "stable"))
		mustProjectWorkerNative(t, err)
		plan, err := reconcile.NewService(store, nil, nil).Plan(t.Context(), project, scan, "", "", nil)
		mustProjectWorkerNative(t, err)
		if len(plan.Added) != 2 {
			t.Fatal("service fixture lost membership")
		}
		admitted, err := reconcile.ProjectSafeSourceApps(plan.Added, scan)
		mustProjectWorkerNative(t, err)
		if len(admitted) != 2 {
			t.Fatal("service fixture not eligible for atomic HTTP deploy")
		}
		found := false
		for _, app := range admitted {
			if app.WorkloadName == "frontend" {
				found = len(app.Manifest.ServiceBindings) == 1 && app.Manifest.ServiceBindings[0].Service == "backend"
			}
		}
		if !found {
			t.Fatal("frontend has no declared backend binding")
		}
	})
	t.Run("http_only", func(t *testing.T) {
		scan, err := reposcan.Scan(projectHTTPNativeFiles([]byte("fixture"), "stable"))
		mustProjectWorkerNative(t, err)
		store := state.NewMemStore()
		account, err := store.CreateAccount(t.Context(), "http-fixture@example.test", api.PlanPro)
		mustProjectWorkerNative(t, err)
		project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "http-fixture"})
		mustProjectWorkerNative(t, err)
		plan, err := reconcile.NewService(store, nil, nil).Plan(t.Context(), project, scan, "", "", nil)
		mustProjectWorkerNative(t, err)
		if len(plan.Added) != 1 || plan.Added[0].WorkloadName != "frontend" {
			t.Fatal("HTTP fixture membership changed")
		}
		admitted, err := reconcile.ProjectSafeSourceApps(plan.Added, scan)
		mustProjectWorkerNative(t, err)
		if len(admitted) != 1 {
			t.Fatal("HTTP fixture requires worker admission")
		}
	})
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "worker-fixture@example.test", api.PlanPro)
	mustProjectWorkerNative(t, err)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "worker-fixture"})
	mustProjectWorkerNative(t, err)
	binary := []byte("fixture executable placeholder")
	files := projectWorkerNativeFiles(binary, "stable", false)
	scan, err := reposcan.Scan(files)
	mustProjectWorkerNative(t, err)
	planned, err := reconcile.NewService(store, nil, nil).Plan(t.Context(), project, scan, "", "", nil)
	mustProjectWorkerNative(t, err)
	if len(planned.Added) != 2 {
		t.Fatalf("native fixture must produce two workloads: %+v", scan.Workloads)
	}
	apps := planned.Added
	for i := range apps {
		if apps[i].WorkloadName == "processor" {
			apps[i].Manifest.ExecutionMode = api.ExecutionModeWorker
		}
	}
	for _, broken := range []bool{false, true} {
		candidate, err := reposcan.Scan(projectWorkerNativeFiles(binary, "candidate", broken))
		mustProjectWorkerNative(t, err)
		admitted, err := reconcile.ProjectSafeSourceApps(apps, candidate, false, true)
		mustProjectWorkerNative(t, err)
		if len(admitted) != 2 {
			t.Fatal("candidate source changed membership instead of source bytes")
		}
		if _, err := reconcile.ProjectSafeSourceApps(apps, candidate); err == nil {
			t.Fatal("native worker fixture accidentally qualifies as an ordinary HTTP release")
		}
	}
	archive := projectWorkerNativeArchive(t, files)
	if !bytes.Equal(archive, projectWorkerNativeArchive(t, files)) {
		t.Fatal("separate scan/apply archives would invalidate the plan token")
	}
	zip, err := gzip.NewReader(bytes.NewReader(archive))
	mustProjectWorkerNative(t, err)
	defer func() { _ = zip.Close() }()
	reader := tar.NewReader(zip)
	executable := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		mustProjectWorkerNative(t, err)
		if header.Name == "projectworker" {
			executable = header.Mode&0111 != 0
			data, err := io.ReadAll(reader)
			mustProjectWorkerNative(t, err)
			if !bytes.Equal(data, binary) {
				t.Fatal("fixture archive changed executable bytes")
			}
		}
	}
	if !executable {
		t.Fatal("source packaging lost the worker executable bit")
	}
}

func qualifyProjectMigrationContractFixtureMeta(t *testing.T) {
	for _, mode := range []string{"success", "failure", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			files := projectContractNativeFiles([]byte("fixture executable placeholder"), mode)
			scan, err := reposcan.Scan(files)
			mustProjectWorkerNative(t, err)
			owners := 0
			for _, workload := range scan.Workloads {
				if len(workload.ReleaseCommand) != 0 {
					owners++
					if workload.Name != "frontend" || !workload.ReleaseCommandShell || workload.ReleaseCommand[0] != "/projectworker migration-expand" {
						t.Fatalf("wrong expansion owner: %+v", workload)
					}
				}
			}
			if len(scan.Workloads) != 2 || owners != 1 {
				t.Fatalf("fixture membership/expansion owners: %+v", scan.Workloads)
			}
			manifest, err := gregalemanifest.ParseBytes(files["gregale.yaml"].Data)
			mustProjectWorkerNative(t, err)
			if manifest.Release == nil || manifest.Release.Command != "" || manifest.Release.ContractCommand != "/projectworker migration-"+mode {
				t.Fatalf("cleanup source policy: %+v", manifest.Release)
			}
			if !bytes.Equal(projectWorkerNativeArchive(t, files), projectWorkerNativeArchive(t, files)) {
				t.Fatal("scan/apply archive identity changed")
			}
		})
	}
}
