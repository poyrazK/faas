// adr: 20261027173012
package builderd

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261118003000: builderd cannot evict the captures and layers the node
// cache's eviction owner keeps, so the cache's floor on a build-sized disk
// must hold a whole build drive. rc.351 on production-us (2026-11-18) failed
// fsn-3's canary build at 18.3 GB free against a 16.1 GB floor.
func TestArtifactCacheBuildFloorHoldsABuildDrive(t *testing.T) {
	if api.ArtifactCacheBuildFreeBytes < BuildDriveMinFreeBytes {
		t.Fatalf("api.ArtifactCacheBuildFreeBytes = %d, below BuildDriveMinFreeBytes %d",
			api.ArtifactCacheBuildFreeBytes, BuildDriveMinFreeBytes)
	}
}

// #5709: a node with 20.9 GB free refused every cache-miss build while its
// artifact cache held tens of gigabytes it could drop.
func TestReclaimBuildDriveSpace(t *testing.T) {
	for _, tc := range []struct {
		name        string
		free        uint64
		reclaim     bool
		reclaimErr  error
		wantAsked   bool
		wantErrText string
	}{
		{name: "enough free", free: BuildDriveMinFreeBytes, reclaim: true},
		{name: "short of a drive", free: BuildDriveMinFreeBytes - 1, reclaim: true, wantAsked: true},
		{name: "no reclaimer", free: 0},
		{name: "reclaim fails", free: 0, reclaim: true, reclaimErr: errors.New("cache busy"), wantAsked: true, wantErrText: "cache busy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asked int64
			var reclaim BuildDriveSpaceReclaimer
			if tc.reclaim {
				reclaim = func(_ context.Context, need int64) error {
					asked = need
					return tc.reclaimErr
				}
			}
			err := reclaimBuildDriveSpace(t.Context(), "/srv/fc/builder/drive", reclaim, func(string) (uint64, error) { return tc.free, nil })
			if (asked == BuildDriveMinFreeBytes) != tc.wantAsked {
				t.Fatalf("asked for %d bytes, want asked=%v", asked, tc.wantAsked)
			}
			if tc.wantErrText == "" && err != nil || tc.wantErrText != "" && (err == nil || !errors.Is(err, tc.reclaimErr)) {
				t.Fatalf("err=%v, want %q", err, tc.wantErrText)
			}
		})
	}
}
