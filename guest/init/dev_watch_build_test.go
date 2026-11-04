package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
)

// adr: 1170 — the watch overlay sets the development command and marks the
// image, and only Node builds accept it.
func TestDevWatchRailpackConfig(t *testing.T) {
	m := api.BuildManifest{Framework: api.FrameworkRailpackNode, DevWatchCommand: "npm run dev", DevWatchHost: "dev-api.gregale.dev"}
	config := map[string]any{"deploy": map[string]any{"base": map[string]any{"image": "runner"}}}
	if err := devWatchRailpackConfig(config, m); err != nil {
		t.Fatal(err)
	}
	deploy := config["deploy"].(map[string]any)
	if deploy["startCommand"] != "npm run dev" || deploy["base"].(map[string]any)["image"] != "runner" {
		t.Fatalf("deploy = %+v", deploy)
	}
	variables := deploy["variables"].(map[string]any)
	if variables["NODE_ENV"] != "development" || variables[api.DevWatchEnv] != "1" || variables[api.DevWatchHostEnv] != "dev-api.gregale.dev" {
		t.Fatalf("variables = %+v", variables)
	}
	// Image environment keys must start with a letter; guest-init adds
	// Vite's at workload start instead (stampDevWatchEnv).
	if _, ok := variables[api.DevWatchViteAllowedHostsEnv]; ok || validEnvKey(api.DevWatchViteAllowedHostsEnv) {
		t.Fatalf("Vite's variable is in the image or would survive the env filter: %+v", variables)
	}
	for _, host := range []string{"", "Dev-API.gregale.dev", "a.example,evil.example"} {
		config := map[string]any{}
		if err := devWatchRailpackConfig(config, api.BuildManifest{Framework: api.FrameworkRailpackNode, DevWatchCommand: "npm run dev", DevWatchHost: host}); err != nil {
			t.Fatal(err)
		}
		variables := config["deploy"].(map[string]any)["variables"].(map[string]any)
		if _, ok := variables[api.DevWatchHostEnv]; ok {
			t.Fatalf("host %q reached the image: %+v", host, variables)
		}
	}
	if err := devWatchRailpackConfig(map[string]any{}, api.BuildManifest{Framework: api.FrameworkRailpackGo, DevWatchCommand: "air"}); err == nil ||
		!strings.HasPrefix(err.Error(), api.CodeDevWatchUnsupported) {
		t.Fatalf("go watch build error = %v, want %s", err, api.CodeDevWatchUnsupported)
	}
	python := map[string]any{}
	if err := devWatchRailpackConfig(python, api.BuildManifest{Framework: api.FrameworkRailpackPython, DevWatchCommand: "uvicorn main:app --reload"}); err != nil {
		t.Fatal(err)
	}
	pyVars := python["deploy"].(map[string]any)["variables"].(map[string]any)
	if _, node := pyVars["NODE_ENV"]; node || pyVars["PYTHONPATH"] != "/app/src:/app" || pyVars[api.DevWatchEnv] != "1" {
		t.Fatalf("python variables = %+v", pyVars)
	}
	if err := devWatchRailpackConfig(map[string]any{"steps": "not an object"}, m); err == nil {
		t.Fatal("a malformed railpack.json was accepted")
	}
	untouched := map[string]any{}
	if err := devWatchRailpackConfig(untouched, api.BuildManifest{Framework: api.FrameworkRailpackNode}); err != nil || len(untouched) != 0 {
		t.Fatalf("a normal build changed the overlay: %+v, %v", untouched, err)
	}
}

// The overlay's build commands must turn Railpack's real plans with a build
// step (a Node build script, a uv project install) into verbatim ones,
// which is what makes live patches apply.
func TestDevWatchOverlayMakesBuildPlansVerbatim(t *testing.T) {
	for _, tc := range []struct {
		fixture   string
		framework api.BuildFramework
		command   string
	}{
		{"railpack-0.38.0-node-build.json", api.FrameworkRailpackNode, "npm run dev"},
		{"railpack-0.38.0-py-uv.json", api.FrameworkRailpackPython, "uvicorn main:app --reload"},
	} {
		t.Run(tc.fixture, func(t *testing.T) { checkDevWatchOverlayVerbatim(t, tc.fixture, tc.framework, tc.command) })
	}
}

func checkDevWatchOverlayVerbatim(t *testing.T, fixture string, framework api.BuildFramework, command string) {
	t.Helper()
	data, err := os.ReadFile("../../pkg/devpatch/testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	if got := devpatch.ClassifyRailpackPlan(data); got.Verbatim || got.Reason != api.DevPatchReasonBuildCommand {
		t.Fatalf("fixture baseline = %+v, want build_command", got)
	}
	config := map[string]any{}
	if err := devWatchRailpackConfig(config, api.BuildManifest{Framework: framework, DevWatchCommand: command}); err != nil {
		t.Fatal(err)
	}
	overrides := config["steps"].(map[string]any)["build"].(map[string]any)["commands"]
	var plan map[string]any
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	for _, step := range plan["steps"].([]any) {
		if s := step.(map[string]any); s["name"] == "build" {
			s["commands"] = overrides
		}
	}
	rewritten, _ := json.Marshal(plan)
	got := devpatch.ClassifyRailpackPlan(rewritten)
	if !got.Verbatim {
		t.Fatalf("watch plan = %+v, want verbatim", got)
	}
	watched, err := devWatchSourceMap(api.BuildManifest{DevWatchCommand: command}, &got)
	if err != nil || !watched.Watch || !watched.Verbatim {
		t.Fatalf("watch source map = %+v, %v", watched, err)
	}
}

func TestDevWatchSourceMap(t *testing.T) {
	plain := &api.DevPatchSourceMap{Version: 1, Reason: api.DevPatchReasonBuildCommand}
	if got, err := devWatchSourceMap(api.BuildManifest{}, plain); err != nil || got != plain {
		t.Fatalf("normal build = %+v, %v; want untouched", got, err)
	}
	if _, err := devWatchSourceMap(api.BuildManifest{DevWatchCommand: "npm run dev"}, plain); err == nil ||
		!strings.Contains(err.Error(), api.CodeDevWatchUnsupported) || !strings.Contains(err.Error(), api.DevPatchReasonBuildCommand) {
		t.Fatalf("non-verbatim watch build error = %v", err)
	}
	if _, err := devWatchSourceMap(api.BuildManifest{DevWatchCommand: "npm run dev"}, nil); err == nil {
		t.Fatal("a watch build without a source map passed")
	}
}

func TestDevPatchRestartSkippedInWatchMode(t *testing.T) {
	t.Cleanup(func() { devWatchActive.Store(false) })
	calls := 0
	restart := devPatchRestart(func() error { calls++; return errors.New("restarted") })
	devWatchActive.Store(true)
	if err := restart(); err != nil || calls != 0 {
		t.Fatalf("watch mode restarted the workload: calls=%d err=%v", calls, err)
	}
	devWatchActive.Store(false)
	if err := restart(); err == nil || calls != 1 {
		t.Fatalf("normal mode did not restart: calls=%d err=%v", calls, err)
	}
}

// adr: 1170 — watch mode writes patched files in place so the development
// server's watcher keeps the inode it watches; other apps keep staged renames.
func TestDevPatchApplyWritesInPlaceInWatchMode(t *testing.T) {
	t.Cleanup(func() { devWatchActive.Store(false) })
	dir := t.TempDir()
	target := filepath.Join(dir, "index.js")
	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		watch    bool
		body     string
		sameFile bool
	}{{watch: true, body: "v2", sameFile: true}, {watch: false, body: "v3", sameFile: false}} {
		devWatchActive.Store(tc.watch)
		before, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := devPatchApply(dir, devPatchArchive(t, "index.js", tc.body), nil); err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if body, _ := os.ReadFile(target); string(body) != tc.body || os.SameFile(before, after) != tc.sameFile {
			t.Fatalf("watch=%t: index.js = %q, same inode %t; want %q, %t", tc.watch, body, os.SameFile(before, after), tc.body, tc.sameFile)
		}
	}
}

// devPatchArchive is a one-file live patch archive.
func devPatchArchive(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// adr: 1170 — a watch-mode workload gets Vite's allowed-hosts override from
// the environment's hostname; other workloads and bad hostnames get nothing.
func TestStampDevWatchEnv(t *testing.T) {
	env := []string{"PATH=/usr/bin", api.DevWatchHostEnv + "=dev-api.gregale.dev"}
	got := stampDevWatchEnv(append([]string(nil), env...), true)
	if devDebugEnvValue(got, api.DevWatchViteAllowedHostsEnv) != "dev-api.gregale.dev" {
		t.Fatalf("watch env = %v", got)
	}
	if got := stampDevWatchEnv(append([]string(nil), env...), false); len(got) != len(env) {
		t.Fatalf("non-watch env = %v", got)
	}
	bad := []string{api.DevWatchHostEnv + "=a.example,evil.example"}
	if got := stampDevWatchEnv(bad, true); len(got) != 1 {
		t.Fatalf("invalid host was stamped: %v", got)
	}
}
