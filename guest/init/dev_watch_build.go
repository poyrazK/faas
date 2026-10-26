package main

import (
	"fmt"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
)

// Developer watch mode (ADR-1170), builder side. A watch-mode developer build
// keeps Railpack's install step (development dependencies included), drops
// the command that transforms the source, ships the source unchanged and
// starts the app's development server. The live patches of ADR-740 then reach
// that server's own reloader.

// errDevWatchUnsupported prefixes every refusal so the build failure carries
// the stable dev_watch_unsupported code.
func errDevWatchUnsupported(format string, args ...any) error {
	return fmt.Errorf("%s: %s", api.CodeDevWatchUnsupported, fmt.Sprintf(format, args...))
}

// devWatchRuntime is what the watch overlay sets per Railpack language.
type devWatchRuntime struct {
	// binPath replaces the build step's commands: a non-executing path
	// command, so the step only layers the source onto the installed
	// dependencies and keeps their executables on PATH.
	binPath   string
	variables map[string]string
}

var devWatchRuntimes = map[api.BuildFramework]devWatchRuntime{
	api.FrameworkRailpackNode: {binPath: "/app/node_modules/.bin", variables: map[string]string{"NODE_ENV": "development"}},
	// Phase 4: the uv build step's `uv sync` over the full source installs
	// the project into the venv, which is what makes uv apps non-verbatim.
	// Without it the project runs from /app, so a src/ layout needs
	// PYTHONPATH to import itself.
	api.FrameworkRailpackPython: {binPath: "/app/.venv/bin", variables: map[string]string{"PYTHONPATH": "/app/src:/app"}},
}

// devWatchRailpackConfig adds the watch-mode settings to the platform
// railpack.json overlay. It replaces the build step's commands with a single
// non-executing path command, so the step only layers the source onto the
// installed dependencies; sets the development command as the start command;
// and marks the image with api.DevWatchEnv=1, which tells guest-init to apply
// live patches without restarting the server and to bridge the runtime port
// to it. Node.js images also get NODE_ENV=development. The app's public
// hostname goes in api.DevWatchHostEnv; at workload start guest-init also
// hands it to Vite-based servers, which answer 403 to a Host they do not
// know (stampDevWatchEnv).
func devWatchRailpackConfig(config map[string]any, m api.BuildManifest) error {
	if m.DevWatchCommand == "" {
		return nil
	}
	runtime, ok := devWatchRuntimes[m.Framework]
	if !ok {
		return errDevWatchUnsupported("watch mode supports Node.js and Python apps built without a Dockerfile; this build is %s", m.Framework)
	}
	steps, err := childObject(config, "steps")
	if err != nil {
		return err
	}
	build, err := childObject(steps, "build")
	if err != nil {
		return err
	}
	build["commands"] = []any{map[string]any{"path": runtime.binPath}}
	deploy, err := childObject(config, "deploy")
	if err != nil {
		return err
	}
	deploy["startCommand"] = m.DevWatchCommand
	variables, err := childObject(deploy, "variables")
	if err != nil {
		return err
	}
	for name, value := range runtime.variables {
		variables[name] = value
	}
	variables[api.DevWatchEnv] = "1"
	if m.DevWatchHost != "" && api.ValidDevWatchHost(m.DevWatchHost) {
		variables[api.DevWatchHostEnv] = m.DevWatchHost
	}
	return nil
}

// stampDevWatchEnv adds Vite's allowed-hosts override, set to the
// environment's hostname, to a watch-mode workload's environment. It cannot
// travel in the image: guest-init only passes environment keys that start
// with a letter (validEnvKey), and Vite's name starts with "__".
func stampDevWatchEnv(env []string, watch bool) []string {
	if !watch {
		return env
	}
	host := devDebugEnvValue(env, api.DevWatchHostEnv)
	if host == "" || !api.ValidDevWatchHost(host) {
		return env
	}
	return append(env, api.DevWatchViteAllowedHostsEnv+"="+host)
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

// devPatchApply writes a live patch. In watch mode it rewrites files in place,
// because the development server's watcher (node --watch) loses a file once a
// patch has renamed a new inode over it.
func devPatchApply(dir string, archive []byte, deleted []string) (devpatch.ApplyResult, error) {
	if devWatchActive.Load() {
		return devpatch.ApplyInPlace(dir, archive, deleted, api.DevPatchMaxBytes)
	}
	return devpatch.Apply(dir, archive, deleted, api.DevPatchMaxBytes)
}
