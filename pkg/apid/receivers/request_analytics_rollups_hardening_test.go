package receivers

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRequestAnalyticsRollupsStoreLongUnicodeReferrers(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	// 253 runes is what the gateway keeps of a referrer host; at 3 bytes each
	// a cut at byte 512 split a rune and Postgres refused the whole flush.
	host := strings.Repeat("日", 253)
	rollups.Add(RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: uuid.New(), AppID: uuid.New(), DeploymentID: uuid.New(),
		ReceivedAt: time.Now(), Route: "GET /" + strings.Repeat("é", 300), Method: "GÉTÉTÉTÉTÉTÉTÉ", Status: 200, Count: 1, ReferrerHost: host})
	if err := rollups.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := rollupStore.requests(RollupKindReferrerHost, ""); got != 1 || len(rollupStore.gaps) != 0 {
		t.Fatalf("referrer rollup = %d requests, gaps %v; want the row kept", got, rollupStore.gaps)
	}
	for _, r := range rollupStore.rows {
		if !utf8.ValidString(r.Dimension) || !utf8.ValidString(r.Method) {
			t.Fatalf("row %q/%q is not valid UTF-8", r.Dimension, r.Method)
		}
	}
}

func TestRequestAnalyticsRollupsDropRefusedRowsAndRecordTheirMinutes(t *testing.T) {
	bad, good := uuid.New(), uuid.New()
	rollupStore := &fakeRollupStore{refuse: func(r state.RequestAnalyticsRollup) bool {
		return r.AppID == bad && r.DimensionKind == RollupKindCountry
	}}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	now := time.Now().UTC()
	add := func(app uuid.UUID) {
		rollups.Add(RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: uuid.New(), AppID: app, DeploymentID: uuid.New(),
			ReceivedAt: now, Route: "GET /", Method: "GET", Status: 200, LatencyMS: 5, Count: 2, Country: "DE"})
	}
	add(bad)
	add(good)
	if err := rollups.Flush(t.Context()); err != nil {
		t.Fatalf("flush with one refused row failed the flush: %v", err)
	}
	if rollupStore.requests(RollupKindRoute, "") != 4 || rollupStore.requests(RollupKindCountry, "") != 2 {
		t.Fatalf("stored route %d, country %d requests; want both apps' routes and only the good app's country",
			rollupStore.requests(RollupKindRoute, ""), rollupStore.requests(RollupKindCountry, ""))
	}
	if len(rollupStore.gaps) != 1 || rollupStore.gaps[0].AppID != bad || !rollupStore.gaps[0].Minute.Equal(now.Truncate(time.Minute)) {
		t.Fatalf("gaps = %+v, want the refused app's minute", rollupStore.gaps)
	}
	// The refused row is gone, not retried forever.
	rollupStore.rows = nil
	add(good)
	if err := rollups.Flush(t.Context()); err != nil || rollupStore.requests(RollupKindRoute, "") != 2 {
		t.Fatalf("next flush err %v, %d route requests; want only the new row", err, rollupStore.requests(RollupKindRoute, ""))
	}
}

func TestRequestAnalyticsRollupsKeepTheBusiestValuesNotTheFirst(t *testing.T) {
	rollupStore := &fakeRollupStore{}
	rollups := NewRequestAnalyticsRollups(rollupStore, nil, nil)
	app, account, deployment := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	for i := range api.RequestAnalyticsRollupDimensionsMax + 10 {
		rollups.Add(RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: account, AppID: app, DeploymentID: deployment,
			ReceivedAt: now, Route: "GET /", Method: "GET", Status: 200, Count: 1, ConsumerID: fmt.Sprintf("c%03d", i)})
	}
	busy := "late-but-busy"
	rollups.Add(RequestAnalyticsRollupRow{EventID: uuid.New(), AccountID: account, AppID: app, DeploymentID: deployment,
		ReceivedAt: now, Route: "GET /", Method: "GET", Status: 200, Count: 500, ConsumerID: busy})
	if err := rollups.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := rollupStore.requests(RollupKindConsumerID, busy); got != 500 {
		t.Fatalf("busiest consumer rollup = %d, want its 500 requests kept out of __other__", got)
	}
	values := map[string]bool{}
	for _, r := range rollupStore.rows {
		if r.DimensionKind == RollupKindConsumerID {
			values[r.Dimension] = true
		}
	}
	if len(values) != api.RequestAnalyticsRollupDimensionsMax+1 || !values[rollupOtherDimension] {
		t.Fatalf("consumer values = %d, want %d plus __other__", len(values), api.RequestAnalyticsRollupDimensionsMax)
	}
}
