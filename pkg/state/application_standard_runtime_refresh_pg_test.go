//go:build !no_pg

package state

import (
	"errors"
	"testing"
)

func TestPgApplicationStandardRuntimeRefresh(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardRuntimeRefreshLifecycle(t, s, func(id string) Snapshot {
		var snap Snapshot
		if err := pool.QueryRow(t.Context(), `SELECT id::text,deployment_id::text,storage_key,stale FROM snapshots WHERE id=$1`, id).Scan(&snap.ID, &snap.DeploymentID, &snap.StorageKey, &snap.Stale); err != nil {
			t.Fatal(err)
		}
		return snap
	})
}

func TestPgApplicationStandardRuntimeRefreshAtomicHandoffFailure(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f, snaps := standardRuntimeRefreshFixture(t, s)
	o, err := s.ApproveApplicationStandardReview(t.Context(), f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_standard_handoff() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.channel='runtime_config_restart' AND NEW.payload::jsonb ? 'application_standard' THEN RAISE EXCEPTION 'injected handoff failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_standard_handoff BEFORE INSERT ON notification_outbox FOR EACH ROW EXECUTE FUNCTION reject_standard_handoff();`); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "handoff-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(t.Context(), c); err == nil {
		t.Fatal("injected handoff failure was ignored")
	}
	appID := o.Targets[0].AppID
	e, err := s.GetApplicationStandardEnrollment(t.Context(), o.OrgID, appID)
	if err != nil || e.PersistedRevision != 0 {
		t.Fatal("installation committed without scheduler handoff", e, err)
	}
	var stale bool
	if err := pool.QueryRow(t.Context(), `SELECT stale FROM snapshots WHERE id=$1`, snaps[appID].ID).Scan(&stale); err != nil || stale {
		t.Fatal("cache invalidation escaped rolled back install", stale, err)
	}
	if _, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), o.OrgID, appID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed install left runtime work", err)
	}
	o, err = s.GetApplicationStandardOperation(t.Context(), o.OrgID, o.ID)
	if err != nil || o.Targets[0].State != "queued" {
		t.Fatal("failed handoff advanced rollout target", o, err)
	}
}
