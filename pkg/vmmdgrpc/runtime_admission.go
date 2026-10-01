// adr: 386 — additive native boot capability; never manufacture a receipt.
package vmmdgrpc

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type admittedRuntimeVMM interface {
	RuntimeAdmissionIdentity() (runtimeadmission.Identity, error)
	WakeAdmitted(context.Context, fcvm.AdmittedWakeRequest, fcvm.WakeNetworkReadyHook) (*fcvm.Instance, runtimeadmission.Receipt, error)
}

func (s *Server) RuntimeAdmissionIdentity(ctx context.Context, req *vmmdpb.RuntimeAdmissionIdentityRequest) (*vmmdpb.RuntimeAdmissionIdentityResponse, error) {
	vmm, ok := s.vmm.(admittedRuntimeVMM)
	if !ok {
		return nil, admissionStatus(runtimeadmission.ErrUnavailable)
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	if err := runtimeadmission.RejectUnknown(req); err != nil {
		return nil, admissionStatus(err)
	}
	i, err := s.admittedRuntimeIdentity(vmm)
	if err != nil {
		return nil, admissionStatus(err)
	}
	return &vmmdpb.RuntimeAdmissionIdentityResponse{ProtocolVersion: i.ProtocolVersion, NodeId: i.NodeID, Incarnation: i.Incarnation}, nil
}

func (s *Server) admittedRuntimeIdentity(vmm admittedRuntimeVMM) (runtimeadmission.Identity, error) {
	i, err := vmm.RuntimeAdmissionIdentity()
	if err != nil {
		return runtimeadmission.Identity{}, err
	}
	if err := i.Validate(); err != nil {
		return runtimeadmission.Identity{}, err
	}
	if s.nodeID == "" || i.NodeID != s.nodeID {
		return runtimeadmission.Identity{}, runtimeadmission.ErrUnavailable
	}
	return i, nil
}

func (s *Server) CreateAdmittedRuntime(ctx context.Context, req *vmmdpb.CreateAdmittedRuntimeRequest) (_ *vmmdpb.CreateAdmittedRuntimeResponse, err error) {
	start := time.Now()
	defer func() { s.ops.Observe("CreateAdmittedRuntime", time.Since(start), err) }()
	vmm, ok := s.vmm.(admittedRuntimeVMM)
	if !ok {
		return nil, admissionStatus(runtimeadmission.ErrUnavailable)
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	ctx = withIncomingCorrelation(ctx)
	native, method, protocol, err := s.parseAdmittedRuntime(ctx, req, vmm)
	if err != nil {
		return nil, admissionStatus(err)
	}
	methodName := "cold_boot"
	if method == vmmdpb.WakeMethod_WAKE_RESTORE {
		methodName = "restore"
	}
	wakeCtx, span := newWakeSpan(ctx, methodName, native.Request)
	inst, receipt, err := s.wakeAdmittedWithBridge(wakeCtx, vmm, native, protocol)
	finishWakeSpan(span, err)
	if err != nil {
		return nil, admissionStatus(err)
	}
	if err := checkNativeReceipt(native, inst, receipt); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cancel()
		_ = s.vmm.Destroy(cleanupCtx, native.Binding.InstanceID)
		if s.streamBridges != nil {
			s.streamBridges.forget(cleanupCtx, native.Binding.InstanceID)
		}
		return nil, admissionStatus(err)
	}
	return &vmmdpb.CreateAdmittedRuntimeResponse{Runtime: wakeResponseFromInstance(native.Binding.InstanceID, native.Request, inst, method), Receipt: receipt.ToProto()}, nil
}

// Remote grants require the scheduler's verified daemon certificate. The
// existing default-local Unix socket uses its filesystem access boundary.
func authorizeRuntimeAdmissionPeer(ctx context.Context) error {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return status.Error(codes.Unauthenticated, "runtime admission requires a scheduler peer")
	}
	if p.AuthInfo == nil && p.Addr != nil && p.Addr.Network() == "unix" {
		return nil
	}
	cn, err := wire.PeerCN(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, "runtime admission requires verified daemon credentials")
	}
	if cn != "schedd.faas" {
		return status.Error(codes.PermissionDenied, "only schedd may issue runtime admission grants")
	}
	return nil
}

func (s *Server) parseAdmittedRuntime(ctx context.Context, req *vmmdpb.CreateAdmittedRuntimeRequest, vmm admittedRuntimeVMM) (fcvm.AdmittedWakeRequest, vmmdpb.WakeMethod, string, error) {
	var empty fcvm.AdmittedWakeRequest
	hash, err := runtimeadmission.HashBootPayload(req)
	if err != nil {
		return empty, 0, "", err
	}
	// Own a copy of the request before flattening nested slices into native inputs.
	req = proto.Clone(req).(*vmmdpb.CreateAdmittedRuntimeRequest)
	b, err := runtimeadmission.BindingFromProto(req.Binding)
	if err != nil {
		return empty, 0, "", err
	}
	if err := b.Validate(time.Now()); err != nil {
		return empty, 0, "", err
	}
	if b.PayloadHash != hash {
		return empty, 0, "", runtimeadmission.ErrInvalid
	}
	i, err := s.admittedRuntimeIdentity(vmm)
	if err != nil {
		return empty, 0, "", err
	}
	if b.NodeID != i.NodeID || b.Incarnation != i.Incarnation {
		return empty, 0, "", runtimeadmission.ErrStale
	}
	var wr fcvm.WakeRequest
	var app *vmmdpb.AppSpec
	method := vmmdpb.WakeMethod_WAKE_COLD_BOOT
	switch boot := req.Boot.(type) {
	case *vmmdpb.CreateAdmittedRuntimeRequest_ColdBoot:
		if boot.ColdBoot == nil || boot.ColdBoot.Build != nil {
			return empty, 0, "", runtimeadmission.ErrInvalid
		}
		app = boot.ColdBoot.App
		wr, err = toColdBootRequest(ctx, boot.ColdBoot)
	case *vmmdpb.CreateAdmittedRuntimeRequest_Restore:
		if boot.Restore == nil || boot.Restore.Build != nil || boot.Restore.Snapshot == nil || boot.Restore.Snapshot.DeploymentId != b.DeploymentID || boot.Restore.Snapshot.Networkless {
			return empty, 0, "", runtimeadmission.ErrInvalid
		}
		app = boot.Restore.App
		wr, err = toWakeRequest(ctx, boot.Restore)
		method = vmmdpb.WakeMethod_WAKE_RESTORE
	default:
		return empty, 0, "", runtimeadmission.ErrInvalid
	}
	if err != nil {
		return empty, 0, "", err
	}
	if err := validateAdmittedApp(app); err != nil {
		return empty, 0, "", err
	}
	if wr.Instance != b.InstanceID || wr.AppID != b.AppID || wr.AccountID != b.AccountID || wr.DeploymentID != "" && wr.DeploymentID != b.DeploymentID || wr.KeepPaused && wr.Snapshot == nil {
		return empty, 0, "", runtimeadmission.ErrInvalid
	}
	// Explicit bound identity is authoritative; correlation metadata cannot
	// change the deployment behind the boot digest.
	wr.DeploymentID = b.DeploymentID
	nativeHash, err := fcvm.NativeWakeInputHash(wr)
	if err != nil {
		return empty, 0, "", err
	}
	return fcvm.AdmittedWakeRequest{Request: wr, Binding: b, NativeInputHash: nativeHash}, method, app.AppProtocol, nil
}

func validateAdmittedApp(app *vmmdpb.AppSpec) error {
	if app == nil || app.Port > 65535 {
		return runtimeadmission.ErrInvalid
	}
	for _, port := range app.EgressPorts {
		if _, forbidden := api.TenantEgressForbiddenPort(int(port)); port == 0 || port > 65535 || forbidden {
			return runtimeadmission.ErrInvalid
		}
	}
	for _, sidecar := range app.Sidecars {
		if sidecar == nil || sidecar.Port > 65535 {
			return runtimeadmission.ErrInvalid
		}
	}
	return nil
}

func checkNativeReceipt(req fcvm.AdmittedWakeRequest, inst *fcvm.Instance, receipt runtimeadmission.Receipt) error {
	if err := receipt.Check(req.Binding, time.Now()); err != nil {
		return err
	}
	if inst == nil || receipt.NativeInputHash != req.NativeInputHash || receipt.Netns != inst.Net.Netns || receipt.HostIP != inst.Lease.HostIP.String() || receipt.LeaseUID != int32(inst.Lease.UID) || inst.Lease.Instance != req.Binding.InstanceID || inst.AppID != req.Binding.AppID || inst.DeploymentID != req.Binding.DeploymentID || inst.AccountID != req.Binding.AccountID || receipt.Paused != inst.Paused || receipt.Paused != req.Request.KeepPaused || receipt.Method != wakeMethodFrom(inst.Method) {
		return runtimeadmission.ErrInvalid
	}
	return nil
}

func (s *Server) wakeAdmittedWithBridge(ctx context.Context, vmm admittedRuntimeVMM, req fcvm.AdmittedWakeRequest, protocol string) (*fcvm.Instance, runtimeadmission.Receipt, error) {
	var hook fcvm.WakeNetworkReadyHook
	if currentStreamBridgeVersion() == "v2" && persistentStreamBridgeEnabled() {
		if s.streamBridges == nil {
			s.streamBridges = newStreamBridgeManager(s.log)
		}
		hook = func(ready fcvm.WakeNetworkReady) {
			s.streamBridges.prewarm(&vmmdpb.ForwardHTTPRequestInit{Instance: ready.Instance, Port: uint32(req.Request.Port), AppProtocol: protocol}, ready.Netns)
		}
	}
	inst, receipt, err := vmm.WakeAdmitted(ctx, req, hook)
	if err != nil && s.streamBridges != nil {
		s.streamBridges.forget(context.WithoutCancel(ctx), req.Binding.InstanceID)
	}
	return inst, receipt, err
}

func admissionStatus(err error) error {
	code := codes.Internal
	switch {
	case errors.Is(err, runtimeadmission.ErrUnavailable):
		code = codes.Unimplemented
	case errors.Is(err, runtimeadmission.ErrInvalid):
		code = codes.InvalidArgument
	case errors.Is(err, runtimeadmission.ErrStale), errors.Is(err, runtimeadmission.ErrExpired):
		code = codes.FailedPrecondition
	case errors.Is(err, runtimeadmission.ErrReplay):
		code = codes.AlreadyExists
	case errors.Is(err, runtimeadmission.ErrCapacity):
		code = codes.ResourceExhausted
	default:
		return grpcerr.ToStatus(toProblem(err))
	}
	return status.Error(code, err.Error())
}
