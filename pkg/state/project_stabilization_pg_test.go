//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
)

func TestProjectStabilizationPG(t *testing.T) {
	testProjectStabilization(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestProjectStabilizationRestoreTransactionPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := NewPgStore(pool)
	f := newCanaryStoreFixture(t, store)
	enableStabilizationFixture(t, f)
	for range 6 {
		f.advance(t, false)
	}
	f.advance(t, true)
	round := f.seedRound(t)
	round.Evidence[0].CandidateErrors = 1
	before, err := store.ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `CREATE FUNCTION reject_monitor_restore() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.status='rolled_back' AND NEW.policy->>'stabilizing'='true' THEN RAISE EXCEPTION 'injected monitor checkpoint failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_monitor_restore BEFORE UPDATE ON project_release_canaries FOR EACH ROW EXECUTE FUNCTION reject_monitor_restore()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err == nil {
		t.Fatal("failed monitor write accepted")
	}
	safe := f.progress(t)
	active, err := store.ActiveProjectReleaseSet(t.Context(), f.account, f.project, "production")
	if err != nil || active.ID != safe.ReleaseID {
		t.Fatalf("failed monitor restore changed graph: %+v %v", active, err)
	}
	head, err := store.ProjectEnvironmentConfigLatest(t.Context(), f.account, f.project, "production")
	if err != nil || head.Version != f.previous.Version+1 {
		t.Fatalf("failed monitor restore changed config: %+v %v", head, err)
	}
	after, err := store.ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	if err != nil || after.Generation != before.Generation || safe.Verification.Stabilization.Status != "monitoring" {
		t.Fatal("failed restore lost checkpoint")
	}
	if _, err = pool.Exec(t.Context(), "DROP TRIGGER reject_monitor_restore ON project_release_canaries; DROP FUNCTION reject_monitor_restore()"); err != nil {
		t.Fatal(err)
	}
	store = NewPgStore(pool)
	if _, err = store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err != nil {
		t.Fatal(err)
	}
	head, err = store.ProjectEnvironmentConfigLatest(t.Context(), f.account, f.project, "production")
	if err != nil || head.Version != f.previous.Version+2 {
		t.Fatalf("recovered monitor restore wrote twice: %+v %v", head, err)
	}
}
