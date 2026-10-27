//go:build promotionacceptance && metal

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sys/unix"
)

type environmentPromotionNative struct {
	*projectWorkerNativeFixture
	source         map[string]state.Deployment
	sourceConfig   api.ProjectEnvironmentConfigResponse
	targetConfig   api.ProjectEnvironmentConfigResponse
	counts         map[string]int
	buildCounts    map[string]int
	values         map[string]string
	workloadValues map[string]map[string]string
}

type promotionNativeOptions struct {
	serviceGraph   bool
	files          func([]byte, string) fstest.MapFS
	beforeDeploy   func(*projectWorkerNativeFixture)
	beforeStaging  func(*projectWorkerNativeFixture)
	values         map[string]string
	workloadValues map[string]map[string]string
	environment    []string
}

func TestProjectEnvironmentPromotionFixtureMeta(t *testing.T) {
	t.Run("fault_ownership", qualifyPromotionVMFaultOwnership)
	t.Run("load_capacity", func(t *testing.T) {
		limits := api.MustLimitsFor(api.PlanScale)
		// Each target can retain its baseline and candidate concurrently, alongside
		// production and staging. Use the fixture's actual 512 MB app setting.
		instances := 2*promotionNativeLoadTargets + 2
		if instances > limits.MaxConcurrency || instances*promotionNativeLoadAccounts*(512+api.PerVMOverheadMB) > api.RAMAdmissionCeilingMB {
			t.Fatal("native load fixture exceeds ordinary admission capacity")
		}
		if promotionNativeLoadTargets*promotionNativeLoadAccounts <= api.ProjectPromotionHealthBatchSize {
			t.Fatal("load fixture no longer exercises the previous batch starvation boundary")
		}
		source, err := reposcan.Scan(projectHTTPNativeFiles([]byte("fixture"), "baseline"))
		mustProjectWorkerNative(t, err)
		if len(source.Workloads) != 1 || source.Workloads[0].Name != "frontend" {
			t.Fatal("load fixture is not a single HTTP workload")
		}
	})
	t.Run("admission_capacity", qualifyPromotionAdmissionNativeCapacity)
	t.Run("migration_source", func(t *testing.T) {
		for _, version := range []string{"stable", "baseline", "tested"} {
			scan, err := reposcan.Scan(promotionMigrationNativeFiles([]byte("fixture"), version))
			mustProjectWorkerNative(t, err)
			if len(scan.Workloads) != 1 || scan.Workloads[0].Name != "frontend" {
				t.Fatal("migration fixture must contain one HTTP workload")
			}
			command := scan.Workloads[0].ReleaseCommand
			if (len(command) > 0) != (version == "tested") || version == "tested" && !strings.Contains(strings.Join(command, " "), "/projectworker migration-promotion") {
				t.Fatal("migration command must belong only to the tested artifact")
			}
		}
	})

	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "promotion-fixture@example.test", api.PlanPro)
	mustProjectWorkerNative(t, err)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "promotion-fixture"})
	mustProjectWorkerNative(t, err)
	scan, err := reposcan.Scan(environmentPromotionNativeFiles([]byte("fixture"), "stable"))
	mustProjectWorkerNative(t, err)
	plan, err := reconcile.NewService(store, nil, nil).Plan(t.Context(), project, scan, "", "", nil)
	mustProjectWorkerNative(t, err)
	if len(plan.Added) != 2 {
		t.Fatal("promotion fixture changed membership")
	}
	workers := 0
	for i, app := range plan.Added {
		if app.WorkloadName == "processor" {
			if app.WorkloadClass != state.WorkloadClassWorker {
				t.Fatal("native promotion source did not classify its worker")
			}
			plan.Added[i].Manifest.ExecutionMode = api.ExecutionModeWorker
			workers++
		}
	}
	if workers != 1 {
		t.Fatal("promotion fixture must have exactly one worker")
	}
	source, err := reposcan.Scan(environmentPromotionNativeFiles([]byte("fixture"), "tested"))
	mustProjectWorkerNative(t, err)
	_, err = reconcile.ProjectSafeSourceApps(plan.Added, source, false, true)
	mustProjectWorkerNative(t, err)
}

// Pin a verified native Firecracker process before signalling it. An app's
// inner process crash can restart inside the same VM and does not exercise
// scheduler recovery. No PID-name-wide kill or lifecycle row mutation is used.
func (p *environmentPromotionNative) killWorkerVM(t *testing.T, instance state.Instance) {
	t.Helper()
	if instance.AppID != p.apps["processor"].ID || instance.GuestUID <= 0 {
		t.Fatal("native fault target is not this fixture's worker")
	}
	p.killGraphVM(t, instance, p.apps["processor"].ID)
}

func (p *environmentPromotionNative) killGraphVM(t *testing.T, instance state.Instance, appID string) {
	t.Helper()
	owned := false
	for _, app := range p.apps {
		owned = owned || app.ID == appID
	}
	if !owned || appID == "" || instance.AppID != appID || instance.GuestUID <= 0 {
		t.Fatal("native fault target is not a fixture-owned guest")
	}
	entries, err := os.ReadDir("/proc")
	mustProjectWorkerNative(t, err)
	var matches []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err == nil && pid > 1 && promotionWorkerProcess(filepath.Join("/proc", entry.Name()), instance) {
			matches = append(matches, pid)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("native worker VM fault requires one owned process, found %d", len(matches))
	}
	fd, err := unix.PidfdOpen(matches[0], 0)
	mustProjectWorkerNative(t, err)
	defer func() {
		if err := unix.Close(fd); err != nil {
			t.Errorf("close native VM fault handle: %v", err)
		}
	}()
	if !promotionWorkerProcess(filepath.Join("/proc", strconv.Itoa(matches[0])), instance) {
		t.Fatal("native worker process identity changed before fault")
	}
	mustProjectWorkerNative(t, unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0))
}

func promotionWorkerProcess(path string, instance state.Instance) bool {
	comm, err := os.ReadFile(filepath.Join(path, "comm"))
	if err != nil || !strings.HasPrefix(string(comm), "firecracker") {
		return false
	}
	executable, err := os.Readlink(filepath.Join(path, "exe"))
	name := filepath.Base(executable)
	if err != nil || name != "firecracker" && !strings.HasPrefix(name, "firecracker-v") {
		return false
	}
	cgroup, err := os.ReadFile(filepath.Join(path, "cgroup"))
	if err != nil || !promotionWorkerCgroup(string(cgroup), instance.ID) {
		return false
	}
	status, err := os.ReadFile(filepath.Join(path, "status"))
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[0] == "Uid:" {
			for _, field := range fields[1:] {
				if field != strconv.Itoa(instance.GuestUID) {
					return false
				}
			}
			return true
		}
	}
	return false
}

func promotionWorkerCgroup(raw, id string) bool {
	for line := range strings.SplitSeq(raw, "\n") {
		if path, ok := strings.CutPrefix(line, "0::/"); ok {
			for part := range strings.SplitSeq(path, "/") {
				if part == id && id != "" {
					return true
				}
			}
		}
	}
	return false
}

func qualifyPromotionVMFaultOwnership(t *testing.T) {
	instance := state.Instance{ID: "11111111-1111-4111-8111-111111111111", GuestUID: 20001}
	for _, tc := range []struct {
		name, cgroup, executable, uid string
		want                          bool
	}{
		{"owned", "0::/faas-tenant-pro.slice/" + instance.ID, "/firecracker-v1.7.0-x86_64", "20001", true},
		{"neighbor", "0::/faas-tenant-pro.slice/22222222-2222-4222-8222-222222222222", "/firecracker", "20001", false},
		{"prefix", "0::/faas-tenant-pro.slice/" + instance.ID + "-other", "/firecracker", "20001", false},
		{"other_uid", "0::/faas-tenant-pro.slice/" + instance.ID, "/firecracker", "20002", false},
		{"other_executable", "0::/faas-tenant-pro.slice/" + instance.ID, "/unrelated-firecracker", "20001", false},
		{"cgroup_v1", "1:cpu:/" + instance.ID, "/firecracker", "20001", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir()
			for name, value := range map[string]string{"comm": "firecracker\n", "cgroup": tc.cgroup, "status": "Uid:\t" + strings.Repeat(tc.uid+"\t", 4) + "\n"} {
				mustProjectWorkerNative(t, os.WriteFile(filepath.Join(path, name), []byte(value), 0600))
			}
			mustProjectWorkerNative(t, os.Symlink(tc.executable, filepath.Join(path, "exe")))
			if promotionWorkerProcess(path, instance) != tc.want {
				t.Fatal("native fault ownership guard accepted the wrong process")
			}
		})
	}
}

// No port mapping on the processor: repository admission must classify it as
// a worker, while its image still exposes the fixture's private evidence port.
func environmentPromotionNativeFiles(binary []byte, version string) fstest.MapFS {
	files := projectWorkerNativeFiles(binary, version, false)
	files["docker-compose.yml"].Data = []byte("services:\n  frontend:\n    build: { context: ., dockerfile: Dockerfile.frontend }\n    ports: [\"8080:8080\"]\n  processor:\n    build: { context: ., dockerfile: Dockerfile.processor }\n")
	return files
}

func newEnvironmentPromotionNative(t *testing.T) *environmentPromotionNative {
	return newEnvironmentPromotionNativeConfigured(t, false, nil)
}

func newEnvironmentPromotionNativeConfigured(t *testing.T, httpOnly bool, shared *e2etest.Harness) *environmentPromotionNative {
	return newEnvironmentPromotionNativeWithOptions(t, httpOnly, shared, promotionNativeOptions{})
}

func newEnvironmentPromotionNativeWithOptions(t *testing.T, httpOnly bool, shared *e2etest.Harness, options promotionNativeOptions) *environmentPromotionNative {
	t.Helper()
	setup := func(f *projectWorkerNativeFixture) {
		f.releaseValues = options.workloadValues
		// Staging, the selected production worker and a retained job's pool
		// must fit at steady state, after the cutover overlap grant ends.
		if !httpOnly {
			worker := f.apps["processor"]
			worker, err := f.store.UpdateApp(t.Context(), worker.ID, state.UpdateAppParams{MaxConcurrency: new(3)})
			mustProjectWorkerNative(t, err)
			f.apps["processor"] = worker
		} else {
			app := f.apps["frontend"]
			app, err := f.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{MaxConcurrency: new(api.MustLimitsFor(api.PlanScale).MaxConcurrency)})
			mustProjectWorkerNative(t, err)
			f.apps["frontend"] = app
		}
		for _, app := range f.apps {
			mustProjectWorkerNative(t, f.owner.SetSecretWithScope(t.Context(), app.Slug, "TOKEN", "promotion-production-secret", "production"))
		}
		if options.beforeDeploy != nil {
			options.beforeDeploy(f)
		}
	}
	files := environmentPromotionNativeFiles
	accountPlan := api.PlanPro
	if httpOnly {
		files = projectHTTPNativeFiles
		accountPlan = api.PlanScale
	}
	if options.serviceGraph {
		accountPlan = api.PlanScale
	}
	if options.files != nil {
		files = options.files
	}
	environment := append([]string{"FAAS_PROMOTION_HEALTH_ENABLED=1", "FAAS_PROMOTION_WORKERS_ENABLED=1", "FAAS_PROMOTION_ROLLOUT_QUALIFICATION=1"}, options.environment...)
	f := newProjectReleaseNativeFixtureOnHarness(t, 3600, setup, httpOnly, options.serviceGraph, files, shared, accountPlan, environment...)
	// A real paired release supplies immutable destination variable snapshots.
	var attempt string
	if httpOnly {
		values := map[string]string{"MODE": "baseline-config"}
		for key, value := range options.values {
			values[key] = value
		}
		allValues := map[string]map[string]string{}
		for name := range f.apps {
			allValues[name] = values
		}
		raw, err := json.Marshal(api.ProjectReleaseConfigRequest{Env: allValues})
		mustProjectWorkerNative(t, err)
		ctx := api.ContextWithProjectReleaseConfig(t.Context(), raw)
		source := projectWorkerNativeArchive(t, files(f.binary, "baseline"))
		preview, err := f.scan(t, ctx, source)
		mustProjectWorkerNative(t, err)
		attempt = strings.ReplaceAll(uuid.NewString(), "-", "")
		ctx = api.ContextWithDeploymentAttempt(api.ContextWithIdempotencyKey(ctx, attempt), attempt)
		accepted, err := f.apply(t, ctx, preview.PlanToken, source)
		mustProjectWorkerNative(t, err)
		for _, build := range accepted.Builds {
			ctx, cancel := context.WithTimeout(t.Context(), 16*time.Minute)
			_, _, err = e2etest.WaitForSourceDeployment(ctx, t, f.h.Pool, build.DeploymentID, e2etest.DefaultBuildStallWindow, 15*time.Minute)
			cancel()
			mustProjectWorkerNative(t, err)
		}
	} else {
		accepted, id := f.candidate(t, "baseline", false, true)
		attempt = id
		f.readyCandidates(t, accepted)
		mustProjectWorkerNative(t, f.h.RestartAPID())
		f.apidStopped = false
	}
	f.receipt(t, attempt, "succeeded")
	baseline, err := f.owner.GetActiveProjectReleaseSet(t.Context(), f.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	deployments := map[string]string{}
	for _, member := range baseline.Members {
		for name, app := range f.apps {
			if app.ID == member.AppID {
				deployments[name] = member.DeploymentID
			}
		}
	}
	// Refresh retention before the staging builds, using the public graph API.
	f.baseline, err = f.owner.PublishProjectReleaseSet(t.Context(), f.project.Slug, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: deployments})
	mustProjectWorkerNative(t, err)
	if !httpOnly {
		_, err = f.owner.UpdateQueueBinding(t.Context(), f.apps["processor"].Slug, f.bindingID, api.UpdateQueueBindingRequest{RetryPolicy: &api.RetryPolicyDTO{MaxAttempts: 3, BaseSeconds: 480, MaxSeconds: 480}})
		mustProjectWorkerNative(t, err)
	}
	_, err = f.owner.CreateProjectEnvironment(t.Context(), f.project.Slug, api.CreateProjectEnvironmentRequest{Slug: "staging"})
	mustProjectWorkerNative(t, err)
	if options.beforeStaging != nil {
		options.beforeStaging(f)
	}
	for _, app := range f.apps {
		// Customer intent only; source builds, runtime acknowledgements, graph
		// publication and queue evidence all use their actual daemon owners.
		mustProjectWorkerNative(t, f.store.UpsertAppEnvInScope(t.Context(), f.accountID, app.ID, "staging", "MODE", "tested-config"))
		mustProjectWorkerNative(t, f.owner.SetSecretWithScope(t.Context(), app.Slug, "TOKEN", "promotion-staging-secret", "staging"))
	}
	p := &environmentPromotionNative{projectWorkerNativeFixture: f, source: map[string]state.Deployment{}, counts: map[string]int{}, buildCounts: map[string]int{}, values: options.values, workloadValues: options.workloadValues}
	p.targetConfig = p.config(t, "production", "baseline-config")
	p.sourceConfig = p.config(t, "staging", "tested-config")
	source := projectWorkerNativeArchive(t, files(f.binary, "tested"))
	plan, err := f.owner.ScanProjectWithBindingEnvironment(t.Context(), bytes.NewReader(source), "project.tgz", f.project.Slug, "", "", 0, nil, nil, false, true, "staging")
	mustProjectWorkerNative(t, err)
	result, err := f.owner.ApplyProjectPlanWithSecrets(t.Context(), plan.PlanToken, bytes.NewReader(source), "project.tgz", f.project.Slug, "", "", 0, nil, nil, false, true, "staging", "", nil)
	mustProjectWorkerNative(t, err)
	if len(result.Builds) != len(f.apps) {
		t.Fatal("staging source must build both workloads")
	}
	deployments = map[string]string{}
	for _, build := range result.Builds {
		ctx, cancel := context.WithTimeout(t.Context(), 16*time.Minute)
		_, _, err = e2etest.WaitForSourceDeployment(ctx, t, f.h.Pool, build.DeploymentID, e2etest.DefaultBuildStallWindow, 15*time.Minute)
		cancel()
		mustProjectWorkerNative(t, err)
		d, err := f.store.DeploymentByID(t.Context(), build.DeploymentID)
		mustProjectWorkerNative(t, err)
		p.source[build.AppID] = d
		deployments[build.Slug] = d.ID
	}
	_, err = f.owner.PublishProjectReleaseSet(t.Context(), f.project.Slug, "staging", api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: deployments})
	mustProjectWorkerNative(t, err)
	if !httpOnly {
		stageWorker := f.worker(t, p.source[f.apps["processor"].ID].ID)
		assertProjectWorkerIsolated(t, stageWorker, "tested", "tested-config")
		if projectWorkerNativeState(t, stageWorker).DestinationSecretMatches {
			t.Fatal("staging received production credentials")
		}
	}
	// Renew the exact retained baseline after potentially slow real builds.
	deployments = map[string]string{}
	for _, member := range f.baseline.Members {
		for name, app := range f.apps {
			if app.ID == member.AppID {
				deployments[name] = member.DeploymentID
			}
		}
	}
	f.baseline, err = f.owner.PublishProjectReleaseSet(t.Context(), f.project.Slug, "production", api.PublishProjectReleaseSetRequest{TTLSeconds: 3600, Deployments: deployments})
	mustProjectWorkerNative(t, err)
	if !httpOnly {
		f.baselineWorker(t)
	}
	for _, app := range f.apps {
		var count int
		mustProjectWorkerNative(t, f.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE app_id=$1", app.ID).Scan(&count))
		p.counts[app.ID] = count
		var builds int
		mustProjectWorkerNative(t, f.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1", app.ID).Scan(&builds))
		p.buildCounts[app.ID] = builds
	}
	return p
}

func (p *environmentPromotionNative) config(t *testing.T, scope, mode string) api.ProjectEnvironmentConfigResponse {
	t.Helper()
	values := map[string]map[string]string{}
	for name := range p.apps {
		values[name] = map[string]string{"MODE": mode}
		for key, value := range p.values {
			values[name][key] = value
		}
		for key, value := range p.workloadValues[name] {
			values[name][key] = value
		}
	}
	raw, err := json.Marshal(api.ProjectReleaseConfigRequest{Env: values})
	mustProjectWorkerNative(t, err)
	c, err := p.owner.UpdateProjectEnvironmentConfig(t.Context(), p.project.Slug, scope, api.UpdateProjectEnvironmentConfigRequest{Values: raw})
	mustProjectWorkerNative(t, err)
	return c
}

func (p *environmentPromotionNative) status(t *testing.T, id string) api.ProjectEnvironmentPromotionStatusResponse {
	t.Helper()
	s, err := p.owner.GetProjectEnvironmentPromotionStatus(t.Context(), p.project.Slug, "production", id)
	mustProjectWorkerNative(t, err)
	if s.Health == nil || p.apps["processor"].ID != "" && s.Health.Policy.Worker == nil || s.Health.Policy.Migration != nil {
		t.Fatal("promotion lost its persisted HTTP/worker policy")
	}
	return s
}

func (p *environmentPromotionNative) controlWorker(t *testing.T, instance state.Instance, control string, want int) {
	t.Helper()
	_, status := projectWorkerNativeRequest(t, "http://"+net.JoinHostPort(instance.HostIP, "8080")+"/control/"+control, "", http.MethodPost)
	if status != want {
		t.Fatalf("worker fault %s status=%d", control, status)
	}
}

func (p *environmentPromotionNative) assertArtifacts(t *testing.T, s api.ProjectEnvironmentPromotionStatusResponse) {
	t.Helper()
	if len(s.Workloads) != len(p.apps) {
		t.Fatal("promotion changed project membership")
	}
	for _, w := range s.Workloads {
		d, err := p.store.DeploymentByID(t.Context(), w.TargetDeploymentID)
		mustProjectWorkerNative(t, err)
		source := p.source[d.AppID]
		if source.ID != w.SourceDeploymentID || d.Scope != "production" || d.RootfsKey == "" ||
			d.RootfsKey != source.RootfsKey || d.SourceSHA256 != source.SourceSHA256 || d.ImageDigest != source.ImageDigest {
			t.Fatal("promotion rebuilt or changed a tested artifact")
		}
		var count int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE app_id=$1", d.AppID).Scan(&count))
		if count != p.counts[d.AppID]+1 {
			t.Fatal("promotion restart or replay duplicated candidate creation")
		}
		var builds int
		mustProjectWorkerNative(t, p.h.Pool.QueryRow(t.Context(), "SELECT count(*) FROM builds b JOIN deployments d ON d.id=b.deployment_id WHERE d.app_id=$1", d.AppID).Scan(&builds))
		if builds != p.buildCounts[d.AppID] {
			t.Fatal("promotion or recovery submitted another build")
		}
	}
}

func (p *environmentPromotionNative) assertConfig(t *testing.T, hash string) {
	t.Helper()
	c, err := p.owner.GetProjectEnvironmentConfig(t.Context(), p.project.Slug, "production")
	mustProjectWorkerNative(t, err)
	if c.ConfigHash != hash {
		t.Fatal("published environment document differs from selected runtime")
	}
}

func (p *environmentPromotionNative) assertHTTP(t *testing.T, generation, mode string) {
	t.Helper()
	p.projectWorkerNativeFixture.assertHTTP(t, generation, mode)
	body, status := projectWorkerNativeRequest(t, p.h.EdgeURL()+"/", p.apps["frontend"].Slug+".apps.test.example", http.MethodGet)
	var report projectWorkerNativeReport
	mustProjectWorkerNative(t, json.Unmarshal(body, &report))
	if status != http.StatusOK || !report.DestinationSecretMatches {
		t.Fatal("HTTP guest lost destination credentials")
	}
}

func (p *environmentPromotionNative) job(t *testing.T, label string, generation, mode string) state.Invocation {
	t.Helper()
	id := p.enqueue(t, label, false)
	p.awaitCompleted(t, id)
	inv, err := p.store.InvocationByID(t.Context(), id)
	mustProjectWorkerNative(t, err)
	var report projectWorkerNativeReport
	mustProjectWorkerNative(t, json.Unmarshal(inv.Result, &report))
	if report.Generation != generation || report.Mode != mode || !report.DestinationSecretMatches {
		t.Fatalf("job %s executed with the wrong artifact/configuration/credential scope", label)
	}
	return inv
}

func (p *environmentPromotionNative) baselineSamples(t *testing.T) {
	t.Helper()
	for i := 0; i < 10; i++ {
		p.job(t, fmt.Sprintf("baseline-%d", i), "baseline", "baseline-config")
	}
}
