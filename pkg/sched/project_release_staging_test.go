// adr: 1351 — exact candidate service allocation, expiry and routing preservation.
package sched_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type releaseStagingFixture struct {
	store              state.Store
	engine             *sched.Engine
	vmm                *sched.FakeVMM
	account            state.Account
	project            state.Project
	app                state.App
	serving, candidate state.Deployment
	manifest           api.CheckedProjectReleaseManifest
}

func newReleaseStagingFixture(t *testing.T, store state.Store, max int) releaseStagingFixture {
	t.Helper()
	ctx := t.Context()
	f := releaseStagingFixture{store: store, vmm: &sched.FakeVMM{}}
	var err error
	f.account, err = store.CreateAccount(ctx, uuid.NewString()+"@stage.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "stage-services"})
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage-api", WorkloadName: "stage-api", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: max, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, RevisionPinTTLSeconds: 3600, ServiceReplicas: &state.ServiceReplicas{Min: 1, Desired: 2, Max: 2}}})
	if err != nil {
		t.Fatalf("create service app: %v", err)
	}
	f.serving, err = store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, f.serving.ID, "/test/rootfs", "stage/"+f.serving.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, f.serving.ID); err != nil {
		t.Fatalf("promote serving deployment: %v", err)
	}
	f.engine = sched.XNewEngine(t, store, f.vmm, &sched.FakeNotifier{}, "1.10.0")
	(*f.engine.ServiceReconcileSubmit()) = func(context.Context, string) {}
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, store, f.app.ID, f.serving.ID, 2)
	// Complete the serving lifecycle before introducing the dark candidate.
	f.candidate, err = store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:" + uuid.NewString(), TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, f.candidate.ID, "/test/rootfs", "stage/"+f.candidate.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, f.candidate.ID); err != nil {
		t.Fatalf("promote dark candidate: %v", err)
	}
	zero := int64(0)
	policy, err := store.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, f.account.ID, f.app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	f.manifest = api.CheckedProjectReleaseManifest{Project: f.project.Slug, Environment: "production", TTLSeconds: 1800, Deployments: map[string]string{f.app.Slug: uuid.MustParse(f.candidate.ID).String()}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{f.app.Slug: uuid.MustParse(f.serving.ID).String()}, ExpectedPolicyRevisions: map[string]int64{f.app.Slug: policy.Revision}, ServiceTargets: map[string]map[string]string{f.app.Slug: {}}}
	sched.AssertRecoveryReplicas(t, store, f.app.ID, f.candidate.ID, 0)
	return f
}
func (f releaseStagingFixture) stage(ctx context.Context, t *testing.T, ttl int) api.ProjectReleaseStage {
	t.Helper()
	out, err := f.store.(state.ProjectReleaseStagingStore).StageProjectRelease(ctx, f.account.ID, f.project.ID, api.StageProjectReleaseRequest{Manifest: f.manifest, TTLSeconds: ttl})
	if err != nil {
		t.Fatalf("create stage intent: %v", err)
	}
	return out
}
func TestProjectReleaseStagingSchedulerCancellation(t *testing.T) {
	exerciseReleaseStagingCancellation(t, state.NewMemStore())
}
func exerciseReleaseStagingCancellation(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	f := newReleaseStagingFixture(t, store, 5)
	stage := f.stage(ctx, t, 1800)
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, store, f.app.ID, f.serving.ID, 2)
	sched.AssertRecoveryReplicas(t, store, f.app.ID, f.candidate.ID, 2)
	view, err := store.(state.ProjectReleaseStagingStore).ProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID)
	if err != nil || view.Status != "ready" {
		t.Fatalf("staging never became ready: %+v %v", view, err)
	}
	if _, err := store.(state.ProjectReleaseStagingStore).CancelProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID); err != nil {
		t.Fatal(err)
	}
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, store, f.app.ID, f.serving.ID, 2)
	sched.AssertRecoveryReplicas(t, store, f.app.ID, f.candidate.ID, 0)
	serving, _ := store.DeploymentByID(ctx, f.serving.ID)
	candidate, _ := store.DeploymentByID(ctx, f.candidate.ID)
	if serving.TrafficPercent != 100 || candidate.TrafficPercent != 0 {
		t.Fatalf("staging changed routes: %+v %+v", serving, candidate)
	}
}
func TestProjectReleaseStagingSchedulerCapacity(t *testing.T) {
	ctx := t.Context()
	f := newReleaseStagingFixture(t, state.NewMemStore(), 2)
	stage := f.stage(ctx, t, 1800)
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.serving.ID, 2)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.candidate.ID, 0)
	view, err := f.store.(state.ProjectReleaseStagingStore).ProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID)
	if err != nil || view.Status != "blocked" || view.Reason != "capacity" {
		t.Fatalf("capacity failure concealed: %+v %v", view, err)
	}
	if (*f.engine.XLedger()).Concurrency(f.app.ID) > 2 {
		t.Fatal("staging bypassed concurrency")
	}
}
func TestProjectReleaseStagingSchedulerExpiry(t *testing.T) {
	ctx := t.Context()
	f := newReleaseStagingFixture(t, state.NewMemStore(), 5)
	stage := f.stage(ctx, t, 1)
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.candidate.ID, 2)
	select {
	case <-time.After(time.Until(stage.ExpiresAt) + 10*time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.serving.ID, 2)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.candidate.ID, 0)
	view, err := f.store.(state.ProjectReleaseStagingStore).ProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID)
	if err != nil || view.Status != "expired" {
		t.Fatalf("expiry: %+v %v", view, err)
	}
}
func TestProjectReleaseStagingCancelDuringBoot(t *testing.T) {
	ctx := t.Context()
	f := newReleaseStagingFixture(t, state.NewMemStore(), 5)
	stage := f.stage(ctx, t, 1800)
	(*f.vmm.ColdBootHook()) = func() {
		if _, err := f.store.(state.ProjectReleaseStagingStore).CancelProjectReleaseStage(ctx, f.account.ID, f.project.ID, "production", stage.ID); err != nil {
			t.Fatal(err)
		}
	}
	before := (*f.vmm.ColdBoots())
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	if (*f.vmm.ColdBoots())-before != 1 {
		t.Fatalf("cancelled lease authorized more boots: %d", (*f.vmm.ColdBoots())-before)
	}
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.serving.ID, 2)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.candidate.ID, 0)
}

func TestProjectReleaseStagingPrioritizesServingReplacement(t *testing.T) {
	ctx := t.Context()
	f := newReleaseStagingFixture(t, state.NewMemStore(), 2)
	_ = f.stage(ctx, t, 1800)
	rows, err := sched.ListServiceReplicas(ctx, f.store, f.app.ID, f.serving.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.engine.Transition(ctx, rows[0].ID, f.app.ID, state.StateStopped)
	(*f.engine.XLedger()).Release(rows[0].ID)
	f.engine.ReconcileServiceApp(ctx, f.app.ID)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.serving.ID, 2)
	sched.AssertRecoveryReplicas(t, f.store, f.app.ID, f.candidate.ID, 0)
}
