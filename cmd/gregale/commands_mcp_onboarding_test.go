package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func privateMCPInitArgs(dest, runtime string) []string {
	return []string{"--path", dest, "--runtime", runtime, "--auth", "external-oauth", "--issuer", "https://identity.example.com", "--jwks-url", "https://identity.example.com/jwks", "--resource", "https://mcp.example.com/mcp", "--scopes", "mcp:tools, mcp:read"}
}

func TestMCPPrivateOnboardingEachRuntime(t *testing.T) {
	for _, runtime := range []string{"node", "go", "python"} {
		t.Run(runtime, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "server")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.CmdMCP(privateMCPInitArgsWithCommand(dest, runtime)); code != 0 {
				t.Fatalf("exit %d: %s", code, stderr())
			}
			config, err := mcphosting.Load(dest)
			if err != nil {
				t.Fatal(err)
			}
			if config.Auth.Mode != "external-oauth" || config.Auth.Resource != "https://mcp.example.com/mcp" || strings.Join(config.Auth.Scopes, ",") != "mcp:tools,mcp:read" {
				t.Fatalf("private config: %+v", config.Auth)
			}
			if config.Auth.ToolScopes["build_report"] == nil || config.Auth.ResourceScopes["customer://records/{recordId}"] == nil || config.Auth.PromptScopes["summarize"] == nil {
				t.Fatal("lost explicit catalog allowlists")
			}
			if config.Tasks == nil || *config.Tasks.Enabled {
				t.Fatal("Tasks unexpectedly enabled")
			}
			if !strings.Contains(out.String(), "--token-env MCP_ACCESS_TOKEN") || strings.Contains(out.String(), "uses public access") {
				t.Fatalf("incorrect next steps: %s", out.String())
			}
		})
	}
}

func privateMCPInitArgsWithCommand(dest, runtime string) []string {
	return append([]string{"init"}, privateMCPInitArgs(dest, runtime)...)
}

func TestMCPOnboardingInvalidAuthDoesNotCreateFiles(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
	}{
		{"incomplete private", []string{"--auth", "external-oauth"}},
		{"unknown mode", []string{"--auth", "private"}},
		{"OAuth in public mode", []string{"--issuer", "https://identity.example.com"}},
		{"insecure issuer", []string{"--issuer", "http://identity.example.com"}},
		{"credentials in JWKS URL", []string{"--jwks-url", "https://user:password@identity.example.com/jwks"}},
		{"wrong endpoint audience", []string{"--resource", "https://mcp.example.com/other"}},
		{"empty scope", []string{"--scopes", "mcp:read,"}},
		{"oversized config", []string{"--issuer", "https://identity.example.com/" + strings.Repeat("a", 70_000)}},
		{"missing task name", []string{"--tasks"}},
		{"oversized prefix", []string{"--tasks", "--name", strings.Repeat("a", 55)}},
		{"name without tasks", []string{"--name", "reports"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "server")
			args := privateMCPInitArgs(dest, "node")
			if tc.name == "OAuth in public mode" || tc.name == "incomplete private" || tc.name == "missing task name" || tc.name == "oversized prefix" || tc.name == "name without tasks" {
				args = []string{"--path", dest}
			}
			args = append(args, tc.flags...)
			_, _, restore := main.SwapIO(t)
			defer restore()
			if code := main.CmdMCPInit(args); code == 0 {
				t.Fatal("invalid onboarding accepted")
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination changed: %v", err)
			}
		})
	}
}

func TestMCPTaskStackReservationPreservesExecutionRoles(t *testing.T) {
	root := t.TempDir()
	main.WithCwd(t, root)
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	if main.CmdMCPInit([]string{"--path", filepath.Join(root, "stack"), "--runtime", "python", "--tasks", "--name", "reports"}) != 0 {
		t.Fatal(stderr())
	}
	for _, role := range []string{"web", "worker", "observer"} {
		t.Run(role, func(t *testing.T) {
			var mode string
			stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *main.ZeroConfigStubServer) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/reports-"+role {
					http.NotFound(w, r)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/apps" {
					t.Errorf("unexpected reservation request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				var request api.CreateAppRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				mode = request.ExecutionMode
				main.WriteJSONTest(w, api.AppResponse{ID: "reserved", Slug: "reports-" + role})
			})
			t.Setenv("FAAS_API", (*stub.Srv()).URL)
			t.Setenv("FAAS_TOKEN", "operator")
			args := []string{"--create-only", "--path", filepath.Join(root, "stack", role), "--name", "reports-" + role}
			if role != "web" {
				args = append(args, "--execution-mode", "worker")
			}
			if code := main.CmdDeployTarball(args); code != 0 {
				t.Fatalf("reservation failed: %d %s", code, stderr())
			}
			if role != "web" && mode != api.ExecutionModeWorker || role == "web" && mode == api.ExecutionModeWorker {
				t.Fatalf("%s reserved with mode %q", role, mode)
			}
		})
	}
}

func TestMCPOnboardingPreservesOccupiedAndSymlinkDestinations(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "occupied", true: "symlink"}[symlink], func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "server")
			actual := dest
			if symlink {
				actual = filepath.Join(root, "target")
			}
			if err := os.Mkdir(actual, 0o755); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(actual, dest); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(actual, "keep"), []byte("customer work"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, _, restore := main.SwapIO(t)
			defer restore()
			if main.CmdMCPInit(privateMCPInitArgs(dest, "node")) == 0 {
				t.Fatal("unsafe destination accepted")
			}
			entries, err := os.ReadDir(actual)
			if err != nil {
				t.Fatal(err)
			}
			if symlink && len(entries) != 0 || !symlink && (len(entries) != 1 || entries[0].Name() != "keep") {
				t.Fatal("customer destination changed")
			}
		})
	}
}

func TestMCPTaskStackMaterializesValidRoles(t *testing.T) {
	for _, runtime := range []string{"node", "go", "python"} {
		t.Run(runtime, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "stack")
			out, stderr, restore := main.SwapIO(t)
			defer restore()
			oldJSON := (*main.JsonOutput)
			(*main.JsonOutput) = true
			defer func() { (*main.JsonOutput) = oldJSON }()
			args := append(privateMCPInitArgs(dest, runtime), "--tasks", "--name", "reports")
			if code := main.CmdMCPInit(args); code != 0 {
				t.Fatalf("exit %d: %s", code, stderr())
			}
			var receipt struct {
				Tasks    bool   `json:"tasks"`
				AuthMode string `json:"auth_mode"`
			}
			if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || !receipt.Tasks || receipt.AuthMode != "external-oauth" {
				t.Fatalf("receipt %s: %v", out.String(), err)
			}
			for _, role := range []string{"web", "worker", "observer"} {
				root := filepath.Join(dest, role)
				config, err := mcphosting.Load(root)
				if err != nil {
					t.Fatal(err)
				}
				if !*config.Tasks.Enabled || config.Auth.Mode != "external-oauth" {
					t.Fatalf("incorrect %s config", role)
				}
				manifest, found, err := gregalemanifest.Load(root)
				if err != nil || !found {
					t.Fatalf("%s manifest: %v", role, err)
				}
				if role == "web" {
					if manifest.Worker != nil || manifest.Hosting.Health != "/healthz" {
						t.Fatal("web configured as worker")
					}
				} else {
					if manifest.Worker == nil || manifest.Worker.Scale.Min != 1 || manifest.Hosting.Port != 0 {
						t.Fatalf("invalid %s worker", role)
					}
					if role == "observer" && manifest.Worker.Scale.Max != 1 {
						t.Fatal("observer must stay at one replica")
					}
					if role == "worker" && manifest.Worker.Scale.Target != float64(config.Tasks.WorkerConcurrency) {
						t.Fatal("scaling target does not match task concurrency")
					}
				}
				env, err := os.ReadFile(filepath.Join(root, "runtime.env"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(env, []byte("MCP_TASKS_ROLE="+role+"\nMCP_TASK_NAMESPACE=reports\n")) {
					t.Fatalf("wrong %s runtime settings: %s", role, env)
				}
				if bytes.Contains(env, []byte("DATABASE_URL=")) || bytes.Contains(env, []byte("MCP_TASK_OWNER_KEY=")) || bytes.Contains(env, []byte("MCP_TASKS_SCALING_TOKEN=")) {
					t.Fatal("credential values in public runtime settings")
				}
			}
			guide, err := os.ReadFile(filepath.Join(dest, "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(guide, []byte("--token-env MCP_ACCESS_TOKEN")) || !bytes.Contains(guide, []byte("--observer-app reports-observer --min 0")) {
				t.Fatal("private or scale-to-zero setup guidance missing")
			}
		})
	}
}

func TestMCPStarterCapabilityReferenceFresh(t *testing.T) {
	var got bytes.Buffer
	if err := main.RenderMCPStarterCapabilities(&got); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../docs/mcp-capabilities.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("regenerate docs/mcp-capabilities.md with gregale mcp server capabilities --markdown")
	}
}

func TestMCPCapabilitiesJSONDispatchAndFilter(t *testing.T) {
	stdout, stderr, restore := main.SwapIO(t)
	defer restore()
	oldJSON := (*main.JsonOutput)
	(*main.JsonOutput) = true
	defer func() { (*main.JsonOutput) = oldJSON }()
	if code := main.CmdMCP([]string{"capabilities", "--runtime", "python"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr())
	}
	var result struct {
		Maturity     string `json:"maturity"`
		Capabilities []struct {
			Runtimes map[string]string `json:"runtimes"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Maturity != "preview" || len(result.Capabilities) == 0 {
		t.Fatal("missing starter qualification scope")
	}
	for _, entry := range result.Capabilities {
		if len(entry.Runtimes) != 1 || entry.Runtimes["python"] == "" {
			t.Fatal("filter included other runtimes")
		}
	}
}
