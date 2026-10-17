// Package sharedruntime decides whether a source build's runtime artifact can
// run on the shared runner base instead of carrying its own toolchain
// (docs/adr/1500-shared-runtime-source-apps.md, H10-140).
package sharedruntime

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Reasons an ineligible build reports. They are stable values for metrics
// and build logs.
const (
	ReasonPlanUnreadable      = "plan_unreadable"
	ReasonPackageUnreadable   = "package_json_unreadable"
	ReasonBaseNotPlatform     = "base_not_platform"
	ReasonSystemPackages      = "system_packages"
	ReasonExtraDeployInput    = "extra_deploy_input"
	ReasonNoStartCommand      = "no_start_command"
	ReasonPackageManagerStart = "package_manager_start"
	ReasonNPMArguments        = "npm_arguments"
	ReasonMissingScript       = "missing_script"
	ReasonNPMLifecycleHooks   = "npm_lifecycle_hooks"
)

// toolchainStep is the Railpack step that installs the language toolchain
// with mise. The runner base supplies that toolchain, so its deploy paths are
// dropped rather than copied.
const toolchainStep = "packages:mise"

// runnerPath is the runner base's PATH with the app's local binaries first,
// which is where npm puts them when it runs a script.
const runnerPath = "/app/node_modules/.bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

var toolchainPaths = []string{"/mise", "/usr/local/bin/mise", "/etc/mise", "/root/.local/state/mise"}

var appPaths = []string{"/app", "/root/.cache"}

type plan struct {
	Steps  []step `json:"steps"`
	Deploy struct {
		Base         layer             `json:"base"`
		Inputs       []layer           `json:"inputs"`
		StartCommand string            `json:"startCommand"`
		Variables    map[string]string `json:"variables"`
		AptPackages  []string          `json:"aptPackages"`
	} `json:"deploy"`
}

type step struct {
	Name     string            `json:"name"`
	Inputs   []layer           `json:"inputs"`
	Commands []json.RawMessage `json:"commands"`
}

type layer struct {
	Image   string   `json:"image"`
	Step    string   `json:"step"`
	Local   bool     `json:"local"`
	Include []string `json:"include"`
}

// ClassifyNode reads a Railpack plan and the app's package.json.
func ClassifyNode(planJSON, packageJSON []byte) api.SharedRuntimeVerdict {
	var p plan
	if err := json.Unmarshal(planJSON, &p); err != nil {
		return ineligible(ReasonPlanUnreadable)
	}
	if reason := checkBase(p); reason != "" {
		return ineligible(reason)
	}
	if reason := checkDeployInputs(p.Deploy.Inputs); reason != "" {
		return ineligible(reason)
	}
	start, reason := resolveStart(p.Deploy.StartCommand, packageJSON)
	if reason != "" {
		return ineligible(reason)
	}
	env := make(map[string]string, len(p.Deploy.Variables)+1)
	for k, v := range p.Deploy.Variables {
		env[k] = v
	}
	env["PATH"] = runnerPath
	return api.SharedRuntimeVerdict{Eligible: true, Start: start, Env: env}
}

func ineligible(reason string) api.SharedRuntimeVerdict {
	return api.SharedRuntimeVerdict{Reason: reason}
}

// checkBase accepts the platform base image directly, or a base step that
// starts from an image and runs nothing on it. A step that installs system
// packages puts them outside /app, where the runner base would lose them.
func checkBase(p plan) string {
	if len(p.Deploy.AptPackages) > 0 {
		return ReasonSystemPackages
	}
	base := p.Deploy.Base
	if base.Image != "" && base.Step == "" {
		return ""
	}
	if base.Step == "" {
		return ReasonBaseNotPlatform
	}
	for _, s := range p.Steps {
		if s.Name != base.Step {
			continue
		}
		for _, in := range s.Inputs {
			if in.Image == "" {
				return ReasonBaseNotPlatform
			}
		}
		for _, raw := range s.Commands {
			var c struct {
				Cmd string `json:"cmd"`
			}
			if json.Unmarshal(raw, &c) == nil && c.Cmd != "" {
				return ReasonSystemPackages
			}
		}
		return ""
	}
	return ReasonBaseNotPlatform
}

func checkDeployInputs(inputs []layer) string {
	for _, in := range inputs {
		if in.Image != "" || in.Local || in.Step == "" {
			return ReasonExtraDeployInput
		}
		allowed := appPaths
		if in.Step == toolchainStep {
			allowed = toolchainPaths
		}
		for _, inc := range in.Include {
			if !within(inc, allowed) {
				return ReasonExtraDeployInput
			}
		}
	}
	return ""
}

// within reports whether p names the app directory (".") or a path under one
// of roots.
func within(p string, roots []string) bool {
	if p == "." {
		return true
	}
	clean := path.Clean(p)
	for _, root := range roots {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return true
		}
	}
	return false
}

// resolveStart turns Railpack's start command into an argv that needs no
// package manager. `npm start` and `npm run <script>` run the script body
// through sh, as npm does; lifecycle hooks and forwarded arguments keep the
// build on the full-layer path rather than approximating npm.
func resolveStart(command string, packageJSON []byte) ([]string, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, ReasonNoStartCommand
	}
	fields := strings.Fields(command)
	switch fields[0] {
	case "npm":
	case "npx", "yarn", "pnpm", "bun", "corepack":
		return nil, ReasonPackageManagerStart
	default:
		return []string{"/bin/sh", "-c", command}, ""
	}
	var script string
	switch {
	case len(fields) == 2 && fields[1] == "start":
		script = "start"
	case len(fields) == 3 && (fields[1] == "run" || fields[1] == "run-script"):
		script = fields[2]
	case len(fields) > 3 && (fields[1] == "run" || fields[1] == "run-script"):
		return nil, ReasonNPMArguments
	default:
		return nil, ReasonPackageManagerStart
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(packageJSON, &pkg); err != nil {
		return nil, ReasonPackageUnreadable
	}
	body := strings.TrimSpace(pkg.Scripts[script])
	if body == "" {
		return nil, ReasonMissingScript
	}
	if pkg.Scripts["pre"+script] != "" || pkg.Scripts["post"+script] != "" {
		return nil, ReasonNPMLifecycleHooks
	}
	return []string{"/bin/sh", "-c", body}, ""
}

// RunnerPath is the PATH a shared-runtime app runs with on the runner base.
const RunnerPath = runnerPath

// ResolveStart resolves a start command without npm; see ClassifyNode.
func ResolveStart(command string, packageJSON []byte) ([]string, string) {
	return resolveStart(command, packageJSON)
}

// DroppableOutsideApp reports whether an export entry outside /app is
// toolchain or cache content the runner base replaces. name is a clean
// relative tar path such as "mise/installs/node/24.1.0/bin/node".
func DroppableOutsideApp(name string) bool {
	clean := "/" + path.Clean(name)
	for _, root := range toolchainPaths {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return true
		}
	}
	return clean == "/root/.cache" || strings.HasPrefix(clean, "/root/.cache/")
}
