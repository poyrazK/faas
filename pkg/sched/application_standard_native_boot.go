// adr: 431 — persist exact grants before native boot; publish only its receipt.
package sched

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardNativeRuntimeVMM interface {
	RuntimeAdmissionIdentity(context.Context, string) (runtimeadmission.Identity, error)
	CreateAdmittedRuntime(context.Context, string, *vmmdpb.CreateAdmittedRuntimeRequest) (*WakeOutcome, error)
	UpdateAppEgressPolicy(context.Context, string, string, int64, []netip.Prefix, []int) error
}

func (e *Engine) capturedStandardRuntime(ctx context.Context, appID, instanceID string) (state.InstanceApplicationStandardAdmission, error) {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return state.InstanceApplicationStandardAdmission{}, err
	}
	if app.OrgID == "" {
		return state.InstanceApplicationStandardAdmission{}, nil
	}
	reader, ok := e.store.(state.InstanceApplicationStandardAdmissionStore)
	if !ok {
		return state.InstanceApplicationStandardAdmission{}, runtimeadmission.ErrUnavailable
	}
	return reader.GetInstanceApplicationStandardAdmission(ctx, instanceID)
}

func (e *Engine) createRuntimeWithStandards(ctx context.Context, nodeID, instanceID, expectedState string, spec AppSpec, snap *SnapshotRef, paused bool) (*WakeOutcome, error) {
	capture, err := e.capturedStandardRuntime(ctx, spec.AppID, instanceID)
	if err != nil {
		return nil, applicationStandardRuntimeProblem(err)
	}
	if !capture.Managed {
		if snap == nil {
			return e.vmm.CreateColdBoot(ctx, nodeID, instanceID, spec)
		}
		if !paused {
			return e.vmm.CreateFromSnapshot(ctx, nodeID, instanceID, spec, *snap)
		}
		vmm, ok := e.vmm.(PausedRestoreVMM)
		if !ok {
			return nil, runtimeadmission.ErrUnavailable
		}
		return vmm.CreatePausedFromSnapshot(ctx, nodeID, instanceID, spec, *snap)
	}
	native, ok := e.vmm.(standardNativeRuntimeVMM)
	if !ok {
		return nil, runtimeadmission.ErrUnavailable
	}
	store, ok := e.store.(state.InstanceApplicationStandardBootStore)
	if !ok {
		return nil, runtimeadmission.ErrUnavailable
	}
	identity, err := native.RuntimeAdmissionIdentity(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if identity.Validate() != nil || identity.NodeID != nodeID || capture.NodeID != nodeID {
		return nil, runtimeadmission.ErrStale
	}
	// MemStore retains the legacy 32-hex spelling on some intent entities.
	// Normalize scoped UUID spellings before the strict native wire boundary;
	// this changes no identity and grants no authority to an invalid identifier.
	spec.AppID = canonicalStandardNativeUUID(spec.AppID)
	spec.AccountID = canonicalStandardNativeUUID(spec.AccountID)
	spec.DeploymentID = canonicalStandardNativeUUID(spec.DeploymentID)
	if snap != nil {
		copy := *snap
		copy.DeploymentID = canonicalStandardNativeUUID(copy.DeploymentID)
		snap = &copy
	}
	now := time.Now().UTC()
	binding := runtimeadmission.Binding{ProtocolVersion: identity.ProtocolVersion, Token: uuid.NewString(), InstanceID: instanceID, AppID: capture.AppID, DeploymentID: capture.DeploymentID, AccountID: capture.AccountID, NodeID: nodeID, Incarnation: identity.Incarnation, DesiredRevision: capture.DesiredRevision, EffectiveHash: capture.EffectiveHash, CapturedInputHash: capture.NativeInputHash, EgressRevision: capture.EgressRevision, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(api.ApplicationStandardRuntimeAdmissionTTL).UnixNano()}
	req, err := prepareStandardAdmittedRuntime(ctx, binding, capture, spec, snap, paused)
	if err != nil {
		return nil, err
	}
	binding, err = runtimeadmission.BindingFromProto(req.Binding)
	if err != nil {
		return nil, err
	}
	binding, err = store.IssueInstanceApplicationStandardBoot(ctx, expectedState, binding)
	if err != nil {
		return nil, applicationStandardRuntimeProblem(err)
	}
	req.Binding = binding.ToProto() // Storage owns the final issue/expiry clock.
	allowlist := make([]netip.Prefix, 0, len(spec.EgressAllowlist))
	for _, cidr := range spec.EgressAllowlist {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, runtimeadmission.ErrInvalid
		}
		allowlist = append(allowlist, prefix.Masked())
	}
	if err := native.UpdateAppEgressPolicy(ctx, nodeID, spec.AppID, binding.EgressRevision, allowlist, spec.EgressPorts); err != nil {
		return nil, fmt.Errorf("sched: install admitted egress: %w", err)
	}
	out, err := native.CreateAdmittedRuntime(ctx, nodeID, req)
	if err != nil {
		return nil, err
	}
	if out == nil || out.RuntimeAdmissionReceipt == nil || out.RuntimeAdmissionReceipt.Check(binding, time.Now()) != nil || out.RuntimeAdmissionReceipt.Paused != paused || out.RuntimeAdmissionReceipt.Netns != out.Netns || out.RuntimeAdmissionReceipt.HostIP != out.HostIP || out.RuntimeAdmissionReceipt.LeaseUID != out.LeaseUID || out.RuntimeAdmissionReceipt.Method != out.Method {
		e.bestEffortDestroy(ctx, nodeID, instanceID)
		return nil, runtimeadmission.ErrInvalid
	}
	return out, nil
}

func canonicalStandardNativeUUID(value string) string {
	if id, err := uuid.Parse(value); err == nil {
		return id.String()
	}
	return value
}

func (e *Engine) publishRuntimeWithStandards(ctx context.Context, instanceID, expectedState string, next state.State, out *WakeOutcome) (state.Instance, error) {
	if out == nil {
		return state.Instance{}, runtimeadmission.ErrInvalid
	}
	if out.RuntimeAdmissionReceipt != nil {
		store, ok := e.store.(state.InstanceApplicationStandardBootStore)
		if !ok {
			return state.Instance{}, runtimeadmission.ErrUnavailable
		}
		if out.RuntimeAdmissionReceipt.Binding.InstanceID != instanceID || out.RuntimeAdmissionReceipt.Netns != out.Netns || out.RuntimeAdmissionReceipt.HostIP != out.HostIP || out.RuntimeAdmissionReceipt.LeaseUID != out.LeaseUID {
			return state.Instance{}, runtimeadmission.ErrInvalid
		}
		return store.PublishInstanceApplicationStandardRuntime(ctx, expectedState, next, *out.RuntimeAdmissionReceipt)
	}
	// The durable guard refuses a managed row even when a caller omits its
	// receipt. Legacy ungoverned rows retain their existing publication path.
	if next == state.StateRunning {
		return e.store.PublishInstanceRuntime(ctx, instanceID, expectedState, out.Netns, out.HostIP, int(out.LeaseUID))
	}
	if next != state.StateWarm {
		return state.Instance{}, runtimeadmission.ErrInvalid
	}
	if err := e.store.SetInstanceRuntime(ctx, instanceID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		return state.Instance{}, err
	}
	if err := e.store.UpdateInstanceStateIf(ctx, instanceID, expectedState, string(next)); err != nil {
		return state.Instance{}, err
	}
	return e.store.InstanceByID(ctx, instanceID)
}
