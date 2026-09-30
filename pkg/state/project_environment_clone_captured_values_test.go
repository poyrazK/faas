package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-375: deleting a source credential after capture cannot erase its required
// recreation, and captured managed envelopes must never become customer rows.
func TestMemCloneCapturedManagedCredentialRequiresIndependentPreparation(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
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
	source := state.AppSecret{AccountID: a.ID, AppID: app.ID, Scope: "production", Key: "DATABASE_URL", Ciphertext: []byte("sealed-source-credential"),
		ManagedPostgresBindingID: "source-binding", ManagedCredentialRef: "source-credential", ManagedCredentialGeneration: 3}
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
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteManagedPostgresSecret(ctx, source.ManagedCredentialRef); err != nil {
		t.Fatal(err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, "")
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
	target := source
	target.Scope, target.ManagedPostgresBindingID, target.ManagedCredentialRef = "stage", "target-binding", "target-credential"
	target.ManagedCredentialGeneration, target.Ciphertext = 1, []byte("sealed-independent-target")
	if err := s.PutManagedPostgresSecret(ctx, target); err != nil {
		t.Fatal(err)
	}
	clone.ManagedBindingsPrepared, clone.PreparedManagedBindingIDs, clone.PreparedManagedSecretCount = true, []string{target.ManagedPostgresBindingID}, 1
	_, result, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(a.Plan))
	if err != nil || result.SecretsCopied != 0 {
		t.Fatalf("managed envelope copied as a customer secret: count=%d, err=%v", result.SecretsCopied, err)
	}
	secrets, err := s.ListAppSecretsInScope(ctx, a.ID, app.ID, "stage")
	if err != nil || len(secrets) != 1 || secrets[0].ManagedPostgresBindingID != target.ManagedPostgresBindingID ||
		secrets[0].ManagedCredentialRef != target.ManagedCredentialRef || string(secrets[0].Ciphertext) != "sealed-independent-target" {
		t.Fatalf("captured credential replaced independently prepared target: count=%d, err=%v", len(secrets), err)
	}
}
