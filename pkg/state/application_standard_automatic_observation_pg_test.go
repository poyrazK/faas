package state

import (
	"errors"
	"testing"
	"time"
)

func TestPgApplicationStandardAutomaticObservationLifecycle(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	automaticObservationLifecycle(t, s, func(id string) {
		if _, err := pool.Exec(t.Context(), `UPDATE app_application_standards SET observation_checked_at=clock_timestamp()-interval '1 minute' WHERE app_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardAutomaticObservationReviewPrecedence(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	automaticObservationReviewPrecedence(t, s)
}

func TestPgApplicationStandardAutomaticObservationFairness(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	automaticObservationFairness(t, s)
}

func TestPgApplicationStandardAutomaticObservationNewRevision(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	automaticObservationNewRevision(t, s)
}

func TestPgApplicationStandardAutomaticObservationLateReview(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	automaticObservationLateReview(t, s)
}

func TestPgApplicationStandardAutomaticObservationLeaseFence(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	automaticObservationLeaseFence(t, s, func(c ApplicationStandardEnrollmentClaim) {
		if _, err := pool.Exec(t.Context(), `UPDATE app_application_standards SET lease_until=clock_timestamp()-interval '1 second' WHERE app_id=$1`, c.AppID); err != nil {
			t.Fatal(err)
		}
	}, func(id string) {
		if _, err := pool.Exec(t.Context(), `UPDATE app_application_standards SET observation_checked_at=clock_timestamp()-interval '1 minute' WHERE app_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardAutomaticObservationLateCheckpoint(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	app, _ := automaticObservationFixture(t, s)
	// Make the checkpoint positive, then delay the actual row write. Its final
	// check must roll it back even though the SQL WHERE predicate saw a live lease.
	standardObservationLoadLogging(t, s, app)
	target := standardEgressPending(t, s, app.ID)
	if _, err := s.RecordApplicationStandardEgress(t.Context(), target, standardEgressReceipt(target)); err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardObservation(t.Context(), "slow-observer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `CREATE FUNCTION standard_observation_slow() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='observed' THEN PERFORM pg_sleep(0.75); END IF; RETURN NEW; END $$;
 CREATE TRIGGER standard_observation_slow BEFORE UPDATE ON app_application_standards FOR EACH ROW EXECUTE FUNCTION standard_observation_slow();`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE app_application_standards SET lease_until=clock_timestamp()+interval '0.5 second' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	c.Until = time.Now().Add(time.Hour)
	started := time.Now()
	if _, err := s.ObserveApplicationStandardEnrollment(t.Context(), c); !errors.Is(err, ErrApplicationStandardLeaseLost) {
		t.Fatal("slow checkpoint outlived storage authority", err)
	}
	if time.Since(started) < 700*time.Millisecond {
		t.Fatal("test did not delay the write")
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || e.State != "persisted" || e.ObservedRevision != 0 || e.ErrorCode != "" {
		t.Fatal("expired positive checkpoint did not roll back", e, err)
	}
}
