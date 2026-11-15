package mcpmanagement

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

var assessmentFixture = map[string]string{
	"compose.yaml": `services:
  web:
    build: ./web
    ports: ["8080:8080"]
    depends_on: [api]
  api:
    build: ./api
    expose: ["8080"]
    depends_on: [db]
    environment:
      DATABASE_URL: postgres://private-user:PRIVATE_VALUE@db/app
      API_SECRET: PRIVATE_SECRET
    command: npm run dev --token=PRIVATE_COMMAND
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: PRIVATE_PASSWORD
`,
	"web/Dockerfile": "FROM node:22\nCOPY . .\nCMD [\"node\", \"index.js\"]\n",
	"api/Dockerfile": "FROM node:22\nEXPOSE 8080\nCOPY . .\nCMD [\"node\", \"index.js\"]\n",
}

func workflowBackend(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	writes := new(atomic.Int64)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			t.Errorf("discovery wrote %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{Capabilities: []api.CapabilityStatus{{Key: "managed-postgres", Enabled: true}, {Key: "custom-domains", Enabled: true}, {Key: "source-deploy", Enabled: true}}})
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "account"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	return s, writes
}

func TestAgentWorkflowProjectDiscovery(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			backend, writes := workflowBackend(t)
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetDiagnose}, agentKey: true}, protocol)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			tools := map[string]*mcp.Tool{}
			for _, tool := range catalog.Tools {
				tools[tool.Name] = tool
			}
			for _, name := range []string{"gregale_context", "gregale_get_workflow", "gregale_assess_project"} {
				if tools[name] == nil || !tools[name].Annotations.ReadOnlyHint {
					t.Fatalf("discovery missing outside core: %s", name)
				}
			}
			fields := callTool(t, ctx, cs, "gregale_assess_project", assessProjectArgs{Files: assessmentFixture}, false)
			raw, _ := json.Marshal(fields)
			validateWorkflowSchema(t, tools["gregale_assess_project"].OutputSchema, raw)
			if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "postgres://") || strings.Contains(string(raw), "npm run dev") {
				t.Fatal("assessment exposed source values or commands")
			}
			var assessment projectAssessment
			if err := json.Unmarshal(raw, &assessment); err != nil {
				t.Fatal(err)
			}
			if len(assessment.Workloads) != 2 || len(assessment.Dependencies) != 1 || assessment.Dependencies[0].Kind != "postgres" || assessment.NextArguments["task"] != "deploy_project" {
				t.Fatalf("assessment: %+v", assessment)
			}
			for _, feature := range []string{"managed-postgres", "private-services", "project-releases", "dev-setup"} {
				if !slices.ContainsFunc(assessment.Features, func(f assessedFeature) bool { return f.ID == feature }) {
					t.Fatalf("missing feature %s", feature)
				}
			}
			fields = callTool(t, ctx, cs, "gregale_get_workflow", workflowArgs{Task: assessment.NextArguments["task"]}, false)
			raw, _ = json.Marshal(fields)
			validateWorkflowSchema(t, tools["gregale_get_workflow"].OutputSchema, raw)
			var recipe workflowResult
			_ = json.Unmarshal(raw, &recipe)
			if recipe.Steps[1].Status != "unsupported" || recipe.Steps[1].Reason != "project_deploy_requires_cli" || !strings.Contains(recipe.Steps[1].Instruction, "gregale scan") {
				t.Fatal("project guidance invented MCP project deployment")
			}
			callTool(t, ctx, cs, "gregale_assess_project", assessProjectArgs{Path: t.TempDir()}, true)
			if writes.Load() != 0 {
				t.Fatal("discovery mutated the API")
			}
		})
	}
}

func TestWorkflowAvailability(t *testing.T) {
	backend, _ := workflowBackend(t)
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"permission", Options{agentKey: true, Toolsets: []string{ToolsetCore}}, "requires_permission"},
		{"catalog", Options{agentKey: true, AllowWrites: true, Capabilities: []string{api.ScopeManagedPostgresManage}, Toolsets: []string{ToolsetCore}}, "requires_toolset"},
		{"registered", Options{agentKey: true, AllowWrites: true, Capabilities: []string{api.ScopeManagedPostgresManage}, Toolsets: []string{ToolsetProvision}}, "available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.APIBase = backend.URL
			tc.opts.LocalCLI = func(context.Context, []string, []byte) (LocalCLIResult, error) {
				t.Fatal("guidance executed CLI")
				return LocalCLIResult{}, nil
			}
			cs, ctx := connectSelectedTestServer(t, backend, tc.opts, "2026-07-28")
			fields := callTool(t, ctx, cs, "gregale_get_workflow", workflowArgs{Task: "add_database"}, false)
			var steps []workflowStep
			_ = json.Unmarshal(fields["steps"], &steps)
			if steps[0].Status != tc.want {
				t.Fatalf("status=%s reason=%s", steps[0].Status, steps[0].Reason)
			}
		})
	}
	for _, available := range []bool{true, false} {
		m := &management{client: api.NewClient(backend.URL, ""), opts: Options{AllowWrites: true, Toolsets: []string{ToolsetAll}}, toolNames: map[string]bool{"gregale_plan_postgres": true}}
		step := workflowStep{Tool: "gregale_plan_postgres", Capability: "managed-postgres"}
		m.workflowAvailability(t.Context(), workflowArgs{}, &step, api.CapabilitiesResponse{Capabilities: []api.CapabilityStatus{{Key: "managed-postgres", Enabled: available}}}, true, false)
		want := "platform_unavailable"
		if available {
			want = "available"
		}
		if step.Status != want {
			t.Fatalf("capability status: %+v", step)
		}
	}
	m := &management{opts: Options{AllowWrites: true, Toolsets: []string{ToolsetAll}}, toolNames: map[string]bool{"gregale_plan_postgres": true}}
	for _, tc := range []struct {
		caps  api.CapabilitiesResponse
		known bool
		want  string
	}{
		{api.CapabilitiesResponse{}, false, "unknown"},
		{api.CapabilitiesResponse{Capabilities: []api.CapabilityStatus{{Key: "managed-postgres", UnavailableReason: api.CapabilityUnavailablePlan}}}, true, "plan_restricted"},
	} {
		step := workflowStep{Tool: "gregale_plan_postgres", Capability: "managed-postgres"}
		m.workflowAvailability(t.Context(), workflowArgs{}, &step, tc.caps, tc.known, false)
		if step.Status != tc.want {
			t.Fatalf("availability=%+v", step)
		}
	}
	m.opts.Environments = &EnvironmentConsent{DefaultWrite: false}
	step := workflowStep{Tool: "gregale_apply_provision"}
	m.workflowAvailability(t.Context(), workflowArgs{}, &step, api.CapabilitiesResponse{}, false, true)
	if step.Status != "requires_permission" {
		t.Fatal("preview ignored default-scope write consent")
	}
}

func TestAssessmentLocalBoundsAndIsolation(t *testing.T) {
	root := t.TempDir()
	for name, data := range assessmentFixture {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(outside, []byte("PRIVATE_OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=PRIVATE_ENV"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &management{opts: Options{LocalCLI: func(context.Context, []string, []byte) (LocalCLIResult, error) {
		t.Fatal("assessment executed CLI")
		return LocalCLIResult{}, nil
	}}}
	value, err := m.assessProject(t.Context(), assessProjectArgs{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if value.(projectAssessment).FilesInspected != 3 {
		t.Fatalf("excluded source was read: %+v", value)
	}
	for _, files := range []map[string]string{{"../escape": "x"}, {".env": "SECRET=x"}, {"compose.yaml": strings.Repeat("x", maxInlineBytes+1)}, {"/absolute": "x"}, {"a\\b": "x"}, {}} {
		if _, err := m.assessProject(t.Context(), assessProjectArgs{Files: files}); err == nil {
			t.Fatalf("accepted invalid source %v", slices.Collect(maps.Keys(files)))
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.assessProject(ctx, assessProjectArgs{Path: root}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", maxAssessmentFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.assessProject(t.Context(), assessProjectArgs{Path: root}); err == nil {
		t.Fatal("accepted oversized local source")
	}
}
