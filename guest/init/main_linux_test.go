//go:build linux

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestDecideMode_BuildBeatsApp covers the precedence rule: a build manifest
// wins when both are present (defensive — base images normally carry at
// most one, but a misconfig shouldn't be a silent regression).
func TestDecideMode_BuildBeatsApp(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/build.json": &fstest.MapFile{Data: mustMarshal(t, api.BuildManifest{BuildID: "b", Framework: api.FrameworkRailpackNode, TimeoutSec: 60})},
		"etc/faas/app.json":   &fstest.MapFile{Data: []byte(`{"kind":"app"}`)},
	}
	mode, m, err := decideMode(fsys)
	if err != nil {
		t.Fatalf("decideMode: %v", err)
	}
	if mode != modeBuild {
		t.Fatalf("mode = %v, want modeBuild (build should beat app)", mode)
	}
	if m.BuildID != "b" || m.Framework != api.FrameworkRailpackNode || m.TimeoutSec != 60 {
		t.Errorf("manifest round-trip mismatch: %+v", m)
	}
}

func TestDecideMode_AppOnly(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/app.json": &fstest.MapFile{Data: []byte(`{}`)},
	}
	mode, _, err := decideMode(fsys)
	if err != nil {
		t.Fatalf("decideMode: %v", err)
	}
	if mode != modeApp {
		t.Errorf("mode = %v, want modeApp", mode)
	}
}

func TestDecideMode_BuildOnly(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/build.json": &fstest.MapFile{Data: mustMarshal(t, api.BuildManifest{BuildID: "b"})},
	}
	mode, m, err := decideMode(fsys)
	if err != nil {
		t.Fatalf("decideMode: %v", err)
	}
	if mode != modeBuild {
		t.Errorf("mode = %v, want modeBuild", mode)
	}
	if m.BuildID != "b" {
		t.Errorf("BuildID = %q, want b", m.BuildID)
	}
}

func TestDecideMode_BadJSONFallsBackToApp(t *testing.T) {
	fsys := fstest.MapFS{
		"etc/faas/build.json": &fstest.MapFile{Data: []byte(`{not json`)},
	}
	mode, _, _ := decideMode(fsys)
	if mode != modeApp {
		t.Errorf("mode = %v, want modeApp (garbage build.json must not panic)", mode)
	}
}

func TestEnsureResolverFile_PreservesUsableConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	want := "nameserver 10.0.0.1\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureResolverFile(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("resolver config changed: %q", got)
	}
}

func TestEnsureResolverFileCreatesFallbackAndReplacesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "resolv.conf")
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := ensureResolverFile(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("resolver path remained a symlink")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNameserver(got) {
		t.Fatalf("fallback has no nameserver: %q", got)
	}
	targetGot, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetGot) != "target" {
		t.Fatalf("symlink target was modified: %q", targetGot)
	}
}

// TestClassifyExitCodes is the canonical exit-code → FailureClass mapping.
// builderd's ProcessOne consumes these strings.
func TestClassifyExitCodes(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{0, ""},
		{1, "FailureUserError"},
		{124, "FailureTimeout"},
		{137, "FailureOOM"},
		{-1, "FailureUserError"},
		{42, "FailureUserError"},
	}
	for _, c := range cases {
		if got := classify(c.code); got != c.want {
			t.Errorf("classify(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestRootlessBuildkitUnshareArgsMapsSubordinateIDs(t *testing.T) {
	want := []string{"--user", "--map-auto", "--map-root-user", "--mount", "--fork", "/bin/sh", "-c", "id"}
	got := rootlessBuildkitUnshareArgs("/bin/sh", "-c", "id")
	if len(got) != len(want) {
		t.Fatalf("rootlessBuildkitUnshareArgs length = %d, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rootlessBuildkitUnshareArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestTailOf covers the LogTailBytes clamp.
func TestTailOf(t *testing.T) {
	if got := tailOf([]byte("hello"), 100); got != "hello" {
		t.Errorf("tailOf short = %q", got)
	}
	if got := tailOf([]byte("0123456789abcdef"), 4); got != "cdef" {
		t.Errorf("tailOf(long, 4) = %q, want cdef", got)
	}
	if got := tailOf([]byte("hello"), 0); got != "hello" {
		t.Errorf("tailOf(long, 0) = %q, want full", got)
	}
}

// TestBuildDoneShape round-trips a representative BuildDone payload through
// JSON to verify the field names match what builderd consumes. The actual
// writeAndPoweroff path is covered by the metal-loop integration test
// (`make metal-lima`); here we just lock the wire shape.
func TestBuildDoneShape(t *testing.T) {
	done := api.BuildDone{
		SchemaVersion: 1,
		BuildID:       "b-shape",
		ExitCode:      137,
		OCIImagePath:  "/build/out/image.tar",
		LogTail:       "step 1: ..., step 2: ...",
		FailureClass:  "FailureOOM",
	}
	data, err := json.Marshal(done)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got api.BuildDone
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.BuildID != "b-shape" || got.ExitCode != 137 || got.FailureClass != "FailureOOM" || got.OCIImagePath != "/build/out/image.tar" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestFlattenSingleSourceDir(t *testing.T) {
	workdir := t.TempDir()
	root := filepath.Join(workdir, "hello-node")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "index.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := flattenSingleSourceDir(workdir); err != nil {
		t.Fatalf("flattenSingleSourceDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "package.json")); err != nil {
		t.Fatalf("package.json not promoted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "src", "index.js")); err != nil {
		t.Fatalf("nested source not promoted: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("archive root still exists: err=%v", err)
	}
}

func TestStageExecutable_CopiesAndMarksExecutable(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "nested", "mise")
	want := []byte("mise-musl")
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stageExecutable(source, target); err != nil {
		t.Fatalf("stageExecutable: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("target = %q, want %q", got, want)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("target mode = %o, want 755", info.Mode().Perm())
	}
}

func TestValidateBuilderShell_RequiresFunctionalBash(t *testing.T) {
	dir := t.TempDir()
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash is unavailable in the test environment: %v", err)
	}
	if err := validateBuilderShell([]string{shell}, nil); err != nil {
		t.Fatalf("validateBuilderShell(valid): %v", err)
	}

	broken := filepath.Join(dir, "busybox-as-bash")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho 'bash: applet not found' >&2\nexit 127\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateBuilderShell([]string{broken}, nil); err == nil || !strings.Contains(err.Error(), "not a functional bash") {
		t.Fatalf("validateBuilderShell(broken) = %v, want functional-shell error", err)
	}
}

func TestValidateBuilderShell_Missing(t *testing.T) {
	err := validateBuilderShell([]string{filepath.Join(t.TempDir(), "bash")}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not contain a functional bash") {
		t.Fatalf("validateBuilderShell(missing) = %v, want missing-shell error", err)
	}
}

func TestFlattenSingleSourceDirLeavesRootFiles(t *testing.T) {
	workdir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workdir, "hello-node"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "package.json"), []byte(`{"name":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := flattenSingleSourceDir(workdir); err != nil {
		t.Fatalf("flattenSingleSourceDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "hello-node")); err != nil {
		t.Fatalf("root directory unexpectedly changed: %v", err)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestBuildArgv pins the in-VM build-engine argv shape. builderd relies
// on the (framework → argv) mapping to render BuildDone.FailureClass
// correctly (the LogTail carries railpack / buildctl output verbatim,
// so the binary name is the operator's grep target). A regression that
// adds a new BuildFramework without extending this table would silently
// land in the `default` (auto) branch and produce a non-Railpack-aware
// log tail that the customer can't act on.
func TestBuildArgv(t *testing.T) {
	workdir := "/build/src"
	outdir := "/build/out"
	cases := []struct {
		name string
		fw   api.BuildFramework
		want []string
	}{
		{
			name: "dockerfile → buildctl",
			fw:   api.FrameworkDockerfile,
			want: []string{
				"/usr/local/bin/buildctl", "--addr", "unix:///run/buildkit/buildkitd.sock", "build",
				"--frontend", "dockerfile.v0",
				"--local", "context=" + workdir,
				"--local", "dockerfile=" + workdir,
				"--output", "type=oci,dest=" + outdir + "/image.tar",
			},
		},
		{
			name: "node → railpack",
			fw:   api.FrameworkRailpackNode,
			want: []string{"/bin/sh", "-c", "set -x; /usr/local/bin/railpack prepare '/build/src' --plan-out '/build/railpack-plan.json' --info-out '/build/railpack-info.json' && exec /usr/local/bin/buildctl --addr unix:///run/buildkit/buildkitd.sock build --frontend gateway.v0 --opt source=ghcr.io/railwayapp/railpack-frontend:latest --opt filename=railpack-plan.json --local context='/build/src' --local dockerfile='/build' --output type=oci,dest='/build/out/image.tar' --progress plain"},
		},
		{
			name: "python → railpack",
			fw:   api.FrameworkRailpackPython,
			want: []string{"/bin/sh", "-c", "set -x; /usr/local/bin/railpack prepare '/build/src' --plan-out '/build/railpack-plan.json' --info-out '/build/railpack-info.json' && exec /usr/local/bin/buildctl --addr unix:///run/buildkit/buildkitd.sock build --frontend gateway.v0 --opt source=ghcr.io/railwayapp/railpack-frontend:latest --opt filename=railpack-plan.json --local context='/build/src' --local dockerfile='/build' --output type=oci,dest='/build/out/image.tar' --progress plain"},
		},
		{
			name: "go → railpack",
			fw:   api.FrameworkRailpackGo,
			want: []string{"/bin/sh", "-c", "set -x; /usr/local/bin/railpack prepare '/build/src' --plan-out '/build/railpack-plan.json' --info-out '/build/railpack-info.json' && exec /usr/local/bin/buildctl --addr unix:///run/buildkit/buildkitd.sock build --frontend gateway.v0 --opt source=ghcr.io/railwayapp/railpack-frontend:latest --opt filename=railpack-plan.json --local context='/build/src' --local dockerfile='/build' --output type=oci,dest='/build/out/image.tar' --progress plain"},
		},
		{
			name: "auto → railpack (default branch)",
			fw:   api.FrameworkAuto,
			want: []string{"/bin/sh", "-c", "set -x; /usr/local/bin/railpack prepare '/build/src' --plan-out '/build/railpack-plan.json' --info-out '/build/railpack-info.json' && exec /usr/local/bin/buildctl --addr unix:///run/buildkit/buildkitd.sock build --frontend gateway.v0 --opt source=ghcr.io/railwayapp/railpack-frontend:latest --opt filename=railpack-plan.json --local context='/build/src' --local dockerfile='/build' --output type=oci,dest='/build/out/image.tar' --progress plain"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildArgv(api.BuildManifest{
				Framework: tc.fw,
				Workdir:   workdir,
				OutDir:    outdir,
			})
			if !equalSlice(got, tc.want) {
				t.Errorf("buildArgv(%q) = %v, want %v", tc.fw, got, tc.want)
			}
		})
	}
}

func TestBuildArgv_WorkspaceContextUsesSelectedWorkdir(t *testing.T) {
	got := buildArgv(api.BuildManifest{
		Framework:    api.FrameworkRailpackNode,
		BuildContext: "/build/src",
		Workdir:      "/build/src/apps/api",
		OutDir:       "/build/out",
	})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "railpack prepare '/build/src/apps/api'") {
		t.Fatalf("workspace argv does not prepare selected workdir: %s", joined)
	}
	if !strings.Contains(joined, "--local context='/build/src/apps/api'") {
		t.Fatalf("workspace argv does not use the plan’s selected source directory: %s", joined)
	}

	docker := buildArgv(api.BuildManifest{
		Framework:    api.FrameworkDockerfile,
		BuildContext: "/build/src",
		Workdir:      "/build/src/apps/api",
		OutDir:       "/build/out",
	})
	dockerJoined := strings.Join(docker, " ")
	if !strings.Contains(dockerJoined, "--local context=/build/src") ||
		!strings.Contains(dockerJoined, "--local dockerfile=/build/src/apps/api") {
		t.Fatalf("workspace docker argv has wrong context/workdir: %s", dockerJoined)
	}
}

func TestBuildArgv_DeveloperDependencyCache(t *testing.T) {
	got := buildArgv(api.BuildManifest{
		Framework:             api.FrameworkRailpackNode,
		Workdir:               "/build/src",
		OutDir:                "/build/out",
		DependencyCache:       true,
		DependencyCacheImport: true,
	})
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"--import-cache 'type=local,src=/build/cache'",
		"--export-cache 'type=local,dest=/build/out/cache,mode=max'",
		"--output type=oci,dest='/build/out/image.tar'",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("developer cache argv missing %q: %s", want, joined)
		}
	}

	cold := strings.Join(buildArgv(api.BuildManifest{
		Framework:       api.FrameworkRailpackNode,
		Workdir:         "/build/src",
		OutDir:          "/build/out",
		DependencyCache: true,
	}), " ")
	if strings.Contains(cold, "--import-cache") || !strings.Contains(cold, "--export-cache") {
		t.Fatalf("cold developer cache argv = %s", cold)
	}
}

func TestRemoveOversizedDependencyCache(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blobs", "one"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := removeOversizedDependencyCache(root, 32)
	if err != nil || removed {
		t.Fatalf("under budget: removed=%v err=%v", removed, err)
	}
	removed, err = removeOversizedDependencyCache(root, 3)
	if err != nil || !removed {
		t.Fatalf("over budget: removed=%v err=%v", removed, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("oversized cache remains: %v", err)
	}
	removed, err = removeOversizedDependencyCache(filepath.Join(t.TempDir(), "missing"), 3)
	if err != nil || removed {
		t.Fatalf("missing cache: removed=%v err=%v", removed, err)
	}
}

func TestManifestBuildContextFallsBackToWorkdir(t *testing.T) {
	if got := manifestBuildContext(api.BuildManifest{Workdir: "/build/src"}); got != "/build/src" {
		t.Fatalf("manifestBuildContext legacy fallback = %q, want /build/src", got)
	}
}

func TestPrepareRailpackConfig_UsesPlatformBaseAndRestoresSource(t *testing.T) {
	workdir := t.TempDir()
	original := []byte(`{"deploy":{"aptPackages":["curl"],"base":{"image":"customer/base"}},"custom":true}`)
	path := filepath.Join(workdir, "railpack.json")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}

	restore, err := prepareRailpackConfig(api.BuildManifest{
		Framework:      api.FrameworkRailpackNode,
		Runtime:        "node22",
		RuntimeBaseRef: "ghcr.io/poyrazk/runner-node22@sha256:" + strings.Repeat("a", 64),
		Workdir:        workdir,
	})
	if err != nil {
		t.Fatalf("prepareRailpackConfig: %v", err)
	}
	staged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(staged, &config); err != nil {
		t.Fatalf("decode staged config: %v", err)
	}
	deploy := config["deploy"].(map[string]any)
	base := deploy["base"].(map[string]any)
	if base["image"] != "ghcr.io/poyrazk/runner-node22@sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("staged base image = %v", base["image"])
	}
	if got := deploy["aptPackages"].([]any); len(got) != 1 || got[0] != "curl" {
		t.Fatalf("explicit aptPackages changed: %v", got)
	}
	if config["custom"] != true {
		t.Fatalf("custom Railpack config was not preserved: %v", config["custom"])
	}
	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("restored config = %q, want original %q", got, original)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat restored config: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o640 {
		t.Fatalf("restored mode = %o, want 0640", got)
	}
}

func TestPrepareRailpackConfig_CreatesAndRemovesPlatformConfig(t *testing.T) {
	workdir := t.TempDir()
	restore, err := prepareRailpackConfig(api.BuildManifest{
		Framework:      api.FrameworkRailpackGo,
		Runtime:        "go124-alpine",
		RuntimeBaseRef: "ghcr.io/poyrazk/runner-go124-alpine@sha256:" + strings.Repeat("b", 64),
		Workdir:        workdir,
	})
	if err != nil {
		t.Fatalf("prepareRailpackConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "railpack.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	deploy := config["deploy"].(map[string]any)
	if got := deploy["aptPackages"].([]any); len(got) != 0 {
		t.Fatalf("default Alpine aptPackages = %v, want empty", got)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "railpack.json")); !os.IsNotExist(err) {
		t.Fatalf("generated config remains after restore, err=%v", err)
	}
}

func TestPrepareRailpackConfig_MinimalBaseDefaultsAptPackagesEmpty(t *testing.T) {
	workdir := t.TempDir()
	restore, err := prepareRailpackConfig(api.BuildManifest{
		Framework:      api.FrameworkAuto,
		Runtime:        "",
		RuntimeBaseRef: "ghcr.io/poyrazk/base-minimal@sha256:" + strings.Repeat("c", 64),
		Workdir:        workdir,
	})
	if err != nil {
		t.Fatalf("prepareRailpackConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "railpack.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	deploy := config["deploy"].(map[string]any)
	if got := deploy["aptPackages"].([]any); len(got) != 0 {
		t.Fatalf("default minimal base aptPackages = %v, want empty", got)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Shutdown must reap the daemon before the guest syncs and powers off.
func TestStopBuildDaemonReapsBeforeReturn(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	start := time.Now()
	stopBuildDaemon(cmd, done)
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("daemon shutdown took %s", elapsed)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Fatalf("daemon was not reaped before return: %v", err)
	}
}
