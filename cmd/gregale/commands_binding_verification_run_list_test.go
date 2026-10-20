package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBindingVerificationRunListCLIReadOnly(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, plain := range []bool{false, true} {
			t.Run(map[bool]string{true: "graph", false: "app"}[graph]+map[bool]string{true: "plain", false: "json"}[plain], func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/verification-runs") {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					}
					q := r.URL.Query()
					if q.Get("limit") != "2" || q.Get("cursor") != "saved-cursor" || q.Get("status") != "pending" {
						t.Errorf("filters lost: %s", r.URL.RawQuery)
					}
					if !graph && (q.Get("scope") != "production" || q.Get("include_members") != "true") {
						t.Error("app filters lost")
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(api.BindingVerificationRunList{ObservedAt: time.Now(), NextCursor: "next-page", Runs: []api.BindingVerificationRunSummary{{ID: "11111111-1111-4111-8111-111111111111", Status: "pending", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}}})
				}))
				defer server.Close()
				args := []string{"bindings", "verification-runs", "list", "api"}
				if graph {
					args = []string{"projects", "environments", "release-sets", "verification-runs", "list", "project", "production"}
				} else {
					args = append(args, "--scope", "production", "--include-members")
				}
				args = append(args, "--limit", "2", "--cursor", "saved-cursor", "--status", "pending")
				if !plain {
					args = append(args, "--json")
				}
				code, out, stderr := main.CaptureBindingCLI(t, server.URL, args...)
				if code != 0 || calls != 1 || !strings.Contains(out, "next-page") || !strings.Contains(out, "11111111-1111-4111-8111-111111111111") {
					t.Fatalf("listing failed %d %d %s %s", code, calls, out, stderr)
				}
			})
		}
	}
}
