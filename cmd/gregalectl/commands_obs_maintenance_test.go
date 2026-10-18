package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261015154500000: operator output exposes durable maintenance state
// and preserves unknown legacy metadata instead of inferring a healthy zero.
func TestWriteObsMaintenanceHuman(t *testing.T) {
	for _, state := range []string{"disabled", "never_collected", "stale", "failed", "current"} {
		t.Run(state, func(t *testing.T) {
			r := api.ObsHealthResponse{
				AlertHistory:   &api.ObsAlertCollectionHealth{State: "unknown", CollectionState: state},
				AlertRetention: &api.ObsAlertRetentionHealth{State: "failed", Reason: "cleanup_failed", ExpiredEpisodes: 7},
			}
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var snap map[string]any
			if err := json.Unmarshal(b, &snap); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			writeObsMaintenanceHuman(&out, snap)
			for _, part := range []string{"alert_collection: " + state, "alert_retention: failed", "reason=cleanup_failed", "expired_episodes=7", "last_pruned_episodes=<nil>"} {
				if !strings.Contains(out.String(), part) {
					t.Fatalf("missing %q: %s", part, out.String())
				}
			}
		})
	}
	var out bytes.Buffer
	writeObsMaintenanceHuman(&out, map[string]any{})
	if !strings.Contains(out.String(), "alert_retention: unknown") || strings.Contains(out.String(), "expired_episodes=0") {
		t.Fatalf("legacy became zero: %s", out.String())
	}
}

func TestObsBundleMaintenanceCoverage(t *testing.T) {
	b := obsBundleTestSnapshot(obsBundleTestRef, 25, 25)
	at := time.Now().UTC()
	b.Health.Data.AlertHistory.CollectionState = "disabled"
	b.Health.Data.AlertRetention = &api.ObsAlertRetentionHealth{State: "stale", LastSuccessAt: &at, ExpiredEpisodes: 9, Reason: "observation_stale"}
	summarizeObsBundle(&b)
	if b.Coverage.AlertCollectionState != "disabled" || b.Coverage.RetentionState != "stale" || b.Coverage.ExpiredEpisodes == nil || *b.Coverage.ExpiredEpisodes != 9 {
		t.Fatalf("coverage=%+v", b.Coverage)
	}
	b.Health.Data.AlertRetention = nil
	summarizeObsBundle(&b)
	if b.Coverage.ExpiredEpisodes != nil || b.Coverage.RetentionState != "unknown" {
		t.Fatalf("missing became zero: %+v", b.Coverage)
	}
	// Legacy evidence can be current without proving collection is enabled.
	b.Health.Data.AlertHistory.State = "current"
	b.Health.Data.AlertHistory.CollectionState = ""
	summarizeObsBundle(&b)
	if b.Coverage.AlertCollectionState != "unknown" || !strings.Contains(strings.Join(b.Coverage.MissingHealthMetadata, ","), "alert_history.collection_state") {
		t.Fatalf("legacy collection configuration was guessed: %+v", b.Coverage)
	}
}
