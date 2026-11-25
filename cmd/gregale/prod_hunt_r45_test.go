package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Prod hunt r45: a webhook aimed at the caller's own key-protected app died
// with 401 on every delivery and create said nothing.
func TestWebhookCreateWarnsOwnAppNeedsKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		authn bool
		want  bool
	}{{"key required", true, true}, {"open app", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/webhooks"):
					_, _ = w.Write([]byte(`{"id":"0fc703ad-0fdc-4619-97fe-a6b8f20a54e1","target_url":"https://demo.gregale.app/hook","enabled":true}`))
				case r.URL.Path == "/v1/apps/demo":
					authn := "false"
					if tc.authn {
						authn = "true"
					}
					_, _ = w.Write([]byte(`{"id":"a","slug":"demo","url":"https://demo.gregale.app","require_authn":` + authn + `}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"title":"not found","status":404,"code":"not_found"}`))
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			code, stdout, stderr := runCLI(t, "webhook", "create", "--app", "demo", "--target-url", "https://demo.gregale.app/hook")
			if code != 0 {
				t.Fatalf("webhook create = %d %q %q", code, stdout, stderr)
			}
			if got := strings.Contains(stderr, "--no-require-authn"); got != tc.want {
				t.Fatalf("warning shown = %v, want %v: %q", got, tc.want, stderr)
			}
		})
	}
}

// Prod hunt r45: `audit list --since 5m` refused a duration although
// `logs --since 5m` takes one.
func TestAuditListSinceDuration(t *testing.T) {
	var since, appID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/demo" {
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"demo"}`))
			return
		}
		since, appID = r.URL.Query().Get("since"), r.URL.Query().Get("app_id")
		_, _ = w.Write([]byte(`{"events":[]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	code, stdout, stderr := runCLI(t, "audit", "list", "demo", "--since", "1h")
	if code != 0 {
		t.Fatalf("audit list = %d %q %q", code, stdout, stderr)
	}
	at, err := time.Parse(time.RFC3339, since)
	if err != nil || time.Since(at) < 59*time.Minute || time.Since(at) > 61*time.Minute {
		t.Fatalf("since sent as %q, want about an hour ago", since)
	}
	if appID != "app-1" {
		t.Fatalf("app_id sent as %q, want app-1", appID)
	}
}

// Prod hunt r45: a read-only key from a coding agent was warned that it could
// delete, deploy and change billing without asking.
func TestReadOnlyKeySkipsAgentWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"k","label":"ro","plaintext":"fp_live_x","scopes":["apps:read"],"status":"active"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("CLAUDECODE", "1")
	_, _, stderr := runCLI(t, "key", "create", "ro", "--scopes", "apps:read")
	if strings.Contains(stderr, "without the agent scope") {
		t.Fatalf("read-only key got the agent warning: %q", stderr)
	}
	_, _, stderr = runCLI(t, "key", "create", "rw", "--scopes", "apps:read,deploy:write")
	if !strings.Contains(stderr, "without the agent scope") {
		t.Fatalf("write key lost the agent warning: %q", stderr)
	}
}
