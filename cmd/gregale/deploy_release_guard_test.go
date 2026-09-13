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

func TestProjectDeployRejectsExecutionConfigurationBeforeRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "function shape", args: []string{"--function"}},
		{name: "app shape", args: []string{"--app"}},
		{name: "runtime", args: []string{"--runtime", "node22"}},
		{name: "handler", args: []string{"--handler", "handler.main"}},
		{name: "dockerfile", args: []string{"--dockerfile"}},
		{name: "vcpu", args: []string{"--vcpu", "2"}},
		{name: "profile", args: []string{"--profile", "small"}},
		{name: "require authn", args: []string{"--require-authn"}},
		{name: "no require authn", args: []string{"--no-require-authn"}},
		{name: "app protocol", args: []string{"--app-protocol", "grpc"}},
		{name: "no triggers", args: []string{"--no-triggers"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_project_execution_guard")

			args := []string{"--tarball", "fixture.tar.gz", "--project-slug", "round15-capture", "--yes"}
			args = append(args, tc.args...)
			if code := cmdDeployTarball(args); code == 0 {
				t.Fatalf("project deploy accepted %s", tc.name)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("API requests = %d, want zero", got)
			}
		})
	}
}
