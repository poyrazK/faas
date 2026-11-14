//go:build unix

package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const localEnvNodeFields = `message:process.env.LOCAL_MESSAGE ?? 'unset', fallback:process.env.LOCAL_FALLBACK, removed:process.env.LOCAL_REMOVED ?? 'unset', declared:process.env.DECLARED, pid:process.pid`

func writeLocalEnv(t *testing.T, path, body string) {
	t.Helper()
	// Match the atomic rename used by editors.
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func waitLocalEnvWarning(t *testing.T, out *localAppTestOutput) {
	t.Helper()
	waitLocalEnvOutput(t, out, "Could not reload local config")
}

func waitLocalEnvOutput(t *testing.T, out *localAppTestOutput, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), text) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("missing %q in local output: %s", text, out.String())
}

func TestDevLocalHTTPAppEnvReload(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(fmt.Sprintf("external=%t", external), func(t *testing.T) {
			t.Setenv("LOCAL_FALLBACK", "shell-fallback")
			root := writeDevLocalProject(t, map[string]string{
				"package.json": `{"scripts":{"dev":"node server.cjs"}}`,
				"server.cjs":   `require('node:http').createServer((q,s)=>{s.setHeader('content-type','application/json');s.end(JSON.stringify({` + localEnvNodeFields + `}));}).listen(Number(process.env.PORT),process.env.HOST);`,
				"gregale.yaml": "dev:\n  env_file: .env.dev\n",
			})
			path := filepath.Join(root, ".env.dev")
			var args []string
			if external {
				path = filepath.Join(t.TempDir(), "outside.env")
				args = []string{"--env-file", path}
			}
			writeLocalEnv(t, path, "LOCAL_MESSAGE=first-fixture\nLOCAL_FALLBACK=file-fallback\nLOCAL_REMOVED=file-only\nHOST=192.0.2.1\nPORT=1\n")
			address, _, out, _ := startLocalAppCLI(t, root, args...)
			first := waitLocalAppBody(t, address, "first-fixture", out)
			writeLocalEnv(t, path, "LOCAL_MESSAGE=next-fixture\n")
			next := waitLocalAppBody(t, address, "next-fixture", out)
			if next["pid"] == first["pid"] || next["fallback"] != "shell-fallback" || next["removed"] != "unset" {
				t.Fatalf("env change did not rebuild the process environment: %v", next)
			}
			writeLocalEnv(t, path, "BROKEN sensitive-fixture\n")
			waitLocalEnvWarning(t, out)
			if got := waitLocalAppBody(t, address, "next-fixture", out); got["pid"] != next["pid"] {
				t.Fatal("invalid env edit replaced the healthy process")
			}
			writeLocalEnv(t, path, "# intentionally clear overrides\n")
			cleared := waitLocalAppBody(t, address, `"message":"unset"`, out)
			if cleared["fallback"] != "shell-fallback" || cleared["pid"] == next["pid"] {
				t.Fatal("clearing all overrides did not restart with the base environment")
			}
			for _, value := range []string{"first-fixture", "next-fixture", "sensitive-fixture", "file-fallback", "file-only"} {
				if strings.Contains(out.String(), value) {
					t.Fatalf("CLI output exposed a fixture env value: %s", out.String())
				}
			}
		})
	}
}

func TestDevLocalFunctionEnvReload(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	t.Setenv("LOCAL_FALLBACK", "shell-fallback")
	root := writeDevLocalProject(t, map[string]string{
		"gregale.yaml":                "functions:\n  - name: web\n    root: functions/web\n    entry: true\n    env: {DECLARED: manifest-value}\n  - name: python\n    root: functions/python\n    routes: [/python/**]\n    env: {DECLARED: python-value}\n",
		"functions/web/handler.js":    `exports.handler = async () => ({status:200,body:{` + localEnvNodeFields + `}});`,
		"functions/python/handler.py": "import os\ndef handler(event, ctx):\n    return {'status':200,'body':{'message':os.getenv('LOCAL_MESSAGE','unset'),'fallback':os.getenv('LOCAL_FALLBACK'),'removed':os.getenv('LOCAL_REMOVED','unset'),'declared':os.getenv('DECLARED'),'pid':os.getpid()}}\n",
	})
	path := filepath.Join(t.TempDir(), "outside.env")
	writeLocalEnv(t, path, "LOCAL_MESSAGE=first-fixture\nLOCAL_FALLBACK=file-fallback\nLOCAL_REMOVED=file-only\nDECLARED=file-value\n")
	address, _, out, _ := startLocalAppCLI(t, root, "--env-file", path)
	initial := map[string]any{}
	for _, route := range []string{"/", "/python/test"} {
		body := waitLocalAppBody(t, address+route, "first-fixture", out)
		initial[route] = body["pid"]
	}
	writeLocalEnv(t, path, "LOCAL_MESSAGE=next-fixture\nDECLARED=changed-file-value\n")
	for _, tc := range []struct{ route, declared string }{{"/", "manifest-value"}, {"/python/test", "python-value"}} {
		body := waitLocalAppBody(t, address+tc.route, "next-fixture", out)
		if body["pid"] == initial[tc.route] || body["declared"] != tc.declared || body["fallback"] != "shell-fallback" || body["removed"] != "unset" {
			t.Fatalf("function env reload or precedence failed: %v", body)
		}
	}
	// A request racing prewarm can create another worker. Wait for both
	// warmups to finish before checking that invalid edits preserve workers.
	waitLocalEnvOutput(t, out, "reloaded python")
	for _, route := range []string{"/", "/python/test"} {
		initial[route] = waitLocalAppBody(t, address+route, "next-fixture", out)["pid"]
	}
	reloads := strings.Count(out.String(), "reloaded ")
	writeLocalEnv(t, path, "BROKEN sensitive-fixture\n")
	waitLocalEnvWarning(t, out)
	for _, route := range []string{"/", "/python/test"} {
		if body := waitLocalAppBody(t, address+route, "next-fixture", out); body["pid"] != initial[route] {
			t.Fatal("invalid env edit recycled a healthy function worker")
		}
	}
	if strings.Count(out.String(), "reloaded ") != reloads {
		t.Fatal("invalid env edit caused a function reload")
	}
	writeLocalEnv(t, path, "LOCAL_MESSAGE=recovered-fixture\n")
	for _, route := range []string{"/", "/python/test"} {
		waitLocalAppBody(t, address+route, "recovered-fixture", out)
	}
	if strings.Contains(out.String(), "sensitive-fixture") || strings.Contains(out.String(), "file-value") {
		t.Fatal("CLI exposed an env value")
	}
}

func TestDevLocalGoFunctionEnvReload(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	template, err := os.ReadFile(filepath.Join("templates", "function-go", "handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the production persistent protocol, including its ready frame.
	source := strings.Replace(string(template), "Path: env.Path", `Path: os.Getenv("LOCAL_MESSAGE")`, 1)
	if source == string(template) {
		t.Fatal("Go template response changed; update the env fixture")
	}
	root := writeDevLocalProject(t, map[string]string{
		"handler.go": source,
	})
	path := filepath.Join(t.TempDir(), "outside.env")
	writeLocalEnv(t, path, "LOCAL_MESSAGE=first-fixture\n")
	address, _, out, _ := startLocalAppCLI(t, root, "--env-file", path)
	waitLocalAppBody(t, address, "first-fixture", out)
	writeLocalEnv(t, path, "LOCAL_MESSAGE=next-fixture\n")
	waitLocalAppBody(t, address, "next-fixture", out)
}

func TestDevLocalEnvNoReload(t *testing.T) {
	for _, function := range []bool{false, true} {
		t.Run(fmt.Sprintf("function=%t", function), func(t *testing.T) {
			files := map[string]string{"handler.js": `exports.handler = async () => ({status:200,body:{` + localEnvNodeFields + `}});`}
			if !function {
				files = map[string]string{"package.json": `{"scripts":{"dev":"node server.cjs"}}`, "server.cjs": `require('node:http').createServer((q,s)=>s.end(JSON.stringify({` + localEnvNodeFields + `}))).listen(Number(process.env.PORT),process.env.HOST);`}
			}
			root := writeDevLocalProject(t, files)
			path := filepath.Join(t.TempDir(), "outside.env")
			writeLocalEnv(t, path, "LOCAL_MESSAGE=first-fixture\n")
			address, _, out, _ := startLocalAppCLI(t, root, "--env-file", path, "--no-reload")
			first := waitLocalAppBody(t, address, "first-fixture", out)
			writeLocalEnv(t, path, "LOCAL_MESSAGE=next-fixture\n")
			time.Sleep(2 * time.Second)
			if body := waitLocalAppBody(t, address, "first-fixture", out); body["pid"] != first["pid"] {
				t.Fatal("--no-reload restarted on an env edit")
			}
		})
	}
}
