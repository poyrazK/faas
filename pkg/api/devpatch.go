package api

// Developer live source patches (ADR-740). Builds record whether their source
// reached the image unchanged, apid reports whether each developer sync can be
// applied as a live patch and, when the operator enables delivery, publishes
// it for vmmd to serve to the running developer instances.

// Remote debugger attach for developer environments (ADR-741). The CLI sets
// DevDebugEnv on its developer app only; guest-init then starts the runtime's
// debugger listener on DevDebugNodePort, which only the authenticated
// /v1/apps/{slug}/debug tunnel can reach.
const (
	DevDebugEnv            = "FAAS_DEV_DEBUG"
	DevDebugRuntimeNode    = "node"
	DevDebugNodePort       = 9229
	DevDebugNodePortString = "9229"
)

// Developer watch mode (ADR-970). A watch-mode developer build ships the
// source unchanged and starts the app's development server; DevWatchEnv=1 is
// set in that image so guest-init applies live patches without restarting
// the workload and lets the server's own watcher reload them.
const (
	DevWatchEnv = "FAAS_DEV_WATCH"
	// DevWatchCommandMaxBytes bounds the development command.
	DevWatchCommandMaxBytes = 512
)

// DevPatchStatusResponse states.
const (
	DevPatchStatePending = "pending"
	DevPatchStateApplied = "applied"
	DevPatchStateFailed  = "failed"
)

// DevPatchSourceMapVersion is the current DevPatchSourceMap schema.
const DevPatchSourceMapVersion = 1

// DevPatchImageDir is where Railpack places the application source in the
// image (its fixed WORKDIR).
const DevPatchImageDir = "/app"

// Reasons a build or a developer sync cannot use a live patch. They are
// stable, machine-readable values carried in --json receipts.
const (
	DevPatchReasonNotRailpack        = "not_railpack"
	DevPatchReasonPlanUnreadable     = "plan_unreadable"
	DevPatchReasonNoSourceLayer      = "no_source_layer"
	DevPatchReasonBuildCommand       = "build_command"
	DevPatchReasonSourceNotDeployed  = "source_not_deployed"
	DevPatchReasonNoLiveBuild        = "no_live_build"
	DevPatchReasonNoBaseManifest     = "no_base_manifest"
	DevPatchReasonFullSnapshot       = "full_snapshot"
	DevPatchReasonRebuildInput       = "rebuild_input_changed"
	DevPatchReasonUnsupportedEntry   = "unsupported_entry"
	DevPatchReasonTooLarge           = "patch_too_large"
	DevPatchReasonUnsupportedVersion = "unsupported_source_map"
)

// DevPatchSourceMap is recorded per build. Verbatim means every file under
// the build's source root reaches ImageDir unchanged, so editing one cannot
// require a build step; RebuildPaths lists the source-relative paths (exact
// names or path.Match globs) that a build step does consume, so changing one
// still needs a real build.
type DevPatchSourceMap struct {
	Version      int      `json:"version"`
	Verbatim     bool     `json:"verbatim"`
	Reason       string   `json:"reason,omitempty"`
	ImageDir     string   `json:"image_dir,omitempty"`
	RebuildPaths []string `json:"rebuild_paths,omitempty"`
	// Watch marks a watch-mode developer build (ADR-970).
	Watch bool `json:"watch,omitempty"`
}
