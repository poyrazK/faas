package main

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dataapi/integration"
)

func hostedIntegrationFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"package.json": `{"name":"app","devDependencies":{"typescript":"5.9.3"}}`, "tsconfig.json": `{"compilerOptions":{"strict":true}}`, "src/app.ts": "export const keep = 1\n", "expected.json": `[{"id":1}]`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	hash := strings.Repeat("a", 64)
	receipt := dataAPIConnectReceipt{Version: 1, App: "notes-data", APIHash: hash, DatabaseID: "database", AppID: "app-id", BindingID: "binding-id", CredentialGeneration: 1, DeploymentID: "deployment-id", ConfigurationHash: hash, CollectionHash: hash, Output: filepath.Join(root, "old-types.ts"), NumericMode: "number", Fingerprint: hash, TypesHash: hash, Stage: "verified"}
	path := filepath.Join(root, "connect.json")
	if err := writeDataAPIConnectReceipt(path, receipt, true); err != nil {
		t.Fatal(err)
	}
	return root, path, filepath.Join(root, "expected.json")
}

func TestHostedIntegrationFreezesApprovedBundleBytes(t *testing.T) {
	root := t.TempDir()
	archive := []byte("approved-sdk")
	sum := sha512.Sum512(archive)
	manifest, _ := json.Marshal(map[string]any{"sdk": map[string]any{"file": "sdk.tgz", "bytes": len(archive), "sha256": dataAPIConnectHash(archive), "integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:])}})
	path := filepath.Join(root, "bundle.json")
	if err := os.WriteFile(path, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sdk.tgz"), archive, 0600); err != nil {
		t.Fatal(err)
	}
	frozen, cleanup, err := freezeHostedBundle(path, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.WriteFile(filepath.Join(root, "sdk.tgz"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(filepath.Dir(frozen), "sdk.tgz")); string(data) != string(archive) {
		t.Fatal("frozen archive changed with source")
	}
	if _, _, err := freezeHostedBundle(path, manifest); err == nil {
		t.Fatal("accepted changed archive")
	}
	bad := []byte(`{"sdk":{"file":"../secret.tgz","bytes":1,"sha256":"` + strings.Repeat("a", 64) + `"}}`)
	if _, _, err := freezeHostedBundle(path, bad); err == nil {
		t.Fatal("accepted unsafe archive path")
	}
}

func TestDataAPIIntegrateHostedPreviewIsOfflineAndRetainsApp(t *testing.T) {
	root, receipt, assertion := hostedIntegrationFixture(t)
	t.Setenv("FAAS_API", "https://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("NOTES_JWT", "")
	stdout, stderr, restore := swapIO(t)
	defer restore()
	code := cmdDataAPIIntegrate([]string{"--hosted", "notes-data", "--receipt", receipt, "--directory", root, "--sdk-version", "0.1.1", "--relation", "notes", "--order-by", "id", "--assertion", assertion, "--token-env", "NOTES_JWT", "--dry-run"})
	if code != 0 {
		t.Fatalf("preview returned %d: %s", code, stderr())
	}
	if !strings.Contains(stdout.String(), "Preview is offline") {
		t.Fatal("preview did not describe unverified remote state")
	}
	if _, err := os.Lstat(filepath.Join(root, integration.HostedClientSource)); !os.IsNotExist(err) {
		t.Fatal("preview wrote hosted client")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "src/app.ts")); string(data) != "export const keep = 1\n" {
		t.Fatal("preview changed app")
	}
}

func TestHostedIntegrationTypesNeverOverwriteDifferentFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, integration.HostedClientSource), 0755); err != nil {
		t.Fatal(err)
	}
	plan := &integration.Plan{Directory: root, GeneratedTypes: filepath.Join(integration.HostedClientSource, "database.types.ts")}
	if err := ensureHostedIntegrationTypes(plan, "original"); err != nil {
		t.Fatal(err)
	}
	if err := ensureHostedIntegrationTypes(plan, "original"); err != nil {
		t.Fatal(err)
	}
	if err := ensureHostedIntegrationTypes(plan, "replacement"); err == nil {
		t.Fatal("accepted changed types")
	}
	if data, _ := os.ReadFile(filepath.Join(root, plan.GeneratedTypes)); string(data) != "original" {
		t.Fatal("overwrote types")
	}
}

func TestHostedIntegrationRefusesExistingDifferentSDKDependency(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "package.json")
	content := `{"dependencies":{"@gregale/data":"0.2.0"}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	plan := &integration.Plan{Directory: root, ClientDirectory: "."}
	if err := checkHostedSDKDependency(plan, "0.1.1", false); err == nil {
		t.Fatal("accepted a different SDK dependency")
	}
	if data, _ := os.ReadFile(path); string(data) != content {
		t.Fatal("changed the existing package")
	}
}

func TestHostedIntegrationBundleResumeRejectsChangedVendoredArchive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0755); err != nil {
		t.Fatal(err)
	}
	plan := &integration.Plan{Directory: root, ClientDirectory: "."}
	manifest, _ := json.Marshal(map[string]any{"sdk": map[string]any{"sha256": dataAPIConnectHash([]byte("approved")), "bytes": 8}})
	vendor := filepath.Join(root, "vendor/gregale-data.tgz")
	if err := os.WriteFile(vendor, []byte("changed!"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkHostedBundleDestinations(plan, manifest, true); err == nil {
		t.Fatal("accepted changed archive")
	}
	if data, _ := os.ReadFile(vendor); string(data) != "changed!" {
		t.Fatal("overwrote archive")
	}
}
