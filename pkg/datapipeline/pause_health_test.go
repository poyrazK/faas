package datapipeline

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPauseHealthAndAlertsRequireWorkerAcknowledgement(t *testing.T) {
	_, store, p, monitor := alertFixture(t)
	ctx := t.Context()
	alertLive(t, store, p)
	paused, err := store.Control(ctx, p.AccountID, p.ID, p.Revision, "pause")
	if err != nil || paused.State != "live" || paused.ObservationFresh || paused.Health.Code != "pause_pending" {
		t.Fatal("pause intent claimed completion", paused, err)
	}
	if code, _ := alertCondition(paused, time.Now().Add(5*time.Minute)); code != "monitoring_stale" {
		t.Fatal("stalled pause suppressed monitoring", code)
	}
	claim, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	ob := (&Engine{}).Reconcile(ctx, claim, "", nil)
	if err = store.Finish(ctx, claim, ob); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || !failed.ObservationFresh || failed.State != "blocked" || failed.Health.Code != "pause_failed" {
		t.Fatal("failed pause reported successful", failed, err)
	}
	alertDue(t, ctx, monitor, failed, false)
	alertDue(t, ctx, monitor, failed, true)
	history := alertHistory(t, ctx, store, failed)
	if len(history) != 1 || history[0].Code != "sync_blocked" {
		t.Fatal("failed pause did not raise an incident", history)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET state='paused',observed_at=clock_timestamp(),observed_revision=revision WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	confirmed, err := store.Get(ctx, p.AccountID, p.ID)
	if err != nil || confirmed.Health.Code != "paused" {
		t.Fatal("current pause acknowledgement not reflected", confirmed, err)
	}
	alertDue(t, ctx, monitor, confirmed, false)
	alertDue(t, ctx, monitor, confirmed, true)
	if len(alertHistory(t, ctx, store, p)) != 1 {
		t.Fatal("confirmed pause emitted a recovery")
	}
	if code, recovered := alertCondition(confirmed, time.Now().Add(5*time.Minute)); code != "" || recovered {
		t.Fatal("confirmed intentional pause reported an incident or recovery", code, recovered)
	}
}
