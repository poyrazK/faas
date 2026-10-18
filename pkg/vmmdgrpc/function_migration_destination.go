package vmmdgrpc

// adr: 1664. schedd's existing authenticated migration RPC carries each phase.

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type functionDestinationVMM interface {
	RuntimeAdmissionIdentity() (runtimeadmission.Identity, error)
	FunctionMigrationDestinationVersion() uint32
	AdoptFunctionMigrationDestination(context.Context, fcvm.AdmittedWakeRequest, func(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error) (*fcvm.Instance, runtimeadmission.Receipt, error)
	ActivateFunctionMigrationDestination(context.Context, runtimeadmission.FunctionMigrationDestinationGrant, func(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error) (*fcvm.Instance, runtimeadmission.Receipt, error)
	RetireFunctionMigrationDestination(context.Context, runtimeadmission.FunctionMigrationDestinationGrant, func(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error) error
}

func (s *Server) adoptFunctionDestination(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	vmm, ok := s.vmm.(functionDestinationVMM)
	store, writable := s.migrationStore.(state.FunctionMigrationDestinationStore)
	if !functionlifetime.NativeQualificationBuild || !ok || !writable || vmm.FunctionMigrationDestinationVersion() != 1 {
		return nil, runtimeadmission.ErrUnavailable
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	identity, err := vmm.RuntimeAdmissionIdentity()
	if err != nil {
		return nil, err
	}
	if identity.Validate() != nil || s.nodeID == "" || identity.NodeID != s.nodeID {
		return nil, runtimeadmission.ErrStale
	}
	d, err := store.GetFunctionMigrationDestination(ctx, req.GetInstanceId(), req.GetLeaseToken())
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, err
	}
	if err == nil && d.Phase == state.FunctionDestinationCommitted {
		return s.activateFunctionDestination(ctx, req, d, identity, vmm, store)
	}
	wr, hash, err := s.prepareFunctionDestinationWake(ctx, req, store)
	if err != nil {
		return nil, err
	}
	grant, err := store.IssueFunctionMigrationDestination(ctx, state.FunctionMigrationDestinationRequest{InstanceID: req.GetInstanceId(), LeaseToken: req.GetLeaseToken(), WakeID: req.GetWakeId(), NativeInputHash: hash, Identity: identity})
	if err != nil {
		return nil, err
	}
	inst, receipt, err := vmm.AdoptFunctionMigrationDestination(ctx, fcvm.AdmittedWakeRequest{Request: wr, Binding: grant.Binding, NativeInputHash: hash, Migration: &grant}, store.CheckFunctionMigrationDestination)
	if err != nil {
		return nil, err
	}
	if err := checkFunctionDestinationResult(grant, inst, receipt, time.Now()); err != nil {
		return nil, err
	}
	if err := store.ReadyFunctionMigrationDestination(ctx, grant, receipt); err != nil {
		return nil, err
	}
	return functionDestinationResponse(req, inst), nil
}
func functionDestinationResponse(req *vmmdpb.AdoptMigratedInstanceRequest, inst *fcvm.Instance) *vmmdpb.AdoptMigratedInstanceResponse {
	return &vmmdpb.AdoptMigratedInstanceResponse{SupportsSecretAliases: true, SupportsImageHealthcheckMonitoring: true,
		ImageHealthcheckVerified: inst.ImageHealthcheckRequired && !inst.Paused, HostIp: addrOrEmpty(inst.Lease.HostIP), Netns: inst.Net.Netns, GuestUid: int32(inst.Lease.UID), Method: vmmdpb.WakeMethod_WAKE_RESTORE, WakeId: req.GetWakeId()}
}

func (s *Server) cancelFunctionDestination(ctx context.Context, req *vmmdpb.CancelLiveMigrationRequest) (bool, error) {
	store, ok := s.migrationStore.(state.FunctionMigrationDestinationStore)
	if !ok {
		return false, nil
	}
	d, err := store.GetFunctionMigrationDestination(ctx, req.GetInstanceId(), req.GetLeaseToken())
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if d.Grant.Binding.NodeID != s.nodeID {
		return false, nil
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return true, err
	}
	if d.Phase == state.FunctionDestinationRetired {
		return true, nil
	}
	vmm, ok := s.vmm.(functionDestinationVMM)
	if !functionlifetime.NativeQualificationBuild || !ok || vmm.FunctionMigrationDestinationVersion() != 1 {
		return true, runtimeadmission.ErrUnavailable
	}
	if err := vmm.RetireFunctionMigrationDestination(ctx, d.Grant, store.CheckFunctionMigrationDestinationRetirement); err != nil {
		return true, err
	}
	return true, store.RetireFunctionMigrationDestination(ctx, d.Grant)
}

func (s *Server) prepareFunctionDestinationWake(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest, store state.FunctionMigrationDestinationStore) (fcvm.WakeRequest, string, error) {
	evidence, err := store.GetFunctionMigrationCapture(ctx, req.GetInstanceId(), req.GetLeaseToken())
	if err != nil {
		return fcvm.WakeRequest{}, "", err
	}
	wr, err := toMigrationWakeRequest(ctx, req)
	if err != nil {
		return fcvm.WakeRequest{}, "", err
	}
	if !functionlifetime.NativeNode(wr.Runtime) || req.GetFcVersion() != evidence.FCVersion || s.fcVer != evidence.FCVersion || wr.Snapshot == nil {
		return fcvm.WakeRequest{}, "", runtimeadmission.ErrInvalid
	}
	wr.SnapshotRestore = &evidence
	wr.Snapshot.MemBytes = evidence.Capture.Memory.Bytes
	for _, drive := range evidence.Capture.Parent.ArtifactConsumption.Drives {
		wr.ArtifactSources = append(wr.ArtifactSources, drive.Source)
	}
	hash, err := fcvm.NativeWakeInputHash(wr)
	if err != nil {
		return fcvm.WakeRequest{}, "", err
	}
	return wr, hash, nil
}
func (s *Server) activateFunctionDestination(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest, d state.FunctionMigrationDestination, identity runtimeadmission.Identity, vmm functionDestinationVMM, store state.FunctionMigrationDestinationStore) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	if d.Grant.Binding.NodeID != s.nodeID || d.Grant.Binding.Incarnation != identity.Incarnation || d.Grant.WakeID != req.GetWakeId() {
		return nil, runtimeadmission.ErrStale
	}
	inst, receipt, err := vmm.ActivateFunctionMigrationDestination(ctx, d.Grant, store.CheckFunctionMigrationDestinationCommitted)
	if err != nil {
		return nil, err
	}
	if d.Receipt == nil || !d.Receipt.Equal(receipt) {
		return nil, runtimeadmission.ErrStale
	}
	if err := checkFunctionDestinationResult(d.Grant, inst, receipt, time.Unix(0, receipt.CompletedAtUnixNano)); err != nil {
		return nil, err
	}
	return functionDestinationResponse(req, inst), nil
}
func checkFunctionDestinationResult(g runtimeadmission.FunctionMigrationDestinationGrant, inst *fcvm.Instance, r runtimeadmission.Receipt, clock time.Time) error {
	b := g.Binding
	if inst == nil || inst.Paused || inst.Method != fcvm.WakeRestore || inst.Lease.Instance != b.InstanceID || inst.AppID != b.AppID || inst.AccountID != b.AccountID || inst.DeploymentID != b.DeploymentID ||
		r.Netns != inst.Net.Netns || r.HostIP != inst.Lease.HostIP.String() || r.LeaseUID != int32(inst.Lease.UID) || g.CheckReceipt(r, clock) != nil {
		return runtimeadmission.ErrStale
	}
	return nil
}
