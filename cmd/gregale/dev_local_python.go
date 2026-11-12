package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Prefer a function's own environment, then the workspace's environment. Keep
// the venv executable's spelling: resolving its symlink loses pyvenv.cfg and
// the project's site-packages. Never search beyond the selected workspace.
func localPythonEnvironment(root, projectRoot string, env []string) (string, []string, error) {
	bin, python := "bin", "python3"
	if runtime.GOOS == "windows" {
		bin, python = "Scripts", "python.exe"
	}
	roots := []string{root}
	if projectRoot != "" && projectRoot != root {
		roots = append(roots, projectRoot)
	}
	for _, dir := range roots {
		for _, name := range []string{".venv", "venv"} {
			venv := filepath.Join(dir, name)
			candidate := filepath.Join(venv, bin, python)
			if _, err := os.Lstat(candidate); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return "", nil, fmt.Errorf("inspect local Python environment: %w", err)
			}
			exe, err := exec.LookPath(candidate)
			if err != nil {
				return "", nil, fmt.Errorf("local Python environment has an unusable interpreter; repair or recreate %s: %w", venv, err)
			}
			return exe, localPythonVenvEnv(env, venv, filepath.Join(venv, bin)), nil
		}
	}
	exe, err := localPythonOnPath(env)
	return exe, env, err
}

func localPythonEnvKey(key, want string) bool {
	return key == want || (runtime.GOOS == "windows" && strings.EqualFold(key, want))
}

func localPythonEnvPath(env []string) string {
	for i := len(env) - 1; i >= 0; i-- {
		key, value, _ := strings.Cut(env[i], "=")
		if localPythonEnvKey(key, "PATH") {
			return value
		}
	}
	return ""
}

func localPythonVenvEnv(env []string, venv, bin string) []string {
	path := localPythonEnvPath(env)
	if path != "" {
		bin += string(os.PathListSeparator) + path
	}
	result := make([]string, 0, len(env)+2)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if !localPythonEnvKey(key, "PATH") && !localPythonEnvKey(key, "VIRTUAL_ENV") && !localPythonEnvKey(key, "PYTHONHOME") {
			result = append(result, kv)
		}
	}
	// Match virtualenv activation for subprocesses as well as imports.
	return append(result, "PATH="+bin, "VIRTUAL_ENV="+venv)
}

// exec.LookPath uses the CLI's environment. Resolve each candidate explicitly
// instead, honoring the same final PATH the function's child process receives.
func localPythonOnPath(env []string) (string, error) {
	for _, dir := range filepath.SplitList(localPythonEnvPath(env)) {
		candidate := filepath.Join(dir, "python3")
		if !filepath.IsAbs(candidate) {
			candidate = "." + string(os.PathSeparator) + candidate
		}
		exe, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(exe) {
			return "", fmt.Errorf("local Python PATH resolved relative to the current directory: %w", exec.ErrDot)
		}
		return exe, nil
	}
	return "", fmt.Errorf("python3 is not available on the function's PATH; install Python or create a project virtual environment: %w", exec.ErrNotFound)
}
