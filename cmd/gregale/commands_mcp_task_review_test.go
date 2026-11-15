package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func taskReviewFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	enabled := true
	cfg := mcphosting.Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{},
		Auth:  mcphosting.AuthConfig{Mode: "open", ToolScopes: mcphosting.ScopePolicy{"build_report": {}}},
		Tasks: &mcphosting.TasksConfig{Enabled: &enabled, DatabaseURLEnv: "DATABASE_URL", OwnerKeyEnv: "MCP_TASK_OWNER_KEY", NamespaceEnv: "MCP_TASK_NAMESPACE", TTLSeconds: 3600, WorkerConcurrency: 2}}
	if err := cfg.Validate(); err != nil {
		t.Fatal("invalid Task source fixture", err)
	}
	for _, name := range []string{"web", "worker"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(cfg)
		for file, content := range map[string]string{mcphosting.ConfigFile: string(body), "package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "throw new Error('review must not run source');", "tasks-release.js": "throw new Error('review must not run migrations');"} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := main.McpNativeReleasePlan{WebApp: "web", WorkerApp: "candidate", ObserverApp: "observer", ObserverMetricApp: "candidate", WebPath: "web", WorkerPath: "worker", TokenEnv: "MCP_UNRESOLVED_TOKEN"}
	body, _ := json.Marshal(p)
	path := filepath.Join(root, "release.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestMCPTaskReleasePlanOffline(t *testing.T) {
	root, path := taskReviewFixture(t)
	baseline, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: []mcphosting.Tool{}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := mcphosting.MarshalContract(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "reader.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	policy := `{"version":1,"roles":[{"name":"reader","token_env":"MCP_READER_TOKEN","baseline":"reader.json"}]}`
	if err := os.WriteFile(filepath.Join(root, "web", "release-policy.json"), []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	var declared main.McpNativeReleasePlan
	if err != nil || json.Unmarshal(body, &declared) != nil {
		t.Fatal("read fixture plan", err)
	}
	declared.ReleasePolicy = "web/release-policy.json"
	body, err = json.Marshal(declared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("MCP_UNRESOLVED_TOKEN", "")
	t.Setenv("MCP_READER_TOKEN", "")
	t.Setenv("MCP_TASK_MIGRATION_DATABASE_URL", "postgres://owner:SECRET_SENTINEL@invalid/db")
	t.Setenv("PATH", t.TempDir()) // Review must not require Node or another executable.
	code, output, stderr := runCLI(t, "mcp", "server", "tasks", "deploy", "plan", "--plan", path, "--json")
	var review mcphosting.TaskReleaseReview
	if code != 0 || json.Unmarshal([]byte(output), &review) != nil {
		t.Fatalf("review: exit=%d output=%s stderr=%s", code, output, stderr)
	}
	if review.Status != "review_only" || review.ReadyToApply || review.BindingsVerified || !review.MigrationsRequired || review.WorkerRuntime != "node" || review.Protocol != mcphosting.ProtocolVersion || review.WorkerLimits.WorkerConcurrency != 2 || review.WebLimits.TTLSeconds != 3600 || len(review.ToolScopes) != 1 || len(review.RequiredSetup) == 0 {
		t.Fatalf("misleading review: %+v", review)
	}
	if requests.Load() != 0 || strings.Contains(output, "SECRET_SENTINEL") || strings.Contains(output, "MCP_UNRESOLVED_TOKEN") || strings.Contains(output, "MCP_TASK_OWNER_KEY") || strings.Contains(output, root) {
		t.Fatal("offline review performed network IO or exposed a credential reference/source path")
	}
	if len(review.ReadinessRoles) != 1 || review.ReadinessRoles[0] != "reader" || strings.Contains(output, "MCP_READER_TOKEN") {
		t.Fatal("review failed to project readiness roles without resolving or exposing their credentials")
	}
	plan, err := main.ReadMCPNativePlan(path)
	if err != nil {
		t.Fatal(err)
	}
	native, err := main.McpNativeFingerprint(plan)
	if err != nil || native != review.Fingerprint {
		t.Fatal("review does not use the existing release fingerprint", err)
	}
	if err := os.WriteFile(filepath.Join(root, "worker", "server.js"), []byte("changed worker"), 0600); err != nil {
		t.Fatal(err)
	}
	code, output, stderr = runCLI(t, "mcp", "tasks", "release", "plan", "--plan", path, "--json")
	var changed mcphosting.TaskReleaseReview
	if code != 0 || json.Unmarshal([]byte(output), &changed) != nil || changed.Fingerprint == review.Fingerprint {
		t.Fatalf("changed source did not change review fingerprint: %d %s", code, stderr)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 3 {
		t.Fatal("review created a journal or other release artifact", entries, err)
	}
}

func TestMCPTaskReleasePlanRefusesUnsafeInputs(t *testing.T) {
	for _, scenario := range []string{"journal", "resume", "unknown field", "symbolic source", "oversized source", "missing Node gate", "non-Node source", "Tasks disabled"} {
		t.Run(scenario, func(t *testing.T) {
			root, path := taskReviewFixture(t)
			args := []string{"mcp", "server", "tasks", "deploy", "plan", "--plan", path, "--json"}
			switch scenario {
			case "journal":
				args = append(args, "--state", filepath.Join(root, "journal.json"))
			case "resume":
				args = append(args, "--resume")
			case "unknown field":
				body, _ := os.ReadFile(path)
				body = append([]byte(`{"credential":"inline-secret",`), body[1:]...)
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
			case "symbolic source":
				if err := os.Symlink(path, filepath.Join(root, "worker", "linked.json")); err != nil {
					t.Fatal(err)
				}
			case "oversized source":
				f, err := os.Create(filepath.Join(root, "worker", "oversized"))
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate(513 << 20)
				_ = f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "missing Node gate":
				if err := os.Remove(filepath.Join(root, "worker", "tasks-release.js")); err != nil {
					t.Fatal(err)
				}
			case "non-Node source":
				if err := os.Remove(filepath.Join(root, "worker", "package.json")); err != nil {
					t.Fatal(err)
				}
			case "Tasks disabled":
				p := filepath.Join(root, "worker", mcphosting.ConfigFile)
				body, _ := os.ReadFile(p)
				if err := os.WriteFile(p, []byte(strings.Replace(string(body), `"enabled":true`, `"enabled":false`, 1)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if code, output, _ := runCLI(t, args...); code == 0 {
				t.Fatalf("accepted %s: %s", scenario, output)
			}
			if _, err := os.Stat(filepath.Join(root, "journal.json")); !os.IsNotExist(err) {
				t.Fatal("review created a journal", err)
			}
		})
	}
}
