package main

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildDevPatchSourceMap(t *testing.T) {
	const verbatimPlan = `{"steps":[{"name":"build","inputs":[{"local":true,"include":["."]}]}],"deploy":{"inputs":[{"step":"build","include":["."]}]}}`
	manifest := api.BuildManifest{Framework: api.FrameworkRailpackNode, OutDir: "/build/out"}
	cases := []struct {
		name     string
		manifest api.BuildManifest
		read     func(string) ([]byte, error)
		want     api.DevPatchSourceMap
	}{
		{
			name:     "dockerfile builds are not classified",
			manifest: api.BuildManifest{Framework: api.FrameworkDockerfile, OutDir: "/build/out"},
			read:     func(string) ([]byte, error) { t.Fatal("dockerfile build read a Railpack plan"); return nil, nil },
			want:     api.DevPatchSourceMap{Version: 1, Reason: api.DevPatchReasonNotRailpack},
		},
		{
			name:     "missing plan",
			manifest: manifest,
			read:     func(string) ([]byte, error) { return nil, errors.New("missing") },
			want:     api.DevPatchSourceMap{Version: 1, Reason: api.DevPatchReasonPlanUnreadable},
		},
		{
			name:     "plan is read from beside the output directory",
			manifest: manifest,
			read: func(name string) ([]byte, error) {
				if name != "/build/railpack-plan.json" {
					t.Fatalf("read %q, want /build/railpack-plan.json", name)
				}
				return []byte(verbatimPlan), nil
			},
			want: api.DevPatchSourceMap{Version: 1, Verbatim: true, ImageDir: "/app",
				RebuildPaths: []string{".gregaleignore", "gregale.yaml", "railpack.json"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDevPatchSourceMap(tc.manifest, tc.read)
			if got == nil || got.Version != tc.want.Version || got.Verbatim != tc.want.Verbatim || got.Reason != tc.want.Reason ||
				got.ImageDir != tc.want.ImageDir || len(got.RebuildPaths) != len(tc.want.RebuildPaths) {
				t.Fatalf("buildDevPatchSourceMap = %+v, want %+v", got, tc.want)
			}
		})
	}
}
