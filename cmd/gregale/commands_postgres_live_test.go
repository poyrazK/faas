package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestPostgresRotateAdoptsACommittedRotation pins the 2026-10-14 live test:
// `bindings rotate --wait` failed "Could not reach Gregale" after 26.6 s while
// generation 2 had committed and the app had already switched roles.
func TestPostgresRotateAdoptsACommittedRotation(t *testing.T) {
	var rotated atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/bindings/binding-1":
			gen := "1"
			if rotated.Load() {
				gen = "2"
			}
			_, _ = w.Write([]byte(`{"id":"binding-1","environment_key":"DATABASE_URL","access":"read_write","credential_generation":` + gen + `,"state":"ready"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/bindings/binding-1/rotate":
			rotated.Store(true)
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"status":504,"code":"gateway_timeout","title":"Gateway timeout"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previous := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = previous })
	if code := main.CmdPostgresBindingsRotate([]string{"binding-1"}); code != 0 {
		t.Fatalf("exit = %d, output %s", code, out.String())
	}
	if !strings.Contains(out.String(), "credential_generation: 2") {
		t.Fatalf("adopted rotation not shown: %s", out.String())
	}
}

// TestPostgresRotateSaysWhenItJoinedARotation: a second rotate during a
// pending rotation returned the same generation with nothing saying so.
func TestPostgresRotateSaysWhenItJoinedARotation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"binding-1","environment_key":"DATABASE_URL","access":"read_write","credential_generation":2,"rotation_pending":true,"state":"ready"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var stderr bytes.Buffer
	previousOut, previousErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &bytes.Buffer{}, &stderr
	t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr) = previousOut, previousErr })
	if code := main.CmdPostgresBindingsRotate([]string{"binding-1"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "already in progress (generation 2)") {
		t.Fatalf("joined rotation not reported: %q", stderr.String())
	}
}

// TestDataAPICreateChecksTheBackendFirst pins H9-P119: data-api create made
// an app before learning the backend offers no data_api credentials.
func TestDataAPICreateChecksTheBackendFirst(t *testing.T) {
	var appCreates atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"db-1","name":"orders","region":"us-east-2","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/capabilities":
			_, _ = w.Write([]byte(`{"region":"us-east-2","database_limit":3,"credential_access":["migration","read_only","read_write"]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
			appCreates.Add(1)
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"notes","type":"app"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var stderr bytes.Buffer
	previous := (*main.OsStderr)
	(*main.OsStderr) = &stderr
	t.Cleanup(func() { (*main.OsStderr) = previous })
	if code := main.CmdDataAPICreate([]string{"notes", "--database", "orders", "--issuer", "https://issuer.example", "--jwks-url", "https://issuer.example/jwks.json", "--audience", "notes"}); code == 0 {
		t.Fatal("data-api create succeeded without data_api access")
	}
	if appCreates.Load() != 0 {
		t.Fatalf("app created before the capability check: %d", appCreates.Load())
	}
	if !strings.Contains(stderr.String(), "does not offer data_api") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
