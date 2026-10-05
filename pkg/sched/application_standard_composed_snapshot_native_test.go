// adr: 592
package sched

// adr: 435, 581. These receipts simulate native effects; no physical bytes or
// guest lifecycle are certified by portable scheduler acceptance.

import (
	"context"
	"strings"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (v *composedWaveNativeVMM) CaptureAdmittedRuntime(ctx context.Context, node string, g runtimeadmission.SnapshotGrant) (SnapshotBytes, runtimeadmission.SnapshotAcknowledgment, error) {
	r, err := v.store.GetApplicationStandardSnapshotCapture(ctx, g.Parent.Binding.AccountID, g.Parent.Binding.AppID, g.Parent.Binding.DeploymentID, g.Token)
	if err != nil || !r.Grant.Equal(g) || r.Acknowledgment != nil || g.Validate(time.Now()) != nil || node != v.identity.NodeID || g.Parent.Binding.Incarnation != v.identity.Incarnation {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, runtimeadmission.ErrInvalid
	}
	v.captures++
	now := time.Now().UnixNano()
	var privateBytes int64
	for _, drive := range g.Parent.ArtifactConsumption.Drives {
		if drive.Source.Role() == "main" {
			privateBytes = drive.InjectedBytes
		}
	}
	digest := "sha256:" + strings.Repeat("e", 64)
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: g.Parent.Clone(), Memory: runtimeadmission.CapturedArtifact{StorageKey: g.MemoryKey, Digest: digest, Bytes: 128 << 20}, VMState: runtimeadmission.CapturedArtifact{StorageKey: g.VMStateKey, Digest: digest, Bytes: 4096}, PrivateDrive: runtimeadmission.CapturedArtifact{StorageKey: g.PrivateDriveKey, Digest: digest, Bytes: privateBytes}, CapturedAtUnixNano: now}
	if g.Mode == "park" {
		if err := v.fakeVMM.Destroy(ctx, node, g.Parent.Binding.InstanceID); err != nil {
			return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, err
		}
	}
	b := SnapshotBytes{MemBytes: c.Memory.Bytes, VMStateBytes: c.VMState.Bytes, StoredBytes: 4096, Capture: c.Clone()}
	return b, runtimeadmission.SnapshotAcknowledgment{Grant: g.Clone(), Capture: c, CompletedAtUnixNano: now}, nil
}

func (v *composedWaveNativeVMM) createComposedRuntime(ctx context.Context, node string, req *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error) {
	if req.GetRestore() == nil {
		return v.standardNativeTestVMM.CreateAdmittedRuntime(ctx, node, req)
	}
	// Match the current native source consumer contract: sources require a
	// verified cold fallback; paused restore is unavailable. A portable fake
	// must not certify a restore that the native manager cannot perform.
	b, err := runtimeadmission.BindingFromProto(req.Binding)
	if err != nil || b.Validate(time.Now()) != nil || node != v.identity.NodeID || b.Incarnation != v.identity.Incarnation || req.GetRestore().KeepPaused {
		return nil, runtimeadmission.ErrUnavailable
	}
	hash, err := runtimeadmission.HashBootPayload(req)
	policy, ok := v.policies[b.AppID]
	if err != nil || hash != b.PayloadHash || !ok || policy.Revision != b.EgressRevision {
		return nil, runtimeadmission.ErrInvalid
	}
	v.lastRequest = req
	out, err := v.fakeVMM.CreateColdBoot(ctx, node, b.InstanceID, AppSpec{AppID: b.AppID, AccountID: b.AccountID, DeploymentID: b.DeploymentID})
	if err != nil {
		return nil, err
	}
	out.RuntimeAdmissionReceipt = &runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("b", 64), Netns: out.Netns, HostIP: out.HostIP, LeaseUID: out.LeaseUID, Method: out.Method, CompletedAtUnixNano: time.Now().UnixNano()}
	return out, nil
}
