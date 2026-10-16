//go:build !no_pg

package state

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestProjectCanaryControlPG(t *testing.T) {
	testProjectCanaryControl(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestProjectCanaryControlRestoreTransactionPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := NewPgStore(pool)
	f := newCanaryStoreFixture(t, store)
	for range 4 {
		f.advance(t, false)
	}
	safe := f.progress(t)
	req := api.ProjectReleaseControlRequest{RequestID: strings.Repeat("a", 32), ExpectedGeneration: safe.Verification.Generation}
	if _, err := store.ControlProjectCanary(t.Context(), f.account, f.attempt, "abort", req); err != nil {
		t.Fatal(err)
	}
	round := f.seedRound(t)
	before, _ := store.ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_control_restore() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.policy#>>'{control,status}'='restored' THEN RAISE EXCEPTION 'injected restore checkpoint failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_control_restore BEFORE UPDATE ON project_release_canaries FOR EACH ROW EXECUTE FUNCTION reject_control_restore()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err == nil {
		t.Fatal("injected restore write failure accepted")
	}
	active, err := store.ActiveProjectReleaseSet(t.Context(), f.account, f.project, "production")
	if err != nil || active.ID != safe.ReleaseID {
		t.Fatalf("failed restore changed graph: %+v %v", active, err)
	}
	head, err := store.ProjectEnvironmentConfigLatest(t.Context(), f.account, f.project, "production")
	if err != nil || head.Version != f.previous.Version+1 {
		t.Fatalf("failed restore changed config: %+v %v", head, err)
	}
	after, err := store.ProjectCanaryCheckpoint(t.Context(), f.account, f.attempt)
	if err != nil || after.Generation != before.Generation || after.Control.Status != "pending" {
		t.Fatalf("failed restore changed decision: %+v %v", after, err)
	}
	if _, err = pool.Exec(t.Context(), "DROP TRIGGER reject_control_restore ON project_release_canaries; DROP FUNCTION reject_control_restore()"); err != nil {
		t.Fatal(err)
	}
	store = NewPgStore(pool)
	if _, err = store.AdvanceProjectReleaseVerification(t.Context(), f.account, f.attempt, &round); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ControlProjectCanary(t.Context(), f.account, f.attempt, "abort", req); err != nil {
		t.Fatal(err)
	}
	head, err = store.ProjectEnvironmentConfigLatest(t.Context(), f.account, f.project, "production")
	if err != nil || head.Version != f.previous.Version+2 {
		t.Fatalf("replayed decision duplicated config restore: %+v %v", head, err)
	}
}
