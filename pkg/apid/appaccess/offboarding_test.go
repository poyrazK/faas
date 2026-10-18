package appaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRemovedCreatorInventorySkipsHiddenPages(t *testing.T) {
	store, creator, successor, org, companyApp := fixture(t, state.OrgRoleAdmin)
	personalApp, err := store.CreateApp(t.Context(), state.App{AccountID: creator.ID, Slug: "personal-api"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, successor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	type resource struct{ ID, AppID string }
	rows := []resource{{"company-1", companyApp.ID}, {"company-2", companyApp.ID}, {"personal-1", personalApp.ID}, {"personal-2", personalApp.ID}}
	r := request(creator, nil, "GET /v1/secrets", "/v1/secrets", "", "")
	fetch := func(before string) ([]resource, error) {
		start := 0
		for i, row := range rows {
			if row.ID == before {
				start = i + 1
				break
			}
		}
		if start >= len(rows) {
			return nil, nil
		}
		return rows[start : start+1], nil
	}
	filter := func(rows []resource) ([]resource, error) {
		return VisibleResources(r, store, creator, rows, func(row resource) string { return row.AppID })
	}
	before := ""
	for _, want := range []string{"personal-1", "personal-2"} {
		visible, next, err := VisiblePage(t.Context(), before, 1, fetch, filter, func(row resource) string { return row.ID })
		if err != nil || len(visible) != 1 || visible[0].ID != want || next != want {
			t.Fatalf("page after %q = %+v cursor=%q err=%v; want %s without company cursor", before, visible, next, err, want)
		}
		before = next
	}
}

func TestInventoryDeniesUnreviewedRoles(t *testing.T) {
	for _, role := range []state.OrgRole{state.OrgRoleBilling, "future-role"} {
		t.Run(string(role), func(t *testing.T) {
			store, _, actor, _, app := fixture(t, role)
			r := request(actor, nil, "GET /v1/apps", "/v1/apps", "", "")
			visible, err := VisibleApps(r, store, actor, []state.App{app})
			if err != nil || len(visible) != 0 {
				t.Fatalf("unreviewed inventory role can read apps: %+v %v", visible, err)
			}
		})
	}
}

func TestOrganizationInventoryKeyCannotReadLegacyPersonalApp(t *testing.T) {
	store, creator, _, org, shared := fixture(t, state.OrgRoleAdmin)
	personal, err := store.CreateApp(t.Context(), state.App{AccountID: creator.ID, Slug: "personal-api"})
	if err != nil {
		t.Fatal(err)
	}
	r := request(creator, &state.APIKey{OrgID: org.ID}, "GET /v1/apps", "/v1/apps", "", "")
	visible, err := VisibleApps(r, store, creator, []state.App{shared, personal})
	if err != nil || len(visible) != 1 || visible[0].ID != shared.ID {
		t.Fatalf("org key exposed personal app: %+v %v", visible, err)
	}
	// A personal session still sees both resources while membership is active.
	r = r.WithContext(authmw.WithPrincipal(r.Context(), creator, nil, nil))
	visible, err = VisibleApps(r, store, creator, []state.App{shared, personal})
	if err != nil || len(visible) != 2 {
		t.Fatalf("session lost legitimate account inventory: %+v %v", visible, err)
	}
}

type unavailableOrgAccess struct{}

func (unavailableOrgAccess) OrgAccessSnapshot(context.Context, string, string) (state.OrgAccessSnapshot, error) {
	return state.OrgAccessSnapshot{}, errors.New("database unavailable")
}

func (unavailableOrgAccess) CheckOrgAccess(context.Context) error {
	return errors.New("database unavailable")
}

func TestStreamAuthorizationFailsClosedOnLookupFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	done := make(chan struct{})
	go func() { watchAccess(ctx, unavailableOrgAccess{}, cancel, time.Millisecond); close(done) }()
	select {
	case <-done:
		if !errors.Is(context.Cause(ctx), state.ErrOrgAccessRevoked) {
			t.Fatalf("stream remained authorized on outage: %v", context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("stream authorization did not fail closed")
	}
}
