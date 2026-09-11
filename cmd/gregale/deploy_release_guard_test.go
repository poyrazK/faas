package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRunDeployRejectsUnexpectedPositionalBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_positional_guard")

	code := run([]string{
		"deploy",
		"--tarball", "fail-build.tar.gz",
		"stray",
		"--name", "should-not-be-ignored",
		"--tag", "definitely-invalid",
		"--pr-number", "-99",
		"--dry-run",
		"--json",
		"--no-doctor",
	})
	if code == 0 {
		t.Fatal("deploy accepted an unexpected positional argument")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("API requests = %d, want zero", got)
	}
}

func TestProjectDeployRejectsUnsupportedPolicyBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_project_policy_guard")

	code := cmdDeployTarball([]string{
		"--tarball", "fixture.tar.gz",
		"--project-slug", "round15-capture",
		"--yes",
		"--traffic-percent", "0",
		"--canary-preset", "aggressive",
		"--reason", "captured project release controls",
		"--tag", "scheduled_maintenance",
		"--deployed-by", "round15-capture-user",
		"--pr-number", "1519",
		"--no-doctor",
	})
	if code == 0 {
		t.Fatal("project deploy silently accepted unsupported policy fields")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("API requests = %d, want zero", got)
	}
}
