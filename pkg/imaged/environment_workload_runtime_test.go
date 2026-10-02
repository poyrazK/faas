package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentWorkloadFrozenManifestProjection(t *testing.T) {
	app := state.App{ID: "app", StartCommand: "./changed-after-review", Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeRequest}}
	frozen := state.EnvironmentWorkloadRuntime{SourceID: "source", EnvironmentID: "original-environment", RevisionID: "approved-revision",
		Generation: 2, PlanHash: strings.Repeat("a", 64), AppID: app.ID, AppType: state.AppTypeApp, Scope: "production", Resource: "workload/api", StartCommand: "./reviewed",
		Baseline: state.AppManifest{ExecutionMode: api.ExecutionModeService, StartupDeadlineS: 12}, Runtime: map[string]json.RawMessage{
			"port": json.RawMessage(`9090`), "ports": json.RawMessage(`[]`), "healthz": json.RawMessage(`"/ready"`),
			"stop_grace_period": json.RawMessage(`1500000000`), "service_replicas": json.RawMessage(`{"min":1,"max":2,"desired":2}`)}}
	raw, _ := json.Marshal(frozen)
	dep := state.Deployment{AppID: app.ID, Scope: "production", EnvironmentWorkloadRuntime: string(raw), OverridePort: 7070}
	resolved, err := state.AppForDeploymentRuntime(app, dep)
	if err != nil || resolved.StartCommand != "./reviewed" || resolved.Manifest.ExecutionMode != api.ExecutionModeService || resolved.Manifest.ServiceReplicas.Desired != 2 {
		t.Fatalf("frozen app projection: %+v %v", resolved, err)
	}
	manifest := api.AppManifest{Entrypoint: []string{"./image-command"}, Port: 8080, Ports: []api.WorkloadPort{{Name: "image", Port: 8080}}, Env: map[string]string{"IMAGE_VALUE": "keep"}}
	manifest = applyAppStartCommand(manifest, resolved)
	manifest, err = applyOverrides(manifest, dep)
	if err != nil {
		t.Fatal(err)
	}
	manifest = applyAppLifecycle(manifest, resolved)
	manifest, err = state.ApplyDeploymentRuntime(manifest, dep)
	if err != nil || manifest.Validate() != nil || manifest.Port != 9090 || manifest.Ports == nil || len(manifest.Ports) != 0 || manifest.Healthz != "/ready" || manifest.StopGracePeriod != 1500*time.Millisecond || manifest.ExecutionMode != api.ExecutionModeService || manifest.Env["IMAGE_VALUE"] != "keep" || manifest.Entrypoint[2] != "./reviewed" {
		t.Fatalf("effective frozen guest manifest: %+v %v", manifest, err)
	}
	resolved.Manifest.ServiceReplicas.Desired = 1
	resolvedAgain, err := state.AppForDeploymentRuntime(app, dep)
	if err != nil || resolvedAgain.Manifest.ServiceReplicas.Desired != 2 || app.StartCommand != "./changed-after-review" || app.Manifest.ExecutionMode != api.ExecutionModeRequest {
		t.Fatal("projection aliased immutable inputs or the shared app")
	}
	dep.Scope = "staging"
	if _, err := state.AppForDeploymentRuntime(app, dep); err == nil {
		t.Fatal("frozen inputs reused in a neighboring scope")
	}
}

func TestEnvironmentWorkloadDurableImageHandoffRejectsOrdinaryDeployment(t *testing.T) {
	fx := seedReleaseGateWithIntent(t, true, false)
	// The dedicated channel must never turn a normal deployment notification
	// into authority for environment graph preparation.
	payload, _ := json.Marshal(map[string]string{"app_id": fx.app.ID, "to": fx.candidate.ID, "kind": "image"})
	err := fx.handler.HandleNotification(t.Context(), db.Notification{Channel: db.NotifyEnvironmentWorkloadImage, Payload: string(payload)})
	if !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("ordinary row accepted on held candidate channel: %v", err)
	}
	if len(fx.notifier.calls) != 0 {
		t.Fatal("invalid candidate handoff emitted runtime work")
	}
}

func TestEnvironmentWorkloadExplicitEntrypointRescuesEmptyImageCommand(t *testing.T) {
	frozen := state.EnvironmentWorkloadRuntime{SourceID: "source", EnvironmentID: "environment", RevisionID: "revision", AppType: state.AppTypeApp,
		AppID: "app", Scope: "production", Generation: 1, PlanHash: strings.Repeat("a", 64), Runtime: map[string]json.RawMessage{"entrypoint": json.RawMessage(`["./reviewed"]`)}}
	raw, _ := json.Marshal(frozen)
	dep := state.Deployment{AppID: "app", Scope: "production", EnvironmentWorkloadRuntime: string(raw)}
	manifest, err := manifestFromImageConfigWithDeployment(oci.ImageConfig{}, state.App{}, dep)
	if err != nil || len(manifest.Entrypoint) != 1 || manifest.Entrypoint[0] != "./reviewed" {
		t.Fatalf("reviewed executable was not applied: %+v %v", manifest, err)
	}
}

func TestEnvironmentWorkloadHandoffHoldsEvenWithReleaseCommand(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.candidate.EnvironmentWorkloadRuntime = "invalid-but-held"
	if err := fx.handler.handoffSnapshotPrime(t.Context(), fx.app, fx.candidate); err != nil {
		t.Fatal(err)
	}
	if len(fx.notifier.calls) != 0 {
		t.Fatal("held graph notified a runtime or release task")
	}
	tasks, err := fx.store.ListAppTasks(t.Context(), fx.app.AccountID, fx.app.ID, 10, 0)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("held graph admitted a release process: %+v %v", tasks, err)
	}
}

type heldEnvironmentArtifactStore struct {
	state.Store
	deploymentID string
}

func (s heldEnvironmentArtifactStore) ListDeploymentsForOperator(ctx context.Context, filter state.OperatorDeploymentFilter) ([]state.Deployment, error) {
	rows, err := s.Store.ListDeploymentsForOperator(ctx, filter)
	for i := range rows {
		if rows[i].ID == s.deploymentID {
			rows[i].EnvironmentWorkloadRuntime = "held"
		}
	}
	return rows, err
}
func TestEnvironmentWorkloadStagedArtifactSurvivesStaleSweep(t *testing.T) {
	fx := seedReleaseGate(t, true)
	loop := NewLoop(LoopConfig{Log: silentLogger()})
	loop.store = heldEnvironmentArtifactStore{Store: fx.store, deploymentID: fx.candidate.ID}
	loop.handler = fx.handler
	loop.now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	loop.reconcileStaleDeployments(t.Context())
	current, err := fx.store.DeploymentByID(t.Context(), fx.candidate.ID)
	if err != nil || current.Status != state.DeploySnapshotting || current.RootfsKey != fx.candidate.RootfsKey {
		t.Fatalf("staged artifact was cancelled while waiting for its graph: %+v %v", current, err)
	}
}

func TestEnvironmentWorkloadReviewedIntentReachesHeldImagingArtifact(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "image-preparation@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "held-api", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 1, StartCommand: "./reviewed", Manifest: state.AppManifest{Port: 8079}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	image := "registry.example/held@sha256:" + strings.Repeat("d", 64)
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
			Runtime: json.RawMessage(`{"port":9090,"stop_grace_period":1500000000}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID,
		ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := store.PreviewEnvironmentGitOpsAdoption(ctx, account.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, account.ID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(ctx, "image-preparer", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := func() environmentsync.Plan {
		observed, err := store.ObserveEnvironmentGitOps(ctx, lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		result, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
			Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if _, err := store.ApplyEnvironmentGitOps(ctx, lease, plan()); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.PrepareEnvironmentGitOpsImageCandidates(ctx, lease, plan())
	if err != nil || len(candidates) != 1 {
		t.Fatalf("prepare: %+v %v", candidates, err)
	}
	command := "./changed-after-preparation"
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{StartCommand: &command}); err != nil {
		t.Fatal(err)
	}
	notifier, builder := &fakeNotifier{}, &fakeBuilder{bytesOut: 4096}
	handler := New(store, notifier, fakePuller{digest: image, cfg: oci.ImageConfig{Cmd: []string{"./image"}}}, builder, "./init", t.TempDir(), silentLogger())
	payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "to": candidates[0].DeploymentID, "kind": "image"})
	notification := db.Notification{Channel: db.NotifyEnvironmentWorkloadImage, Payload: string(payload)}
	for attempt := 0; attempt < 2; attempt++ {
		if err := handler.HandleNotification(ctx, notification); err != nil {
			t.Fatal(err)
		}
	}
	if len(builder.calls) != 1 || builder.calls[0].Manifest.Port != 9090 || builder.calls[0].Manifest.StopGracePeriod != 1500*time.Millisecond || builder.calls[0].Manifest.Entrypoint[2] != "./reviewed" {
		t.Fatalf("imaging did not consume frozen inputs exactly once: %+v", builder.calls)
	}
	dep, err := store.DeploymentByID(ctx, candidates[0].DeploymentID)
	if err != nil || dep.Status != state.DeploySnapshotting || !dep.EnvironmentWorkloadHeld() || dep.RootfsPath == "" || dep.RootfsBytes != 4096 {
		t.Fatalf("durable artifact missing: %+v %v", dep, err)
	}
	if findNotify(notifier, db.NotifySnapshotPrime) != nil {
		t.Fatal("imaging released the unqualified graph")
	}
	if err := handler.handleDeploymentActivation(ctx, snapshotWrittenPayload{DeploymentID: dep.ID}, nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unqualified artifact accepted for activation: %v", err)
	}
	if _, err := store.LatestSnapshot(ctx, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("artifact assembly invented a qualified snapshot: %v", err)
	}
}
