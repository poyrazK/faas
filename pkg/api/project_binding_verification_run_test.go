package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestProjectBindingVerificationRunClientFrozenReviewAndRoutes(t *testing.T) {
	id := "b9aeef19-a494-4268-8f6d-bda5c8363625"
	target := "e746a535-dac5-49f6-abf4-c73cd2543aeb"
	empty := ""
	request := CreateProjectBindingVerificationRunRequest{ID: id, TTLSeconds: 60, QueuePublish: true, Manifest: CheckedProjectReleaseManifest{Project: "project", Environment: "production", TTLSeconds: 60, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, Deployments: map[string]string{"app": target}, ExpectedPolicyRevisions: map[string]int64{"app": 3}, ServiceTargets: map[string]map[string]string{"app": {}}}, ReviewDigests: map[string]string{"app": strings.Repeat("a", 64)}}
	base := "/v1/projects/project/environments/production/release-sets/verification-runs"
	calls := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.Method+" "+r.URL.Path] = true
		if r.URL.Path == base {
			var in CreateProjectBindingVerificationRunRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !reflect.DeepEqual(in, request) {
				t.Errorf("review changed: %+v %v", in, err)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ProjectBindingVerificationRun{ID: id, Manifest: request.Manifest, ReviewDigest: ProjectBindingVerificationReviewDigest(request), Members: map[string]BindingVerificationRun{"app": {GraphRunID: id, Inventory: AppBindingInventory{ServiceTargets: map[string]string{"billing": target}}}}})
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	ctx := t.Context()
	run, err := client.CreateProjectBindingVerificationRun(ctx, "project", "production", request)
	if err != nil || run.ID != id || run.Members["app"].Inventory.ServiceTargets["billing"] != target {
		t.Fatalf("create: %+v %v", run, err)
	}
	for _, method := range []func(*Client) error{func(c *Client) error {
		_, err := c.GetProjectBindingVerificationRun(ctx, "project", "production", id)
		return err
	}, func(c *Client) error {
		_, err := c.ResumeProjectBindingVerificationRun(ctx, "project", "production", id)
		return err
	}, func(c *Client) error {
		_, err := c.CompleteProjectBindingVerificationRun(ctx, "project", "production", id)
		return err
	}, func(c *Client) error {
		_, err := c.CancelProjectBindingVerificationRun(ctx, "project", "production", id)
		return err
	}} {
		if err := method(client); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []string{"POST " + base, "GET " + base + "/" + id, "POST " + base + "/" + id + "/resume", "POST " + base + "/" + id + "/complete", "DELETE " + base + "/" + id} {
		if !calls[route] {
			t.Errorf("missing route %s", route)
		}
	}
}
