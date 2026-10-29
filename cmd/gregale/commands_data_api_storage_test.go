// adr: 20261021101510 — data-api storage prints the migration, binds a bucket
// and turns storage on.
package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataAPIStorageSQLRendersTheSchema(t *testing.T) {
	sql, err := main.DataAPIStorageSQL("catalog", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CREATE TABLE catalog.storage_objects", "ENABLE ROW LEVEL SECURITY", "CREATE TRIGGER storage_objects_guard", "gregale_storage", "object_key text CHECK"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %q", want)
		}
	}
	upgrade, err := main.DataAPIStorageSQL("catalog", true)
	if err != nil || !strings.Contains(upgrade, "ALTER TABLE catalog.storage_objects ADD COLUMN IF NOT EXISTS object_key text") || !strings.Contains(upgrade, "SET LOCAL lock_timeout") {
		t.Fatalf("upgrade SQL: %v\n%s", err, upgrade)
	}
	for _, rendered := range []string{sql, upgrade} {
		if strings.Contains(rendered, "{{schema}}") {
			t.Fatal("unrendered placeholder")
		}
	}
	for _, bad := range []string{"", "API", "api;drop", "1api"} {
		if _, err := main.DataAPIStorageSQL(bad, true); err == nil {
			t.Fatalf("accepted schema %q", bad)
		}
	}
	for bucket, want := range map[string]string{"uploads": "uploads", "my-files": "my_files", "2024-files": ""} {
		name, ok := main.DataAPIStorageName(bucket)
		if ok != (want != "") || (ok && name != want) {
			t.Fatalf("%s: %q %v", bucket, name, ok)
		}
	}
}

// storageAPI fakes the bucket, binding, secret and restart routes.
type storageAPI struct {
	mu         sync.Mutex
	bucket     *api.ObjectBucket
	binding    *api.ObjectStorageComputeBinding
	secrets    []api.AppSecretResponse
	writes     []string
	bindingReq api.CreateObjectStorageComputeBindingRequest
}

func newStorageAPI(t *testing.T, s *storageAPI) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet {
			s.writes = append(s.writes, r.Method+" "+r.URL.Path)
		}
		switch {
		case r.URL.Path == "/v1/apps/shop-data/buckets" && r.Method == http.MethodPost:
			s.bucket = &api.ObjectBucket{ID: "bkt-1", Name: "user-files", Scope: "default", State: "ready"}
			_ = json.NewEncoder(w).Encode(s.bucket)
		case r.URL.Path == "/v1/apps/shop-data/buckets":
			list := api.ObjectBucketList{}
			if s.bucket != nil {
				list.Items = []api.ObjectBucket{*s.bucket}
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.URL.Path == "/v1/apps/shop-data/buckets/bkt-1/compute-bindings" && r.Method == http.MethodPost:
			_ = json.Unmarshal(body, &s.bindingReq)
			s.binding = &api.ObjectStorageComputeBinding{ID: "bind-1", BucketID: "bkt-1", Scope: "default", Prefix: s.bindingReq.Prefix}
			s.binding.Credential.Permission = s.bindingReq.Permission
			_ = json.NewEncoder(w).Encode(s.binding)
		case r.URL.Path == "/v1/apps/shop-data/buckets/bkt-1/compute-bindings":
			list := api.ObjectStorageComputeBindingList{}
			if s.binding != nil {
				list.Items = []api.ObjectStorageComputeBinding{*s.binding}
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.URL.Path == "/v1/apps/shop-data/secrets":
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: s.secrets})
		case strings.HasPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/"):
			key := strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-data/secrets/")
			if r.Method == http.MethodPut {
				s.secrets = append(s.secrets, api.AppSecretResponse{Key: key, Scope: "default"})
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "restart"):
			_, _ = w.Write([]byte(`{"wake_id":"wake-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_API", server.URL)
}

func TestDataAPIStorageEnableBindsTheBucketReadWriteAndTurnsStorageOn(t *testing.T) {
	main.ResetJSONOut(t)
	fake := &storageAPI{}
	newStorageAPI(t, fake)
	stdout, stderr, restore := main.SwapIO(t)
	code := main.CmdDataAPIStorage([]string{"enable", "shop-data", "--bucket", "user-files", "--max-object-bytes", "1048576"})
	errors := stderr()
	restore()
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout.String(), errors)
	}
	want := "POST /v1/apps/shop-data/buckets|POST /v1/apps/shop-data/buckets/bkt-1/compute-bindings|PUT /v1/apps/shop-data/secrets/DATA_API_STORAGE|PUT /v1/apps/shop-data/secrets/DATA_API_STORAGE_MAX_OBJECT_BYTES|POST /v1/apps/shop-data/restart"
	if got := strings.Join(fake.writes, "|"); got != want {
		t.Fatalf("writes %s", got)
	}
	if fake.bindingReq.Permission != api.ObjectBucketPermissionReadWrite || fake.bindingReq.Prefix != "GREGALE_S3_USER_FILES" {
		t.Fatalf("binding %+v", fake.bindingReq)
	}
	if !strings.Contains(stdout.String(), `"user_files"`) || !strings.Contains(stdout.String(), "storage_unavailable") {
		t.Fatalf("output %s", stdout.String())
	}

	fake.writes = nil
	stdout, _, restore = main.SwapIO(t)
	code = main.CmdDataAPIStorage([]string{"status", "shop-data"})
	restore()
	if code != 0 || !strings.Contains(stdout.String(), "storage  on") || !strings.Contains(stdout.String(), `user-files as "user_files" (read_write)`) || len(fake.writes) != 0 {
		t.Fatalf("status %d: %s (writes %v)", code, stdout.String(), fake.writes)
	}

	fake.binding.Credential.Permission = api.ObjectBucketPermissionRead
	_, stderr, restore = main.SwapIO(t)
	code = main.CmdDataAPIStorage([]string{"enable", "shop-data", "--bucket", "user-files", "--no-restart"})
	refusal := stderr()
	restore()
	if code == 0 || !strings.Contains(refusal, "needs read_write") {
		t.Fatalf("a read-only binding was accepted: %d %s", code, refusal)
	}
	for _, args := range [][]string{
		{"enable", "shop-data"},
		{"enable", "shop-data", "--bucket", "2024-files"},
		{"enable", "shop-data", "--bucket", "uploads", "--max-object-bytes", "104857601"},
	} {
		_, _, restore = main.SwapIO(t)
		code = main.CmdDataAPIStorage(args)
		restore()
		if code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestDataAPIStorageDisableUnsetsTheSwitchAndKeepsBuckets(t *testing.T) {
	main.ResetJSONOut(t)
	fake := &storageAPI{}
	newStorageAPI(t, fake)
	_, _, restore := main.SwapIO(t)
	code := main.CmdDataAPIStorage([]string{"disable", "shop-data", "--no-restart"})
	restore()
	if code != 0 || strings.Join(fake.writes, "|") != "DELETE /v1/apps/shop-data/secrets/DATA_API_STORAGE_MAX_OBJECT_BYTES|DELETE /v1/apps/shop-data/secrets/DATA_API_STORAGE" {
		t.Fatalf("disable %d: %v", code, fake.writes)
	}
}
