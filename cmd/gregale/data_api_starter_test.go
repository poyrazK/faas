package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func TestDataAPIStarterScaffoldIncludesApplicationWorkflows(t *testing.T) {
	resetJSONOut(t)
	dest := filepath.Join(t.TempDir(), "notes")
	var out, stderr bytes.Buffer
	if code := runCmdInit("data-api-starter", dest, false, "", &out, &stderr); code != 0 {
		t.Fatalf("init exit=%d: %s", code, stderr.String())
	}
	for _, file := range []string{
		"README.md", "Procfile", "package-lock.json", "data-api.json", ".gitignore", ".gregaleignore",
		"client/package-lock.json", "client/src/database.types.ts", "client/test/authorization.mjs",
		"tools/artifacts.mjs", "tools/artifact-lib.mjs",
		".github/workflows/data-api-client.yml", ".github/workflows/data-api-preview.yml",
	} {
		if content, err := os.ReadFile(filepath.Join(dest, file)); err != nil || len(content) == 0 {
			t.Fatalf("scaffold missing %s: %v", file, err)
		}
	}
	for _, name := range []string{"client", "preview"} {
		workflow, _ := os.ReadFile(filepath.Join(dest, ".github/workflows/data-api-"+name+".yml"))
		source, _ := os.ReadFile(filepath.Join(dest, "ci/"+name+".yml"))
		if !bytes.Equal(workflow, source) {
			t.Fatalf("scaffolded %s workflow differs from embedded source", name)
		}
	}
	manifest, err := loadDevManifest(dest)
	if err != nil || manifest == nil || manifest.Hosting == nil || manifest.Hosting.Health != "/healthz" || manifest.Release == nil || manifest.Release.Command != "node migrations/migrate.mjs" {
		t.Fatalf("starter must declare its readiness endpoint and release migration: %+v, %v", manifest, err)
	}
	configFile, _ := os.ReadFile(filepath.Join(dest, "data-api.json"))
	var config dataAPISyncConfig
	if err := json.Unmarshal(configFile, &config); err != nil || config.Output != "client/src/database.types.ts" || strings.Join(config.Check.Command, " ") != "npm run typecheck" {
		t.Fatalf("scaffold cannot use the sync workflow: %+v, %v", config, err)
	}
	if !strings.Contains(out.String(), "data-api sync") || !strings.Contains(out.String(), "tools/artifacts.mjs pin") || docsURLForTemplate("data-api-starter") != "https://gregale.dev/docs/data-api" {
		t.Fatal("scaffold did not explain the Data API next steps")
	}
}

func TestDataAPIStarterUploadKeepsMigrationAndExcludesClientTools(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "notes")
	if err := templates.Materialize("data-api-starter", dest); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dest, ".gregale-tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{".gregale-tools/gregale", "data-api-artifacts.json"} {
		if err := os.WriteFile(filepath.Join(dest, file), []byte("owner tooling"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "migration.tar.gz")
	if _, err := packDirToTarGz(dest, archive, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatal(err)
	}
	entries := tarEntries(t, archive)
	for _, file := range []string{"Dockerfile", "Procfile", "gregale.yaml", "package.json", "package-lock.json", "server.mjs", "migrations/migrate.mjs", "migrations/sql/0001_notes.sql", "migrations/sql/0002_priority.sql", "migrations/sql/0003_relationships.sql", "migrations/sql/0004_tags.sql", "migrations/sql/0005_favorite_tags.sql"} {
		if !entries["notes/"+file] {
			t.Errorf("migration upload missing %s", file)
		}
	}
	for name := range entries {
		for _, excluded := range []string{"notes/client/", "notes/tools/", "notes/ci/", "notes/test/", "notes/.github/", "notes/.gregale-tools/", "notes/data-api-artifacts.json"} {
			if strings.HasPrefix(name, excluded) {
				t.Errorf("migration upload included application-only file %s", name)
			}
		}
	}
}
