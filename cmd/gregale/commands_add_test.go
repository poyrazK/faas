package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
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
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.Run([]string{"--json", "add", "postgres", "--app", "api", "--env", "production", "--region", "eu"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	var got main.AddPostgresResult
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

// addBucketTestServer fakes the app, bucket and compute-binding routes that
// `gregale add bucket assets --app api` uses, counting creations.
func addBucketTestServer(t *testing.T) (*httptest.Server, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	bucketCreates, bindingCreates := &atomic.Int64{}, &atomic.Int64{}
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
	t.Cleanup(srv.Close)
	return srv, bucketCreates, bindingCreates
}

func TestCmdAddBucketCreatesAndReusesBindingWithoutCredentialOutput(t *testing.T) {
	srv, bucketCreates, bindingCreates := addBucketTestServer(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	args := []string{"--json", "add", "bucket", "assets", "--app", "api", "--env", "production", "--region", "eu"}
	if code := main.Run(append([]string(nil), args...)); code != 0 {
		t.Fatalf("first add exit = %d, output = %s", code, out.String())
	}
	if strings.Contains(out.String(), "AKIA_TEST_SECRET") {
		t.Fatalf("first add output exposed access key: %s", out.String())
	}
	out.Reset()
	if code := main.Run(append([]string(nil), args...)); code != 0 {
		t.Fatalf("second add exit = %d, output = %s", code, out.String())
	}
	if bucketCreates.Load() != 1 || bindingCreates.Load() != 1 {
		t.Fatalf("resource add was not idempotent: bucket creates=%d binding creates=%d", bucketCreates.Load(), bindingCreates.Load())
	}
	if strings.Contains(out.String(), "AKIA_TEST_SECRET") || strings.Contains(out.String(), "SECRET_VALUE") {
		t.Fatalf("second add output exposed credential material: %s", out.String())
	}
}

// Hunt #9: running instances kept their boot environment after `add bucket`
// (the GREGALE_S3_* settings stayed absent until a restart) and nothing said so.
func TestCmdAddBucketSaysWhenTheNewBindingReachesRunningInstances(t *testing.T) {
	srv, _, _ := addBucketTestServer(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	args := []string{"add", "bucket", "assets", "--app", "api", "--env", "production", "--region", "eu"}
	if code := main.Run(append([]string(nil), args...)); code != 0 {
		t.Fatalf("first add exit = %d, output = %s", code, out.String())
	}
	if !strings.Contains(out.String(), "gregale app restart api") || !strings.Contains(out.String(), "GREGALE_S3_ASSETS_*") {
		t.Fatalf("new binding output lacks the restart guidance: %s", out.String())
	}
	out.Reset()
	if code := main.Run(append([]string(nil), args...)); code != 0 {
		t.Fatalf("second add exit = %d, output = %s", code, out.String())
	}
	if strings.Contains(out.String(), "restart") {
		t.Fatalf("reused binding repeated the restart guidance: %s", out.String())
	}
}

func TestFindAddPostgresDatabaseIgnoresDeletedAndRejectsAmbiguous(t *testing.T) {
	items := []api.ManagedPostgresDatabase{
		{ID: "deleted", Name: "orders", State: "deleted"},
		{ID: "ready", Name: "orders", State: "ready"},
	}
	got, found, err := main.FindAddPostgresDatabase(items, "orders")
	if err != nil || !found || got.ID != "ready" {
		t.Fatalf("find existing database = %+v, %v, %v", got, found, err)
	}
	items = append(items, api.ManagedPostgresDatabase{ID: "other", Name: "orders", State: "ready"})
	if _, _, err := main.FindAddPostgresDatabase(items, "orders"); err == nil {
		t.Fatal("ambiguous database name unexpectedly resolved")
	}
}

// TestCmdAddPostgresAdoptsWorkCommittedBehindAnUncertainError pins H9-P109:
// on production-us the first `add postgres` failed with 503 after 28 s while
// the database and binding had committed; the identical rerun succeeded.
// A server-side failure must re-read the catalog and adopt the same-name
// database and same-target binding instead of failing.
func TestCmdAddPostgresAdoptsWorkCommittedBehindAnUncertainError(t *testing.T) {
	var created, bound atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		unavailable := func() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":503,"code":"managed_postgres_unavailable","title":"Managed PostgreSQL unavailable"}`))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api","egress_ports":[5432]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			if created.Load() {
				_, _ = w.Write([]byte(`{"items":[{"id":"db-1","name":"api-postgres","state":"ready"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[]}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases":
			created.Store(true)
			unavailable()
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/db-1":
			_, _ = w.Write([]byte(`{"id":"db-1","name":"api-postgres","state":"ready"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			bound.Store(true)
			unavailable()
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			if bound.Load() {
				_, _ = w.Write([]byte(`{"items":[{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":1,"state":"ready"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.Run([]string{"--json", "add", "postgres", "--app", "api", "--env", "production", "--region", "eu"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	var got main.AddPostgresResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.Database.ID != "db-1" || got.Binding.ID != "binding-1" || !got.DatabaseCreated {
		t.Fatalf("adopted result = %+v", got)
	}
}
