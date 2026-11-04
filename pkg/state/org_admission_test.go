package state_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func orgAdmissionStore(t *testing.T, postgres bool) (state.Store, *pgxpool.Pool) {
	t.Helper()
	if !postgres {
		return state.NewMemStore(), nil
	}
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool
}

func orgAdmissionActivity(org state.Org, actor state.Account, kind string) state.OrgActivity {
	actorID := uuid.MustParse(actor.ID)
	return state.OrgActivity{OrgID: uuid.MustParse(org.ID), Kind: kind, ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actorID, ActorLabel: actor.Email, ResourceType: "invitation", ResourceLabel: "invitation",
		SourceType: kind, SourceID: uuid.NewString(), Data: []byte(`{}`)}
}

func orgAdmissionAccept(ctx context.Context, st state.Store, org state.Org, acct state.Account, hash []byte, activity bool) error {
	if !activity {
		_, _, err := st.ConsumeOrgInvitation(ctx, hash, acct)
		return err
	}
	_, _, _, _, err := st.(state.OrgActivityAccessMutationStore).ConsumeOrgInvitationWithActivity(ctx, hash, acct,
		orgAdmissionActivity(org, acct, "org.invitation.accepted"), orgAdmissionActivity(org, acct, "org.member.added"))
	return err
}

func orgAdmissionInvite(t *testing.T, st state.Store, org state.Org, index int) (state.Account, []byte) {
	t.Helper()
	acct, err := st.CreateAccount(t.Context(), fmt.Sprintf("org-admission-%d@example.com", index), api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	hash := api.HashToken([]byte(fmt.Sprintf("local-org-admission-%d", index)))
	if _, err = st.CreateOrgInvitation(t.Context(), state.OrgInvitation{OrgID: org.ID, Email: acct.Email, Role: state.OrgRoleDeveloper, TokenHash: hash, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return acct, hash
}

func TestOrgInvitationAdmissionRefusesInactiveAndFree(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		for _, activity := range []bool{false, true} {
			t.Run(fmt.Sprintf("postgres=%v/activity=%v", postgres, activity), func(t *testing.T) {
				st, _ := orgAdmissionStore(t, postgres)
				for i, scenario := range []string{"Free downgrade", "pending deletion", "suspended", "past due"} {
					t.Run(scenario, func(t *testing.T) {
						org, err := st.CreateOrg(t.Context(), state.Org{Slug: fmt.Sprintf("admission-%d", i), Name: "Admission", Plan: api.PlanHobby})
						if err != nil {
							t.Fatal(err)
						}
						acct, hash := orgAdmissionInvite(t, st, org, i)
						want := state.ErrOrgInvitationInvalid
						switch scenario {
						case "Free downgrade":
							err = st.UpdateOrgPlan(t.Context(), org.ID, api.PlanFree)
							want = state.ErrOrgMemberCapExceeded
						case "pending deletion":
							err = st.SoftDeleteOrg(t.Context(), org.ID)
						case "suspended":
							err = st.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusSuspended)
						case "past due":
							err = st.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusPastDue)
						}
						if err != nil {
							t.Fatal(err)
						}
						if err = orgAdmissionAccept(t.Context(), st, org, acct, hash, activity); !errors.Is(err, want) {
							t.Fatalf("accept: %v, want %v", err, want)
						}
						inv, err := st.OrgInvitationByTokenHash(t.Context(), hash)
						if err != nil || inv.ConsumedAt != nil {
							t.Fatalf("rejected invitation consumed: %v", err)
						}
						if _, err = st.OrgMemberByAccount(t.Context(), org.ID, acct.ID); !errors.Is(err, state.ErrNotFound) {
							t.Fatalf("rejected member exists: %v", err)
						}
					})
				}
			})
		}
	}
}

func TestOrgInvitationAdmissionSerializesLastSeatPostgres(t *testing.T) {
	for _, activity := range []bool{false, true} {
		t.Run(fmt.Sprintf("activity=%v", activity), func(t *testing.T) {
			st, pool := orgAdmissionStore(t, true)
			ctx := t.Context()
			org, err := st.CreateOrg(ctx, state.Org{Slug: "last-seat", Name: "Last seat", Plan: api.PlanHobby})
			if err != nil {
				t.Fatal(err)
			}
			for i := range 9 {
				acct, err := st.CreateAccount(ctx, fmt.Sprintf("seed-%d@example.com", i), api.PlanHobby)
				if err != nil {
					t.Fatal(err)
				}
				if err = st.AddOrgMember(ctx, org.ID, acct.ID, state.OrgRoleDeveloper, nil); err != nil {
					t.Fatal(err)
				}
			}
			a, h1 := orgAdmissionInvite(t, st, org, 1)
			b, h2 := orgAdmissionInvite(t, st, org, 2)
			// Hold both requests before org admission, then let them contend for
			// the same seat. Unlike timing-only stress, the row lock is explicit.
			gate, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = gate.Rollback(ctx) }()
			if _, err = gate.Exec(ctx, `select id from orgs where id=$1 for no key update`, org.ID); err != nil {
				t.Fatal(err)
			}
			results := make(chan error, 2)
			go func() { results <- orgAdmissionAccept(ctx, st, org, a, h1, activity) }()
			go func() { results <- orgAdmissionAccept(ctx, st, org, b, h2, activity) }()
			orgAdmissionWaitLocks(t, pool, 2)
			if err = gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			accepted, refused := 0, 0
			for range 2 {
				err = <-results
				if err == nil {
					accepted++
				} else if errors.Is(err, state.ErrOrgMemberCapExceeded) {
					refused++
				} else {
					t.Fatalf("accept: %v", err)
				}
			}
			members, err := st.ListOrgMembers(ctx, org.ID)
			if err != nil || accepted != 1 || refused != 1 || len(members) != 10 {
				t.Fatalf("accepted=%d refused=%d members=%d err=%v", accepted, refused, len(members), err)
			}
		})
	}
}

func orgAdmissionWaitLocks(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := pool.QueryRow(t.Context(), `select count(*) from pg_stat_activity where datname=current_database() and wait_event_type='Lock'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %d blocked admission requests", want)
}

func TestSharedOrgCreationEnforcesFreeLimitAtomically(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprintf("postgres=%v", postgres), func(t *testing.T) {
			st, _ := orgAdmissionStore(t, postgres)
			ctx := t.Context()
			owner, err := st.CreateAccount(ctx, "creation-owner@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			creator := st.(state.SharedOrgCreationStore)
			results := make(chan error, 8)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range 8 {
				wg.Go(func() {
					<-start
					_, err := creator.CreateSharedOrg(ctx, owner.ID, fmt.Sprintf("free-%d", i), "Free")
					results <- err
				})
			}
			close(start)
			wg.Wait()
			close(results)
			created := 0
			for err := range results {
				var capErr *state.OrgFreeLimitError
				if err == nil {
					created++
				} else if !errors.As(err, &capErr) || capErr.Owned != 1 {
					t.Fatalf("creation: %v", err)
				}
			}
			orgs, err := st.ListOrgsForAccount(ctx, owner.ID)
			shared := make([]state.Org, 0, len(orgs))
			for _, org := range orgs {
				if !org.Personal {
					shared = append(shared, org)
				}
			}
			orgs = shared
			if err != nil || created != 1 || len(orgs) != 1 {
				t.Fatalf("created=%d orgs=%d err=%v", created, len(orgs), err)
			}
			member, err := st.OrgMemberByAccount(ctx, orgs[0].ID, owner.ID)
			if err != nil || member.Role != state.OrgRoleOwner {
				t.Fatalf("owner not committed: %v", err)
			}
			if err = st.UpdateOrgPlan(ctx, orgs[0].ID, api.PlanHobby); err != nil {
				t.Fatal(err)
			}
			if _, err = creator.CreateSharedOrg(ctx, owner.ID, "after-upgrade", "After upgrade"); err != nil {
				t.Fatalf("creation after upgrade: %v", err)
			}
		})
	}
}

func TestSharedOrgCreationRollsBackIfOwnerInsertFails(t *testing.T) {
	st, pool := orgAdmissionStore(t, true)
	ctx := t.Context()
	owner, err := st.CreateAccount(ctx, "rollback-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `create function fail_shared_owner() returns trigger language plpgsql as $$ begin raise exception 'test owner insert failure'; end $$;
create trigger fail_shared_owner before insert on org_memberships for each row execute function fail_shared_owner()`); err != nil {
		t.Fatal(err)
	}
	creator := st.(state.SharedOrgCreationStore)
	if _, err = creator.CreateSharedOrg(ctx, owner.ID, "atomic-owner", "Atomic"); err == nil {
		t.Fatal("expected owner insertion failure")
	}
	if _, err = st.OrgBySlug(ctx, "atomic-owner"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("orphan organization after failure: %v", err)
	}
	if _, err = pool.Exec(ctx, `drop trigger fail_shared_owner on org_memberships`); err != nil {
		t.Fatal(err)
	}
	if _, err = creator.CreateSharedOrg(ctx, owner.ID, "atomic-owner", "Atomic"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}
