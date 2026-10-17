package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckedProjectReleaseClientContractsFailClosed(t *testing.T) {
	empty := ""
	manifest := CheckedProjectReleaseManifest{Project: "shop", Environment: "production", TTLSeconds: 1800, Deployments: map[string]string{"api": "11111111-1111-4111-8111-111111111111"}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{"api": 1}, ServiceTargets: map[string]map[string]string{"api": {}}}
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "new server", true: "old server"}[old], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/release-sets/activate") {
					t.Fatal("unexpected mutation route")
				}
				var got CheckedProjectReleaseManifest
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.ExpectedActiveReleaseID == nil || *got.ExpectedActiveReleaseID != "" || got.ExpectedFallbackDeployments == nil || got.ServiceTargets["api"] == nil {
					t.Fatalf("intent omitted: %+v %v", got, err)
				}
				w.Header().Set("Content-Type", "application/json")
				if old {
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not found","status":404,"code":"not_found","detail":"Upgrade the server"}`))
					return
				}
				w.WriteHeader(201)
				_, _ = w.Write([]byte(`{"release":{"id":"r","active":true},"bindings_checks":{}}`))
			}))
			defer server.Close()
			_, err := NewClient(server.URL, "key").ActivateCheckedProjectRelease(context.Background(), "shop", "production", manifest)
			if (err != nil) != old || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCheckedProjectRollbackClientPreservesRecoveryAndFailsClosed(t *testing.T) {
	current := "22222222-2222-4222-8222-222222222222"
	manifest := CheckedProjectReleaseManifest{RollbackReleaseID: "11111111-1111-4111-8111-111111111111", ExpectedActiveReleaseID: &current}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/projects/shop/environments/production/release-sets/rollback" || r.Method != http.MethodPost {
			t.Fatal("rollback fallback or wrong route")
		}
		var got CheckedProjectReleaseManifest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.RollbackReleaseID != manifest.RollbackReleaseID || got.ExpectedActiveReleaseID == nil || *got.ExpectedActiveReleaseID != *manifest.ExpectedActiveReleaseID {
			t.Fatalf("lost recovery intent: %+v %v", got, err)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"status":404,"title":"Not found","code":"not_found"}`))
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "key").RollbackCheckedProjectRelease(context.Background(), "shop", "production", manifest); err == nil || calls != 1 {
		t.Fatalf("old server fallback: calls=%d error=%v", calls, err)
	}
}

func TestCheckedProjectDeactivationClientContractsFailClosed(t *testing.T) {
	active := "11111111-1111-4111-8111-111111111111"
	manifest := CheckedProjectReleaseManifest{Deactivate: true, Project: "shop", Environment: "production", TTLSeconds: 0, ExpectedActiveReleaseID: &active, Deployments: map[string]string{"api": "22222222-2222-4222-8222-222222222222"}, ExpectedFallbackDeployments: map[string]string{"api": "22222222-2222-4222-8222-222222222222"}, ExpectedPolicyRevisions: map[string]int64{"api": 1}, ServiceTargets: map[string]map[string]string{"api": {}}}
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "current server", true: "old server"}[old], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/release-sets/deactivate") {
					t.Fatal("unchecked fallback mutation")
				}
				var got CheckedProjectReleaseManifest
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !got.Deactivate || got.TTLSeconds != 0 || got.ExpectedActiveReleaseID == nil || *got.ExpectedActiveReleaseID != active || got.ExpectedFallbackDeployments["api"] != manifest.Deployments["api"] {
					t.Fatalf("lost deactivation intent=%+v %v", got, err)
				}
				w.Header().Set("Content-Type", "application/json")
				if old {
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"status":404,"title":"Not found","code":"not_found"}`))
					return
				}
				_, _ = w.Write([]byte(`{"release":{"id":"11111111-1111-4111-8111-111111111111","active":false,"deactivation":{"id":"33333333-3333-4333-8333-333333333333","release_id":"11111111-1111-4111-8111-111111111111","completed_at":"2026-10-13T00:00:00Z","fallback_members":[]}},"bindings_checks":{}}`))
			}))
			defer server.Close()
			out, err := NewClient(server.URL, "key").DeactivateCheckedProjectRelease(context.Background(), "shop", "production", manifest)
			if (err != nil) != old || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
			if !old && (out.Release.Active || out.Release.Deactivation == nil || out.Release.Deactivation.ReleaseID != active) {
				t.Fatalf("receipt=%+v", out)
			}
		})
	}
}
