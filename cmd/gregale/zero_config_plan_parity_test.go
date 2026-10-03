package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingcontract"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

// TestZeroConfigPlanMatchesDeployedSourceAndReceipt keeps the customer-facing
// --plan contract in step with the bytes and app settings a real CLI deploy
// sends. The API is stubbed so this test needs no account or builder; the
// reference-node catalog separately checks the resulting hosting receipt.
func TestZeroConfigPlanMatchesDeployedSourceAndReceipt(t *testing.T) {
	catalog, err := apihostingcontract.Load()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := make(map[string]apihostingcontract.Fixture, len(catalog.Fixtures))
	for _, fixture := range catalog.Fixtures {
		fixtures[fixture.ID] = fixture
	}

	type parityCase struct {
		name             string
		fixtureID        string
		workspace        bool
		goWorkspace      bool
		worktree         bool
		dirtyOverride    bool
		wantSourceRoot   string
		wantPort         int
		wantHealth       string
		workspaceSibling string
	}
	testCases := []parityCase{
		{name: "committed FastAPI root ignores dirty config", fixtureID: "fastapi", dirtyOverride: true, wantPort: 8000, wantHealth: "/healthz"},
		{name: "committed Express workspace member", fixtureID: "express", workspace: true, wantSourceRoot: "apps/api", wantPort: 3000, wantHealth: "/healthz", workspaceSibling: "packages/shared/index.js"},
		{name: "Express workspace worktree includes dirty config", fixtureID: "express", workspace: true, worktree: true, dirtyOverride: true, wantSourceRoot: "apps/api", wantPort: 8787, wantHealth: "/ready", workspaceSibling: "packages/shared/index.js"},
		{name: "Go workspace member preserves repository context", fixtureID: "go-net-http", goWorkspace: true, wantSourceRoot: "apps/api", workspaceSibling: "apps/worker/go.mod"},
	}
	for _, fixture := range catalog.Fixtures {
		if fixture.Expected.Framework == "" || fixture.Expected.Framework == "oci" {
			continue
		}
		testCases = append(testCases, parityCase{name: "catalog " + fixture.ID, fixtureID: fixture.ID})
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, ok := fixtures[tc.fixtureID]
			if !ok {
				t.Fatalf("API hosting catalog is missing %q", tc.fixtureID)
			}
			repo := initZeroConfigRepo(t)
			if fixture.Expected.PackageManager != "npm" {
				// The shared Git helper seeds a Node marker. Remove it for
				// Python and Go fixtures so they exercise their own markers.
				if err := os.Remove(filepath.Join(repo, "package.json")); err != nil {
					t.Fatal(err)
				}
			}
			member := repo
			if tc.workspace || tc.goWorkspace {
				member = filepath.Join(repo, "apps", "api")
			}
			if fixture.SourceRoot != "" {
				member = filepath.Join(repo, filepath.FromSlash(fixture.SourceRoot))
			}
			if tc.workspace {
				writeParityFile(t, repo, "package.json", `{"private":true,"workspaces":["apps/*","packages/*"]}`)
				writeParityFile(t, repo, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
				writeParityFile(t, repo, "packages/shared/index.js", "module.exports = {}\n")
			}
			if tc.goWorkspace {
				writeParityFile(t, repo, "go.work", "go 1.24\n\nuse (\n  ./apps/api\n  ./apps/worker\n)\n")
				writeParityFile(t, repo, "apps/worker/go.mod", "module example.com/worker\ngo 1.24\n")
				writeParityFile(t, repo, "apps/worker/main.go", "package main\nfunc main() {}\n")
			}
			for name, body := range fixture.Files {
				writeRoot := member
				if fixture.SourceRoot != "" {
					writeRoot = repo
				}
				writeParityFile(t, writeRoot, name, body)
			}
			parityGit(t, repo, "add", "-A")
			parityGit(t, repo, "commit", "-q", "-m", "add catalog app")
			if tc.dirtyOverride {
				writeParityFile(t, member, "gregale.yaml", "hosting:\n  port: 8787\n  health: /ready\n")
			}
			sha := strings.TrimSpace(parityGit(t, repo, "rev-parse", "HEAD"))
			withCwd(t, repo)

			var created api.CreateAppRequest
			var uploaded []byte
			var sourceRoot string
			stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, z *zeroConfigStubServer) {
				switch {
				case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
					z.gotCalls["create"]++
					if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
						t.Errorf("decode app request: %v", err)
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
				case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodPost:
					z.gotCalls["deploy"]++
					mr, err := r.MultipartReader()
					if err != nil {
						t.Errorf("multipart reader: %v", err)
						http.Error(w, "bad multipart", http.StatusBadRequest)
						return
					}
					for {
						part, err := mr.NextPart()
						if errors.Is(err, io.EOF) {
							break
						}
						if err != nil {
							t.Errorf("next multipart part: %v", err)
							break
						}
						body, err := io.ReadAll(part)
						if err != nil {
							t.Errorf("read multipart part %q: %v", part.FormName(), err)
						}
						switch part.FormName() {
						case "source":
							uploaded = body
						case "source_root":
							sourceRoot = string(body)
						}
						_ = part.Close()
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "a1"})
				default:
					http.Error(w, "not found", http.StatusNotFound)
				}
			})
			t.Setenv("FAAS_API", stub.srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())

			args := []string{"--json", "--name", "demo", "--profile", "small"}
			selectedPath := ""
			if tc.workspace || tc.goWorkspace {
				selectedPath = "apps/api"
			}
			if fixture.SourceRoot != "" {
				selectedPath = fixture.SourceRoot
			}
			if selectedPath != "" {
				args = append(args, "--path", selectedPath)
			}
			if tc.worktree {
				args = append(args, "--worktree")
			}
			var plan simpleapp.Plan
			runParityCLI(t, append(append([]string{}, args...), "--plan"), &plan)
			if len(stub.gotCalls) != 0 {
				t.Fatalf("--plan contacted the API: %+v", stub.gotCalls)
			}
			var receipt DeployReceipt
			runParityCLI(t, append(append([]string{}, args...), "--no-wait"), &receipt)
			if stub.gotCalls["create"] != 1 || stub.gotCalls["deploy"] != 1 {
				t.Fatalf("deploy calls = %+v, want one create and upload", stub.gotCalls)
			}
			wantPort, wantHealth := fixture.Expected.Port, fixture.Expected.HealthPath
			if tc.wantPort != 0 {
				wantPort = tc.wantPort
			}
			if tc.wantHealth != "" {
				wantHealth = tc.wantHealth
			}
			if plan.Framework != fixture.Expected.Framework || plan.Port != wantPort || plan.HealthPath != wantHealth {
				t.Fatalf("plan profile = %+v, want %s :%d %s", plan, fixture.Expected.Framework, wantPort, wantHealth)
			}
			if created.Type != "app" || created.ExecutionMode != plan.ExecutionMode || created.HealthPath != plan.HealthPath || created.ResourceProfile != plan.ResourceProfile {
				t.Errorf("created app = %+v, differs from plan %+v", created, plan)
			}
			if receipt.ID != "d1" || receipt.CommitSHA != sha || receipt.SimpleAppPlan == nil {
				t.Fatalf("deployment receipt = %+v, want ID, commit, and simple app plan", receipt)
			}
			rp := receipt.SimpleAppPlan
			if rp.ResourceProfile != plan.ResourceProfile || rp.Port != plan.Port || rp.HealthPath != plan.HealthPath || rp.ExecutionMode != plan.ExecutionMode || rp.ScaleToZero != plan.ScaleToZero || rp.LocalStorage != plan.LocalStorage || rp.DurableState != plan.DurableState {
				t.Errorf("receipt plan = %+v, differs from preview %+v", rp, plan)
			}
			wantSourceRoot := tc.wantSourceRoot
			if fixture.SourceRoot != "" {
				wantSourceRoot = fixture.SourceRoot
			}
			if sourceRoot != wantSourceRoot || len(uploaded) == 0 {
				t.Fatalf("uploaded source root = %q, bytes = %d; want %q and nonempty source", sourceRoot, len(uploaded), wantSourceRoot)
			}
			digest := sha256.Sum256(uploaded)
			if receipt.SourceSHA256 != hex.EncodeToString(digest[:]) {
				t.Errorf("receipt source digest = %q, differs from uploaded bytes", receipt.SourceSHA256)
			}
			entries := readCapturedDeployArchive(t, uploaded)
			if fixture.SourceRoot != "" {
				for name, want := range fixture.Files {
					if got, ok := entries[name]; !ok || string(got) != want {
						t.Errorf("workspace upload file %q missing or changed (present=%t)", name, ok)
					}
				}
			}
			profile, err := frameworkprofile.Analyze(paritySelectedSource(entries, sourceRoot))
			if err != nil {
				t.Fatalf("analyze uploaded source: %v", err)
			}
			if profile.Framework != plan.Framework || profile.Port != plan.Port || profile.HealthPath != plan.HealthPath {
				t.Errorf("uploaded profile = %+v, differs from plan %+v", profile, plan)
			}
			_, shippedOverride := entries[path.Join(sourceRoot, "gregale.yaml")]
			wantConfigFile := fixture.Expected.ConfigFile != "" || (tc.worktree && tc.dirtyOverride)
			if shippedOverride != wantConfigFile {
				t.Errorf("uploaded hosting override present = %t, want %t", shippedOverride, wantConfigFile)
			}
			if tc.workspaceSibling != "" {
				if _, ok := entries[tc.workspaceSibling]; !ok {
					t.Errorf("workspace upload lost sibling %q", tc.workspaceSibling)
				}
			}
		})
	}
}

func writeParityFile(t *testing.T, dir, name, body string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func parityGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runParityCLI(t *testing.T, args []string, target any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &stdout, &stderr, true
	defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
	if code := cmdDeployTarball(args); code != 0 {
		t.Fatalf("gregale deploy %v: exit %d\nstderr: %s\nstdout: %s", args, code, stderr.String(), stdout.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), target); err != nil {
		t.Fatalf("gregale deploy %v: invalid JSON: %v\nstdout: %s", args, err, stdout.String())
	}
}

func paritySelectedSource(entries map[string][]byte, root string) fs.FS {
	selected := fstest.MapFS{}
	for name, body := range entries {
		if root != "" {
			if !strings.HasPrefix(name, root+"/") {
				continue
			}
			name = strings.TrimPrefix(name, root+"/")
		}
		selected[name] = &fstest.MapFile{Data: body, Mode: 0o644}
	}
	return selected
}
