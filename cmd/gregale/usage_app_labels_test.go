package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUsageTablesShowAppSlugs — `usage daily` and `usage storage` printed bare
// app UUIDs on production-us. Known apps now print their slug. An unknown ID
// (a deleted app) keeps the ID.
func TestUsageTablesShowAppSlugs(t *testing.T) {
	resetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps":
			_, _ = w.Write([]byte(`[{"id":"11111111-1111-4111-8111-111111111111","slug":"h3-rollout","type":"app"}]`))
		case "/v1/usage/daily":
			_, _ = w.Write([]byte(`{"items":[{"app_id":"11111111-1111-4111-8111-111111111111","day":"2026-10-04","mb_seconds":3600000,"requests":7},{"app_id":"22222222-2222-4222-8222-222222222222","day":"2026-10-04","mb_seconds":0,"requests":1}]}`))
		case "/v1/usage/storage":
			_, _ = w.Write([]byte(`{"items":[{"app_id":"11111111-1111-4111-8111-111111111111","day":"2026-10-04","snapshot_bytes":1048576,"layer_bytes":0}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdUsageDaily([]string{"--day", "2026-10-04"}); code != 0 {
		t.Fatalf("usage daily exit = %d", code)
	}
	if code := cmdUsageStorage([]string{"--day", "2026-10-04"}); code != 0 {
		t.Fatalf("usage storage exit = %d", code)
	}
	got := stdout.String()
	if strings.Count(got, "h3-rollout") != 2 || strings.Contains(got, "11111111-1111-4111-8111-111111111111") ||
		!strings.Contains(got, "22222222-2222-4222-8222-222222222222") {
		t.Fatalf("usage tables:\n%s", got)
	}
}
