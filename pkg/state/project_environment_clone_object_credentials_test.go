// adr: 531
package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneCredentialPreparationTestStore interface {
	cloneBucketReservationTestStore
	state.ProjectEnvironmentCloneObjectCredentialStore
	state.ProjectEnvironmentCloneLeasedObjectManifestStore
	PutManagedObjectStorageSecret(context.Context, state.AppSecret) error
	DeleteManagedObjectStorageSecrets(context.Context, string) error
	GetAppSecretInScope(context.Context, string, string, string, string) (*state.AppSecret, error)
	LiveDeploymentForScope(context.Context, string, string) (state.Deployment, error)
	CreateSnapshot(context.Context, state.Snapshot) (state.Snapshot, error)
	LatestSnapshot(context.Context, string) (state.Snapshot, error)
	AppRuntimeConfigChangedAt(context.Context, string) (time.Time, bool, error)
}

func TestMemCloneObjectCredentialPreparationIsAtomicAndRecoverable(t *testing.T) {
	cloneObjectCredentialPreparationContract(t, state.NewMemStore())
}

func cloneCredentialSecrets(accountID, appID, scope, credentialID, prefix, marker string) []state.AppSecret {
	secrets := []state.AppSecret{}
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		hash := sha256.Sum256([]byte(marker + suffix))
		secrets = append(secrets, state.AppSecret{AccountID: accountID, AppID: appID, Scope: scope, Key: prefix + suffix,
			Ciphertext: []byte(marker + suffix), Kid: marker + "-kid", ValueHash: hex.EncodeToString(hash[:8]), ManagedObjectStorageCredentialID: credentialID})
	}
	return secrets
}

func cloneObjectCredentialPreparationContract(t *testing.T, s cloneCredentialPreparationTestStore, beforeMaterialization ...func(state.ProjectEnvironmentClone, state.ObjectS3ComputeBindingCreateRequest)) {
	t.Helper()
	ctx := context.Background()
	account, project, app, op := cloneBindingFixture(t, s)
	source := cloneBindingBucket(t, s, account, app, "assets", "production", true)
	managedID := uuid.NewString()
	managed, err := s.CreateObjectS3ComputeBinding(ctx, state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{ID: managedID, AccountID: account.ID,
		BucketID: source.ID, AccessKeyID: "GRGAAAAAAAAAAAAAAAAA", SecretSealed: []byte("source-signing-key"), KID: "source-kid", Label: "compute", Permission: "read_write", Status: "active",
		ManagedAppID: app.ID, ManagedScope: "production", ManagedPrefix: "FILES"}, Secrets: cloneCredentialSecrets(account.ID, app.ID, "production", managedID, "FILES", "source"),
		MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 100})
	if err != nil {
		t.Fatal(err)
	}
	customer, err := s.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: account.ID, BucketID: source.ID,
		AccessKeyID: "GRGA" + strings.Repeat("B", 16), SecretSealed: []byte("customer-signing-key"), KID: "customer-kid", Label: "reader", Permission: "read", Status: "active"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "CUSTOMER_CONFIG", "captured-config"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, "production", "CUSTOMER_TOKEN", "customer-kid", "1111111111111111", state.SecretClassPersistent, []byte("captured-token")); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op = lease.Operation
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, op.Revision)
	if err != nil || len(views) != 1 {
		t.Fatal(err)
	}
	view := views[0]
	target, _, err := s.ReserveProjectEnvironmentCloneObjectBucket(ctx, lease, app.ID, source.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimObjectBucket(ctx, account.ID, app.ID, target.ID, "target", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishObjectBucket(ctx, target.ID, "target", "ready"); err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash([]state.ProjectEnvironmentCloneObjectVersion{})
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.ProjectEnvironmentCloneObjectManifest{OperationID: op.ID, SourceBucketID: source.ID, TargetBucketID: target.ID, CapturedAt: point, Hash: hash, Objects: []state.ProjectEnvironmentCloneObjectCheckpoint{}}
	if _, err := s.PutProjectEnvironmentCloneObjectManifestForLease(ctx, lease, manifest); err != nil {
		t.Fatal(err)
	}
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "object_storage", Name: source.ID, SourceID: source.ID, TargetID: target.ID, SourceVersion: hash, CapturePoint: point.Format(time.RFC3339Nano), Status: "captured"}}
	resources = append(resources,
		state.ProjectEnvironmentCloneResource{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"},
		state.ProjectEnvironmentCloneResource{Kind: "project_config", Name: "production", SourceVersion: view.SourceProjectConfigHash, Status: "ready"},
		state.ProjectEnvironmentCloneResource{Kind: "workload", Name: view.WorkloadSlug, SourceID: view.SourceDeploymentID, SourceVersion: view.SourceHash, Status: "captured"})
	for _, kind := range []string{"variables", "secrets"} {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: kind, Name: view.WorkloadSlug, SourceID: app.ID, TargetID: app.ID, SourceVersion: view.SourceValuesHash, Status: "ready"})
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	newID := uuid.NewString()
	request := state.ProjectEnvironmentCloneObjectCredentialRequest{AppID: app.ID, SourceBucketID: source.ID, SourceCredentialID: managed.ID,
		Target: state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{ID: newID, AccountID: account.ID, BucketID: target.ID,
			AccessKeyID: "GRGA" + strings.Repeat("C", 16), SecretSealed: []byte("fresh-signing-key"), KID: "fresh-kid", Label: managed.Label, Permission: managed.Permission, Status: "active",
			ManagedAppID: app.ID, ManagedScope: op.TargetEnvironment, ManagedPrefix: managed.ManagedPrefix}, Secrets: cloneCredentialSecrets(account.ID, app.ID, op.TargetEnvironment, newID, "FILES", "fresh"),
			MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 100}}
	if _, err := s.PrepareProjectEnvironmentCloneObjectCredential(ctx, lease, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("prepared credentials before copy proof: %v", err)
	}
	resources[0].Status = "ready"
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, op.Status, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	clone := state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: op.TargetEnvironment,
		CloneOperationID: op.ID, CloneOperationRevision: op.Revision, ManagedBindingsPrepared: true, PreparedManagedBindingIDs: []string{customer.ID}, PreparedManagedSecretCount: 6}
	// Matching target counts and managed ownership markers do not establish an
	// independent preparation. These envelopes have no operation receipt.
	for _, forged := range cloneCredentialSecrets(account.ID, app.ID, op.TargetEnvironment, customer.ID, "FILES", "forged") {
		if err := s.PutManagedObjectStorageSecret(ctx, forged); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(account.Plan)); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedValueProof) {
		t.Fatalf("materialized forged managed preparation with matching counts: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("forged preparation created an environment: %v", err)
	}
	if err := s.DeleteManagedObjectStorageSecrets(ctx, customer.ID); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"token", "revision", "source", "target", "permission", "prefix", "scope", "incomplete_envelopes", "credential_quota", "secret_quota", "mixed_secret_owner", "ephemeral"} {
		badLease, bad := lease, request
		bad.Target.Secrets = append([]state.AppSecret(nil), request.Target.Secrets...)
		switch fault {
		case "token":
			badLease.Token = uuid.NewString()
		case "revision":
			badLease.Operation.Revision--
		case "source":
			bad.SourceCredentialID = uuid.NewString()
		case "target":
			bad.Target.Credential.BucketID = source.ID
		case "permission":
			bad.Target.Credential.Permission = "read"
		case "prefix":
			bad.Target.Credential.ManagedPrefix = "OTHER"
		case "scope":
			bad.Target.Credential.ManagedScope = "production"
		case "incomplete_envelopes":
			bad.Target.Secrets = bad.Target.Secrets[:5]
		case "credential_quota":
			bad.Target.MaxCredentialsPerBucket = 0
		case "secret_quota":
			bad.Target.MaxSecretsPerApp = 11
		case "mixed_secret_owner":
			bad.Target.Secrets[0].ManagedPostgresBindingID = uuid.NewString()
		case "ephemeral":
			bad.Target.Secrets[0].SecretClass = "ephemeral"
		}
		if _, err := s.PrepareProjectEnvironmentCloneObjectCredential(ctx, badLease, bad); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("accepted invalid %s preparation: %v", fault, err)
		}
		creds, err := s.ListObjectS3Credentials(ctx, account.ID, target.ID)
		if err != nil || len(creds) != 0 {
			t.Fatalf("failed %s preparation wrote credentials: %d, %v", fault, len(creds), err)
		}
		if _, err := s.GetAppSecretInScope(ctx, account.ID, app.ID, op.TargetEnvironment, "FILES_ENDPOINT"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed %s preparation wrote envelopes: %v", fault, err)
		}
	}

	// A collision at secret insertion must roll back the fresh credential and
	// retry receipt as well. PostgreSQL reaches this after credential INSERT.
	collision := request.Target.Secrets[0]
	collision.ManagedObjectStorageCredentialID = managed.ID
	collision.Ciphertext = []byte("existing-envelope")
	if err := s.PutManagedObjectStorageSecret(ctx, collision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareProjectEnvironmentCloneObjectCredential(ctx, lease, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted conflicting runtime key: %v", err)
	}
	if credentials, err := s.ListObjectS3Credentials(ctx, account.ID, target.ID); err != nil || len(credentials) != 0 {
		t.Fatalf("partial credential survived envelope collision: %d, %v", len(credentials), err)
	}
	if _, err := s.ProjectEnvironmentCloneObjectCredentialForLease(ctx, lease, managed.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("partial receipt survived envelope collision: %v", err)
	}
	if err := s.DeleteManagedObjectStorageSecrets(ctx, managed.ID); err != nil {
		t.Fatal(err)
	}
	// The worker uses the frozen source permissions after source credentials
	// and managed envelopes have been removed. Target preparation precedes
	// materialization of the environment and cannot rely on that row existing.
	if _, err := s.RevokeObjectS3ComputeBinding(ctx, account.ID, source.ID, managed.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeObjectS3Credential(ctx, account.ID, source.ID, customer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimObjectBucket(ctx, account.ID, app.ID, source.ID, "delete-source", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishObjectBucket(ctx, source.ID, "delete-source", "deleted"); err != nil {
		t.Fatal(err)
	}
	stamp, stamped, err := s.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := s.LiveDeploymentForScope(ctx, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: deployment.ID, FCVersion: "fc-test", MemBytes: 1024,
		DiskBytes: 512, StorageKey: "clone-credential/" + deployment.ID, Tier: state.SnapshotTierInit})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareProjectEnvironmentCloneObjectCredential(ctx, lease, request)
	if err != nil || prepared.Credential.ID != newID || len(prepared.Secrets) != 6 || len(prepared.Hash) != 64 {
		t.Fatalf("prepare fresh binding: %v", err)
	}
	for range 2 {
		if replay, err := s.PrepareProjectEnvironmentCloneObjectCredential(ctx, lease, request); err != nil || replay.Hash != prepared.Hash || replay.Credential.ID != newID {
			t.Fatalf("changed preparation on replay: %v", err)
		}
	}
	prepared.Credential.SecretSealed[0], prepared.Secrets[0].Ciphertext[0] = 'X', 'X'
	recovered, err := s.ProjectEnvironmentCloneObjectCredentialForLease(ctx, lease, managed.ID)
	if err != nil || string(recovered.Credential.SecretSealed) != "fresh-signing-key" || strings.HasPrefix(string(recovered.Secrets[0].Ciphertext), "X") {
		t.Fatalf("caller modified private receipt: %v", err)
	}
	standalone := request
	standalone.SourceCredentialID = customer.ID
	standalone.Target.Credential = state.ObjectS3Credential{ID: uuid.NewString(), AccountID: account.ID, BucketID: target.ID, AccessKeyID: "GRGA" + strings.Repeat("D", 16),
		SecretSealed: []byte("fresh-customer-key"), KID: "fresh-kid", Label: customer.Label, Permission: customer.Permission, Status: "active"}
	standalone.Target.Secrets = nil
	if receipt, err := s.PrepareProjectEnvironmentCloneObjectCredential(ctx, lease, standalone); err != nil || len(receipt.Secrets) != 0 {
		t.Fatalf("prepare customer credential: %v", err)
	}
	if after, ok, err := s.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || ok != stamped || !after.Equal(stamp) {
		t.Fatalf("private preparation changed production runtime stamp: %v", err)
	}
	if actual, err := s.LatestSnapshot(ctx, deployment.ID); err != nil || actual.ID != snapshot.ID {
		t.Fatalf("private preparation invalidated production snapshot: %v", err)
	}
	if _, _, err := s.ResolveObjectS3Credential(ctx, request.Target.Credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("used prepared credential before publication: %v", err)
	}
	if _, _, err := s.ResolveObjectS3Credential(ctx, standalone.Target.Credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("used customer credential before publication: %v", err)
	}
	clone.PreparedManagedBindingIDs = []string{newID}
	changed := request.Target.Secrets[0]
	changed.Ciphertext = []byte("changed-before-materialization")
	if err := s.PutManagedObjectStorageSecret(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(account.Plan)); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedValueProof) {
		t.Fatalf("materialized changed prepared envelope: %v", err)
	}
	if err := s.PutManagedObjectStorageSecret(ctx, request.Target.Secrets[0]); err != nil {
		t.Fatal(err)
	}
	for _, verify := range beforeMaterialization {
		verify(clone, request.Target)
	}
	if _, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(account.Plan)); err != nil {
		t.Fatalf("materialize prepared target: %v", err)
	}
	spec, err := s.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, op.TargetEnvironment, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	stageDeployment, err := s.CreateDeploymentForEnvironmentClone(ctx, account.ID, project.ID, op.ID, op.Revision, app.ID, spec.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLiveDark(ctx, stageDeployment.ID); err != nil {
		t.Fatal(err)
	}
	resources[3].TargetID, resources[3].Status = stageDeployment.ID, "ready"
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, ""); !errors.Is(err, state.ErrProjectEnvironmentCloneResourcePublicationProof) {
		t.Fatalf("authentic object envelopes did not reach independent resource publication guard: %v", err)
	}
	if _, err := s.ActiveProjectReleaseSet(ctx, account.ID, project.ID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("incomplete object isolation acquired a serving release: %v", err)
	}
	old := lease
	if err := s.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	lease, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneObjectCredentialForLease(ctx, old, managed.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old worker read preparation: %v", err)
	}
	if got, err := s.ProjectEnvironmentCloneObjectCredentialForLease(ctx, lease, managed.ID); err != nil || got.Hash != recovered.Hash {
		t.Fatalf("takeover lost preparation: %v", err)
	}
	changed = request.Target.Secrets[0]
	changed.Ciphertext = []byte("changed-after-preparation")
	if err := s.PutManagedObjectStorageSecret(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneObjectCredentialForLease(ctx, lease, managed.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted changed target envelope: %v", err)
	}
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, lease.Operation.Status, state.CloneOperationPublishing, lease.Operation.Revision, resources, ""); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedValueProof) {
		t.Fatalf("altered object envelope reached resource publication validation: %v", err)
	}
}
