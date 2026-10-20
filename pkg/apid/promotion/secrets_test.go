package promotion

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSecretChangesForLegacyWorkloadName(t *testing.T) {
	view := &state.ProjectEnvironmentPromotionReleaseConfiguration{Hash: "capture", Secrets: &api.ProjectReleaseSecretConfiguration{
		Versions:         map[string]map[string]string{"legacy": {"TOKEN": "new"}},
		PreviousVersions: map[string]map[string]string{"legacy": {"TOKEN": "old"}},
	}}
	change := ApplySnapshotChange(view, api.ProjectEnvironmentPromotionChange{WorkloadSlug: "legacy", Kind: "unchanged"})
	if change.Kind != "update" || len(change.SecretChanges) != 1 || change.SecretChanges[0].Key != "TOKEN" {
		t.Fatal("legacy workload omitted its secret change")
	}
}
