package fcvm

// LivenessReasonImageHealthcheck is confirmed command failure after the
// effective image's consecutive retry threshold. It uses ordinary restart
// accounting and invalidates snapshots through scheduler-owned recovery.
const LivenessReasonImageHealthcheck = "image_healthcheck_unhealthy"

// LivenessReasonInfrastructure is emitted when the host has evidence that a
// liveness probe failed because the request/bridge path was under pressure,
// rather than because the guest reported an unhealthy workload. The schedd
// still replaces the affected VM, but must not use this observation by itself
// to permanently park the application.
const LivenessReasonInfrastructure = "liveness_infrastructure"

// LivenessReasonProcessExited is emitted when a transport-style probe failure
// is corroborated by the Firecracker process no longer being alive. This is a
// confirmed VM failure and is therefore eligible for the restart circuit
// breaker.
const LivenessReasonProcessExited = "liveness_process_exited"

// LivenessReasonTrafficUnreachable is emitted when the instance's requests
// keep failing at the bridge-to-guest hop even through a freshly started
// bridge (vmmdgrpc streamBridgeInstanceReport). On 2026-11-15/16 a restored
// instance in that state answered 502 for up to nine hours while its own
// health check passed. schedd replaces the VM; like infrastructure, the
// restart does not count toward parking the app.
const LivenessReasonTrafficUnreachable = "liveness_traffic_unreachable"

// LivenessReasonBudgeted reports whether a liveness restart counts toward the
// app's eviction budget: platform-side evidence (infrastructure pressure, an
// unreachable traffic hop) replaces the VM but never parks a healthy app.
func LivenessReasonBudgeted(reason string) bool {
	return reason != LivenessReasonInfrastructure && reason != LivenessReasonTrafficUnreachable
}
