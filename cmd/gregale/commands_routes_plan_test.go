package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

const routePlanConfig = `version: 1
routes:
  - method: POST
    path: /checkout
    require:
      authentication: consumer
      throttle: {key_by: consumer_id, max_rps: 10, missing_key_policy: reject}
      budget: {max_ms: 1000}
`

func TestRoutesPlanServerSnapshotExportAndGate(t *testing.T) {
	for _, status := range []string{"ready", "partial"} {
		t.Run(status, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			dir := t.TempDir()
			requirements, destination := filepath.Join(dir, "routes.yaml"), filepath.Join(dir, "plan.json")
			if err := os.WriteFile(requirements, []byte(routePlanConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v1/apps/my-api/route-policy/plan" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				var request api.RoutePolicyPlanRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				context := routerequirements.Context{Host: "my-api.gregale.dev", App: api.AppResponse{ID: "app-id", Slug: "my-api", ConsumerAuthMode: "required",
					EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}}
				burst := 20
				if status == "partial" {
					burst = 0
				}
				plan, err := routerequirements.BuildServerPlan(request.Requirements, context, routerequirements.PlanOptions{PlanName: "pro", ThrottleBurst: burst})
				if err != nil {
					t.Fatal(err)
				}
				writeJSONTest(w, plan)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = oldOut })
			code := run([]string{"routes", "plan", "my-api", "--requirements", requirements, "--out", destination, "--fail-on-unresolved", "--json"})
			wantCode := 0
			if status == "partial" {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("exit=%d: %s", code, out.String())
			}
			var plan api.RoutePolicyPlan
			if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Status != status || plan.Version != 2 || plan.Authority != "server" || plan.Requirements == nil || calls != 1 {
				t.Fatalf("plan=%+v calls=%d", plan, calls)
			}
			body, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			var saved api.RoutePolicyPlan
			if err := json.Unmarshal(body, &saved); err != nil || saved.SHA256 != plan.SHA256 {
				t.Fatalf("export: %v", err)
			}
			info, _ := os.Stat(destination)
			if info.Mode().Perm() != 0o600 {
				t.Fatal("plan permission")
			}
		})
	}
}

func TestRoutesPlanInvalidInputsAndExistingOutputBeforeNetwork(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	dir := t.TempDir()
	requirements, invalid, destination := filepath.Join(dir, "routes.yaml"), filepath.Join(dir, "invalid.yaml"), filepath.Join(dir, "existing")
	if err := os.WriteFile(requirements, []byte(routePlanConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte("version: 1\nroutes: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(destination, link); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"my-api"}, {"my-api", "--requirements", invalid}, {"my-api", "--requirements", requirements, "--throttle-burst", "-1"},
		{"my-api", "--requirements", requirements, "--out", destination},
		{"my-api", "--requirements", requirements, "--out", link},
	} {
		if code := cmdRoutesPlan(args); code != 1 {
			t.Fatalf("%v: exit = %d", args, code)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid inputs performed %d authenticated reads", calls)
	}
	body, _ := os.ReadFile(destination)
	if string(body) != "keep me" {
		t.Fatal("existing output was replaced")
	}
}

func TestRoutePlanArtifactDoesNotReplaceRacingDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")
	if err := writeRoutePolicyPlan(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeRoutePolicyPlan(path, []byte("second")); err == nil {
		t.Fatal("planner replaced an existing destination")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "first" {
		t.Fatal("second plan replaced original")
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := writeRoutePolicyPlan(link, []byte("unsafe")); err == nil {
		t.Fatal("planner followed destination symlink")
	}
	temporary, _ := filepath.Glob(filepath.Join(dir, ".gregale-route-plan-*.tmp"))
	if len(temporary) != 0 {
		t.Fatal("planner leaked temporary artifacts")
	}
}

func TestRoutesPlanHelpIsLocalAndRendererSanitizesCodes(t *testing.T) {
	resetJSONOut(t)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	if code := run([]string{"routes", "plan", "--help"}); code != 0 || !strings.Contains(out.String(), "--requirements") {
		t.Fatalf("missing local help: %d %s", code, out.String())
	}
	out.Reset()
	renderRoutePolicyPlan(&out, routerequirements.PolicyPlan{App: "my-api", Status: "blocked",
		Unresolved: []routerequirements.PlanUnresolved{{Method: "POST", Path: "/checkout", Code: "app:\x1b[31m", Requirement: "throttle"}}})
	if strings.ContainsRune(out.String(), '\x1b') {
		t.Fatal("renderer included terminal control sequence")
	}
}
