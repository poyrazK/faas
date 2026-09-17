package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeManifestObjectStorageClient struct {
	buckets     api.ObjectBucketList
	bucketsErr  error
	bindings    map[string]api.ObjectStorageComputeBindingList
	bindingsErr error
	requests    []api.CreateObjectStorageComputeBindingRequest
	requestKeys []string
	created     api.ObjectStorageComputeBinding
	createErr   error
}

func (f *fakeManifestObjectStorageClient) ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error) {
	return f.buckets, f.bucketsErr
}

func (f *fakeManifestObjectStorageClient) ListObjectStorageComputeBindings(_ context.Context, _, bucket string) (api.ObjectStorageComputeBindingList, error) {
	if f.bindingsErr != nil {
		return api.ObjectStorageComputeBindingList{}, f.bindingsErr
	}
	return f.bindings[bucket], nil
}

func (f *fakeManifestObjectStorageClient) CreateObjectStorageComputeBinding(ctx context.Context, _, _ string, request api.CreateObjectStorageComputeBindingRequest) (api.ObjectStorageComputeBinding, error) {
	f.requests = append(f.requests, request)
	f.requestKeys = append(f.requestKeys, api.IdempotencyKeyFromContext(ctx))
	return f.created, f.createErr
}

func TestDeployManifestObjectStorageBindingsCreatesBinding(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `buckets:
  - bucket: assets
    app: api
    scope: production
    permission: read
    label: uploads
    prefix: GREGALE_S3_ASSETS
`)
	fake := &fakeManifestObjectStorageClient{
		buckets:  api.ObjectBucketList{Items: []api.ObjectBucket{{ID: "bucket-id", Name: "assets", Scope: "production", State: "ready"}}},
		bindings: map[string]api.ObjectStorageComputeBindingList{"bucket-id": {}},
		created:  api.ObjectStorageComputeBinding{ID: "binding-id", BucketID: "bucket-id", Scope: "production", Prefix: "GREGALE_S3_ASSETS"},
	}
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })

	if err := deployManifestObjectStorageBindings(context.Background(), fake, "api", dir); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fake.requests) != 1 || len(fake.requestKeys) != 1 {
		t.Fatalf("requests = %+v, keys = %v; want one binding", fake.requests, fake.requestKeys)
	}
	request := fake.requests[0]
	if request.Label != "uploads" || request.Permission != api.ObjectBucketPermissionRead || request.Prefix != "GREGALE_S3_ASSETS" {
		t.Fatalf("binding request = %+v", request)
	}
	if fake.requestKeys[0] == "" {
		t.Fatal("manifest binding create missing idempotency key")
	}
}

func TestDeployManifestObjectStorageBindingsReusesMatchingBinding(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "buckets:\n  - bucket: assets\n")
	fake := &fakeManifestObjectStorageClient{
		buckets: api.ObjectBucketList{Items: []api.ObjectBucket{{ID: "bucket-id", Name: "assets", Scope: api.DefaultEnvScope, State: "ready"}}},
		bindings: map[string]api.ObjectStorageComputeBindingList{"bucket-id": {Items: []api.ObjectStorageComputeBinding{{
			ID: "binding-id", BucketID: "bucket-id", Scope: api.DefaultEnvScope, Prefix: "GREGALE_S3_ASSETS",
			Credential: api.ObjectS3Credential{Permission: api.ObjectBucketPermissionReadWrite, Status: "active"},
		}}}},
	}
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })

	if err := deployManifestObjectStorageBindings(context.Background(), fake, "api", dir); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("created %d bindings, want reuse", len(fake.requests))
	}
}

func TestDeployManifestObjectStorageBindingsRejectsPermissionDrift(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "buckets:\n  - bucket: assets\n    permission: read\n")
	fake := &fakeManifestObjectStorageClient{
		buckets: api.ObjectBucketList{Items: []api.ObjectBucket{{ID: "bucket-id", Name: "assets", Scope: api.DefaultEnvScope, State: "ready"}}},
		bindings: map[string]api.ObjectStorageComputeBindingList{"bucket-id": {Items: []api.ObjectStorageComputeBinding{{
			ID: "binding-id", BucketID: "bucket-id", Scope: api.DefaultEnvScope, Prefix: "GREGALE_S3_ASSETS",
			Credential: api.ObjectS3Credential{Permission: api.ObjectBucketPermissionReadWrite, Status: "active"},
		}}}},
	}
	err := deployManifestObjectStorageBindings(context.Background(), fake, "api", dir)
	if err == nil || !strings.Contains(err.Error(), "delete the binding and redeploy") {
		t.Fatalf("err = %v, want actionable permission drift", err)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("created %d bindings after permission drift", len(fake.requests))
	}
}

func TestDeployManifestObjectStorageBindingsUsesDeploymentEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "buckets:\n  - bucket: assets\n")
	fake := &fakeManifestObjectStorageClient{
		buckets:  api.ObjectBucketList{Items: []api.ObjectBucket{{ID: "bucket-id", Name: "assets", Scope: "staging", State: "ready"}}},
		bindings: map[string]api.ObjectStorageComputeBindingList{"bucket-id": {}},
		created:  api.ObjectStorageComputeBinding{Scope: "staging", Prefix: "GREGALE_S3_ASSETS"},
	}
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })

	if err := deployManifestObjectStorageBindings(context.Background(), fake, "api", dir, "staging"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(fake.requests) != 1 || fake.requests[0].Prefix != "GREGALE_S3_ASSETS" {
		t.Fatalf("requests = %+v, want staging binding", fake.requests)
	}
}

func TestDeployManifestObjectStorageBindingsPropagatesListError(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "buckets:\n  - bucket: assets\n")
	fake := &fakeManifestObjectStorageClient{bucketsErr: errors.New("temporary")}
	if err := deployManifestObjectStorageBindings(context.Background(), fake, "api", dir); !errors.Is(err, fake.bucketsErr) {
		t.Fatalf("err = %v, want list error", err)
	}
}

func TestManifestDeploymentScopeCombinesDatabasesAndBuckets(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `databases:
  - database: orders
    scope: default
buckets:
  - bucket: assets
    scope: staging
`)
	if _, err := manifestDeploymentScope("api", dir); err == nil || !strings.Contains(err.Error(), "multiple scopes") {
		t.Fatalf("err = %v, want mixed resource scopes", err)
	}
}
