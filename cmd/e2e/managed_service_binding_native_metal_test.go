//go:build metal && managed_service_native

package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/servicestarter"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const serviceNativeScope = servicestarter.Scope
const serviceNativeManifestPath = servicestarter.ManifestPath

type serviceNativeJourney struct {
	h                                                       *e2etest.Harness
	store                                                   *state.PgStore
	client                                                  *api.Client
	provider                                                *gitOpsSourceFixture
	project                                                 state.Project
	caller, target, worker                                  state.App
	definition                                              api.EnvironmentDefinition
	sourceFiles                                             map[string][]byte
	sourceWorkerBuildID                                     string
	nextImage, key, cli, cliHome, fixtureDir, policyControl string
	spec                                                    api.ManagedServiceProbeSpec
	artifacts                                               map[string]string
	manifest                                                servicestarter.RuntimeManifest
	tasks                                                   map[string]bool
	workerTasks                                             map[string]bool
	queueSentinel                                           *state.Invocation
	canaries                                                map[string]api.QueueBindingCanary
	runs                                                    map[string]bool
}

// This suite owns every daemon, source fixture, certificate and guest. A skipped
// phase cannot qualify, and neither fixture scan evidence nor this guest report
// is provider approval or production release authority.
func TestManagedServiceBindingNativeMetal(t *testing.T) {
	serviceNativePrerequisites(t)
	var j, worker *serviceNativeJourney
	verified := false
	t.Cleanup(func() {
		if t.Failed() || !verified {
			return
		}
		encoded, err := json.Marshal(j.manifest)
		if err != nil {
			t.Error(err)
			return
		}
		t.Log(servicestarter.IdentityPrefix + string(encoded))
	})
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Fatal("disposable service catalog missing")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	j = newServiceNativeJourney(t, pool)
	phase := func(name string, fn func(*testing.T)) {
		t.Helper()
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}
	phase("reviewed_graph_and_custom_https_env", func(t *testing.T) {
		j.approve(t, strings.Repeat("a", 40), "initial_definition", true)
		j.refreshSpec(t)
		if j.spec.Binding != "BILLING_ENDPOINT" || j.spec.Service != "billing" {
			t.Fatal("custom frozen HTTPS edge missing")
		}
		j.captureLayers(t, "caller_app_layer", "initial_target_app_layer")
		worker = j.workerView()
		worker.refreshSpec(t)
		if worker.spec.Binding != "WORKER_BILLING_ENDPOINT" {
			t.Fatal("custom frozen worker HTTPS binding missing")
		}
		j.captureWorkerLayer(t, worker.spec.CallerDeploymentID, "worker_app_layer")
	})
	phase("single_cli_and_refreshed_receipt", func(t *testing.T) { j.verifyCLI(t, false) })
	phase("all_cli_requires_complete", func(t *testing.T) { j.verifyCLI(t, true) })
	phase("worker_single_probe_and_task_isolation", func(t *testing.T) {
		worker.seedQueueSentinel(t)
		worker.verifyCLI(t, false)
		worker.assertWorkerTaskIsolation(t)
	})
	phase("worker_all_completes_https_and_queue", func(t *testing.T) { worker.verifyCLI(t, true) })
	phase("source_built_worker_strict_verification", func(t *testing.T) {
		old := worker.spec
		files, err := e2etest.ManagedServiceWorkerSource()
		j.sourceFiles = files
		if err != nil {
			t.Fatal(err)
		}
		j.definition = servicestarter.WithSourceWorker(j.definition)
		j.approve(t, strings.Repeat("d", 40), "source_definition", false, func() { worker.rejectQueueCanaryEvidence(t, old.ReleaseID) })
		worker.refreshSpec(t)
		worker.rejectOldReceipt(t, old)
		j.captureSourceWorker(t, worker.spec.CallerDeploymentID, "source_worker")
		worker.verifyCLI(t, true)
		j.refreshSpec(t)
		j.verifyCLI(t, true)
	})
	phase("conflicting_target_and_release_rejected", func(t *testing.T) {
		before := j.taskCount(t)
		_, err := j.client.CreateAppTask(t.Context(), j.caller.Slug, api.CreateAppTaskRequest{VerificationDeploymentID: j.spec.CallerDeploymentID, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, "billing", uuid.NewString()}})
		serviceNativeProblem(t, err, http.StatusConflict)
		// The release context is server-selected. Even a well-formed internal guest
		// payload cannot appoint a different release through public task admission.
		forged := j.spec
		forged.ReleaseID = uuid.NewString()
		_, err = j.client.CreateAppTask(t.Context(), j.caller.Slug, api.CreateAppTaskRequest{VerificationDeploymentID: j.spec.CallerDeploymentID, Command: forged.Command()})
		serviceNativeProblem(t, err, http.StatusBadRequest)
		if j.taskCount(t) != before {
			t.Fatal("conflicting pins admitted guest work")
		}
		j.requireReceipt(t, j.spec, "")
	})
	phase("release_change_invalidates_receipt", func(t *testing.T) {
		old := j.spec
		target := j.definition.Workloads["billing"]
		target.Source.Image = j.nextImage
		j.definition.Workloads["billing"] = target
		j.approve(t, strings.Repeat("b", 40), "next_definition", false, func() { worker.rejectQueueCanaryEvidence(t, worker.spec.ReleaseID) })
		j.refreshSpec(t)
		if old.ReleaseID == j.spec.ReleaseID || old.TargetDeploymentID == j.spec.TargetDeploymentID || old.ContextRevision == j.spec.ContextRevision {
			t.Fatal("reviewed target change reused old routing identity")
		}
		j.rejectOldReceipt(t, old)
		j.captureLayers(t, "next_caller_app_layer", "next_target_app_layer")
		oldWorker := worker.spec
		worker.refreshSpec(t)
		if oldWorker.ReleaseID == worker.spec.ReleaseID || oldWorker.TargetDeploymentID == worker.spec.TargetDeploymentID || oldWorker.ContextRevision == worker.spec.ContextRevision {
			t.Fatal("worker retained its old release context")
		}
		worker.rejectOldReceipt(t, oldWorker)
		j.captureWorkerLayer(t, worker.spec.CallerDeploymentID, "next_worker_app_layer")
		j.captureSourceWorker(t, worker.spec.CallerDeploymentID, "rebuilt_worker")
		worker.verifyCLI(t, true)
		j.verifyCLI(t, true)
	})
	phase("policy_change_invalidates_receipt", func(t *testing.T) {
		old := j.spec
		request := api.UpdateAppRequest{AllowedServiceCallers: json.RawMessage(`["binding-native-caller"]`)}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(j.fixtureDir, "service-policy-request.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		j.artifacts["service_policy_request"] = path
		// Service dependency snapshots deliberately cover the account namespace.
		// Change an owned standalone policy through its supported owner API; do not
		// inject unsupported fields into a frozen environment runtime or edit SQL.
		if _, err := j.client.UpdateApp(t.Context(), j.policyControl, request); err != nil {
			t.Fatal(err)
		}
		j.refreshSpec(t)
		if old.ReleaseID != j.spec.ReleaseID || old.CallerDeploymentID != j.spec.CallerDeploymentID || old.TargetDeploymentID != j.spec.TargetDeploymentID || old.ContextRevision == j.spec.ContextRevision {
			t.Fatal("service policy did not invalidate the receipt independently of release identity")
		}
		j.recordBundle(t, false)
		inventory, err := j.client.GetAppBindingInventoryForDeployment(t.Context(), j.caller.Slug, serviceNativeScope, j.spec.CallerDeploymentID)
		if err != nil || !inventory.Complete || len(inventory.Bindings) != j.bindingCount() || j.serviceItem(t, inventory).VerificationStatus == "passed" {
			t.Fatal("old service-policy receipt still qualified")
		}
		j.cliResult(t, false, "bindings", "check", j.caller.Slug, "--deployment", j.spec.CallerDeploymentID, "--json")
		j.verifyCLI(t, false)
		policy, err := j.client.SetBindingReleasePolicy(t.Context(), j.caller.Slug, serviceNativeScope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m"})
		if err != nil {
			t.Fatal("cannot configure durable verification policy")
		}
		run := j.createRun(t, 120)
		if _, err := j.client.SetBindingReleasePolicy(t.Context(), j.caller.Slug, serviceNativeScope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &policy.Revision, MaxVerificationAge: "4m"}); err != nil {
			t.Fatal("cannot revise binding release policy")
		}
		if result, err := j.client.ResumeBindingVerificationRun(t.Context(), j.caller.Slug, run.ID); err == nil || result.Status == "passed" {
			t.Fatal("old release-policy receipt resumed")
		}
		if result, err := j.client.CompleteBindingVerificationRun(t.Context(), j.caller.Slug, run.ID); err == nil || result.Status == "passed" {
			t.Fatal("old release-policy receipt qualified")
		}
		fresh := j.createRun(t, 120)
		result, err := j.client.CompleteBindingVerificationRun(t.Context(), j.caller.Slug, fresh.ID)
		if err != nil || result.Status != "passed" {
			t.Fatal("freshly reviewed release-policy receipt did not qualify")
		}
	})
	phase("worker_service_and_queue_policy_invalidation", func(t *testing.T) {
		worker.rejectQueueCanaryEvidence(t, worker.spec.ReleaseID)
		old := worker.spec
		worker.refreshSpec(t)
		if old.ReleaseID != worker.spec.ReleaseID || old.CallerDeploymentID != worker.spec.CallerDeploymentID || old.TargetDeploymentID != worker.spec.TargetDeploymentID || old.ContextRevision == worker.spec.ContextRevision {
			t.Fatal("worker policy invalidation did not preserve release identity")
		}
		inventory, err := worker.client.GetAppBindingInventoryForDeployment(t.Context(), worker.caller.Slug, serviceNativeScope, worker.spec.CallerDeploymentID)
		if err != nil || !inventory.Complete || worker.serviceItem(t, inventory).VerificationStatus == "passed" {
			t.Fatal("worker retained a receipt from the old service policy")
		}
		worker.cliResult(t, false, "bindings", "check", worker.caller.Slug, "--deployment", worker.spec.CallerDeploymentID, "--json")
		worker.verifyCLI(t, false)
	})
	phase("cancelled_work_rejected", func(t *testing.T) {
		if err := j.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		task, err := j.client.CreateAppTask(t.Context(), j.caller.Slug, api.CreateAppTaskRequest{VerificationDeploymentID: j.spec.CallerDeploymentID, Command: []string{api.AppTaskServiceBindingProbeCommand, "billing"}, TimeoutSeconds: 15, MaxOutputBytes: 4096})
		if err != nil || task.Status != api.AppTaskStatusQueued {
			t.Fatal("cannot admit queued cancellation fixture")
		}
		j.tasks[task.ID] = true
		task, err = j.client.CancelAppTask(t.Context(), j.caller.Slug, task.ID)
		if err != nil || task.Status != api.AppTaskStatusCancelled {
			t.Fatal("queued probe cancellation failed")
		}
		if err := j.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		serviceNativeWait(t, "cancelled task remains terminal", 30*time.Second, func(ctx context.Context) bool {
			current, err := j.client.GetAppTask(ctx, j.caller.Slug, task.ID)
			return err == nil && current.Status == api.AppTaskStatusCancelled
		})
		j.cliResult(t, false, "bindings", "check", j.caller.Slug, "--deployment", j.spec.CallerDeploymentID, "--json")
		j.verifyCLI(t, false)
		// Cancellation also removes durable qualification authority after real guest
		// evidence exists. Completing the cancelled receipt must never pass.
		run := j.createRun(t, 120)
		cancelled, err := j.client.CancelBindingVerificationRun(t.Context(), j.caller.Slug, run.ID)
		if err != nil || cancelled.Status != "cancelled" {
			t.Fatal("durable receipt cancellation failed")
		}
		result, err := j.client.CompleteBindingVerificationRun(t.Context(), j.caller.Slug, run.ID)
		if err == nil || result.Status == "passed" {
			t.Fatal("cancelled receipt qualified")
		}
	})
	phase("expired_evidence_rejected", func(t *testing.T) {
		j.verifyCLI(t, false)
		j.cliResult(t, true, "bindings", "check", j.caller.Slug, "--deployment", j.spec.CallerDeploymentID, "--json")
		run := j.createRun(t, 2)
		serviceNativeWait(t, "original durable receipt deadline", 15*time.Second, func(ctx context.Context) bool {
			current, err := j.client.GetBindingVerificationRun(ctx, j.caller.Slug, run.ID)
			return err == nil && current.Status == "expired" && current.ExpiresAt.Equal(run.ExpiresAt)
		})
		result, err := j.client.CompleteBindingVerificationRun(t.Context(), j.caller.Slug, run.ID)
		if err == nil || result.Status == "passed" {
			t.Fatal("expired receipt qualified")
		}
		// This checks elapsed wall time against the actual guest receipt; no test
		// clock, SQL timestamp mutation or manufactured expired work is involved.
		j.cliResult(t, false, "bindings", "check", j.caller.Slug, "--deployment", j.spec.CallerDeploymentID, "--max-verification-age", "1s", "--json")
	})
	phase("independent_cleanup", func(t *testing.T) {
		for id := range j.runs {
			if _, err := j.client.CancelBindingVerificationRun(t.Context(), j.caller.Slug, id); err != nil {
				t.Fatal("cancel owned receipt during cleanup")
			}
		}
		j.requireTerminalTasks(t)
		worker.requireTerminalTasks(t)

		// Retire all reviewed workloads through reviewed intent before stopping owners.
		// Native VMMD recovery deliberately preserves live processes on exit;
		// daemon shutdown alone is not physical cleanup authority.
		j.retire(t)
		worker.requireQueueSentinel(t)
		j.recordBundle(t, true)
		if err := j.client.DeleteApp(t.Context(), j.policyControl); err != nil {
			t.Fatal("delete owned policy control")
		}
		// The independent runner leakcheck must also pass before publication.
		status, err := j.client.GetEnvironmentGitOps(t.Context(), j.project.Slug, serviceNativeScope)
		if err != nil {
			t.Fatal(err)
		}
		suspended := true
		if _, err := j.client.UpdateEnvironmentGitSource(t.Context(), j.project.Slug, serviceNativeScope, api.EnvironmentGitSourceUpdate{ExpectedGeneration: status.Source.Generation, Suspended: &suspended}); err != nil {
			t.Fatal(err)
		}
		j.h.Stop()
		final, err := e2etest.ServiceGuestRuntimeManifest(j.artifacts, j.manifest.SourceRevision, true)
		if err != nil || !reflect.DeepEqual(final, j.manifest) {
			t.Fatal("managed HTTPS source/runtime bundle changed during cleanup")
		}
		verified = true
	})
}

func serviceNativePrerequisites(t *testing.T) {
	t.Helper()
	if os.Getenv("GREGALE_SERVICE_NATIVE_ACCEPTANCE") != "1" {
		t.Fatal("use make test-managed-service-native for isolated acceptance")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("root Linux amd64 required")
	}
	for _, name := range []string{"DATABASE_URL", "FAAS_TEST_KERNEL", "FAAS_BUILDER_BASE_PATH", "FAAS_TEST_MINIMAL_BASE_PATH", "FAAS_GUEST_INIT"} {
		if os.Getenv(name) == "" {
			t.Fatalf("missing native prerequisite %s", name)
		}
	}
	if info, err := os.Stat(os.Getenv("FAAS_BUILDER_BASE_PATH")); err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		t.Fatal("real builder base required for reviewed source acceptance")
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("cannot skip PostgreSQL")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal("native KVM required")
	}
	if err := e2etest.ValidateNativeGuestInit(os.Getenv("FAAS_GUEST_INIT")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_GITOPS_NATIVE_ACCEPTANCE", "1")
}

func newServiceNativeJourney(t *testing.T, pool *pgxpool.Pool) *serviceNativeJourney {
	t.Helper()
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	// Source conversion resolves the host-approved runtime base through the
	// production producer path; an image-only unreachable-base override cannot
	// supply reviewed source-build acceptance.
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", "")
	storageRoot := filepath.Join(t.TempDir(), "storage")
	t.Setenv("FAAS_STORAGE_ROOT", storageRoot)
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	stageNativeAcceptanceBase(t, storageRoot)
	builder, _ := e2etest.ImageOnlyBuilderBaseImage("onebox-faas/builder-base")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builder))
	callerImage, _ := e2etest.HelloGitOpsRequestImage("library/service-caller", "isolated caller")
	targetImage, _ := e2etest.HelloGitOpsRequestImage("library/service-billing", "isolated target")
	nextImage, _ := e2etest.HelloGitOpsRequestImage("library/service-billing-next", "reviewed next target")
	callerRef := registry.AddImage("library/service-caller", callerImage)
	targetRef := registry.AddImage("library/service-billing", targetImage)
	nextRef := registry.AddImage("library/service-billing-next", nextImage)
	workerImage, _ := e2etest.HelloManagedServiceWorkerImage("library/service-worker")
	workerRef := registry.AddImage("library/service-worker", workerImage)
	provider := newGitOpsSourceFixture(t)
	ca := e2etest.ServiceGuestTLS(t)
	version, err := fcvm.DetectFirecrackerVersion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	kernelPath := filepath.Join(storageRoot, sched.KernelKey(version))
	if err := os.MkdirAll(filepath.Dir(kernelPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(os.Getenv("FAAS_TEST_KERNEL")) //nolint:forbidigo // Host-approved kernel fixture, never a customer-supplied path.
	if err != nil {
		t.Fatal(err)
	}
	dest, err := os.OpenFile(kernelPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(dest, source)
	if err := errors.Join(copyErr, source.Close(), dest.Close()); err != nil {
		t.Fatal(err)
	}
	catalog, err := json.Marshal(e2etest.QueueGuestCatalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(catalogPath, catalog, 0o600); err != nil {
		t.Fatal(err)
	}
	bridge := api.DefaultHostBridgeCIDR().Addr().Next().String()
	h := e2etest.Start(t, pool, e2etest.All|e2etest.GatewaydPublic,
		"FAAS_STORAGE_ROOT="+storageRoot, "FAAS_STORAGE_BACKEND=local", "FAAS_GITHUBD_SOCKET="+provider.socket,
		"FAAS_MANAGED_POSTGRES_CONFIG="+catalogPath, "GREGALE_QUEUE_UNUSED_PROVIDER_KEY=unused-native-fixture",
		"GREGALE_GITOPS_ACCEPTANCE=1", "FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT=1", "FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH=1",
		"FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTP_URL=http://"+net.JoinHostPort(bridge, strconv.Itoa(netns.ServiceProxyPort)),
		"FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_SERVICE_PROXY_HTTPS_URL=https://"+net.JoinHostPort(bridge, "443"),
		"FAAS_AUDIT_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "audit.key"), "FAAS_RECOVERY_HMAC_KEY_FILE="+gitOpsAcceptanceHMACKeyFile(t, "recovery.key"))
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
		h.Stop()
	})
	key := h.SeedAccount(t.Context(), api.PlanPro, "service-native")
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(t.Context(), "e2e+pro+service-native@test.example")
	if err != nil {
		t.Fatal(err)
	}
	provider.setAccount(account.ID)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "binding-native-service", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	j := &serviceNativeJourney{h: h, store: store, client: api.NewClient(h.APIDURL, key), provider: provider, project: project, nextImage: nextRef, key: key, cliHome: t.TempDir(), fixtureDir: t.TempDir(), tasks: map[string]bool{}, runs: map[string]bool{}, workerTasks: map[string]bool{}, canaries: map[string]api.QueueBindingCanary{}}
	for name, app := range map[string]*state.App{"caller": &j.caller, "billing": &j.target, "worker": &j.worker} {
		class := state.WorkloadClassHTTP
		manifest := state.AppManifest{Port: 8080, StartupDeadlineS: 30}
		if name == "worker" {
			class = state.WorkloadClassWorker
			manifest.ExecutionMode = api.ExecutionModeWorker
		}
		*app, err = store.CreateApp(t.Context(), state.App{WorkloadClass: class, AccountID: account.ID, ProjectID: project.ID, Slug: "binding-native-" + name, WorkloadName: name, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, Manifest: manifest})
		if err != nil {
			t.Fatal(err)
		}
	}
	control, err := j.client.CreateApp(t.Context(), api.CreateAppRequest{Slug: "binding-native-policy-control", Type: "app", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	j.policyControl = control.Slug
	if _, err := j.client.CreateProjectEnvironment(t.Context(), project.Slug, api.CreateProjectEnvironmentRequest{Slug: serviceNativeScope}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.client.CreateEnvironmentGitSource(t.Context(), project.Slug, serviceNativeScope, api.CreateEnvironmentGitSourceRequest{ManifestPath: serviceNativeManifestPath, Mode: "enforce", ApprovalPolicy: "manual"}); err != nil {
		t.Fatal(err)
	}
	j.definition = servicestarter.WithWorker(servicestarter.Definition(project.Slug, j.caller.Slug, j.target.Slug, callerRef, targetRef), j.worker.Slug, workerRef)
	j.cli = filepath.Join(t.TempDir(), "gregale")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=true", "-o", j.cli, "./cmd/gregale")
	cmd.Dir = filepath.Join("..", "..")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build exact-source CLI: %v\n%s", err, output)
	}
	revision, err := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	j.artifacts = map[string]string{"kernel": os.Getenv("FAAS_TEST_KERNEL"), "guest_init": os.Getenv("FAAS_GUEST_INIT"), "builder_base": e2etest.StagedBuilderBasePath(), "minimal_base": os.Getenv("FAAS_TEST_MINIMAL_BASE_PATH"), "deploy_base": filepath.Join(storageRoot, sched.BaseKeyForArch("", "amd64")), "service_ca": ca, "gregale": j.cli}
	for _, name := range append(append([]string(nil), e2etest.DaemonBinaries...), e2etest.StaticHelperBinaries...) {
		j.artifacts[name] = filepath.Join(h.BinDir, name)
	}
	for _, name := range []string{"firecracker", "jailer"} {
		j.artifacts[name], err = exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	j.artifacts["e2e_test"], err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	j.manifest, err = e2etest.ServiceGuestRuntimeManifest(j.artifacts, strings.TrimSpace(string(revision)), false)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (j *serviceNativeJourney) approve(t *testing.T, sha, artifact string, adopt bool, onApproved ...func()) {
	t.Helper()
	desired, err := environmentsync.Compile(j.definition)
	if err != nil {
		t.Fatal(err)
	}
	j.provider.addAtPathWithFiles(t, sha, desired.Definition, serviceNativeManifestPath, j.sourceFiles)
	raw, err := json.Marshal(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.fixtureDir, artifact+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	j.artifacts[artifact] = path
	review, err := j.client.PreviewEnvironmentGitRevision(t.Context(), j.project.Slug, serviceNativeScope, api.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := j.client.ApproveEnvironmentGitRevision(t.Context(), j.project.Slug, serviceNativeScope, api.ApproveEnvironmentGitRevisionRequest{CommitSHA: sha, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation})
	if err != nil {
		t.Fatal(err)
	}
	// Observe revocation while prior queue proof is still unexpired. Source
	// builds can legitimately outlast a bounded canary receipt.
	for _, observe := range onApproved {
		observe()
	}
	if adopt {
		plan, err := j.client.PreviewEnvironmentGitOpsAdoption(t.Context(), j.project.Slug, serviceNativeScope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := j.client.AdoptEnvironmentGitOps(t.Context(), j.project.Slug, serviceNativeScope, api.AdoptEnvironmentGitOpsRequest{PlanHash: plan.Hash}); err != nil {
			t.Fatal(err)
		}
	}
	activationBudget := 12 * time.Minute
	if len(j.sourceFiles) > 0 {
		activationBudget += e2etest.DefaultBuildCeiling
	}
	serviceNativeWait(t, "reviewed graph qualification and activation", activationBudget, func(ctx context.Context) bool {
		status, err := j.client.GetEnvironmentGitOps(ctx, j.project.Slug, serviceNativeScope)
		e := status.WorkloadEvidence
		return err == nil && status.Source.ApprovedRevisionID == approved.Revision.ID && status.Source.Generation == approved.Source.Generation && e != nil && e.RevisionID == approved.Revision.ID && e.Generation == approved.Source.Generation && e.Qualified && e.Activated && e.CapturesRecorded == 3 && e.RestoresRecorded == 3 && e.SmokesRecorded == 3
	})
	// Worker serving needs a genuine post-activation delivery as well as its
	// private qualification smoke. Publish this explicit bounded fixture through
	// customer admission before measuring the service probes' queue isolation.
	body, code := doReq(t, j.h, j.key, http.MethodPost, "/v1/apps/"+j.worker.Slug+"/queues/send",
		api.QueueSendRequest{Environment: serviceNativeScope, QueueName: "orders", Payload: json.RawMessage(`{"id":"worker-service-serving"}`)})
	var accepted api.QueueSendResponse
	if code != http.StatusCreated || json.Unmarshal(body, &accepted) != nil || accepted.ID == "" || accepted.QueueBindingID == "" {
		t.Fatal("cannot admit owned worker serving message")
	}
	serviceNativeWait(t, "worker serving delivery", 3*time.Minute, func(ctx context.Context) bool {
		invocation, err := j.store.InvocationByID(ctx, accepted.ID)
		if err != nil || invocation.AppID != j.worker.ID || invocation.DeploymentScope != serviceNativeScope || invocation.QueueBindingID != accepted.QueueBindingID {
			t.Fatal("worker serving message lost its accepted identity")
		}
		if invocation.State == state.InvocationFailed || invocation.State == state.InvocationDeadLetter {
			t.Fatal("worker serving delivery failed")
		}
		return invocation.State == state.InvocationCompleted && invocation.Outcome != nil && *invocation.Outcome == state.OutcomeSuccess && invocation.Attempts > 0
	})
	serviceNativeWait(t, "reviewed graph serving", 4*time.Minute, func(ctx context.Context) bool {
		status, err := j.client.GetEnvironmentGitOps(ctx, j.project.Slug, serviceNativeScope)
		e := status.WorkloadEvidence
		return err == nil && status.Source.AppliedRevisionID == approved.Revision.ID && status.Source.Generation == approved.Source.Generation && e != nil && e.RevisionID == approved.Revision.ID && e.Generation == approved.Source.Generation && e.Serving
	})
}

func (j *serviceNativeJourney) refreshSpec(t *testing.T) {
	t.Helper()
	release, err := j.store.ActiveProjectReleaseSet(t.Context(), j.caller.AccountID, j.project.ID, serviceNativeScope)
	if err != nil || !release.Active {
		t.Fatal("active reviewed release unavailable")
	}
	deployment := ""
	for _, member := range release.Members {
		if member.AppID == j.caller.ID {
			deployment = member.DeploymentID
		}
	}
	inventory, err := j.client.GetAppBindingInventoryForDeployment(t.Context(), j.caller.Slug, serviceNativeScope, deployment)
	if err != nil || !inventory.Complete || len(inventory.Bindings) != j.bindingCount() || inventory.ServiceBindingSource != "environment_release" || inventory.VerificationDeploymentID != deployment || inventory.VerificationScope != serviceNativeScope {
		t.Fatal("complete frozen service inventory unavailable")
	}
	spec, ok := api.ManagedServiceProbeForItem(j.serviceItem(t, inventory))
	if !ok || spec.ReleaseID != release.ID || spec.TargetAppID != j.target.ID {
		t.Fatal("inventory did not pin the active reviewed target")
	}
	j.spec = spec
}

func (j *serviceNativeJourney) captureLayers(t *testing.T, callerName, targetName string) {
	t.Helper()
	for name, id := range map[string]string{callerName: j.spec.CallerDeploymentID, targetName: j.spec.TargetDeploymentID} {
		dep, err := j.store.DeploymentByID(t.Context(), id)
		if err != nil || dep.RootfsKey == "" || dep.ImageDigest == "" || dep.RootfsPath == "" {
			t.Fatal("reviewed guest artifact missing")
		}
		j.artifacts[name] = dep.RootfsPath
	}
	j.recordBundle(t, false)
}

func (j *serviceNativeJourney) cliResult(t *testing.T, success bool, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, j.cli, args...)
	cmd.Dir = j.cliHome
	// Never inherit operator CLI credentials or ambient project discovery.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + j.cliHome, "XDG_CONFIG_HOME=" + j.cliHome, "FAAS_API=" + j.h.APIDURL, "FAAS_TOKEN=" + j.key, "NO_COLOR=1"}
	output, err := cmd.CombinedOutput()
	if (err == nil) != success || ctx.Err() != nil {
		t.Fatalf("fixture CLI outcome mismatch for %s", strings.Join(args, " "))
	}
	return output
}

func (j *serviceNativeJourney) verifyCLI(t *testing.T, all bool) {
	t.Helper()
	j.requireQueueSentinel(t)
	before := j.taskCount(t)
	invocations, instances := j.observations(t)
	targetInstances := j.targetInstances(t)
	workerBatch := all && j.caller.WorkloadClass == state.WorkloadClassWorker
	args := []string{"bindings", "verify", j.caller.Slug}
	if all {
		args = append(args, "--all", "--require-complete")
	} else {
		args = append(args, "billing")
	}
	args = append(args, "--deployment", j.spec.CallerDeploymentID, "--wait-timeout", "4m", "--poll-interval", "250ms", "--json")
	if workerBatch {
		args = append(args, "--queue-publish")
	}
	output := j.cliResult(t, true, args...)
	if all {
		var result struct {
			Coverage    string `json:"coverage"`
			Total       int    `json:"total"`
			Checked     int    `json:"checked"`
			Passed      int    `json:"passed"`
			Failed      int    `json:"failed"`
			Unsupported int    `json:"unsupported"`
			NotChecked  int    `json:"not_checked"`
			Bindings    []struct {
				Type, Binding, Status, Reason string
			} `json:"bindings"`
			Gate struct {
				Required, Passed bool
				Blockers         []struct{ Code, Type, Binding string }
			} `json:"gate"`
		}
		expected, unsupported, coverage := j.bindingCount(), 0, "complete"
		if json.Unmarshal(output, &result) != nil || result.Coverage != coverage || result.Total != expected || result.Checked != expected || result.Passed != expected || result.Failed != 0 || result.Unsupported != unsupported || result.NotChecked != 0 || !result.Gate.Required || !result.Gate.Passed {
			t.Fatal("strict CLI coverage did not account for every eligible binding")
		}

	} else {
		var report api.ServiceBindingProbeReport
		if json.Unmarshal(output, &report) != nil || !report.Passed() || report.ManagedService == nil || *report.ManagedService != j.spec {
			t.Fatal("CLI did not return the exact managed guest report")
		}
	}
	tasks, err := j.client.ListAppTasks(t.Context(), j.caller.Slug, 100, 0)
	if err != nil || len(tasks.Tasks) != before+1 {
		t.Fatal("CLI did not create exactly one guest probe")
	}
	newest := ""
	for _, task := range tasks.Tasks {
		if j.tasks[task.ID] {
			continue
		}
		if newest != "" || task.Status != api.AppTaskStatusSucceeded || task.DeploymentID != j.spec.CallerDeploymentID || task.ExitCode == nil || *task.ExitCode != 0 || task.OutputTruncated {
			t.Fatal("CLI probe task failed or lost identity")
		}
		var report api.ServiceBindingProbeReport
		if json.Unmarshal([]byte(task.StdoutTail), &report) != nil || !report.Passed() || report.ManagedService == nil || *report.ManagedService != j.spec {
			t.Fatal("real guest output did not confirm reviewed target/release")
		}
		newest = task.ID
		j.tasks[task.ID] = true
	}
	if newest == "" {
		t.Fatal("CLI reused old evidence without a guest probe")
	}
	if _, err := j.store.InstanceByID(t.Context(), newest); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("probe task entered serving-instance inventory")
	}
	j.requireReceipt(t, j.spec, newest)
	afterInvocations, afterInstances := j.observations(t)
	if workerBatch {
		added := 0
		for id := range afterInvocations {
			if !invocations[id] {
				added++
				canary, err := j.client.GetQueueBindingCanary(t.Context(), j.caller.Slug, id)
				if err != nil || canary.Status != "succeeded" || canary.Binding != "orders" || canary.DeploymentID != j.spec.CallerDeploymentID || canary.ReleaseID != j.spec.ReleaseID || canary.Scope != serviceNativeScope || canary.DeliveredAt == nil || canary.CompletedAt == nil {
					t.Fatal("worker batch did not confirm exact active-release handler completion")
				}
				j.canaries[id] = canary
			}
		}
		if added != 1 || len(afterInvocations) != len(invocations)+1 {
			t.Fatal("worker batch changed unrelated queue work")
		}
	} else {
		if !reflect.DeepEqual(invocations, afterInvocations) {
			t.Fatal("route probe published or consumed application work")
		}
		for id := range afterInstances {
			if !instances[id] {
				t.Fatal("route-only probe woke a serving instance")
			}
		}
	}

	for id := range j.targetInstances(t) {
		if !targetInstances[id] {
			t.Fatal("route probe woke its HTTP target")
		}
	}
	j.requireQueueSentinel(t)
}

func (j *serviceNativeJourney) taskCount(t *testing.T) int {
	t.Helper()
	tasks, err := j.client.ListAppTasks(t.Context(), j.caller.Slug, 100, 0)
	if err != nil || tasks.NextOffset >= 0 {
		t.Fatal("task inventory unavailable or unexpectedly paginated")
	}
	return len(tasks.Tasks)
}

func (j *serviceNativeJourney) requireReceipt(t *testing.T, spec api.ManagedServiceProbeSpec, id string) {
	t.Helper()
	inventory, err := j.client.GetAppBindingInventoryForDeployment(t.Context(), j.caller.Slug, serviceNativeScope, spec.CallerDeploymentID)
	if err != nil || !inventory.Complete || len(inventory.Bindings) != j.bindingCount() {
		t.Fatal("authoritative receipt inventory unavailable")
	}
	item := j.serviceItem(t, inventory)
	current, ok := api.ManagedServiceProbeForItem(item)
	if !ok || current != spec || item.VerificationStatus != "passed" || item.Verification == nil || id != "" && item.Verification.TaskID != id || item.Verification.ManagedService == nil || *item.Verification.ManagedService != spec {
		t.Fatal("guest evidence did not survive an authoritative inventory refresh")
	}
}

func (j *serviceNativeJourney) rejectOldReceipt(t *testing.T, old api.ManagedServiceProbeSpec) {
	t.Helper()
	inventory, err := j.client.GetAppBindingInventoryForDeployment(t.Context(), j.caller.Slug, serviceNativeScope, j.spec.CallerDeploymentID)
	if err != nil || !inventory.Complete || len(inventory.Bindings) != j.bindingCount() || j.serviceItem(t, inventory).VerificationStatus == "passed" {
		t.Fatal("old graph receipt qualified the changed release")
	}
	j.cliResult(t, false, "bindings", "check", j.caller.Slug, "--deployment", j.spec.CallerDeploymentID, "--json")
	_, err = j.client.CreateAppTask(t.Context(), j.caller.Slug, api.CreateAppTaskRequest{VerificationDeploymentID: old.CallerDeploymentID, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, "billing", old.TargetDeploymentID}})
	serviceNativeProblem(t, err, http.StatusConflict)
}

func (j *serviceNativeJourney) createRun(t *testing.T, ttl int) api.BindingVerificationRun {
	t.Helper()
	review, err := j.client.ReviewBindingVerification(t.Context(), j.caller.Slug, api.ReviewBindingVerificationRequest{DeploymentID: j.spec.CallerDeploymentID})
	if err != nil || !review.Ready {
		t.Fatal("cannot review durable managed receipt")
	}
	run, err := j.client.CreateBindingVerificationRun(t.Context(), j.caller.Slug, api.CreateBindingVerificationRunRequest{ID: uuid.NewString(), DeploymentID: j.spec.CallerDeploymentID, ReviewDigest: review.ReviewDigest, TTLSeconds: ttl})
	if err != nil || run.ID == "" || run.Status != "pending" {
		t.Fatal("cannot create bounded durable receipt")
	}
	j.runs[run.ID] = true
	return run
}

func serviceNativeProblem(t *testing.T, err error, status int) {
	t.Helper()
	var problem *api.Problem
	if !errors.As(err, &problem) || problem.Status != status {
		t.Fatalf("expected fail-closed API status %d", status)
	}
}

func serviceNativeWait(t *testing.T, phase string, timeout time.Duration, ready func(context.Context) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s did not complete", phase)
		case <-ticker.C:
		}
	}
}

// recordBundle extends the artifact set without replacing any earlier hash.
func (j *serviceNativeJourney) recordBundle(t *testing.T, complete bool) {
	t.Helper()
	current, err := e2etest.ServiceGuestRuntimeManifest(j.artifacts, j.manifest.SourceRevision, complete)
	if err != nil {
		t.Fatal(err)
	}
	for name, digest := range j.manifest.Artifacts {
		if current.Artifacts[name] != digest {
			t.Fatal("executed runtime or earlier fixture artifact changed")
		}
	}
	j.manifest = current
}

func (j *serviceNativeJourney) retire(t *testing.T) {
	t.Helper()
	status, err := j.client.GetEnvironmentGitOps(t.Context(), j.project.Slug, serviceNativeScope)
	if err != nil {
		t.Fatal(err)
	}
	prune := true
	if _, err := j.client.UpdateEnvironmentGitSource(t.Context(), j.project.Slug, serviceNativeScope, api.EnvironmentGitSourceUpdate{ExpectedGeneration: status.Source.Generation, Prune: &prune}); err != nil {
		t.Fatal(err)
	}
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: j.project.Slug, Environment: serviceNativeScope, WorkloadPruningPolicy: "retain", QueuePruningPolicy: "retain", Workloads: map[string]api.EnvironmentWorkload{}}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("d", 40)
	j.provider.addAtPath(t, sha, desired.Definition, serviceNativeManifestPath)
	raw, err := json.Marshal(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.fixtureDir, "retirement.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	j.artifacts["retirement_definition"] = path
	j.recordBundle(t, true)
	review, err := j.client.PreviewEnvironmentGitRevision(t.Context(), j.project.Slug, serviceNativeScope, api.PreviewEnvironmentGitRevisionRequest{CommitSHA: sha})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := j.client.ApproveEnvironmentGitRevision(t.Context(), j.project.Slug, serviceNativeScope, api.ApproveEnvironmentGitRevisionRequest{CommitSHA: sha, DefinitionDigest: review.DefinitionDigest, ExpectedGeneration: review.Generation})
	if err != nil {
		t.Fatal(err)
	}
	serviceNativeWait(t, "reviewed workload retirement", 6*time.Minute, func(ctx context.Context) bool {
		status, err := j.client.GetEnvironmentGitOps(ctx, j.project.Slug, serviceNativeScope)
		if err != nil || status.Source.AppliedRevisionID != approved.Revision.ID || len(status.WorkloadRetirements) != 3 {
			return false
		}
		for _, retirement := range status.WorkloadRetirements {
			if retirement.Generation != approved.Source.Generation || retirement.Phase != "retired" || retirement.AppID != j.caller.ID && retirement.AppID != j.target.ID && retirement.AppID != j.worker.ID {
				return false
			}
		}
		return true
	})
}

func (j *serviceNativeJourney) observations(t *testing.T) (map[string]bool, map[string]bool) {
	t.Helper()
	invocations, instances := map[string]bool{}, map[string]bool{}
	for _, app := range []state.App{j.caller, j.target} {
		calls, err := j.store.ListInvocationsForApp(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			invocations[call.ID] = true
		}
		guests, err := j.store.ListInstancesForApp(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, guest := range guests {
			instances[guest.ID] = true
		}
	}
	return invocations, instances
}

func (j *serviceNativeJourney) bindingCount() int {
	if j.caller.WorkloadClass == state.WorkloadClassWorker {
		return 2
	}
	return 1
}

func (j *serviceNativeJourney) serviceItem(t *testing.T, inventory api.AppBindingInventory) api.AppBindingInventoryItem {
	t.Helper()
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeService && item.Name == "billing" {
			return item
		}
	}
	t.Fatal("reviewed billing service edge missing")
	return api.AppBindingInventoryItem{}
}

func (j *serviceNativeJourney) workerView() *serviceNativeJourney {
	worker := *j
	worker.caller = j.worker
	worker.tasks = j.workerTasks
	worker.runs = map[string]bool{}
	return &worker
}

func (j *serviceNativeJourney) captureWorkerLayer(t *testing.T, id, artifact string) {
	t.Helper()
	dep, err := j.store.DeploymentByID(t.Context(), id)
	if err != nil || dep.AppID != j.worker.ID || dep.RootfsPath == "" || dep.RootfsKey == "" {
		t.Fatal("reviewed worker layer missing")
	}
	j.artifacts[artifact] = dep.RootfsPath
	j.recordBundle(t, false)
}

func (j *serviceNativeJourney) requireTerminalTasks(t *testing.T) {
	t.Helper()
	tasks, err := j.store.ListAppTasks(t.Context(), j.caller.AccountID, j.caller.ID, 100, 0)
	if err != nil || len(tasks) != len(j.tasks) {
		t.Fatal("independent task inventory found untracked work")
	}
	for _, task := range tasks {
		if !j.tasks[task.ID] || !task.Status.Terminal() {
			t.Fatal("cleanup found foreign or nonterminal task")
		}
	}
}

func (j *serviceNativeJourney) targetInstances(t *testing.T) map[string]bool {
	t.Helper()
	instances, err := j.store.ListInstancesForApp(t.Context(), j.target.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]bool{}
	for _, instance := range instances {
		result[instance.ID] = true
	}
	return result
}

func (j *serviceNativeJourney) assertWorkerTaskIsolation(t *testing.T) {
	t.Helper()
	beforeCalls, beforeGuests := j.observations(t)
	task, err := j.client.CreateAppTask(t.Context(), j.caller.Slug, api.CreateAppTaskRequest{Command: []string{"/hello-server", "-assert-worker-task-isolation"}, TimeoutSeconds: 15, MaxOutputBytes: 512})
	if err != nil || task.ID == "" || task.DeploymentID != j.spec.CallerDeploymentID || task.DeploymentScope != serviceNativeScope {
		t.Fatal("cannot admit isolated worker command on the active reviewed deployment")
	}
	j.tasks[task.ID] = true
	serviceNativeWait(t, "worker task isolation assertion", 3*time.Minute, func(ctx context.Context) bool {
		current, err := j.client.GetAppTask(ctx, j.caller.Slug, task.ID)
		if err == nil {
			task = current
		}
		return err == nil && task.Status.Terminal()
	})
	var report struct {
		Entry    *bool `json:"worker_entrypoint_started"`
		Listener *bool `json:"worker_listener_started"`
	}
	if task.Status != api.AppTaskStatusSucceeded || task.ExitCode == nil || *task.ExitCode != 0 || task.OutputTruncated || json.Unmarshal([]byte(task.StdoutTail), &report) != nil || report.Entry == nil || *report.Entry || report.Listener == nil || *report.Listener {
		t.Fatal("task guest started the worker entrypoint/listener or restored its snapshot")
	}
	afterCalls, afterGuests := j.observations(t)
	if !reflect.DeepEqual(beforeCalls, afterCalls) {
		t.Fatal("worker task consumed or published queue work")
	}
	for id := range afterGuests {
		if !beforeGuests[id] {
			t.Fatal("worker task entered serving inventory")
		}
	}
	if _, err := j.store.InstanceByID(t.Context(), task.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("worker task has a serving-instance row")
	}
	j.requireReceipt(t, j.spec, "")
	j.requireQueueSentinel(t)
}

func (j *serviceNativeJourney) seedQueueSentinel(t *testing.T) {
	t.Helper()
	bindings, err := j.store.ListQueueBindingsForApp(t.Context(), j.caller.AccountID, j.caller.ID)
	if err != nil || len(bindings) != 1 || bindings[0].Name != "orders" || bindings[0].DeploymentScope != serviceNativeScope {
		t.Fatal("reviewed worker queue identity unavailable")
	}
	// Genuine accepted fixture work, outside the dispatch window. Probe tasks
	// must leave its original identity, payload and attempt budget untouched.
	original, err := j.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: j.caller.AccountID, AppID: j.caller.ID,
		Source: state.InvocationQueue, DeploymentScope: serviceNativeScope, QueueBindingID: bindings[0].ID, QueueName: "orders",
		Method: http.MethodPost, Path: "/", Payload: json.RawMessage(`{"id":"worker-probe-isolation"}`), DueAt: time.Now().Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	j.queueSentinel = &original
	j.requireQueueSentinel(t)
}

func (j *serviceNativeJourney) requireQueueSentinel(t *testing.T) {
	t.Helper()
	if j.queueSentinel == nil {
		return
	}
	original := *j.queueSentinel
	current, err := j.store.InvocationByID(t.Context(), original.ID)
	if err != nil || current.AppID != original.AppID || current.QueueBindingID != original.QueueBindingID || current.DeploymentScope != original.DeploymentScope || current.State != state.InvocationPending || current.Attempts != original.Attempts || !current.DueAt.Equal(original.DueAt) || string(current.Payload) != string(original.Payload) {
		t.Fatal("service verification changed accepted worker queue work")
	}
}

func (j *serviceNativeJourney) rejectQueueCanaryEvidence(t *testing.T, release string) {
	t.Helper()
	checked := 0
	for id, old := range j.canaries {
		if old.ReleaseID == release {
			if !old.ExpiresAt.After(time.Now()) {
				t.Fatal("queue proof expired before context invalidation was observed")
			}
			checked++
			current, err := j.client.GetQueueBindingCanary(t.Context(), j.caller.Slug, id)
			if err != nil || current.Status != "blocked" {
				t.Fatal("release or policy change retained worker queue completion evidence")
			}
		}
	}
	if checked == 0 {
		t.Fatal("no real worker queue completion evidence to invalidate")
	}
}

// This observes production builder receipts and the actual pinned archive. No
// status, build provenance or qualification row is manufactured by this phase.
func (j *serviceNativeJourney) captureSourceWorker(t *testing.T, deployment, prefix string) {
	t.Helper()
	dep, err := j.store.DeploymentByID(t.Context(), deployment)
	if err != nil || dep.ImageDigest != "" || dep.BuildID == "" || dep.SourceSHA256 == "" || dep.RootfsBytes <= 0 {
		t.Fatal("source worker did not execute the source build pipeline")
	}
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen == nil || frozen.SourceArchive == nil || frozen.SourceArchive.BuildID != dep.BuildID {
		t.Fatal("source worker lost reviewed build provenance")
	}
	build, err := j.store.BuildByID(t.Context(), dep.BuildID)
	if err != nil || build.Status != state.BuildSucceeded {
		t.Fatal("source worker build did not succeed")
	}
	provenance, err := j.store.BuildProvenanceByBuildID(t.Context(), dep.BuildID)
	if err != nil || provenance.SourceSHA256 != dep.SourceSHA256 || provenance.CommitSHA != dep.CommitSHA || provenance.BuilderNodeID == "" || provenance.StartedAt.IsZero() || !provenance.FinishedAt.After(provenance.StartedAt) {
		t.Fatal("source worker lacks actual builder provenance")
	}
	if j.sourceWorkerBuildID == dep.BuildID {
		t.Fatal("reviewed source replacement reused its earlier build")
	}
	j.sourceWorkerBuildID = dep.BuildID
	j.provider.mu.RLock()
	archive := append([]byte(nil), j.provider.archives[dep.CommitSHA]...)
	j.provider.mu.RUnlock()
	checksum := sha256.Sum256(archive)
	if len(archive) == 0 || int64(len(archive)) != dep.SourceBytes || hex.EncodeToString(checksum[:]) != dep.SourceSHA256 {
		t.Fatal("built source differs from reviewed archive")
	}
	path := filepath.Join(j.fixtureDir, prefix+".tar.gz")
	if err := os.WriteFile(path, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	j.artifacts[prefix+"_source_archive"] = path
	raw, err := json.Marshal(struct {
		Reviewed                                        *state.EnvironmentWorkloadSourceArtifact
		BuildID, SourceSHA256, CommitSHA, BuilderNodeID string
		StartedAt, FinishedAt                           time.Time
	}{frozen.SourceArchive, provenance.BuildID, provenance.SourceSHA256, provenance.CommitSHA, provenance.BuilderNodeID, provenance.StartedAt, provenance.FinishedAt})
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(j.fixtureDir, prefix+"-build.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	j.artifacts[prefix+"_build_provenance"] = path
	j.captureWorkerLayer(t, deployment, prefix+"_app_layer")
}
