// adr: 430 — exact prepared boot payload and backend-produced receipt.
package sched

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/protobuf/proto"
)

// PrepareAdmittedRuntime constructs the exact payload before a durable grant
// is committed. It performs no VM call and cannot fabricate storage authority.
// The caller must persist this binding before using CreateAdmittedRuntime.
func PrepareAdmittedRuntime(ctx context.Context, binding runtimeadmission.Binding, app AppSpec, snapshot *SnapshotRef, keepPaused bool) (*vmmdpb.CreateAdmittedRuntimeRequest, error) {
	if app.AppID != binding.AppID || app.AccountID != binding.AccountID || app.DeploymentID != binding.DeploymentID || app.Port < 0 || app.Port > 65535 || keepPaused && snapshot == nil {
		return nil, runtimeadmission.ErrInvalid
	}
	// The legacy encoder silently drops invalid ports. The admitted encoder
	// refuses lossy input instead of hashing a weakened projection.
	for _, port := range app.EgressPorts {
		if _, forbidden := api.TenantEgressForbiddenPort(port); port < 1 || port > 65535 || forbidden {
			return nil, runtimeadmission.ErrInvalid
		}
	}
	for _, sidecar := range app.Sidecars {
		if sidecar.Port < 0 || sidecar.Port > 65535 {
			return nil, runtimeadmission.ErrInvalid
		}
	}
	fields, _ := wire.FromContext(ctx)
	req := &vmmdpb.CreateAdmittedRuntimeRequest{}
	if snapshot == nil {
		req.Boot = &vmmdpb.CreateAdmittedRuntimeRequest_ColdBoot{ColdBoot: &vmmdpb.CreateColdBootRequest{Instance: binding.InstanceID, App: app.toProto(), Plan: string(app.Plan), AccountId: app.AccountID, WakeId: fields.WakeID}}
	} else {
		if snapshot.DeploymentID != binding.DeploymentID || snapshot.Networkless {
			return nil, runtimeadmission.ErrInvalid
		}
		req.Boot = &vmmdpb.CreateAdmittedRuntimeRequest_Restore{Restore: &vmmdpb.CreateFromSnapshotRequest{Instance: binding.InstanceID, App: app.toProto(), Plan: string(app.Plan), AccountId: app.AccountID, WakeId: fields.WakeID, KeepPaused: keepPaused, Snapshot: &vmmdpb.SnapshotRef{DeploymentId: snapshot.DeploymentID, VmstatePath: snapshot.VMStatePath, FcVersion: snapshot.FCVersion, StorageKey: snapshot.StorageKey, VmstateStorageKey: snapshot.VMStateStorageKey}}}
	}
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil {
		return nil, err
	}
	binding.PayloadHash = hash
	if err := binding.Validate(time.Now()); err != nil {
		return nil, err
	}
	req.Binding = binding.ToProto()
	// AppSpec contains caller-owned byte slices (sealed env included).
	return proto.Clone(req).(*vmmdpb.CreateAdmittedRuntimeRequest), nil
}

func (c *VMMClient) RuntimeAdmissionIdentity(ctx context.Context) (runtimeadmission.Identity, error) {
	resp, err := c.cli.RuntimeAdmissionIdentity(ctx, &vmmdpb.RuntimeAdmissionIdentityRequest{})
	if err != nil {
		return runtimeadmission.Identity{}, liftErr(err)
	}
	if err := runtimeadmission.RejectUnknown(resp); err != nil {
		return runtimeadmission.Identity{}, err
	}
	i := runtimeadmission.Identity{ProtocolVersion: resp.ProtocolVersion, NodeID: resp.NodeId, Incarnation: resp.Incarnation}
	return i, i.Validate()
}

func (c *VMMClient) CreateAdmittedRuntime(ctx context.Context, req *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error) {
	if req == nil {
		return nil, runtimeadmission.ErrInvalid
	}
	req = proto.Clone(req).(*vmmdpb.CreateAdmittedRuntimeRequest)
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil {
		return nil, err
	}
	binding, err := runtimeadmission.BindingFromProto(req.Binding)
	if err != nil {
		return nil, err
	}
	if err := binding.Validate(time.Now()); err != nil {
		return nil, err
	}
	if hash != binding.PayloadHash {
		return nil, runtimeadmission.ErrInvalid
	}
	fields, _ := wire.FromContext(ctx)
	ctx = wire.WithCorrelationOutgoing(ctx, fields)
	resp, err := c.cli.CreateAdmittedRuntime(ctx, req)
	if err != nil {
		return nil, liftErr(err)
	}
	if err := checkAdmittedRuntimeResponse(req, resp, binding); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cancel()
		_ = c.Destroy(cleanupCtx, binding.InstanceID)
		return nil, err
	}
	receipt, _ := runtimeadmission.ReceiptFromProto(resp.Receipt)
	out := outcomeFromProto(resp.Runtime)
	out.RuntimeAdmissionReceipt = &receipt
	return out, nil
}

func checkAdmittedRuntimeResponse(req *vmmdpb.CreateAdmittedRuntimeRequest, resp *vmmdpb.CreateAdmittedRuntimeResponse, binding runtimeadmission.Binding) error {
	if err := runtimeadmission.RejectUnknown(resp); err != nil {
		return err
	}
	if resp.Runtime == nil {
		return runtimeadmission.ErrInvalid
	}
	receipt, err := runtimeadmission.ReceiptFromProto(resp.Receipt)
	if err != nil {
		return err
	}
	if err := receipt.Check(binding, time.Now()); err != nil {
		return err
	}
	actual := resp.Runtime
	paused := req.GetRestore() != nil && req.GetRestore().KeepPaused
	method := vmmdpb.WakeMethod_WAKE_COLD_BOOT
	if req.GetRestore() != nil {
		method = vmmdpb.WakeMethod_WAKE_RESTORE
	}
	if actual.Instance != binding.InstanceID || actual.Netns != receipt.Netns || actual.HostIp != receipt.HostIP || actual.LeaseUid != receipt.LeaseUID || actual.Method != receipt.Method || actual.RequestedMethod != method || receipt.Paused != paused {
		return runtimeadmission.ErrInvalid
	}
	return nil
}
