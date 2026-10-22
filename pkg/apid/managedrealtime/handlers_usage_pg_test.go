package managedrealtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// usageTestHost serves RealtimeUsage from a PgStore; no app lookup is needed.
type usageTestHost struct {
	ticketTestHost
	pg *state.PgStore
}

func (h *usageTestHost) Store() state.Store { return h.pg }

func TestRealtimeUsageHandler(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "usage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "usage-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.com/cb", Enabled: true,
		ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
		CallbackAuthTokenSealed: []byte("sealed"), AuthTokenSealed: []byte("sealed"), AuthMode: api.RealtimeAuthModeStaticBearer}, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	peaks := make([]int32, state.RealtimeUsageMinutes)
	peaks[0] = 9
	if _, err := store.RecordRealtimeUsage(ctx, state.RealtimeUsageReport{NodeName: "node-a", Incarnation: uuid.NewString(), Hour: hour,
		Endpoints: []state.RealtimeEndpointUsage{{EndpointID: ep.ID, MinutePeakConnections: peaks, ConnectionSeconds: 7200}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncRealtimeNodeConnections(ctx, "node-a", []state.RealtimeAccountConnections{{AccountID: acct.ID, Connections: 4}}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Host: &usageTestHost{pg: store}}
	get := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		svc.RealtimeUsage(rec, httptest.NewRequest(http.MethodGet, "/v1/usage/realtime"+query, nil), acct)
		return rec
	}
	if rec := get(""); rec.Code != http.StatusNotFound {
		t.Fatalf("usage without FAAS_REALTIME_USAGE_ENABLED = %d %s, want 404", rec.Code, rec.Body)
	}
	svc.UsageEnabled = true
	rec := get("")
	var out api.RealtimeUsageResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("usage = %d %s", rec.Code, rec.Body)
	}
	if out.ConnectionLimit != 1_000 || out.OpenConnections != 4 || out.Plan != api.PlanPro || out.To.Sub(out.From) != 24*time.Hour {
		t.Fatalf("usage summary = %+v", out)
	}
	if len(out.Hours) != 1 || out.Hours[0].PeakConnections != 9 || out.Hours[0].ConnectionSeconds != 7200 ||
		len(out.Endpoints) != 1 || out.Endpoints[0].EndpointID != ep.ID || out.Endpoints[0].AppID != app.ID {
		t.Fatalf("usage hours = %+v / %+v", out.Hours, out.Endpoints)
	}
	if rec := get("?hours=1"); rec.Code != http.StatusOK {
		t.Fatalf("hours=1 = %d", rec.Code)
	}
	for _, bad := range []string{"?hours=0", "?hours=745", "?hours=x", "?hours=1&hours=2"} {
		if rec := get(bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", bad, rec.Code)
		}
	}
}
