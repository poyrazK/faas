package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRegionRouterCacheIsolationAndRemovedRoute(t *testing.T) {
	ctx := context.Background()
	legacy, _ := NewLocalStorageBackend(t.TempDir())
	eu, _ := NewLocalStorageBackend(t.TempDir())
	us, _ := NewLocalStorageBackend(t.TempDir())
	router, err := NewRegionRouter(map[string]StorageBackend{"eu-fsn": eu, "us-east": us}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(router.backend, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	router.backend = cache
	for key, body := range map[string]string{
		"apps/app/dep.ext4": "legacy", "regions/eu-fsn/apps/app/dep.ext4": "eu", "regions/us-east/apps/app/dep.ext4": "us",
	} {
		if err := router.Put(ctx, key, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
		reader, err := router.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil || string(got) != body {
			t.Fatalf("%s: %q %v", key, got, err)
		}
		resolved, relative, err := CacheBackendForKey(router, key)
		if err != nil || resolved != cache || relative != key {
			t.Fatalf("cache locator: %s %v", relative, err)
		}
	}
	if AsCacheBackend(router) != cache {
		t.Fatal("cache observer cannot find shared cache")
	}
	keys, err := ListArtifactKeys(ctx, router, "apps/")
	want := []string{"apps/app/dep.ext4", "regions/eu-fsn/apps/app/dep.ext4", "regions/us-east/apps/app/dep.ext4"}
	if err != nil || !reflect.DeepEqual(keys, want) {
		t.Fatalf("inventory=%v err=%v", keys, err)
	}
	if err := router.Delete(ctx, want[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := eu.Get(ctx, "apps/app/dep.ext4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("regional deletion: %v", err)
	}
	if _, err := router.Get(ctx, want[2]); err != nil {
		t.Fatalf("deletion crossed region: %v", err)
	}
	// Simulate a daemon restart with a route removed but its cache retained.
	removed, _ := NewRegionRouter(nil, legacy)
	removed.backend = cache
	if _, err := removed.Get(ctx, want[2]); !errors.Is(err, ErrRegionNotConfigured) {
		t.Fatalf("cached removed route: %v", err)
	}
	if _, _, _, err := removed.LocalPathWithSource(want[2]); !errors.Is(err, ErrRegionNotConfigured) {
		t.Fatalf("local path bypass: %v", err)
	}
	if _, _, err := CacheBackendForKey(removed, want[2]); !errors.Is(err, ErrRegionNotConfigured) {
		t.Fatalf("prefetch bypass: %v", err)
	}
}

func TestRegionRouterNeverFallsBackOnMissingOrUnavailableArtifact(t *testing.T) {
	ctx := context.Background()
	legacy, _ := NewLocalStorageBackend(t.TempDir())
	eu, _ := NewLocalStorageBackend(t.TempDir())
	const relative = "apps/app/dep.ext4"
	if err := legacy.Put(ctx, relative, strings.NewReader("wrong region")); err != nil {
		t.Fatal(err)
	}
	router, _ := NewRegionRouter(map[string]StorageBackend{"eu-fsn": eu}, legacy)
	if _, err := router.Get(ctx, "regions/eu-fsn/"+relative); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing regional object: %v", err)
	}
	for _, key := range []string{"regions/us-east/" + relative, "regions/eu-fsn/regions/us-east/" + relative, "regions/Eu-Fsn/" + relative, "regions/eu-fsn/../" + relative} {
		if err := router.Put(ctx, key, strings.NewReader("bad")); err == nil {
			t.Fatalf("accepted %s", key)
		}
		if _, err := router.Get(ctx, key); err == nil {
			t.Fatalf("served %s", key)
		}
		if err := router.Delete(ctx, key); err == nil {
			t.Fatalf("deleted %s", key)
		}
		if _, err := router.Exists(ctx, key); err == nil {
			t.Fatalf("probed %s", key)
		}
	}
	f := newFakeRegistry(t)
	remote := f.client(t)
	f.srv.Close()
	router, _ = NewRegionRouter(map[string]StorageBackend{"eu-fsn": remote}, legacy)
	if _, err := router.Get(ctx, "regions/eu-fsn/"+relative); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("outage became fallback/not-found: %v", err)
	}
}

func TestRegionalBackendFromEnvPublishesAndListsOCIStores(t *testing.T) {
	ctx := context.Background()
	eu, us := newFakeRegistry(t), newFakeRegistry(t)
	defer eu.srv.Close()
	defer us.srv.Close()
	root := t.TempDir()
	config, _ := json.Marshal(map[string]any{"regions": []RegionalStoreConfig{
		{ID: "eu-fsn", Backend: "oci", RegistryURL: eu.srv.URL},
		{ID: "us-east", Backend: "oci", RegistryURL: us.srv.URL},
	}})
	file := filepath.Join(root, "regions.json")
	if err := os.WriteFile(file, config, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_STORAGE_REGIONS_FILE", file)
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_STORAGE_ROOT", filepath.Join(root, "legacy"))
	t.Setenv("FAAS_APPS_ROOT", filepath.Join(root, "apps"))
	t.Setenv("FAAS_STORAGE_CACHE_DIR", filepath.Join(root, "cache"))
	t.Setenv("FAAS_STORAGE_SNAPSHOT_COMPRESSION", "zstd")
	backend, err := BackendFromEnvContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	backend.(*RegionRouter).routes["eu-fsn"].(*OCIRegistryStorageBackend).hc = eu.srv.Client()
	backend.(*RegionRouter).routes["us-east"].(*OCIRegistryStorageBackend).hc = us.srv.Client()
	if err := backend.(SnapshotRepositoryIndexer).ReconcileSnapshotRepositoryIndex(ctx, nil); err != nil {
		t.Fatal(err)
	}
	key := "regions/eu-fsn/snap/00000000-0000-0000-0000-000000002429/captures/00000000-0000-0000-0000-000000002430/v2/mem"
	if err := backend.Put(ctx, key, strings.NewReader("memory")); err != nil {
		t.Fatal(err)
	}
	keys, err := ListArtifactKeys(ctx, backend, "snap/")
	if err != nil || !reflect.DeepEqual(keys, []string{key}) {
		t.Fatalf("snapshot inventory=%v err=%v", keys, err)
	}
	// Read through a fresh cache to exercise the registry and decoding path.
	t.Setenv("FAAS_STORAGE_CACHE_DIR", filepath.Join(root, "fresh-cache"))
	fresh, err := BackendFromEnvContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fresh.(*RegionRouter).routes["eu-fsn"].(*OCIRegistryStorageBackend).hc = eu.srv.Client()
	fresh.(*RegionRouter).routes["us-east"].(*OCIRegistryStorageBackend).hc = us.srv.Client()
	reader, err := fresh.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(body) != "memory" {
		t.Fatalf("regional OCI read=%q err=%v", body, err)
	}
	if _, err := fresh.Get(ctx, strings.Replace(key, "eu-fsn", "us-east", 1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-region read: %v", err)
	}
	if err := fresh.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
}

func TestRegionalRoutesRejectInvalidConfig(t *testing.T) {
	for _, raw := range []string{
		`{"regions":[]}`, `{"regions":[{"id":"EU","backend":"oci","registry_url":"https://example.test"}]}`,
		`{"regions":[{"id":"eu","backend":"local"}]}`,
		`{"regions":[{"id":"eu","backend":"oci","registry_url":"https://example.test","typo":true}]}`,
		`{"regions":[{"id":"eu","backend":"oci","registry_url":"https://example.test"},{"id":"eu","backend":"oci","registry_url":"https://another.test"}]}`,
		`{"regions":[{"id":"eu","backend":"oci","registry_url":"https://example.test"},{"id":"us","backend":"oci","registry_url":"https://example.test/"}]}`,
		`{"regions":[{"id":"eu","backend":"oci","registry_url":"https://example.test","username_env":"MISSING_USER","password_env":"MISSING_PASSWORD"}]}`,
		`{"regions":[{"id":"eu","backend":"oci","registry_url":"https://example.test"}]} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "regions.json")
			if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAAS_STORAGE_REGIONS_FILE", file)
			if _, err := regionalRoutesFromEnv(t.Context()); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
}

func TestRegionRouterGCSBucketsKeepRuntimeArtifactsSeparate(t *testing.T) {
	ctx := t.Context()
	objects := newMemoryGCSStore()
	eu, err := newGCSStorageBackend("gregale-eu-runtime", snapshotCompressionZstd, objects)
	if err != nil {
		t.Fatal(err)
	}
	us, err := newGCSStorageBackend("gregale-us-runtime", snapshotCompressionZstd, objects)
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := NewLocalStorageBackend(t.TempDir())
	router, _ := NewRegionRouter(map[string]StorageBackend{"eu-fsn": eu, "us-east": us}, legacy)
	key := "regions/eu-fsn/snap/dep/captures/c/v2/mem"
	if err := router.Put(ctx, key, strings.NewReader("regional snapshot")); err != nil {
		t.Fatal(err)
	}
	reader, err := router.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(body) != "regional snapshot" {
		t.Fatalf("GCS read=%q err=%v", body, err)
	}
	if _, err := router.Get(ctx, strings.Replace(key, "eu-fsn", "us-east", 1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GCS crossed bucket: %v", err)
	}
	keys, err := ListArtifactKeys(ctx, router, "snap/")
	if err != nil || !reflect.DeepEqual(keys, []string{key}) {
		t.Fatalf("GCS inventory=%v err=%v", keys, err)
	}
	if err := router.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if len(objects.objects) != 0 {
		t.Fatal("regional GCS cleanup left an object")
	}
}
