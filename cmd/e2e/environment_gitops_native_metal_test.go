//go:build metal

package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
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
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	// Keep the fixture independent of external DNS while exercising the
	// production fallback for a validated, already-published local base.
	e2etest.PointDeployBaseAtUnreachable(t, registry.Host()+"/acceptance-base:unreachable")
	storageRoot := filepath.Join(t.TempDir(), "storage")
	t.Setenv("FAAS_STORAGE_ROOT", storageRoot)
	stageNativeAcceptanceBase(t, storageRoot)
	builderImage, _ := e2etest.ImageOnlyBuilderBaseImage("onebox-faas/builder-base")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImage))
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
	provider := newGitOpsSourceFixture(t)
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
		"FAAS_STORAGE_ROOT="+storageRoot, "FAAS_GITHUBD_SOCKET="+provider.socket,
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
	provider.setAccount(account.ID)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "gitops-native",
		RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
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
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main",
		ManifestPath: "environments/production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
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

// A full-rootfs deployment still boots guest-init from drive0. Patch the
// accepted host base through the production copy-and-verify path so the
// private image contains the exact guest-init binary used by this test.
// Never mutate the host's shared /srv/fc tree during acceptance.
func stageNativeAcceptanceBase(t *testing.T, storageRoot string) {
	t.Helper()
	source := strings.TrimSpace(os.Getenv("FAAS_TEST_MINIMAL_BASE_PATH"))
	if source == "" {
		t.Fatal("FAAS_TEST_MINIMAL_BASE_PATH must name a validated generic base ext4")
	}
	guestInitPath := strings.TrimSpace(os.Getenv("FAAS_GUEST_INIT"))
	guestInit, err := os.Open(guestInitPath)
	if err != nil {
		t.Fatalf("open native acceptance guest-init: %v", err)
	}
	guestHash := sha256.New()
	_, hashErr := io.Copy(guestHash, guestInit)
	closeErr := guestInit.Close()
	if hashErr != nil || closeErr != nil {
		t.Fatalf("hash native acceptance guest-init: read=%v close=%v", hashErr, closeErr)
	}
	guestDigest := hex.EncodeToString(guestHash.Sum(nil))
	key := sched.BaseKeyForArch("", "amd64")
	backend, err := storage.NewLocalStorageBackend(storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rootfs.NewBuilder(wire.ExecRunner{}).PatchBaseGuestInit(t.Context(), rootfs.BasePatchInput{
		SourceImage: source, GuestInitPath: guestInitPath, GuestInitSHA256: guestDigest,
		Storage: backend, StorageKey: key,
	}); err != nil {
		t.Fatalf("patch private native acceptance base: %v", err)
	}
	dest := filepath.Join(storageRoot, filepath.FromSlash(key))
	sourceDigest, err := os.ReadFile(source + ".digest")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sourceDigest)), "\n")
	updated := false
	for i, line := range lines {
		if strings.HasPrefix(line, "guest-init-sha256=") {
			lines[i] = "guest-init-sha256=" + guestDigest
			updated = true
		}
	}
	if !updated {
		t.Fatal("native acceptance base digest sidecar has no guest-init provenance")
	}
	if err := os.WriteFile(dest+".digest", []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	image, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	contentHash := sha256.New()
	size, hashErr := io.Copy(contentHash, image)
	closeErr = image.Close()
	if hashErr != nil || closeErr != nil {
		t.Fatalf("hash private native acceptance base: read=%v close=%v", hashErr, closeErr)
	}
	content, err := json.Marshal(struct {
		Version int    `json:"version"`
		SHA256  string `json:"sha256"`
		Size    int64  `json:"size"`
	}{Version: 1, SHA256: "sha256:" + hex.EncodeToString(contentHash.Sum(nil)), Size: size})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".content", content, 0o644); err != nil {
		t.Fatal(err)
	}
	// This locally patched base is a lifecycle fixture, not Grype evidence.
	// Model the admission sidecar explicitly so the VM can exercise the
	// reviewed workload's native boot and qualification path. The image
	// identity must match vmmd's configured test-only base generation.
	baseRef := os.Getenv("FAAS_TEST_DEPLOY_BASE_REF")
	if baseRef == "" {
		t.Fatal("native acceptance base has no configured generation")
	}
	scan, err := json.Marshal(struct {
		Image                string         `json:"image"`
		Findings             map[string]int `json:"findings"`
		FixAvailableFindings map[string]int `json:"fix_available_findings"`
	}{
		Image:    baseRef,
		Findings: map[string]int{"CRITICAL": 0}, FixAvailableFindings: map[string]int{"CRITICAL": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(t.Context(), wire.ScanKeyForBaseKey(key), bytes.NewReader(scan)); err != nil {
		t.Fatalf("publish private base admission fixture: %v", err)
	}
}
