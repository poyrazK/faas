//go:build unix

package main_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261112120356
// Exercise the CLI entrypoint with real executable/API spies, including the
// destructive --reset path. A failed preflight must not even inspect Docker
// or look up a remote session, much less start tunnels or mint credentials.
func TestDevLocalPreflightBeforeResources(t *testing.T) {
	for _, workload := range []struct {
		name, file, body, missing, tools string
	}{
		{"node function", "handler.js", "export default () => new Response('ok')", "node", ""},
		{"python function", "handler.py", "def handler(event, ctx): return {'status': 200}", "python3", ""},
		{"go function", "handler.go", "package main\nfunc main() {}\n", "go", ""},
		{"node app", "package.json", `{"scripts":{"dev":"node server.cjs"}}`, "node", ""},
		{"package manager", "package.json", `{"scripts":{"dev":"vite"}}`, "npm", "node"},
		{"framework tool", "package.json", `{"scripts":{"dev":"vite"}}`, "vite", "node npm"},
	} {
		for _, mode := range []string{"offline reset", "remote"} {
			t.Run(workload.name+"/"+mode, func(t *testing.T) {
				dir := writeDevLocalProject(t, map[string]string{workload.file: workload.body})
				bin := t.TempDir()
				for _, tool := range strings.Fields(workload.tools) {
					writePreflightTool(t, bin, tool, "#!/bin/sh\nexit 97\n")
				}
				log := filepath.Join(t.TempDir(), "docker-calls")
				writePreflightTool(t, bin, "docker", `#!/bin/sh
printf '%s\n' "$*" >> "$GREGALE_PREFLIGHT_DOCKER_LOG"
case "$1" in run) exit 1;; esac
`)
				t.Setenv("GREGALE_PREFLIGHT_DOCKER_LOG", log)
				t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
				// CLI tools remain available, but the selected child PATH does
				// not contain this workload's runtime.
				writeLocalEnv(t, filepath.Join(dir, "app.env"), "PATH="+bin+"\nSECRET=never-print-this-value\n")
				var requests atomic.Int32
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					http.NotFound(w, r)
				}))
				defer api.Close()
				t.Setenv("FAAS_API", api.URL)
				t.Setenv("FAAS_TOKEN", main.TestAPIKey('p'))
				t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("ab", 16))
				args := []string{"dev", "--local", "--path", dir, "--env-file", filepath.Join(dir, "app.env")}
				if mode == "offline reset" {
					args = append(args, "--offline", "--postgres", "--bucket", "uploads", "--reset")
				} else {
					args = append(args, "--name", "local-preflight")
				}
				out := runLocalPreflightCLI(t, args...)
				if !strings.Contains(out, "Local preflight failed") || !strings.Contains(out, workload.missing) {
					t.Fatalf("missing-runtime failure = %s", out)
				}
				if strings.Contains(out, "never-print-this-value") {
					t.Fatal("preflight exposed an env value")
				}
				if calls, err := os.ReadFile(log); !os.IsNotExist(err) {
					t.Fatalf("preflight touched Docker, including reset: %q, %v", calls, err)
				}
				if requests.Load() != 0 {
					t.Fatalf("preflight made %d remote requests", requests.Load())
				}
			})
		}
	}
}

func writePreflightTool(t *testing.T, bin, name, body string) {
	t.Helper()
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func runLocalPreflightCLI(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestDevLocalAppCLIHelper$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "GREGALE_LOCAL_APP_CLI_HELPER=1", "XDG_CONFIG_HOME="+t.TempDir(), "XDG_CACHE_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil {
		t.Fatalf("CLI should fail promptly: %v, %s", err, out)
	}
	return string(out)
}

func TestDevLocalPreflightPackageTools(t *testing.T) {
	for _, tc := range []struct {
		name, manager, lock, want string
	}{
		{"npm lock", "npm", "package-lock.json", "npm ci"},
		{"npm no lock", "npm", "", "npm install"},
		{"pnpm", "pnpm", "pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
		{"yarn classic", "yarn", "yarn.lock", "yarn install --frozen-lockfile"},
		{"yarn modern", "yarn", ".yarnrc.yml", "yarn install --immutable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"package.json": fmt.Sprintf(`{"packageManager":%q,"scripts":{"dev":"vite --host"},"devDependencies":{"vite":"*"}}`, tc.manager+"@9.0.0")}
			if tc.lock != "" {
				files[tc.lock] = ""
			}
			dir, bin := writeDevLocalProject(t, files), t.TempDir()
			writePreflightTool(t, bin, "node", "#!/bin/sh\nexit 97\n")
			env := []string{"PATH=" + bin}
			// Check the manager before its script. No probe may execute it.
			err := main.DevLocalPreflightForTest(dir, "", env)
			if err == nil || !strings.Contains(err.Error(), "install the project's "+tc.manager) {
				t.Fatalf("missing package manager: %v", err)
			}
			writePreflightTool(t, bin, tc.manager, "#!/bin/sh\nexit 97\n")
			err = main.DevLocalPreflightForTest(dir, "", env)
			if err == nil || !strings.Contains(err.Error(), "vite") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("missing framework repair: %v", err)
			}
			writePreflightTool(t, filepath.Join(dir, "node_modules", ".bin"), "vite", "#!/bin/sh\nexit 97\n")
			if err := main.DevLocalPreflightForTest(dir, "", env); err != nil {
				t.Fatalf("installed framework rejected: %v", err)
			}
		})
	}
}

func TestDevLocalPreflightPnPAndOpaqueCommands(t *testing.T) {
	bin := t.TempDir()
	for _, tool := range []string{"node", "yarn", "npm", "bun"} {
		writePreflightTool(t, bin, tool, "#!/bin/sh\nexit 97\n")
	}
	for _, tc := range []struct {
		name, pkg, extraFile, command string
	}{
		{"yarn pnp", `{"packageManager":"yarn@4.6.0","scripts":{"dev":"vite"}}`, ".pnp.cjs", ""},
		{"legacy pnp", `{"packageManager":"yarn@1.22.0","scripts":{"dev":"vite"}}`, ".pnp.js", ""},
		{"wrapper", `{"scripts":{"dev":"cross-env CUSTOM=1 vite"}}`, "", ""},
		{"shell", `{"scripts":{"dev":"prepare-tools && vite"}}`, "", ""},
		{"predev", `{"scripts":{"predev":"prepare-tools","dev":"vite"}}`, "", ""},
		{"custom", `{}`, "", "activate-custom-env && launch-server"},
		{"bun", `{"packageManager":"bun@1.3.0","scripts":{"dev":"bun server.ts"}}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"package.json": tc.pkg}
			if tc.extraFile != "" {
				files[tc.extraFile] = ""
			}
			dir := writeDevLocalProject(t, files)
			if err := main.DevLocalPreflightForTest(dir, tc.command, []string{"PATH=" + bin}); err != nil {
				t.Fatalf("valid conservative case rejected: %v", err)
			}
		})
	}
}

func TestDevLocalPreflightAllFunctionEnvironments(t *testing.T) {
	bin := t.TempDir()
	writePreflightTool(t, bin, "node", "#!/bin/sh\nexit 97\n")
	dir := writeDevLocalProject(t, map[string]string{
		"gregale.yaml":      "functions:\n  - name: first\n    root: first\n  - name: second\n    root: second\n    env:\n      PATH: /missing-local-tool\n",
		"first/handler.js":  "export default () => new Response('ok')",
		"second/handler.js": "export default () => new Response('ok')",
	})
	err := main.DevLocalPreflightForTest(dir, "", []string{"PATH=" + bin})
	if err == nil || !strings.Contains(err.Error(), "function second") || strings.Contains(err.Error(), "function first") {
		t.Fatalf("function PATH overrides not respected: %v", err)
	}
	py := writeDevLocalProject(t, map[string]string{"handler.py": "def handler(event, ctx): return {'status': 200}"})
	writePreflightTool(t, filepath.Join(py, ".venv", "bin"), "python3", "#!/bin/sh\nexit 97\n")
	if err := main.DevLocalPreflightForTest(py, "", []string{"PATH="}); err != nil {
		t.Fatalf("project virtualenv rejected: %v", err)
	}
}

func TestDevLocalPreflightSelectedFunctionTools(t *testing.T) {
	for _, tool := range []string{"node", "go"} {
		t.Run(tool, func(t *testing.T) {
			realTool, err := exec.LookPath(tool)
			if err != nil {
				t.Skipf("%s is not installed", tool)
			}
			files := map[string]string{"handler.js": `exports.handler = async () => ({status:200,body:{path:"selected-tool"}});`}
			if tool == "go" {
				source, err := os.ReadFile(filepath.Join("templates", "function-go", "handler.go"))
				if err != nil {
					t.Fatal(err)
				}
				files = map[string]string{"handler.go": strings.Replace(string(source), "Path: env.Path", `Path: "selected-tool"`, 1)}
			}
			root, bin := writeDevLocalProject(t, files), t.TempDir()
			log := filepath.Join(t.TempDir(), "selected-tool")
			t.Setenv("GREGALE_TEST_RUNTIME", realTool)
			t.Setenv("GREGALE_TEST_RUNTIME_LOG", log)
			writePreflightTool(t, bin, tool, `#!/bin/sh
printf '%s\n' "$LOCAL_PREFLIGHT_VALUE" >> "$GREGALE_TEST_RUNTIME_LOG"
exec "$GREGALE_TEST_RUNTIME" "$@"
`)
			path := filepath.Join(t.TempDir(), "selected.env")
			writeLocalEnv(t, path, "PATH="+bin+":"+os.Getenv("PATH")+"\nLOCAL_PREFLIGHT_VALUE=selected-tool\n")
			address, _, out, _ := startLocalAppCLI(t, root, "--env-file", path)
			waitLocalAppBody(t, address, "selected-tool", out)
			calls, err := os.ReadFile(log)
			if err != nil || !strings.Contains(string(calls), "selected-tool") {
				t.Fatalf("startup ignored the selected runtime or its environment: %q, %v", calls, err)
			}
		})
	}
}

func TestDevLocalPreflightPythonAppModules(t *testing.T) {
	dir := writeDevLocalProject(t, map[string]string{
		"pyproject.toml": "[project]\nname='preflight-fixture'\nversion='0.1.0'\ndependencies=['fastapi','uvicorn']\n",
		"uv.lock":        "",
		"main.py":        "from fastapi import FastAPI\napp = FastAPI()\n",
	})
	python := createLocalPythonVenv(t, filepath.Join(dir, ".venv"), "unused")
	err := main.DevLocalPreflightForTest(dir, "", os.Environ())
	if err == nil || !strings.Contains(err.Error(), "module uvicorn") || !strings.Contains(err.Error(), "uv sync") {
		t.Fatalf("missing selected-environment dependency: %v", err)
	}
	// A stdlib lookup must not import either application or framework code.
	cmd := exec.CommandContext(t.Context(), python, "-c", "import sysconfig; print(sysconfig.get_path('purelib'))")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"uvicorn", "fastapi"} {
		if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(out)), module+".py"), []byte("raise RuntimeError('must not import framework')\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := main.DevLocalPreflightForTest(dir, "", os.Environ()); err != nil {
		t.Fatalf("installed modules rejected: %v", err)
	}
}
