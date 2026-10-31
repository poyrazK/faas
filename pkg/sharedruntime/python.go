package sharedruntime

import (
	"encoding/json"
	"path"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Shared runner bases for Python source apps (ADR-1500 amendment 2).
const (
	RuntimePython312 = "python312"
	RuntimePython313 = "python313"
)

// pythonVenv is where Railpack's Python provider installs dependencies.
const pythonVenv = "/app/.venv"

// PythonRunnerPath is the PATH a shared-runtime Python app runs with: the
// virtualenv's scripts first, then the runner base.
const PythonRunnerPath = pythonVenv + "/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// pythonInterpreters are the runner bases' interpreters: runner-python312 is
// Debian python:3.12-slim, runner-python313 is Wolfi.
var pythonInterpreters = map[string]string{
	RuntimePython312: "/usr/local/bin/python3.12",
	RuntimePython313: "/usr/bin/python3.13",
}

// pythonMinors maps the minor versions the shared bases carry.
var pythonMinors = map[int]string{12: RuntimePython312, 13: RuntimePython313}

// pythonDefault is the base for a source that declares no Python version.
// Railpack 0.38 installs 3.13 then, so the build keeps the Python it would
// have used on its own base.
const pythonDefault = RuntimePython313

// pythonPackageManagers are start commands that need a tool the runner base
// does not ship: Railpack installs them with mise, which the /app-only layer
// drops. ResolvePythonStart drops the `run` of those in pythonRunTools.
var pythonPackageManagers = map[string]bool{
	"uv": true, "uvx": true, "poetry": true, "pdm": true, "pipenv": true, "pip": true,
	"pipx": true, "hatch": true, "rye": true, "conda": true, "mise": true,
}

// PythonRuntime reports whether runtime is a shared Python runner base.
func PythonRuntime(runtime string) bool { return pythonInterpreters[runtime] != "" }

// PythonInterpreter is the interpreter of a shared Python runner base, or ""
// for any other runtime.
func PythonInterpreter(runtime string) string { return pythonInterpreters[runtime] }

// PythonRuntimeForSpecs picks the shared runner base for a Python source's
// version declarations (.python-version, runtime.txt, Pipfile, mise files,
// railpack.json; see markers.PythonVersionSpecsAtRoot). No declaration means
// python313. Every declaration must name the same supported minor ("3.12",
// "3.12.4", "3.12.x", "python-3.12.4"); anything else returns "" so the build
// keeps its own toolchain, because the builder pins Railpack's Python to the
// base's minor and a declared version must never be replaced.
func PythonRuntimeForSpecs(specs []string) string {
	runtime := ""
	for _, spec := range specs {
		r := pythonRuntimeForSpec(spec)
		if r == "" || runtime != "" && r != runtime {
			return ""
		}
		runtime = r
	}
	if runtime == "" {
		return pythonDefault
	}
	return runtime
}

func pythonRuntimeForSpec(spec string) string {
	spec = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(spec)), "python-")
	major, rest, ok := strings.Cut(spec, ".")
	if !ok || major != "3" {
		return ""
	}
	digits, tail := rest, ""
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		digits, tail = rest[:i], rest[i:]
	}
	minor, err := strconv.Atoi(digits)
	if err != nil || !versionTail(tail) {
		return ""
	}
	return pythonMinors[minor]
}

// ClassifyPython reads a Railpack Python plan. The start command runs with
// the virtualenv's scripts on PATH, without a package manager's `run` in
// front of it (see ResolvePythonStart; the plan alone cannot check its
// target, which imaged does).
func ClassifyPython(planJSON []byte) api.SharedRuntimeVerdict {
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
	start, reason := ResolvePythonStart(p.Deploy.StartCommand, nil)
	if reason != "" {
		return ineligible(reason)
	}
	env := make(map[string]string, len(p.Deploy.Variables)+2)
	for k, v := range p.Deploy.Variables {
		env[k] = v
	}
	env["PATH"] = PythonRunnerPath
	env["VIRTUAL_ENV"] = pythonVenv
	return api.SharedRuntimeVerdict{Eligible: true, Start: start, Env: env}
}

// PythonPackageManager reports whether a start command's program is a Python
// package manager the runner base does not ship.
func PythonPackageManager(program string) bool { return pythonPackageManagers[path.Base(program)] }

// RelinkVenvLink returns the target a symlink in the app's virtualenv keeps
// on the runner base. Railpack creates the virtualenv with mise's Python, so
// its interpreter links (python, python3, python3.N) point into the toolchain
// the /app-only layer drops; they move to the base's interpreter of the same
// minor. name is a clean relative tar path. ok is false for any other link
// into the toolchain, which would dangle.
func RelinkVenvLink(name, target, interpreter string) (string, bool) {
	if !droppableToolchain(target) {
		return target, true
	}
	dir, base := path.Split(name)
	if path.Clean(dir) != strings.TrimPrefix(pythonVenv, "/")+"/bin" || !venvInterpreterName(base) {
		return "", false
	}
	return interpreter, true
}

func venvInterpreterName(base string) bool {
	switch {
	case base == "python", base == "python3":
		return true
	case strings.HasPrefix(base, "python3."):
		_, err := strconv.Atoi(strings.TrimPrefix(base, "python3."))
		return err == nil
	}
	return false
}

// PyvenvConfigPath is the virtualenv's pyvenv.cfg as a relative tar path.
const PyvenvConfigPath = "app/.venv/pyvenv.cfg"

// RewritePyvenvConfig points a virtualenv's pyvenv.cfg at the runner base's
// interpreter: Python finds its standard library through `home`. Keys naming
// the toolchain interpreter or its prefix (virtualenv's, which Pipenv and PDM
// use) move with it. ok is false when `home` is missing or names something
// other than the toolchain.
func RewritePyvenvConfig(body []byte, interpreter string) ([]byte, bool) {
	lines := strings.Split(string(body), "\n")
	home := false
	for i, line := range lines {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "home":
			if !droppableToolchain(value) {
				return nil, false
			}
			lines[i], home = "home = "+path.Dir(interpreter), true
		case "executable", "base-executable":
			if droppableToolchain(value) {
				lines[i] = key + " = " + interpreter
			}
		case "base-prefix", "base-exec-prefix":
			if droppableToolchain(value) {
				lines[i] = key + " = " + path.Dir(path.Dir(interpreter))
			}
		}
	}
	if !home {
		return nil, false
	}
	return []byte(strings.Join(lines, "\n")), true
}

// droppableToolchain reports whether an absolute path lies in the mise
// toolchain the runner base replaces.
func droppableToolchain(abs string) bool { return path.IsAbs(abs) && within(abs, toolchainPaths) }
