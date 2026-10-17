//go:build !no_pg

package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgApplicationStandardAutomaticOnboarding(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardAutomaticOnboarding(t, s)
}
func TestPgApplicationStandardAutomaticDetach(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardAutomaticDetach(t, s)
}
func TestPgApplicationStandardAutomaticRestoreDefault(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardAutomaticRestoreDefault(t, s)
}
func TestPgApplicationStandardAutomaticCreatorPlan(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardAutomaticCreatorPlan(t, s)
}
func TestPgApplicationStandardAutomaticReviewPrecedence(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardAutomaticReviewPrecedence(t, s)
}

func TestPgApplicationStandardAutomaticLeaseFencing(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardAutomaticLeaseFencing(t, s, func(c ApplicationStandardEnrollmentClaim) {
		if _, err := pool.Exec(context.Background(), `UPDATE app_application_standards SET lease_until=clock_timestamp()-interval '1 second' WHERE app_id=$1`, c.AppID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardAutomaticBlockedRecovery(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	standardAutomaticBlockedRecovery(t, s, func(appID string) {
		if _, err := pool.Exec(context.Background(), `UPDATE app_application_standards SET updated_at=clock_timestamp()-interval '1 minute' WHERE app_id=$1`, appID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardWorkerFairness(t *testing.T) {
	s, _ := standardOperationPGStore(t)
	standardWorkerFairness(t, s)
}

func TestPgApplicationStandardAutomaticLeaseExpiryRollsBack(t *testing.T) {
	s, pool := standardOperationPGStore(t)
	ctx := context.Background()
	f := standardAutomaticSetup(t, s, false, false)
	app := automaticCreate(t, s, f, "automatic-expiring-transaction")
	c, err := s.ClaimApplicationStandardEnrollment(ctx, "expiring-enrollment-worker")
	if err != nil {
		t.Fatal(err)
	}
	// Expire after actual scalar/drain/signer writes, before the transaction's
	// final checkpoint. The caller cannot prolong the storage lease.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION standard_automatic_slow() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='persisted' THEN PERFORM pg_sleep(0.75); END IF; RETURN NEW; END $$;
 CREATE TRIGGER standard_automatic_slow BEFORE UPDATE ON app_application_standards FOR EACH ROW EXECUTE FUNCTION standard_automatic_slow();`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE app_application_standards SET lease_until=clock_timestamp()+interval '0.5 second' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	c.Until = time.Now().Add(time.Hour)
	started := time.Now()
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, c); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatalf("expired transaction installed controls: %v", err)
	}
	if time.Since(started) < 700*time.Millisecond {
		t.Fatal("lease expired before the post-write checkpoint was exercised")
	}
	e, err := s.GetApplicationStandardEnrollment(ctx, f.owner.PersonalOrg.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := s.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	drains, err := s.ListAppLogDrainsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	signers, err := s.ListAppTrustedSignersForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != "pending" || e.PersistedRevision != 0 || e.DesiredRevision != 1 || len(e.MaterializedFields) != 0 || actual.RequireSigned || actual.SecurityPolicy != api.AppSecurityPolicyOff || len(drains) != 0 || len(signers) != 0 {
		t.Fatalf("expired transaction partly committed: %+v %+v drains=%d signers=%d", e, actual, len(drains), len(signers))
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER standard_automatic_slow ON app_application_standards; DROP FUNCTION standard_automatic_slow();`); err != nil {
		t.Fatal(err)
	}
	if repaired := automaticRepair(t, s); repaired.State != "persisted" {
		t.Fatalf("expired work did not resume: %+v", repaired)
	}
}
