package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsCustomerEnforcementGate(t *testing.T) {
	srv, store, account, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	srv.githubd = &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}}
	request := map[string]any{"manifest_path": "env.yaml", "mode": "enforce"}
	rec := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source", request, srv.createEnvironmentGitSource)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "environment_git_enforcement_unavailable") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	request["mode"] = "report"
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source", request, srv.createEnvironmentGitSource)
	var source state.EnvironmentGitSource
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &source) != nil {
		t.Fatalf("report create: %d %s", rec.Code, rec.Body.String())
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPatch, "source", map[string]any{"expected_generation": source.Generation, "mode": "enforce"}, srv.updateEnvironmentGitSource)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "environment_git_enforcement_unavailable") {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	current, err := store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
	if err != nil || current.Generation != source.Generation || current.Spec.Mode != "report" {
		t.Fatalf("rejection mutated source: %+v %v", current, err)
	}
}
func TestEnvironmentGitOpsBindingLifecycleHandlers(t *testing.T) {
	srv, store, account, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	srv.githubd = &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}}
	rec := gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source", map[string]string{"manifest_path": "env.yaml"}, srv.createEnvironmentGitSource)
	var source state.EnvironmentGitSource
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &source) != nil {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodPost, "source/rebind", map[string]any{"expected_generation": source.Generation, "manifest_path": "env/new.yaml", "ref": "refs/heads/new"}, srv.rebindEnvironmentGitSource)
	var next state.EnvironmentGitSource
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &next) != nil || next.ID == source.ID || next.Spec.ManifestPath != "env/new.yaml" {
		t.Fatalf("rebind: %d %s", rec.Code, rec.Body.String())
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodDelete, "source", map[string]any{"expected_generation": source.Generation}, srv.detachEnvironmentGitSource)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale detach: %d %s", rec.Code, rec.Body.String())
	}
	rec = gitOpsHandlerRequest(t, srv, account, http.MethodDelete, "source", map[string]any{"expected_generation": next.Generation}, srv.detachEnvironmentGitSource)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("detach: %d %s", rec.Code, rec.Body.String())
	}
}
