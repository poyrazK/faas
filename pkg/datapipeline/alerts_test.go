package datapipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/alertdelivery"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type alertRoundTripper func(*http.Request) (*http.Response, error)

func (f alertRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func alertFixture(t *testing.T) (*Service, *PostgresStore, Pipeline, *AlertWorker) {
	t.Helper()
	s, store, p, _ := schemaPipelineFixture(t)
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	s.Recipient = key.Recipient()
	if err = s.ConfigureHealthAlerts(t.Context(), p.AccountID, p.ID, 0, true, "https://alerts.example/hook?token=url-sentinel", strings.Repeat("signing-secret-sentinel", 2)); err != nil {
		t.Fatal(err)
	}
	w := &AlertWorker{Store: store, Accounts: []string{p.AccountID}, Identities: func() ([]*age.X25519Identity, error) { return []*age.X25519Identity{key}, nil }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w.Sender = alertdelivery.New(nil, w.Log)
	return s, store, p, w
}

func alertDue(t *testing.T, ctx context.Context, w *AlertWorker, p Pipeline, mature bool) {
	t.Helper()
	if _, err := w.Store.pool.Exec(ctx, `UPDATE data_pipeline_alerts SET check_at=clock_timestamp(),candidate_since=CASE WHEN $2 THEN clock_timestamp()-interval '3 minutes' ELSE candidate_since END WHERE pipeline_id=$1`, p.ID, mature); err != nil {
		t.Fatal(err)
	}
	if more, err := w.CheckTick(ctx); err != nil || !more {
		t.Fatal("monitor did not observe", more, err)
	}
}

func alertLive(t *testing.T, store *PostgresStore, p Pipeline) {
	t.Helper()
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET state='live',reason='',observed_at=clock_timestamp(),observed_revision=revision,retained_wal_bytes=0 WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
}

func alertHistory(t *testing.T, ctx context.Context, store *PostgresStore, p Pipeline) []AlertDelivery {
	t.Helper()
	a, err := store.Alerts(ctx, p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a.History
}

func TestPipelineAlertDestinationValidationAndRevision(t *testing.T) {
	s, store, p, w := alertFixture(t)
	for _, endpoint := range []string{"http://alerts.example/hook", "https://user:pass@alerts.example", "https://127.0.0.1/hook", "https://169.254.169.254/", "https://[::1]/", "https://alerts.example:8443/", "https://alerts.example/#secret"} {
		if err := s.ConfigureHealthAlerts(t.Context(), p.AccountID, p.ID, 1, true, endpoint, strings.Repeat("x", 32)); !errors.Is(err, ErrInvalid) {
			t.Fatal("unsafe endpoint", endpoint, err)
		}
	}
	if err := s.ConfigureHealthAlerts(t.Context(), uuid.NewString(), p.ID, 1, true, "https://alerts.example/", strings.Repeat("x", 32)); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign config accepted", err)
	}
	if _, err := store.Alerts(t.Context(), uuid.NewString(), p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign history returned", err)
	}
	if err := s.ConfigureHealthAlerts(t.Context(), p.AccountID, p.ID, 0, true, "https://alerts.example/", strings.Repeat("x", 32)); !errors.Is(err, ErrConflict) {
		t.Fatal("stale config accepted", err)
	}
	a, err := store.q.PipelineAlertGet(t.Context(), store.pool, sqlc.PipelineAlertGetParams{AccountID: p.AccountID, PipelineID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"url-sentinel", "signing-secret-sentinel", "alerts.example"} {
		if bytes.Contains(a.Ciphertext, []byte(secret)) {
			t.Fatal("plaintext destination persisted")
		}
	}
	other := a
	other.AccountID = pgUUID(t, uuid.NewString())
	if _, err = w.destination(other); err == nil {
		t.Fatal("ciphertext copied across accounts")
	}
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	s.Allowed = func(context.Context, string) bool { return false }
	if err = s.ConfigureHealthAlerts(t.Context(), p.AccountID, p.ID, 1, false, "", ""); err != nil {
		t.Fatal("opt-out must work when pilot admission closes", err)
	}
	h := alertHistory(t, t.Context(), store, p)
	if len(h) != 1 || h[0].Status != "cancelled" {
		t.Fatal("opt-out left pending notification", h)
	}
	a, _ = store.q.PipelineAlertGet(t.Context(), store.pool, sqlc.PipelineAlertGetParams{AccountID: p.AccountID, PipelineID: p.ID})
	if a.Enabled || len(a.Ciphertext) != 0 {
		t.Fatal("disabled destination retained secret")
	}
}

func TestPipelineAlertConditionsAvoidIdleAndIntentFalseAlarms(t *testing.T) {
	now := time.Now().UTC()
	zero, pressure, lag := int64(0), api.DataPipelineRetentionWarningBytes, api.DataPipelineLiveLagMaxBytes
	p := Pipeline{DataPipeline: api.DataPipeline{DesiredState: "running", State: "live", Revision: 2, ObservedRevision: 2, ObservedAt: now.Format(time.RFC3339Nano), AppliedThroughAt: now.Add(-time.Hour).Format(time.RFC3339Nano), RetainedWALBytes: &zero}}
	if code, healthy := alertCondition(p, now); code != "" || !healthy {
		t.Fatal("idle source falsely stale", code, healthy)
	}
	p.RetainedWALBytes = &lag
	if code, _ := alertCondition(p, now); code != "apply_unconfirmed" {
		t.Fatal("unconfirmed apply and retained WAL missed", code)
	}
	p.RetainedWALBytes = &pressure
	p.DesiredState = "paused"
	if code, _ := alertCondition(p, now); code != "retention_pressure" {
		t.Fatal("pause hid WAL pressure", code)
	}
	p.RetainedWALBytes = &zero
	if code, healthy := alertCondition(p, now); code != "" || healthy {
		t.Fatal("pause claimed recovery", code, healthy)
	}
	p.DesiredState = "running"
	p.ObservedAt = now.Add(-api.DataPipelineObservationMaxAge).Format(time.RFC3339Nano)
	if code, _ := alertCondition(p, now); code != "monitoring_stale" {
		t.Fatal("stopped worker not detected", code)
	}
	p.ObservedAt = now.Format(time.RFC3339Nano)
	p.ObservedRevision = 1
	if code, healthy := alertCondition(p, now); code != "" || healthy {
		t.Fatal("unacknowledged control claimed recovery")
	}
}

func TestPipelineAlertDestinationSurvivesEquivalentUUIDSpelling(t *testing.T) {
	s, store, p, w := alertFixture(t)
	secret := strings.Repeat("canonical-secret", 2)
	if err := s.ConfigureHealthAlerts(t.Context(), p.AccountID, strings.ToUpper(p.ID), 1, true, "https://alerts.example/canonical", secret); err != nil {
		t.Fatal(err)
	}
	a, err := store.q.PipelineAlertGet(t.Context(), store.pool, sqlc.PipelineAlertGetParams{AccountID: p.AccountID, PipelineID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	d, err := w.destination(a)
	if err != nil || d.Secret != secret {
		t.Fatal("PostgreSQL UUID canonicalization broke the encryption binding", err)
	}
}

func TestPipelineAlertWorkerStoppedAndRecoveryPersistAcrossRestart(t *testing.T) {
	_, store, p, w := alertFixture(t)
	alertLive(t, store, p)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET observed_at=clock_timestamp()-interval '2 minutes' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	// No replication-worker tick runs. The independent worker alone detects it.
	alertDue(t, t.Context(), w, p, false)
	if len(alertHistory(t, t.Context(), store, p)) != 0 {
		t.Fatal("transient outage notified before debounce")
	}
	w = &AlertWorker{Store: NewPostgresStore(store.pool), Accounts: w.Accounts}
	alertDue(t, t.Context(), w, p, true)
	for range 3 {
		alertDue(t, t.Context(), w, p, true)
	}
	h := alertHistory(t, t.Context(), store, p)
	if len(h) != 1 || h[0].Kind != "incident" || h[0].Code != "monitoring_stale" {
		t.Fatal("duplicate or missing stopped-worker incident", h)
	}
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET desired_state='paused',state='paused',observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, true)
	if len(alertHistory(t, t.Context(), store, p)) != 1 {
		t.Fatal("pause sent a false recovery")
	}
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET desired_state='running' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	alertLive(t, store, p)
	alertDue(t, t.Context(), w, p, false)
	if len(alertHistory(t, t.Context(), store, p)) != 1 {
		t.Fatal("recovery skipped debounce")
	}
	alertDue(t, t.Context(), w, p, true)
	alertDue(t, t.Context(), w, p, true)
	h = alertHistory(t, t.Context(), store, p)
	if len(h) != 2 || h[0].Kind != "recovery" || h[0].IncidentID != h[1].IncidentID {
		t.Fatal("recovery lost incident identity", h)
	}
}

func TestPipelineAlertDeliveryRetriesSignatureOrderingAndMinimization(t *testing.T) {
	_, store, p, w := alertFixture(t)
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	alertLive(t, store, p)
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	var attempts atomic.Int32
	var mu sync.Mutex
	var ids, bodies []string
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		ts, err := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if err != nil {
			t.Error(err)
		}
		id := r.Header.Get("X-Faas-Delivery-Id")
		if err := webhookout.NewSigner([]byte(strings.Repeat("signing-secret-sentinel", 2))).Verify(ts, id, body, strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256=")); err != nil {
			t.Error(err)
		}
		attempt := attempts.Add(1)
		want := "1"
		if attempt == 2 {
			want = "2"
		}
		if r.Header.Get("X-Faas-Webhook-Attempt") != want {
			t.Error("attempt header", r.Header)
		}
		mu.Lock()
		ids = append(ids, id)
		bodies = append(bodies, string(body))
		mu.Unlock()
		if attempt == 1 {
			out.Header().Set("Retry-After", "60")
			out.WriteHeader(503)
			_, _ = out.Write([]byte("private-response-sentinel"))
			return
		}
		out.WriteHeader(204)
	}))
	defer receiver.Close()
	local := receiver.Client()
	w.Sender = alertdelivery.New(&http.Client{Transport: alertRoundTripper(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Host = strings.TrimPrefix(receiver.URL, "https://")
		return local.Transport.RoundTrip(clone)
	})}, w.Log)
	if more, err := w.DeliveryTick(t.Context()); err != nil || !more {
		t.Fatal(more, err)
	}
	h := alertHistory(t, t.Context(), store, p)
	if len(h) != 2 || h[1].Status != "pending" || h[1].Attempts != 1 {
		t.Fatal("retry was not durable", h)
	}
	if more, err := w.DeliveryTick(t.Context()); err != nil || more {
		t.Fatal("recovery overtook failed incident", more, err)
	}
	// Restart with a new store, retaining the persisted backoff and event ID.
	w.Store = NewPostgresStore(store.pool)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_alert_deliveries SET next_attempt_at=clock_timestamp() WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if more, err := w.DeliveryTick(t.Context()); err != nil || !more {
			t.Fatal(more, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ids) != 3 || ids[0] != ids[1] || ids[1] == ids[2] || bodies[0] != bodies[1] {
		t.Fatal("retry mutated identity/body", ids, bodies)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &event); err != nil {
		t.Fatal(err)
	}
	payload := event["payload"].(map[string]any)
	if len(payload) != 5 || payload["pipeline_id"] != p.ID || payload["code"] != "sync_blocked" {
		t.Fatal("unexpected payload", payload)
	}
	all := strings.Join(bodies, "")
	history, _ := json.Marshal(alertHistory(t, t.Context(), store, p))
	all += string(history)
	for _, secret := range []string{"url-sentinel", "signing-secret-sentinel", "private-response-sentinel", "private-sentinel", "orders", "alice", "schema_change_pending", "postgres://"} {
		if strings.Contains(all, secret) {
			t.Fatal("sensitive data leaked", secret)
		}
	}
	h = alertHistory(t, t.Context(), store, p)
	if h[0].Status != "delivered" || h[1].Status != "delivered" || h[1].Attempts != 2 {
		t.Fatal("delivery not acknowledged", h)
	}
}

func TestPipelineAlertConcurrentMonitorExpiredLeaseAndCancellation(t *testing.T) {
	s, store, p, w := alertFixture(t)
	alertDue(t, t.Context(), w, p, false)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_alerts SET check_at=clock_timestamp(),candidate_since=clock_timestamp()-interval '3 minutes' WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if _, err := w.CheckTick(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if len(alertHistory(t, t.Context(), store, p)) != 1 {
		t.Fatal("concurrent monitors duplicated incident")
	}
	accounts, _ := w.accounts()
	now := time.Now()
	d, err := store.q.PipelineAlertClaimDelivery(t.Context(), store.pool, sqlc.PipelineAlertClaimDeliveryParams{Accounts: accounts, NowAt: alertTime(now), LeaseToken: "crashed-worker", LeaseUntil: alertTime(now.Add(-time.Second))})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	w.Sender = alertdelivery.New(&http.Client{Transport: alertRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}, w.Log)
	if more, err := w.DeliveryTick(t.Context()); !more || err != nil {
		t.Fatal("expired claim did not recover", more, err)
	}
	n, err := store.q.PipelineAlertFinishDelivery(t.Context(), store.pool, sqlc.PipelineAlertFinishDeliveryParams{ID: uid(d.ID), LeaseToken: "crashed-worker", NowAt: alertTime(now), Status: "dead", Result: "attempts_exhausted", NextAttemptAt: alertTime(now)})
	if err != nil || n != 0 {
		t.Fatal("stale dispatcher overwrote acknowledgment", n, err)
	}
	if err = s.ConfigureHealthAlerts(t.Context(), p.AccountID, p.ID, 1, true, "https://replacement.example/", strings.Repeat("r", 32)); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	if err = s.ConfigureHealthAlerts(t.Context(), p.AccountID, p.ID, 2, false, "", ""); err != nil {
		t.Fatal(err)
	}
	if more, err := w.DeliveryTick(t.Context()); more || err != nil || calls.Load() != 1 {
		t.Fatal("disabled destination received queued event", more, err)
	}
}

func pgUUID(t *testing.T, id string) (result pgtype.UUID) {
	t.Helper()
	if err := result.Scan(id); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPipelineAlertLostAcknowledgmentKeepsEventIdentity(t *testing.T) {
	_, store, p, w := alertFixture(t)
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	var ids []string
	w.Sender = alertdelivery.New(&http.Client{Transport: alertRoundTripper(func(r *http.Request) (*http.Response, error) {
		ids = append(ids, r.Header.Get("X-Faas-Delivery-Id"))
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}, w.Log)
	accounts, _ := w.accounts()
	now := time.Now()
	d, err := store.q.PipelineAlertClaimDelivery(t.Context(), store.pool, sqlc.PipelineAlertClaimDeliveryParams{Accounts: accounts, NowAt: alertTime(now), LeaseToken: "lost-ack", LeaseUntil: alertTime(now.Add(api.DataPipelineAlertLease))})
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.q.PipelineAlertGet(t.Context(), store.pool, sqlc.PipelineAlertGetParams{AccountID: p.AccountID, PipelineID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	target, err := w.destination(a)
	if err != nil {
		t.Fatal(err)
	}
	if result := w.send(t.Context(), target, d); !result.Accepted {
		t.Fatal("attempt not accepted", result)
	}
	// The receiver accepted the POST; the worker died before its catalog ack.
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_alert_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, uid(d.ID)); err != nil {
		t.Fatal(err)
	}
	w.Store = NewPostgresStore(store.pool)
	if more, err := w.DeliveryTick(t.Context()); err != nil || !more {
		t.Fatal(more, err)
	}
	if len(ids) != 2 || ids[0] != ids[1] || len(alertHistory(t, t.Context(), store, p)) != 1 {
		t.Fatal("lost ack duplicated logical event", ids)
	}
}

func TestPipelineAlertDeliveryTerminalAndRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		code           int
		status, result string
	}{
		{"redirect", 302, "dead", "redirect_rejected"},
		{"client_error", 400, "dead", "receiver_rejected"},
		{"rate_limit", 429, "pending", "network_failure"},
		{"nonstandard_status", 999, "pending", "network_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store, p, w := alertFixture(t)
			alertDue(t, t.Context(), w, p, false)
			alertDue(t, t.Context(), w, p, true)
			var calls int
			w.Sender = alertdelivery.New(&http.Client{Transport: alertRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.code, Header: http.Header{"Location": []string{"https://another.example/"}, "Retry-After": []string{"600"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}, w.Log)
			if _, err := w.DeliveryTick(t.Context()); err != nil {
				t.Fatal(err)
			}
			h := alertHistory(t, t.Context(), store, p)
			if calls != 1 || h[0].Status != tc.status || h[0].Result != tc.result {
				t.Fatal("unsafe terminal/retry handling", calls, h)
			}
			if tc.code == 429 {
				var next time.Time
				if err := store.pool.QueryRow(t.Context(), `SELECT next_attempt_at FROM data_pipeline_alert_deliveries WHERE id=$1`, h[0].ID).Scan(&next); err != nil || time.Until(next) < 9*time.Minute {
					t.Fatal("receiver backpressure ignored", err)
				}
				for range api.DataPipelineAlertAttemptsMax - 1 {
					if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_alert_deliveries SET next_attempt_at=clock_timestamp() WHERE pipeline_id=$1`, p.ID); err != nil {
						t.Fatal(err)
					}
					if _, err := w.DeliveryTick(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				h = alertHistory(t, t.Context(), store, p)
				if h[0].Status != "dead" || h[0].Attempts != api.DataPipelineAlertAttemptsMax || calls != api.DataPipelineAlertAttemptsMax {
					t.Fatal("unbounded retry", calls, h)
				}
			}
		})
	}
}

func TestPipelineAlertMonitorSurvivesMissingKeysAndDeleteCancels(t *testing.T) {
	_, store, p, w := alertFixture(t)
	w.Identities = func() ([]*age.X25519Identity, error) { return nil, ErrUnavailable }
	alertDue(t, t.Context(), w, p, false)
	alertDue(t, t.Context(), w, p, true)
	if more, err := w.DeliveryTick(t.Context()); !more || err != nil {
		t.Fatal(more, err)
	}
	h := alertHistory(t, t.Context(), store, p)
	if len(h) != 1 || h[0].Status != "pending" || h[0].Result != "credential_unavailable" {
		t.Fatal("key failure prevented durable incident", h)
	}
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipelines SET desired_state='deleted' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	alertDue(t, t.Context(), w, p, true)
	a, err := store.Alerts(t.Context(), p.AccountID, p.ID)
	if err != nil || a.Enabled || a.History[0].Status != "cancelled" || a.History[0].Result != "pipeline_deleted" {
		t.Fatal("deleted pipeline still delivers", a, err)
	}
}
