// SQL-shape regression for the TZ normalisation in UsageByHour /
// UsageByAccount (PR #428 review warning #4). The previous SQL
// used `date_trunc('hour', minute)` directly on the column —
// Postgres interprets that in the session's TimeZone, so a
// session whose TZ != UTC buckets minutes at the wrong wall
// clock (memory note: pkg-state-usage-monthly-tz-compare). The
// fix appends `AT TIME ZONE 'UTC'` so the truncation happens
// against a UTC-anchored timestamp regardless of session TZ.
//
// We pin the substring in the pgstore.go source directly via
// //go:embed — same pattern as pgstore_latest_snapshot_bytes_sql_test.go.
// A future refactor that drops the AT TIME ZONE clause will fail
// here at unit-test time, well before a misconfigured session
// silently buckets rows to the wrong hour.

package state_test

import (
	_ "embed"
	"strings"
	"testing"
)

//go:embed pgstore.go
var pgStoreUsageByHourSource string

func TestUsageByHour_UsesUTCTruncation(t *testing.T) {
	body := extractFn(pgStoreUsageByHourSource, "func (s *PgStore) UsageByHour(")
	if body == "" {
		t.Fatal("could not locate UsageByHour in pgstore.go")
	}
	if !strings.Contains(body, "date_trunc('hour', minute AT TIME ZONE 'UTC')") {
		t.Errorf("UsageByHour SQL must use `date_trunc('hour', minute AT TIME ZONE 'UTC')` — without the AT TIME ZONE, a non-UTC session TZ buckets minutes at the wrong wall-clock hour (PR #428 review warning #4). Got:\n%s", body)
	}
	// Belt-and-braces: the buggy form must NOT appear.
	if strings.Contains(body, "date_trunc('hour', minute)") && !strings.Contains(body, "date_trunc('hour', minute AT TIME ZONE 'UTC')") {
		t.Errorf("UsageByHour still has the TZ-naive form `date_trunc('hour', minute)` — session TZ drift will split hour buckets incorrectly")
	}
}

func TestUsageByAccount_UsesUTCTruncation(t *testing.T) {
	body := extractFn(pgStoreUsageByHourSource, "func (s *PgStore) UsageByAccount(")
	if body == "" {
		t.Fatal("could not locate UsageByAccount in pgstore.go")
	}
	// Both branches of UsageByAccount (since.IsZero() and since
	// given) must use UTC truncation. We grep both `date_trunc`
	// calls inside the function.
	truncs := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "date_trunc('month'") {
			truncs++
			if !strings.Contains(line, "minute AT TIME ZONE 'UTC'") {
				t.Errorf("UsageByAccount line missing UTC truncation: %q", strings.TrimSpace(line))
			}
		}
	}
	if truncs == 0 {
		t.Errorf("UsageByAccount has no date_trunc — function body may have changed shape:\n%s", body)
	}
	if truncs != 2 {
		t.Errorf("UsageByAccount date_trunc count = %d, want 2 (since.IsZero() + since-given branches)", truncs)
	}
}
