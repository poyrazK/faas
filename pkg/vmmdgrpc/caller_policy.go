package vmmdgrpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

// vmmdCaller is a class of certificate caller.
type vmmdCaller uint8

const (
	callerSchedd vmmdCaller = 1 << iota
	callerGateway
	callerBuilder
	callerImaged
	callerPgproxy
	// callerDatabasePeer is any verified daemon certificate: the method's
	// handler checks the peer itself (DatabaseReplication matches it to the
	// standby the replication op recorded).
	callerDatabasePeer
)

// callerClasses maps a client certificate's CN to its class (pkg/pki).
// gatewayd-internal.faas is the name earlier fleets issued.
var callerClasses = map[string]vmmdCaller{
	"schedd.faas":            callerSchedd,
	"gatewayd.faas":          callerGateway,
	"gatewayd-internal.faas": callerGateway,
	"builderd.faas":          callerBuilder,
	"imaged.faas":            callerImaged,
	"pgproxyd.faas":          callerPgproxy,
}

// vmmdCallerPolicy names, for every vmmd RPC, the certificate callers that
// may use it. vmmd authorized every caller by transport alone, and the H17
// fix only denied four exec and destroy methods to non-schedd certificates,
// so a stolen gateway certificate could still restore an app task and exec
// into it with ExecuteAppTaskStream, stop or park any tenant instance, open
// an app's egress allowlist, or ask for a snapshot under a storage key of its
// choosing (bug hunt H95). The policy is now an allow-list: schedd for
// lifecycle, exec and policy; each other daemon for the few RPCs it calls.
// A method without an entry is refused, and TestCallerPolicyCoversEveryMethod
// fails until a new RPC gets one. Unix-socket callers keep the filesystem
// boundary: every daemon runs as one uid, so peer credentials cannot tell
// them apart until daemons get distinct uids (H45).
var vmmdCallerPolicy = map[string]vmmdCaller{
	vmmdpb.Vmmd_MountParentExt4ReadOnly_FullMethodName:                 callerSchedd | callerImaged,
	vmmdpb.Vmmd_MaterializeParentExt4_FullMethodName:                   callerSchedd | callerImaged,
	vmmdpb.Vmmd_MaterializeVerifiedParentExt4_FullMethodName:           callerSchedd | callerImaged,
	vmmdpb.Vmmd_UmountParentExt4_FullMethodName:                        callerSchedd | callerImaged,
	vmmdpb.Vmmd_AcknowledgeMigration_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_AdoptMigratedInstance_FullMethodName:                   callerSchedd,
	vmmdpb.Vmmd_AdoptionInventory_FullMethodName:                       callerSchedd,
	vmmdpb.Vmmd_AdoptionReport_FullMethodName:                          callerSchedd,
	vmmdpb.Vmmd_AllowResolvedEgress_FullMethodName:                     callerSchedd | callerGateway,
	vmmdpb.Vmmd_AttachAppTask_FullMethodName:                           callerSchedd | callerGateway,
	vmmdpb.Vmmd_CancelLiveMigration_FullMethodName:                     callerSchedd,
	vmmdpb.Vmmd_CaptureAdmittedRuntime_FullMethodName:                  callerSchedd,
	vmmdpb.Vmmd_CaptureAgentSnapshot_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_CaptureEnvironmentQualification_FullMethodName:         callerSchedd,
	vmmdpb.Vmmd_CaptureProfile_FullMethodName:                          callerSchedd,
	vmmdpb.Vmmd_CreateAdmittedRuntime_FullMethodName:                   callerSchedd,
	vmmdpb.Vmmd_CreateColdBoot_FullMethodName:                          callerSchedd | callerBuilder,
	vmmdpb.Vmmd_CreateDatabaseVolume_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_CreateEnvironmentQualification_FullMethodName:          callerSchedd,
	vmmdpb.Vmmd_CreateFromSnapshot_FullMethodName:                      callerSchedd | callerBuilder,
	vmmdpb.Vmmd_CreateRehearsalColdBoot_FullMethodName:                 callerSchedd,
	vmmdpb.Vmmd_CreateRehearsalFromSnapshot_FullMethodName:             callerSchedd,
	vmmdpb.Vmmd_DataRepairRehearsalIdentity_FullMethodName:             callerSchedd,
	vmmdpb.Vmmd_DatabaseArchiveBytes_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_DatabaseArchiveHold_FullMethodName:                     callerSchedd,
	vmmdpb.Vmmd_DatabaseControl_FullMethodName:                         callerSchedd,
	vmmdpb.Vmmd_DatabaseReplication_FullMethodName:                     callerSchedd | callerDatabasePeer,
	vmmdpb.Vmmd_DatabaseSession_FullMethodName:                         callerSchedd | callerPgproxy,
	vmmdpb.Vmmd_DeleteDatabaseVolume_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_DeleteWarmSnapshot_FullMethodName:                      callerSchedd | callerBuilder,
	vmmdpb.Vmmd_Destroy_FullMethodName:                                 callerSchedd | callerBuilder,
	vmmdpb.Vmmd_ExecForkCommand_FullMethodName:                         callerSchedd,
	vmmdpb.Vmmd_ExecuteAppTask_FullMethodName:                          callerSchedd,
	vmmdpb.Vmmd_ExecuteAppTaskStream_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_ExecuteExecution_FullMethodName:                        callerSchedd,
	vmmdpb.Vmmd_ExecuteExecutionBrokerStream_FullMethodName:            callerSchedd,
	vmmdpb.Vmmd_ExecuteExecutionStream_FullMethodName:                  callerSchedd,
	vmmdpb.Vmmd_ExecuteOperatorPark_FullMethodName:                     callerSchedd,
	vmmdpb.Vmmd_ExecuteOperatorRestartDestroy_FullMethodName:           callerSchedd,
	vmmdpb.Vmmd_ExportDatabaseVolume_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_ForwardHTTPStream_FullMethodName:                       callerSchedd | callerGateway,
	vmmdpb.Vmmd_ForwardRawStream_FullMethodName:                        callerSchedd | callerGateway,
	vmmdpb.Vmmd_ForwardTCPStream_FullMethodName:                        callerSchedd | callerGateway,
	vmmdpb.Vmmd_ForwardUDPStream_FullMethodName:                        callerSchedd | callerGateway,
	vmmdpb.Vmmd_FrameworkReady_FullMethodName:                          callerSchedd,
	vmmdpb.Vmmd_FreezeInstance_FullMethodName:                          callerSchedd,
	vmmdpb.Vmmd_GetOperatorRestartGeneration_FullMethodName:            callerSchedd,
	vmmdpb.Vmmd_GrowDatabaseVolume_FullMethodName:                      callerSchedd,
	vmmdpb.Vmmd_Heartbeat_FullMethodName:                               callerSchedd,
	vmmdpb.Vmmd_ImportDatabaseVolume_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_InflightRequestTrace_FullMethodName:                    callerSchedd | callerGateway,
	vmmdpb.Vmmd_JobColdBoot_FullMethodName:                             callerSchedd,
	vmmdpb.Vmmd_JobColdBootHeld_FullMethodName:                         callerSchedd,
	vmmdpb.Vmmd_ListDatabaseVolumes_FullMethodName:                     callerSchedd,
	vmmdpb.Vmmd_Logs_FullMethodName:                                    callerSchedd | callerGateway,
	vmmdpb.Vmmd_MaterializeRuntimeScan_FullMethodName:                  callerSchedd | callerImaged,
	vmmdpb.Vmmd_MountOverlayParent_FullMethodName:                      callerSchedd | callerImaged,
	vmmdpb.Vmmd_PauseAndSnapshot_FullMethodName:                        callerSchedd,
	vmmdpb.Vmmd_Ping_FullMethodName:                                    callerSchedd | callerGateway | callerBuilder | callerImaged | callerPgproxy,
	vmmdpb.Vmmd_PrepareLiveMigration_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_PromoteAdmittedRuntime_FullMethodName:                  callerSchedd,
	vmmdpb.Vmmd_PruneDatabaseArchive_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_ReconcilePrivateNetworkFabric_FullMethodName:           callerSchedd,
	vmmdpb.Vmmd_ReconcilePrivateNetworkPeerings_FullMethodName:         callerSchedd,
	vmmdpb.Vmmd_RehearseSuspendResume_FullMethodName:                   callerSchedd,
	vmmdpb.Vmmd_ReleaseJobStart_FullMethodName:                         callerSchedd,
	vmmdpb.Vmmd_RemovePrivateNetworkFabric_FullMethodName:              callerSchedd,
	vmmdpb.Vmmd_ResolveServiceCaller_FullMethodName:                    callerSchedd | callerGateway,
	vmmdpb.Vmmd_RestoreAppTask_FullMethodName:                          callerSchedd,
	vmmdpb.Vmmd_RestoreEnvironmentQualification_FullMethodName:         callerSchedd,
	vmmdpb.Vmmd_RestoreExecution_FullMethodName:                        callerSchedd,
	vmmdpb.Vmmd_RestoreManagedServiceProbe_FullMethodName:              callerSchedd,
	vmmdpb.Vmmd_RestoreRehearsalAppTask_FullMethodName:                 callerSchedd,
	vmmdpb.Vmmd_RestrictPrivateNetworkPeerings_FullMethodName:          callerSchedd,
	vmmdpb.Vmmd_ResumeWarmInstance_FullMethodName:                      callerSchedd,
	vmmdpb.Vmmd_RetireDataRepairRehearsal_FullMethodName:               callerSchedd,
	vmmdpb.Vmmd_RetireEnvironmentQualification_FullMethodName:          callerSchedd,
	vmmdpb.Vmmd_RetireEnvironmentQualificationArtifacts_FullMethodName: callerSchedd,
	vmmdpb.Vmmd_RetireManagedPostgresDrain_FullMethodName:              callerSchedd,
	vmmdpb.Vmmd_RetireMigrationAdoption_FullMethodName:                 callerSchedd,
	vmmdpb.Vmmd_RetireNativeRuntimeSnapshotObject_FullMethodName:       callerSchedd | callerImaged,
	vmmdpb.Vmmd_RunAgent_FullMethodName:                                callerSchedd,
	vmmdpb.Vmmd_RunDataRepairRehearsal_FullMethodName:                  callerSchedd,
	vmmdpb.Vmmd_RuntimeAdmissionIdentity_FullMethodName:                callerSchedd,
	vmmdpb.Vmmd_SeccompStatus_FullMethodName:                           callerSchedd,
	vmmdpb.Vmmd_StartDatabase_FullMethodName:                           callerSchedd,
	vmmdpb.Vmmd_Stats_FullMethodName:                                   callerSchedd | callerGateway,
	vmmdpb.Vmmd_StopDatabase_FullMethodName:                            callerSchedd,
	vmmdpb.Vmmd_StopInstance_FullMethodName:                            callerSchedd | callerBuilder,
	vmmdpb.Vmmd_SyncDatabaseArchive_FullMethodName:                     callerSchedd,
	vmmdpb.Vmmd_ThawInstance_FullMethodName:                            callerSchedd,
	vmmdpb.Vmmd_UmountOverlayParent_FullMethodName:                     callerSchedd | callerImaged,
	vmmdpb.Vmmd_UpdateAdmittedAppEgressPolicy_FullMethodName:           callerSchedd,
	vmmdpb.Vmmd_UpdateAppCPULimit_FullMethodName:                       callerSchedd,
	vmmdpb.Vmmd_UpdateAppEgressPolicy_FullMethodName:                   callerSchedd,
	vmmdpb.Vmmd_UpdateEgressAllowlist_FullMethodName:                   callerSchedd,
	vmmdpb.Vmmd_UpdateEgressCircuit_FullMethodName:                     callerSchedd,
	vmmdpb.Vmmd_UpdatePrivateNetwork_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_UpdateStaticEgressIP_FullMethodName:                    callerSchedd,
	vmmdpb.Vmmd_WaitBuilderReady_FullMethodName:                        callerSchedd | callerBuilder,
	vmmdpb.Vmmd_WaitJobExit_FullMethodName:                             callerSchedd,
	vmmdpb.Vmmd_WarmSnapshot_FullMethodName:                            callerSchedd | callerBuilder,
}

// CallerPolicyUnaryInterceptor enforces vmmdCallerPolicy on unary RPCs.
func CallerPolicyUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authorizeCaller(ctx, info.FullMethod, req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// CallerPolicyStreamInterceptor enforces vmmdCallerPolicy on streams.
func CallerPolicyStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorizeCaller(ss.Context(), info.FullMethod, nil); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func authorizeCaller(ctx context.Context, method string, req any) error {
	if p, ok := peer.FromContext(ctx); ok && p != nil && p.AuthInfo == nil && p.Addr != nil && p.Addr.Network() == "unix" {
		return nil
	}
	allowed, known := vmmdCallerPolicy[method]
	if !known {
		return status.Error(codes.PermissionDenied, "vmmd method "+method+" has no caller policy")
	}
	cn, err := wire.PeerCN(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, method+" requires verified daemon credentials")
	}
	class := callerClasses[cn]
	switch {
	case class&allowed&^callerBuilder != 0:
		return nil
	case class == callerBuilder && allowed&callerBuilder != 0 && builderMayCall(method, req):
		return nil
	case allowed&callerDatabasePeer != 0:
		return nil
	}
	return status.Error(codes.PermissionDenied, cn+" may not call "+method)
}

// builderInstancePrefix names the builder VMs builderd boots over TCP and must
// tear down itself (pkg/builderd/vm_metal.go). Rejecting those destroys failed
// every build on rc.301 and leaked the builder's cgroup, netns and jail.
const builderInstancePrefix = "build-"

// builderMayCall lets builderd's certificate act only on builder instances.
// Tenant instance IDs are UUIDs minted by schedd, so they never carry the
// prefix, and builderd never retires rehearsal workers. A request that names
// no instance (Ping, warm snapshot deletion) passes, except Destroy and
// StopInstance, which must name a builder.
func builderMayCall(method string, req any) bool {
	if d, ok := req.(*vmmdpb.DestroyRequest); ok && (d.GetRehearsalColdPreparationId() != "" || d.GetRehearsalWorkerFaultId() != "") {
		return false
	}
	named, ok := req.(interface{ GetInstance() string })
	if !ok || named.GetInstance() == "" {
		return method != vmmdpb.Vmmd_Destroy_FullMethodName && method != vmmdpb.Vmmd_StopInstance_FullMethodName
	}
	id, found := strings.CutPrefix(named.GetInstance(), builderInstancePrefix)
	return found && id != ""
}
