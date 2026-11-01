package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestAppErrorsRecorderFoldsAnErrorFlood pins bug hunt 2026-10-28 H41: every
// 4xx/5xx became two control-plane writes, scanner 404s included. Repeats of
// an app's error within a flush now fold into a few sampled records whose
// counts add up to the requests, and an app's request rows are capped per
// minute.
func TestAppErrorsRecorderFoldsAnErrorFlood(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	r := newAppErrorsRecorder(appErrorsRecorderConfig{Enabled: true, Now: func() time.Time { return now }}, nil, nil, slog.Default())
	failing := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		api.PlatformIdentity{AppID: "app-1", TenantID: "acct-1"}.ApplyGuestHeaders(req.Header)
		http.NotFound(w, req)
	}))
	hit := func(n int) {
		for i := 0; i < n; i++ {
			failing.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/wp-login.php", nil))
		}
	}
	tally := func(rows []appErrorRow) (count int64, requestRows int) {
		for _, row := range rows {
			count += row.Count
			if !row.RequestRowOmitted {
				requestRows++
			}
		}
		return count, requestRows
	}

	hit(2000)
	rows := r.drainBatch(0)
	if count, requestRows := tally(rows); len(rows) != api.AppErrorsRequestSamplesPerFlush || count != 2000 || requestRows != len(rows) {
		t.Fatalf("2000 404s queued %d records counting %d with %d request rows; want %d records counting 2000", len(rows), count, requestRows, api.AppErrorsRequestSamplesPerFlush)
	}

	// Within the minute the app runs out of request rows; later records only
	// count.
	used := api.AppErrorsRequestSamplesPerFlush
	var counted int64
	for flush := 0; flush < 30; flush++ {
		hit(10)
		count, requestRows := tally(r.drainBatch(0))
		counted += count
		used += requestRows
	}
	if used != api.AppErrorsRequestRowsPerAppPerMinute || counted != 300 {
		t.Fatalf("request rows in a minute = %d, counted %d; want %d rows and 300 requests", used, counted, api.AppErrorsRequestRowsPerAppPerMinute)
	}

	now = now.Add(time.Minute)
	hit(1)
	if rows := r.drainBatch(0); len(rows) != 1 || rows[0].RequestRowOmitted {
		t.Fatalf("next minute = %+v, want a request row again", rows)
	}
}
