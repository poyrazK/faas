//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-585: deleting a source credential after capture cannot erase its required
// recreation, and captured managed envelopes must never become customer rows.
func TestPgCloneCapturedManagedCredentialRequiresIndependentPreparation(t *testing.T) {
	ctx := context.Background()
	s, _, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "captured-managed@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "managed"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "managed-api", WorkloadName: "api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:captured"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/source.ext4", "layers/source.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	source := clonePostgresSecretFixture(t, pool, a, app, "production", "source-db", 3)
	if err := s.PutManagedPostgresSecret(ctx, source); err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: a.ID, ProjectID: p.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "managed", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || len(views) != 1 {
		t.Fatal(err)
	}
	if err := s.DeleteManagedPostgresSecret(ctx, source.ManagedCredentialRef); err != nil {
		t.Fatal(err)
	}
	view := views[0]
	resources := []state.ProjectEnvironmentCloneResource{
		{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"},
		{Kind: "project_config", Name: "production", SourceVersion: view.SourceProjectConfigHash, Status: "ready"},
		{Kind: "workload", Name: view.WorkloadSlug, SourceID: view.SourceDeploymentID, SourceVersion: view.SourceHash, Status: "captured"},
	}
	for _, kind := range []string{"variables", "secrets"} {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: kind, Name: view.WorkloadSlug, SourceID: app.ID, TargetID: app.ID, SourceVersion: view.SourceValuesHash, Status: "ready"})
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	clone := state.ProjectEnvironmentClone{AccountID: a.ID, ProjectID: p.ID, SourceSlug: "production", TargetSlug: "stage", CloneOperationID: op.ID, CloneOperationRevision: op.Revision}
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(a.Plan)); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedBindings) {
		t.Fatalf("deleted source credential bypassed captured binding requirement: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unprepared managed resource materialized the target: %v", err)
	}
	target := clonePostgresSecretFixture(t, pool, a, app, "stage", "target-db", 1)
	target.Ciphertext = []byte("sealed-independent-target")
	if err := s.PutManagedPostgresSecret(ctx, target); err != nil {
		t.Fatal(err)
	}
	clone.ManagedBindingsPrepared, clone.PreparedManagedBindingIDs, clone.PreparedManagedSecretCount = true, []string{target.ManagedPostgresBindingID}, 1
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(a.Plan)); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedValueProof) {
		t.Fatalf("unowned target and matching secret count passed materialization proof: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("forged preparation materialized the target: %v", err)
	}
	secrets, err := s.ListAppSecretsInScope(ctx, a.ID, app.ID, "stage")
	if err != nil || len(secrets) != 1 || secrets[0].ManagedPostgresBindingID != target.ManagedPostgresBindingID ||
		secrets[0].ManagedCredentialRef != target.ManagedCredentialRef || string(secrets[0].Ciphertext) != "sealed-independent-target" {
		t.Fatalf("captured credential replaced independently prepared target: count=%d, err=%v", len(secrets), err)
	}
	if _, err := s.ActiveProjectReleaseSet(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unverified managed stage acquired a serving graph: %v", err)
	}
}
