// adr: 581
package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type capturedObjectWorkerProvider struct {
	*environmentSnapshotProvider
	created, copiedSources, copiedTargets []string
}

func (*capturedObjectWorkerProvider) ObserveObjectVersionRetention(_ context.Context, _ string, item objectstorage.ObjectVersion) (objectstorage.ObjectVersionRetention, error) {
	return objectstorage.ObjectVersionRetention{VersionID: item.VersionID, MetadataVersion: item.MetadataVersion, RetainedUntil: time.Now().Add(time.Hour)}, nil
}

func (p *capturedObjectWorkerProvider) CreateBucket(_ context.Context, name string) error {
	p.created = append(p.created, name)
	return nil
}

func (p *capturedObjectWorkerProvider) CopyObjectBetweenBuckets(ctx context.Context, source, target string, request objectstorage.CopyObjectRequest) (objectstorage.CopyObjectResult, error) {
	p.copiedSources = append(p.copiedSources, source)
	p.copiedTargets = append(p.copiedTargets, target)
	return p.environmentSnapshotProvider.CopyObjectBetweenBuckets(ctx, source, target, request)
}

type cloneObjectWorkerTestStore interface {
	state.Store
	cloneObjectWorkerStore
	state.ProjectEnvironmentCloneWorkloadStore
	state.ProjectEnvironmentCloneBindingCaptureStore
	state.ProjectEnvironmentCloneObjectCredentialStore
	state.ObjectS3CredentialBindingStore
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
}

type cloneObjectCheckpointFailureStore struct {
	cloneObjectWorkerTestStore
	failStatus       string
	failCredential   bool
	credentialWrites int
}

func (s *cloneObjectCheckpointFailureStore) PrepareProjectEnvironmentCloneObjectCredential(ctx context.Context, lease state.ProjectEnvironmentCloneLease, request state.ProjectEnvironmentCloneObjectCredentialRequest) (state.ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	prepared, err := s.cloneObjectWorkerTestStore.PrepareProjectEnvironmentCloneObjectCredential(ctx, lease, request)
	if err == nil {
		s.credentialWrites++
		if s.failCredential {
			s.failCredential = false
			return state.ProjectEnvironmentCloneObjectCredentialPreparation{}, errors.New("credential acknowledgement unavailable")
		}
	}
	return prepared, err
}

func (s *cloneObjectCheckpointFailureStore) AdvanceProjectEnvironmentCloneOperation(ctx context.Context, accountID, projectID, operationID, expected, next string, revision int64, resources []state.ProjectEnvironmentCloneResource, code string) (state.ProjectEnvironmentCloneOperation, error) {
	for _, resource := range resources {
		if resource.Kind == "object_storage" && resource.Status == s.failStatus {
			s.failStatus = ""
			return state.ProjectEnvironmentCloneOperation{}, errors.New("object resource checkpoint unavailable")
		}
	}
	return s.cloneObjectWorkerTestStore.AdvanceProjectEnvironmentCloneOperation(ctx, accountID, projectID, operationID, expected, next, revision, resources, code)
}

func TestCapturedCloneObjectWorkerResumesDurableManifestAndCopies(t *testing.T) {
	srv, base, account, project, app := newProjectLifecycleFixture(t)
	capturedCloneObjectWorkerContract(t, srv, base, account, project, app)
}

func capturedCloneObjectWorkerContract(t *testing.T, srv *server, base cloneObjectWorkerTestStore, account state.Account, project state.Project, app state.App) {
	t.Helper()
	ctx := context.Background()
	identity, teardown := withTestIdentities(t)
	defer teardown()
	store := &cloneObjectCheckpointFailureStore{cloneObjectWorkerTestStore: base}
	srv.store = store
	if err := srv.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	provider := &capturedObjectWorkerProvider{environmentSnapshotProvider: &environmentSnapshotProvider{
		versions: []objectstorage.ObjectVersion{{Key: "data.json", VersionID: "v1", Size: 3, LastModified: point.Add(-time.Second)},
			{Key: "data.json", VersionID: "v2", Size: 3, LastModified: point.Add(time.Second)}},
		bodies: map[string]string{"v1": "old", "v2": "new"},
	}}
	config := objectstorage.BackendConfig{ID: "storage", Driver: "fixture", Region: "us-east-1", Namespace: "fixture", Endpoint: "https://storage.example.test", S3Region: "us-east-1"}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: config.Region, Defaults: map[string]string{config.Region: config.ID}, Backends: []objectstorage.BackendConfig{config}},
		func(string) string { return "" }, map[string]objectstorage.Factory{"fixture": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
			return provider, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	srv.WithObjectStorage(registry)
	backend, err := registry.Default(config.Region)
	if err != nil {
		t.Fatal(err)
	}
	sources := []state.ObjectBucket{}
	for _, name := range []string{"assets", "standalone"} {
		id := uuid.NewString()
		bucket, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: account.ID, AppID: app.ID, Name: name, Scope: "production",
			Region: config.Region, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", ""), PublicRead: true, ServeAt: "/" + name}, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "source", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishObjectBucket(ctx, bucket.ID, "source", "ready"); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, bucket)
	}
	sourceCredentials := capturedCloneObjectWorkerSourceCredentials(t, srv, store, account, app, sources)
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:captured"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, d.ID, "/captured.ext4", "layers/captured-"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: account.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "object-worker", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	if _, err := store.CaptureProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	plans, err := srv.capturedProjectEnvironmentObjectPlans(ctx, op)
	if err != nil || len(plans) != 2 {
		t.Fatalf("standalone bucket omitted: %+v, %v", plans, err)
	}
	resources, err := capturedProjectEnvironmentObjectResources(plans, point)
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, op.Status, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	// Freeze metadata first, then remove source rows. Provider identities still
	// come exclusively from the private catalogue on every worker retry.
	for _, credential := range sourceCredentials {
		if credential.ManagedAppID != "" {
			if _, err := store.RevokeObjectS3ComputeBinding(ctx, account.ID, credential.BucketID, credential.ID); err != nil {
				t.Fatal(err)
			}
		} else if err := store.RevokeObjectS3Credential(ctx, account.ID, credential.BucketID, credential.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range sources {
		if _, err := store.ClaimObjectBucket(ctx, account.ID, app.ID, source.ID, "delete-source", "deleting"); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishObjectBucket(ctx, source.ID, "delete-source", "deleted"); err != nil {
			t.Fatal(err)
		}
	}
	store.failStatus = "captured"
	lease, err = srv.captureProjectEnvironmentCloneObjects(ctx, lease)
	if err == nil || len(provider.created) != 1 || provider.listCalls != 1 || lease.Operation.Resources[0].TargetID == "" {
		t.Fatalf("missing persisted manifest boundary: lease %+v, error %v, creates %d, lists %d", lease, err, len(provider.created), provider.listCalls)
	}
	manifest, err := store.ProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, op.ID, plans[0].source.ID)
	if err != nil || manifest.TargetBucketID != lease.Operation.Resources[0].TargetID || len(manifest.Objects) != 1 || manifest.Objects[0].Source.VersionID != "v1" {
		t.Fatalf("lost committed manifest: %+v, %v", manifest, err)
	}
	lease, err = srv.captureProjectEnvironmentCloneObjects(ctx, lease)
	if err != nil || len(provider.created) != 2 || provider.listCalls != 2 {
		t.Fatalf("capture retry changed target or point: %v, creates %d, lists %d", err, len(provider.created), provider.listCalls)
	}
	stable := lease.Operation.Revision
	lease, err = srv.captureProjectEnvironmentCloneObjects(ctx, lease)
	if err != nil || provider.listCalls != 2 || len(provider.created) != 2 || lease.Operation.Revision != stable {
		t.Fatalf("completed capture replay wrote progress: %v, revision %d", err, lease.Operation.Revision)
	}
	provider.versions = nil
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, lease.Operation.Revision, lease.Operation.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	store.failStatus = "ready"
	lease, ready, err := srv.prepareProjectEnvironmentCloneObjects(ctx, lease)
	if err == nil || ready || provider.copyCalls != 1 {
		t.Fatalf("copy checkpoint boundary: ready %v, error %v, copies %d", ready, err, provider.copyCalls)
	}
	if _, _, _, err := srv.prepareProjectEnvironmentCloneObjectCredentials(ctx, lease); !errors.Is(err, state.ErrConflict) || store.credentialWrites != 0 {
		t.Fatalf("prepared credential before all copies ready: %v", err)
	}
	lease, ready, err = srv.prepareProjectEnvironmentCloneObjects(ctx, lease)
	if err != nil || !ready || provider.copyCalls != 2 || provider.listCalls != 2 || provider.destination != "old" || provider.lastCopiedVersion != "v1" {
		t.Fatalf("copy retry changed source: ready %v, error %v, copies %d, lists %d", ready, err, provider.copyCalls, provider.listCalls)
	}
	stable = lease.Operation.Revision
	lease, ready, err = srv.prepareProjectEnvironmentCloneObjects(ctx, lease)
	if err != nil || !ready || lease.Operation.Revision != stable || provider.copyCalls != 2 {
		t.Fatalf("completed copy replay wrote progress: ready %v, error %v, revision %d", ready, err, lease.Operation.Revision)
	}
	for i, plan := range plans {
		if provider.copiedSources[i] != plan.source.PhysicalName || provider.copiedTargets[i] == plan.source.PhysicalName || lease.Operation.Resources[i].Status != "ready" {
			t.Fatalf("wrong physical copy: source %q, target %q, resource %+v", provider.copiedSources[i], provider.copiedTargets[i], lease.Operation.Resources[i])
		}
		if _, err := store.GetObjectBucket(ctx, account.ID, app.ID, lease.Operation.Resources[i].TargetID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("copy leaked before publication: %v", err)
		}
	}
	lease = capturedCloneObjectWorkerCredentialContract(t, srv, store, account, project, app, lease, sourceCredentials, identity)
	old := lease
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.prepareProjectEnvironmentCloneObjects(ctx, old); !errors.Is(err, state.ErrConflict) || provider.copyCalls != 2 {
		t.Fatalf("stale worker reached provider: %v, copies %d", err, provider.copyCalls)
	}
	if _, _, _, err := srv.prepareProjectEnvironmentCloneObjectCredentials(ctx, old); !errors.Is(err, state.ErrConflict) || store.credentialWrites != 2 {
		t.Fatalf("stale worker prepared credential: %v", err)
	}
	lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ready, err := srv.prepareProjectEnvironmentCloneObjects(ctx, lease); err != nil || !ready || provider.copyCalls != 2 {
		t.Fatalf("takeover recopied complete targets: ready %v, error %v, copies %d", ready, err, provider.copyCalls)
	}
	if _, ids, count, err := srv.prepareProjectEnvironmentCloneObjectCredentials(ctx, lease); err != nil || len(ids) != 1 || count != 6 || store.credentialWrites != 2 {
		t.Fatalf("takeover regenerated credentials: %v", err)
	}
}

func capturedCloneObjectWorkerSourceCredentials(t *testing.T, srv *server, store cloneObjectWorkerTestStore, account state.Account, app state.App, buckets []state.ObjectBucket) []state.ObjectS3Credential {
	t.Helper()
	ctx := context.Background()
	result := []state.ObjectS3Credential{}
	for i, bucket := range buckets {
		key, _, err := api.GenerateObjectS3Credential()
		if err != nil {
			t.Fatal(err)
		}
		credential := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: account.ID, BucketID: bucket.ID, AccessKeyID: key,
			SecretSealed: []byte("unopenable-production-secret"), KID: "production-kid", Label: "customer-reader", Permission: "read", Status: "active"}
		if i == 0 {
			credential.ManagedAppID, credential.ManagedScope, credential.ManagedPrefix = app.ID, "production", "FILES"
			credential.Label, credential.Permission = "compute", "read_write"
			values := objectStorageBindingSecretValues(objectStorageBindingSecretKeys("FILES"), srv.objectStorage.PublicEndpoint, srv.objectStorage.PublicRegion, bucket.Name, key, "production-only-key")
			secrets, problem := srv.sealObjectStorageBindingValues(account, app, credential.ID, "production", values, api.MustLimitsFor(account.Plan))
			if problem != nil {
				t.Fatal(problem)
			}
			credential, err = store.CreateObjectS3ComputeBinding(ctx, state.ObjectS3ComputeBindingCreateRequest{Credential: credential, Secrets: secrets,
				MaxCredentialsPerBucket: api.MaxObjectS3CredentialsPerBucket, MaxSecretsPerApp: api.MustLimitsFor(account.Plan).SecretCountMax})
		} else {
			credential, err = store.CreateObjectS3Credential(ctx, credential, api.MaxObjectS3CredentialsPerBucket)
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, credential)
	}
	return result
}
