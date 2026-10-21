// adr: 435 — persist exact grants before native boot; publish only its receipt.
package sched

import (
	"context"
	"errors"
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
	return e.capturedStandardRuntimeForApp(ctx, app, instanceID)
}

func (e *Engine) capturedStandardRuntimeForApp(ctx context.Context, app state.App, instanceID string) (state.InstanceApplicationStandardAdmission, error) {
	if app.OrgID == "" {
		return state.InstanceApplicationStandardAdmission{}, nil
	}
	reader, ok := e.store.(state.InstanceApplicationStandardAdmissionStore)
	if !ok {
		return state.InstanceApplicationStandardAdmission{}, runtimeadmission.ErrUnavailable
	}
	capture, err := reader.GetInstanceApplicationStandardAdmission(ctx, instanceID)
	if !errors.Is(err, state.ErrNotFound) {
		return capture, err
	}
	// An instance created before runtime capture existed never receives a
	// fabricated one. Like the instance guard, let it continue unmanaged only
	// while its application requires no native admission.
	enrollments, ok := e.store.(interface {
		GetApplicationStandardEnrollment(context.Context, string, string) (state.ApplicationStandardEnrollment, error)
	})
	if !ok {
		return state.InstanceApplicationStandardAdmission{}, runtimeadmission.ErrUnavailable
	}
	enrollment, err := enrollments.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err != nil {
		return state.InstanceApplicationStandardAdmission{}, err
	}
	if state.ApplicationStandardEnrollmentRequiresNative(enrollment) {
		return state.InstanceApplicationStandardAdmission{}, state.ErrApplicationStandardRuntimeStale
	}
	return state.InstanceApplicationStandardAdmission{InstanceID: instanceID, AppID: app.ID}, nil
}

func (e *Engine) createRuntimeWithStandards(ctx context.Context, nodeID, instanceID, expectedState string, spec AppSpec, snap *SnapshotRef, paused bool) (*WakeOutcome, error) {
	// A wake hands over the rehearsal network and the standards capture it
	// already read (ADR-1741); anything not handed over is read here, the
	// two reads together.
	prepared, _ := bootPreparedFor(ctx, instanceID)
	runID, capture := prepared.rehearsalRunID, prepared.capture
	var runErr, captureErr error
	reads := make([]func(), 0, 2)
	if !prepared.haveRehearsal {
		reads = append(reads, func() { runID, runErr = e.rehearsalNetworkRun(ctx, spec.AppID) })
	}
	if !prepared.haveCapture {
		reads = append(reads, func() { capture, captureErr = e.capturedStandardRuntime(ctx, spec.AppID, instanceID) })
	}
	concurrently(reads...)
	if runErr != nil {
		return nil, runErr
	}
	spec.RehearsalRunID = runID
	if runID != "" {
		spec.APIEnv = appendRehearsalPlatformAPI(spec.APIEnv)
	}
	if captureErr != nil {
		return nil, applicationStandardRuntimeProblem(captureErr)
	}
	if capture.Managed && runID != "" {
		return nil, errors.New("rehearsal containment does not yet support managed native admission")
	}
	if !capture.Managed {
		return e.createRehearsalCheckedRuntime(ctx, nodeID, instanceID, spec, snap, paused)
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
	req, err = e.prepareStandardSnapshotRestore(ctx, identity, req, snap)
	if err != nil {
		return nil, err
	}
	// Sources are prepared before the catalog envelope. A paused measured load
	// must have both before any durable grant or native call is issued.
	if paused && binding.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion && (req.SnapshotRestore == nil || identity.SnapshotRestoreVersion != runtimeadmission.SnapshotRestoreVersion) {
		return nil, runtimeadmission.ErrUnavailable
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
	if !out.RuntimeAdmissionReceipt.SnapshotConsumption.IsZero() {
		evidence, err := runtimeadmission.SnapshotRestoreEvidenceFromProto(req.SnapshotRestore)
		if err != nil || out.RuntimeAdmissionReceipt.SnapshotConsumption.CheckEvidence(binding, out.RuntimeAdmissionReceipt.ArtifactConsumption, paused, evidence, time.Now()) != nil {
			e.bestEffortDestroy(ctx, nodeID, instanceID)
			return nil, runtimeadmission.ErrInvalid
		}
	}
	return out, nil
}

func canonicalStandardNativeUUID(value string) string {
	if id, err := uuid.Parse(value); err == nil {
		return id.String()
	}
	return value
}
