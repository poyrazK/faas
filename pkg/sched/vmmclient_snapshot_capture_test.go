package sched_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func wireSnapshotFixture(t *testing.T) fcvm.SnapshotInfo {
	t.Helper()
	_, binding, _ := preparedWireFixture(t, false)
	binding.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	sources := []runtimeadmission.ArtifactSource{{Kind: "base-image", StorageKey: "base/approved.ext4", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 4096}, {Kind: "app-layer", StorageKey: "apps/approved.ext4", Digest: "sha256:" + strings.Repeat("b", 64), Bytes: 8192}}
	var err error
	binding.ArtifactSourcesHash, err = runtimeadmission.HashArtifactSources(sources)
	if err != nil {
		t.Fatal(err)
	}
	consumption := runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("c", 64), ProcessPID: 42, ProcessStart: "101"}
	for _, source := range sources {
		consumption.Drives = append(consumption.Drives, runtimeadmission.ConsumedDrive{Source: source, DriveID: source.Role(), ReadOnly: source.Role() == "base", RootDevice: source.Role() == "base", ProducerDigest: source.Digest, ProducerBytes: source.Bytes, InjectedDigest: source.Digest, InjectedBytes: source.Bytes})
	}
	parent := runtimeadmission.Receipt{Binding: binding, NativeInputHash: strings.Repeat("d", 64), Netns: "fc-wire", HostIP: "10.100.0.2", LeaseUID: 20000, Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, CompletedAtUnixNano: time.Now().UnixNano(), ArtifactConsumption: consumption}
	key := state.SnapshotCaptureMemKey(binding.DeploymentID, state.SnapshotTierWarm, uuid.NewString())
	prefix := strings.TrimSuffix(key, "mem")
	capture := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: parent, Memory: runtimeadmission.CapturedArtifact{StorageKey: key, Digest: "sha256:" + strings.Repeat("1", 64), Bytes: 16384}, VMState: runtimeadmission.CapturedArtifact{StorageKey: prefix + "vmstate", Digest: "sha256:" + strings.Repeat("2", 64), Bytes: 4096}, PrivateDrive: runtimeadmission.CapturedArtifact{StorageKey: prefix + "drive", Digest: "sha256:" + strings.Repeat("3", 64), Bytes: 8192}, CapturedAtUnixNano: time.Now().UnixNano()}
	return fcvm.SnapshotInfo{MemBytes: 16384, VMStateBytes: 4096, StoredBytes: 4096, Capture: capture}
}

func TestVMMClientNativeSnapshotCaptureSurvivesServerAndClientWire(t *testing.T) {
	for _, warm := range []bool{false, true} {
		info := wireSnapshotFixture(t)
		capture := info.Capture.Clone()
		callback := func(context.Context, string, fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) { return info, nil }
		client := newClient(t, &fakeVMM{parkFn: callback, warmFn: callback})
		var err error
		if warm {
			result, callErr := client.WarmSnapshot(t.Context(), capture.Parent.Binding.InstanceID, capture.Memory.StorageKey, capture.VMState.StorageKey)
			err = callErr
			if !result.Capture.Equal(capture) {
				t.Fatal("warm capture evidence changed across the wire", err)
			}
		} else {
			result, callErr := client.PauseAndSnapshot(t.Context(), capture.Parent.Binding.InstanceID, "", capture.Memory.StorageKey, capture.VMState.StorageKey, true)
			err = callErr
			if !result.Capture.Equal(capture) {
				t.Fatal("park capture evidence changed across the wire", err)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestVMMClientNativeSnapshotCaptureRejectsWrongScopeAndCounts(t *testing.T) {
	for _, edit := range []func(*fcvm.SnapshotInfo){
		func(i *fcvm.SnapshotInfo) { i.Capture.Parent.Binding.InstanceID = uuid.NewString() },
		func(i *fcvm.SnapshotInfo) { i.Capture.Memory.StorageKey = "another-capture" },
		func(i *fcvm.SnapshotInfo) { i.MemBytes++ },
		func(i *fcvm.SnapshotInfo) { i.Capture.PrivateDrive.Digest = "" },
	} {
		info := wireSnapshotFixture(t)
		capture := info.Capture.Clone()
		edit(&info)
		callback := func(context.Context, string, fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error) { return info, nil }
		client := newClient(t, &fakeVMM{parkFn: callback, warmFn: callback})
		result, err := client.PauseAndSnapshot(t.Context(), capture.Parent.Binding.InstanceID, "", capture.Memory.StorageKey, capture.VMState.StorageKey, false)
		if err == nil || !result.Capture.IsZero() || result.MemBytes != 0 {
			t.Fatal("invalid native capture was accepted by the scheduler", err)
		}
	}
}
