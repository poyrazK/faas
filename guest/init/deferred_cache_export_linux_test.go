//go:build linux

package main

// adr: 20261029171525

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeferCacheExport(t *testing.T) {
	deferred := api.BuildManifest{Framework: api.FrameworkRailpackNode, DependencyCache: true, DeferCacheExport: true}
	for _, tc := range []struct {
		name   string
		mutate func(*api.BuildManifest)
		want   bool
	}{
		{name: "Railpack", want: true},
		{name: "Dockerfile", mutate: func(m *api.BuildManifest) { m.Framework = api.FrameworkDockerfile }, want: true},
		{name: "switch off", mutate: func(m *api.BuildManifest) { m.DeferCacheExport = false }},
		{name: "no dependency cache", mutate: func(m *api.BuildManifest) { m.DependencyCache = false }},
		{name: "warm builder", mutate: func(m *api.BuildManifest) { m.KeepWarm = true }},
		{name: "cache export run", mutate: func(m *api.BuildManifest) { m.CacheExportOnly = true }},
		{name: "Dockerfile without the cache", mutate: func(m *api.BuildManifest) {
			m.Framework, m.BuildDependencyDigest = api.FrameworkDockerfile, "sha256:"+strings.Repeat("a", 64)
		}},
		{name: "Railpack with selected dependencies", mutate: func(m *api.BuildManifest) {
			m.BuildDependencyDigest = "sha256:" + strings.Repeat("a", 64)
		}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := deferred
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			if got := deferCacheExport(m); got != tc.want {
				t.Fatalf("deferCacheExport = %v, want %v", got, tc.want)
			}
		})
	}
}

// A deferred build exports its image and no cache. Railpack still runs the
// inputs check, whose answer and exit status no longer matter.
func TestBuildArgv_DeferredCacheExport(t *testing.T) {
	dockerfile := strings.Join(buildArgv(api.BuildManifest{Framework: api.FrameworkDockerfile, Workdir: "/build/src", OutDir: "/build/out",
		DependencyCache: true, DependencyCacheImport: true, DeferCacheExport: true}), " ")
	if strings.Contains(dockerfile, "--export-cache") || !strings.Contains(dockerfile, "--import-cache type=local,src=/build/cache") ||
		!strings.Contains(dockerfile, "--output type=oci,dest=/build/out/image.tar") {
		t.Fatalf("deferred Dockerfile argv = %s", dockerfile)
	}

	script := buildArgv(api.BuildManifest{Framework: api.FrameworkRailpackNode, Workdir: "/build/src", OutDir: "/build/out",
		DependencyCache: true, DependencyCacheImport: true, DeferCacheExport: true})[2]
	for _, helperBody := range []string{"echo export", "echo keep", "exit 3"} {
		dir := t.TempDir()
		ran := filepath.Join(dir, "ran")
		helper := filepath.Join(dir, "helper")
		if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch "+ran+"\n"+helperBody+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		stubbed := strings.NewReplacer(
			"/usr/local/bin/railpack", "true",
			"/proc/1/exe", helper,
			"exec /usr/local/bin/buildctl", "exec echo buildctl",
		).Replace(script)
		out, err := exec.Command("/bin/sh", "-c", stubbed).Output()
		if err != nil {
			t.Fatalf("helper %q: %v", helperBody, err)
		}
		if _, err := os.Stat(ran); err != nil {
			t.Fatalf("helper %q: the inputs check did not run", helperBody)
		}
		got := string(out)
		if strings.Contains(got, "--export-cache") || !strings.Contains(got, "--output type=oci,dest=/build/out/image.tar") {
			t.Fatalf("helper %q ran: %s", helperBody, got)
		}
	}
}

// The cache export run solves the deferred run's plan with the cache exporter
// and no image output.
func TestBuildArgv_CacheExportOnly(t *testing.T) {
	dockerfile := buildArgv(api.BuildManifest{Framework: api.FrameworkDockerfile, Workdir: "/build/src", OutDir: "/build/out",
		DependencyCache: true, DependencyCacheImport: true, CacheExportOnly: true})
	joined := strings.Join(dockerfile, " ")
	if strings.Contains(joined, "--output") || !strings.HasSuffix(joined, "--export-cache type=local,dest=/build/out/cache,mode=max") ||
		!strings.Contains(joined, "--import-cache type=local,src=/build/cache") {
		t.Fatalf("Dockerfile cache export argv = %s", joined)
	}

	root := t.TempDir()
	outDir := filepath.Join(root, "out")
	m := api.BuildManifest{Framework: api.FrameworkRailpackNode, Workdir: "/build/src", OutDir: outDir, GoCommand: "api",
		IssueSourceMaps: &api.IssueSourceMapBuildConfig{}, LayerCompression: api.BuildLayerCompressionZstd,
		DependencyCache: true, DependencyCacheImport: true, CacheExportOnly: true}
	script := buildArgv(m)[2]
	for _, unwanted := range []string{"railpack prepare", dependencyInputsHelperEnv + "=", "--output"} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("cache export script contains %q: %s", unwanted, script)
		}
	}
	stubbed := strings.NewReplacer("exec /usr/local/bin/buildctl", `exec printf '[%s]\n' "$RAILPACK_GO_BIN" buildctl`).Replace(script)
	if out, err := exec.Command("/bin/sh", "-c", stubbed).CombinedOutput(); err == nil {
		t.Fatalf("cache export ran without the deferred run's plan: %s", out)
	}
	if err := os.WriteFile(filepath.Join(root, "railpack-plan.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", "-c", stubbed).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[api]", "[--import-cache]", "[type=local,dest=/build/out/cache,mode=max,compression=zstd]", "[plain]"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("buildctl argument %s missing from:\n%s", want, out)
		}
	}
}

func TestClearOutputExcept(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"image.tar", dependencyInputsFile, "rootfs/app/index.js"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := clearOutputExcept(dir, dependencyInputsFile); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != dependencyInputsFile {
		t.Fatalf("output kept %v, want only %s", entries, dependencyInputsFile)
	}
}
