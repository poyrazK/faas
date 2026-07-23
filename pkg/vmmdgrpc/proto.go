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
		Instance:         req.GetInstance(),
		BaseKey:          app.GetBaseKey(),
		LayerKey:         app.GetLayerKey(),
		VcpuCount:        int(app.GetVcpuCount()),
		MemSizeMiB:       int(app.GetMemSizeMib()),
		EgressMbit:       int(app.GetEgressMbit()),
		SealedEnvEntries: sealedFromProto(app.GetSealedEnv()),
	}
	if snap != nil {
		// #96 / ADR-025 axis 2 (slice 3) — mem_path is gone from the
		// proto. The StorageBackend is the only carrier for the mem
		// blob; if a caller hands us a SnapshotRef with an empty
		// StorageKey, fall back to cold-boot (the createcoldboot
		// branch) by leaving wr.Snapshot = nil. The Manager treats
		// nil Snapshot as cold-boot, which is exactly the
		// cold-boot-must-always-work guarantee (ADR-005).
		//
		// #121 / ADR-025 axis 2 slice 4 — vmstate_storage_key is the
		// canonical key the vmstate blob lives under when the new
		// StorageBackend carrier is used; vmstate_path is the legacy
		// host-path fallback (default-local single-box). Both flow
		// through unchanged so fcvm.Snapshot.Usable() can accept
		// either locator and pick the right resume path.
		if snap.GetStorageKey() == "" {
			return wr, nil
		}
		wr.Snapshot = &fcvm.Snapshot{
			VMStatePath:       snap.GetVmstatePath(),
			FCVersion:         snap.GetFcVersion(),
			StorageKey:        snap.GetStorageKey(),
			VMStateStorageKey: snap.GetVmstateStorageKey(),
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
		Instance:         req.GetInstance(),
		BaseKey:          app.GetBaseKey(),
		LayerKey:         app.GetLayerKey(),
		VcpuCount:        int(app.GetVcpuCount()),
		MemSizeMiB:       int(app.GetMemSizeMib()),
		EgressMbit:       int(app.GetEgressMbit()),
		SealedEnvEntries: sealedFromProto(app.GetSealedEnv()),
	}, nil
}

// sealedFromProto converts a slice of vmmdpb.SealedSecret into the fcvm
// shape Manager.Wake consumes. Nil in -> nil out (the Manager treats
// nil and empty equivalently: no StageSecretsEnv call). We don't reject
// malformed rows here — the recipient + key validation already happened
// at apid's PUT, and the Manager will surface an Open failure on a
// truly bogus ciphertext.
func sealedFromProto(pbs []*vmmdpb.SealedSecret) []fcvm.SealedEnvEntry {
	if len(pbs) == 0 {
		return nil
	}
	out := make([]fcvm.SealedEnvEntry, 0, len(pbs))
	for _, p := range pbs {
		out = append(out, fcvm.SealedEnvEntry{
			Key:        p.GetKey(),
			Ciphertext: p.GetCiphertext(),
		})
	}
	return out
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
