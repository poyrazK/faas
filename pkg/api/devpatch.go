package api

// Developer live source patches (ADR-740). Phase 1 only measures: builds
// record whether their source reached the image unchanged, and apid reports
// whether each developer sync could have been applied as a live patch. No
// patch is delivered to a running instance yet.

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
}
