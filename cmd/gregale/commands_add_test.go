package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdAddPostgresCreatesAndBindsWithoutSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("database create missing idempotency key")
			}
			var req api.CreateManagedPostgresDatabaseRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode database request: %v", err)
			}
			if req.Name != "api-postgres" || req.Region != "eu" {
				t.Fatalf("database request = %+v", req)
			}
			_, _ = w.Write([]byte(`{"id":"db-1","name":"api-postgres","region":"eu","postgres_major":16,"service_class":"development","availability":"single_zone","scale_to_zero":true,"state":"ready"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("binding create missing idempotency key")
			}
			var req api.CreateManagedPostgresBindingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode binding request: %v", err)
			}
			if req.AppID != "app-1" || req.Scope != "production" || req.EnvironmentKey != "DATABASE_URL" {
				t.Fatalf("binding request = %+v", req)
			}
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":1,"state":"ready"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })

	if code := run([]string{"--json", "add", "postgres", "--app", "api", "--env", "production", "--region", "eu"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	var got addPostgresResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.AppID != "app-1" || got.Database.Name != "api-postgres" || got.Binding.EnvironmentKey != "DATABASE_URL" || !got.DatabaseCreated {
		t.Fatalf("unexpected output: %+v", got)
	}
	if strings.Contains(out.String(), "postgres://") || strings.Contains(out.String(), "password") || strings.Contains(out.String(), "secret") {
		t.Fatalf("resource add output exposed credential material: %s", out.String())
	}
}

func TestCmdAddBucketCreatesAndReusesBindingWithoutCredentialOutput(t *testing.T) {
	var bucketCreates, bindingCreates atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets":
			if bucketCreates.Load() == 0 {
				_, _ = w.Write([]byte(`{"items":[]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"id":"bucket-1","name":"assets","scope":"production","region":"eu","state":"ready"}]}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/buckets":
			bucketCreates.Add(1)
			var req api.CreateObjectBucketRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode bucket request: %v", err)
			}
			if req.Name != "assets" || req.Scope != "production" || req.Region != "eu" {
				t.Fatalf("bucket request = %+v", req)
			}
			_, _ = w.Write([]byte(`{"id":"bucket-1","name":"assets","scope":"production","region":"eu","state":"ready"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets/bucket-1/compute-bindings":
			if bindingCreates.Load() == 0 {
				_, _ = w.Write([]byte(`{"items":[]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"id":"binding-1","bucket_id":"bucket-1","scope":"production","prefix":"GREGALE_S3_ASSETS","credential":{"access_key_id":"AKIA_TEST_SECRET","permission":"read_write"},"secret_keys":{"secret_access_key":"GREGALE_S3_ASSETS_SECRET_ACCESS_KEY"}}]}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/buckets/bucket-1/compute-bindings":
			bindingCreates.Add(1)
			var req api.CreateObjectStorageComputeBindingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode binding request: %v", err)
			}
			if req.Permission != api.ObjectBucketPermissionReadWrite || req.Prefix != "GREGALE_S3_ASSETS" {
				t.Fatalf("binding request = %+v", req)
			}
			_, _ = w.Write([]byte(`{"id":"binding-1","bucket_id":"bucket-1","scope":"production","prefix":"GREGALE_S3_ASSETS","credential":{"access_key_id":"AKIA_TEST_SECRET","permission":"read_write"},"secret_keys":{"secret_access_key":"GREGALE_S3_ASSETS_SECRET_ACCESS_KEY"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })

	args := []string{"--json", "add", "bucket", "assets", "--app", "api", "--env", "production", "--region", "eu"}
	if code := run(append([]string(nil), args...)); code != 0 {
		t.Fatalf("first add exit = %d, output = %s", code, out.String())
	}
	if strings.Contains(out.String(), "AKIA_TEST_SECRET") {
		t.Fatalf("first add output exposed access key: %s", out.String())
	}
	out.Reset()
	if code := run(append([]string(nil), args...)); code != 0 {
		t.Fatalf("second add exit = %d, output = %s", code, out.String())
	}
	if bucketCreates.Load() != 1 || bindingCreates.Load() != 1 {
		t.Fatalf("resource add was not idempotent: bucket creates=%d binding creates=%d", bucketCreates.Load(), bindingCreates.Load())
	}
	if strings.Contains(out.String(), "AKIA_TEST_SECRET") || strings.Contains(out.String(), "SECRET_VALUE") {
		t.Fatalf("second add output exposed credential material: %s", out.String())
	}
}

func TestFindAddPostgresDatabaseIgnoresDeletedAndRejectsAmbiguous(t *testing.T) {
	items := []api.ManagedPostgresDatabase{
		{ID: "deleted", Name: "orders", State: "deleted"},
		{ID: "ready", Name: "orders", State: "ready"},
	}
	got, found, err := findAddPostgresDatabase(items, "orders")
	if err != nil || !found || got.ID != "ready" {
		t.Fatalf("find existing database = %+v, %v, %v", got, found, err)
	}
	items = append(items, api.ManagedPostgresDatabase{ID: "other", Name: "orders", State: "ready"})
	if _, _, err := findAddPostgresDatabase(items, "orders"); err == nil {
		t.Fatal("ambiguous database name unexpectedly resolved")
	}
}
