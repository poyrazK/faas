package functionlifetime

// Snapshot hooks a function declares in code (ADR-20261110190000). guest-init
// calls these reserved loopback paths with its lifecycle marker; the runner
// answers them itself by running the hook in every loaded worker, so no
// customer route sees them.
const (
	FunctionBeforeSnapshotPath = "/__gregale/function/before-snapshot"
	FunctionAfterRestorePath   = "/__gregale/function/after-restore"
)

// Hook names as the adapters receive them in the envelope's function_hook.
const (
	FunctionHookBeforeSnapshot = "before_snapshot"
	FunctionHookAfterRestore   = "after_restore"
)

// FunctionHook names the in-code hook a guest-init lifecycle call asks for:
// the reserved path must match the marker guest-init sent (lifecycle is
// "before_checkpoint" or "after_restore", derived from the loopback peer and
// header, never from the request body). Anything else is "".
func FunctionHook(path, lifecycle string) string {
	switch {
	case path == FunctionBeforeSnapshotPath && lifecycle == "before_checkpoint":
		return FunctionHookBeforeSnapshot
	case path == FunctionAfterRestorePath && lifecycle == "after_restore":
		return FunctionHookAfterRestore
	}
	return ""
}
