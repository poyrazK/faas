package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestAutomaticProfileDeploymentClient(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 123000000, time.UTC)
	config := faas.ProfileDeploymentPolicyConfig{NotifyRouteRegressions: true, Enabled: true, Runtime: "node24", WindowSeconds: 300, WarmupSeconds: 120, Options: faas.DefaultProfileRegressionOptions()}
	config.Options.Routes = []string{"POST /checkout"}
	policy := faas.ProfileDeploymentPolicy{AppID: "app", Config: config}
	check := faas.ProfileDeploymentCheck{DeploymentID: "dep", AppID: "app", Scope: "prod", PolicyRevision: 1, Config: config, Candidate: faas.ProfileQuery{DeploymentID: "dep", Runtime: "node24", Start: at, End: at.Add(5 * time.Minute)}, Status: "queued", Reason: "Waiting for capture", NextAttemptAt: &at, CreatedAt: at}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		var result any = check
		if strings.HasSuffix(r.URL.Path, "/deployment-policy") {
			if r.Method == "PUT" {
				var req faas.SaveProfileDeploymentPolicyRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.ExpectedRevision == nil || *req.ExpectedRevision != 0 || !reflect.DeepEqual(req.Config, config) {
					t.Error("initial revision/settings lost", req)
				}
				policy.Revision, policy.UpdatedAt = 1, &at
			}
			result = policy
		} else if strings.HasSuffix(r.URL.Path, "/deployment-checks") {
			result = faas.ListProfileDeploymentChecksResponse{Checks: []faas.ProfileDeploymentCheck{check}}
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	p, err := client.GetProfileDeploymentPolicy(context.Background(), "demo")
	if err != nil || p.Revision != 0 || p.UpdatedAt != nil {
		t.Fatal(p, err)
	}
	zero := int64(0)
	p, err = client.SaveProfileDeploymentPolicy(context.Background(), "demo", faas.SaveProfileDeploymentPolicyRequest{ExpectedRevision: &zero, Config: config})
	if err != nil || p.Revision != 1 || !p.UpdatedAt.Equal(at) {
		t.Fatal(p, err)
	}
	list, err := client.ListProfileDeploymentChecks(context.Background(), "demo")
	if err != nil || len(list.Checks) != 1 {
		t.Fatal(list, err)
	}
	got, err := client.GetProfileDeploymentCheck(context.Background(), "demo", "dep")
	if err != nil || got.Baseline != nil || got.Status != "queued" || !got.Candidate.Start.Equal(at) || got.CompletedAt != nil {
		t.Fatal(got, err)
	}
}
