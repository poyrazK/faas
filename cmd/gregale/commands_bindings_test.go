package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCmdBindingsJSONCombinesAndSanitizesExistingBindings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api","service_bindings":[{"binding":"GREGALE_SERVICE_BILLING_URL","service":"billing"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"db-1","name":"primary"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			_, _ = w.Write([]byte(`{"items":[{"id":"pg-binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","state":"ready"},{"id":"pg-binding-2","database_id":"db-1","app_id":"another-app","scope":"production","environment_key":"DATABASE_URL","access":"read_only","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets":
			_, _ = w.Write([]byte(`{"items":[{"id":"bucket-1","name":"assets","scope":"production","region":"eu","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets/bucket-1/compute-bindings":
			_, _ = w.Write([]byte(`{"items":[{"id":"bucket-binding-1","bucket_id":"bucket-1","scope":"production","prefix":"GREGALE_S3_ASSETS","credential":{"id":"credential-1","access_key_id":"AKIA_PRIVATE_VALUE","label":"compute","permission":"read_write","status":"active"},"secret_keys":{"access_key_id":"GREGALE_S3_ASSETS_ACCESS_KEY_ID","secret_access_key":"GREGALE_S3_ASSETS_SECRET_ACCESS_KEY"}}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/queue-bindings":
			_, _ = w.Write([]byte(`[{"id":"queue-binding-1","app_id":"app-1","name":"email-worker","queue_name":"email","mode":"push","workload_class":"worker","enabled":true}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")

	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := run([]string{"bindings", "api", "--json"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	var got appBindingInventory
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	want := appBindingInventory{
		App: "api",
		Bindings: []appBindingInventoryItem{
			{Type: bindingTypeObjectStorage, Name: "assets", Binding: "GREGALE_S3_ASSETS", Scope: "production", Access: "read_write", State: "active"},
			{Type: bindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production", Access: "read_write", State: "ready"},
			{Type: bindingTypeQueue, Name: "email", Binding: "email-worker", Scope: "app", Access: "push", State: "active"},
			{Type: bindingTypeService, Name: "billing", Binding: "GREGALE_SERVICE_BILLING_URL", Scope: "app", Access: "invoke", State: "declared"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inventory = %#v, want %#v", got, want)
	}
	for _, sensitive := range []string{
		"AKIA_PRIVATE_VALUE",
		"credential-1",
		"GREGALE_S3_ASSETS_ACCESS_KEY_ID",
		"GREGALE_S3_ASSETS_SECRET_ACCESS_KEY",
		"pg-binding-1",
		"queue-binding-1",
	} {
		if strings.Contains(out.String(), sensitive) {
			t.Fatalf("output exposed %q: %s", sensitive, out.String())
		}
	}
}

func TestCmdBindingsJSONUsesAnEmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case "/v1/postgres/databases", "/v1/apps/api/buckets":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/v1/apps/api/queue-bindings":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")

	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := run([]string{"--json", "bindings", "api"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if got, want := strings.TrimSpace(out.String()), `{
  "app": "api",
  "bindings": []
}`; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRenderAppBindingInventory(t *testing.T) {
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	renderAppBindingInventory(appBindingInventory{
		App: "api",
		Bindings: []appBindingInventoryItem{
			{Type: bindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production", Access: "read_write", State: "ready"},
			{Type: bindingTypeQueue, Name: "jobs", Binding: "worker", Scope: "app", Access: "push", State: "active"},
		},
	})

	for _, want := range []string{"TYPE", "NAME", "BINDING", "SCOPE", "ACCESS", "STATE", "postgres", "DATABASE_URL", "queue", "worker"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}
