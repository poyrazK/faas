// adr: 672
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

const customerWorkflowStarter = "customer-operation-workflow-export"

func TestCustomerOperationWorkflowExportStarter(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	var stdout, stderr bytes.Buffer
	if code := runCmdInit(customerWorkflowStarter, dest, false, "", &stdout, &stderr); code != 0 {
		t.Fatalf("initialize: code=%d stderr=%s", code, stderr.String())
	}
	for _, file := range []string{"package.json", "gregale.yaml", "server.mjs", "artifact.mjs", "public/app.mjs", "public/progress.mjs", "schemas/export-input.json", "schemas/export-output.json", "test/artifact.test.mjs", "test/export.test.mjs", "README.md", "RECOVERY.md"} {
		if _, err := os.Stat(filepath.Join(dest, file)); err != nil {
			t.Errorf("missing starter file %s: %v", file, err)
		}
	}
	report, err := validateCustomerOperationSource(customerOperationDeveloperCommand{dir: dest, app: "exports", plan: "hobby", name: "customer-export"})
	if err != nil || len(report.Definitions) != 1 {
		t.Fatalf("validate generated feature: %+v %v", report, err)
	}
	spec := report.Definitions[0].Spec
	if spec.Workflow != "export-chain" || spec.Job != "" || spec.Owner != "platform_tenant" || spec.Recovery != "reconcile_on_unknown" || spec.Path != "/exports" || !slices.Equal(spec.ProgressStages, []string{"collect", "transform", "finish"}) {
		t.Fatalf("generated workflow contract changed: %+v", spec)
	}
	contract, err := operations.Compile(spec, api.MustLimitsFor(api.PlanHobby).Operations)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input string
		valid bool
	}{{`{"count":3}`, true}, {`{"count":101}`, false}, {`{"count":0}`, false}, {`{"count":3,"owner":"other"}`, false}} {
		if err := contract.ValidateInput([]byte(tc.input), 4096); (err == nil) != tc.valid {
			t.Errorf("input %s: valid=%t err=%v", tc.input, tc.valid, err)
		}
	}
	for _, tc := range []struct {
		output string
		valid  bool
	}{{`{"rows":3,"artifact_id":"44444444-4444-4444-8444-444444444444"}`, true}, {`{"rows":3,"csv":"unconfirmed"}`, false}, {`{"rows":101,"artifact_id":"44444444-4444-4444-8444-444444444444"}`, false}, {`{"rows":3,"artifact_id":"missing"}`, false}} {
		if err := contract.ValidateOutput([]byte(tc.output), 4096); (err == nil) != tc.valid {
			t.Errorf("output %s: valid=%t err=%v", tc.output, tc.valid, err)
		}
	}
	if !strings.Contains(stdout.String(), "packages/gregale-sdk.tgz") || !strings.Contains(stdout.String(), "RECOVERY.md") || !strings.Contains(stdout.String(), "--plan hobby") {
		t.Fatalf("missing setup/recovery guidance: %s", stdout.String())
	}
}

func TestCustomerOperationWorkflowExportRequiresPreparedSource(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	var stdout, stderr bytes.Buffer
	if code := runCmdInit(customerWorkflowStarter, dest, true, "exports", &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "local SDK bundle") {
		t.Fatalf("unprepared initialization: code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected initialization wrote source: %v", err)
	}
	if code := cmdDeployTarball([]string{"--template", customerWorkflowStarter, "--name", "exports"}); code != 1 {
		t.Fatalf("unprepared deployment returned %d", code)
	}
}

func TestCustomerOperationWorkflowExportPackRetainsSDKAndContract(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "feature")
	if err := templates.Materialize(customerWorkflowStarter, dest); err != nil {
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
		if errors.Is(err, io.EOF) {
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
	for _, path := range []string{"packages/gregale-sdk.tgz", "package-lock.json", "gregale.yaml", "schemas/export-output.json", "artifact.mjs", "public/progress.mjs", "RECOVERY.md"} {
		if !entries["feature/"+path] {
			t.Errorf("source pack discarded %s", path)
		}
	}
}

func TestCustomerOperationWorkflowExportExampleMatchesEmbeddedStarter(t *testing.T) {
	err := fs.WalkDir(templates.FS, customerWorkflowStarter, func(path string, d fs.DirEntry, err error) error {
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

func TestCustomerOperationWorkflowExportDiscovery(t *testing.T) {
	var stdout bytes.Buffer
	if code := runCmdInitList(&stdout); code != 0 || !strings.Contains(stdout.String(), customerWorkflowStarter) || templates.CategoryFor(customerWorkflowStarter) != "operations" {
		t.Fatalf("starter missing from init catalog: %s", stdout.String())
	}
	if !slices.Contains(templateNames13, customerWorkflowStarter) || docsURLForTemplate(customerWorkflowStarter) != "https://gregale.dev/docs/operations" {
		t.Fatal("starter missing from CLI metadata or docs")
	}
}
