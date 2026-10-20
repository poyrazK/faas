//go:build metal

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

const projectWorkerNativeGrace = 5 * time.Second

type projectWorkerNativeReport struct {
	DeploymentID                    string         `json:"deployment_id,omitempty"`
	InstanceID                      string         `json:"instance_id,omitempty"`
	BackendDeploymentID             string         `json:"backend_deployment_id,omitempty"`
	BackendInstanceID               string         `json:"backend_instance_id,omitempty"`
	BackendDestinationSecretMatches bool           `json:"backend_destination_secret_matches"`
	BindingTransport                string         `json:"binding_transport,omitempty"`
	DestinationSecretMatches        bool           `json:"destination_secret_matches"`
	SecretMatchesGeneration         bool           `json:"secret_matches_generation"`
	BackendGeneration               string         `json:"backend_generation,omitempty"`
	BackendMode                     string         `json:"backend_mode,omitempty"`
	ReleaseID                       string         `json:"release_id,omitempty"`
	BackendReleaseID                string         `json:"backend_release_id,omitempty"`
	Generation                      string         `json:"generation"`
	Mode                            string         `json:"mode"`
	Received                        map[string]int `json:"received"`
	Completed                       map[string]int `json:"completed"`
	Active                          int            `json:"active"`
	Stopping                        bool           `json:"stopping"`
	IgnoreStop                      bool           `json:"ignore_stop"`
	SQLValue                        string         `json:"sql_value,omitempty"`
	SQLWrites                       int64          `json:"sql_writes,omitempty"`
}

type projectWorkerNativeFixture struct {
	sourceFiles      func([]byte, string) fstest.MapFS
	serviceGraph     bool
	releaseValues    map[string]map[string]string
	secretRelease    bool
	h                *e2etest.Harness
	store            *state.PgStore
	owner            *api.Client
	accountID        string
	project          state.Project
	apps             map[string]state.App
	baseline         api.ProjectReleaseSetResponse
	binary           []byte
	apidStopped      bool
	completed        []string
	bindingID        string
	deploymentCounts map[string]int
	sql              *projectContractSQL
	cleanupDone      bool
}

func newProjectWorkerNativeFixture(t *testing.T, extraEnv ...string) *projectWorkerNativeFixture {
	return newProjectWorkerNativeFixtureWithPinTTL(t, 3600, extraEnv...)
}

func newProjectWorkerNativeFixtureWithPinTTL(t *testing.T, pinTTL int, extraEnv ...string) *projectWorkerNativeFixture {
	return newProjectWorkerNativeFixtureConfigured(t, pinTTL, nil, extraEnv...)
}

func newProjectWorkerNativeFixtureConfigured(t *testing.T, pinTTL int, setup func(*projectWorkerNativeFixture), extraEnv ...string) *projectWorkerNativeFixture {
	return newProjectReleaseNativeFixture(t, pinTTL, setup, false, false, extraEnv...)
}

func newProjectReleaseNativeFixture(t *testing.T, pinTTL int, setup func(*projectWorkerNativeFixture), httpOnly, serviceGraph bool, extraEnv ...string) *projectWorkerNativeFixture {
	return newProjectReleaseNativeFixtureWithFiles(t, pinTTL, setup, httpOnly, serviceGraph, nil, extraEnv...)
}

func newProjectReleaseNativeFixtureWithFiles(t *testing.T, pinTTL int, setup func(*projectWorkerNativeFixture), httpOnly, serviceGraph bool, sourceFiles func([]byte, string) fstest.MapFS, extraEnv ...string) *projectWorkerNativeFixture {
	return newProjectReleaseNativeFixtureOnHarness(t, pinTTL, setup, httpOnly, serviceGraph, sourceFiles, nil, api.PlanPro, extraEnv...)
}

// Shared-harness fixtures seed separate accounts/projects while retaining one
// daemon fleet. The primary fixture owns the final native leakcheck.
func newProjectReleaseNativeFixtureOnHarness(t *testing.T, pinTTL int, setup func(*projectWorkerNativeFixture), httpOnly, serviceGraph bool, sourceFiles func([]byte, string) fstest.MapFS, shared *e2etest.Harness, accountPlan api.Plan, extraEnv ...string) *projectWorkerNativeFixture {
	t.Helper()
	if _, err := os.Stat("/dev/kvm"); err != nil || os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("native KVM, kernel and builder base required for project worker release acceptance")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 || os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("project worker acceptance requires native Linux amd64, root and real PostgreSQL")
	}
	if _, err := os.Stat("/etc/faas/builder-acceptance-host"); err != nil {
		t.Fatal("dedicated acceptance host marker required:", err)
	}
	virtualization, err := exec.CommandContext(t.Context(), "systemd-detect-virt").Output()
	var exit *exec.ExitError
	if strings.TrimSpace(string(virtualization)) != "none" || err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		t.Fatal("nested or virtualized acceptance hosts are unsupported")
	}
	var pool *pgxpool.Pool
	if shared == nil {
		pool = pgtest.OpenMigrated(t)
	} else {
		pool = shared.Pool
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	builderBase, _ := e2etest.HelloImage("project-worker-builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("project-worker-builder-base", builderBase))
	deployBase, _ := e2etest.BaseLayerImage("project-worker-deploy-base", "unused-base-fixture")
	deployRef := registry.AddImage("project-worker-deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, deployRef)
	f := &projectWorkerNativeFixture{sourceFiles: sourceFiles, serviceGraph: serviceGraph, apps: map[string]state.App{}, deploymentCounts: map[string]int{}, binary: projectWorkerNativeBinary(t)}
	if !httpOnly {
		extraEnv = append(extraEnv, "FAAS_ATOMIC_WORKER_RELEASES_ENABLED=1")
	}
	if shared == nil {
		f.h = e2etest.Start(t, pool, e2etest.All|e2etest.GatewaydPublic, extraEnv...)
	} else {
		f.h = shared
	}
	f.store = state.NewPgStore(pool)
	accountLabel := ""
	projectSlug := "native-worker-release"
	if shared != nil || accountPlan == api.PlanScale {
		accountLabel = uuid.NewString()
		projectSlug += "-" + strings.ReplaceAll(accountLabel, "-", "")[:8]
	}
	f.owner = api.NewClient(f.h.APIDURL, f.h.SeedAccount(t.Context(), accountPlan, accountLabel))
	if shared == nil {
		t.Cleanup(func() { f.cleanup(t) })
	} else {
		t.Cleanup(func() {
			if f.cleanupDone {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			for _, app := range f.apps {
				if err := f.owner.DeleteApp(ctx, app.Slug); err != nil {
					t.Errorf("delete shared fixture: %v", err)
				}
			}
		})
	}
	identity, err := f.owner.Whoami(t.Context())
	mustProjectWorkerNative(t, err)
	f.accountID = identity.ID
	f.project, err = f.store.CreateProject(t.Context(), state.Project{AccountID: f.accountID, Slug: projectSlug})
	mustProjectWorkerNative(t, err)
	_, err = f.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: f.accountID, ProjectID: f.project.ID, Slug: "production"})
	mustProjectWorkerNative(t, err)
	_, err = f.store.UpdateProjectEnvironmentProtection(t.Context(), f.accountID, f.project.ID, "production", false)
	mustProjectWorkerNative(t, err)
	// Seed only customer intent. Every deployment, runtime proof, instance and
	// graph transition below goes through the real daemons and public APIs.
	files := projectWorkerNativeFiles(f.binary, "stable", false)
	if httpOnly {
		files = projectHTTPNativeFiles(f.binary, "stable")
		if serviceGraph {
			files = projectServiceGraphNativeFiles(f.binary, "stable")
		}
	}
	if sourceFiles != nil {
		files = sourceFiles(f.binary, "stable")
	}
	scan, err := reposcan.Scan(files)
	mustProjectWorkerNative(t, err)
	planned, err := reconcile.NewService(f.store, nil, slog.Default()).Plan(t.Context(), f.project, scan, "", "", nil)
	mustProjectWorkerNative(t, err)
	wantWorkloads := 2
	if serviceGraph && !httpOnly {
		wantWorkloads = 3
	}
	if httpOnly && !serviceGraph {
		wantWorkloads = 1
	}
	if len(planned.Added) != wantWorkloads {
		t.Fatalf("fixture must resolve exactly two workloads: %+v", scan.Workloads)
	}
	for _, app := range planned.Added {
		app.Type, app.RAMMB, app.MaxConcurrency = state.AppTypeApp, 512, 2
		app.RequireAuthn, app.PublicAuthMode = false, api.AppPublicAuthModeOpen
		app.Manifest.RevisionPinTTLSeconds = pinTTL
		app.Manifest.ProjectSourceSHA256 = strings.Repeat("0", 64)
		if app.WorkloadName == "processor" {
			app.Manifest.ExecutionMode = api.ExecutionModeWorker
			app.Manifest.StopGracePeriodS = int(projectWorkerNativeGrace / time.Second)
			app.Manifest.RequestTimeoutS = 60
		}
		created, err := f.store.CreateApp(t.Context(), app)
		mustProjectWorkerNative(t, err)
		f.apps[created.WorkloadName] = created
		mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, created.ID, "production", "MODE", "stable-config"))
		if serviceGraph && created.WorkloadName == "frontend" {
			for key, value := range api.ServiceBindingEnvForTransport(nil, created.Manifest.ServiceBindings, created.Manifest.ServiceBindingTransport) {
				mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, created.ID, "production", key, value))
			}
		}
	}
	if f.apps["frontend"].ID == "" || (!httpOnly && f.apps["processor"].ID == "") {
		t.Fatal("fixture workload identity changed")
	}
	if setup != nil {
		setup(f)
	}
	// Route probes address the harness gateway, never production ingress.
	mustProjectWorkerNative(t, f.h.SetAPIDEnv("FAAS_ROUTE_PROBE_URL", f.h.GatewayURL))
	mustProjectWorkerNative(t, f.h.RestartAPID())
	source := projectWorkerNativeArchive(t, files)
	plan, err := f.scan(t, t.Context(), source)
	mustProjectWorkerNative(t, err)
	initial, err := f.apply(t, t.Context(), plan.PlanToken, source)
	mustProjectWorkerNative(t, err)
	if len(initial.Builds) != wantWorkloads {
		t.Fatalf("initial source deployment accepted %d builds, want two", len(initial.Builds))
	}
	deployments := map[string]string{}
	for _, build := range initial.Builds {
		ctx, cancel := context.WithTimeout(t.Context(), 16*time.Minute)
		_, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, build.DeploymentID, e2etest.DefaultBuildStallWindow, 15*time.Minute)
		cancel()
		mustProjectWorkerNative(t, err)
		deployments[build.Slug] = build.DeploymentID
		f.deploymentCounts[build.AppID]++
	}
	f.baseline, err = f.owner.PublishProjectReleaseSet(t.Context(), f.project.Slug, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: min(1800, pinTTL), Deployments: deployments})
	mustProjectWorkerNative(t, err)
	_, err = f.owner.SetRouteHealthGate(t.Context(), f.apps["frontend"].Slug, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: new(int64), Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/critical", Probe: &api.RouteHealthProbe{Path: "/critical"}}}})
	mustProjectWorkerNative(t, err)
	if !httpOnly {
		enabled := true
		binding, err := f.owner.CreateQueueBinding(t.Context(), f.apps["processor"].Slug, api.CreateQueueBindingRequest{Name: "orders", QueueName: "orders", Mode: "push", Environment: "production", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: 1})
		mustProjectWorkerNative(t, err)
		f.bindingID = binding.ID
	}
	f.assertHTTP(t, "stable", "stable-config")
	return f
}

func projectWorkerNativeBinary(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projectworker")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-ldflags=-s -w", "-o", path, "../../pkg/e2etest/testdata/projectworker")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build native workload: %v\n%s", err, output)
	}
	binary, err := os.ReadFile(path)
	mustProjectWorkerNative(t, err)
	return binary
}

func projectWorkerNativeFiles(binary []byte, version string, broken bool) fstest.MapFS {
	files := fstest.MapFS{
		"docker-compose.yml": {Data: []byte("services:\n  frontend:\n    build: { context: ., dockerfile: Dockerfile.frontend }\n    ports: [\"8080:8080\"]\n  processor:\n    build: { context: ., dockerfile: Dockerfile.processor }\n    ports: [\"8080:8080\"]\n")},
		"projectworker":      {Data: binary, Mode: 0755},
		"release-version":    {Data: []byte(version + "\n")},
	}
	for name, role := range map[string]string{"frontend": "api", "processor": "worker"} {
		command := fmt.Sprintf("[\"/projectworker\",%q]", role)
		if name == "processor" && broken {
			// The image builds normally; guest exec must reject the missing
			// entrypoint before issuing the startup/configuration acknowledgement.
			command = `["/missing-worker"]`
		}
		files["Dockerfile."+name] = &fstest.MapFile{Data: []byte("FROM scratch\nCOPY projectworker /projectworker\nCOPY release-version /release-version\nEXPOSE 8080\nENTRYPOINT " + command + "\n")}
	}
	return files
}

func projectWorkerNativeArchive(t *testing.T, files fstest.MapFS) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(zip)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		file := files[name]
		mode := int64(0644)
		if file.Mode != 0 {
			mode = int64(file.Mode)
		}
		mustProjectWorkerNative(t, tarWriter.WriteHeader(&tar.Header{Name: name, Mode: mode, Typeflag: tar.TypeReg, Size: int64(len(file.Data))}))
		_, err := tarWriter.Write(file.Data)
		mustProjectWorkerNative(t, err)
	}
	mustProjectWorkerNative(t, tarWriter.Close())
	mustProjectWorkerNative(t, zip.Close())
	return buffer.Bytes()
}

func (f *projectWorkerNativeFixture) scan(t *testing.T, ctx context.Context, source []byte) (api.PlanResponse, error) {
	t.Helper()
	return f.owner.ScanProjectWithBindingEnvironment(ctx, bytes.NewReader(source), "project.tgz", f.project.Slug, "", "", 0, nil, nil, false, true, "production")
}

func (f *projectWorkerNativeFixture) apply(t *testing.T, ctx context.Context, token string, source []byte) (api.ApplyResponse, error) {
	t.Helper()
	return f.owner.ApplyProjectPlanWithSecrets(ctx, token, bytes.NewReader(source), "project.tgz", f.project.Slug, "", "", 0, nil, nil, false, true, "production", "", nil)
}

func (f *projectWorkerNativeFixture) candidate(t *testing.T, version string, broken, stopAPID bool) (api.ApplyResponse, string) {
	t.Helper()
	values := map[string]map[string]string{}
	for name := range f.apps {
		values[name] = map[string]string{"MODE": version + "-config"}
		for key, value := range f.releaseValues[name] {
			values[name][key] = value
		}
	}
	raw, err := json.Marshal(api.ProjectReleaseConfigRequest{Env: values})
	mustProjectWorkerNative(t, err)
	ctx := api.ContextWithProjectReleaseWorkers(api.ContextWithProjectReleaseConfig(t.Context(), raw))
	files := projectWorkerNativeFiles(f.binary, version, broken)
	if f.sourceFiles != nil && !broken {
		files = f.sourceFiles(f.binary, version)
	}
	source := projectWorkerNativeArchive(t, files)
	plan, err := f.scan(t, ctx, source)
	mustProjectWorkerNative(t, err)
	if plan.SafeRelease == nil || plan.SafeRelease.Worker == nil || plan.SafeRelease.Worker.AppID != f.apps["processor"].ID {
		t.Fatal("server did not bind the production worker")
	}
	attempt := strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx = api.ContextWithDeploymentAttempt(api.ContextWithIdempotencyKey(ctx, attempt), attempt)
	accepted, err := f.apply(t, ctx, plan.PlanToken, source)
	mustProjectWorkerNative(t, err)
	if stopAPID {
		// Stop the release owner at acceptance, before native source builds
		// finish. Imaged/schedd/queue delivery continue against durable intent.
		mustProjectWorkerNative(t, f.h.KillAPID())
		f.apidStopped = true
	}
	if accepted.SafeRelease == nil || accepted.SafeRelease.Status != "building" || accepted.SubmissionAttemptID != attempt || len(accepted.Builds) != len(f.apps) {
		t.Fatalf("candidate admission did not return one building batch: %+v", accepted)
	}
	for _, build := range accepted.Builds {
		f.deploymentCounts[build.AppID]++
	}
	f.assertGraph(t, f.baseline.ID)
	return accepted, attempt
}

func (f *projectWorkerNativeFixture) assertGraph(t *testing.T, id string) {
	t.Helper()
	graph, err := f.store.ActiveProjectReleaseSet(t.Context(), f.accountID, f.project.ID, "production")
	mustProjectWorkerNative(t, err)
	if graph.ID != id {
		t.Fatalf("active graph=%s, want %s", graph.ID, id)
	}
}

func (f *projectWorkerNativeFixture) worker(t *testing.T, deployment string) state.Instance {
	t.Helper()
	var selected state.Instance
	waitProjectWorkerNative(t, time.Minute, "running worker for "+deployment, func() bool {
		instances, err := f.store.ListInstancesForApp(t.Context(), f.apps["processor"].ID)
		mustProjectWorkerNative(t, err)
		for _, instance := range instances {
			if instance.DeploymentID == deployment && instance.Mode == string(state.InstanceModeWorker) && instance.State == string(state.StateRunning) && instance.HostIP != "" {
				selected = instance
				return true
			}
		}
		return false
	})
	return selected
}

func projectWorkerNativeRequest(t *testing.T, url, host, method string) ([]byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, url, nil)
	mustProjectWorkerNative(t, err)
	request.Host = host
	// Guest evidence reads use the local native VM address and never a proxy.
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 10 * time.Second}
	response, err := client.Do(request)
	mustProjectWorkerNative(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	mustProjectWorkerNative(t, err)
	return body, response.StatusCode
}

func projectWorkerNativeState(t *testing.T, instance state.Instance) projectWorkerNativeReport {
	t.Helper()
	url := "http://" + net.JoinHostPort(instance.HostIP, "8080") + "/state"
	body, status := projectWorkerNativeRequest(t, url, "", http.MethodGet)
	if status != http.StatusOK {
		t.Fatalf("native worker evidence status=%d body=%s", status, body)
	}
	var report projectWorkerNativeReport
	mustProjectWorkerNative(t, json.Unmarshal(body, &report))
	return report
}

func (f *projectWorkerNativeFixture) assertHTTP(t *testing.T, generation, mode string) {
	t.Helper()
	body, status := projectWorkerNativeRequest(t, f.h.EdgeURL()+"/", f.apps["frontend"].Slug+".apps.test.example", http.MethodGet)
	var report projectWorkerNativeReport
	mustProjectWorkerNative(t, json.Unmarshal(body, &report))
	if f.secretRelease && !report.SecretMatchesGeneration {
		t.Fatal("native release secret mismatch")
	}
	if f.serviceGraph {
		assertNativeServiceGraphReport(t, report)
	}
	if status != http.StatusOK || report.Generation != generation || report.Mode != mode {
		t.Fatalf("HTTP release/configuration mismatch: status=%d body=%s", status, body)
	}
}

func (f *projectWorkerNativeFixture) enqueue(t *testing.T, label string, hold bool) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"label": label, "hold": hold})
	mustProjectWorkerNative(t, err)
	if !f.apidStopped {
		queued, err := f.owner.QueueSend(t.Context(), f.apps["processor"].Slug, api.QueueSendRequest{Environment: "production", QueueName: "orders", Payload: payload})
		mustProjectWorkerNative(t, err)
		return queued.ID
	}
	// Inject a producer event while APID is down, without impersonating its
	// release controller. The real named-binding poller and gateway deliver it.
	queued, err := f.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: f.accountID, AppID: f.apps["processor"].ID, DeploymentScope: "production", QueueBindingID: f.bindingID, QueueName: "orders", Source: state.InvocationQueue, Method: http.MethodPost, Path: "/", Payload: payload, DueAt: time.Now()})
	mustProjectWorkerNative(t, err)
	return queued.ID
}

func (f *projectWorkerNativeFixture) awaitCompleted(t *testing.T, id string) {
	t.Helper()
	waitProjectWorkerNative(t, time.Minute, "first-attempt queue completion", func() bool {
		inv, err := f.store.InvocationByID(t.Context(), id)
		mustProjectWorkerNative(t, err)
		if inv.Attempts > 1 || inv.State == state.InvocationFailed || inv.State == state.InvocationDeadLetter {
			t.Fatalf("native queue delivery failed or replayed: %+v", inv)
		}
		return inv.State == state.InvocationCompleted
	})
	f.completed = append(f.completed, id)
}

func (f *projectWorkerNativeFixture) receipt(t *testing.T, attempt, status string) api.ProjectSubmissionReceipt {
	t.Helper()
	var selected api.ProjectSubmissionReceipt
	waitProjectWorkerNative(t, 5*time.Minute, "release receipt "+status, func() bool {
		receipt, err := f.owner.GetProjectSubmission(t.Context(), attempt)
		mustProjectWorkerNative(t, err)
		if receipt.Apply.SafeRelease == nil {
			t.Fatal("durable release receipt lost worker policy")
		}
		selected = receipt
		current := receipt.Apply.SafeRelease.Status
		if current == status {
			return true
		}
		if current == "failed" || current == "conflicted" || current == "succeeded" || current == "rolled_back" {
			t.Fatalf("unexpected terminal release status: want=%s receipt=%+v", status, receipt)
		}
		return false
	})
	return selected
}

func (f *projectWorkerNativeFixture) assertNoResubmission(t *testing.T, accepted api.ApplyResponse) {
	t.Helper()
	for _, build := range accepted.Builds {
		var count int
		mustProjectWorkerNative(t, f.h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM deployments WHERE app_id=$1`, build.AppID).Scan(&count))
		if count != f.deploymentCounts[build.AppID] {
			t.Fatalf("restart duplicated candidate acceptance: app=%s count=%d", build.AppID, count)
		}
	}
}

func (f *projectWorkerNativeFixture) cleanup(t *testing.T) {
	t.Helper()
	if f.cleanupDone {
		return
	}
	f.cleanupDone = true
	if t.Failed() {
		f.h.DumpLogs(t)
	}
	if f.apidStopped {
		if err := f.h.RestartAPID(); err != nil {
			t.Errorf("restore APID for cleanup: %v", err)
		} else {
			f.apidStopped = false
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, app := range f.apps {
		if err := f.owner.DeleteApp(ctx, app.Slug); err != nil {
			t.Errorf("delete native fixture: %v", err)
		}
	}
	for ctx.Err() == nil {
		settled := true
		for _, app := range f.apps {
			instances, err := f.store.ListInstancesForApp(ctx, app.ID)
			if err != nil {
				t.Errorf("read cleanup instances: %v", err)
				settled = false
				continue
			}
			for _, instance := range instances {
				if instance.State != string(state.StateStopped) && instance.State != string(state.StateFailed) && instance.State != string(state.StateParked) {
					settled = false
				}
			}
		}
		if settled && len(leakcheck.Zero()) == 0 {
			leakcheck.AssertZero(t)
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Error("native worker fixture instances did not retire before cleanup deadline")
	leakcheck.AssertZero(t)
}

func waitProjectWorkerNative(t *testing.T, budget time.Duration, description string, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", description, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func mustProjectWorkerNative(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func projectHTTPNativeFiles(binary []byte, version string) fstest.MapFS {
	files := projectWorkerNativeFiles(binary, version, false)
	delete(files, "Dockerfile.processor")
	files["docker-compose.yml"].Data = []byte("services:\n  frontend:\n    build: { context: ., dockerfile: Dockerfile.frontend }\n    ports: [\"8080:8080\"]\n")
	return files
}
