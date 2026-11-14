package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPPublicationBaselineGuards(t *testing.T) {
	for _, tc := range []struct {
		name, appID, serving, expected string
		pass                           bool
	}{
		{"same release", "app-one", "stable", "stable", true},
		{"new app", "app-one", "", "", true},
		{"changed app", "replacement", "stable", "stable", false},
		{"changed serving release", "app-one", "replacement", "stable", false},
		{"first publish gained serving release", "app-one", "replacement", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("baseline validation mutated native state")
				}
				switch r.URL.Path {
				case "/v1/apps/alpha":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: tc.appID, Slug: "alpha"})
				case "/v1/apps/alpha/deployments":
					items := []api.DeploymentResponse{{ID: "candidate", AppID: tc.appID, Status: "live"}}
					if tc.serving != "" {
						items = append(items, api.DeploymentResponse{ID: tc.serving, AppID: tc.appID, Status: "live", TrafficPercent: 100})
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: items})
				default:
					t.Errorf("unexpected baseline request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer backend.Close()
			err := main.CheckMCPPublicationBaselineForTest(t.Context(), main.NewClient(backend.URL, "test-key"), "alpha", "candidate", "app-one", &tc.expected)
			if (err == nil) != tc.pass {
				t.Fatalf("baseline error=%v pass=%v", err, tc.pass)
			}
		})
	}
}
