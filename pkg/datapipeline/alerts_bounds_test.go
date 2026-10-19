package datapipeline

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/alertdelivery"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func TestPipelineAlertFullQueueDefersTransitionWithoutBusyLoop(t *testing.T) {
	_, store, p, w := alertFixture(t)
	alertDue(t, t.Context(), w, p, false)
	var oldest string
	for i := range api.DataPipelineAlertPendingMax {
		id := uuid.NewString()
		if i == 0 {
			oldest = id
		}
		err := store.q.PipelineAlertEnqueue(t.Context(), store.pool, sqlc.PipelineAlertEnqueueParams{ID: id, PipelineID: p.ID, AccountID: p.AccountID, ConfigRevision: 1, IncidentID: uuid.NewString(), Kind: "incident", Code: "sync_blocked", OccurredAt: alertTime(time.Now())})
		if err != nil {
			t.Fatal(err)
		}
	}
	alertDue(t, t.Context(), w, p, true)
	a, err := store.q.PipelineAlertGet(t.Context(), store.pool, sqlc.PipelineAlertGetParams{AccountID: p.AccountID, PipelineID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if a.IncidentID != "" || a.CandidateCode != "sync_blocked" || !a.CheckAt.Time.After(time.Now()) {
		t.Fatal("full queue lost deferred state", a.IncidentID, a.CandidateCode, a.CheckAt)
	}
	if more, err := w.CheckTick(t.Context()); more || err != nil {
		t.Fatal("full queue starves later checks", more, err)
	}
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_alert_deliveries SET status='delivered' WHERE id=$1`, oldest); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, true)
	pending, err := store.q.PipelineAlertPendingCount(t.Context(), store.pool, p.ID)
	if err != nil || pending != api.DataPipelineAlertPendingMax {
		t.Fatal("deferred event not queued after space recovered", pending, err)
	}
	a, err = store.q.PipelineAlertGet(t.Context(), store.pool, sqlc.PipelineAlertGetParams{AccountID: p.AccountID, PipelineID: p.ID})
	if err != nil || a.IncidentID == "" {
		t.Fatal("deferred incident not recorded", err)
	}
}

func TestPipelineAlertSlowReceiverDoesNotStopMonitor(t *testing.T) {
	_, store, p, w := alertFixture(t)
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	started := make(chan struct{})
	w.Sender = alertdelivery.New(&http.Client{Transport: alertRoundTripper(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}, w.Log)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not start")
	}
	alertLive(t, store, p)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_alerts SET check_at=clock_timestamp(),candidate_code='recovered',candidate_since=clock_timestamp()-interval '1 minute' WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(alertHistory(t, t.Context(), store, p)) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("receiver blocked independent monitoring")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alert worker did not stop")
	}
}

func TestPipelineAlertDefaultTransportRejectsPrivateDNS(t *testing.T) {
	s, store, p, w := alertFixture(t)
	if err := s.ConfigureHealthAlerts(t.Context(), p.AccountID, p.ID, 1, true, "https://localhost/hook", "private-destination-signing-secret"); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	if more, err := w.DeliveryTick(t.Context()); !more || err != nil {
		t.Fatal(more, err)
	}
	h := alertHistory(t, t.Context(), store, p)
	if len(h) != 1 || h[0].Status != "dead" || h[0].Result != "receiver_rejected" || h[0].ResponseCode != 0 {
		t.Fatal("DNS private address bypassed default egress guard", h)
	}
}

func TestPipelineAlertHistoryPrunesOnlyTerminalEvents(t *testing.T) {
	_, store, p, w := alertFixture(t)
	if _, err := store.pool.Exec(t.Context(), `INSERT INTO data_pipeline_alert_deliveries(id,pipeline_id,account_id,config_revision,incident_id,kind,code,occurred_at,status)
SELECT gen_random_uuid(),$1,$2,1,gen_random_uuid(),'incident','sync_blocked',clock_timestamp(),'delivered' FROM generate_series(1,$3)`, p.ID, p.AccountID, api.DataPipelineAlertHistoryMax+5); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	var terminal, pending int
	if err := store.pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE status<>'pending'),count(*) FILTER(WHERE status='pending') FROM data_pipeline_alert_deliveries WHERE pipeline_id=$1`, p.ID).Scan(&terminal, &pending); err != nil {
		t.Fatal(err)
	}
	if terminal != api.DataPipelineAlertHistoryMax || pending != 1 || len(alertHistory(t, t.Context(), store, p)) != api.DataPipelineAlertHistoryMax {
		t.Fatal("unbounded history or pending event pruned", terminal, pending)
	}
}
