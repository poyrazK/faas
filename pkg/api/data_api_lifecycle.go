package api

// The Data API runtime stops its engine before a snapshot and starts it after
// a restore, so no snapshot holds a SQL session (ADR-20261026131131,
// cmd/gregale/templates/data-api/engine.mjs). Every app that runs the runtime
// registers these hooks, whether the CLI or a data pipeline configured it.
const (
	DataAPIBeforeCheckpointPath = "/__gregale/lifecycle/before-checkpoint"
	DataAPIAfterRestorePath     = "/__gregale/lifecycle/after-restore"
)

// SetDataAPILifecycle registers the Data API runtime's snapshot hooks.
func (r *UpdateAppRequest) SetDataAPILifecycle() {
	r.BeforeCheckpoint = &BeforeCheckpointHook{Path: DataAPIBeforeCheckpointPath, TimeoutMS: AfterRestoreHookMaxTimeoutMS}
	r.AfterRestore = &AfterRestoreHook{Path: DataAPIAfterRestorePath, TimeoutMS: AfterRestoreHookMaxTimeoutMS}
}
