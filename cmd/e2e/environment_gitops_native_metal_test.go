//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEnvironmentGitOpsNativeMetal follows one reviewed image through the
// real apid, imaged, schedd, vmmd, guest and gateway processes. It is opt-in
// because the qualification dispatcher is deliberately disabled in production
// until this acceptance path is proven. Native process recovery is separate
// from this serving graph and remains unqualified with restart quarantine.
// Run it with a private /srv/fc/jail mount in a disposable mount namespace.
func TestEnvironmentGitOpsNativeMetal(t *testing.T) {
	if os.Getenv("GREGALE_GITOPS_NATIVE_ACCEPTANCE") != "1" {
		t.Skip("isolated native GitOps acceptance only")
	}
	if !metalAvailable(t) {
		return
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	image, _ := e2etest.HelloImage("library/gitops-hello", helloBody)
	imageRef := registry.AddImage("library/gitops-hello", image)
	storageRoot := filepath.Join(t.TempDir(), "storage")
	fcVersion, err := fcvm.DetectFirecrackerVersion(t.Context())
	if err != nil {
		t.Fatalf("detect Firecracker version: %v", err)
	}
	kernel, err := os.Open(os.Getenv("FAAS_TEST_KERNEL"))
	if err != nil {
		t.Fatal(err)
	}
	defer kernel.Close()
	kernelPath := filepath.Join(storageRoot, sched.KernelKey(fcVersion))
	if err := os.MkdirAll(filepath.Dir(kernelPath), 0o700); err != nil {
		t.Fatal(err)
	}
	stagedKernel, err := os.OpenFile(kernelPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(stagedKernel, kernel); err != nil {
		stagedKernel.Close()
		t.Fatal(err)
	}
	if err := stagedKernel.Close(); err != nil {
		t.Fatal(err)
	}

	h := e2etest.Start(t, pool, e2etest.DeployWake,
		"FAAS_STORAGE_ROOT="+storageRoot,
		"GREGALE_GITOPS_ACCEPTANCE=1", "FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT=1",
		"FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH=1",
		"FAAS_AUDIT_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "audit-hmac.key"),
		"FAAS_RECOVERY_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "recovery-hmac.key"))
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(t.Context(), api.PlanPro, "gitops-native")
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(t.Context(), "e2e+pro+gitops-native@test.example")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "gitops-native",
		RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api",
		WorkloadName: "api", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive,
		RAMMB: 256, MaxConcurrency: 1, Manifest: state.AppManifest{Port: 8080, StartupDeadlineS: 30}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main",
		ManifestPath: "environments/production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
		Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
			"api": {App: app.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: imageRef, FullRootfs: true},
				Runtime:   json.RawMessage(`{"port":8080,"healthz":"/healthz","startup_deadline_s":30,"execution_mode":"request"}`),
				Variables: map[string]string{"MODE": "reviewed"}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{
		AccountID: account.ID, SourceID: source.ID, CommitSHA: strings.Repeat("a", 40), Desired: desired,
		ApprovedBy: "acceptance-owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), account.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption preview: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), account.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		current, err := store.EnvironmentGitSource(ctx, account.ID, project.ID, "production")
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			t.Fatal(err)
		}
		if current.AppliedRevisionID == source.ApprovedRevisionID {
			visibleBody, visibleStatus := doReq(t, h, key, http.MethodGet,
				"/v1/projects/"+project.Slug+"/environments/production/gitops", nil)
			var visible api.EnvironmentGitOpsStatusResponse
			if visibleStatus != http.StatusOK || json.Unmarshal(visibleBody, &visible) != nil ||
				visible.Source.AppliedRevisionID != source.ApprovedRevisionID {
				t.Fatalf("HTTP GitOps status omitted serving revision: %d %s", visibleStatus, visibleBody)
			}
			client := h.HTTPClient()
			body, status := doGetWithHost(t, client, gatewayAppURL(h, app.Slug), app.Slug+".apps.test.example", 30*time.Second)
			if status != 200 || !strings.Contains(string(body), helloBody) {
				t.Fatalf("approved revision did not serve through gateway: status=%d body=%s", status, body)
			}
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
	runs, _ := store.ListEnvironmentGitOpsRuns(context.Background(), account.ID, source.ID, 3)
	t.Fatalf("native GitOps revision did not reach serving before timeout: runs=%+v", runs)
}
