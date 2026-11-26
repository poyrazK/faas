package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/dataapi/integration"
)

func app(t *testing.T, client string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, client, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"package.json":  `{"name":"existing-app","private":true,"scripts":{"dev":"vite","test":"existing-test"},"devDependencies":{"typescript":"5.9.3"}}`,
		"src/app.ts":    "export const existing = 42\n",
		"tsconfig.json": `{"compilerOptions":{"strict":true}}`,
	} {
		if err := os.WriteFile(filepath.Join(root, client, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPlanAndApplyRetainExistingApplication(t *testing.T) {
	for _, client := range []string{".", "web app"} {
		t.Run(client, func(t *testing.T) {
			root := app(t, client)
			before := map[string][]byte{}
			for _, name := range []string{"package.json", "tsconfig.json", "src/app.ts"} {
				before[name], _ = os.ReadFile(filepath.Join(root, client, name))
			}
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/"), 0o640); err != nil {
				t.Fatal(err)
			}
			plan, err := integration.Build(root, client)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "gregale-data")); !os.IsNotExist(err) {
				t.Fatal("planning wrote files")
			}
			if err := plan.Apply(); err != nil {
				t.Fatal(err)
			}
			for name, want := range before {
				got, _ := os.ReadFile(filepath.Join(root, client, name))
				if string(got) != string(want) {
					t.Fatalf("changed existing %s", name)
				}
			}
			ignore, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
			if string(ignore) != "node_modules/\n/.gregale/data-api-dev-*.json\n" {
				t.Fatal(string(ignore))
			}
			info, _ := os.Stat(filepath.Join(root, ".gitignore"))
			if info.Mode().Perm() != 0o640 {
				t.Fatal("changed ignore permissions")
			}
			config, _ := os.ReadFile(filepath.Join(root, integration.Config))
			var value map[string]any
			if err := json.Unmarshal(config, &value); err != nil {
				t.Fatal(err)
			}
			if value["output"] != filepath.ToSlash(filepath.Join(client, integration.ClientSource, "database.types.ts")) {
				t.Fatal(value)
			}
			if _, err := os.Stat(filepath.Join(root, plan.GeneratedTypes)); !os.IsNotExist(err) {
				t.Fatal("fabricated database declarations")
			}
			if _, err := integration.Build(root, client); err == nil {
				t.Fatal("accepted existing integration")
			}
		})
	}
}

func TestConflictAndSymlinkPreflightNeverOverwritesFiles(t *testing.T) {
	for _, name := range []string{"gregale-data", "src/gregale-data", "data-api-artifacts.json", "vendor/gregale-data.tgz"} {
		t.Run(name, func(t *testing.T) {
			root := app(t, ".")
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, name), []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := integration.Build(root, "."); err == nil {
				t.Fatal("accepted conflict")
			}
			got, _ := os.ReadFile(filepath.Join(root, name))
			if string(got) != "keep" {
				t.Fatal("overwrote conflict")
			}
		})
	}
	root := app(t, ".")
	if err := os.RemoveAll(filepath.Join(root, "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "src")); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Build(root, "."); err == nil {
		t.Fatal("accepted escaping source symlink")
	}
}

func TestStalePlanFailsBeforeWriting(t *testing.T) {
	root := app(t, ".")
	plan, err := integration.Build(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("new user change"), 0o644); err != nil {
		t.Fatal(err)
	}
	if plan.Apply() == nil {
		t.Fatal("accepted stale plan")
	}
	if _, err := os.Stat(filepath.Join(root, "gregale-data")); !os.IsNotExist(err) {
		t.Fatal("wrote part of stale plan")
	}
}

func TestUnsupportedPackagesAndEscapingClientsFail(t *testing.T) {
	for _, metadata := range []string{`{}`, `{"devDependencies":{"typescript":"5"},"packageManager":"pnpm@10"}`} {
		root := app(t, ".")
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(metadata), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := integration.Build(root, "."); err == nil {
			t.Fatal("accepted unsupported package")
		}
	}
	root := app(t, ".")
	for _, client := range []string{"../outside", root, ""} {
		if _, err := integration.Build(root, client); err == nil {
			t.Fatalf("accepted %q", client)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), []byte("lockfileVersion: 9"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Build(root, "."); err == nil {
		t.Fatal("accepted non-npm lock")
	}
}

func TestNPMWorkspaceRootCanBeTheClient(t *testing.T) {
	root := app(t, ".")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"workspaces":["apps/*"],"devDependencies":{"typescript":"5.9.3"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Build(root, "."); err != nil {
		t.Fatal(err)
	}
}

func TestNPMWorkspaceIntegrationAndResume(t *testing.T) {
	root := app(t, "apps/web")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"workspaces":["apps/*"],"devDependencies":{"typescript":"5.9.3"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps/web/package.json"), []byte(`{"name":"web","private":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := integration.BuildWithNumericMode(root, "apps/web", "exact")
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorkspaceRoot != "." {
		t.Fatalf("workspace root = %q", plan.WorkspaceRoot)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(root, "apps/web", integration.ClientSource, "client.ts")
	if err := os.WriteFile(client, []byte("// developer edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, integration.Config)
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	config["migrations"] = "custom-migrations"
	configBytes, _ = json.Marshal(config)
	if err := os.WriteFile(configPath, configBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	resumed, err := integration.Resume(root, "apps/web", "exact")
	if err != nil || !resumed.Resume || len(resumed.Files) != 0 {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	content, err := os.ReadFile(client)
	if err != nil || string(content) != "// developer edit\n" {
		t.Fatal("resume changed the client")
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != string(configBytes) {
		t.Fatal("resume changed the configuration")
	}
	if _, err := integration.Resume(root, "apps/web", "number"); err == nil {
		t.Fatal("resumed with the wrong numeric mode")
	}
	if err := os.Remove(filepath.Join(root, integration.Requests)); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Resume(root, "apps/web", "exact"); err == nil {
		t.Fatal("resumed with missing setup")
	}
}

func TestNPMWorkspaceRejectsNonMember(t *testing.T) {
	root := app(t, "apps/web")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"workspaces":["packages/*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.Build(root, "apps/web"); err == nil {
		t.Fatal("accepted a directory outside the workspace patterns")
	}
}

func TestNPMWorkspaceObjectConfiguration(t *testing.T) {
	root := app(t, "apps/web")
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"workspaces":{"packages":["apps/*"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := integration.Build(root, "apps/web")
	if err != nil || plan.WorkspaceRoot != "." {
		t.Fatalf("workspace = %+v, error = %v", plan, err)
	}
}

func TestNewGeneratedTypeFileInvalidatesPlan(t *testing.T) {
	root := app(t, ".")
	plan, err := integration.Build(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, plan.GeneratedTypes)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, plan.GeneratedTypes), []byte("user content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if plan.Apply() == nil {
		t.Fatal("accepted a type-generation destination created after planning")
	}
	content, _ := os.ReadFile(filepath.Join(root, plan.GeneratedTypes))
	if string(content) != "user content" {
		t.Fatal("overwrote user's types")
	}
}

func TestExactNumericModeIsRecordedInTheIntegrationPlan(t *testing.T) {
	root := app(t, ".")
	plan, err := integration.BuildWithNumericMode(root, ".", "exact")
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, integration.Config))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		NumericMode string `json:"numeric_mode"`
	}
	if err := json.Unmarshal(content, &config); err != nil || config.NumericMode != "exact" {
		t.Fatalf("numeric mode = %q, parse: %v", config.NumericMode, err)
	}
}
