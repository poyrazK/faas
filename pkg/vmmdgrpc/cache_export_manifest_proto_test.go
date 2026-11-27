package vmmdgrpc

import (
	"bytes"
	"context"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261029171525 amendment 2 — vmmd stages only a builder's manifest
// that asks for the dependency cache export alone.
func TestToColdBootRequestCacheExportManifest(t *testing.T) {
	const valid = `{"schema_version":1,"dependency_cache":true,"cache_export_only":true}`
	cases := []struct {
		name      string
		export    string
		manifest  string
		wantError bool
	}{
		{name: "none", export: "/export"},
		{name: "cache export run", export: "/export", manifest: valid},
		{name: "not a builder", manifest: valid, wantError: true},
		{name: "not json", export: "/export", manifest: "{", wantError: true},
		{name: "an ordinary build", export: "/export", manifest: `{"schema_version":1,"dependency_cache":true}`, wantError: true},
		{name: "still defers", export: "/export", manifest: `{"dependency_cache":true,"cache_export_only":true,"defer_cache_export":true}`, wantError: true},
		{name: "no dependency cache", export: "/export", manifest: `{"cache_export_only":true}`, wantError: true},
		{name: "too large", export: "/export", manifest: valid[:len(valid)-1] + `,"x":"` + strings.Repeat("a", api.BuildCacheExportManifestMaxBytes) + `"}`, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &vmmdpb.CreateColdBootRequest{Instance: "build-1-cache", App: &vmmdpb.AppSpec{BaseKey: "/base", LayerKey: "/srv/fc/builder/drive/build-1.ext4"}}
			if tc.export != "" || tc.manifest != "" {
				req.Build = &vmmdpb.BuildSpec{ExportDir: tc.export, CacheExportManifest: []byte(tc.manifest)}
			}
			got, err := toColdBootRequest(context.Background(), req)
			if (err != nil) != tc.wantError {
				t.Fatalf("toColdBootRequest error = %v, want error %v", err, tc.wantError)
			}
			if err == nil && !bytes.Equal(got.CacheExportManifest, []byte(tc.manifest)) {
				t.Fatalf("CacheExportManifest = %q, want %q", got.CacheExportManifest, tc.manifest)
			}
		})
	}
}
