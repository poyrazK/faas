package main

import (
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// devPatchExplanation says why a developer sync rebuilds instead of being
// applied as a live patch (ADR-740), and what would make the app patchable.
// ok is false for reasons that are expected or transient (the first sync,
// a running version from before live patches), which stay quiet.
func devPatchExplanation(preview api.DevPatchPreview) (string, bool) {
	switch preview.Reason {
	case api.DevPatchReasonBuildCommand:
		return "your app has a build step, so edits go through the build. Apps that run their source files directly (no build script) are live-patched in seconds", true
	case api.DevPatchReasonNotRailpack:
		return "this app is built from a Dockerfile. Live patches work for apps Gregale builds without one", true
	case api.DevPatchReasonNoSourceLayer, api.DevPatchReasonSourceNotDeployed:
		return "the build does not ship your source files unchanged, so they cannot be patched in place", true
	case api.DevPatchReasonPlanUnreadable:
		return "the running build's plan could not be read, so this sync rebuilds", true
	case api.DevPatchReasonRebuildInput:
		return "you changed a file the build depends on (dependencies, a lockfile, railpack.json, gregale.yaml or .gregaleignore). Source-only edits after this build can be live-patched", true
	case api.DevPatchReasonUnsupportedEntry:
		return "the change includes a symlink or another non-regular file, which live patches do not carry", true
	case api.DevPatchReasonTooLarge:
		return fmt.Sprintf("the change (%d files, %s) is larger than a live patch allows (%d files, %s)",
			preview.ChangedPaths, formatDevPatchBytes(preview.PatchBytes), api.DevPatchMaxEntries, formatDevPatchBytes(api.DevPatchMaxBytes)), true
	case api.DevPatchReasonUnsupportedVersion:
		return "the running build predates this live patch format. After this build, source edits can be live-patched", true
	}
	return "", false
}

func formatDevPatchBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// devPatchExplainer reports why syncs rebuild without repeating itself: a
// reason is shown when it first appears, and again only after a sync with a
// different outcome. An app with a build step hears it once per session.
type devPatchExplainer struct {
	mu   sync.Mutex
	last string
}

// explain returns the line to print for this sync's preview, or "".
func (e *devPatchExplainer) explain(preview *api.DevPatchPreview) string {
	reason := ""
	if preview != nil && !preview.Eligible {
		reason = preview.Reason
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if reason == e.last {
		return ""
	}
	e.last = reason
	if reason == "" {
		return ""
	}
	why, ok := devPatchExplanation(*preview)
	if !ok {
		return ""
	}
	return "Full rebuild instead of a live patch: " + why + "."
}
