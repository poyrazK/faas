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
	// ReasonScriptPackageManager: the start script, or a script it reaches,
	// calls a package manager or a script runner the runner base does not
	// ship (npm, npx, yarn, pnpm, corepack, mise, npm-run-all and the
	// like), so it would exit 127 there.
	ReasonScriptPackageManager = "script_package_manager"
	// ReasonYarnPnP: the app installed its dependencies with Yarn Plug'n'Play
	// (/app/.pnp.cjs, no node_modules). Node resolves them only through
	// yarn's loader, which yarn adds when it runs a script.
	ReasonYarnPnP = "yarn_pnp"
)

// YarnPnPLoader reports whether name, a clean relative tar path, is Yarn
// Plug'n'Play's loader at the root of /app.
func YarnPnPLoader(name string) bool {
	return name == "app/.pnp.cjs" || name == "app/.pnp.js"
}

// toolchainStep is the Railpack step that installs the language toolchain
// with mise. The runner base supplies that toolchain, so its deploy paths are
// dropped rather than copied.
const toolchainStep = "packages:mise"

// runnerPath is the runner base's PATH with the app's local binaries first,
// which is where npm puts them when it runs a script.
const runnerPath = "/app/node_modules/.bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// toolchainPaths hold the toolchain the runner base replaces. /opt/corepack
// is Railpack's COREPACK_HOME: with a packageManager field, Railpack installs
// yarn through corepack there (ADR-20261125170000).
var toolchainPaths = []string{"/mise", "/usr/local/bin/mise", "/etc/mise", "/root/.local/state/mise", corepackHome}

const corepackHome = "/opt/corepack"

var appPaths = []string{"/app", "/root/.cache"}

// corepackBuildPaths are what Railpack adds to the build step's deploy input
// when it installs the package manager with corepack: corepack's home and
// the mise Node tree `corepack enable` changed. All are toolchain, which the
// layer drops.
var corepackBuildPaths = []string{corepackHome, "/mise/installs", "/mise/shims"}

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
	command := p.Deploy.StartCommand
	bun := UsesBun(command)
	start, reason := resolveStart(command, packageJSON)
	switch {
	case bun && BunStartNeedsNodePackageManager(command, packageJSON):
		return ineligible(ReasonPackageManagerStart)
	case bun:
		// ADR-20261124160000: the layer keeps Bun's own install, if the
		// artifact has one (imaged decides).
		start = ShellStart(command)
	case reason != "":
		return ineligible(reason)
	case StartReachesBun(command, packageJSON):
		// A package manager script runs Bun; the runner base has neither.
		return ineligible(ReasonBunInScript)
	}
	env := make(map[string]string, len(p.Deploy.Variables)+1)
	for k, v := range p.Deploy.Variables {
		env[k] = v
	}
	env["PATH"] = runnerPath
	if bun {
		// Bun's bin directory is known only from the artifact; imaged sets
		// PATH with BunRunnerPath.
		delete(env, "PATH")
	}
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
		allowed := append(append([]string(nil), appPaths...), corepackBuildPaths...)
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
// package manager. `npm start`, `npm run <script>`, and the same through pnpm
// and yarn (ADR-20261125170000), run the script body through sh, as the
// package manager does; lifecycle hooks, forwarded arguments and scripts that
// call a package manager keep the build on the full-layer path rather than
// approximating it.
func resolveStart(command string, packageJSON []byte) ([]string, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, ReasonNoStartCommand
	}
	fields := strings.Fields(command)
	switch fields[0] {
	case "npm", "pnpm", "yarn":
	case "npx", "bun", "bunx", "corepack":
		return nil, ReasonPackageManagerStart
	default:
		return ShellStart(command), ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(packageJSON, &pkg); err != nil {
		return nil, ReasonPackageUnreadable
	}
	script, reason := startScript(fields, pkg.Scripts)
	if reason != "" {
		return nil, reason
	}
	body := strings.TrimSpace(pkg.Scripts[script])
	if body == "" {
		return nil, ReasonMissingScript
	}
	if pkg.Scripts["pre"+script] != "" || pkg.Scripts["post"+script] != "" {
		return nil, ReasonNPMLifecycleHooks
	}
	if walkStartScripts(body, packageJSON, func(name string) bool {
		return name != "bun" && name != "bunx" && (nodePackageManagers[name] || nodeScriptRunners[name])
	}) {
		return nil, ReasonScriptPackageManager
	}
	return ShellStart(body), ""
}

// startScript names the package.json script a package manager start runs:
// `<pm> start`, `<pm> run <name>` (npm also `run-script`), and pnpm's and
// yarn's `<pm> <name>` for a script that is not one of their own commands.
// More words are arguments the script would get.
func startScript(fields []string, scripts map[string]string) (string, string) {
	manager := fields[0]
	run := len(fields) >= 3 && (fields[1] == "run" || manager == "npm" && fields[1] == "run-script")
	switch {
	case len(fields) == 2 && fields[1] == "start":
		return "start", ""
	case run && len(fields) == 3:
		return fields[2], ""
	case run:
		return "", ReasonNPMArguments
	case manager != "npm" && len(fields) >= 2 && !strings.HasPrefix(fields[1], "-") && !shorthandCommands[fields[1]]:
		if _, ok := scripts[fields[1]]; !ok {
			return "", ReasonPackageManagerStart
		}
		if len(fields) > 2 {
			return "", ReasonNPMArguments
		}
		return fields[1], ""
	}
	return "", ReasonPackageManagerStart
}

// shorthandCommands are pnpm and yarn commands that `<pm> <name>` runs
// instead of a script of that name.
var shorthandCommands = map[string]bool{
	"add": true, "audit": true, "bin": true, "cache": true, "config": true, "create": true, "dedupe": true,
	"deploy": true, "dlx": true, "env": true, "exec": true, "fetch": true, "import": true, "info": true,
	"init": true, "install": true, "link": true, "list": true, "ls": true, "node": true, "outdated": true,
	"pack": true, "patch": true, "plugin": true, "prune": true, "publish": true, "rebuild": true,
	"remove": true, "root": true, "run": true, "server": true, "set": true, "setup": true, "store": true,
	"unlink": true, "unplug": true, "up": true, "update": true, "upgrade": true, "version": true,
	"why": true, "workspace": true, "workspaces": true,
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
