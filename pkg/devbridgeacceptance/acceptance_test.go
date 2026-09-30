package devbridgeacceptance

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// adr: 379
func TestNativeAcceptanceRejectsUnsafeConfiguration(t *testing.T) {
	valid := Config{API: "https://api.gregale.example", Token: "test", Project: "bridge-fixture", Environment: "development", Payments: "payments", Frontend: "frontend", Inventory: "inventory", IdleFor: time.Minute}
	if err := validate(valid); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://127.0.0.1:8080", "https://user:password@api.example", "https://api.example?token=secret"} {
		config := valid
		config.API = endpoint
		if validate(config) == nil {
			t.Fatalf("unsafe native endpoint accepted: %s", endpoint)
		}
	}
	config := valid
	config.IdleFor = api.DevBridgeSessionTTL
	if validate(config) == nil {
		t.Fatal("idle interval consumed the cleanup margin")
	}
}

// adr: 379 — scan the actual packaged artifact, including command roles and
// service bindings, so the native guide cannot deploy three identical roles.
func TestNativeAcceptanceFixtureArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture packager requires bash")
	}
	archive := filepath.Join(t.TempDir(), "fixture.tar.gz")
	command := exec.CommandContext(t.Context(), "bash", "../../scripts/ci/package-dev-bridge-fixture.sh", archive)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("package fixture: %v %s", err, output)
	}
	file, err := os.Open(archive) //nolint:forbidigo // Owned test archive in t.TempDir; no customer path or upload.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = compressed.Close() }()
	reader := tar.NewReader(compressed)
	fsys := fstest.MapFS{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.ToSlash(filepath.Clean(header.Name))
		fsys[name] = &fstest.MapFile{Data: body, Mode: 0o644}
	}
	scan, err := reposcan.Scan(fsys)
	if err != nil || len(scan.Workloads) != 3 {
		t.Fatalf("fixture scan: %+v %v", scan, err)
	}
	for _, workload := range scan.Workloads {
		if len(workload.Command) != 3 || workload.Command[2] != workload.Name || workload.Dockerfile != "tests/dev-bridge-acceptance/Dockerfile" {
			t.Fatalf("fixture role/build lost: %+v", workload)
		}
		if workload.Name != "inventory" && len(workload.DependsOn) != 1 {
			t.Fatalf("service binding lost: %+v", workload)
		}
	}
}

// adr: 379
func TestNativeAcceptanceRequiresLiveFixtureRevision(t *testing.T) {
	releases := api.ProjectEnvironmentReleaseListResponse{Workloads: []api.ProjectEnvironmentReleaseWorkloadResponse{{WorkloadSlug: "payments", Status: "pending", DeploymentID: "deployment", URL: "https://payments.example"}}}
	if _, err := fixtureMember(releases, "payments"); err == nil {
		t.Fatal("pending revision accepted as native evidence")
	}
	releases.Workloads[0].Status = "live"
	if _, err := fixtureMember(releases, "payments"); err != nil {
		t.Fatal(err)
	}
}
