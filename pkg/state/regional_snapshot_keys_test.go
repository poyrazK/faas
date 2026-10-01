package state

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/region"
)

func TestRegionalSnapshotCaptureCompanions(t *testing.T) {
	for _, tier := range []string{SnapshotTierInit, SnapshotTierWarm} {
		key := region.ArtifactKey("eu-fsn", SnapshotCaptureMemKey("dep", tier, "capture"))
		snapshot := Snapshot{StorageKey: key, DeploymentID: "dep", Tier: tier}
		prefix := strings.TrimSuffix(key, "/mem")
		if !IsSnapshotCaptureKey(key) || SnapshotVMStateKey(snapshot) != prefix+"/vmstate" || SnapshotDriveKey(snapshot) != prefix+"/drive" {
			t.Fatalf("lost regional capture companions: %+v", snapshot)
		}
	}
	if IsSnapshotCaptureKey("regions/Eu/snap/dep/captures/c/v2/mem") {
		t.Fatal("accepted invalid region")
	}
}
