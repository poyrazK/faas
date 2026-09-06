// Whitebox tests for BuildPlan population on DeploymentResponse
// (cmd/apid/handlers_ext.go:deploymentResponse, issue #961 / Mega-A PR-2).
//
// Pins the wire shape of the auto-detected BuildPlan block:
//   - app kind + framework + version populate from SourcePath
//   - function kind with no SourcePath → BuildPlan is nil (omit on wire)
//   - tarball with no recognised marker → Framework="unknown", Version=""
//   - per-deployment override_entrypoint + override_port echo verbatim
//
// The handler calls markers.DetectFromTarball directly (not the
// builderd shim) so FrameworkUnknown is a NON-error graceful
// degradation; pre-PR-2 callers saw BuildPlan=nil because the
// field didn't exist. Pre-PR-2 wire-equal callers see no diff
// (omitempty on the *BuildPlan pointer).

package main

import (
	"archive/tar"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeTarball creates a gzipped tarfile at `dir/file` containing the
// given entries + bodies. Returns the absolute path. Used by the
// BuildPlan tests to feed a real on-disk tarball to
// deploymentResponse — a zero-byte path is the "no SourcePath"
// sentinel an image deploy carries.
func writeTarball(t *testing.T, entries []tar.Header, bodies map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create tarball: %v", err)
	}
	defer func() { _ = f.Close() }()
	buildTestTarGzTo(t, f, entries, bodies)
	return path
}

// buildTestTarGzTo wraps buildTestTarGz's in-memory output and writes
// it to the given file. The existing buildTestTarGz returns []byte; we
// hand it to os.File.Write to get a real path on disk.
func buildTestTarGzTo(t *testing.T, f *os.File, entries []tar.Header, bodies map[string][]byte) {
	t.Helper()
	if _, err := f.Write(buildTestTarGz(t, entries, bodies)); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
}

// TestDeploymentResponse_BuildPlan_AppWithFramework: when SourcePath
// points at a tarball with package.json, deploymentResponse surfaces
// BuildPlan.Framework="node" + Class="app" + the resolved version.
func TestDeploymentResponse_BuildPlan_AppWithFramework(t *testing.T) {
	srv := newServer(state.NewMemStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	tarPath := writeTarball(t,
		[]tar.Header{
			{Name: "package.json"},
			{Name: "index.js"},
		},
		map[string][]byte{
			"package.json": []byte(`{"name":"x","engines":{"node":"22.11.0"}}`),
			"index.js":     []byte("exports.handler=()=>0;"),
		},
	)
	d := state.Deployment{
		ID: "d1", AppID: "a1", Kind: state.DeploymentKindTarball,
		SourcePath: tarPath, Status: state.DeployPending,
	}
	app := state.App{ID: "a1", Type: state.AppTypeApp}
	resp := srv.deploymentResponse(d, app)
	if resp.BuildPlan == nil {
		t.Fatalf("BuildPlan = nil; want populated")
	}
	if resp.BuildPlan.Framework != "node" {
		t.Errorf("framework = %q, want node", resp.BuildPlan.Framework)
	}
	if resp.BuildPlan.Class != "app" {
		t.Errorf("class = %q, want app", resp.BuildPlan.Class)
	}
	if resp.BuildPlan.Version != "22.11.0" {
		t.Errorf("version = %q, want 22.11.0", resp.BuildPlan.Version)
	}
}

// TestDeploymentResponse_BuildPlan_FunctionImageNoSourcePath: when
// SourcePath is empty (an image deploy), BuildPlan is nil. The wire's
// omitempty keeps the field off the JSON; pre-PR-2 clients see
// bit-identical payloads.
func TestDeploymentResponse_BuildPlan_FunctionImageNoSourcePath(t *testing.T) {
	srv := newServer(state.NewMemStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	d := state.Deployment{
		ID: "d1", AppID: "a1", Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: state.DeployLive,
	}
	app := state.App{ID: "a1", Type: state.AppTypeFunction, Runtime: "node22"}
	resp := srv.deploymentResponse(d, app)
	if resp.BuildPlan != nil {
		t.Errorf("BuildPlan = %+v; want nil for image deploy", resp.BuildPlan)
	}
}

// TestDeploymentResponse_BuildPlan_UnknownFramework: a tarball with
// no recognised marker (just README.md) still produces a BuildPlan
// with Framework="unknown" and Version="". Graceful degradation —
// the wire carries the literal value rather than dropping the
// field. This is why PR-2 calls markers.DetectFromTarball directly
// (the builderd shim errors on FrameworkUnknown).
func TestDeploymentResponse_BuildPlan_UnknownFramework(t *testing.T) {
	srv := newServer(state.NewMemStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	tarPath := writeTarball(t,
		[]tar.Header{{Name: "README.md"}},
		map[string][]byte{"README.md": []byte("# hello\n")},
	)
	d := state.Deployment{
		ID: "d1", AppID: "a1", Kind: state.DeploymentKindTarball,
		SourcePath: tarPath, Status: state.DeployPending,
	}
	app := state.App{ID: "a1", Type: state.AppTypeApp}
	resp := srv.deploymentResponse(d, app)
	if resp.BuildPlan == nil {
		t.Fatalf("BuildPlan = nil; want unknown framework block")
	}
	if resp.BuildPlan.Framework != string(markers.FrameworkUnknown) {
		t.Errorf("framework = %q, want %q", resp.BuildPlan.Framework, markers.FrameworkUnknown)
	}
	if resp.BuildPlan.Version != "" {
		t.Errorf("version = %q, want empty for unknown", resp.BuildPlan.Version)
	}
	if resp.BuildPlan.Class != "app" {
		t.Errorf("class = %q, want app", resp.BuildPlan.Class)
	}
}

// TestDeploymentResponse_BuildPlan_OverridesPopulated: when the
// customer passed override_entrypoint / override_port on deploy,
// BuildPlan.Entrypoint and BuildPlan.Port echo verbatim. Mirrors the
// existing top-level OverrideEntrypoint / OverridePort fields but is
// kept inside the BuildPlan block so the dashboard can render the
// "effective" plan in one place.
func TestDeploymentResponse_BuildPlan_OverridesPopulated(t *testing.T) {
	srv := newServer(state.NewMemStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	tarPath := writeTarball(t,
		[]tar.Header{{Name: "package.json"}},
		map[string][]byte{"package.json": []byte(`{"name":"x"}`)},
	)
	d := state.Deployment{
		ID: "d1", AppID: "a1", Kind: state.DeploymentKindTarball,
		SourcePath:         tarPath,
		Status:             state.DeployPending,
		OverrideEntrypoint: []string{"node", "server.js"},
		OverridePort:       3000,
	}
	app := state.App{ID: "a1", Type: state.AppTypeApp}
	resp := srv.deploymentResponse(d, app)
	if resp.BuildPlan == nil {
		t.Fatalf("BuildPlan = nil")
	}
	if resp.BuildPlan.Entrypoint != "node" {
		t.Errorf("entrypoint = %q, want %q", resp.BuildPlan.Entrypoint, "node")
	}
	if resp.BuildPlan.Port != 3000 {
		t.Errorf("port = %d, want 3000", resp.BuildPlan.Port)
	}
}

func TestDeploymentResponse_HostingReceiptRoundTrips(t *testing.T) {
	srv := newServer(state.NewMemStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	d := state.Deployment{
		ID: "d1", AppID: "a1", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, APIHostingReceipt: []byte(`{"schema_version":1,"smoke":{"status":"verified"}}`),
	}
	resp := srv.deploymentResponse(d, state.App{ID: "a1"})
	if string(resp.APIHostingReceipt) != string(d.APIHostingReceipt) {
		t.Fatalf("hosting receipt = %s, want %s", resp.APIHostingReceipt, d.APIHostingReceipt)
	}
}
