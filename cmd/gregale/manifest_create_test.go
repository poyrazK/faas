package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// creatingPostgresClient is a manifest Postgres client that can also create
// databases (ADR-20261106120000 §1).
type creatingPostgresClient struct {
	databases []api.ManagedPostgresDatabase
	created   []api.CreateManagedPostgresDatabaseRequest
	gets      int
	bindings  []api.CreateManagedPostgresBindingRequest
	boundIDs  []string
	binding   api.ManagedPostgresBinding
}

func (f *creatingPostgresClient) UpdateApp(context.Context, string, api.UpdateAppRequest) (api.AppResponse, error) {
	return api.AppResponse{}, nil
}
func (f *creatingPostgresClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return api.AppResponse{ID: "app-id", Slug: "gateway"}, nil
}
func (f *creatingPostgresClient) ListManagedPostgresDatabases(context.Context) (api.ManagedPostgresDatabaseList, error) {
	return api.ManagedPostgresDatabaseList{Items: f.databases}, nil
}
func (f *creatingPostgresClient) CreateManagedPostgresBinding(_ context.Context, id string, request api.CreateManagedPostgresBindingRequest) (api.ManagedPostgresBinding, error) {
	f.boundIDs = append(f.boundIDs, id)
	f.bindings = append(f.bindings, request)
	return f.binding, nil
}
func (f *creatingPostgresClient) GetManagedPostgresCapabilities(context.Context, string) (api.ManagedPostgresCapabilities, error) {
	return api.ManagedPostgresCapabilities{Region: "us-east-1", PostgresMajors: []int{16, 18, 17}}, nil
}
func (f *creatingPostgresClient) CreateManagedPostgresDatabase(_ context.Context, request api.CreateManagedPostgresDatabaseRequest) (api.ManagedPostgresDatabase, error) {
	f.created = append(f.created, request)
	return api.ManagedPostgresDatabase{ID: "new-db", Name: request.Name, Region: request.Region, PostgresMajor: request.PostgresMajor, State: "provisioning"}, nil
}
func (f *creatingPostgresClient) GetManagedPostgresDatabase(_ context.Context, id string) (api.ManagedPostgresDatabase, error) {
	f.gets++
	return api.ManagedPostgresDatabase{ID: id, Name: "chronolake-db", State: "ready"}, nil
}

func writeTestManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func quietManifestCreate(t *testing.T) {
	t.Helper()
	previousJSON, previousWait, previousPoll := jsonOutput, manifestDatabaseReadyWait, manifestDatabasePoll
	jsonOutput, manifestDatabaseReadyWait, manifestDatabasePoll = true, time.Second, time.Millisecond
	t.Cleanup(func() {
		jsonOutput, manifestDatabaseReadyWait, manifestDatabasePoll = previousJSON, previousWait, previousPoll
	})
}

func TestManifestDatabaseCreateCreatesWaitsAndBinds(t *testing.T) {
	quietManifestCreate(t)
	dir := writeTestManifest(t, `databases:
  - database: chronolake-db
    app: gateway
    create:
      postgres_major: 17
  - database: chronolake-db
    app: gateway
    env: MIGRATION_DATABASE_URL
    access: migration
`)
	fake := &creatingPostgresClient{binding: api.ManagedPostgresBinding{ID: "b", State: "ready", EnvironmentKey: "DATABASE_URL"}}
	if err := deployManifestPostgresBindings(context.Background(), fake, "gateway", dir); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 || fake.created[0].Name != "chronolake-db" || fake.created[0].Region != "us-east-1" || fake.created[0].PostgresMajor != 17 {
		t.Fatalf("create requests %+v, want one chronolake-db in the default region on 17", fake.created)
	}
	if fake.gets == 0 {
		t.Fatal("did not wait for the provisioning database")
	}
	if len(fake.bindings) != 2 || fake.boundIDs[0] != "new-db" || fake.boundIDs[1] != "new-db" || fake.bindings[1].Access != "migration" {
		t.Fatalf("bindings %+v on %v", fake.bindings, fake.boundIDs)
	}
}

func TestManifestDatabaseWithoutCreateStillFailsBeforeMutation(t *testing.T) {
	quietManifestCreate(t)
	dir := writeTestManifest(t, "databases:\n  - database: missing-db\n")
	fake := &creatingPostgresClient{}
	err := deployManifestPostgresBindings(context.Background(), fake, "gateway", dir)
	if err == nil || !strings.Contains(err.Error(), "create: true") {
		t.Fatalf("err = %v, want a hint to declare create", err)
	}
	if len(fake.created) != 0 || len(fake.bindings) != 0 {
		t.Fatal("mutated before reporting the missing database")
	}
}

func TestManifestDatabaseCreateAcceptsPendingBindingOfNewDatabase(t *testing.T) {
	quietManifestCreate(t)
	manifestDatabaseReadyWait = 0
	dir := writeTestManifest(t, "databases:\n  - database: chronolake-db\n    create: true\n")
	fake := &creatingPostgresClient{binding: api.ManagedPostgresBinding{ID: "b", State: "provisioning"}}
	if err := deployManifestPostgresBindings(context.Background(), fake, "gateway", dir); err != nil {
		t.Fatalf("err = %v; a binding on a database this deploy creates may still be provisioning", err)
	}
	if len(fake.created) != 1 || fake.created[0].PostgresMajor != 18 {
		t.Fatalf("create requests %+v, want the newest offered major when none is declared", fake.created)
	}
}

// creatingBucketClient adds bucket creation to the manifest bucket client.
type creatingBucketClient struct {
	buckets  []api.ObjectBucket
	created  []api.CreateObjectBucketRequest
	bindings []api.CreateObjectStorageComputeBindingRequest
}

func (f *creatingBucketClient) ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error) {
	return api.ObjectBucketList{Items: f.buckets}, nil
}
func (f *creatingBucketClient) ListObjectStorageComputeBindings(context.Context, string, string) (api.ObjectStorageComputeBindingList, error) {
	return api.ObjectStorageComputeBindingList{}, nil
}
func (f *creatingBucketClient) CreateObjectStorageComputeBinding(_ context.Context, _, _ string, request api.CreateObjectStorageComputeBindingRequest) (api.ObjectStorageComputeBinding, error) {
	f.bindings = append(f.bindings, request)
	return api.ObjectStorageComputeBinding{ID: "binding", Prefix: request.Prefix}, nil
}
func (f *creatingBucketClient) CreateObjectBucket(_ context.Context, _ string, request api.CreateObjectBucketRequest) (api.ObjectBucket, error) {
	f.created = append(f.created, request)
	return api.ObjectBucket{ID: "bucket-" + request.Name, Name: request.Name, Scope: request.Scope, State: "ready"}, nil
}

func TestManifestBucketCreateCreatesThenBinds(t *testing.T) {
	quietManifestCreate(t)
	dir := writeTestManifest(t, `buckets:
  - bucket: segments
    create:
      region: us-east-1
  - bucket: exports
    create: true
`)
	fake := &creatingBucketClient{buckets: []api.ObjectBucket{{ID: "bucket-exports", Name: "exports", Scope: api.DefaultEnvScope, State: "ready"}}}
	if err := deployManifestObjectStorageBindings(context.Background(), fake, "store", dir); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 || fake.created[0].Name != "segments" || fake.created[0].Region != "us-east-1" || fake.created[0].Scope != api.DefaultEnvScope {
		t.Fatalf("created %+v, want only the missing segments bucket", fake.created)
	}
	if len(fake.bindings) != 2 {
		t.Fatalf("bindings %+v", fake.bindings)
	}
}

func TestManifestBucketWithoutCreateFailsBeforeMutation(t *testing.T) {
	quietManifestCreate(t)
	dir := writeTestManifest(t, "buckets:\n  - bucket: segments\n    create: true\n  - bucket: missing\n")
	fake := &creatingBucketClient{}
	err := deployManifestObjectStorageBindings(context.Background(), fake, "store", dir)
	if err == nil || !strings.Contains(err.Error(), "create: true") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.created) != 0 || len(fake.bindings) != 0 {
		t.Fatal("created a bucket before reporting the missing one")
	}
}
