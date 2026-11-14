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
	for _, metadata := range []string{`{}`, `{"devDependencies":{"typescript":"5"},"packageManager":"pnpm@10"}`, `{"devDependencies":{"typescript":"5"},"workspaces":["web"]}`} {
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
