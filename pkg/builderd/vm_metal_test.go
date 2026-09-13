//go:build metal && linux

// spec: §13

package builderd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

type warmSnapshotFailureClient struct {
	vmmdpb.VmmdClient
	snapshotErr  error
	readyErr     error
	stopErr      error
	destroyErr   error
	deleteErr    error
	stopCalls    int
	destroyCalls int
	deleteCalls  int
}

func (c *warmSnapshotFailureClient) WaitBuilderReady(context.Context, *vmmdpb.WaitBuilderReadyRequest, ...grpc.CallOption) (*vmmdpb.WaitBuilderReadyResponse, error) {
	if c.readyErr != nil {
		return nil, c.readyErr
	}
	return &vmmdpb.WaitBuilderReadyResponse{Ready: true}, nil
}

func (c *warmSnapshotFailureClient) Ping(context.Context, *vmmdpb.PingRequest, ...grpc.CallOption) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{FcVersion: "firecracker-test"}, nil
}

func (c *warmSnapshotFailureClient) WarmSnapshot(context.Context, *vmmdpb.WarmSnapshotRequest, ...grpc.CallOption) (*vmmdpb.SnapshotResponse, error) {
	if c.snapshotErr != nil {
		return nil, c.snapshotErr
	}
	return &vmmdpb.SnapshotResponse{}, nil
}

func (c *warmSnapshotFailureClient) StopInstance(context.Context, *vmmdpb.StopInstanceRequest, ...grpc.CallOption) (*vmmdpb.StopInstanceResponse, error) {
	c.stopCalls++
	return &vmmdpb.StopInstanceResponse{}, c.stopErr
}

func (c *warmSnapshotFailureClient) Destroy(context.Context, *vmmdpb.DestroyRequest, ...grpc.CallOption) (*vmmdpb.DestroyResponse, error) {
	c.destroyCalls++
	if c.destroyErr != nil {
		return nil, c.destroyErr
	}
	return &vmmdpb.DestroyResponse{ExitCode: 0}, nil
}

func (c *warmSnapshotFailureClient) DeleteWarmSnapshot(context.Context, *vmmdpb.DeleteWarmSnapshotRequest, ...grpc.CallOption) (*vmmdpb.DeleteWarmSnapshotResponse, error) {
	c.deleteCalls++
	if c.deleteErr != nil {
		return nil, c.deleteErr
	}
	return &vmmdpb.DeleteWarmSnapshotResponse{}, nil
}

func TestReadBuildDonePrefersGuestExitCode(t *testing.T) {
	dir := t.TempDir()
	want := api.BuildDone{
		SchemaVersion: 1,
		BuildID:       "build-1",
		ExitCode:      0,
		OCIImagePath:  "/build/out/image.tar",
		LogTail:       "build complete",
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-done.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := readBuildDone(dir)
	if !ok {
		t.Fatal("readBuildDone returned !ok")
	}
	if got.ExitCode != 0 || got.OCIImagePath != want.OCIImagePath || got.LogTail != want.LogTail {
		t.Fatalf("readBuildDone = %#v, want %#v", got, want)
	}
}

func TestWarmBuilderSnapshotKeysAreStableAndScopedToBuild(t *testing.T) {
	mem, vmstate := warmBuilderSnapshotKeys("build-1")
	if mem == "" || vmstate == "" || mem == vmstate {
		t.Fatalf("snapshot keys = %q/%q, want distinct non-empty keys", mem, vmstate)
	}
	if gotMem, gotVMState := warmBuilderSnapshotKeys("build-1"); gotMem != mem || gotVMState != vmstate {
		t.Fatalf("snapshot keys changed between calls: %q/%q then %q/%q", mem, vmstate, gotMem, gotVMState)
	}
	if otherMem, _ := warmBuilderSnapshotKeys("build-2"); otherMem == mem {
		t.Fatalf("different builds share memory snapshot key %q", mem)
	}
	parts := strings.Split(mem, "/")
	if len(parts) != 4 || parts[0] != "snap" || parts[2] != "warm" || parts[3] != "mem" {
		t.Fatalf("memory snapshot key %q does not match snap/<uuid>/warm/mem", mem)
	}
	snapshotID, err := uuid.Parse(parts[1])
	if err != nil {
		t.Fatalf("memory snapshot key %q has invalid UUID: %v", mem, err)
	}
	if snapshotID.Version() != 8 {
		t.Fatalf("snapshot UUID version = %d, want 8", snapshotID.Version())
	}
	if vmstate != strings.TrimSuffix(mem, "mem")+"vmstate" {
		t.Fatalf("snapshot siblings do not share a prefix: %q/%q", mem, vmstate)
	}
}

func TestWaitForWarmCompletionPreservesBuildWhenSnapshotFails(t *testing.T) {
	exportDir := t.TempDir()
	imagePath := filepath.Join(exportDir, "build", "out", "image.tar")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("oci"), 0o644); err != nil {
		t.Fatal(err)
	}
	done, err := json.Marshal(api.BuildDone{SchemaVersion: 1, BuildID: "build-1", ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exportDir, "build-done.json"), done, 0o644); err != nil {
		t.Fatal(err)
	}
	drivePath := filepath.Join(t.TempDir(), "builder.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &warmSnapshotFailureClient{snapshotErr: errors.New("snapshot cgroup exhausted")}
	driver := &VMMDriver{cli: client}
	out, snapshot, err := driver.WaitForWarmCompletion(context.Background(), BuildHandle{
		Instance:     "build-build-1",
		HostDrive1:   drivePath,
		ExportDir:    exportDir,
		BuildID:      "build-1",
		TimeoutSec:   30,
		WarmScopeKey: "scope-1",
	})
	if err != nil {
		t.Fatalf("WaitForWarmCompletion: %v", err)
	}
	if out.ExitCode != 0 || out.OCIImage != imagePath {
		t.Fatalf("outcome = %+v, want successful exported image %q", out, imagePath)
	}
	if !strings.Contains(out.WarmSnapshotError, "snapshot cgroup exhausted") {
		t.Fatalf("WarmSnapshotError = %q, want snapshot failure", out.WarmSnapshotError)
	}
	if snapshot.StorageKey != "" || snapshot.LayerPath != "" {
		t.Fatalf("snapshot = %+v, want empty fallback snapshot", snapshot)
	}
	if client.stopCalls != 1 || client.destroyCalls != 1 {
		t.Fatalf("stop/destroy calls = %d/%d, want 1/1", client.stopCalls, client.destroyCalls)
	}
	if _, statErr := os.Stat(drivePath); !os.IsNotExist(statErr) {
		t.Fatalf("fallback drive stat error = %v, want removed", statErr)
	}
}

func TestWaitForWarmCompletionKeepsDriveUntilExportWhenStopFails(t *testing.T) {
	exportDir := t.TempDir()
	imagePath := filepath.Join(exportDir, "build", "out", "image.tar")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("oci"), 0o644); err != nil {
		t.Fatal(err)
	}
	done, err := json.Marshal(api.BuildDone{SchemaVersion: 1, BuildID: "build-2", ExitCode: 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exportDir, "build-done.json"), done, 0o644); err != nil {
		t.Fatal(err)
	}
	drivePath := filepath.Join(t.TempDir(), "builder.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &warmSnapshotFailureClient{
		stopErr:   errors.New("stop unavailable"),
		deleteErr: errors.New("delete unavailable"),
	}
	driver := &VMMDriver{cli: client}
	out, snapshot, err := driver.WaitForWarmCompletion(context.Background(), BuildHandle{
		Instance:     "build-build-2",
		HostDrive1:   drivePath,
		ExportDir:    exportDir,
		BuildID:      "build-2",
		TimeoutSec:   30,
		WarmScopeKey: "scope-2",
	})
	if err != nil {
		t.Fatalf("WaitForWarmCompletion: %v", err)
	}
	if out.ExitCode != 0 || out.OCIImage != imagePath || !strings.Contains(out.WarmSnapshotError, "stop unavailable") {
		t.Fatalf("outcome = %+v, want successful export with stop diagnostic", out)
	}
	if snapshot.StorageKey == "" || snapshot.LayerPath != "" || client.deleteCalls != 1 || client.destroyCalls != 1 {
		t.Fatalf("snapshot/delete/destroy = %+v/%d/%d, want storage key without layer path/1/1", snapshot, client.deleteCalls, client.destroyCalls)
	}
	if !strings.Contains(out.WarmSnapshotError, "delete unavailable") {
		t.Fatalf("WarmSnapshotError = %q, want cleanup diagnostic", out.WarmSnapshotError)
	}
	if _, statErr := os.Stat(drivePath); !os.IsNotExist(statErr) {
		t.Fatalf("fallback drive stat error = %v, want removed after export", statErr)
	}
}

func TestWaitForWarmCompletionReturnsSnapshotWhenPostWaitCleanupFails(t *testing.T) {
	exportDir := t.TempDir()
	drivePath := filepath.Join(t.TempDir(), "builder.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &warmSnapshotFailureClient{
		destroyErr: errors.New("export unavailable"),
		deleteErr:  errors.New("delete unavailable"),
	}
	driver := &VMMDriver{cli: client}
	_, snapshot, err := driver.WaitForWarmCompletion(context.Background(), BuildHandle{
		Instance:     "build-build-3",
		HostDrive1:   drivePath,
		ExportDir:    exportDir,
		BuildID:      "build-3",
		TimeoutSec:   30,
		WarmScopeKey: "scope-3",
	})
	if err == nil || !strings.Contains(err.Error(), "export unavailable") || !strings.Contains(err.Error(), "delete unavailable") {
		t.Fatalf("error = %v, want export and cleanup failures", err)
	}
	if snapshot.StorageKey == "" || snapshot.VMStateStorageKey == "" || snapshot.LayerPath != drivePath {
		t.Fatalf("snapshot = %+v, want complete cleanup metadata", snapshot)
	}
	if client.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", client.deleteCalls)
	}
	if _, statErr := os.Stat(drivePath); !os.IsNotExist(statErr) {
		t.Fatalf("drive stat error = %v, want removed during cleanup", statErr)
	}
}

func TestWaitForWarmCompletionReadinessFailureRemovesDrive(t *testing.T) {
	drivePath := filepath.Join(t.TempDir(), "builder.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &warmSnapshotFailureClient{readyErr: errors.New("readiness unavailable")}
	driver := &VMMDriver{cli: client}
	_, _, err := driver.WaitForWarmCompletion(context.Background(), BuildHandle{
		Instance:   "build-build-4",
		HostDrive1: drivePath,
		BuildID:    "build-4",
		TimeoutSec: 30,
	})
	if err == nil || !strings.Contains(err.Error(), "readiness unavailable") {
		t.Fatalf("error = %v, want readiness failure", err)
	}
	if client.stopCalls != 1 || client.destroyCalls != 1 {
		t.Fatalf("stop/destroy calls = %d/%d, want 1/1", client.stopCalls, client.destroyCalls)
	}
	if _, statErr := os.Stat(drivePath); !os.IsNotExist(statErr) {
		t.Fatalf("drive stat error = %v, want removed after readiness failure", statErr)
	}
}

func TestDeleteWarmSnapshotRemovesLegacyLocalState(t *testing.T) {
	layerPath := filepath.Join(t.TempDir(), "builder.ext4")
	vmstatePath := filepath.Join(t.TempDir(), "builder.vmstate")
	for _, path := range []string{layerPath, vmstatePath} {
		if err := os.WriteFile(path, []byte("warm state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	driver := &VMMDriver{cli: &warmSnapshotFailureClient{}}
	err := driver.DeleteWarmSnapshot(context.Background(), WarmSnapshot{
		LayerPath:   layerPath,
		VMStatePath: vmstatePath,
	})
	if err != nil {
		t.Fatalf("DeleteWarmSnapshot: %v", err)
	}
	for _, path := range []string{layerPath, vmstatePath} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("local warm state %q still exists: %v", path, statErr)
		}
	}
}

func TestBuildManifestForRequestCarriesWarmInputs(t *testing.T) {
	manifest, err := buildManifestForRequest(VMRequest{
		BuildID:            "build-1",
		TenantID:           "acct-1",
		DeploymentID:       "dep-1",
		SourceRoot:         "services/api",
		Framework:          FrameworkNode,
		Runtime:            "node22",
		RuntimeBaseRef:     "base-ref",
		DependencyCacheKey: "cache-key",
		KeepWarm:           true,
		Function:           true,
	}, 900)
	if err != nil {
		t.Fatalf("buildManifestForRequest: %v", err)
	}
	if !manifest.KeepWarm || !manifest.DependencyCache || !manifest.Function || manifest.TimeoutSec != 900 {
		t.Fatalf("manifest warm/cache/timeout = %v/%v/%d", manifest.KeepWarm, manifest.DependencyCache, manifest.TimeoutSec)
	}
	if !strings.HasSuffix(manifest.Workdir, "/services/api") {
		t.Fatalf("manifest workdir = %q, want services/api suffix", manifest.Workdir)
	}
}
