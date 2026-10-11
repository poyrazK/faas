package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func TestCustomerOperationExportPackRetainsSDK(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	if err := templates.Materialize("customer-operation-export", dest); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dest, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(body, &pkg); err != nil {
		t.Fatal(err)
	}
	relative := strings.TrimPrefix(pkg.Dependencies["@gregale/sdk-node"], "file:")
	if relative != "packages/gregale-sdk.tgz" {
		t.Fatalf("SDK dependency must live in a packed source directory: %q", relative)
	}
	writeFile(t, dest, relative, "local SDK archive")
	writeFile(t, dest, "node_modules/@gregale/sdk-node/dist/index.js", "installed build output")
	archive := filepath.Join(t.TempDir(), "source.tar.gz")
	if _, err := packDirToTarGz(dest, archive, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatal(err)
	}
	entries := tarEntries(t, archive)
	if !entries["feature/"+relative] {
		t.Fatal("source packing discarded the SDK needed by the remote builder")
	}
	for entry := range entries {
		if strings.Contains(entry, "/node_modules/") {
			t.Fatalf("source packing retained installed build output: %s", entry)
		}
	}
}

// adr: 521 — a scaffold must retain the immutable HTTP contract and require
// its local SDK dependency before any attempted customer deployment.
func TestCustomerOperationExportStarter(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	var stdout, stderr bytes.Buffer
	if code := runCmdInit("customer-operation-export", dest, false, "", &stdout, &stderr); code != 0 {
		t.Fatalf("initialize: code=%d stderr=%s", code, stderr.String())
	}
	for _, file := range []string{"package.json", "gregale.yaml", "server.mjs", "export.mjs", "public/app.mjs", "public/index.html", "schemas/export-input.json", "schemas/export-output.json", "test/export.test.mjs", "README.md"} {
		if _, err := os.Stat(filepath.Join(dest, file)); err != nil {
			t.Errorf("missing starter file %s: %v", file, err)
		}
	}
	sample := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(sample, []byte(`{"count":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := validateCustomerOperationSource(customerOperationDeveloperCommand{dir: dest, app: "exports", plan: "hobby", name: "customer-export", input: sample})
	if err != nil || len(report.Definitions) != 1 {
		t.Fatalf("validate generated feature: %+v %v", report, err)
	}
	def := report.Definitions[0]
	if !def.InputValidated || def.Spec.Owner != "platform_tenant" || def.Spec.Recovery != "reconcile_on_unknown" || def.Spec.Path != "/exports" {
		t.Fatalf("generated HTTP contract changed: %+v", def)
	}
	contract, err := operations.Compile(def.Spec, api.MustLimitsFor(api.PlanHobby).Operations)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []struct {
		body  string
		valid bool
	}{
		{`{"csv":"id,value\n1,10\n","rows":1}`, false},
		{`{"artifact_id":"11111111-1111-4111-8111-111111111111","rows":1}`, true},
		{`{"rows":1}`, false},
		{`{"artifact_id":"unconfirmed","rows":1}`, false},
		{`{"artifact_id":"11111111-1111-4111-8111-111111111111","csv":"ambiguous","rows":1}`, false},
	} {
		if err := contract.ValidateOutput([]byte(result.body), 4096); (err == nil) != result.valid {
			t.Errorf("starter result %s: valid=%t error=%v", result.body, result.valid, err)
		}
	}
	if !strings.Contains(stdout.String(), "packages/gregale-sdk.tgz") {
		t.Fatalf("missing SDK setup instructions: %s", stdout.String())
	}
	if templates.CategoryFor("customer-operation-export") != "operations" {
		t.Fatal("Operations starter missing from discovery")
	}
}

func TestCustomerOperationExportInitDeployRequiresPreparedSource(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	var stdout, stderr bytes.Buffer
	if code := runCmdInit("customer-operation-export", dest, true, "exports", &stdout, &stderr); code != 1 {
		t.Fatalf("unprepared deploy returned %d", code)
	}
	if !strings.Contains(stderr.String(), "local SDK bundle") {
		t.Fatalf("missing preparation error: %s", stderr.String())
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("rejected deploy wrote source: %v", err)
	}
}

func TestCustomerOperationExportDirectDeployRequiresPreparedSource(t *testing.T) {
	_, readStderr, restore := swapIO(t)
	defer restore()
	if code := cmdDeployTarball([]string{"--template", "customer-operation-export", "--name", "exports"}); code != 1 {
		t.Fatalf("unprepared deploy returned %d", code)
	}
	stderr := readStderr()
	if !strings.Contains(stderr, "install the local SDK bundle") {
		t.Fatalf("did not reject before authentication: %s", stderr)
	}
}
