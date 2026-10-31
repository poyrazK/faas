// adr: 20261023171905 — rollout step 3: the account usage summary shows the
// quiet-paused time meterd did not bill, beside the billed GB-hours.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUsageSummaryShowsUnbilledQuietPause(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "paused", RAMMB: 1024, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	// Half a GB-hour billed and one GB-hour paused for free, in September.
	minute := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	instance := uuid.NewString()
	if err := store.AppendUsage(ctx, acct.ID, app.ID, instance, minute, 1024*1800, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordQuietPausedUsage(ctx, acct.ID, app.ID, instance, minute, 1024*3600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/v1/usage/summary?month=2026-09", "/v1/account/usage?month=2026-09"} {
		rec := e.Do(t, http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
		}
		summary := decodeComputeUsage(t, path, rec.Body.Bytes())
		if summary.UsedGBHours != 0.5 || summary.QuietPausedGBHours != 1 {
			t.Fatalf("GET %s: used %v GB-h, quiet paused %v GB-h; want 0.5 billed and 1 paused beside it",
				path, summary.UsedGBHours, summary.QuietPausedGBHours)
		}
	}

	// A month without unbilled pause leaves the field out.
	rec := e.Do(t, http.MethodGet, "/v1/usage/summary?month=2026-08", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("August summary = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "quiet_paused_gb_hours") {
		t.Fatalf("August summary carries quiet_paused_gb_hours: %s", rec.Body)
	}
}

func decodeComputeUsage(t *testing.T, path string, body []byte) api.UsageSummaryResponse {
	t.Helper()
	if strings.HasPrefix(path, "/v1/account/usage") {
		var out api.AccountUsageResponse
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out.Compute
	}
	var out api.UsageSummaryResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}
