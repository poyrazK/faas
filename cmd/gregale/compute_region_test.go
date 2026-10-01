package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComputeRegionDeployCreationAndRedeploy(t *testing.T) {
	for _, tc := range []struct {
		name, existingRegion string
		existing             bool
		wantCode             int
	}{
		{name: "new"},
		{name: "same region", existing: true, existingRegion: "eu-fsn"},
		{name: "different region", existing: true, existingRegion: "eu-hel", wantCode: 1},
		{name: "legacy app", existing: true, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var created api.CreateAppRequest
			mutations := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/regional-reservation":
					if !tc.existing {
						http.Error(w, "missing", http.StatusNotFound)
						return
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "regional-reservation", Type: "app", ComputeRegion: tc.existingRegion})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
					mutations++
					_ = json.NewDecoder(r.Body).Decode(&created)
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: created.Slug, ComputeRegion: created.ComputeRegion})
				default:
					mutations++
					http.Error(w, "unexpected mutation", http.StatusInternalServerError)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			if code := cmdDeployTarball([]string{"--create-only", "--app", "--name", "regional-reservation", "--region", "eu-fsn"}); code != tc.wantCode {
				t.Fatalf("exit = %d, want %d", code, tc.wantCode)
			}
			if tc.existing && mutations != 0 {
				t.Fatalf("existing app was mutated: %d requests", mutations)
			}
			if !tc.existing && (mutations != 1 || created.ComputeRegion != "eu-fsn") {
				t.Fatalf("created = %+v, mutations = %d", created, mutations)
			}
		})
	}
}

func TestComputeRegionUnsupportedFlowsFailBeforeRemoteChanges(t *testing.T) {
	for _, flag := range []string{"--project", "--dry-run", "--diff", "--server-diff", "--plan", "--github"} {
		t.Run(flag, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected remote request: %s %s", r.Method, r.URL.Path)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			if code := cmdDeployTarball([]string{flag, "--region", "eu-fsn", "--name", "regional-reservation"}); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
		})
	}
}

func TestComputeRegionSourceRefCreation(t *testing.T) {
	var created api.CreateAppRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "regional", ComputeRegion: created.ComputeRegion})
	}))
	defer srv.Close()
	if _, err := ensureSourceRefApp(context.Background(), NewClient(srv.URL, "fp_live_x"), "regional", sourceRefAppPolicy{ComputeRegion: "eu-fsn"}); err != nil {
		t.Fatal(err)
	}
	if created.ComputeRegion != "eu-fsn" {
		t.Fatalf("create region = %q", created.ComputeRegion)
	}
}

func TestComputeRegionDeveloperDeploymentRejectsRegion(t *testing.T) {
	if code := cmdDeployTarballToExisting(t.Context(), []string{"--name", "regional", "--region", "eu-fsn"}, true); code != 1 {
		t.Fatalf("developer deploy exit = %d, want 1", code)
	}
}
