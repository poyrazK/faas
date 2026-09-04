package fcvm

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
