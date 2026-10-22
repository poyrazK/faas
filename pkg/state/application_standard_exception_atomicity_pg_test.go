package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgApplicationStandardExceptionAuditAtomicity(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	r := state.StandardExceptionRequest(f)
	if _, err := pool.Exec(ctx, `UPDATE app_application_standards SET lease_owner='exception-old-worker',lease_generation=3,lease_until=clock_timestamp()+interval '1 minute' WHERE app_id=$1`, (*f.App()).ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION refuse_standard_exception_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind LIKE 'application_standard.exception_%' THEN RAISE EXCEPTION 'test exception audit refusal'; END IF; RETURN NEW; END $$;
CREATE TRIGGER refuse_standard_exception_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION refuse_standard_exception_audit();`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveApplicationStandardException(ctx, (*f.Owner()).PersonalOrg.ID, (*f.Owner()).Account.ID, (*f.App()).ID, r); err == nil {
		t.Fatal("approval escaped failed audit")
	}
	history, err := s.ListApplicationStandardExceptions(ctx, (*f.Owner()).PersonalOrg.ID, (*f.App()).ID, "")
	if err != nil || len(history) != 0 {
		t.Fatalf("failed audit left approval: %+v %v", history, err)
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, (*f.Owner()).PersonalOrg.ID, (*f.App()).ID)
	if err != nil || e.DesiredRevision != (*f.Enrollment()).DesiredRevision || e.State != "persisted" {
		t.Fatalf("failed audit changed enrollment: %+v %v", e, err)
	}
	var generation int64
	var owner string
	if err := pool.QueryRow(ctx, `SELECT lease_generation,lease_owner FROM app_application_standards WHERE app_id=$1`, (*f.App()).ID).Scan(&generation, &owner); err != nil {
		t.Fatal(err)
	}
	if generation != 3 || owner != "exception-old-worker" {
		t.Fatal("failed audit revoked old authority")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_standard_exception_audit ON audit_log`); err != nil {
		t.Fatal(err)
	}
	x, err := s.ApproveApplicationStandardException(ctx, (*f.Owner()).PersonalOrg.ID, (*f.Owner()).Account.ID, (*f.App()).ID, r)
	if err != nil {
		t.Fatal(err)
	}
	(*f.Enrollment()) = state.StandardLocalIntentMaterialize(ctx, t, s, f)
	if _, err := pool.Exec(ctx, `CREATE TRIGGER refuse_standard_exception_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION refuse_standard_exception_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeApplicationStandardException(ctx, x.OrgID, (*f.Owner()).Account.ID, x.AppID, x.ID, (*f.Enrollment()).DesiredRevision); err == nil {
		t.Fatal("revocation escaped failed audit")
	}
	history, err = s.ListApplicationStandardExceptions(ctx, x.OrgID, x.AppID, "")
	if err != nil || len(history) != 1 || history[0].RevokedAt != nil {
		t.Fatalf("failed audit revoked approval: %+v %v", history, err)
	}
}

func TestPgApplicationStandardExceptionExpiryAuditAtomicity(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	r := state.StandardExceptionRequest(f)
	r.ExpiresAt = time.Now().UTC().Add(2 * time.Second)
	x, err := s.ApproveApplicationStandardException(ctx, (*f.Owner()).PersonalOrg.ID, (*f.Owner()).Account.ID, (*f.App()).ID, r)
	if err != nil {
		t.Fatal(err)
	}
	(*f.Enrollment()) = state.StandardLocalIntentMaterialize(ctx, t, s, f)
	state.WaitStandardExceptionExpiry(t, x)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION refuse_standard_exception_expiry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='application_standard.exception_expiry_queued' THEN RAISE EXCEPTION 'test expiry audit refusal'; END IF; RETURN NEW; END $$;
CREATE TRIGGER refuse_standard_exception_expiry BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION refuse_standard_exception_expiry();`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimApplicationStandardEnrollment(ctx, "expiry-audit-worker"); err == nil {
		t.Fatal("expiry escaped failed audit")
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, x.OrgID, x.AppID)
	if err != nil || e.DesiredRevision != (*f.Enrollment()).DesiredRevision || e.State != "persisted" || state.ApplicationStandardEnrollmentPermitsRuntime((*f.App()), e) {
		t.Fatalf("audit failure extended authority or partially queued: %+v %v", e, err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_standard_exception_expiry ON audit_log`); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardEnrollment(ctx, "expiry-audit-recovery")
	if err != nil || c.DesiredRevision != e.DesiredRevision+1 {
		t.Fatalf("expiry retry: %+v %v", c, err)
	}
}

func TestPgApplicationStandardExceptionImmutableHistory(t *testing.T) {
	s, pool := state.StandardOperationPGStore(t)
	ctx := t.Context()
	f := state.NewStandardLocalIntentFixture(ctx, t, s)
	x, err := s.ApproveApplicationStandardException(ctx, (*f.Owner()).PersonalOrg.ID, (*f.Owner()).Account.ID, (*f.App()).ID, state.StandardExceptionRequest(f))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE application_standard_exceptions SET reason='rewritten' WHERE id=$1`, `UPDATE application_standard_exceptions SET expires_at=expires_at+interval '1 minute' WHERE id=$1`, `DELETE FROM application_standard_exceptions WHERE id=$1`} {
		_, err := pool.Exec(ctx, query, x.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "application_standard_exception_immutable" {
			t.Fatalf("immutable history: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id=$1`, (*f.App()).ID); err != nil {
		t.Fatalf("application erasure blocked: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM application_standard_exceptions WHERE id=$1`, x.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("erasure retained history: %d %v", count, err)
	}
}
