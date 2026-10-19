package datapipeline

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/alertdelivery"
)

// This runs inside the isolated TLS/native replication acceptance fixture.
// PostgreSQL itself rejects a generated nonexistent login; no shared fixture
// role is changed. The resulting engine observation goes through normal Finish.
func assertNativeHealthAlerts(t *testing.T, ctx context.Context, s *Service, store *PostgresStore, engine *Engine, key *age.X25519Identity, raw string, current api.DataPipeline, await func(string) api.DataPipeline) api.DataPipeline {
	t.Helper()
	var err error
	var account string
	if err = store.pool.QueryRow(ctx, `SELECT account_id::text FROM data_pipelines WHERE id=$1`, current.ID).Scan(&account); err != nil {
		t.Fatal(err)
	}
	p, err := store.Get(ctx, account, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("native-alert-secret", 2)
	if err = s.ConfigureHealthAlerts(ctx, account, p.ID, 0, true, "https://alerts.example/native", secret); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "orders") || strings.Contains(string(body), secret) || strings.Contains(string(body), "postgres://") {
			t.Error("customer data entered alert")
		}
		if posts.Add(1) == 1 {
			out.WriteHeader(503)
			return
		}
		out.WriteHeader(204)
	}))
	defer receiver.Close()
	local := receiver.Client()
	client := &http.Client{Transport: alertRoundTripper(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Host = strings.TrimPrefix(receiver.URL, "https://")
		return local.Transport.RoundTrip(clone)
	})}
	monitor := func() *AlertWorker {
		return &AlertWorker{Store: NewPostgresStore(store.pool), Accounts: []string{account}, Sender: alertdelivery.New(client, slog.New(slog.NewTextHandler(io.Discard, nil))), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Identities: func() ([]*age.X25519Identity, error) { return []*age.X25519Identity{key}, nil }}
	}
	w := monitor()
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET retry_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	bad, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	bad.User = url.UserPassword("missing_"+strings.ReplaceAll(uuid.NewString(), "-", ""), "unavailable")
	ob := engine.Reconcile(ctx, claimed, bad.String(), func(context.Context) error { return nil })
	if ob.State != "blocked" || ob.Reason != "source_authentication_failed" {
		t.Fatal("native source auth fault was not classified", ob.State, ob.Reason)
	}
	if err = store.Finish(ctx, claimed, ob); err != nil {
		t.Fatal(err)
	}
	alertDue(t, ctx, w, p, false)
	alertDue(t, ctx, w, p, true)
	if more, err := w.DeliveryTick(ctx); !more || err != nil {
		t.Fatal(more, err)
	}
	w = monitor() // restart during the receiver's durable retry window
	alertDue(t, ctx, w, p, true)
	if h := alertHistory(t, ctx, store, p); len(h) != 1 || h[0].Code != "sync_blocked" || h[0].Attempts != 1 {
		t.Fatal("native auth incident duplicated or lost", h)
	}
	current = await("live")
	alertDue(t, ctx, w, p, false)
	alertDue(t, ctx, w, p, true)
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_alert_deliveries SET next_attempt_at=clock_timestamp() WHERE pipeline_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if more, err := w.DeliveryTick(ctx); !more || err != nil {
			t.Fatal(more, err)
		}
	}
	h := alertHistory(t, ctx, store, p)
	if len(h) != 2 || h[0].Kind != "recovery" || h[0].Status != "delivered" || h[1].Status != "delivered" || h[0].IncidentID != h[1].IncidentID || posts.Load() != 3 {
		t.Fatal("native incident/recovery delivery", h, posts.Load())
	}
	if err = s.ConfigureHealthAlerts(ctx, account, p.ID, 1, false, "", ""); err != nil {
		t.Fatal(err)
	}
	return current
}
