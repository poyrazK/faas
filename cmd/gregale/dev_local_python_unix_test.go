//go:build unix

package main_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func localPythonTestEnv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if name, value, _ := strings.Cut(env[i], "="); name == key {
			return value
		}
	}
	return ""
}

func writeLocalPythonExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestDevLocalPythonEnvironmentResolution(t *testing.T) {
	project := t.TempDir()
	function := filepath.Join(project, "functions", "web")
	functionEnv := filepath.Join(function, "venv", "bin", "python3")
	projectEnv := filepath.Join(project, ".venv", "bin", "python3")
	writeLocalPythonExecutable(t, functionEnv)
	writeLocalPythonExecutable(t, projectEnv)
	env := []string{"PATH=/original", "KEEP=present", "PATH=/effective", "VIRTUAL_ENV=/old", "PYTHONHOME=/old"}
	for _, tc := range []struct{ name, root, want string }{
		{"function before workspace", function, functionEnv},
		{"workspace fallback", filepath.Join(project, "functions", "other"), projectEnv},
		{"single function", project, projectEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe, childEnv, err := main.LocalPythonEnvironmentForTest(tc.root, project, env)
			venv := filepath.Dir(filepath.Dir(tc.want))
			if err != nil || exe != tc.want || localPythonTestEnv(childEnv, "VIRTUAL_ENV") != venv || localPythonTestEnv(childEnv, "PATH") != filepath.Dir(exe)+":/effective" {
				t.Fatalf("resolved = %q %v %v", exe, childEnv, err)
			}
			if localPythonTestEnv(childEnv, "PYTHONHOME") != "" || localPythonTestEnv(childEnv, "KEEP") != "present" || env[0] != "PATH=/original" {
				t.Fatal("virtualenv activation lost settings or mutated the caller's environment")
			}
		})
	}
	// An unrelated ancestor's environment must not override the selected project.
	outside := filepath.Join(t.TempDir(), ".venv", "bin", "python3")
	writeLocalPythonExecutable(t, outside)
	child := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(outside))), "project")
	if _, _, err := main.LocalPythonEnvironmentForTest(child, child, []string{"PATH="}); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("searched beyond the workspace: %v", err)
	}
}

func TestDevLocalPythonUsesEffectivePath(t *testing.T) {
	root := t.TempDir()
	parentBin, childBin := t.TempDir(), t.TempDir()
	writeLocalPythonExecutable(t, filepath.Join(parentBin, "python3"))
	writeLocalPythonExecutable(t, filepath.Join(childBin, "python3"))
	t.Setenv("PATH", parentBin)
	exe, _, err := main.LocalPythonEnvironmentForTest(root, root, []string{"PATH=" + parentBin, "PATH=" + childBin})
	if err != nil || exe != filepath.Join(childBin, "python3") {
		t.Fatalf("ignored the child's final PATH: %q %v", exe, err)
	}
	if _, _, err := main.LocalPythonEnvironmentForTest(root, root, []string{"PATH="}); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("empty child PATH fell back to the CLI PATH: %v", err)
	}
	t.Chdir(childBin)
	if _, _, err := main.LocalPythonEnvironmentForTest(root, root, []string{"PATH=.:" + parentBin}); !errors.Is(err, exec.ErrDot) {
		t.Fatalf("relative PATH execution lost the standard Go guard: %v", err)
	}
}

func TestDevLocalPythonRejectsBrokenProjectInterpreter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".venv", "bin", "python3")
	writeLocalPythonExecutable(t, path)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := main.LocalPythonEnvironmentForTest(root, root, os.Environ()); err == nil || !strings.Contains(err.Error(), "repair or recreate") {
		t.Fatalf("silently used another interpreter for a broken project environment: %v", err)
	}
}

const localPythonPrivateModule = "_gregale_project_venv_fixture_8bcb"

func createLocalPythonVenv(t *testing.T, dir, value string) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	cmd := exec.CommandContext(t.Context(), python, "-m", "venv", "--without-pip", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create real Python virtualenv: %v: %s", err, out)
	}
	interpreter := filepath.Join(dir, "bin", "python3")
	cmd = exec.CommandContext(t.Context(), interpreter, "-c", "import sysconfig; print(sysconfig.get_path('purelib'))")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(strings.TrimSpace(string(out)), localPythonPrivateModule+".py")
	if err := os.WriteFile(module, []byte("VALUE = "+"'"+value+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return interpreter
}

const localPythonVenvHandler = `import os, sys, subprocess
from ` + localPythonPrivateModule + ` import VALUE
def handler(event, ctx):
    child = subprocess.check_output(['python3', '-c', 'from ` + localPythonPrivateModule + ` import VALUE; print(VALUE)'], text=True).strip()
    return {'status': 200, 'body': {'value': VALUE, 'child': child, 'prefix': sys.prefix, 'executable': sys.executable, 'virtualenv': os.getenv('VIRTUAL_ENV'), 'pid': os.getpid()}}
`

func TestDevLocalPythonImportsProjectVenvPackages(t *testing.T) {
	for _, name := range []string{".venv", "venv"} {
		t.Run(name, func(t *testing.T) {
			root := writeDevLocalProject(t, map[string]string{"handler.py": localPythonVenvHandler})
			venv := filepath.Join(root, name)
			interpreter := createLocalPythonVenv(t, venv, "project-only")
			server, _, err := main.DevLocalServerForTest(t, root)
			if err != nil {
				t.Fatal(err)
			}
			status, body := devLocalGet(t, server, "localhost:8787", "/", nil)
			if status != 200 || body["value"] != "project-only" || body["child"] != "project-only" || body["prefix"] != venv || body["executable"] != interpreter || body["virtualenv"] != venv {
				t.Fatalf("project virtualenv was not used by the function and its subprocess: %d %v", status, body)
			}
		})
	}
}

func TestDevLocalPythonFunctionAndWorkspaceVenvs(t *testing.T) {
	root := writeDevLocalProject(t, map[string]string{
		"gregale.yaml":                "functions:\n  - name: own\n    root: functions/own\n    entry: true\n  - name: shared\n    root: functions/shared\n",
		"functions/own/handler.py":    localPythonVenvHandler,
		"functions/shared/handler.py": localPythonVenvHandler,
	})
	createLocalPythonVenv(t, filepath.Join(root, ".venv"), "workspace")
	createLocalPythonVenv(t, filepath.Join(root, "functions", "own", "venv"), "function")
	server, _, err := main.DevLocalServerForTest(t, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want string }{{"own", "function"}, {"shared", "workspace"}} {
		status, body := devLocalGet(t, server, tc.name+".localhost:8787", "/", nil)
		if status != 200 || body["value"] != tc.want || body["child"] != tc.want {
			t.Fatalf("%s selected the wrong environment: %d %v", tc.name, status, body)
		}
	}
}

func TestDevLocalPythonFunctionPathOverride(t *testing.T) {
	interpreter := createLocalPythonVenv(t, filepath.Join(t.TempDir(), "function environment"), "function-path")
	root := writeDevLocalProject(t, map[string]string{
		"gregale.yaml":             "functions:\n  - name: web\n    root: functions/web\n    entry: true\n    env:\n      PATH: " + strconv.Quote(filepath.Dir(interpreter)) + "\n",
		"functions/web/handler.py": localPythonVenvHandler,
	})
	server, _, err := main.DevLocalServerForTest(t, root)
	if err != nil {
		t.Fatal(err)
	}
	status, body := devLocalGet(t, server, "localhost:8787", "/", nil)
	if status != 200 || body["value"] != "function-path" || body["child"] != "function-path" || body["executable"] != interpreter {
		t.Fatalf("function env PATH did not select its runtime: %d %v", status, body)
	}
}

func TestDevLocalPythonPathChangesReloadWorkers(t *testing.T) {
	root := writeDevLocalProject(t, map[string]string{
		"gregale.yaml":             "functions:\n  - name: web\n    root: functions/web\n    entry: true\n",
		"functions/web/handler.py": localPythonVenvHandler,
	})
	first := createLocalPythonVenv(t, filepath.Join(t.TempDir(), "first environment"), "first-fixture")
	second := createLocalPythonVenv(t, filepath.Join(t.TempDir(), "second environment"), "second-fixture")
	envFile := filepath.Join(t.TempDir(), "selected.env")
	writeLocalEnv(t, envFile, "PATH="+filepath.Dir(first)+"\n")
	address, _, out, _ := startLocalAppCLI(t, root, "--env-file", envFile)
	initial := waitLocalAppBody(t, address, "first-fixture", out)
	writeLocalEnv(t, envFile, "PATH="+filepath.Dir(second)+"\n")
	next := waitLocalAppBody(t, address, "second-fixture", out)
	if next["pid"] == initial["pid"] || next["executable"] != second || next["child"] != "second-fixture" {
		t.Fatalf("PATH edit did not replace the Python runtime: %v -> %v", initial, next)
	}
	waitLocalEnvOutput(t, out, "reloaded web")
	next = waitLocalAppBody(t, address, "second-fixture", out)
	writeLocalEnv(t, envFile, "PATH=/missing-python-fixture\n")
	waitLocalEnvOutput(t, out, "python3 is not available on the function's PATH")
	if current := waitLocalAppBody(t, address, "second-fixture", out); current["pid"] != next["pid"] {
		t.Fatal("invalid runtime selection stopped a healthy worker")
	}
	writeLocalEnv(t, envFile, "PATH="+filepath.Dir(first)+"\n")
	if recovered := waitLocalAppBody(t, address, "first-fixture", out); recovered["executable"] != first {
		t.Fatal("repairing PATH did not recover the selected runtime")
	}
}
