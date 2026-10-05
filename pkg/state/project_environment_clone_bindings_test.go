package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneBindingTestStore interface {
	cloneWorkloadTestStore
	state.ProjectEnvironmentCloneBindingCaptureStore
	state.ObjectBucketStore
	state.ObjectS3CredentialBindingStore
	state.ObjectBucketAccessStore
	CreateAPIKey(context.Context, string, []byte, string, []string) (state.APIKey, error)
}

// ADR-585: independent buckets and credential/access policies belong to the
// frozen catalogue even when no application secret refers to them.
func TestMemCloneBindingCatalogueSurvivesSourceDeletion(t *testing.T) {
	cloneBindingCatalogueSurvivesSourceDeletion(t, state.NewMemStore())
}

func cloneBindingFixture(t *testing.T, s cloneWorkloadTestStore) (state.Account, state.Project, state.App, state.ProjectEnvironmentCloneOperation) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, "binding-capture-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "bindings"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "bindings-api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:bindings"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/bindings.ext4", "layers/bindings-"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: a.ID, ProjectID: p.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "bindings", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return a, p, app, op
}

func cloneBindingBucket(t *testing.T, s state.ObjectBucketStore, a state.Account, app state.App, name, scope string, ready bool) state.ObjectBucket {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	b, err := s.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: a.ID, AppID: app.ID, Name: name, Scope: scope,
		Region: "us-east-1", BackendID: "storage", BackendFingerprint: strings.Repeat("b", 64), PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", ""),
		PublicRead: true, ServeAt: "/assets/" + name}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		if _, err := s.ClaimObjectBucket(ctx, a.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func cloneBindingCatalogueSurvivesSourceDeletion(t *testing.T, s cloneBindingTestStore) {
	t.Helper()
	ctx := context.Background()
	a, p, app, op := cloneBindingFixture(t, s)
	bucket := cloneBindingBucket(t, s, a, app, "assets", "production", true)
	cloneBindingBucket(t, s, a, app, "ignored", "other", false)
	credential, err := s.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: a.ID, BucketID: bucket.ID,
		AccessKeyID: "GRGAAAAAAAAAAAAAAAAA", SecretSealed: []byte("source-signing-material"), KID: "source-kid", Label: "customer-reader", Permission: "read", Status: "active"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	_, keyHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateAPIKey(ctx, a.ID, keyHash, "bucket-reader", []string{api.ScopeStorageRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetObjectBucketAccessGrant(ctx, a.ID, bucket.ID, key.ID, "read"); err != nil {
		t.Fatal(err)
	}
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || len(views) != 1 || views[0].SourceBindingsHash == "" {
		t.Fatalf("binding capture: count=%d err=%v", len(views), err)
	}
	bindings, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID)
	if err != nil || len(bindings) != 1 || len(bindings[0].Buckets) != 1 {
		t.Fatalf("binding catalogue: count=%d err=%v", len(bindings), err)
	}
	captured := bindings[0].Buckets[0]
	if bindings[0].Hash != views[0].SourceBindingsHash || captured.ID != bucket.ID || !captured.PublicRead || captured.ServeAt != bucket.ServeAt ||
		len(captured.Credentials) != 1 || captured.Credentials[0].ID != credential.ID || captured.Credentials[0].Permission != "read" ||
		len(captured.AccessGrants) != 1 || captured.AccessGrants[0].APIKeyID != key.ID {
		t.Fatal("standalone storage configuration omitted from capture")
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{credential.AccessKeyID, "source-signing-material", "source-kid", "secret_sealed"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("worker catalogue disclosed credential material: %s", forbidden)
		}
	}
	bindings[0].Buckets[0].Credentials[0].Permission = "write"
	bindings[0].Buckets[0].AccessGrants[0].Permission = "write"
	if err := s.DeleteObjectBucketAccessGrant(ctx, a.ID, bucket.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeObjectS3Credential(ctx, a.ID, bucket.ID, credential.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimObjectBucket(ctx, a.ID, app.ID, bucket.ID, "delete", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishObjectBucket(ctx, bucket.ID, "delete", "deleted"); err != nil {
		t.Fatal(err)
	}
	again, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	againRaw, _ := json.Marshal(again)
	if string(raw) != string(againRaw) {
		t.Fatal("source deletion or caller mutation changed the frozen catalogue")
	}
	retry, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || !reflect.DeepEqual(views, retry) {
		t.Fatalf("capture retry consulted deleted source resources: %v", err)
	}
	for _, identities := range [][2]string{{uuid.NewString(), p.ID}, {a.ID, uuid.NewString()}} {
		if _, err := s.ProjectEnvironmentCloneBindings(ctx, identities[0], identities[1], op.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("binding catalogue crossed account/project ownership: %v", err)
		}
	}
	resources := []state.ProjectEnvironmentCloneResource{
		{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"},
		{Kind: "project_config", Name: "production", SourceVersion: views[0].SourceProjectConfigHash, Status: "ready"},
		{Kind: "workload", Name: views[0].WorkloadSlug, SourceID: views[0].SourceDeploymentID, SourceVersion: views[0].SourceHash, Status: "captured"},
	}
	for _, kind := range []string{"variables", "secrets"} {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: kind, Name: views[0].WorkloadSlug, SourceID: app.ID, TargetID: app.ID, SourceVersion: views[0].SourceValuesHash, Status: "ready"})
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: a.ID, ProjectID: p.ID, SourceSlug: "production", TargetSlug: "stage", CloneOperationID: op.ID, CloneOperationRevision: op.Revision}, api.MustLimitsFor(a.Plan)); err != nil {
		t.Fatal(err)
	}
	spec, err := s.ProjectEnvironmentWorkloadSpec(ctx, a.ID, p.ID, "stage", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeploymentForEnvironmentClone(ctx, a.ID, p.ID, op.ID, op.Revision, app.ID, spec.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLiveDark(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	resources[2].TargetID, resources[2].Status = d.ID, "ready"
	if _, err := s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, ""); !errors.Is(err, state.ErrProjectEnvironmentCloneResourcePublicationProof) {
		t.Fatalf("standalone bucket advertised ready without an isolated copy proof: %v", err)
	}
	if _, err := s.ActiveProjectReleaseSet(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("uncopied standalone data acquired a serving graph")
	}
}

// ADR-585: unready standalone data resources cannot disappear from a full capture.
func TestMemCloneBindingCatalogueRejectsUnreadyBucket(t *testing.T) {
	cloneBindingCatalogueRejectsUnreadyBucket(t, state.NewMemStore())
}

// ADR-585: six managed object envelopes must agree with the frozen compute
// binding; customer credentials do not substitute for this ownership proof.
func TestMemCloneBindingCatalogueCapturesObjectComputeBinding(t *testing.T) {
	cloneBindingCatalogueCapturesObjectComputeBinding(t, state.NewMemStore())
}

func cloneObjectComputeBindingFixture(t *testing.T, s state.ObjectS3CredentialBindingStore, a state.Account, app state.App, bucket state.ObjectBucket) state.ObjectS3Credential {
	t.Helper()
	id := uuid.NewString()
	req := state.ObjectS3ComputeBindingCreateRequest{Credential: state.ObjectS3Credential{ID: id, AccountID: a.ID, BucketID: bucket.ID,
		AccessKeyID: "GRGABBBBBBBBBBBBBBBB", SecretSealed: []byte("sealed-compute-key"), KID: "test-kid", Label: "compute-reader", Permission: "read", Status: "active",
		ManagedAppID: app.ID, ManagedScope: "production", ManagedPrefix: "ASSETS"}, MaxCredentialsPerBucket: 10, MaxSecretsPerApp: 20}
	for _, suffix := range []string{"_ENDPOINT", "_REGION", "_BUCKET", "_ACCESS_KEY_ID", "_SECRET_ACCESS_KEY", "_ADDRESSING_STYLE"} {
		req.Secrets = append(req.Secrets, state.AppSecret{AccountID: a.ID, AppID: app.ID, Scope: "production", Key: "ASSETS" + suffix,
			Ciphertext: []byte("sealed-compute-value"), Kid: "test-kid", ValueHash: "123456789abcdef0", ManagedObjectStorageCredentialID: id})
	}
	credential, err := s.CreateObjectS3ComputeBinding(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func cloneBindingCatalogueCapturesObjectComputeBinding(t *testing.T, s cloneBindingTestStore) {
	t.Helper()
	ctx := context.Background()
	a, p, app, op := cloneBindingFixture(t, s)
	bucket := cloneBindingBucket(t, s, a, app, "compute", "production", true)
	credential := cloneObjectComputeBindingFixture(t, s, a, app, bucket)
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || len(views) != 1 || views[0].SourceBindingsHash == "" {
		t.Fatalf("managed storage capture: %v", err)
	}
	bindings, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID)
	if err != nil || len(bindings) != 1 || len(bindings[0].Buckets) != 1 || len(bindings[0].Buckets[0].Credentials) != 1 {
		t.Fatalf("managed storage catalogue: %v", err)
	}
	frozen := bindings[0].Buckets[0].Credentials[0]
	if frozen.ID != credential.ID || frozen.ManagedAppID != app.ID || frozen.ManagedScope != "production" || frozen.ManagedPrefix != "ASSETS" || frozen.Permission != "read" {
		t.Fatal("managed object binding omitted from catalogue")
	}
}

func cloneBindingCatalogueRejectsUnreadyBucket(t *testing.T, s cloneBindingTestStore) {
	t.Helper()
	ctx := context.Background()
	a, p, app, op := cloneBindingFixture(t, s)
	cloneBindingBucket(t, s, a, app, "pending", "production", false)
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); !errors.Is(err, state.ErrProjectEnvironmentCloneBindingCapture) {
		t.Fatalf("unready bucket omitted: %v", err)
	}
	if records, err := s.ProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID); err != nil || len(records) != 0 {
		t.Fatal("incomplete capture left durable workload records")
	}
}

// ADR-585: the separate PostgreSQL memory store is not an atomic binding
// catalogue. A managed-secret ID alone cannot prove a complete source capture.
func TestMemCloneBindingCatalogueRequiresPostgresCatalogue(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	a, p, app, op := cloneBindingFixture(t, s)
	if err := s.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: a.ID, AppID: app.ID, Scope: "production", Key: "DATABASE_URL", Ciphertext: []byte("sealed"),
		ManagedPostgresBindingID: "binding", ManagedPostgresAccess: "read_write", ManagedCredentialRef: "ref", ManagedCredentialGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); !errors.Is(err, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable) {
		t.Fatalf("invented PostgreSQL catalogue: %v", err)
	}
}
