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

	"google.golang.org/grpc"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

type warmSnapshotFailureClient struct {
	vmmdpb.VmmdClient
	snapshotErr  error
	stopErr      error
	stopCalls    int
	destroyCalls int
	deleteCalls  int
}

func (c *warmSnapshotFailureClient) WaitBuilderReady(context.Context, *vmmdpb.WaitBuilderReadyRequest, ...grpc.CallOption) (*vmmdpb.WaitBuilderReadyResponse, error) {
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
	return &vmmdpb.DestroyResponse{ExitCode: 0}, nil
}

func (c *warmSnapshotFailureClient) DeleteWarmSnapshot(context.Context, *vmmdpb.DeleteWarmSnapshotRequest, ...grpc.CallOption) (*vmmdpb.DeleteWarmSnapshotResponse, error) {
	c.deleteCalls++
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

	client := &warmSnapshotFailureClient{stopErr: errors.New("stop unavailable")}
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
	if snapshot.StorageKey != "" || client.deleteCalls != 1 || client.destroyCalls != 1 {
		t.Fatalf("snapshot/delete/destroy = %+v/%d/%d, want empty/1/1", snapshot, client.deleteCalls, client.destroyCalls)
	}
	if _, statErr := os.Stat(drivePath); !os.IsNotExist(statErr) {
		t.Fatalf("fallback drive stat error = %v, want removed after export", statErr)
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
	}, 900)
	if err != nil {
		t.Fatalf("buildManifestForRequest: %v", err)
	}
	if !manifest.KeepWarm || !manifest.DependencyCache || manifest.TimeoutSec != 900 {
		t.Fatalf("manifest warm/cache/timeout = %v/%v/%d", manifest.KeepWarm, manifest.DependencyCache, manifest.TimeoutSec)
	}
	if !strings.HasSuffix(manifest.Workdir, "/services/api") {
		t.Fatalf("manifest workdir = %q, want services/api suffix", manifest.Workdir)
	}
}
