// adr: 1384 — live-query enablement and the shown-once live secret.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdPostgresLive(t *testing.T) {
	var calls []string
	stored := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"database","name":"orders","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/database":
			_, _ = w.Write([]byte(`{"id":"database","name":"orders","state":"ready"}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/apps/web/secrets/"):
			var body api.PutAppSecretRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			stored[strings.TrimPrefix(r.URL.Path, "/v1/apps/web/secrets/")] = body.Value
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/postgres/databases/database/live/rotate":
			var body api.RotateManagedPostgresLiveSecretRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.OverlapSeconds == nil || *body.OverlapSeconds != 3600 {
				t.Error("rotation overlap", body, err)
			}
			_, _ = w.Write([]byte(`{"database_id":"database","enabled":true,"secret":"glsk1_rotated","keys":[{"key_id":"bbbbbbbbbbbbbbbb","active":true,"created_at":"2026-10-17T00:00:00Z"},{"key_id":"aaaaaaaaaaaaaaaa","active":false,"created_at":"2026-10-16T00:00:00Z","retires_at":"2026-10-17T01:00:00Z"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/database/live":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"database_id":"database","enabled":true,"secret":"glsk1_first","keys":[{"key_id":"aaaaaaaaaaaaaaaa","active":true,"created_at":"2026-10-16T00:00:00Z"}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/postgres/databases/database/live":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	priorOut, priorJSON := osStdout, jsonOutput
	t.Cleanup(func() { osStdout, jsonOutput = priorOut, priorJSON })
	var out bytes.Buffer
	osStdout, jsonOutput = &out, false

	// Without --app the secret is printed once.
	if code := cmdPostgres([]string{"live", "enable", "orders"}); code != 0 {
		t.Fatal("enable", code)
	}
	if !strings.Contains(out.String(), "glsk1_first") || !strings.Contains(out.String(), "enabled") {
		t.Fatalf("enable output = %q", out.String())
	}

	// With --app it is stored, not printed.
	out.Reset()
	if code := cmdPostgres([]string{"live", "rotate", "orders", "--overlap", "1h", "--app", "web"}); code != 0 {
		t.Fatal("rotate", code)
	}
	if stored["GREGALE_LIVE_SECRET"] != "glsk1_rotated" || strings.Contains(out.String(), "glsk1_rotated") {
		t.Fatalf("rotate stored %v, printed %q", stored, out.String())
	}
	if !strings.Contains(out.String(), "retiring at 2026-10-17T01:00:00Z") {
		t.Fatalf("rotate output = %q", out.String())
	}

	// JSON output drops a secret that was stored.
	out.Reset()
	jsonOutput = true
	if code := cmdPostgres([]string{"live", "rotate", "orders", "--overlap", "1h", "--app", "web", "--env", "LIVE_KEY"}); code != 0 {
		t.Fatal("rotate --json", code)
	}
	if stored["LIVE_KEY"] != "glsk1_rotated" || strings.Contains(out.String(), "glsk1_") {
		t.Fatalf("json rotate printed %q", out.String())
	}
	jsonOutput = false

	out.Reset()
	if code := cmdPostgres([]string{"live", "disable", "orders"}); code != 0 || !strings.Contains(out.String(), "disabled") {
		t.Fatal("disable", code, out.String())
	}
	if code := cmdPostgres([]string{"live", "enable", "orders", "--overlap", "1h"}); code == 0 {
		t.Fatal("enable accepted --overlap")
	}
	if code := cmdPostgres([]string{"live", "rotate", "orders", "--overlap", "200h"}); code == 0 {
		t.Fatal("rotate accepted an overlap past the limit")
	}
}
