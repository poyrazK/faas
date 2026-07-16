// Proto ↔ fcvm adapters. Kept separate from server.go so each handler stays
// under the §Conventions 50-line limit and so every conversion is in one
// place if a future proto revision lands.

package vmmdgrpc

import (
	"net/netip"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/codes"
)

// toWakeRequest flattens CreateFromSnapshotRequest into an fcvm.WakeRequest.
// The caller resolves (app) here; vmmd stores none of it (ADR-014).
func toWakeRequest(req *vmmdpb.CreateFromSnapshotRequest) (fcvm.WakeRequest, error) {
	if req.GetInstance() == "" {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing instance", "instance is required").
			WithDocs("https://docs/DOMAIN/vmmd#create")
	}
	app := req.GetApp()
	if app == nil {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing app", "AppSpec is required").
			WithDocs("https://docs/DOMAIN/vmmd#appspec")
	}
	snap := req.GetSnapshot()
	wr := fcvm.WakeRequest{
		Instance:   req.GetInstance(),
		BasePath:   app.GetBasePath(),
		LayerPath:  app.GetLayerPath(),
		VcpuCount:  int(app.GetVcpuCount()),
		MemSizeMiB: int(app.GetMemSizeMib()),
	}
	if snap != nil && snap.GetMemPath() != "" {
		wr.Snapshot = &fcvm.Snapshot{
			MemPath:     snap.GetMemPath(),
			VMStatePath: snap.GetVmstatePath(),
			FCVersion:   snap.GetFcVersion(),
		}
	}
	return wr, nil
}

// toColdBootRequest flattens CreateColdBootRequest into an fcvm.WakeRequest
// with no snapshot. Same validations as toWakeRequest minus snapshot.
func toColdBootRequest(req *vmmdpb.CreateColdBootRequest) (fcvm.WakeRequest, error) {
	if req.GetInstance() == "" {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing instance", "instance is required").
			WithDocs("https://docs/DOMAIN/vmmd#create")
	}
	app := req.GetApp()
	if app == nil {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing app", "AppSpec is required").
			WithDocs("https://docs/DOMAIN/vmmd#appspec")
	}
	return fcvm.WakeRequest{
		Instance:   req.GetInstance(),
		BasePath:   app.GetBasePath(),
		LayerPath:  app.GetLayerPath(),
		VcpuCount:  int(app.GetVcpuCount()),
		MemSizeMiB: int(app.GetMemSizeMib()),
	}, nil
}

// wakeResponseFromInstance builds a WakeResponse from a just-woken instance.
// requestMethod is what the *caller* asked for (WAKE_RESTORE or
// WAKE_COLD_BOOT); the actual method reflects what Manager did (a restore
// that fell back reads WAKE_COLD_BOOT).
func wakeResponseFromInstance(instance string, req fcvm.WakeRequest, inst *fcvm.Instance, requestMethod vmmdpb.WakeMethod) *vmmdpb.WakeResponse {
	return &vmmdpb.WakeResponse{
		Instance:        instance,
		LeaseUid:        int32(inst.Lease.UID),
		HostIp:          addrOrEmpty(inst.Lease.HostIP),
		Netns:           inst.Net.Netns,
		VethHost:        inst.Net.VethHost,
		VethPeer:        inst.Net.VethPeer,
		Method:          wakeMethodFrom(inst.Method),
		RequestedMethod: requestMethod,
	}
}

func wakeMethodFrom(m fcvm.WakeMethod) vmmdpb.WakeMethod {
	if m == fcvm.WakeRestore {
		return vmmdpb.WakeMethod_WAKE_RESTORE
	}
	return vmmdpb.WakeMethod_WAKE_COLD_BOOT
}

// addrOrEmpty renders an addr as a string if valid; "" otherwise. Mirrors
// the netip.Addr.IsValid() guard so callers that hand us Lease.Zero /
// unset addr fields don't produce impossible literal strings.
func addrOrEmpty(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}
