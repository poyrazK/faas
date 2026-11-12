package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

// Validate execution prerequisites before Docker resets, credential minting or
// task-VM tunnels. Do not run customer scripts, build, install or contact a
// package registry. Remote resource bindings are not needed to find tools.
func devLocalPreflight(root string, fns []*localFunction, app *devLocalAppPlan, env []string) error {
	var issues []error
	for _, fn := range fns {
		childEnv := fn.environment(env)
		var err error
		if fn.Family == "python" {
			_, _, err = localPythonEnvironment(fn.Root, fn.ProjectRoot, childEnv)
		} else {
			_, err = devLocalToolOnPath(fn.Family, childEnv)
		}
		if err != nil {
			issues = append(issues, fmt.Errorf("function %s: %w", fn.Name, err))
		}
	}
	if app != nil {
		if err := devLocalAppPreflight(root, *app, env); err != nil {
			issues = append(issues, err)
		}
	}
	return errors.Join(issues...)
}

// Resolve against the child's final PATH, including env-file/function
// overrides. exec.LookPath(name) would use the CLI's PATH instead.
func devLocalToolOnPath(name string, env []string) (string, error) {
	for _, dir := range filepath.SplitList(localPythonEnvPath(env)) {
		candidate := filepath.Join(dir, name)
		if !filepath.IsAbs(candidate) {
			candidate = "." + string(os.PathSeparator) + candidate
		}
		exe, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(exe) {
			return "", fmt.Errorf("%s PATH resolved relative to the current directory: %w", name, exec.ErrDot)
		}
		return exe, nil
	}
	return "", fmt.Errorf("%s is not available on the local process PATH; install %s or update the selected PATH: %w", name, name, exec.ErrNotFound)
}

func devLocalAppPreflight(root string, app devLocalAppPlan, env []string) error {
	// The supervisor launches a shell from the CLI's PATH. Arbitrary shell
	// commands can activate environments or define tools themselves; do not
	// attempt to interpret them as a detected framework command.
	if _, err := exec.LookPath("sh"); err != nil {
		return fmt.Errorf("local HTTP apps require sh on the CLI PATH: %w", err)
	}
	if app.framework == "custom command" {
		return nil
	}
	profile, err := frameworkprofile.AnalyzeDir(root)
	if err != nil {
		return err
	}
	env = devLocalAppEnv(root, env, "0")
	paths := filepath.SplitList(localPythonEnvPath(env))
	for i, path := range paths {
		if !filepath.IsAbs(path) {
			paths[i] = filepath.Join(root, path)
		}
	}
	env = append(env, "PATH="+strings.Join(paths, string(os.PathListSeparator)))
	tool := strings.Fields(app.command)[0]
	if profile.StartFromConfig && app.command == profile.StartCommand {
		return nil
	}
	if profile.Family == "node" {
		if tool != "bun" {
			if _, err := devLocalToolOnPath("node", env); err != nil {
				return err
			}
		}
		return devLocalNodePreflight(root, app.command, profile.PackageManager, env)
	}
	if _, err := devLocalToolOnPath(tool, env); err != nil {
		if profile.Family == "python" {
			return fmt.Errorf("%w; %s", err, devLocalPythonRepair(root))
		}
		return err
	}
	// These are modules in the selected interpreter, not standalone launchers.
	var modules []string
	switch app.framework {
	case "FastAPI":
		modules = []string{"uvicorn", "fastapi"}
	case "Flask":
		modules = []string{"flask"}
	case "Django":
		modules = []string{"django"}
	}
	if len(modules) == 0 {
		return nil
	}
	python, err := devLocalToolOnPath("python3", env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, module := range modules {
		cmd := exec.CommandContext(ctx, python, "-B", "-c", "import importlib.util, sys; sys.exit(0 if importlib.util.find_spec(sys.argv[1]) is not None else 1)", module)
		cmd.Dir, cmd.Env = root, env
		// Never echo probe output: site customization can print env values.
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not verify Python module %s in the selected environment; %s: %w", module, devLocalPythonRepair(root), err)
		}
	}
	return nil
}

func devLocalNodePreflight(root, command, manager string, env []string) error {
	tool := strings.Fields(command)[0]
	if tool == manager {
		if _, err := devLocalToolOnPath(manager, env); err != nil {
			return fmt.Errorf("%w; install the project's %s package manager, then run %s in the project directory", err, manager, devLocalNodeRepair(root, manager))
		}
		// Yarn PnP resolves package binaries itself; node_modules/.bin is not
		// required. Never execute a package manager merely to probe a script.
		if manager == "yarn" && (fileExists(filepath.Join(root, ".pnp.cjs")) || fileExists(filepath.Join(root, ".pnp.js"))) {
			return nil
		}
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		body, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &pkg); err != nil {
			return fmt.Errorf("read package scripts: %w", err)
		}
		fields := strings.Fields(command)
		if len(fields) < 3 || fields[1] != "run" {
			return nil
		}
		script := pkg.Scripts[fields[2]]
		// Check plain framework scripts only. Wrappers, assignments and shell
		// operators may supply their own PATH; leave them to the supervisor.
		if strings.TrimSpace(pkg.Scripts["pre"+fields[2]]) != "" || strings.ContainsAny(script, ";&|<>$`'\"()\\\n\r") {
			return nil
		}
		fields = strings.Fields(script)
		if len(fields) == 0 {
			return nil
		}
		tool = fields[0]
		// Package managers expose ancestor workspace binaries as well.
		for dir := filepath.Dir(root); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			env = append(env, "PATH="+localPythonEnvPath(env)+string(os.PathListSeparator)+filepath.Join(dir, "node_modules", ".bin"))
		}
	}
	switch tool {
	case "node":
		_, err := devLocalToolOnPath(tool, env)
		return err
	case "next", "vite", "astro", "nuxt", "react-router", "remix", "tsx", "nodemon", "webpack":
		if _, err := devLocalToolOnPath(tool, env); err != nil {
			return fmt.Errorf("%w; run %s in the project directory", err, devLocalNodeRepair(root, manager))
		}
	}
	return nil
}

func devLocalNodeRepair(root, manager string) string {
	switch manager {
	case "pnpm":
		if fileExists(filepath.Join(root, "pnpm-lock.yaml")) {
			return "pnpm install --frozen-lockfile"
		}
		return "pnpm install"
	case "bun":
		if fileExists(filepath.Join(root, "bun.lock")) || fileExists(filepath.Join(root, "bun.lockb")) {
			return "bun install --frozen-lockfile"
		}
		return "bun install"
	case "yarn":
		if fileExists(filepath.Join(root, ".yarnrc.yml")) || fileExists(filepath.Join(root, ".pnp.cjs")) {
			return "yarn install --immutable"
		}
		return "yarn install --frozen-lockfile"
	default:
		if fileExists(filepath.Join(root, "package-lock.json")) || fileExists(filepath.Join(root, "npm-shrinkwrap.json")) {
			return "npm ci"
		}
		return "npm install"
	}
}

func devLocalPythonRepair(root string) string {
	switch {
	case fileExists(filepath.Join(root, "uv.lock")):
		return "run uv sync in the project directory"
	case fileExists(filepath.Join(root, "poetry.lock")):
		return "run poetry install in the project directory"
	case fileExists(filepath.Join(root, "Pipfile.lock")):
		return "run pipenv sync --dev in the project directory"
	case fileExists(filepath.Join(root, "requirements.txt")):
		return "activate the project virtualenv and run python3 -m pip install -r requirements.txt"
	default:
		return "activate the project virtualenv and run python3 -m pip install -e ."
	}
}
