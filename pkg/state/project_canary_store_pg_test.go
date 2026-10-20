//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestProjectCanaryStorePG(t *testing.T) {
	testProjectCanaryStore(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestProjectCanaryWorkersAndPinnedRetryPG(t *testing.T) {
	testProjectCanaryWorkersAndPinnedRetry(t, NewPgStore(pgtest.OpenMigrated(t)))
}

func TestProjectCanaryActivationRollsBackEveryCheckpointOnWriteFailure(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := NewPgStore(pool)
	f := newCanaryStoreFixture(t, store)
	f.advance(t, false)
	f.advance(t, false)
	f.advance(t, false)
	round := f.seedRound(t)
	before, err := store.ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `CREATE FUNCTION reject_canary_activation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF (NEW.policy->>'percent')::integer=100 THEN RAISE EXCEPTION 'injected canary checkpoint failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_canary_activation BEFORE UPDATE ON project_release_canaries FOR EACH ROW EXECUTE FUNCTION reject_canary_activation()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err == nil {
		t.Fatal("injected checkpoint failure accepted")
	}
	active, err := store.ActiveProjectReleaseSet(t.Context(), f.account, f.project, "production")
	if err != nil || active.ID != f.baseline {
		t.Fatalf("failed activation changed graph: %+v %v", active, err)
	}
	head, err := store.ProjectEnvironmentConfigLatest(t.Context(), f.account, f.project, "production")
	if err != nil || head.Version != f.previous.Version {
		t.Fatalf("failed activation changed config: %+v %v", head, err)
	}
	after, err := store.ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	if err != nil || after.Generation != before.Generation || after.Percent != before.Percent {
		t.Fatalf("failed activation changed checkpoint: %+v %v", after, err)
	}
	if _, err = pool.Exec(t.Context(), "DROP TRIGGER reject_canary_activation ON project_release_canaries; DROP FUNCTION reject_canary_activation()"); err != nil {
		t.Fatal(err)
	}
	store = NewPgStore(pool)
	safe, err := store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round)
	if err != nil || safe.Verification.Percent != 100 {
		t.Fatalf("recovery failed: %+v %v", safe, err)
	}
	if _, err = store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err != nil {
		t.Fatal(err)
	}
	head, err = store.ProjectEnvironmentConfigLatest(t.Context(), f.account, f.project, "production")
	if err != nil || head.Version != f.previous.Version+1 {
		t.Fatalf("lost-response retry wrote configuration twice: %+v %v", head, err)
	}
}
