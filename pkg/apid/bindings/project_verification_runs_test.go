package bindings

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func graphBackendFixture(t *testing.T) (ProjectVerificationRunBackend, api.CreateProjectBindingVerificationRunRequest, map[string]*api.AppBindingInventory) {
	t.Helper()
	ctx := t.Context()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "graph-backend@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "graph-backend"})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	manifest := api.CheckedProjectReleaseManifest{Project: project.Slug, Environment: "production", TTLSeconds: 60, Deployments: map[string]string{}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{}, ServiceTargets: map[string]map[string]string{}}
	members := map[string]VerificationRunBackend{}
	inventories := map[string]*api.AppBindingInventory{}
	digests := map[string]string{}
	for _, name := range []string{"graph-first", "graph-last"} {
		app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: name, WorkloadName: name, Type: state.AppTypeFunction, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600, ServiceBindings: []api.AppServiceBinding{{Service: "external", Binding: "EXTERNAL"}}}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:graph"})
		if err != nil {
			t.Fatal(err)
		}
		if err = m.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "rootfs", 1); err != nil {
			t.Fatal(err)
		}
		if err = m.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if err = m.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
			t.Fatal(err)
		}
		stamp, _, err := m.AppRuntimeConfigChangedAt(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := m.SetBindingReleasePolicy(ctx, acct.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{ExpectedRevision: new(int64), Mode: "enforce", MaxVerificationAge: "1s"})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		inventory := &api.AppBindingInventory{App: name, Scope: "production", Complete: true, RequestedDeploymentID: dep.ID, VerificationDeploymentID: dep.ID, VerificationScope: "production", RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ConfigChangedAt: &stamp, ObservedAt: now, Deployments: []api.BindingRuntimeDeployment{{DeploymentID: dep.ID, Scope: "production", DeploymentStatus: "live", Status: "current", Resident: api.BindingRuntimeInstanceCounts{Current: 1}, Serving: api.BindingRuntimeInstanceCounts{Current: 1}}}}, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "external", Binding: "EXTERNAL", Scope: "app", Access: "invoke", State: "declared", VerificationStatus: "passed", Verification: &api.BindingVerification{DeploymentID: dep.ID, Scope: "production", Source: "task_guest", Result: "passed", CheckedAt: &now, Checks: []api.BindingVerificationCheck{{Name: "dns", Status: "passed"}, {Name: "tls", Status: "passed"}, {Name: "authorization", Status: "passed"}, {Name: "routing", Status: "passed"}}}}}}
		inventories[name] = inventory
		members[name] = VerificationRunBackend{Store: m, Tasks: m, Canaries: m, AccountID: acct.ID, AppID: app.ID, App: name, Inventory: func(context.Context, string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
			return *inventory, policy, nil
		}, CaptureTask: func(_ context.Context, _ string, step api.BindingVerificationPlanStep, at time.Time) (*state.CreateAppTaskParams, error) {
			return &state.CreateAppTaskParams{AccountID: acct.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, RequireLiveDeployment: true, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: step.Name, Revision: strings.Repeat("a", 64)}, Command: []string{api.AppTaskServiceBindingProbeCommand, step.Name}, TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, CreatedAt: at}, nil
		}}
		parsed, _ := uuid.Parse(dep.ID)
		manifest.Deployments[name] = parsed.String()
		manifest.ExpectedPolicyRevisions[name] = policy.Revision
		manifest.ServiceTargets[name] = map[string]string{}
		deps, err := m.LiveDeployments(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range deps {
			if d.TrafficPercent > 0 {
				id, _ := uuid.Parse(d.ID)
				manifest.ExpectedFallbackDeployments[name] = id.String()
			}
		}
		digests[name] = bindingcheck.VerificationReviewDigest(*inventory, policy)
	}
	backend := ProjectVerificationRunBackend{Store: m, Catalog: m, Project: project, AccountID: acct.ID, Environment: "production", Member: func(_ context.Context, name, id string) (VerificationRunBackend, error) {
		b, ok := members[name]
		if !ok || b.AppID != id {
			return b, state.ErrNotFound
		}
		return b, nil
	}}
	request := api.CreateProjectBindingVerificationRunRequest{ID: uuid.NewString(), Manifest: manifest, ReviewDigests: digests, TTLSeconds: 60}
	return backend, request, inventories
}
func TestGraphVerificationBackendFreshCompletionAndExpiredEvidence(t *testing.T) {
	backend, in, inventories := graphBackendFixture(t)
	ctx := t.Context()
	run, err := backend.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range run.Members {
		if len(child.Steps) != 1 || child.Steps[0].Plan.Action != "reuse" || child.Steps[0].Owned {
			t.Fatalf("reuse review changed: %+v", child)
		}
	}
	completed, err := backend.Complete(ctx, run.ID)
	if err != nil || completed.Status != "passed" {
		t.Fatalf("fresh graph completion: %+v %v", completed, err)
	}
	expired := time.Now().Add(-2 * time.Second)
	inventories["graph-last"].Bindings[0].Verification.CheckedAt = &expired
	status, err := backend.Status(ctx, run.ID)
	if err != nil || status.Status != "blocked" {
		t.Fatalf("aged graph still passed: %+v %v", status, err)
	}
	if _, err = backend.Resume(ctx, run.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired proof resumed: %v", err)
	}
	if _, err = backend.Complete(ctx, run.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired proof completed: %v", err)
	}
	cancelled, err := backend.Cancel(ctx, run.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel after expiry: %+v %v", cancelled, err)
	}
}
func TestGraphVerificationBackendChangedReviewCannotReplay(t *testing.T) {
	backend, in, _ := graphBackendFixture(t)
	ctx := t.Context()
	run, err := backend.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ReviewDigests["graph-last"] = strings.Repeat("b", 64)
	if _, err = backend.Create(ctx, in); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed review reused run: %v", err)
	}
	current, err := backend.Read(ctx, run.ID)
	if err != nil || !current.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("deadline changed: %+v %v", current, err)
	}
}

func TestGraphVerificationBackendBatchedPrivatePinReview(t *testing.T) {
	backend, in, _ := graphBackendFixture(t)
	ctx := t.Context()
	run, err := backend.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	original := backend.Member
	backend.Member = func(ctx context.Context, name, id string) (VerificationRunBackend, error) {
		member, err := original(ctx, name, id)
		if err != nil || name != "graph-last" {
			return member, err
		}
		capture := member.CaptureTask
		member.CaptureTask = func(ctx context.Context, dep string, step api.BindingVerificationPlanStep, at time.Time) (*state.CreateAppTaskParams, error) {
			params, err := capture(ctx, dep, step, at)
			if err == nil {
				params.BindingVerification.Revision = strings.Repeat("b", 64)
			}
			return params, err
		}
		return member, nil
	}
	if _, err = backend.Resume(ctx, run.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed private pin passed batched review: %v", err)
	}
}
