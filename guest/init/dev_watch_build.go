package main

import (
	"fmt"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// Developer watch mode (ADR-970), builder side. A watch-mode developer build
// keeps Railpack's install step (development dependencies included), drops
// the command that transforms the source, ships the source unchanged and
// starts the app's development server. The live patches of ADR-740 then reach
// that server's own reloader.

// errDevWatchUnsupported prefixes every refusal so the build failure carries
// the stable dev_watch_unsupported code.
func errDevWatchUnsupported(format string, args ...any) error {
	return fmt.Errorf("%s: %s", api.CodeDevWatchUnsupported, fmt.Sprintf(format, args...))
}

// devWatchRailpackConfig adds the watch-mode settings to the platform
// railpack.json overlay. It replaces the build step's commands with a single
// non-executing path command, so the step only layers the source onto the
// installed dependencies; sets the development command as the start command;
// and marks the image with NODE_ENV=development and api.DevWatchEnv=1, which
// tells guest-init to apply live patches without restarting the server.
func devWatchRailpackConfig(config map[string]any, m api.BuildManifest) error {
	if m.DevWatchCommand == "" {
		return nil
	}
	if m.Framework != api.FrameworkRailpackNode {
		return errDevWatchUnsupported("watch mode supports Node.js apps built without a Dockerfile; this build is %s", m.Framework)
	}
	steps, err := childObject(config, "steps")
	if err != nil {
		return err
	}
	build, err := childObject(steps, "build")
	if err != nil {
		return err
	}
	build["commands"] = []any{map[string]any{"path": "/app/node_modules/.bin"}}
	deploy, err := childObject(config, "deploy")
	if err != nil {
		return err
	}
	deploy["startCommand"] = m.DevWatchCommand
	variables, err := childObject(deploy, "variables")
	if err != nil {
		return err
	}
	variables["NODE_ENV"] = "development"
	variables[api.DevWatchEnv] = "1"
	return nil
}

// devWatchSourceMap finishes a watch-mode build's source map: the overlay
// must have produced a plan that ships the source unchanged, or the build
// fails rather than labelling a production-shaped image as watch mode.
func devWatchSourceMap(m api.BuildManifest, sourceMap *api.DevPatchSourceMap) (*api.DevPatchSourceMap, error) {
	if m.DevWatchCommand == "" {
		return sourceMap, nil
	}
	if sourceMap == nil || !sourceMap.Verbatim {
		reason := "no source map"
		if sourceMap != nil && sourceMap.Reason != "" {
			reason = sourceMap.Reason
		}
		return sourceMap, errDevWatchUnsupported("the watch-mode build plan still transforms the source (%s); set dev.watch.command to a command that runs your source directly", reason)
	}
	watched := *sourceMap
	watched.Watch = true
	return &watched, nil
}

// childObject returns parent[key] as an object, creating it when absent.
func childObject(parent map[string]any, key string) (map[string]any, error) {
	raw, ok := parent[key]
	if !ok || raw == nil {
		child := map[string]any{}
		parent[key] = child
		return child, nil
	}
	child, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("railpack.json %s is not an object", key)
	}
	return child, nil
}

// devWatchActive is set at main-workload start when the image is a
// watch-mode developer build (api.DevWatchEnv=1).
var devWatchActive atomic.Bool

// devPatchRestart wraps the workload restart used after a live patch. In
// watch mode the development server's own watcher reloads the written files,
// and a restart would throw away its compiled state, so it is skipped.
func devPatchRestart(restart func() error) func() error {
	return func() error {
		if devWatchActive.Load() {
			return nil
		}
		return restart()
	}
}
