package schedule

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type schedulerFixture struct {
	store  *state.MemStore
	member state.Account
	org    state.Org
	app    state.App
	key    state.APIKey
	sr     state.ScheduledRelease
	now    time.Time
}

func newSchedulerFixture(t *testing.T, role state.OrgRole) schedulerFixture {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	owner, err := store.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.CreateAccount(ctx, "member@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, member.ID, role, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: owner.ID, OrgID: org.ID, Slug: "team-api", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	key, err := store.CreateOrgAPIKey(ctx, org.ID, member.ID, []byte("hash"), "ci", []string{"deploy:write"}, &expires)
	if err != nil {
		t.Fatal(err)
	}
	sr := state.ScheduledRelease{
		ID: "s1", ReleaseAt: now, ScheduledBy: "api_key:" + key.ID,
		ScheduledByAccountID: member.ID, ScheduledByKeyID: key.ID,
	}
	return schedulerFixture{store: store, member: member, org: org, app: app, key: key, sr: sr, now: now}
}

func (fx schedulerFixture) check(t *testing.T, active ...state.ReleaseFreeze) (Decision, bool) {
	t.Helper()
	return SchedulerStanding(t.Context(), fx.store, fx.sr, fx.app, active, fx.now)
}

func wantLost(t *testing.T, d Decision, ok bool, why string) {
	t.Helper()
	if ok || d.Action != ActionEnd || d.State != state.ScheduledReleaseBlocked || d.Code != CodeSchedulerLostAccess {
		t.Fatalf("decision = %+v ok=%v, want blocked %s", d, ok, CodeSchedulerLostAccess)
	}
	if !strings.Contains(d.Detail, why) {
		t.Fatalf("detail = %q, want it to say %q", d.Detail, why)
	}
}

// adr: 20261021152201 — amendment 2026-11-10: a schedule starts only while
// whoever scheduled it may still release the app.
func TestSchedulerStanding(t *testing.T) {
	t.Run("a developer with a live key may release", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		if d, ok := fx.check(t); !ok {
			t.Fatalf("decision = %+v, want the start to go ahead", d)
		}
	})
	t.Run("a schedule recorded before scheduler identities is not checked", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		if err := fx.store.RemoveOrgMember(t.Context(), fx.org.ID, fx.member.ID); err != nil {
			t.Fatal(err)
		}
		fx.sr.ScheduledByAccountID, fx.sr.ScheduledByKeyID = "", ""
		if d, ok := fx.check(t); !ok {
			t.Fatalf("decision = %+v, want a legacy schedule to start", d)
		}
	})
	t.Run("a signed-in member removed from the company", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		fx.sr.ScheduledBy, fx.sr.ScheduledByKeyID = "member@example.com", ""
		if err := fx.store.RemoveOrgMember(t.Context(), fx.org.ID, fx.member.ID); err != nil {
			t.Fatal(err)
		}
		d, ok := fx.check(t)
		wantLost(t, d, ok, "member@example.com scheduled this release and may no longer deploy team-api")
	})
	t.Run("a removed member's key", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		if err := fx.store.RemoveOrgMember(t.Context(), fx.org.ID, fx.member.ID); err != nil {
			t.Fatal(err)
		}
		d, ok := fx.check(t)
		wantLost(t, d, ok, "its API key was revoked")
	})
	t.Run("a member demoted to viewer", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		if err := fx.store.UpdateOrgMemberRole(t.Context(), fx.org.ID, fx.member.ID, state.OrgRoleViewer); err != nil {
			t.Fatal(err)
		}
		d, ok := fx.check(t)
		wantLost(t, d, ok, "may no longer deploy")
	})
	t.Run("a deleted key", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		if err := fx.store.DeleteAPIKey(t.Context(), fx.member.ID, fx.key.ID); err != nil {
			t.Fatal(err)
		}
		d, ok := fx.check(t)
		if ok || d.Code != CodeSchedulerLostAccess {
			t.Fatalf("decision = %+v ok=%v, want blocked", d, ok)
		}
	})
	t.Run("an expired key", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		fx.now = fx.now.Add(2 * time.Hour)
		d, ok := fx.check(t)
		wantLost(t, d, ok, "its API key expired")
	})
	t.Run("a suspended account", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleDeveloper)
		if err := fx.store.UpdateAccountStatus(t.Context(), fx.member.ID, state.AccountSuspended); err != nil {
			t.Fatal(err)
		}
		d, ok := fx.check(t)
		wantLost(t, d, ok, "that account is suspended")
	})
	t.Run("a company freeze override needs the right to lift it at release time", func(t *testing.T) {
		fx := newSchedulerFixture(t, state.OrgRoleAdmin)
		freeze := state.ReleaseFreeze{ID: "f1", OrgID: fx.org.ID, Kind: state.ReleaseFreezeWindow, Source: state.ReleaseFreezeSourceManual, CreatedBy: "owner@example.com"}
		fx.sr.OverrideFreezeIDs = []string{freeze.ID}
		if d, ok := fx.check(t, freeze); !ok {
			t.Fatalf("admin override decision = %+v, want the start to go ahead", d)
		}
		if err := fx.store.UpdateOrgMemberRole(t.Context(), fx.org.ID, fx.member.ID, state.OrgRoleDeveloper); err != nil {
			t.Fatal(err)
		}
		d, ok := fx.check(t, freeze)
		wantLost(t, d, ok, "may no longer lift the company freeze f1")
		// The window's own creator may still lift it.
		freeze.CreatedBy = fx.sr.ScheduledBy
		if d, ok := fx.check(t, freeze); !ok {
			t.Fatalf("creator override decision = %+v, want the start to go ahead", d)
		}
		// A freeze the schedule does not override is not its concern here.
		fx.sr.OverrideFreezeIDs = nil
		freeze.CreatedBy = "owner@example.com"
		if d, ok := fx.check(t, freeze); !ok {
			t.Fatalf("uncovered freeze decision = %+v, want the start to go ahead", d)
		}
	})
}
