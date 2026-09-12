package api

// Debug running reason codes. This is intentionally a closed vocabulary: a
// customer should be able to act on each value, and the scheduler must not
// leak implementation-specific labels into the public debugger.
const (
	DebugRunningReasonRequestActivity   = "request_activity"
	DebugRunningReasonOpenConnection    = "open_connection"
	DebugRunningReasonTailTasks         = "tail_tasks"
	DebugRunningReasonMinInstances      = "min_instances"
	DebugRunningReasonPrewarmFloor      = "prewarm_floor"
	DebugRunningReasonScaleInCooldown   = "scale_in_cooldown"
	DebugRunningReasonWorkloadMode      = "workload_mode"
	DebugRunningReasonStartupGrace      = "startup_grace"
	DebugRunningReasonUnknownActivity   = "activity_unknown"
	DebugRunningReasonNoBlockerObserved = "no_blocker_observed"
)

// AllDebugRunningReasonCodes is the stable order used by documentation and
// validation tests. Runtime responses are sorted by this order before being
// serialized, so a repeated observation has a deterministic fingerprint.
var AllDebugRunningReasonCodes = []string{
	DebugRunningReasonRequestActivity,
	DebugRunningReasonOpenConnection,
	DebugRunningReasonTailTasks,
	DebugRunningReasonMinInstances,
	DebugRunningReasonPrewarmFloor,
	DebugRunningReasonScaleInCooldown,
	DebugRunningReasonWorkloadMode,
	DebugRunningReasonStartupGrace,
	DebugRunningReasonUnknownActivity,
	DebugRunningReasonNoBlockerObserved,
}
