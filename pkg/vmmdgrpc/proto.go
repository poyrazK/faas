// Proto ↔ fcvm adapters. Kept separate from server.go so each handler stays
// under the §Conventions 50-line limit and so every conversion is in one
// place if a future proto revision lands.

package vmmdgrpc

import (
	"context"
	"net/netip"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/structpb"
)

// deploymentIDFromContext (issue #463 / ADR-069 / PR-B AC #1) lifts
// the per-deployment UUID schedd stamped onto the inbound gRPC MD
// via wire.WithCorrelationOutgoing(x-faas-deployment-id) at the
// schedd engine's bootCtx seam (pkg/sched/engine.go:1379). Returns ""
// for legacy callers that don't stamp the MD (pre-PR-B schedd
// versions); the Instance.DeploymentID is left empty and the
// sidecar-init-failed dispatch skips the deploy-row flip on "".
// ctx must be the inbound server-side ctx (the value passed to the
// vmmdgrpc handler that owns the toWakeRequest / toColdBootRequest
// call). The reader tolerates nil ctx — it returns "" — so future
// callers (e.g. metal tests) can call the converters directly.
func deploymentIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	corr, _ := wire.CorrelationFromIncoming(ctx)
	return corr.DeploymentID
}

// jobBootRequestFromProto converts the scheduler's flat job envelope into
// the Manager request. Keeping validation here makes malformed callers fail
// before vmmd allocates a slot or stages any image bytes.
func jobBootRequestFromProto(req *vmmdpb.JobColdBootRequest) (fcvm.JobBootRequest, error) {
	if req == nil {
		return fcvm.JobBootRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing request", "request is required")
	}
	checks := []struct {
		value string
		name  string
	}{
		{req.GetInstance(), "instance"},
		{req.GetAccountId(), "account_id"},
		{req.GetNodeId(), "node_id"},
		{req.GetPlan(), "plan"},
		{req.GetRunId(), "run_id"},
		{req.GetImageRef(), "image_ref"},
		{req.GetKernelKey(), "kernel_key"},
		{req.GetBaseKey(), "base_key"},
		{req.GetLeaseToken(), "lease_token"},
	}
	for _, check := range checks {
		if check.value == "" {
			return fcvm.JobBootRequest{}, api.NewProblem(int(codes.InvalidArgument),
				api.CodeValidation, "Invalid job request", check.name+" is required")
		}
	}
	plan := api.Plan(req.GetPlan())
	if !plan.Valid() {
		return fcvm.JobBootRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Invalid job request", "plan is invalid")
	}
	if len(req.GetCommand()) == 0 || len(req.GetCommand()) > 64 {
		return fcvm.JobBootRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Invalid job request", "command must contain between 1 and 64 arguments")
	}
	if req.GetTaskIndex() < 0 || req.GetVcpuCount() < 1 || req.GetMemSizeMib() < 1 || req.GetTaskTimeoutSec() < 1 {
		return fcvm.JobBootRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Invalid job request", "task_index, vcpu_count, mem_size_mib, and task_timeout_sec must be valid")
	}
	if req.GetTaskTimeoutSec() > fcvm.JobMaxTaskTimeoutSec {
		return fcvm.JobBootRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Invalid job request", "task_timeout_sec exceeds the host limit")
	}
	return fcvm.JobBootRequest{
		Instance:       req.GetInstance(),
		AccountID:      req.GetAccountId(),
		NodeID:         req.GetNodeId(),
		Plan:           plan,
		RunID:          req.GetRunId(),
		TaskIndex:      int(req.GetTaskIndex()),
		ImageRef:       req.GetImageRef(),
		KernelKey:      req.GetKernelKey(),
		BaseKey:        req.GetBaseKey(),
		Command:        append([]string(nil), req.GetCommand()...),
		Env:            cloneStringMap(req.GetEnv()),
		VcpuCount:      int(req.GetVcpuCount()),
		MemSizeMiB:     int(req.GetMemSizeMib()),
		TaskTimeoutSec: int(req.GetTaskTimeoutSec()),
		LeaseToken:     req.GetLeaseToken(),
	}, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// withIncomingCorrelation makes the wire envelope available to wake timing
// emitters throughout Manager and JailerVMM, independently of OTel setup.
func withIncomingCorrelation(ctx context.Context) context.Context {
	if fields, ok := wire.CorrelationFromIncoming(ctx); ok {
		return wire.WithContext(ctx, fields)
	}
	return ctx
}

// toWakeRequest flattens CreateFromSnapshotRequest into an fcvm.WakeRequest.
// The caller resolves (app) here; vmmd stores none of it (ADR-014).
func toWakeRequest(ctx context.Context, req *vmmdpb.CreateFromSnapshotRequest) (fcvm.WakeRequest, error) {
	if req.GetInstance() == "" {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing instance", "instance is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#create")
	}
	app := req.GetApp()
	if app == nil {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing app", "AppSpec is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#appspec")
	}
	snap := req.GetSnapshot()
	wr := fcvm.WakeRequest{
		Instance: req.GetInstance(),
		// issue #463 / ADR-069 / PR-B AC #1 — pull the deployment_id
		// schedd stamped onto the inbound gRPC MD so the vsock DGRAM
		// sidecar-init-failed dispatch can flip the deployments row
		// back to status='failed' on init exit. Empty for legacy
		// callers; the dispatch skips the flip on "".
		DeploymentID:     deploymentIDFromContext(ctx),
		BaseKey:          app.GetBaseKey(),
		LayerKey:         app.GetLayerKey(),
		VcpuCount:        int(app.GetVcpuCount()),
		MemSizeMiB:       int(app.GetMemSizeMib()),
		EgressMbit:       int(app.GetEgressMbit()),
		SealedEnvEntries: sealedFromProto(app.GetSealedEnv()),
		// Issue #395 / ADR-045: plaintext api_env channel. apid
		// enforces the per-plan EnvValueMaxBytes + EnvVarsMax
		// quota upstream; vmmd just forwards to StageAPIEnv which
		// writes /etc/faas/env.json on drive1. Empty slice = no
		// env.json file written (manifest env still flows in via
		// /etc/faas/app.json, the legacy path).
		APIEnvEntries: apiEnvFromProto(app.GetApiEnv()),
		// ADR-031: forward the per-app outbound IP allowlist on the
		// wake wire. apid parses + plan-gates + size-caps upstream;
		// vmmd translates CIDRs into netns.Config.EgressAllowlist on
		// Wake. Empty slice = no allowlist rule (current behaviour).
		EgressAllowlist: app.GetEgressAllowlist(),
		// tier-2 PR-B: schedd fans UpdateEgressAllowlist out by
		// app_id, so the live Instance needs to remember which app
		// it was woken for. The scheduler already knows the app
		// when it calls CreateFromSnapshot; passing it on the wire
		// means vmmd doesn't have to round-trip back to apid.
		AppID: app.GetAppId(),
		// issue #301 / ADR-044 — Plan + AccountID thread the
		// apps-row context onto the wire so vmmd can land the VM
		// under the per-plan cgroup sub-slice
		// (faas-tenant.slice/<plan-slice>/<instance>) and label
		// the vmmd_cpu_throttle_seconds_total counter. Empty
		// Plan falls back to the legacy 2-level path
		// (ParentCgroupRoot/<instance>) for pre-#301 callers;
		// new callers must always populate this.
		Plan:      api.Plan(req.GetPlan()),
		AccountID: req.GetAccountId(),
		// Issue #460 / ADR-053 (PR-C): per-deployment override
		// port copied from app.GetPort(). The host's waitReady +
		// DNAT stay fixed on 8080 (ADR-009 +
		// guest/init/portnorm_linux.go); vmmd's forwarder uses
		// this port to dial the guest. 0 = legacy 8080 default
		// at the buildBridgeScript boundary.
		Port: int(app.GetPort()),
		// Issue #460 / ADR-053, ADR-057 / PR-D: per-deployment
		// override readiness probe path. "" = legacy TCP-accept
		// on :8080 (pre-PR-D default). Non-empty → vmmd's
		// waitReady does HTTP GET <HealthcheckPath> against
		// <HostIP>:8080 and accepts 2xx as ready. The host
		// probe target is always :8080 — ADR-009 + portnorm
		// re-expose the customer bind on :8080 inside the guest,
		// so the path is the customer's choice and the port is
		// the host's choice.
		HealthcheckPath: app.GetHealthcheckPath(),
		// ADR-138: carry the per-app readiness budget to vmmd. 0 is
		// retained for pre-M3 callers, which use vmmd.readyTimeout.
		StartupDeadlineS: int(app.GetStartupDeadlineS()),
		// Issue #470 / PR #470-FU-B: the runner id (e.g.
		// "node22") is forwarded verbatim so the vmmd can
		// stamp it on the live Instance and the framework_ready
		// DGRAM receipt path can label the
		// vmmd_guest_framework_warmup_seconds histogram by
		// runner. Empty falls back to "unknown" in the
		// histogram observer. Bounded cardinality (≤5 runner
		// ids today; the runner set is guest-init build-time).
		Runtime: app.GetRuntime(),
		// Issue #463 / ADR-069 / PR-B: per-workload sidecar
		// wire. schedd populates AppSpec.sidecars from
		// deployment_sidecar_layers at wake time; vmmd turns
		// each entry into one FC Drive + one nested cgroup
		// scope. Empty slice = legacy single-workload path
		// (pre-PR-B callers). Additive per ADR-016.
		Sidecars: sidecarsFromProto(app.GetSidecars()),
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

// toMigrationWakeRequest adapts the migration-specific envelope into the
// normal snapshot wake path. Keeping one adapter means adoption receives the
// same environment, cgroup, network, and restore/fallback behavior as an
// ordinary CreateFromSnapshot call.
func toMigrationWakeRequest(ctx context.Context, req *vmmdpb.AdoptMigratedInstanceRequest) (fcvm.WakeRequest, error) {
	if req == nil {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing request", "request is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#adopt")
	}
	wr, err := toWakeRequest(ctx, &vmmdpb.CreateFromSnapshotRequest{
		Instance:  req.GetInstanceId(),
		App:       req.GetAppSpec(),
		Plan:      req.GetPlan(),
		AccountId: req.GetAccountId(),
		Snapshot: &vmmdpb.SnapshotRef{
			DeploymentId:      req.GetDeploymentId(),
			StorageKey:        req.GetMemStorageKey(),
			VmstateStorageKey: req.GetVmstateStorageKey(),
			FcVersion:         req.GetFcVersion(),
		},
	})
	if err != nil {
		return fcvm.WakeRequest{}, err
	}
	// A migration caller may not have propagated correlation metadata. The
	// explicit request field is authoritative for the Instance record.
	wr.DeploymentID = req.GetDeploymentId()
	return wr, nil
}

// toColdBootRequest flattens CreateColdBootRequest into an fcvm.WakeRequest
// with no snapshot. Same validations as toWakeRequest minus snapshot.
func toColdBootRequest(ctx context.Context, req *vmmdpb.CreateColdBootRequest) (fcvm.WakeRequest, error) {
	if req.GetInstance() == "" {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing instance", "instance is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#create")
	}
	app := req.GetApp()
	if app == nil {
		return fcvm.WakeRequest{}, api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing app", "AppSpec is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#appspec")
	}
	return fcvm.WakeRequest{
		Instance: req.GetInstance(),
		// issue #463 / ADR-069 / PR-B AC #1 — see toWakeRequest's
		// comment. Cold-boot mirrors the deployment_id resolution
		// so deploy's first boot (which is the cold-boot path,
		// per spec §9.6) starts with the deployments row stamped
		// onto the live Instance, ready for the sidecar-init-failed
		// dispatch to flip on init exit.
		DeploymentID:     deploymentIDFromContext(ctx),
		BaseKey:          app.GetBaseKey(),
		LayerKey:         app.GetLayerKey(),
		VcpuCount:        int(app.GetVcpuCount()),
		MemSizeMiB:       int(app.GetMemSizeMib()),
		EgressMbit:       int(app.GetEgressMbit()),
		SealedEnvEntries: sealedFromProto(app.GetSealedEnv()),
		// Issue #395 / ADR-045: see toWakeRequest's APIEnvEntries
		// comment. Cold-boot mirrors the wake path so deploy's
		// first boot primes the same plaintext env layer.
		APIEnvEntries: apiEnvFromProto(app.GetApiEnv()),
		// ADR-031: see toWakeRequest for the rationale; cold-boot
		// mirrors it so deploy primes the same egress policy.
		EgressAllowlist: app.GetEgressAllowlist(),
		// tier-2 PR-B: see toWakeRequest. The cold-boot path is
		// the first boot of a deploy; setting AppID here means
		// the very first UpdateEgressAllowlist fan-out finds the
		// instance via m.live[].AppID without a separate
		// bootstrap path.
		AppID: app.GetAppId(),
		// issue #301 / ADR-044 — see toWakeRequest. Cold-boot
		// mirrors Plan + AccountID so deploy's first boot on a
		// fresh VM lands under the per-plan cgroup sub-slice
		// and the throttle counter labels are populated.
		Plan:      api.Plan(req.GetPlan()),
		AccountID: req.GetAccountId(),
		// Issue #460 / ADR-053 (PR-C): see toWakeRequest for
		// rationale. Cold-boot mirrors the port so deploy's
		// first boot primes the same per-deployment override.
		Port: int(app.GetPort()),
		// Issue #460 / ADR-053, ADR-057 / PR-D: see
		// toWakeRequest. Cold-boot mirrors the healthcheck
		// path so deploy's first boot primes the same probe
		// semantics on the freshly-deployed app.
		HealthcheckPath: app.GetHealthcheckPath(),
		// ADR-138: cold-boot mirrors the snapshot wake's readiness budget.
		StartupDeadlineS: int(app.GetStartupDeadlineS()),
		// Issue #470 / PR #470-FU-B: see toWakeRequest.
		// Cold-boot mirrors the runtime so deploy's first
		// boot primes the same per-runner histogram labelling.
		Runtime: app.GetRuntime(),
		// Issue #463 / ADR-069 / PR-B: see toWakeRequest.
		// Cold-boot mirrors the per-workload sidecar wire so
		// deploy's first boot stages the same drives +
		// cgroups as every subsequent wake.
		Sidecars: sidecarsFromProto(app.GetSidecars()),
		// BuildSpec (spec §4.5, ADR-003) marks the cold-boot as a
		// builder VM. vmmd remembers the export dir and its Destroy
		// runs the build-aware teardown (wait for exit, capture
		// exit code, copy /build/out/* + build-done.json). Without
		// this mapping the Manager records no export dir, builderd's
		// WaitForCompletion finds no artifacts, and every build fails
		// after the VM exits.
		ExportDir:       buildSpecExportDir(req.GetBuild()),
		BuildTimeoutSec: buildSpecTimeoutSec(req.GetBuild()),
	}, nil
}

// buildSpecExportDir extracts the export dir from an optional
// BuildSpec. Nil Build (app VMs) or an empty ExportDir both map to ""
// so the Manager keeps the plain-Destroy contract.
func buildSpecExportDir(b *vmmdpb.BuildSpec) string {
	if b == nil {
		return ""
	}
	return b.GetExportDir()
}

// buildSpecTimeoutSec extracts the builder's configured guest timeout. Zero
// preserves the legacy/default path; Manager.Wake applies the platform
// default for builder VMs before recording the lease.
func buildSpecTimeoutSec(b *vmmdpb.BuildSpec) int {
	if b == nil {
		return 0
	}
	return int(b.GetTimeoutSec())
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

// apiEnvFromProto is the plaintext sibling of sealedFromProto (issue
// #395 / ADR-045). Mirrors the nil-in/nil-out shape — Manager.Wake
// treats nil and empty equivalently: no StageAPIEnv call. We don't
// re-validate key regex or byte cap here; apid's PUT handler enforces
// both against Limits.EnvVarsMax / Limits.EnvValueMaxBytes BEFORE the
// row reaches PG, so by the time the value arrives on the wire it's
// already trusted.
func apiEnvFromProto(pbs []*vmmdpb.APIEnvEntry) []fcvm.APIEnvEntry {
	if len(pbs) == 0 {
		return nil
	}
	out := make([]fcvm.APIEnvEntry, 0, len(pbs))
	for _, p := range pbs {
		out = append(out, fcvm.APIEnvEntry{
			Key:   p.GetKey(),
			Value: p.GetValue(),
		})
	}
	return out
}

// sidecarsFromProto (issue #463 / ADR-069 / PR-B) flattens the
// AppSpec.sidecars wire slice into fcvm.WorkloadSpec entries.
// Nil in / nil out matches sealedFromProto + apiEnvFromProto —
// the Manager treats nil and empty equivalently (legacy
// single-workload path).
//
// We don't re-validate name grammar, denylist, ram_mb range, or
// essential semantics here — schedd's caller (the wake path)
// already pulled the rows from deployment_sidecar_layers
// (which imaged stamped at build time after running the same
// gates), and the wire shape is a 1:1 mirror of those rows.
// vmmd trusts the wire as it trusts every other wake-field
// (the gRPC server runs on a unix socket reachable only by the
// faas group; ADR-014 / ADR-015).
func sidecarsFromProto(pbs []*vmmdpb.SidecarSpec) []fcvm.WorkloadSpec {
	if len(pbs) == 0 {
		return nil
	}
	out := make([]fcvm.WorkloadSpec, 0, len(pbs))
	for _, p := range pbs {
		sealedEnv := sealedFromProto(p.GetSealedEnv())
		out = append(out, fcvm.WorkloadSpec{
			Name:       p.GetName(),
			Type:       p.GetType(),
			Image:      p.GetImage(),
			StorageKey: p.GetStorageKey(),
			DriveID:    p.GetDriveSlot(),
			RamMB:      int(p.GetRamMb()),
			Port:       int(p.GetPort()),
			Essential:  p.GetEssential(),
			SealedEnv:  sealedEnv,
		})
	}
	return out
}

// wakeResponseFromInstance builds a WakeResponse from a just-woken instance.
// requestMethod is what the *caller* asked for (WAKE_RESTORE or
// WAKE_COLD_BOOT); the actual method reflects what Manager did (a restore
// that fell back reads WAKE_COLD_BOOT).
//
// ADR-051 Phase 4 / PR-D: when the wake was a cold boot and the host
// received a CharacterizationReport, it's shipped on the wire as a
// google.protobuf.Struct so schedd can persist the workload class via
// SetAppWorkloadClass. Restores leave Characterization nil (the class
// is inherited from the apps row captured in the original cold boot).
func wakeResponseFromInstance(instance string, req fcvm.WakeRequest, inst *fcvm.Instance, requestMethod vmmdpb.WakeMethod) *vmmdpb.WakeResponse {
	resp := &vmmdpb.WakeResponse{
		Instance:        instance,
		LeaseUid:        int32(inst.Lease.UID),
		HostIp:          addrOrEmpty(inst.Lease.HostIP),
		Netns:           inst.Net.Netns,
		VethHost:        inst.Net.VethHost,
		VethPeer:        inst.Net.VethPeer,
		Method:          wakeMethodFrom(inst.Method),
		RequestedMethod: requestMethod,
		// ADR-098 C11: phase-decomposed wake timings. RestoreMs is
		// 0 on cold boot (no /snapshot/load ran) and on any restore
		// that errored before /snapshot/load returned. NetnsTapMs
		// is stamped for both methods; the netns+TAP setup runs
		// every wake. GuestReadyMs is 0 on restore (the framework-
		// ready stamp is inherited from the original cold-boot's
		// row) and on cold-boot deadline-elapsed (guest never
		// reached ready). Schedd threads these onto the per-phase
		// wakePhaseDur histogram on the schedd side (issue #517 /
		// PR-C / ADR-064).
		RestoreMs:    inst.RestoreMs,
		NetnsTapMs:   inst.NetnsTapMs,
		GuestReadyMs: inst.GuestReadyMs,
	}
	if inst.Method == fcvm.WakeColdBoot {
		if structVal, ok := characterizationToStruct(inst.Characterization); ok {
			resp.Characterization = structVal
		}
	}
	return resp
}

func wakeMethodFrom(m fcvm.WakeMethod) vmmdpb.WakeMethod {
	if m == fcvm.WakeRestore {
		return vmmdpb.WakeMethod_WAKE_RESTORE
	}
	return vmmdpb.WakeMethod_WAKE_COLD_BOOT
}

// characterizationToStruct encodes a CharacterizationReport as a
// google.protobuf.Struct for the WakeResponse wire. The zero-value
// report (the guest's deadline elapsed, no class observed) returns
// (nil, false) so the wire field stays unset and schedd's "fall
// back to scan-hint class" path runs unchanged. The encoding shape
// mirrors pkg/api.CharacterizationReport's JSON tags exactly —
// schedd decodes via json.Unmarshal into the same struct.
func characterizationToStruct(r api.CharacterizationReport) (*structpb.Struct, bool) {
	// Empty report = nothing useful to ship. ObservedClass is the
	// canonical "did the probe run" signal: empty means bind_timeout
	// or ack_timeout.
	if r.ObservedClass == "" && r.ObservedPort == 0 && r.ExitCode == 0 {
		return nil, false
	}
	m := map[string]any{
		"observed_class": r.ObservedClass,
		"observed_port":  r.ObservedPort,
		"exit_code":      r.ExitCode,
		"outbound_count": r.OutboundCount,
		"log_tail":       r.LogTail,
		"port_norm_mode": r.PortNormalizationMode,
	}
	if len(r.ListeningAddrs) > 0 {
		l := make([]any, len(r.ListeningAddrs))
		for i, a := range r.ListeningAddrs {
			l[i] = a
		}
		m["listening_addrs"] = l
	}
	// ADR-122 §D2 — endpoint discovery. The OpenAPIDoc field is
	// wire-additive; structpb.NewStruct accepts []byte as a base64
	// string when the underlying MapValue is marshalled to JSON,
	// matching the characterization_test.go round-trip. The
	// truncation flag is a bool (no omitempty on the vmmdgrpc side
	// because the structpb path always materialises the key).
	if len(r.OpenAPIDoc) > 0 {
		m["openapi_doc"] = r.OpenAPIDoc
	}
	m["openapi_doc_truncated"] = r.OpenAPIDocTruncated
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil, false
	}
	return s, true
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

// toEgressAllowlist parses the wire's repeated string of CIDR
// literals into the netip.Prefix slice Manager.UpdateEgressAllowlist
// consumes. The wire carries the same shape as
// AppSpec.egress_allowlist (field 7), so the renderer partition
// (prefix.Addr().Is4()) works unchanged. A malformed entry is
// rejected with a typed Problem: the DB trigger and the apid
// validator already enforce v4-or-v6 + non-/0 upstream — a bad
// entry here is a contract violation that the manager surfaces
// with InvalidArgument rather than silently dropping the rule.
func toEgressAllowlist(ss []string) ([]netip.Prefix, error) {
	if len(ss) == 0 {
		return nil, nil
	}
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, api.NewProblem(int(codes.InvalidArgument),
				api.CodeValidation,
				"Invalid egress_allowlist entry",
				err.Error(),
			)
		}
		out = append(out, p)
	}
	return out, nil
}
