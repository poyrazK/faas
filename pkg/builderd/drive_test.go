//go:build linux

package builderd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWriteBuildManifest_RoundTrip(t *testing.T) {
	mp := t.TempDir()
	m := api.BuildManifest{
		SchemaVersion: 1,
		BuildID:       "b-test",
		TenantID:      "t-1",
		DeploymentID:  "d-1",
		SourceTarPath: "/build/src.tar",
		Framework:     api.FrameworkRailpackNode,
		TimeoutSec:    600,
		LogTailBytes:  4096,
	}
	if err := writeBuildManifest(mp, m); err != nil {
		t.Fatalf("writeBuildManifest: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(mp, "etc", "faas", "build.json"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got api.BuildManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.BuildID != m.BuildID || got.Framework != m.Framework || got.TimeoutSec != m.TimeoutSec {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, m)
	}
}

func TestCreateBuildDrive1_RejectsEmptyPaths(t *testing.T) {
	ctx := context.Background()
	if err := CreateBuildDrive1(ctx, "", api.BuildManifest{BuildID: "x"}); err == nil {
		t.Error("expected error for empty dest")
	}
	if err := CreateBuildDrive1(ctx, "/tmp/x", api.BuildManifest{}); err == nil {
		t.Error("expected error for empty BuildID")
	}
}
