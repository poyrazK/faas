package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemStoreOrgActivityTenantOrderingPaginationAndDedupe(t *testing.T) {
	t.Parallel()
	store := NewMemStore()
	ctx := context.Background()
	orgA, orgB := uuid.New(), uuid.New()
	appA, appB := uuid.New(), uuid.New()
	at := time.Date(2026, 9, 22, 14, 32, 0, 0, time.UTC)

	appendRow := func(org, app uuid.UUID, source, kind string, when time.Time) OrgActivity {
		t.Helper()
		row, err := store.AppendOrgActivity(ctx, OrgActivity{
			OrgID: org, OccurredAt: when, Kind: kind,
			ActorType: OrgActivityActorUser, ActorLabel: "person@example.com",
			ResourceType: "app", ResourceID: app.String(), ResourceLabel: "payments",
			AppID: &app, SourceType: "test", SourceID: source,
		})
		if err != nil {
			t.Fatalf("AppendOrgActivity: %v", err)
		}
		return row
	}

	oldest := appendRow(orgA, appA, "one", "domain.added", at.Add(-time.Minute))
	middle := appendRow(orgA, appA, "two", "env.set", at)
	newest := appendRow(orgA, appB, "three", "app.deployed", at)
	appendRow(orgB, appB, "foreign", "app.deployed", at.Add(time.Hour))

	duplicate, err := store.AppendOrgActivity(ctx, OrgActivity{
		OrgID: orgA, Kind: "env.set", ActorType: OrgActivityActorSystem,
		ActorLabel: "different", ResourceType: "app", ResourceLabel: "different",
		SourceType: "test", SourceID: "two",
	})
	if err != nil {
		t.Fatalf("dedupe append: %v", err)
	}
	if duplicate.ID != middle.ID || duplicate.ActorLabel != middle.ActorLabel {
		t.Fatalf("dedupe returned %#v, want original %#v", duplicate, middle)
	}

	page, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: orgA, Limit: 2})
	if err != nil {
		t.Fatalf("ListOrgActivity page 1: %v", err)
	}
	if len(page) != 2 || page[0].ID != newest.ID || page[1].ID != middle.ID {
		t.Fatalf("page 1 = %#v, want newest/tied-id order", page)
	}

	page, err = store.ListOrgActivity(ctx, OrgActivityFilter{
		OrgID: orgA, Limit: 2,
		Before: &OrgActivityCursor{OccurredAt: middle.OccurredAt, ID: middle.ID},
	})
	if err != nil {
		t.Fatalf("ListOrgActivity page 2: %v", err)
	}
	if len(page) != 1 || page[0].ID != oldest.ID {
		t.Fatalf("page 2 = %#v, want oldest only", page)
	}

	filtered, err := store.ListOrgActivity(ctx, OrgActivityFilter{OrgID: orgA, AppID: &appB, KindPrefix: "app.", Limit: 10})
	if err != nil || len(filtered) != 1 || filtered[0].ID != newest.ID {
		t.Fatalf("filtered = %#v, err=%v", filtered, err)
	}
}

func TestNormalizeOrgActivityRejectsNonObjectData(t *testing.T) {
	t.Parallel()
	_, err := normalizeOrgActivity(OrgActivity{
		OrgID: uuid.New(), Kind: "env.set", ActorType: OrgActivityActorUser,
		ActorLabel: "person@example.com", ResourceType: "environment_variable",
		ResourceLabel: "DATABASE_URL", SourceType: "test", SourceID: "bad",
		Data: []byte(`"secret"`),
	}, time.Now())
	if err == nil {
		t.Fatal("normalizeOrgActivity accepted scalar data")
	}
}

func TestNormalizeOrgActivityRejectsUnnamespacedKind(t *testing.T) {
	t.Parallel()
	_, err := normalizeOrgActivity(OrgActivity{
		OrgID: uuid.New(), Kind: "deployed", ActorType: OrgActivityActorSystem,
		ActorLabel: "Gregale", ResourceType: "app", ResourceLabel: "payments",
		SourceType: "test", SourceID: "bad-kind",
	}, time.Now())
	if err == nil {
		t.Fatal("normalizeOrgActivity accepted an unnamespaced kind")
	}
}
