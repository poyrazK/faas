package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

const customerJobStarter = "customer-operation-job-export"

func TestCustomerOperationJobExportStarter(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	var stdout, stderr bytes.Buffer
	if code := runCmdInit(customerJobStarter, dest, false, "", &stdout, &stderr); code != 0 {
		t.Fatalf("initialize: code=%d stderr=%s", code, stderr.String())
	}
	for _, file := range []string{"package.json", "gregale.yaml", "Dockerfile.job", "job.mjs", "export.mjs", "server.mjs", "public/app.mjs", "test/export.test.mjs", "test/fixture.mjs", "README.md"} {
		if _, err := os.Stat(filepath.Join(dest, file)); err != nil {
			t.Errorf("missing starter file %s: %v", file, err)
		}
	}
	report, err := validateCustomerOperationSource(customerOperationDeveloperCommand{dir: dest, app: "exports", plan: "pro", name: "customer-export"})
	if err != nil || len(report.Definitions) != 1 {
		t.Fatalf("validate generated feature: %+v %v", report, err)
	}
	spec := report.Definitions[0].Spec
	if spec.Job != "customer-export-job" || spec.Workflow != "" || spec.Owner != "platform_tenant" || spec.Recovery != "reconcile_on_unknown" || spec.Path != "/exports" {
		t.Fatalf("generated Job contract changed: %+v", spec)
	}
	contract, err := operations.Compile(spec, api.MustLimitsFor(api.PlanPro).Operations)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"rows":2,"artifact_id":"11111111-1111-4111-8111-111111111111"}`, true},
		{`{"rows":2,"csv":"private file required"}`, false},
		{`{"rows":2,"artifact_id":"unconfirmed"}`, false},
		{`{"rows":1001,"artifact_id":"11111111-1111-4111-8111-111111111111"}`, false},
	} {
		if err := contract.ValidateOutput([]byte(tc.body), 4096); (err == nil) != tc.valid {
			t.Errorf("result %s: valid=%t err=%v", tc.body, tc.valid, err)
		}
	}
	if templates.CategoryFor(customerJobStarter) != "operations" || !strings.Contains(stdout.String(), "Dockerfile.job") || !strings.Contains(stdout.String(), "--plan pro") {
		t.Fatalf("missing discovery/setup guidance: %s", stdout.String())
	}
}

func TestCustomerOperationJobExportRequiresPreparedSource(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	var stdout, stderr bytes.Buffer
	if code := runCmdInit(customerJobStarter, dest, true, "exports", &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "local SDK bundle") {
		t.Fatalf("unprepared initialization: code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("rejected initialization wrote source: %v", err)
	}
	if code := cmdDeployTarball([]string{"--template", customerJobStarter, "--name", "exports"}); code != 1 {
		t.Fatalf("unprepared direct deployment returned %d", code)
	}
}

func TestCustomerOperationJobExportPackRetainsSDKAndJobImageInputs(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	if err := templates.Materialize(customerJobStarter, dest); err != nil {
		t.Fatal(err)
	}
	for file, value := range map[string]string{"packages/gregale-sdk.tgz": "packed SDK", "package-lock.json": "{}", "node_modules/installed.js": "installed"} {
		path := filepath.Join(dest, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(t.TempDir(), "source.tar.gz")
	if _, err := packDirToTarGz(dest, archive, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatal(err)
	}
	f, err := openCustomerFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	reader, entries := tar.NewReader(gz), map[string]bool{}
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(h.Name, "/node_modules/") {
			t.Fatalf("packed installed dependencies: %s", h.Name)
		}
		entries[h.Name] = true
	}
	for _, path := range []string{"packages/gregale-sdk.tgz", "package-lock.json", "Dockerfile.job", "job.mjs", "contract.mjs"} {
		if !entries["feature/"+path] {
			t.Errorf("source pack discarded %s", path)
		}
	}
}

func TestCustomerOperationJobExportExampleMatchesEmbeddedStarter(t *testing.T) {
	err := fs.WalkDir(templates.FS, customerJobStarter, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want, err := templates.FS.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join("..", "..", "examples", filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if !bytes.Equal(want, got) {
			t.Errorf("example drifted from embedded starter: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCustomerOperationJobExportDiscovery(t *testing.T) {
	var stdout bytes.Buffer
	if code := runCmdInitList(&stdout); code != 0 || !strings.Contains(stdout.String(), customerJobStarter) {
		t.Fatalf("starter missing from init catalog: %s", stdout.String())
	}
	found := false
	for _, name := range templateNames13 {
		found = found || name == customerJobStarter
	}
	if !found || docsURLForTemplate(customerJobStarter) != "https://gregale.dev/docs/operations" {
		t.Fatal("starter missing from CLI metadata or docs")
	}
}
