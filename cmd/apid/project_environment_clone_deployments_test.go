// adr: 581
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type clonePrimeNotifier struct {
	capturingNotifier
	fail bool
}

func (n *clonePrimeNotifier) Notify(ctx context.Context, channel, payload string) error {
	if n.fail {
		n.fail = false
		return errors.New("outbox unavailable")
	}
	return n.capturingNotifier.Notify(ctx, channel, payload)
}

func TestProjectEnvironmentCloneDeploymentPreparationResumesAfterHandoffFailure(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	source, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:captured"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/captured.ext4", "layers/captured.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "prepare", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	views, err := store.CaptureProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, op.ID, op.Revision)
	if err != nil || len(views) != 1 {
		t.Fatalf("capture = %+v, %v", views, err)
	}
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"},
		{Kind: "workload", Name: app.Slug, SourceID: source.ID, SourceVersion: views[0].SourceHash, Status: "captured"},
		{Kind: "project_config", Name: "production", SourceVersion: views[0].SourceProjectConfigHash, Status: "ready"}}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: acct.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "stage",
		CloneOperationID: op.ID, CloneOperationRevision: op.Revision}, api.MustLimitsFor(acct.Plan)); err != nil {
		t.Fatal(err)
	}
	notifier := &clonePrimeNotifier{fail: true}
	srv.notif = notifier
	if _, _, err := srv.prepareProjectEnvironmentCloneDeployments(ctx, op); err == nil {
		t.Fatal("missing outbox failure")
	}
	checkpoint, err := store.ProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, op.ID)
	if err != nil || len(checkpoint) != 1 || checkpoint[0].TargetDeploymentID == "" {
		t.Fatalf("lost prepared target = %+v, %v", checkpoint, err)
	}
	// A source artifact write after the capture cannot replace the retry input.
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/newer.ext4", "layers/newer.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	updated, ready, err := srv.prepareProjectEnvironmentCloneDeployments(ctx, op)
	if err != nil || ready || updated[1].TargetID != checkpoint[0].TargetDeploymentID || updated[1].Status != "verifying" {
		t.Fatalf("retry = %+v, %v, %v", updated, ready, err)
	}
	if len(notifier.emitted) != 1 || notifier.emitted[0].Channel != db.NotifySnapshotPrime || !strings.Contains(notifier.emitted[0].Payload, checkpoint[0].TargetDeploymentID) {
		t.Fatalf("wrong handoff: %+v", notifier.emitted)
	}
	target, err := store.DeploymentByID(ctx, checkpoint[0].TargetDeploymentID)
	if err != nil || target.RootfsKey != "layers/captured.ext4" || target.Scope != "stage" || target.TrafficPercent != 0 {
		t.Fatalf("target = %+v, %v", target, err)
	}
	deployments, err := store.ListDeploymentsForApp(ctx, app.ID, 0, 0)
	if err != nil || len(deployments) != 2 {
		t.Fatalf("retry created duplicate deployment: %+v, %v", deployments, err)
	}
	if err := store.MarkDeploymentLiveDark(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	updated, ready, err = srv.prepareProjectEnvironmentCloneDeployments(ctx, op)
	if err != nil || !ready || updated[1].Status != "ready" || len(notifier.emitted) != 1 {
		t.Fatalf("ready retry = %+v, %v, %v", updated, ready, err)
	}
	bad := op
	bad.Resources = append([]state.ProjectEnvironmentCloneResource(nil), op.Resources...)
	bad.Resources[1].SourceVersion = strings.Repeat("b", 64)
	if _, _, err := srv.prepareProjectEnvironmentCloneDeployments(ctx, bad); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed capture accepted: %v", err)
	}
}
