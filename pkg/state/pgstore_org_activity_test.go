package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreOrgActivityRoundTripAndDedupe(t *testing.T) {
	store, ctx := pgStore(t)
	orgID, appID := uuid.New(), uuid.New()
	at := time.Date(2026, 9, 22, 14, 32, 0, 123, time.UTC)
	entry := state.OrgActivity{
		OrgID: orgID, OccurredAt: at, Kind: "app.deployed",
		ActorType: state.OrgActivityActorGitHub, ActorLabel: "GitHub Actions",
		ResourceType: "app", ResourceID: appID.String(), ResourceLabel: "payments",
		AppID: &appID, SourceType: "deployment", SourceID: uuid.NewString(),
		Data: []byte(`{"branch":"main"}`),
	}

	first, err := store.AppendOrgActivity(ctx, entry)
	if err != nil {
		t.Fatalf("AppendOrgActivity: %v", err)
	}
	entry.ActorLabel = "duplicate"
	duplicate, err := store.AppendOrgActivity(ctx, entry)
	if err != nil {
		t.Fatalf("AppendOrgActivity duplicate: %v", err)
	}
	if duplicate.ID != first.ID || duplicate.ActorLabel != first.ActorLabel {
		t.Fatalf("duplicate = %#v, want original %#v", duplicate, first)
	}

	rows, err := store.ListOrgActivity(ctx, state.OrgActivityFilter{
		OrgID: orgID, KindPrefix: "app.", ActorType: state.OrgActivityActorGitHub,
		AppID: &appID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListOrgActivity: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatalf("rows = %#v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil || data["branch"] != "main" {
		t.Fatalf("data = %#v, err=%v", data, err)
	}

	foreign, err := store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.New(), Limit: 10})
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign rows = %#v, err=%v", foreign, err)
	}
}
