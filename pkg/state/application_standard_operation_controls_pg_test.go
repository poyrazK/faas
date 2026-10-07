package state

import (
	"context"
	"errors"
	"testing"
)

func TestPgApplicationStandardOperationControls(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardOperationControlsLifecycle(t, s)
}

func TestPgApplicationStandardOperationControlAuthorization(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardOperationControlAuthorization(t, s)
}

func TestPgApplicationStandardOperationReviewedRollback(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardOperationReviewedRollback(t, s)
}

func TestPgApplicationStandardOperationControlContention(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	o, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sql, id string
	}{
		{"organization", "SELECT id FROM orgs WHERE id=$1 FOR UPDATE", o.OrgID},
		{"actor", "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", f.owner.Account.ID},
		{"membership", "SELECT account_id FROM org_memberships WHERE account_id=$1 FOR UPDATE", f.owner.Account.ID},
		{"operation", "SELECT id FROM application_standard_operations WHERE id=$1 FOR UPDATE", o.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, tc.sql, tc.id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationPause); !errors.Is(err, ErrApplicationStandardReviewBusy) {
				t.Fatalf("contended control waited or bypassed locks: %v", err)
			}
			if got := standardReadOperation(ctx, t, s, o); !standardControlOperationEqual(got, o) {
				t.Fatal("contended control changed intent")
			}
			standardOperationControlAuditCount(ctx, t, s, 0)
		})
	}
	standardControlOperation(t, s, o, f.owner.Account.ID, ApplicationStandardOperationPause, "paused")
}

func TestPgApplicationStandardOperationControlAuditAtomicity(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := context.Background()
	f := newStandardApprovalFixture(t, s)
	o, err := s.ApproveApplicationStandardReview(ctx, f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(ctx, "audit-failure-worker")
	if err != nil {
		t.Fatal(err)
	}
	o = standardReadOperation(ctx, t, s, o)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION refuse_standard_operation_control_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind LIKE 'application_standard.operation_%' THEN RAISE EXCEPTION 'test audit refusal'; END IF; RETURN NEW; END $$;
CREATE TRIGGER refuse_standard_operation_control_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION refuse_standard_operation_control_audit();`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ControlApplicationStandardOperation(ctx, o.OrgID, f.owner.Account.ID, o.ID, o.UpdatedAt, ApplicationStandardOperationAbort); err == nil {
		t.Fatal("audit failure committed operator control")
	}
	if got := standardReadOperation(ctx, t, s, o); !standardControlOperationEqual(got, o) {
		t.Fatal("audit failure left aborted intent or skipped targets")
	}
	standardOperationControlAuditCount(ctx, t, s, 0)
	got, err := s.MaterializeNextApplicationStandardTarget(ctx, c)
	if err != nil || got.Targets[0].State != "persisted" || got.Targets[1].State != "queued" {
		t.Fatalf("failed control revoked the worker lease: %+v %v", got, err)
	}
}
