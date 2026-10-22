//go:build metal

package e2e_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEnvironmentGitOpsNativeMetal follows a reviewed API and push worker
// through the real apid, imaged, schedd, vmmd, guest, queue and gateway
// processes. It is opt-in because the qualification dispatcher is deliberately disabled in production
// until this acceptance path is proven. Run it with private jail and network
// mounts in a disposable namespace.
func TestEnvironmentGitOpsNativeMetal(t *testing.T) {
	if os.Getenv("GREGALE_GITOPS_NATIVE_ACCEPTANCE") != "1" {
		t.Skip("isolated native GitOps acceptance only")
	}
	if !metalAvailable(t) {
		return
	}
	requireNativeGitOpsImageRefs(t)
	for _, provider := range []string{"github", "gitlab"} {
		t.Run(provider, func(t *testing.T) { runEnvironmentGitOpsNative(t, provider) })
	}
}

func runEnvironmentGitOpsNative(t *testing.T, providerName string) {
	t.Helper()
	// The image-only fixture never writes the operator's source builder base.
	t.Setenv("FAAS_BUILDER_BASE_PATH", "")
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	storageRoot := filepath.Join(t.TempDir(), "storage")
	t.Setenv("FAAS_STORAGE_ROOT", storageRoot)
	// This journey never starts a builder; only the source-Job cases below
	// use the real, report-bound builder image.
	builderImage, _ := e2etest.ImageOnlyBuilderBaseImage("onebox-faas/builder-base")
	e2etest.ConfigureNativeBaseImages(t, registry.AddImage("onebox-faas/builder-base", builderImage),
		os.Getenv("GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF"), filepath.Join(storageRoot, sched.BaseKeyForArch("builder", "amd64")))
	image, _ := e2etest.HelloGitOpsRequestImage("library/gitops-hello", helloBody)
	imageRef := registry.AddImage("library/gitops-hello", image)
	workerImage, _ := e2etest.HelloGitOpsWorkerImage("library/gitops-worker")
	workerImageRef := registry.AddImage("library/gitops-worker", workerImage)
	pendingImage, _ := e2etest.HelloGitOpsRequestImage("library/gitops-pending", "superseded revision must never serve")
	pendingImageRef := registry.AddImage("library/gitops-pending", pendingImage)
	candidateImage, _ := e2etest.HelloGitOpsRequestImage("library/gitops-next", "reviewed next revision")
	candidateImageRef := registry.AddImage("library/gitops-next", candidateImage)
	pullEntered, releasePull := registry.HoldRepository("library/gitops-pending")
	t.Cleanup(releasePull)
	provider, providerEnv := newGitOpsProviderFixture(t, providerName)
	fcVersion, err := fcvm.DetectFirecrackerVersion(t.Context())
	if err != nil {
		t.Fatalf("detect Firecracker version: %v", err)
	}
	stageNativeAcceptanceKernel(t, storageRoot, fcVersion)

	daemonEnv := []string{"FAAS_STORAGE_ROOT=" + storageRoot,
		"GREGALE_GITOPS_ACCEPTANCE=1", "FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT=1",
		"FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH=1",
		"FAAS_AUDIT_HMAC_KEY_FILE=" + gitOpsAcceptanceHMACKeyFile(t, "audit-hmac.key"),
		"FAAS_RECOVERY_HMAC_KEY_FILE=" + gitOpsAcceptanceHMACKeyFile(t, "recovery-hmac.key")}
	h := e2etest.StartWithAPIDEnv(t, pool, e2etest.DeployWake, providerEnv, daemonEnv...)
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
	connectGitOpsProvider(t, provider, store, account.ID)
	project, err := store.CreateProject(t.Context(), gitOpsProviderProject(providerName, "gitops-native", account.ID))
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api",
		WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive,
		RAMMB: 256, MaxConcurrency: 1, Manifest: state.AppManifest{Port: 8080, StartupDeadlineS: 30}})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-worker",
		WorkloadName: "worker", Type: state.AppTypeApp, Status: state.AppActive,
		WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 1,
		Manifest: state.AppManifest{Port: 8080, ExecutionMode: api.ExecutionModeWorker, StartupDeadlineS: 30}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", gitOpsProviderSource(providerName, "enforce"))
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
		Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
			"api": {App: app.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: imageRef, FullRootfs: true},
				Runtime:   json.RawMessage(`{"port":8080,"healthz":"/healthz","startup_deadline_s":30,"execution_mode":"request"}`),
				Variables: map[string]string{"MODE": "reviewed"}},
			"worker": {App: worker.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: workerImageRef, FullRootfs: true},
				Runtime: json.RawMessage(`{"port":8080,"startup_deadline_s":30,"execution_mode":"worker"}`),
				QueueBindings: map[string]api.EnvironmentQueueBinding{
					"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: 1},
				},
				QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"qualification-smoke"}`)}},
			},
		}})
	if err != nil {
		t.Fatal(err)
	}
	provider.add(t, strings.Repeat("a", 40), desired.Definition)
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
	journey := nativeGitOpsJourney{
		h: h, store: store, client: api.NewClient(h.APIDURL, key), key: key,
		project: project, app: app, worker: worker, source: source, original: desired.Definition,
		provider: provider, pendingImage: pendingImageRef, candidateImage: candidateImageRef,
		pullEntered: pullEntered, releasePull: releasePull,
	}
	journey.run(t)
}

func stageNativeAcceptanceKernel(t *testing.T, storageRoot, fcVersion string) {
	t.Helper()
	kernel, err := os.Open(os.Getenv("FAAS_TEST_KERNEL")) //nolint:forbidigo // Guarded operator fixture, fingerprinted before and after the native run.
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

}

func gitOpsProviderSource(provider, mode string) state.EnvironmentGitSourceSpec {
	spec := state.EnvironmentGitSourceSpec{Provider: provider, RepositoryID: 123, Repository: "example/shop",
		Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: mode, ApprovalPolicy: "manual"}
	if provider == "github" {
		spec.InstallationID = 42
	}
	return spec
}
