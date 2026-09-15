package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectsListJSONUsesAccountEndpointAndNDJSON(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `[{"id":"0123456789abcdef0123456789abcdef","slug":"shop","scan_source":"compose","workload_count":2,"created_at":"2026-09-15T00:00:00Z","updated_at":"2026-09-15T00:00:00Z"}]`, http.StatusOK)
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	jsonOutput = true
	t.Cleanup(func() { osStdout = previousOut; jsonOutput = false })

	if code := cmdProjectsList(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodGet || f.sawPath != "/v1/projects" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("NDJSON lines = %d, want 1; output=%q", len(lines), out.String())
	}
	var got api.ProjectSummaryResponse
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil || got.Slug != "shop" {
		t.Fatalf("NDJSON project = %+v, err=%v", got, err)
	}
}

func TestProjectsUpdateCarriesExplicitFields(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"0123456789abcdef0123456789abcdef","slug":"shop","production_branch":"release","scan_source":"compose","workload_count":0,"created_at":"2026-09-15T00:00:00Z","updated_at":"2026-09-15T00:00:00Z","workloads":[],"exclusions":[]}`, http.StatusOK)
	if code := cmdProjectsUpdate([]string{"shop", "--branch", "release"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodPatch || f.sawPath != "/v1/projects/shop" {
		t.Fatalf("route = %s %s", f.sawMethod, f.sawPath)
	}
	var body map[string]any
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["production_branch"] != "release" {
		t.Fatalf("body = %s", f.sawBody)
	}
	if _, present := body["repo_full_name"]; present {
		t.Fatalf("unset repo leaked into patch: %s", f.sawBody)
	}
}

func TestProjectsRemoveRequiresAnExplicitMode(t *testing.T) {
	resetJSONOut(t)
	code, output := runWithStderr(t, func() int { return cmdProjectsRemove([]string{"shop"}) })
	if code != 1 || !strings.Contains(output, "--dry-run | --yes") {
		t.Fatalf("exit=%d stderr=%q", code, output)
	}
}

func TestProjectsRemoveConfirmedPreviewsThenDeletes(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"project":{"id":"0123456789abcdef0123456789abcdef","slug":"shop","scan_source":"compose","workload_count":1,"created_at":"2026-09-15T00:00:00Z","updated_at":"2026-09-15T00:00:00Z"},"workloads":[{"slug":"api","workload_name":"api","status":"active"}],"domain_count":1,"env_count":2,"cron_count":3}`, http.StatusOK)
	if code := cmdProjectsRemove([]string{"shop", "--yes"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != http.MethodDelete || f.sawPath != "/v1/projects/shop" {
		t.Fatalf("final route = %s %s", f.sawMethod, f.sawPath)
	}
}
