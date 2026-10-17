//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresOrgAppQuotaOwnershipMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	source, err := migrations.FS.ReadFile("20261018003100002_org_app_quota_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("ownership migration has no rollback")
	}
	if _, err := pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	// Seed a pre-attribution row directly. Current Go writers fill OrgID, and
	// the existing application-standard guard forbids clearing a saved owner.
	personal := state.App{}
	if err := pool.QueryRow(t.Context(), `INSERT INTO apps (account_id, slug, type, ram_mb, max_concurrency)
		VALUES ($1, 'legacy-personal-quota', 'app', 128, 1) RETURNING id`, f.actor.ID).Scan(&personal.ID); err != nil {
		t.Fatal(err)
	}
	var unattributed bool
	if err := pool.QueryRow(t.Context(), "SELECT org_id IS NULL FROM apps WHERE id = $1", personal.ID).Scan(&unattributed); err != nil || !unattributed {
		t.Fatalf("legacy fixture is not unattributed: %v %v", unattributed, err)
	}

	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatalf("apply actual ownership migration: %v", err)
	}
	personalOrg, err := store.OrgByPersonalAccount(t.Context(), f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOwnership := func() {
		t.Helper()
		shared, err := store.AppByID(t.Context(), f.app.ID)
		if err != nil || shared.OrgID != f.org.ID {
			t.Fatalf("migration reassigned company app: %+v %v", shared, err)
		}
		kept, err := store.AppByID(t.Context(), personal.ID)
		if err != nil || kept.OrgID != personalOrg.ID {
			t.Fatalf("personal backfill missing: %+v %v", kept, err)
		}
	}
	assertOwnership()
	created, err := store.CreateApp(t.Context(), state.App{AccountID: f.actor.ID, Slug: "new-personal-attribution"})
	if err != nil || created.OrgID != personalOrg.ID {
		t.Fatalf("new app missing personal attribution: %+v %v", created, err)
	}
	// Rollback does not erase attribution that quota and authorization now use.
	if _, err := pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	assertOwnership()
	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatalf("migration cannot be reapplied after rollback: %v", err)
	}
}

func TestPostgresOrgAppQuota(t *testing.T) {
	orgAppQuotaSuite(t, func(t *testing.T) state.Store {
		t.Helper()
		return state.NewPgStore(pgtest.OpenMigrated(t))
	})
}

func TestPostgresOrgAppQuotaConcurrentPlanChange(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed downgrade", true: "rolled back downgrade"}[rollback], func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			f := setupOffboarding(t, store)
			fillOrgAppQuota(t, f)
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			if _, err := tx.Exec(t.Context(), "UPDATE orgs SET plan = 'hobby' WHERE id = $1", f.org.ID); err != nil {
				t.Fatal(err)
			}
			var pid int32
			if err := tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				_, err := store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.owner.ID, OrgID: f.org.ID, Slug: "after-plan-change"}, api.MustLimitsFor(api.PlanScale))
				result <- err
			}()
			waitForOffboardingBlock(t, pool, pid)
			if rollback {
				err = tx.Rollback(t.Context())
			} else {
				err = tx.Commit(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if rollback {
					if err != nil {
						t.Fatalf("rolled-back plan changed admission: %v", err)
					}
				} else {
					assertOrgAppQuota(t, err, api.PlanHobby, api.MustLimitsFor(api.PlanHobby).DeployedApps, state.QuotaErrorKindApps)
					if _, err := store.AppBySlug(t.Context(), "after-plan-change"); !errors.Is(err, state.ErrNotFound) {
						t.Fatalf("over-cap app committed after downgrade: %v", err)
					}
				}
			case <-time.After(10 * time.Second):
				t.Fatal("admission did not resume after plan transaction")
			}
		})
	}
}

func TestPostgresOrgAppQuotaAdmissionBeforePlanChange(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	f := setupOffboarding(t, store)
	fillOrgAppQuota(t, f)
	// Pause the real insert after quota admission, before the existing
	// application-standard guard takes its organization row SHARE lock.
	if _, err := pool.Exec(t.Context(), `
		CREATE FUNCTION pause_org_app_quota_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
		  PERFORM pg_advisory_xact_lock(1347001::bigint);
		  RETURN NEW;
		END $$;
		CREATE TRIGGER app_quota_pause BEFORE INSERT ON apps FOR EACH ROW
		WHEN (NEW.slug = 'reservation-before-plan') EXECUTE FUNCTION pause_org_app_quota_test();`); err != nil {
		t.Fatal(err)
	}
	pause, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pause.Rollback(t.Context()) }()
	if _, err := pause.Exec(t.Context(), "SELECT pg_advisory_xact_lock(1347001::bigint)"); err != nil {
		t.Fatal(err)
	}
	var pausePID int32
	if err := pause.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pausePID); err != nil {
		t.Fatal(err)
	}
	created := make(chan error, 1)
	go func() {
		_, err := store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.owner.ID, OrgID: f.org.ID, Slug: "reservation-before-plan"}, api.MustLimitsFor(api.PlanScale))
		created <- err
	}()
	waitForOffboardingBlock(t, pool, pausePID)
	var creatorPID int32
	if err := pool.QueryRow(t.Context(), "SELECT pid FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))", pausePID).Scan(&creatorPID); err != nil {
		t.Fatal(err)
	}
	changed := make(chan error, 1)
	go func() { changed <- store.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanHobby) }()
	waitForOffboardingBlock(t, pool, creatorPID)
	if err := pause.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]<-chan error{"create": created, "plan change": changed} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("%s failed in admission-before-plan order: %v", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not finish", name)
		}
	}
	// The accepted reservation survives a later downgrade; new reservations
	// use the smaller plan and cannot grow an already-over-cap organization.
	_, err = store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "after-admitted-downgrade"}, api.MustLimitsFor(api.PlanScale))
	var quota *state.QuotaError
	limit := api.MustLimitsFor(api.PlanHobby).DeployedApps
	if !errors.As(err, &quota) || quota.Plan != api.PlanHobby || quota.Limit != limit || quota.Observed != limit+1 {
		t.Fatalf("post-downgrade admission: %+v", err)
	}
}
