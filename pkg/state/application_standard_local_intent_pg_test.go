package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPgApplicationStandardLocalIntent(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardLocalIntentLifecycle(context.Background(), t, s)
}

func TestPgApplicationStandardLocalIntentRefusals(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardLocalIntentRefusals(context.Background(), t, s)
}

func TestPgApplicationStandardLocalIntentConcurrent(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardLocalIntentConcurrent(t.Context(), t, s)
}

func TestPgApplicationStandardLocalIntentAuditAtomicity(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	if _, err := pool.Exec(ctx, `UPDATE app_application_standards SET lease_owner='old-local-worker',lease_generation=3,lease_until=clock_timestamp()+interval '1 minute' WHERE app_id=$1`, f.app.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION refuse_local_standard_intent_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='application_standard.local_intent_changed' THEN RAISE EXCEPTION 'test local intent audit refusal'; END IF; RETURN NEW; END $$;
CREATE TRIGGER refuse_local_standard_intent_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION refuse_local_standard_intent_audit();`); err != nil {
		t.Fatal(err)
	}
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: before.DesiredRevision, Settings: json.RawMessage(`{"egress_extra_ports":[8443]}`)}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); err == nil {
		t.Fatal("audit failure saved local intent")
	}
	got, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
	if err != nil || got.DesiredRevision != before.DesiredRevision || got.State != before.State || !got.UpdatedAt.Equal(before.UpdatedAt) || !standardControlValueEqual(got.LocalSettings, before.LocalSettings) {
		t.Fatalf("audit failure changed intent: %+v %v", got, err)
	}
	var owner string
	var generation int64
	var until *time.Time
	if err := pool.QueryRow(ctx, `SELECT lease_owner,lease_generation,lease_until FROM app_application_standards WHERE app_id=$1`, f.app.ID).Scan(&owner, &generation, &until); err != nil {
		t.Fatal(err)
	}
	if owner != "old-local-worker" || generation != 3 || until == nil {
		t.Fatalf("audit failure revoked authority: %s %d %v", owner, generation, until)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_local_standard_intent_audit ON audit_log`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, ApplicationStandardEnrollmentClaim{AppID: f.app.ID, OrgID: f.owner.PersonalOrg.ID, Owner: owner, Generation: generation, DesiredRevision: before.DesiredRevision}); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("old worker changed new intent: %v", err)
	}
}

func TestPgApplicationStandardLocalIntentContention(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := t.Context()
	f := newStandardLocalIntentFixture(ctx, t, s)
	r := ApplicationStandardLocalIntentRequest{ExpectedRevision: f.enrollment.DesiredRevision, Settings: json.RawMessage(`{"egress_extra_ports":[8443]}`)}
	for _, tc := range []struct{ name, query, id string }{
		{"organization", `SELECT id FROM orgs WHERE id=$1 FOR UPDATE`, f.owner.PersonalOrg.ID},
		{"actor", `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, f.owner.Account.ID},
		{"membership", `SELECT account_id FROM org_memberships WHERE account_id=$1 FOR UPDATE`, f.owner.Account.ID},
		{"application", `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, f.app.ID},
		{"enrollment", `SELECT app_id FROM app_application_standards WHERE app_id=$1 FOR UPDATE`, f.app.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, tc.query, tc.id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetApplicationStandardLocalIntent(ctx, f.owner.PersonalOrg.ID, f.owner.Account.ID, f.app.ID, r); !errors.Is(err, ErrApplicationStandardReviewBusy) {
				t.Fatalf("contended local intent: %v", err)
			}
			got, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, f.app.ID)
			if err != nil || got.DesiredRevision != f.enrollment.DesiredRevision || !got.UpdatedAt.Equal(f.enrollment.UpdatedAt) {
				t.Fatalf("contended intent changed enrollment: %+v %v", got, err)
			}
		})
	}
}
