package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// adr: 20261030122204 — vmmd attaches only a seed copy builderd wrote beside
// the builder's drive1.
func TestToColdBootRequestBuildKitSeedDrive(t *testing.T) {
	const drive1 = "/srv/fc/builder/drive/build-1.ext4"
	cases := []struct {
		name      string
		layer     string
		export    string
		seed      string
		want      string
		wantError bool
	}{
		{name: "no seed", layer: drive1, export: "/export", seed: "", want: ""},
		{name: "beside drive1", layer: drive1, export: "/export", seed: "/srv/fc/builder/drive/build-1-buildkit.ext4", want: "/srv/fc/builder/drive/build-1-buildkit.ext4"},
		{name: "not a builder", layer: drive1, export: "", seed: "/srv/fc/builder/drive/build-1-buildkit.ext4", wantError: true},
		{name: "another directory", layer: drive1, export: "/export", seed: "/srv/fc/builder/seed/seed.ext4", wantError: true},
		{name: "relative", layer: drive1, export: "/export", seed: "build-1-buildkit.ext4", wantError: true},
		{name: "unclean", layer: drive1, export: "/export", seed: "/srv/fc/builder/drive/../drive/build-1-buildkit.ext4", wantError: true},
		{name: "drive1 itself", layer: drive1, export: "/export", seed: drive1, wantError: true},
		{name: "not ext4", layer: drive1, export: "/export", seed: "/srv/fc/builder/drive/build-1-buildkit.img", wantError: true},
		{name: "storage-key layer", layer: "apps/a/l.ext4", export: "/export", seed: "/srv/fc/builder/drive/build-1-buildkit.ext4", wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &vmmdpb.CreateColdBootRequest{Instance: "build-1", App: &vmmdpb.AppSpec{BaseKey: "/base", LayerKey: tc.layer}}
			if tc.export != "" || tc.seed != "" {
				req.Build = &vmmdpb.BuildSpec{ExportDir: tc.export, BuildkitSeedDrive: tc.seed}
			}
			got, err := toColdBootRequest(context.Background(), req)
			if (err != nil) != tc.wantError {
				t.Fatalf("toColdBootRequest error = %v, want error %v", err, tc.wantError)
			}
			if err == nil && got.BuildKitSeedDrive != tc.want {
				t.Fatalf("BuildKitSeedDrive = %q, want %q", got.BuildKitSeedDrive, tc.want)
			}
		})
	}
}
