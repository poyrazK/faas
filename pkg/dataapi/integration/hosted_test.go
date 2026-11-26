package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dataapi/integration"
)

func hostedSpec() integration.HostedSpec {
	return integration.HostedSpec{AppID: "app-1", DeploymentID: "deployment-1", APIHash: strings.Repeat("d", 64), Relation: "notes", OrderBy: "id", Fingerprint: strings.Repeat("a", 64), NumericMode: "exact", InstallKey: strings.Repeat("b", 64), AssertionHash: strings.Repeat("c", 64)}
}

func TestHostedPlanPreservesAppAndResumesOnlyUnchangedScaffold(t *testing.T) {
	root := app(t, ".")
	original, err := os.ReadFile(filepath.Join(root, "src/app.ts"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := integration.BuildHosted(root, ".", hostedSpec(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, integration.HostedClientSource)); !os.IsNotExist(err) {
		t.Fatal("preview wrote files")
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	client, err := os.ReadFile(filepath.Join(root, integration.HostedClientSource, "client.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`.schema('public')`, `.from("notes")`, `.order("id")`, `.limit(100)`, `expectedSchemaFingerprint`, `"exact"`} {
		if !strings.Contains(string(client), value) {
			t.Fatalf("client missing %s", value)
		}
	}
	if strings.Contains(string(client), ".insert(") {
		t.Fatal("client includes write operations")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "src/app.ts")); string(got) != string(original) {
		t.Fatal("changed the existing application")
	}
	if _, err := integration.BuildHosted(root, ".", hostedSpec(), true); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(root, integration.HostedClientSource, "README.md")
	if err := os.WriteFile(readme, []byte("Developer notes\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.BuildHosted(root, ".", hostedSpec(), true); err != nil {
		t.Fatal("resume rejected developer notes", err)
	}
	if err := os.WriteFile(filepath.Join(root, integration.HostedClientSource, "client.ts"), []byte("// developer edit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.BuildHosted(root, ".", hostedSpec(), true); err == nil {
		t.Fatal("accepted modified scaffold")
	}
	if got, _ := os.ReadFile(filepath.Join(root, integration.HostedClientSource, "client.ts")); string(got) != "// developer edit\n" {
		t.Fatal("rewrote developer edits")
	}
}

func TestHostedPlanRejectsInvalidInputsAndConflicts(t *testing.T) {
	for _, mutate := range []func(*integration.HostedSpec){func(s *integration.HostedSpec) { s.Relation = "notes;drop" }, func(s *integration.HostedSpec) { s.OrderBy = "x/y" }, func(s *integration.HostedSpec) { s.Fingerprint = "bad" }, func(s *integration.HostedSpec) { s.InstallKey = "bad" }} {
		root := app(t, ".")
		spec := hostedSpec()
		mutate(&spec)
		if _, err := integration.BuildHosted(root, ".", spec, false); err == nil {
			t.Fatal("accepted invalid hosted input")
		}
	}
	root := app(t, ".")
	if err := os.MkdirAll(filepath.Join(root, integration.HostedClientSource), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.BuildHosted(root, ".", hostedSpec(), false); err == nil {
		t.Fatal("accepted destination conflict")
	}
}

func TestHostedPlanSelectsNPMWorkspaceAndChecksItsTSConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps/web/src"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"package.json":           `{"private":true,"workspaces":["apps/*"],"devDependencies":{"typescript":"5.9.3"}}`,
		"apps/web/package.json":  `{"name":"web","private":true}`,
		"apps/web/tsconfig.json": `{"compilerOptions":{"strict":true}}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := integration.BuildHosted(root, "apps/web", hostedSpec(), false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorkspaceRoot != "." || plan.GeneratedTypes != "apps/web/src/gregale-hosted/database.types.ts" {
		t.Fatalf("unexpected workspace plan: %+v", plan)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.BuildHosted(root, "apps/web", hostedSpec(), true); err != nil {
		t.Fatal(err)
	}
}
