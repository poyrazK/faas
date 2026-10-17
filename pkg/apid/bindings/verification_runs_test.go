package bindings

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func verificationBackendFixture(t *testing.T) (VerificationRunBackend, *state.MemStore, *api.AppBindingInventory, *api.BindingReleasePolicy) {
	t.Helper()
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "receipt@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "receipt", Type: state.AppTypeFunction, Manifest: state.AppManifest{ServiceBindings: []api.AppServiceBinding{{Service: "billing", Binding: "BILLING"}}}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:receipt"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	policy, err := m.SetBindingReleasePolicy(ctx, account.ID, app.ID, dep.Scope, api.SetBindingReleasePolicyRequest{ExpectedRevision: new(int64), Mode: "enforce", MaxVerificationAge: "5m"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	stamp, _, err := m.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	inventory := api.AppBindingInventory{App: app.Slug, Scope: policy.Scope, Complete: true, GeneratedAt: now, RequestedDeploymentID: dep.ID, VerificationDeploymentID: dep.ID, VerificationScope: policy.Scope, RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: now, ConfigChangedAt: &stamp, Deployments: []api.BindingRuntimeDeployment{{DeploymentID: dep.ID, Scope: dep.Scope, DeploymentStatus: "live", Status: "current", Serving: api.BindingRuntimeInstanceCounts{Current: 1}, Resident: api.BindingRuntimeInstanceCounts{Current: 1}}}}, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING", Scope: "app", Access: "invoke", State: "declared", VerificationStatus: "unknown"}}}
	backend := VerificationRunBackend{Store: m, Tasks: m, Canaries: m, AccountID: account.ID, AppID: app.ID, App: app.Slug, Inventory: func(context.Context, string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
		return inventory, policy, nil
	}, CaptureTask: func(_ context.Context, _ string, step api.BindingVerificationPlanStep, at time.Time) (*state.CreateAppTaskParams, error) {
		return &state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, RequireLiveDeployment: true, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: step.Name, Revision: strings.Repeat("a", 64)}, Command: []string{api.AppTaskServiceBindingProbeCommand, step.Name}, TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, CreatedAt: at}, nil
	}}
	return backend, m, &inventory, &policy
}
func newReceiptRequest(i api.AppBindingInventory, p api.BindingReleasePolicy) api.CreateBindingVerificationRunRequest {
	return api.CreateBindingVerificationRunRequest{ID: uuid.NewString(), DeploymentID: i.VerificationDeploymentID, ReviewDigest: bindingcheck.VerificationReviewDigest(i, p), TTLSeconds: 60}
}
func TestVerificationRunBackendLostResponseAndCancellationOwnership(t *testing.T) {
	b, m, i, p := verificationBackendFixture(t)
	ctx := context.Background()
	request := newReceiptRequest(*i, *p)
	run, err := b.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	first, err := b.Admit(ctx, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	} // Lose this response.
	resumed, err := b.Resume(ctx, run.ID)
	if err != nil || resumed.Steps[0].ResourceID != first.ResourceID {
		t.Fatalf("resume: %+v %v", resumed, err)
	}
	again, err := b.Admit(ctx, run.ID, 0)
	if err != nil || again.ResourceID != first.ResourceID {
		t.Fatalf("re-admission: %+v %v", again, err)
	}
	if _, err = b.Complete(ctx, run.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("queued work qualified: %v", err)
	}
	// A second run observes the first run's pending task without owning it.
	i.Bindings[0].Verification = &api.BindingVerification{TaskID: first.ResourceID, Source: "task_guest", Result: "unknown", Reason: "probe_pending", DeploymentID: run.DeploymentID, Scope: run.Scope}
	i.Bindings[0].VerificationStatus = "unknown"
	observed, err := b.Create(ctx, newReceiptRequest(*i, *p))
	if err != nil {
		t.Fatal(err)
	}
	if observed.Steps[0].Owned || observed.Steps[0].ResourceID != first.ResourceID {
		t.Fatalf("reused ownership: %+v", observed)
	}
	if _, err = b.Cancel(ctx, observed.ID); err != nil {
		t.Fatal(err)
	}
	task, err := m.AppTaskByID(ctx, b.AccountID, b.AppID, first.ResourceID)
	if err != nil || task.Status != state.AppTaskQueued {
		t.Fatalf("cancelled foreign pending task: %+v %v", task, err)
	}
	if _, err = b.Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	task, err = m.AppTaskByID(ctx, b.AccountID, b.AppID, first.ResourceID)
	if err != nil || task.Status != state.AppTaskCancelled {
		t.Fatalf("owned task not cancelled: %+v %v", task, err)
	}
}
func TestVerificationRunBackendCompletionAndDrift(t *testing.T) {
	b, m, i, p := verificationBackendFixture(t)
	ctx := context.Background()
	run, err := b.Create(ctx, newReceiptRequest(*i, *p))
	if err != nil {
		t.Fatal(err)
	}
	step, err := b.Admit(ctx, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := m.ClaimNextAppTask(ctx, "fixture", time.Now(), time.Minute)
	if err != nil || claimed.ID != step.ResourceID || claimed.LeaseToken == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, err = m.MarkAppTaskRunning(ctx, claimed.ID, *claimed.LeaseToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	stage := api.ServiceBindingProbeCheck{Status: "passed"}
	output, _ := json.Marshal(api.ServiceBindingProbeReport{Service: "billing", DNS: stage, TLS: stage, Authorization: stage, Routing: stage})
	completed := time.Now()
	exit := 0
	if _, err = m.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: claimed.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, StdoutTail: string(output), ExitCode: &exit, FinishedAt: completed}); err != nil {
		t.Fatal(err)
	}
	i.Bindings[0].VerificationStatus = "passed"
	i.Bindings[0].Verification = &api.BindingVerification{TaskID: claimed.ID, Result: "passed", Source: "task_guest", DeploymentID: run.DeploymentID, Scope: run.Scope, CheckedAt: &completed, Checks: []api.BindingVerificationCheck{{Name: "dns", Status: "passed"}, {Name: "tls", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "routing", Status: "passed"}}}
	passed, err := b.Complete(ctx, run.ID)
	if err != nil || passed.Status != "passed" {
		t.Fatalf("completion: %+v %v", passed, err)
	}
	p.Revision++
	status, err := b.Status(ctx, run.ID)
	if err != nil || status.Status != "blocked" {
		t.Fatalf("policy drift: %+v %v", status, err)
	}
	if _, err = b.Resume(ctx, run.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("drift resumed: %v", err)
	}
}
func TestVerificationRunBackendConsentBeforeAdmission(t *testing.T) {
	b, m, i, p := verificationBackendFixture(t)
	scope := p.Scope
	observed := time.Now()
	p.QueueRequirements = map[string]string{"orders": api.QueueEvidenceHandlerCompletion}
	i.Bindings = append(i.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: scope, QueueEnvironment: &scope, Access: "push", State: "enabled", ConsumerState: "active", ConsumerLiveness: "healthy", ObservedAt: &observed, QueueCanarySupported: true})
	request := newReceiptRequest(*i, *p)
	if _, err := b.Create(context.Background(), request); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("missing consent: %v", err)
	}
	if _, err := m.GetBindingVerificationRun(context.Background(), b.AccountID, b.AppID, request.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("consent persisted receipt: %v", err)
	}
	tasks, err := m.ListAppTasks(context.Background(), b.AccountID, b.AppID, 10, 0)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("consent admitted work: %d %v", len(tasks), err)
	}
}

func TestVerificationRunBackendExpiredEvidenceDoesNotResume(t *testing.T) {
	b, _, i, p := verificationBackendFixture(t)
	past := time.Now().Add(-time.Minute)
	p.MaxVerificationAge = "1s"
	i.Bindings[0].VerificationStatus = "passed"
	i.Bindings[0].Verification = &api.BindingVerification{Result: "passed", Source: "task_guest", DeploymentID: i.VerificationDeploymentID, Scope: p.Scope, CheckedAt: &past, Checks: []api.BindingVerificationCheck{{Name: "dns", Status: "passed"}, {Name: "tls", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "routing", Status: "passed"}}}
	// Mint with a fresh observed result, then age it out without changing config.
	fresh := time.Now()
	i.Bindings[0].Verification.CheckedAt = &fresh
	run, err := b.Create(context.Background(), newReceiptRequest(*i, *p))
	if err != nil {
		t.Fatal(err)
	}
	if run.Steps[0].Plan.Action != "reuse" {
		t.Fatalf("fixture was not fresh reused evidence: %+v", run.Steps[0])
	}
	i.Bindings[0].Verification.CheckedAt = &past
	if _, err = b.Resume(context.Background(), run.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired evidence resumed: %v", err)
	}
}
