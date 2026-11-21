package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Submission and lifecycle reads share one discoverable family. The legacy
// submit spelling must keep its positional meaning, including apps named after
// verbs in that family (ADR-20261113202515).
func TestInvocationFamilySubmissionRouting(t *testing.T) {
	main.ResetJSONOut(t)
	requests := make(chan string, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		if strings.HasSuffix(r.URL.Path, "/invoke/async") {
			_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-family"})
		} else {
			_ = json.NewEncoder(w).Encode(api.InvokeResponse{ID: "inv-family", Status: "completed"})
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "family-test-token")
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"invocation", "run", "billing"}, "/v1/apps/billing/invoke"},
		{[]string{"invocation", "run", "billing", "--async"}, "/v1/apps/billing/invoke/async"},
		{[]string{"invocation", "run", "wait"}, "/v1/apps/wait/invoke"},
		{[]string{"invoke", "billing"}, "/v1/apps/billing/invoke"},
		{[]string{"invoke", "run"}, "/v1/apps/run/invoke"},
		{[]string{"invoke", "list"}, "/v1/apps/list/invoke"},
		{[]string{"invoke", "get"}, "/v1/apps/get/invoke"},
		{[]string{"invoke", "wait"}, "/v1/apps/wait/invoke"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.Run(append([]string{"--json"}, tc.args...)); code != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), stderr())
			}
			select {
			case request := <-requests:
				if request != "POST "+tc.path || len(requests) != 0 {
					t.Fatalf("request=%s extra requests=%d", request, len(requests))
				}
			default:
				t.Fatal("submission did not reach the API")
			}
		})
	}
}

func TestInvocationFamilyDiscovery(t *testing.T) {
	enableEveryCLIFeature(t)
	_, help, stderr := runCLI(t, "help", "invocation")
	for _, verb := range []string{"run", "list", "get", "wait"} {
		if !strings.Contains(help, verb) {
			t.Fatalf("family help omits %s: %s %s", verb, help, stderr)
		}
	}
	_, leaf, stderr := runCLI(t, "help", "invocation", "run")
	if !strings.Contains(leaf, "invocation run") || !strings.Contains(leaf, "--payload") || !strings.Contains(leaf, "--wait") {
		t.Fatalf("submission help is incomplete: %s %s", leaf, stderr)
	}
	_, all, _ := runCLI(t, "help", "--all")
	if regexp.MustCompile(`(?m)^  invoke\s`).MatchString(all) {
		t.Fatalf("root discovery still offers a separate invoke command: %s", all)
	}
	_, completion, _ := runCLI(t, "completion", "bash")
	root := regexp.MustCompile(`compgen -W "(--non-interactive [^"]*)"`).FindStringSubmatch(completion)
	if len(root) != 2 {
		t.Fatal("completion has no root word list")
	}
	words := strings.Fields(root[1])
	if !slices.Contains(words, "invocation") || slices.Contains(words, "invoke") || slices.Contains(words, "invocations") {
		t.Fatalf("completion exposes inconsistent invocation commands: %v", words)
	}
	manifest := main.CliTreeManifest(main.PublicReferenceCommands())
	for _, command := range manifest {
		if command.Name == "invoke" || command.Name == "invocations" {
			t.Fatalf("published manifest offers duplicate family %q", command.Name)
		}
		if command.Name == "invocation" {
			if len(command.Positionals) != 0 {
				t.Errorf("family root incorrectly requires a target: %v", command.Positionals)
			}
			var verbs []string
			for _, sub := range command.Subcommands {
				verbs = append(verbs, sub.Name)
			}
			for _, verb := range []string{"run", "list", "get", "wait"} {
				if !slices.Contains(verbs, verb) {
					t.Errorf("published invocation manifest omits %s", verb)
				}
			}
			return
		}
	}
	t.Fatal("published manifest has no invocation family")
}
