//go:build !no_pg

package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPgApplicationStandardMaterialization(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardMaterializationLifecycle(t, s, func(id string) {
		if _, err := pool.Exec(context.Background(), `UPDATE application_standard_operations SET state='completed' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	})
}
func TestPgApplicationStandardMaterializationWaveGate(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardMaterializationWaveGate(t, s)
}
func TestPgApplicationStandardMaterializationStale(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardMaterializationStale(t, s)
}
func TestPgApplicationStandardMaterializationLeaseFencing(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	if _, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	old, err := s.ClaimApplicationStandardOperation(ctx, "dead-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE application_standard_operations SET lease_until=now()-interval '1 second' WHERE id=$1`, old.OperationID); err != nil {
		t.Fatal(err)
	}
	current, err := s.ClaimApplicationStandardOperation(ctx, "replacement-worker")
	if err != nil {
		t.Fatal(err)
	}
	if current.Generation != old.Generation+1 {
		t.Fatal("restart did not advance fencing generation")
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, old); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("old generation wrote: %v", err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, current); err != nil {
		t.Fatal(err)
	}
}

func TestPgApplicationStandardMaterializationAtomicFailure(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	o, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	// The intent and scalar writes have already run when persistence fails.
	// They must roll back together with the target and durable worker checkpoint.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION standard_projection_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='persisted' THEN RAISE EXCEPTION 'injected persistence failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER standard_projection_fail BEFORE UPDATE ON app_application_standards FOR EACH ROW EXECUTE FUNCTION standard_projection_fail();`); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "atomic-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, c); err == nil {
		t.Fatal("injected failure did not fail installation")
	}
	actual, err := s.AppByID(ctx, o.Targets[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.plan.OrgID, o.Targets[0].AppID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.GetApplicationStandardOperation(ctx, f.plan.OrgID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.SecurityPolicy != "off" || e.DesiredRevision != 1 || e.PersistedRevision != 0 || len(e.Adoptions) != 0 || after.Targets[0].State != "queued" || after.State != "queued" {
		t.Fatalf("partial installation survived: %+v %+v %+v", actual, e, after)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER standard_projection_fail ON app_application_standards; DROP FUNCTION standard_projection_fail();`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, c); err != nil {
		t.Fatal(err)
	}
}

func TestPgApplicationStandardMaterializationRawControlGuards(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	if _, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "guard-worker")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET security_policy='off' WHERE id=$1`, o.Targets[0].AppID); !errors.Is(mapErr(err), ErrApplicationStandardManagedControl) {
		t.Fatalf("raw SQL bypassed standard: %v", err)
	}
}

func TestPgApplicationStandardMaterializationChildRowContention(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	f := newStandardApprovalFixture(t, s)
	ctx := context.Background()
	d, err := s.CreateAppLogDrain(ctx, AppLogDrain{AppID: f.plan.Applications[0].AppID, AccountID: f.owner.Account.ID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://contention.example.com/logs", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewApplicationStandardAssignment(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.Request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardReview(ctx, p.OrgID, f.owner.Account.ID, p.ID, p.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "contended-worker")
	if err != nil {
		t.Fatal(err)
	}
	child, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Rollback(ctx)
	if _, err := child.Exec(ctx, `SELECT 1 FROM app_log_drains WHERE id=$1 FOR UPDATE`, d.ID); err != nil {
		t.Fatal(err)
	}
	attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := s.MaterializeNextApplicationStandardTarget(attempt, c); !errors.Is(err, ErrApplicationStandardReviewBusy) {
		t.Fatalf("materializer waited on the child row while holding its fence: %v", err)
	}
	if err := child.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeNextApplicationStandardTarget(ctx, c); err != nil {
		t.Fatal(err)
	}
}
