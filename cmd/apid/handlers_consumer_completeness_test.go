package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 941
func TestCompletenessWindow(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 5, 0, 0, time.UTC)
	cases := []struct {
		name         string
		since, until time.Time
		from, end    time.Time
		ok           bool
	}{
		{"rounds inward to whole hours", now.Add(-5*time.Hour - 30*time.Minute), now.Add(-2*time.Hour - 30*time.Minute),
			time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC), true},
		{"unsettled hour excluded", now.Add(-3 * time.Hour), now,
			time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC), true},
		{"retention clamps since", now.AddDate(0, 0, -30), now.AddDate(0, 0, -13),
			time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC), time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), true},
		{"fully expired", now.AddDate(0, 0, -60), now.AddDate(0, 0, -30), time.Time{}, time.Time{}, false},
	}
	for _, tc := range cases {
		from, end, ok := completenessWindow(tc.since, tc.until, now)
		if ok != tc.ok || (ok && (!from.Equal(tc.from) || !end.Equal(tc.end))) {
			t.Errorf("%s: got %s..%s ok=%v, want %s..%s ok=%v", tc.name, from, end, ok, tc.from, tc.end, tc.ok)
		}
	}
}

// adr: 941
func TestAPIConsumerUsageCompletenessEndpoint(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-completeness-app")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-completeness-app/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "checked-customer", Name: "Checked Customer",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatalf("decode consumer: %v", err)
	}
	hour := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Hour)
	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID,
		ConsumerKey: consumer.ID, WindowStart: hour.Add(5 * time.Minute),
		RequestCount: 6, ErrorCount: 1, BillableUnits: 6,
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	e.store.SeedAPIConsumerTelemetryHour(e.acct.ID, consumer.AppID, consumer.ID, hour, 7)

	q := url.Values{}
	q.Set("since", hour.Add(-time.Hour).Format(time.RFC3339))
	q.Set("until", time.Now().UTC().Format(time.RFC3339))
	path := "/v1/apps/consumer-completeness-app/consumers/" + consumer.ID + "/usage-completeness?" + q.Encode()
	rec := e.do(t, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("completeness: %d %s", rec.Code, rec.Body)
	}
	var got api.APIConsumerUsageCompletenessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode completeness: %v", err)
	}
	if got.Status != billing.CompletenessGapsDetected || got.LedgerRequests != 5 || got.TelemetryRequests != 7 ||
		got.MissingRequests != 2 || got.ConfirmedRequests != 5 || got.HoursChecked != 1 {
		t.Fatalf("completeness = %+v", got)
	}

	if bad := e.do(t, http.MethodGet, "/v1/apps/consumer-completeness-app/consumers/"+consumer.ID+"/usage-completeness?since=x", nil, nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d %s", bad.Code, bad.Body)
	}
	if missing := e.do(t, http.MethodGet, "/v1/apps/consumer-completeness-app/consumers/nope/usage-completeness?"+q.Encode(), nil, nil); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown consumer: %d %s", missing.Code, missing.Body)
	}
}
