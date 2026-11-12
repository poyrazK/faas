package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/projectconfig"
)

const legacyProjectYAML = `# services that only the gateway calls
service_policy: declared
services:
  billing:
    visibility: internal # not public
    depends_on: [ledger]
`

func writeProjectMigrationFixture(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gregale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gregale", "project.yaml"), []byte(legacyProjectYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ADR-20261107180000: migrate keeps the existing manifest byte for byte,
// appends the block, and removes the legacy file.
func TestMigrateProjectConfigAppendsToExistingManifest(t *testing.T) {
	existing := "# my app\nbuckets:\n  - name: uploads   # keep this comment\n"
	dir := writeProjectMigrationFixture(t, existing)
	result, err := migrateProjectConfig(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.CreatedFile || !result.RemovedLegacy {
		t.Fatalf("result = %+v", result)
	}
	body, err := os.ReadFile(filepath.Join(dir, "gregale.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), existing) {
		t.Fatalf("existing manifest changed:\n%s", body)
	}
	if !strings.Contains(string(body), "    visibility: internal # not public") {
		t.Fatalf("legacy comments not carried over:\n%s", body)
	}
	cfg, found, err := projectconfig.FromManifest(body)
	if err != nil || !found || cfg.ServicePolicy != "declared" || cfg.Services["billing"].Visibility != "internal" {
		t.Fatalf("migrated block = %+v found=%v err=%v", cfg, found, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gregale", "project.yaml")); !os.IsNotExist(err) {
		t.Fatalf("legacy file still present: %v", err)
	}
}

func TestMigrateProjectConfigCreatesManifest(t *testing.T) {
	dir := writeProjectMigrationFixture(t, "")
	result, err := migrateProjectConfig(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CreatedFile {
		t.Fatalf("result = %+v, want a created manifest", result)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "gregale.yaml"))
	if cfg, found, err := projectconfig.FromManifest(body); err != nil || !found || len(cfg.Services) != 1 {
		t.Fatalf("created manifest block = %+v found=%v err=%v\n%s", cfg, found, err, body)
	}
}

func TestMigrateProjectConfigDryRunWritesNothing(t *testing.T) {
	dir := writeProjectMigrationFixture(t, "labels:\n  team: core\n")
	result, err := migrateProjectConfig(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "\nproject:\n  # services that only the gateway calls\n  service_policy: declared\n") {
		t.Fatalf("dry-run content:\n%s", result.Content)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "gregale.yaml")); string(body) != "labels:\n  team: core\n" {
		t.Fatalf("dry run wrote the manifest:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gregale", "project.yaml")); err != nil {
		t.Fatalf("dry run removed the legacy file: %v", err)
	}
}

func TestMigrateProjectConfigRefusesExistingBlock(t *testing.T) {
	dir := writeProjectMigrationFixture(t, "project:\n  service_policy: account\n")
	if _, err := migrateProjectConfig(dir, false); err == nil || !strings.Contains(err.Error(), "already has a `project:` block") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gregale", "project.yaml")); err != nil {
		t.Fatalf("refused migration removed the legacy file: %v", err)
	}
}

func TestMigrateProjectConfigWithoutLegacyFile(t *testing.T) {
	if _, err := migrateProjectConfig(t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "nothing to migrate") {
		t.Fatalf("err = %v", err)
	}
}
