// Whitebox tests for BuildPlan population on DeploymentResponse
// (cmd/apid/handlers_ext.go:deploymentResponse, issue #961 / Mega-A PR-2).
//
// Pins the wire shape of the auto-detected BuildPlan block:
//   - app kind + framework + version populate from SourcePath
//   - function kind with no SourcePath → BuildPlan is nil (omit on wire)
//   - tarball with no recognised marker → Framework="unknown", Version=""
//   - per-deployment override_entrypoint + override_port echo verbatim
//
// New rows use the persisted profile captured from the exact archive;
// legacy rows keep the marker-detection fallback so FrameworkUnknown is a
// NON-error graceful degradation. Pre-PR-2 wire-equal callers see no diff
// (omitempty on the *BuildPlan pointer).

package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/markers"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppResponseSurfacesMaintenanceMode(t *testing.T) {
	s := &server{}
	got := s.appResponse(state.App{MaintenanceMode: true}, api.PlanHobby)
	if !got.MaintenanceMode {
		t.Fatal("MaintenanceMode = false, want persisted true value")
	}
}

func TestAppResponseSurfacesDeclaredServiceBindings(t *testing.T) {
	s := &server{}
	bindings := []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_BILLING_URL", Service: "billing"}}
	got := s.appResponse(state.App{Manifest: state.AppManifest{ServiceBindings: bindings}}, api.PlanHobby)
	if !reflect.DeepEqual(got.ServiceBindings, bindings) {
		t.Fatalf("service bindings = %#v, want %#v", got.ServiceBindings, bindings)
	}
	bindings[0].Service = "mutated"
	if got.ServiceBindings[0].Service != "billing" {
		t.Fatal("app response aliases persisted service bindings")
	}
}

func TestAppResponseUsesVerifiedDefaultDomain(t *testing.T) {
	store := state.NewMemStore()
	const appID = "app-canonical"
	if _, err := store.CreateCustomDomain(context.Background(), "api.example.com", appID, "token"); err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if err := store.MarkDomainVerified(context.Background(), "api.example.com"); err != nil {
		t.Fatalf("MarkDomainVerified: %v", err)
	}
	if err := store.SetDefaultCustomDomain(context.Background(), appID, "api.example.com"); err != nil {
		t.Fatalf("SetDefaultCustomDomain: %v", err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	got := srv.appResponseWithContext(context.Background(), state.App{ID: appID, Slug: "canonical-app"}, api.PlanHobby)
	if got.URL != "https://canonical-app.gregale.dev" {
		t.Fatalf("platform URL = %q, want platform hostname", got.URL)
	}
	if got.DefaultDomain != "api.example.com" {
		t.Fatalf("default_domain = %q, want api.example.com", got.DefaultDomain)
	}
	if got.CanonicalURL != "https://api.example.com" {
		t.Fatalf("canonical_url = %q, want https://api.example.com", got.CanonicalURL)
	}
}

func TestAppResponseFallsBackWhenDefaultDomainIsRemoved(t *testing.T) {
	store := state.NewMemStore()
	const appID = "app-fallback"
	if _, err := store.CreateCustomDomain(context.Background(), "api.example.com", appID, "token"); err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if err := store.MarkDomainVerified(context.Background(), "api.example.com"); err != nil {
		t.Fatalf("MarkDomainVerified: %v", err)
	}
	if err := store.SetDefaultCustomDomain(context.Background(), appID, "api.example.com"); err != nil {
		t.Fatalf("SetDefaultCustomDomain: %v", err)
	}
	if err := store.DeleteCustomDomain(context.Background(), "api.example.com"); err != nil {
		t.Fatalf("DeleteCustomDomain: %v", err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	got := srv.appResponseWithContext(context.Background(), state.App{ID: appID, Slug: "fallback-app"}, api.PlanHobby)
	if got.DefaultDomain != "" {
		t.Fatalf("default_domain = %q, want empty after delete", got.DefaultDomain)
	}
	if got.CanonicalURL != got.URL || got.CanonicalURL != "https://fallback-app.gregale.dev" {
		t.Fatalf("canonical URL = %q, platform URL = %q; want platform fallback", got.CanonicalURL, got.URL)
	}
}

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

func TestDeploymentResponse_NormalizesEmptyRolloutState(t *testing.T) {
	srv := newServer(state.NewMemStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	resp := srv.deploymentResponse(state.Deployment{
		ID: "d1", AppID: "a1", Status: state.DeployLive,
	}, state.App{ID: "a1"})
	if resp.RolloutState != "pending" {
		t.Fatalf("rollout_state = %q, want pending for legacy zero value", resp.RolloutState)
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

func TestDeploymentResponse_BuildPlan_UsesPersistedProfileAfterSpoolCleanup(t *testing.T) {
	srv := newServer(state.NewMemStore(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	raw, err := json.Marshal(frameworkprofile.Profile{
		Version: frameworkprofile.Version, Framework: "fastapi", FrameworkVer: "0.115",
		StartCommand: "uvicorn app:app --host 0.0.0.0 --port 8000", Port: 8000, HealthPath: "/ready", Inferred: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	d := state.Deployment{
		ID: "d1", AppID: "a1", Kind: state.DeploymentKindTarball,
		Status: state.DeployLive, InferredProfile: raw,
	}
	resp := srv.deploymentResponse(d, state.App{ID: "a1", Type: state.AppTypeApp})
	if resp.BuildPlan == nil {
		t.Fatalf("BuildPlan = nil; want persisted profile")
	}
	if resp.BuildPlan.Framework != "python" || resp.BuildPlan.Version != "0.115" || resp.BuildPlan.Entrypoint == "" || resp.BuildPlan.HealthPath != "/ready" {
		t.Fatalf("BuildPlan = %+v; want persisted profile values", resp.BuildPlan)
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
