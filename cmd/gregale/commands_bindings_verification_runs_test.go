package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDurableVerificationCrashChild(t *testing.T) {
	endpoint := os.Getenv("GREGALE_VERIFICATION_TEST_URL")
	if endpoint == "" {
		return
	}
	client := api.NewClient(endpoint, "test")
	id := os.Getenv("GREGALE_VERIFICATION_TEST_RUN")
	if os.Getenv("GREGALE_VERIFICATION_TEST_GRAPH") == "1" {
		if os.Getenv("GREGALE_VERIFICATION_TEST_RESUME") == "1" {
			run, err := client.ResumeProjectBindingVerificationRun(t.Context(), "receipt-graph", "production", id)
			if err != nil {
				t.Fatal(err)
			}
			if code := executeDurableProjectVerification(t.Context(), client, run, time.Millisecond, 10*time.Second); code != 0 {
				t.Fatalf("graph resume exit=%d", code)
			}
		} else {
			var manifest api.CheckedProjectReleaseManifest
			if err := json.Unmarshal([]byte(os.Getenv("GREGALE_VERIFICATION_TEST_MANIFEST")), &manifest); err != nil {
				t.Fatal(err)
			}
			if code := createDurableProjectVerification(t.Context(), client, manifest, id, false, false, time.Millisecond, 10*time.Second); code != 0 {
				t.Fatalf("graph create exit=%d", code)
			}
		}
		return
	}

	if os.Getenv("GREGALE_VERIFICATION_TEST_RESUME") == "1" {
		run, err := client.ResumeBindingVerificationRun(context.Background(), "receipt", id)
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("GREGALE_VERIFICATION_TEST_QUEUE") == "1" {
			bounded, cancel := context.WithTimeout(context.WithValue(context.WithValue(context.Background(), bindingVerificationRunKey{}, &run), bindingVerificationRunStepKey{}, bindingVerificationRunStepContext{Run: &run, Index: 0}), 30*time.Millisecond)
			defer cancel()
			_, err := executeQueueBindingCanary(bounded, client, run.App, "orders", run.DeploymentID, true, time.Millisecond, "")
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("pending queue resume: %v", err)
			}
			return
		}
		if code := executeDurableBindingVerification(context.Background(), client, run, time.Millisecond, 10*time.Second); code != 0 {
			t.Fatalf("resume exit=%d", code)
		}
	} else {
		if code := runDurableBindingVerification(context.Background(), client, "receipt", os.Getenv("GREGALE_VERIFICATION_TEST_DEPLOYMENT"), id, false, false, os.Getenv("GREGALE_VERIFICATION_TEST_QUEUE") == "1", time.Millisecond, 10*time.Second); code != 0 {
			t.Fatalf("create exit=%d", code)
		}
	}
}
func TestDurableVerificationCrashAfterAtomicAdmissionResumesWithoutDuplicate(t *testing.T) {
	t.Run("task", func(t *testing.T) { durableVerificationCrashFixture(t, false) })
	t.Run("queue", func(t *testing.T) { durableVerificationCrashFixture(t, true) })
	t.Run("graph", func(t *testing.T) { durableVerificationCrashFixture(t, false, true) })
}
func durableVerificationCrashFixture(t *testing.T, queue bool, graphOptions ...bool) {
	graph := len(graphOptions) > 0 && graphOptions[0]
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "cli-receipt@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	var project state.Project
	if graph {
		var err error
		project, err = store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "receipt-graph"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "production"}); err != nil && !errors.Is(err, state.ErrConflict) {
			t.Fatal(err)
		}
	}
	services := []api.AppServiceBinding{{Service: "billing", Binding: "BILLING"}}
	if queue {
		services = nil
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "receipt", WorkloadName: "receipt", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{ServiceBindings: services, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	var override json.RawMessage
	if queue {
		override, _ = json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:receipt", OverrideEnv: override})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	var queueRequirements map[string]string
	if queue {
		_, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		queueRequirements = map[string]string{"orders": api.QueueEvidenceHandlerCompletion}
	}
	policy, err := store.SetBindingReleasePolicy(ctx, acct.ID, app.ID, dep.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m", QueueRequirements: queueRequirements})
	if err != nil {
		t.Fatal(err)
	}
	stamp, _, err := store.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Now()
	inventory := api.AppBindingInventory{App: app.Slug, Scope: policy.Scope, Complete: true, GeneratedAt: observed, RequestedDeploymentID: dep.ID, VerificationDeploymentID: dep.ID, VerificationScope: dep.Scope, RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: observed, ConfigChangedAt: &stamp, Deployments: []api.BindingRuntimeDeployment{{DeploymentID: dep.ID, Scope: dep.Scope, DeploymentStatus: "live", Status: "current", Serving: api.BindingRuntimeInstanceCounts{Current: 1}, Resident: api.BindingRuntimeInstanceCounts{Current: 1}}}}, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING", Scope: "app", Access: "invoke", State: "declared", VerificationStatus: "unknown"}}}
	if queue {
		scope := policy.Scope
		inventory.Bindings = []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: scope, QueueEnvironment: &scope, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &observed, QueueCanarySupported: true}}
	}
	var targetInventory api.AppBindingInventory
	var targetPolicy api.BindingReleasePolicy
	var manifest api.CheckedProjectReleaseManifest
	var targetApp state.App
	if graph {
		var err error
		targetApp, err = store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "billing", WorkloadName: "billing", Type: state.AppTypeFunction, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		target, err := store.CreateDeployment(ctx, state.Deployment{AppID: targetApp.ID, Scope: "production", ImageDigest: "sha256:billing"})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetDeploymentRootfs(ctx, target.ID, "/rootfs", "billing-rootfs", 1); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, target.ID); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkAppRuntimeConfigChanged(ctx, targetApp.ID); err != nil {
			t.Fatal(err)
		}
		targetPolicy, err = store.SetBindingReleasePolicy(ctx, acct.ID, targetApp.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m"})
		if err != nil {
			t.Fatal(err)
		}
		stamp, _, err := store.AppRuntimeConfigChangedAt(ctx, targetApp.ID)
		if err != nil {
			t.Fatal(err)
		}
		targetInventory = api.AppBindingInventory{App: targetApp.Slug, Scope: "production", Complete: true, RequestedDeploymentID: target.ID, VerificationDeploymentID: target.ID, VerificationScope: "production", RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ConfigChangedAt: &stamp, ObservedAt: time.Now(), Deployments: []api.BindingRuntimeDeployment{{DeploymentID: target.ID, Scope: "production", DeploymentStatus: "live", Status: "current", Serving: api.BindingRuntimeInstanceCounts{Current: 1}, Resident: api.BindingRuntimeInstanceCounts{Current: 1}}}}}
		empty := ""
		primaryID, _ := uuid.Parse(dep.ID)
		targetID, _ := uuid.Parse(target.ID)
		manifest = api.CheckedProjectReleaseManifest{Project: project.Slug, Environment: "production", TTLSeconds: 60, Deployments: map[string]string{app.Slug: primaryID.String(), targetApp.Slug: targetID.String()}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{app.Slug: policy.Revision, targetApp.Slug: targetPolicy.Revision}, ServiceTargets: map[string]map[string]string{app.Slug: {targetApp.Slug: targetID.String()}, targetApp.Slug: {}}}
		for _, member := range []state.App{app, targetApp} {
			deps, err := store.LiveDeployments(ctx, member.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range deps {
				if d.TrafficPercent > 0 {
					id, _ := uuid.Parse(d.ID)
					manifest.ExpectedFallbackDeployments[member.Slug] = id.String()
				}
			}
		}
		inventory.ServiceTargets = manifest.ServiceTargets[app.Slug]
		inventory.Bindings[0].ServiceTargetDeploymentID = targetID.String()
	}
	var mu sync.Mutex
	backend := bindings.VerificationRunBackend{Store: store, Tasks: store, Canaries: store, AccountID: acct.ID, AppID: app.ID, App: app.Slug, Inventory: func(context.Context, string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
		mu.Lock()
		defer mu.Unlock()
		return inventory, policy, nil
	}, CaptureTask: func(_ context.Context, _ string, step api.BindingVerificationPlanStep, at time.Time) (*state.CreateAppTaskParams, error) {
		return &state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, RequireLiveDeployment: true, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: step.Name, Revision: strings.Repeat("a", 64), TargetDeploymentID: step.TargetDeploymentID}, Command: func() []string {
			if step.TargetDeploymentID != "" {
				return []string{api.AppTaskServiceBindingTargetProbeCommand, step.Name, step.TargetDeploymentID}
			}
			return []string{api.AppTaskServiceBindingProbeCommand, step.Name}
		}(), TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, CreatedAt: at}, nil
	}}
	backend.InventoryTargets = func(ctx context.Context, dep string, targets map[string]string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
		return backend.Inventory(ctx, dep)
	}
	targetBackend := bindings.VerificationRunBackend{Store: store, Tasks: store, Canaries: store, AccountID: acct.ID, AppID: targetApp.ID, App: targetApp.Slug, Inventory: func(context.Context, string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
		return targetInventory, targetPolicy, nil
	}}
	graphBackend := bindings.ProjectVerificationRunBackend{Store: store, Catalog: store, Project: project, AccountID: acct.ID, Environment: "production", Member: func(_ context.Context, name, appID string) (bindings.VerificationRunBackend, error) {
		if name == app.Slug {
			return backend, nil
		}
		if name == targetApp.Slug {
			return targetBackend, nil
		}
		return bindings.VerificationRunBackend{}, state.ErrNotFound
	}}
	admitted := make(chan string, 1)
	loseResponse := true
	unconfirmedCompletion := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var result any
		var err error
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/projects/receipt-graph/environments/production/release-sets/verification-runs"):
			base := "/v1/projects/receipt-graph/environments/production/release-sets/verification-runs"
			switch {
			case r.URL.Path == base+"/review":
				var in api.ReviewProjectBindingVerificationRequest
				err = json.NewDecoder(r.Body).Decode(&in)
				if err == nil {
					result, err = graphBackend.Review(r.Context(), in)
				}

			case r.URL.Path == base:
				var in api.CreateProjectBindingVerificationRunRequest
				err = json.NewDecoder(r.Body).Decode(&in)
				if err == nil {
					result, err = graphBackend.Create(r.Context(), in)
				}
			case strings.HasSuffix(r.URL.Path, "/resume"):
				result, err = graphBackend.Resume(r.Context(), strings.Split(r.URL.Path, "/")[8])
			case strings.HasSuffix(r.URL.Path, "/complete"):
				result, err = graphBackend.Complete(r.Context(), strings.Split(r.URL.Path, "/")[8])
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/billing":
			result = api.AppResponse{ID: targetApp.ID, Slug: targetApp.Slug, Type: "function", Status: "active"}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/billing/bindings":
			copy := targetInventory
			copy.Scope = r.URL.Query().Get("scope")
			result = copy
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/billing/bindings/release-policy":
			result = targetPolicy
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/receipt/bindings/verification-runs/review":
			var in api.ReviewBindingVerificationRequest
			err = json.NewDecoder(r.Body).Decode(&in)
			if err == nil {
				result, err = backend.Review(r.Context(), in)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/receipt":
			result = api.AppResponse{ID: app.ID, Slug: app.Slug, Type: "function", Status: "active"}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/receipt/bindings":
			mu.Lock()
			copy := inventory
			mu.Unlock()
			copy.Scope = r.URL.Query().Get("scope")
			copy.RequestedDeploymentID = r.URL.Query().Get("deployment_id")
			result = copy
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/receipt/bindings/release-policy":
			result = policy
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/receipt/bindings/verification-runs":
			var in api.CreateBindingVerificationRunRequest
			err = json.NewDecoder(r.Body).Decode(&in)
			if err == nil {
				result, err = backend.Create(r.Context(), in)
			}
		case strings.HasSuffix(r.URL.Path, "/resume"):
			id := strings.Split(r.URL.Path, "/")[6]
			result, err = backend.Resume(r.Context(), id)
		case strings.HasSuffix(r.URL.Path, "/complete"):
			id := strings.Split(r.URL.Path, "/")[6]
			result, err = backend.Complete(r.Context(), id)
			mu.Lock()
			unconfirmed := unconfirmedCompletion
			mu.Unlock()
			if err == nil && unconfirmed {
				completed := result.(api.BindingVerificationRun)
				completed.Status = "blocked"
				result = completed
			}
		case strings.HasSuffix(r.URL.Path, "/steps/0/admit"):
			id := strings.Split(r.URL.Path, "/")[6]
			var step api.BindingVerificationRunStep
			step, err = backend.Admit(r.Context(), id, 0)
			result = step
			mu.Lock()
			hold := loseResponse
			loseResponse = false
			mu.Unlock()
			if err == nil && hold {
				admitted <- step.ResourceID
				<-r.Context().Done()
				return
			}
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/receipt/bindings/verification-runs/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/apps/receipt/bindings/verification-runs/")
			result, err = backend.Status(r.Context(), id)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/receipt/bindings/queue-canaries/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/apps/receipt/bindings/queue-canaries/")
			result, err = store.GetQueueBindingCanary(r.Context(), acct.ID, app.ID, id)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/receipt/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/apps/receipt/tasks/")
			var task state.AppTask
			task, err = store.AppTaskByID(r.Context(), acct.ID, app.ID, id)
			result = api.AppTaskResponse{ID: task.ID, AppID: task.AppID, DeploymentID: task.DeploymentID, DeploymentScope: task.DeploymentScope, Command: task.Command, Status: api.AppTaskStatus(task.Status), StdoutTail: task.StdoutTail, ExitCode: task.ExitCode}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if err != nil {
			t.Logf("fixture %s %s: %v", r.Method, r.URL.Path, err)
		}
		if problem := bindings.VerificationRunProblem(err); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if err = json.NewEncoder(w).Encode(result); err != nil && !strings.Contains(err.Error(), "broken pipe") {
			t.Error(err)
		}
	}))
	defer server.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	child := exec.Command(executable, "-test.run=^TestDurableVerificationCrashChild$")
	child.Env = append(os.Environ(), "GREGALE_VERIFICATION_TEST_URL="+server.URL, "GREGALE_VERIFICATION_TEST_RUN="+runID, "GREGALE_VERIFICATION_TEST_DEPLOYMENT="+dep.ID)
	if queue {
		child.Env = append(child.Env, "GREGALE_VERIFICATION_TEST_QUEUE=1")
	}
	if graph {
		raw, _ := json.Marshal(manifest)
		child.Env = append(child.Env, "GREGALE_VERIFICATION_TEST_GRAPH=1", "GREGALE_VERIFICATION_TEST_MANIFEST="+string(raw))
	}
	var childOutput bytes.Buffer
	child.Stdout, child.Stderr = io.Discard, io.Discard
	if graph {
		child.Stdout, child.Stderr = &childOutput, &childOutput
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill() }()
	var taskID string
	select {
	case taskID = <-admitted:
	case <-time.After(10 * time.Second):
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatalf("child never reached committed admission: %s", childOutput.String())
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("child was not killed")
	}
	memberRunID := runID
	if graph {
		parent, err := store.GetProjectBindingVerificationRun(ctx, acct.ID, project.ID, "production", runID)
		if err != nil {
			t.Fatal(err)
		}
		memberRunID = parent.Members[app.Slug].ID
	}
	recorded, err := store.GetBindingVerificationRun(ctx, acct.ID, app.ID, memberRunID)
	if err != nil || recorded.Steps[0].ResourceID != taskID {
		t.Fatalf("receipt lost on client crash: %+v %v", recorded, err)
	}
	if !queue {
		claimed, err := store.ClaimNextAppTask(ctx, "fixture", time.Now(), time.Minute)
		if err != nil || claimed.ID != taskID || claimed.LeaseToken == nil {
			t.Fatalf("claim: %+v %v", claimed, err)
		}
		if _, err = store.MarkAppTaskRunning(ctx, taskID, *claimed.LeaseToken, time.Now()); err != nil {
			t.Fatal(err)
		}
		stage := api.ServiceBindingProbeCheck{Status: "passed"}
		output, _ := json.Marshal(api.ServiceBindingProbeReport{Service: "billing", TargetDeploymentID: manifest.Deployments["billing"], DNS: stage, TLS: stage, Authorization: stage, Routing: stage})
		completed, exit := time.Now(), 0
		if _, err = store.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: taskID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, StdoutTail: string(output), ExitCode: &exit, FinishedAt: completed}); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		inventory.Bindings[0].VerificationStatus = "passed"
		inventory.Bindings[0].Verification = &api.BindingVerification{TaskID: taskID, TargetDeploymentID: manifest.Deployments["billing"], Result: "passed", Source: "task_guest", DeploymentID: dep.ID, Scope: dep.Scope, CheckedAt: &completed, Checks: []api.BindingVerificationCheck{{Name: "dns", Status: "passed"}, {Name: "tls", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "routing", Status: "passed"}}}
		mu.Unlock()
	}
	resumed := exec.CommandContext(ctx, executable, "-test.run=^TestDurableVerificationCrashChild$")
	resumed.Env = append(child.Env, "GREGALE_VERIFICATION_TEST_RESUME=1")
	outputBytes, err := resumed.CombinedOutput()
	if err != nil {
		t.Fatalf("resume child: %v %s", err, outputBytes)
	}
	if queue {
		final, err := backend.Status(ctx, memberRunID)
		if err != nil || final.Status != "pending" || final.Steps[0].ResourceID != taskID || !final.ExpiresAt.Equal(recorded.ExpiresAt) {
			t.Fatalf("pending queue receipt: %+v %v", final, err)
		}
		canary, err := store.GetQueueBindingCanary(ctx, acct.ID, app.ID, taskID)
		if err != nil || canary.Status != "published" {
			t.Fatalf("pending canary lost: %+v %v", canary, err)
		}
		due, err := store.QueuePeek(ctx, app.ID, 10, "")
		if err != nil || len(due) != 1 {
			t.Fatalf("duplicate queue messages: %d %v", len(due), err)
		}
	} else {
		tasks, err := store.ListAppTasks(ctx, acct.ID, app.ID, 10, 0)
		if err != nil || len(tasks) != 1 {
			t.Fatalf("duplicate task on resume: %d %v", len(tasks), err)
		}
		final, err := backend.Status(ctx, memberRunID)
		if err != nil || final.Status != "passed" || !final.ExpiresAt.Equal(recorded.ExpiresAt) {
			t.Fatalf("resume receipt: %+v %v", final, err)
		}
		if graph {
			parent, err := graphBackend.Status(ctx, runID)
			if err != nil || parent.Status != "passed" || !parent.ExpiresAt.Equal(recorded.ExpiresAt) || len(parent.Members) != 2 {
				t.Fatalf("resumed graph: %+v %v", parent, err)
			}
			return
		}
		mu.Lock()
		unconfirmedCompletion = true
		mu.Unlock()
		unconfirmed := exec.CommandContext(ctx, executable, "-test.run=^TestDurableVerificationCrashChild$")
		unconfirmed.Env = resumed.Env
		output, err := unconfirmed.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "server did not confirm a passed run") {
			t.Fatalf("unconfirmed completion reported success: %v %s", err, output)
		}
	}
}
