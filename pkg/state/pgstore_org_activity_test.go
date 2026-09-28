package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreListAppsByOrgFiltersPersistedOrganization(t *testing.T) {
	store, ctx := pgStore(t)
	owner, err := store.CreateAccount(ctx, "org-app-owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount owner: %v", err)
	}
	otherOwner, err := store.CreateAccount(ctx, "org-app-other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount other owner: %v", err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "org-app-" + uuid.NewString()[:8], Name: "Workspace", Plan: api.PlanPro})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	foreignOrg, err := store.CreateOrg(ctx, state.Org{Slug: "org-app-x-" + uuid.NewString()[:8], Name: "Foreign", Plan: api.PlanPro})
	if err != nil {
		t.Fatalf("CreateOrg foreign: %v", err)
	}
	for _, app := range []state.App{
		{AccountID: owner.ID, OrgID: org.ID, Slug: "org-app-a-" + uuid.NewString()[:8], Type: state.AppTypeApp},
		{AccountID: otherOwner.ID, OrgID: org.ID, Slug: "org-app-b-" + uuid.NewString()[:8], Type: state.AppTypeFunction},
		{AccountID: owner.ID, OrgID: org.ID, Slug: "org-app-deleted-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppDeleted},
		{AccountID: owner.ID, OrgID: foreignOrg.ID, Slug: "org-app-foreign-" + uuid.NewString()[:8], Type: state.AppTypeApp},
		{AccountID: owner.ID, Slug: "org-app-unattributed-" + uuid.NewString()[:8], Type: state.AppTypeApp},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %q: %v", app.Slug, err)
		}
	}

	apps, err := store.ListAppsByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListAppsByOrg: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("ListAppsByOrg returned %d apps, want 2: %+v", len(apps), apps)
	}
	for _, app := range apps {
		if app.OrgID != org.ID || app.Status == state.AppDeleted {
			t.Errorf("ListAppsByOrg returned out-of-scope/deleted app: %+v", app)
		}
	}
}

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
