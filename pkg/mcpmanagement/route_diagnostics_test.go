package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRouteDiagnosticsMCP(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			reads := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+testToken {
					t.Error("diagnosis mutated state or lost credentials")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/alpha":
					_, _ = w.Write([]byte(`{"id":"app-one","slug":"alpha"}`))
				case "/v1/apps/alpha/routes/awake", "/v1/apps/alpha/routes/advice":
					reads++
					if r.URL.Query().Get("scope") != "default" || r.URL.Query().Has("environment") {
						t.Error("lost explicit default scope")
					}
					if r.URL.Query().Get("since") != "7d" || r.URL.Query().Get("until") != "2026-10-15T00:00:00Z" {
						t.Error("lost diagnostic window")
					}
					if strings.HasSuffix(r.URL.Path, "awake") {
						_, _ = w.Write([]byte(`{"app_id":"app-one","scope":"default","coverage":"retained_deployment_history","partial":true,"slug":"alpha","window_clamped":true,"awake_seconds":600,"unattributed_seconds":90,"routes":[{"method":"GET","route":"/poll","wakes":3,"pattern":"periodic","if_removed":{"awake_seconds_saved":120}}]}`))
					} else {
						if r.URL.Query().Get("cache_max_age") != "300" {
							t.Error("lost cache what-if lifetime")
						}
						_, _ = w.Write([]byte(`{"app_id":"app-one","scope":"default","coverage":"retained_deployment_history","partial":true,"cacheability_coverage":"deployment_isolated_observations_only","slug":"alpha","routes_analyzed":1,"suggestions":[{"kind":"cache","method":"GET","route":"/poll","evidence":{"content_observed":30,"content_repeats":29,"content_changes":1,"safe_max_age_seconds":60}}]}`))
					}
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer backend.Close()
			cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowedApps: map[string]string{"alpha": "app-one"}, Environments: &EnvironmentConsent{}}, version)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range catalog.Tools {
				if tool.Name == "gregale_get_route_awake" || tool.Name == "gregale_get_route_advice" {
					if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || *tool.Annotations.DestructiveHint {
						t.Fatal("diagnostic tool lacks read-only annotations")
					}
				}
			}
			for _, tool := range []string{"gregale_get_route_awake", "gregale_get_route_advice"} {
				args := map[string]any{"app": "alpha", "since": "7d", "until": "2026-10-15T00:00:00Z"}
				if tool == "gregale_get_route_advice" {
					args["cache_max_age_seconds"] = 300
				}
				out := callTool(t, ctx, cs, tool, args, false)
				if string(out["coverage"]) != `"retained_deployment_history"` || string(out["estimates_only"]) != "true" {
					t.Fatalf("lost coverage: %s", out)
				}
				if !strings.Contains(string(out["report"]), map[string]string{"gregale_get_route_awake": "unattributed_seconds", "gregale_get_route_advice": "content_repeats"}[tool]) {
					t.Fatal("lost evidence")
				}
			}
			if reads != 2 {
				t.Fatalf("report reads=%d", reads)
			}
			prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_awake_diagnosis", Arguments: map[string]string{"app": "alpha"}})
			if err != nil {
				t.Fatal(err)
			}
			text := prompt.Messages[0].Content.(*mcp.TextContent).Text
			for _, want := range []string{"gregale_get_route_awake", "gregale_get_route_advice", "retained deployment-attributed history", "Do not apply changes", "untrusted data"} {
				if !strings.Contains(text, want) {
					t.Fatalf("prompt missing %s", want)
				}
			}
			if reads != 2 {
				t.Fatal("retrieving prompt fetched app-wide telemetry")
			}
			if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_awake_diagnosis", Arguments: map[string]string{"app": "alpha", "deployment_id": "dep-one"}}); err == nil {
				t.Fatal("awake prompt accepted unrelated deployment")
			}
			fields, err := completionFields(&mcp.CompleteReference{Type: "ref/prompt", Name: "gregale_awake_diagnosis"})
			if err != nil || !fields["app"] || fields["deployment_id"] {
				t.Fatalf("prompt completion fields: %v %v", fields, err)
			}
		})
	}
}

func TestRouteDiagnosticsRejectBeforeReportRead(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/alpha" {
			t.Errorf("rejected call reached backend: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"id":"app-one","slug":"alpha"}`))
	}))
	defer backend.Close()
	cs, ctx := connectTestServer(t, backend, 0, "")
	for _, args := range []map[string]any{
		{"app": "alpha", "since": "-1h"}, {"app": "alpha", "since": "99999999999999999999d"},
		{"app": "alpha", "until": "tomorrow"}, {"app": "alpha", "project": "shop"},
		{"app": "alpha", "environment": "staging"}, {"app": "../alpha"},
	} {
		callTool(t, ctx, cs, "gregale_get_route_awake", args, true)
	}
	for _, age := range []int{-1, 3601} {
		callTool(t, ctx, cs, "gregale_get_route_advice", map[string]any{"app": "alpha", "cache_max_age_seconds": age}, true)
	}
	denied, dctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowedApps: map[string]string{}}, "")
	callTool(t, dctx, denied, "gregale_get_route_awake", map[string]any{"app": "alpha"}, true)
}

func TestRouteDiagnosticsDoNotInventEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		recreate   bool
	}{
		{name: "legacy_aggregate", body: `{"slug":"alpha","routes":[]}`, status: 200},
		{name: "wrong_scope", body: `{"app_id":"app-one","slug":"alpha","scope":"production","coverage":"retained_deployment_history","partial":true,"routes":[]}`, status: 200},
		{name: "wrong_app", body: `{"slug":"other","routes":[]}`, status: 200},
		{name: "missing_telemetry", body: `{"type":"capacity","title":"telemetry unavailable","status":503}`, status: 503},
		{name: "recreated_app", body: `{"app_id":"app-one","scope":"default","coverage":"retained_deployment_history","partial":true,"slug":"alpha","routes":[]}`, status: 200, recreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reportRead := false
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/apps/alpha" {
					id := "app-one"
					if reportRead && tc.recreate {
						id = "replacement"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "slug": "alpha"})
					return
				}
				reportRead = true
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer backend.Close()
			cs, ctx := connectConfiguredTestServer(t, backend, Options{APIBase: backend.URL, AllowedApps: map[string]string{"alpha": "app-one"}}, "")
			callTool(t, ctx, cs, "gregale_get_route_awake", map[string]any{"app": "alpha"}, true)
			if !reportRead {
				t.Fatal("test never exercised report response")
			}
		})
	}
}

func TestRouteDiagnosticsNamedEnvironment(t *testing.T) {
	f, backend, opts := namedStageOptions(t)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging"}
	for _, tool := range []string{"gregale_get_route_awake", "gregale_get_route_advice"} {
		out := callTool(t, ctx, cs, tool, args, false)
		if !strings.Contains(string(out["report"]), "env-staging") {
			t.Fatalf("named scope refusal: %s", out)
		}
	}
	prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_awake_diagnosis", Arguments: map[string]string{"app": "alpha", "project": "shop", "environment": "staging"}})
	if err != nil {
		t.Fatal(err)
	}
	text := prompt.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(text, "gregale://projects/shop/environments/staging/apps/alpha/health") || !strings.Contains(text, "scoped evidence only") {
		t.Fatal("named diagnosis lost its scope")
	}
	if f.writes != 0 {
		t.Fatal("diagnosis wrote intent")
	}
}
