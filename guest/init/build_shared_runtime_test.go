package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
)

// spec: §4.6
// H10-140: only Node source builds of apps are classified, and a build whose
// inputs cannot be read says so instead of passing as eligible.
func TestBuildSharedRuntimeVerdict(t *testing.T) {
	plan, err := os.ReadFile("../../pkg/sharedruntime/testdata/railpack-0.38.0-node-plain.json")
	if err != nil {
		t.Fatal(err)
	}
	m := api.BuildManifest{Framework: api.FrameworkRailpackNode, Workdir: "/build/src", OutDir: "/build/out"}
	files := map[string][]byte{
		filepath.Join(m.Workdir, "package.json"): []byte(`{"scripts":{"start":"node worker.js"}}`),
		railpackPlanPath(m):                      plan,
	}
	read := func(name string) ([]byte, error) {
		if data, ok := files[name]; ok {
			return data, nil
		}
		return nil, fs.ErrNotExist
	}
	withFramework := func(f api.BuildFramework) api.BuildManifest { c := m; c.Framework = f; return c }
	function := m
	function.Function = true

	tests := []struct {
		name       string
		manifest   api.BuildManifest
		read       func(string) ([]byte, error)
		wantNil    bool
		wantReason string
	}{
		{name: "node app is classified", manifest: m, read: read, wantReason: sharedruntime.ReasonSystemPackages},
		{name: "function is not classified", manifest: function, read: read, wantNil: true},
		{name: "dockerfile is not classified", manifest: withFramework(api.FrameworkDockerfile), read: read, wantNil: true},
		{name: "python is not classified yet", manifest: withFramework(api.FrameworkRailpackPython), read: read, wantNil: true},
		{name: "auto without package.json is not node", manifest: withFramework(api.FrameworkAuto), read: func(string) ([]byte, error) { return nil, fs.ErrNotExist }, wantNil: true},
		{name: "node without package.json", manifest: m, read: func(string) ([]byte, error) { return nil, fs.ErrNotExist }, wantReason: sharedruntime.ReasonPackageUnreadable},
		{name: "unreadable plan", manifest: m, read: func(name string) ([]byte, error) {
			if name == railpackPlanPath(m) {
				return nil, fs.ErrNotExist
			}
			return read(name)
		}, wantReason: sharedruntime.ReasonPlanUnreadable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSharedRuntimeVerdict(tt.manifest, tt.read)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("verdict = %+v, want none", *got)
				}
				return
			}
			if got == nil || got.Eligible || got.Reason != tt.wantReason {
				t.Fatalf("verdict = %+v, want reason %q", got, tt.wantReason)
			}
		})
	}
}
